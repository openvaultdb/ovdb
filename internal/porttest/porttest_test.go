package porttest

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const childEnv = "OVDB_PORTTEST_CHILD"

// A leased number is refused to a second Hold in this process, and a UDP
// socket from outside (not asking for address reuse) cannot take it on either
// loopback family or on the wildcard.
func TestHeldNumberIsRefusedToEveryOtherBinder(t *testing.T) {
	port := Lease(t)
	if release, ok := Hold(port); ok {
		release()
		t.Fatalf("port %d can be held again while leased", port)
	}
	for _, a := range []struct{ network, addr string }{
		{"udp4", "127.0.0.1"}, {"udp4", "0.0.0.0"}, {"udp6", "[::1]"}, {"udp6", "[::]"},
	} {
		if a.network == "udp6" && !hasIPv6Loopback() {
			continue
		}
		conn, err := net.ListenPacket(a.network, a.addr+":"+strconv.Itoa(port))
		if err == nil {
			_ = conn.Close()
			t.Errorf("%s %s:%d bound while the lease is held", a.network, a.addr, port)
		}
	}
}

// Where the machine has IPv6 loopback, the lease holds it too: a holder of
// only the IPv4 side would let a [::1] binder in.
func TestLeaseCoversIPv6Loopback(t *testing.T) {
	if !hasIPv6Loopback() {
		t.Skip("no IPv6 loopback on this machine")
	}
	port := Lease(t)
	if conn, err := net.ListenPacket("udp6", "[::1]:"+strconv.Itoa(port)); err == nil {
		_ = conn.Close()
		t.Fatalf("[::1]:%d bound while leased", port)
	}
}

// Closing the lease gives the number back, and the TCP side is never blocked:
// the server the test starts binds the number it leased.
func TestReleaseFreesTheNumberAndTCPStaysBindable(t *testing.T) {
	port := First + 17
	release, ok := Hold(port)
	if !ok {
		t.Skipf("port %d taken on this machine", port)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		release()
		t.Fatalf("TCP listen on a leased number: %v", err)
	}
	_ = listener.Close()
	release()
	again, ok := Hold(port)
	if !ok {
		t.Fatalf("port %d not holdable after release", port)
	}
	again()
}

// Parallel tests, as the module's are, never get the same lease.
func TestParallelLeasesAreDistinct(t *testing.T) {
	ports := make(chan int, 32)
	t.Run("group", func(t *testing.T) {
		for i := 0; i < cap(ports); i++ {
			t.Run("lease", func(t *testing.T) {
				t.Parallel()
				ports <- Lease(t)
			})
		}
	})
	close(ports)
	seen := map[int]bool{}
	for port := range ports {
		if port < First || port >= First+Count {
			t.Errorf("port %d is outside the private range", port)
		}
		if seen[port] {
			t.Errorf("port %d leased twice at once", port)
		}
		seen[port] = true
	}
}

// LeaseRun gives consecutive ports that are all held, and a number something
// listens on is not leased.
func TestLeaseRunIsConsecutiveAndSkipsWhatIsListenedOn(t *testing.T) {
	first := LeaseRun(t, 4)
	if first < First || first+3 >= First+Count {
		t.Fatalf("ports %d.. are outside the private range", first)
	}
	for p := first; p < first+4; p++ {
		if release, ok := Hold(p); ok {
			release()
			t.Errorf("port %d of the run is not held", p)
		}
	}
	busy, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(first+4))
	if err != nil {
		t.Skipf("neighbouring port taken: %v", err)
	}
	defer func() { _ = busy.Close() }()
	if release, ok := Hold(first + 4); ok {
		release()
		t.Error("Hold took a number something listens on")
	}
	if free(first + 4) {
		t.Error("free reports a listened-on port as free")
	}
	if !free(first) {
		t.Error("free reports an idle leased port as busy")
	}
}

// TestChild is the body of the processes the tests below start (the test binary
// re-executes itself); it does nothing in a normal run.
func TestChild(t *testing.T) {
	mode := os.Getenv(childEnv)
	if mode == "" {
		t.Skip("only runs as a child of another test")
	}
	port, _ := strconv.Atoi(os.Getenv(childEnv + "_PORT"))
	switch mode {
	case "hold":
		release, ok := Hold(port)
		if ok {
			fmt.Println("RESULT held")
			release()
		} else {
			fmt.Println("RESULT refused")
		}
	case "holdwait":
		release, ok := Hold(port)
		if !ok {
			fmt.Println("RESULT refused")
			return
		}
		defer release()
		fmt.Println("RESULT held")
		_, _ = os.Stdin.Read(make([]byte, 1)) // until the parent closes our stdin
	case "tcp":
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			fmt.Println("RESULT tcp-error", err)
			return
		}
		_ = listener.Close()
		fmt.Println("RESULT tcp-bound")
	}
}

// child runs the test binary as TestChild in mode, with its own TMPDIR (as
// another user, a sandbox or another package's test run has), and returns the
// "RESULT ..." line it printed.
func child(t *testing.T, mode string, port int) string {
	t.Helper()
	cmd := childCommand(t, mode, port)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child %s: %v\n%s", mode, err, out)
	}
	return result(t, string(out))
}

func childCommand(t *testing.T, mode string, port int) *exec.Cmd {
	t.Helper()
	other := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestChild$", "-test.v=false")
	cmd.Env = append(os.Environ(),
		childEnv+"="+mode, childEnv+"_PORT="+strconv.Itoa(port),
		"TMPDIR="+other, "TMP="+other, "TEMP="+other)
	return cmd
}

func result(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "RESULT "); ok {
			return rest
		}
	}
	t.Fatalf("child printed no RESULT line:\n%s", out)
	return ""
}

// The exclusion holds between processes that do not share a temporary
// directory (the first version of the lease, a lock file under os.TempDir,
// handed the same number to both), and a process of the test can still bind
// the leased number for TCP.
func TestLeaseExcludesProcessesWithOtherTMPDIR(t *testing.T) {
	port := Lease(t)
	if got := child(t, "hold", port); got != "refused" {
		t.Errorf("child with another TMPDIR: %q, want it refused a number this process leased", got)
	}
	if got := child(t, "tcp", port); got != "tcp-bound" {
		t.Errorf("child binding TCP on the leased number: %q", got)
	}
}

// The other direction: a child holds a number, this process is refused it
// while the child lives, and gets it once the child has gone (the OS gives the
// lease back).
func TestLeaseHeldByAnotherProcessIsRefusedUntilItExits(t *testing.T) {
	port := First + 3500 + os.Getpid()%400
	cmd := childCommand(t, "holdwait", port)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := false
	defer func() {
		if !exited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	line := make([]byte, 4096)
	n, _ := stdout.Read(line)
	got := result(t, string(line[:n]))
	if got == "refused" {
		t.Skipf("port %d taken on this machine", port)
	}
	if release, ok := Hold(port); ok {
		release()
		t.Fatalf("this process got port %d while the child holds it", port)
	}
	_ = stdin.Close()
	_ = cmd.Wait()
	exited = true
	deadline := time.Now().Add(5 * time.Second)
	for {
		release, ok := Hold(port)
		if ok {
			release()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("port %d not given back after the holder exited", port)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
