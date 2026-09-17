package runtime_test

// These tests run real detached servers. The test binary doubles as every
// child process it needs: TestMain dispatches on OVDB_RUNTIME_TEST_CHILD to
// run the local server, to act as a caller whose stdout is a pipe, or to
// sleep as an unrelated process. They run on Linux, macOS and Windows in the
// go-os-matrix CI job.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/strongo/cli-helpers/daemonlifecycle"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/runtime"
)

const (
	childEnv    = "OVDB_RUNTIME_TEST_CHILD"
	faultEnv    = "OVDB_RUNTIME_TEST_FAULT"
	testVersion = "9.9.9-test"
)

func TestMain(m *testing.M) {
	switch os.Getenv(childEnv) {
	case "":
		os.Exit(m.Run())
	case "serve":
		os.Exit(childServe())
	case "start":
		os.Exit(childStart())
	case "sleep":
		time.Sleep(time.Minute)
		os.Exit(0)
	default:
		os.Exit(3)
	}
}

// childServe is the detached server: the equivalent of `ovdb server run`.
func childServe() int {
	dirs, err := paths.Resolve(os.Getenv)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	port, _ := strconv.Atoi(os.Args[len(os.Args)-1])
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = localserver.Run(ctx, localserver.RunOptions{
		Dirs: dirs, Port: port, Version: testVersion, Log: os.Stdout, FailBeforeReady: os.Getenv(faultEnv) == "1",
	})
	if err != nil {
		return 1
	}
	return 0
}

// childStart is a caller whose stdout the test reads through a pipe: it
// starts the server, prints the result as JSON and exits.
func childStart() int {
	dirs, err := paths.Resolve(os.Getenv)
	if err != nil {
		return 1
	}
	port, _ := strconv.Atoi(os.Args[len(os.Args)-1])
	result, err := runtime.Start(context.Background(), startOptions(dirs, port))
	if err != nil {
		fmt.Println(err)
		return 1
	}
	_ = json.NewEncoder(os.Stdout).Encode(result.State.Record)
	return 0
}

func startOptions(dirs paths.Dirs, port int) runtime.StartOptions {
	return runtime.StartOptions{
		Dirs: dirs, Port: port,
		Command: func(port int) *exec.Cmd {
			command := exec.Command(os.Args[0], "serve", strconv.Itoa(port))
			command.Env = append(os.Environ(), childEnv+"=serve")
			return command
		},
	}
}

func testDirs(t *testing.T) paths.Dirs {
	t.Helper()
	base := t.TempDir()
	return paths.Dirs{
		Home:    filepath.Join(base, "home"),
		Runtime: filepath.Join(base, "cache", "run"),
		Data:    filepath.Join(base, "data"),
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

// stopOnCleanup stops whatever server dirs has and waits for its process,
// which Windows needs before it can delete the temporary directories.
func stopOnCleanup(t *testing.T, dirs paths.Dirs) {
	t.Cleanup(func() {
		if _, err := runtime.Stop(context.Background(), dirs.Runtime, 0); err != nil {
			t.Logf("cleanup stop: %v", err)
		}
	})
}

func start(t *testing.T, dirs paths.Dirs, port int) runtime.StartResult {
	t.Helper()
	stopOnCleanup(t, dirs)
	result, err := runtime.Start(context.Background(), startOptions(dirs, port))
	if err != nil {
		t.Fatalf("Start: %v\nlog:\n%s", err, readLog(dirs))
	}
	return result
}

func readLog(dirs paths.Dirs) string {
	data, _ := os.ReadFile(runtime.LogPath(dirs.Runtime))
	return string(data)
}

func wantCode(t *testing.T, err error, code envelope.Code) *envelope.Error {
	t.Helper()
	e := envelope.As(err)
	if e == nil || e.Code != code {
		t.Fatalf("error = %v, want envelope code %s", err, code)
	}
	return e
}

func dialable(port int, host string) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 300*time.Millisecond)
	if err == nil {
		_ = conn.Close()
	}
	return err == nil
}

// AC:start-returns-to-piped-caller: a caller whose stdout is a pipe gets EOF
// as soon as it exits, while the server keeps running and later answers an
// authenticated whoami from another process.
func TestStartReturnsToPipedCallerThenStop(t *testing.T) {
	dirs := testDirs(t)
	port := freePort(t)
	stopOnCleanup(t, dirs)

	caller := exec.Command(os.Args[0], "start", strconv.Itoa(port))
	caller.Env = append(append(os.Environ(), childEnv+"=start"), dirs.Env()...)
	stdout, err := caller.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	if err := caller.Start(); err != nil {
		t.Fatal(err)
	}
	output := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(stdout) // returns only at EOF on the pipe
		output <- data
	}()
	var data []byte
	select {
	case data = <-output:
	case <-time.After(runtime.DefaultTimeout + 5*time.Second):
		t.Fatalf("no EOF on the caller's stdout; log:\n%s", readLog(dirs))
	}
	eof := time.Since(began)
	if err := caller.Wait(); err != nil {
		t.Fatalf("caller: %v, output %s\nlog:\n%s", err, data, readLog(dirs))
	}
	var record runtime.Record
	if err := json.Unmarshal(data, &record); err != nil || record.Port != port {
		t.Fatalf("caller output %q: %v", data, err)
	}
	// EOF arrived while the server still runs, so the child holds no copy
	// of the pipe; the caller's own wait is bounded by readiness plus 2 s.
	if limit := runtime.DefaultTimeout + 2*time.Second; eof > limit {
		t.Errorf("EOF after %s, want under %s", eof, limit)
	}
	t.Logf("piped caller got EOF after %s", eof)

	state, err := runtime.Inspect(context.Background(), dirs.Runtime)
	if err != nil || !state.Running || state.Whoami.Version != testVersion {
		t.Fatalf("Inspect after caller exit = %+v, %v", state, err)
	}
	if state.Record.Home != dirs.Home || state.Record.ProcessIdentity == "" {
		t.Errorf("record = %+v", state.Record)
	}

	// A second start for the same home reports the running server.
	again, err := runtime.Start(context.Background(), startOptions(dirs, port))
	if err != nil || !again.AlreadyRunning || again.State.Record.InstanceID != state.Record.InstanceID {
		t.Fatalf("second Start = %+v, %v", again, err)
	}
	// A different home or explicit port is a mismatch, and starts nothing.
	other := dirs
	other.Home = filepath.Join(t.TempDir(), "other-home")
	_, err = runtime.Start(context.Background(), startOptions(other, port))
	_ = wantCode(t, err, envelope.ServerConfigMismatch)
	explicit := startOptions(dirs, port+1)
	explicit.ExplicitPort = true
	_, err = runtime.Start(context.Background(), explicit)
	_ = wantCode(t, err, envelope.ServerConfigMismatch)

	stopped, err := runtime.Stop(context.Background(), dirs.Runtime, 0)
	if err != nil || !stopped.WasRunning || stopped.Forced {
		t.Fatalf("Stop = %+v, %v", stopped, err)
	}
	if dialable(port, "127.0.0.1") {
		t.Error("port still answers after stop")
	}
	if record, _ := runtime.ReadRecord(dirs.Runtime); record != nil {
		t.Error("server.json left behind after stop")
	}
	if secret, _ := runtime.ReadSecret(dirs.Runtime); secret != "" {
		t.Error("secret left behind after stop")
	}
}

// REQ:stale-runtime-state: a record naming a gone server is "not running"
// and the next start replaces it.
func TestStaleRecordIsReplaced(t *testing.T) {
	t.Parallel()
	dirs := testDirs(t)
	port := freePort(t)
	if _, err := runtime.PrepareDirs(dirs); err != nil {
		t.Fatal(err)
	}
	stale := fmt.Sprintf(`{"schema":1,"instance_id":"stale","home":%q,"pid":999999,"process_identity":"x","port":%d,"version":"old"}`, dirs.Home, port)
	if err := paths.WriteFilePrivate(filepath.Join(dirs.Runtime, runtime.RecordFile), []byte(stale)); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteFilePrivate(filepath.Join(dirs.Runtime, runtime.SecretFile), []byte("old-secret")); err != nil {
		t.Fatal(err)
	}
	state, err := runtime.Inspect(context.Background(), dirs.Runtime)
	if err != nil || state.Running {
		t.Fatalf("Inspect stale = %+v, %v", state, err)
	}
	result := start(t, dirs, port)
	if result.AlreadyRunning || result.State.Record.InstanceID == "stale" {
		t.Fatalf("Start over stale state = %+v", result)
	}
	if secret, _ := runtime.ReadSecret(dirs.Runtime); secret == "old-secret" {
		t.Error("stale secret kept")
	}
}

// AC:stop-never-kills-reused-pid.
func TestStopNeverKillsReusedPID(t *testing.T) {
	t.Parallel()
	dirs := testDirs(t)
	sleeper := exec.Command(os.Args[0])
	sleeper.Env = append(os.Environ(), childEnv+"=sleep")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() })

	if _, err := runtime.PrepareDirs(dirs); err != nil {
		t.Fatal(err)
	}
	record := fmt.Sprintf(`{"schema":1,"instance_id":"gone","home":%q,"pid":%d,"process_identity":"not-this-process","port":%d,"version":"x"}`,
		dirs.Home, sleeper.Process.Pid, freePort(t))
	if err := paths.WriteFilePrivate(filepath.Join(dirs.Runtime, runtime.RecordFile), []byte(record)); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteFilePrivate(filepath.Join(dirs.Runtime, runtime.SecretFile), []byte("s")); err != nil {
		t.Fatal(err)
	}

	_, err := runtime.Stop(context.Background(), dirs.Runtime, time.Second)
	e := wantCode(t, err, envelope.ServerNotRunning)
	if !strings.Contains(e.Reason, strconv.Itoa(sleeper.Process.Pid)) {
		t.Errorf("reason %q does not name the pid", e.Reason)
	}
	state, err := runtime.Inspect(context.Background(), dirs.Runtime)
	if err != nil || state.Running {
		t.Errorf("Inspect = %+v, %v; want not running", state, err)
	}
	if !processAlive(sleeper.Process) {
		t.Fatal("the unrelated process was killed")
	}
	if kept, _ := runtime.ReadRecord(dirs.Runtime); kept == nil {
		t.Error("stop changed server.json although it could not confirm the process")
	}
}

func processAlive(process *os.Process) bool {
	if goruntime.GOOS == "windows" {
		done := make(chan struct{})
		go func() { _, _ = process.Wait(); close(done) }()
		select {
		case <-done:
			return false
		case <-time.After(200 * time.Millisecond):
			return true
		}
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// AC:impostor-port: a program only on [::1] makes start fail with
// port_in_use instead of serving IPv4 only next to it.
func TestImpostorOnIPv6LoopbackIsPortInUse(t *testing.T) {
	t.Parallel()
	impostor, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer func() { _ = impostor.Close() }()
	port := impostor.Addr().(*net.TCPAddr).Port
	if dialable(port, "127.0.0.1") {
		t.Skip("IPv4 port also taken")
	}
	dirs := testDirs(t)
	stopOnCleanup(t, dirs)
	_, err = runtime.Start(context.Background(), startOptions(dirs, port))
	e := wantCode(t, err, envelope.PortInUse)
	if !strings.Contains(e.Reason, strconv.Itoa(port)) {
		t.Errorf("reason %q does not name port %d", e.Reason, port)
	}
	if dialable(port, "127.0.0.1") {
		t.Error("a server is serving IPv4 next to the impostor")
	}
}

// AC:error-envelope-shape (runtime half): a non-OVDB program on the port.
func TestPortHeldByAnotherProgram(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port
	dirs := testDirs(t)
	_, err = runtime.Start(context.Background(), startOptions(dirs, port))
	e := wantCode(t, err, envelope.PortInUse)
	next := strconv.Itoa(port + 1)
	if len(e.Next) != 2 || e.Next[0].Command != "ovdb server start --port "+next || e.Next[1].Command != "ovdb config set server.port "+next {
		t.Errorf("next = %+v", e.Next)
	}
}

// AC:reserved-port: the OS refuses the bind with nobody listening.
func TestReservedPortIsUnavailable(t *testing.T) {
	t.Parallel()
	dirs := testDirs(t)
	port := freePort(t)
	options := startOptions(dirs, port)
	options.Listen = func(network, address string) (net.Listener, error) {
		return nil, &net.OpError{Op: "listen", Net: network, Err: os.NewSyscallError("bind", syscall.EACCES)}
	}
	_, err := runtime.Start(context.Background(), options)
	e := wantCode(t, err, envelope.PortUnavailable)
	if !strings.Contains(e.Reason, "isn't available on this computer") {
		t.Errorf("reason = %q", e.Reason)
	}
}

// REQ:start-failure-in-restricted-environments.
func TestChildExitBeforeReadinessIsStartFailed(t *testing.T) {
	t.Parallel()
	dirs := testDirs(t)
	options := startOptions(dirs, freePort(t))
	command := options.Command
	options.Command = func(port int) *exec.Cmd {
		cmd := command(port)
		cmd.Env = append(cmd.Env, faultEnv+"=1")
		return cmd
	}
	_, err := runtime.Start(context.Background(), options)
	e := wantCode(t, err, envelope.ServerStartFailed)
	if !strings.Contains(e.Reason, "test fault") {
		t.Errorf("reason = %q, want the last log line", e.Reason)
	}
	var sandbox bool
	for _, next := range e.Next {
		sandbox = sandbox || strings.Contains(next.Label, "AI agent in a sandbox")
	}
	if !sandbox {
		t.Errorf("next lacks the sandbox guidance: %+v", e.Next)
	}
	if state, _ := runtime.Inspect(context.Background(), dirs.Runtime); state.Running || state.Record != nil {
		t.Errorf("state after failed start = %+v", state)
	}
}

// AC:existing-dirs-not-chmodded.
func TestExistingOpenDirsAreReportedNotChanged(t *testing.T) {
	t.Parallel()
	if goruntime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dirs := testDirs(t)
	for _, dir := range []string{dirs.Home, dirs.Runtime} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	result, err := runtime.Start(context.Background(), startOptions(dirs, freePort(t)))
	_ = wantCode(t, err, envelope.Forbidden)
	warnings := strings.Join(result.Warnings, "\n")
	for _, dir := range []string{dirs.Home, dirs.Runtime} {
		if !strings.Contains(warnings, "chmod 700 "+dir) {
			t.Errorf("warnings %q lack the fix for %s", warnings, dir)
		}
		if info, _ := os.Stat(dir); info.Mode().Perm() != 0o755 {
			t.Errorf("%s mode changed to %o", dir, info.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(dirs.Runtime, runtime.SecretFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("secret written into an open runtime directory: %v", err)
	}
}

// AC:runtime-files-private-and-local.
func TestRuntimeFilesArePrivate(t *testing.T) {
	t.Parallel()
	dirs := testDirs(t)
	start(t, dirs, freePort(t))
	for _, path := range []string{
		dirs.Home, dirs.Runtime, filepath.Dir(dirs.Runtime),
		filepath.Join(dirs.Runtime, runtime.RecordFile), filepath.Join(dirs.Runtime, runtime.SecretFile),
		filepath.Join(dirs.Runtime, runtime.LockFile), runtime.LogPath(dirs.Runtime),
	} {
		if err := daemonlifecycle.ValidateOwnerOnly(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestIPv6UnavailableContinuesOnIPv4(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	listeners, err := runtime.Listen(port, func(network, address string) (net.Listener, error) {
		if network == "tcp6" {
			return nil, &net.OpError{Op: "listen", Net: network, Err: os.NewSyscallError("bind", ipv6UnavailableErrno)}
		}
		return net.Listen(network, address)
	})
	if err != nil {
		t.Fatalf("Listen = %v", err)
	}
	defer func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}()
	if len(listeners) != 1 || !strings.HasPrefix(listeners[0].Addr().String(), "127.0.0.1:") {
		t.Errorf("listeners = %v", listeners)
	}
}
