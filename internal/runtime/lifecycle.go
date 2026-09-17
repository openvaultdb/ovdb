package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/strongo/cli-helpers/daemonlifecycle"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/redact"
)

// DefaultTimeout bounds readiness after start and port release after stop.
const DefaultTimeout = 10 * time.Second

const pollInterval = 50 * time.Millisecond

// State is what the runtime directory says about the server, confirmed by
// an authenticated whoami.
type State struct {
	Running bool
	Record  *Record // nil when there is no server.json
	Secret  string
	Whoami  *Whoami // the running server's answer
}

// Inspect reads server.json and the secret and asks the recorded port who
// is there. A record whose server does not answer as the recorded instance
// is stale: the state is "not running" (REQ:stale-runtime-state).
func Inspect(ctx context.Context, runtimeDir string) (State, error) {
	record, err := ReadRecord(runtimeDir)
	if err != nil || record == nil {
		return State{}, err
	}
	state := State{Record: record}
	if state.Secret, err = ReadSecret(runtimeDir); err != nil || state.Secret == "" {
		return state, err
	}
	whoami, err := Probe(ctx, record.Port, state.Secret)
	if err == nil && whoami.InstanceID == record.InstanceID {
		state.Running, state.Whoami = true, whoami
	}
	return state, nil
}

// CheckMatch fails with server_config_mismatch when a client whose home or
// explicitly chosen port differs from the running server would otherwise
// silently talk to — or start next to — the wrong server.
func CheckMatch(record *Record, dirs paths.Dirs, port int, explicitPort bool) *envelope.Error {
	params := map[string]string{"home": record.Home, "port": strconv.Itoa(record.Port)}
	var reason string
	switch {
	case record.Home != dirs.Home:
		reason = uicopy.T("server.mismatch.home", params)
	case explicitPort && port != record.Port:
		params["requested"] = strconv.Itoa(port)
		reason = uicopy.T("server.mismatch.port", params)
	default:
		return nil
	}
	return envelope.New(envelope.ServerConfigMismatch, uicopy.T("server.mismatch.message", nil)).
		WithReason(reason).
		WithNext(
			envelope.Next{Label: uicopy.T("next.unset_overrides", nil), Command: "unset " + paths.EnvHome + " OVDB_PORT"},
			envelope.Next{Label: uicopy.T("next.restart_from_here", nil), Command: "ovdb server restart"},
		)
}

// StartOptions describe one start.
type StartOptions struct {
	Dirs         paths.Dirs
	Port         int
	ExplicitPort bool // --port or OVDB_PORT, as opposed to config or default
	// Command returns the template of the process that serves port. Start
	// adds the working directory (OVDB home) and the directory variables.
	Command func(port int) *exec.Cmd
	Timeout time.Duration // DefaultTimeout when zero
	Listen  ListenFunc    // net.Listen when nil
}

// StartResult reports a successful start or an already running server.
type StartResult struct {
	State          State
	AlreadyRunning bool
	Warnings       []string // directories other users can access
}

// Start starts the local server detached and waits for authenticated
// readiness (REQ:background-start). It succeeds without starting anything
// when this home's server already runs, fails with server_config_mismatch
// when a different home or port runs, and fails with port_in_use,
// port_unavailable, forbidden or server_start_failed otherwise. Warnings are
// returned on failure too.
func Start(ctx context.Context, opts StartOptions) (StartResult, error) {
	var result StartResult
	var dirErr *envelope.Error
	if result.Warnings, dirErr = PrepareDirs(opts.Dirs); dirErr != nil {
		return result, dirErr
	}
	if running, err := runningServer(ctx, opts); running != nil || err != nil {
		result.State, result.AlreadyRunning = *running, err == nil
		return result, err
	}
	if portErr := CheckPort(ctx, opts.Port, opts.Listen); portErr != nil {
		return result, portErr
	}

	logPath := LogPath(opts.Dirs.Runtime)
	process, logOffset, err := spawn(opts, logPath)
	if err != nil {
		return result, startFailed(logPath, err.Error())
	}
	exited := make(chan struct{})
	go func() {
		_, _ = process.Wait()
		close(exited)
	}()

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-exited:
			// A concurrent start may have won the home lock; that is success.
			if running, err := runningServer(ctx, opts); running != nil || err != nil {
				result.State, result.AlreadyRunning = *running, err == nil
				return result, err
			}
			return result, startFailed(logPath, lastLogLine(logPath, logOffset))
		case <-ticker.C:
			state, err := Inspect(ctx, opts.Dirs.Runtime)
			if err == nil && state.Running && state.Record.PID == process.Pid {
				result.State = state
				return result, nil
			}
		case <-deadline.C:
			_ = process.Kill()
			<-exited
			return result, startFailed(logPath, lastLogLine(logPath, logOffset))
		case <-ctx.Done():
			_ = process.Kill()
			<-exited
			return result, ctx.Err()
		}
	}
}

// runningServer returns this home's running server, a mismatch error for a
// different one, or nil, nil when none answers.
func runningServer(ctx context.Context, opts StartOptions) (*State, error) {
	state, err := Inspect(ctx, opts.Dirs.Runtime)
	if err != nil || !state.Running {
		return nil, nil // unreadable or stale state is replaced by the start
	}
	if mismatch := CheckMatch(state.Record, opts.Dirs, opts.Port, opts.ExplicitPort); mismatch != nil {
		return &state, mismatch
	}
	return &state, nil
}

func spawn(opts StartOptions, logPath string) (*os.Process, int64, error) {
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = log.Close() }()
	if err := daemonlifecycle.ProtectOwnerOnlyFile(log); err != nil {
		return nil, 0, err
	}
	var offset int64
	if info, statErr := log.Stat(); statErr == nil {
		offset = info.Size()
	}
	template := opts.Command(opts.Port)
	template.Dir = opts.Dirs.Home
	if template.Env == nil {
		template.Env = os.Environ()
	}
	template.Env = append(template.Env, opts.Dirs.Env()...)
	process, err := daemonlifecycle.StartDetached(template, log)
	return process, offset, err
}

// startFailed is server_start_failed with the redacted last log line and
// the sandbox guidance (REQ:start-failure-in-restricted-environments).
func startFailed(logPath, lastLine string) *envelope.Error {
	reason := uicopy.T("server.start.not_ready", nil)
	if lastLine != "" {
		reason = redact.String(lastLine)
	}
	return envelope.New(envelope.ServerStartFailed, uicopy.T("server.start.failed", nil)).
		WithReason(reason).
		WithNext(
			envelope.Next{Label: uicopy.T("next.sandbox", nil), Command: "ovdb open"},
			envelope.Next{Label: uicopy.T("next.read_log", map[string]string{"log": logPath})},
		)
}

// lastLogLine returns the last line the server logged after offset, without
// its timestamp. Lines the server logged itself start with an RFC 3339
// timestamp and are preferred over anything else written to the log (such
// as the failing command's own error output); without one, the last
// non-empty line is used.
func lastLogLine(logPath string, offset int64) string {
	file, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	var last, lastLogged string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		last = line
		if stamp, message, ok := strings.Cut(line, " "); ok {
			if _, err := time.Parse(time.RFC3339, stamp); err == nil {
				lastLogged = message
			}
		}
	}
	if lastLogged != "" {
		return lastLogged
	}
	return last
}

// StopResult reports what Stop did.
type StopResult struct {
	WasRunning bool
	Forced     bool // the server did not answer and its verified process was terminated
	Record     *Record
}

// Stop shuts the server down through the authenticated API and waits until
// its process is gone. If the server does not answer, the recorded pid is
// terminated only while its process identity still matches server.json;
// otherwise nothing is changed and the failure says the process could not be
// confirmed (REQ:authenticated-stop, AC:stop-never-kills-reused-pid).
func Stop(ctx context.Context, runtimeDir string, timeout time.Duration) (StopResult, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	state, err := Inspect(ctx, runtimeDir)
	if err != nil || state.Record == nil {
		return StopResult{}, err
	}
	record := state.Record
	result := StopResult{Record: record}
	if state.Running {
		if err := requestShutdown(ctx, record.Port, state.Secret); err != nil {
			return result, err
		}
		result.WasRunning = true
		return result, waitGone(ctx, record, timeout)
	}

	switch identity, err := daemonlifecycle.ProcessIdentity(record.PID); {
	case errors.Is(err, daemonlifecycle.ErrProcessNotFound):
		return result, nil // stale record; the next start replaces it
	case err != nil:
		return result, err
	case identity != record.ProcessIdentity:
		return result, unconfirmedProcess(record)
	}
	if err := daemonlifecycle.TerminateIfSameProcess(record.PID, record.ProcessIdentity); err != nil {
		if errors.Is(err, daemonlifecycle.ErrProcessMismatch) {
			return result, unconfirmedProcess(record)
		}
		if !errors.Is(err, daemonlifecycle.ErrProcessNotFound) {
			return result, err
		}
	}
	result.WasRunning, result.Forced = true, true
	if err := waitGone(ctx, record, timeout); err != nil {
		return result, err
	}
	removeRuntimeFiles(runtimeDir)
	return result, nil
}

func unconfirmedProcess(record *Record) *envelope.Error {
	return envelope.New(envelope.ServerNotRunning, uicopy.T("server.stop.failed", nil)).
		WithReason(uicopy.T("server.stop.unconfirmed", map[string]string{"pid": strconv.Itoa(record.PID)})).
		WithNext(envelope.Next{Label: uicopy.T("next.server_status", nil), Command: "ovdb server status"})
}

// waitGone waits until the recorded process no longer exists, which also
// means its port and home lock are free.
func waitGone(ctx context.Context, record *Record, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		identity, err := daemonlifecycle.ProcessIdentity(record.PID)
		if errors.Is(err, daemonlifecycle.ErrProcessNotFound) || (err == nil && identity != record.ProcessIdentity) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the OVDB server (pid %d) did not exit within %s", record.PID, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
