// Package porttest leases TCP ports to tests that start a server on a port
// they must know beforehand: the server is a separate process (a detached
// `ovdb server run`, `ovdb serve`), or the port is the one a command line,
// an environment variable or a configuration names, so the test cannot hand
// the server a listener it already holds (the runtime's own check dials the
// port, Windows has no inherited sockets, and 0 is no valid port for a command
// line).
//
// The usual way to get such a port, binding 127.0.0.1:0 and closing it, hands
// back a port from the range the operating system also gives to every other
// bind(0) and to the source side of every outgoing connection, and releases it
// for the whole time until the server binds it. Another parallel test, in this
// package or in any other package running at the same time, can be given the
// same number then (it is free), or use it as the source port of a connection
// to its own server: "Port N is already used by another program" in whichever
// test lost the race (issue #29).
//
// Lease takes ports from a range below every operating system's ephemeral
// range (Linux 32768-60999, macOS and Windows 49152-65535), so nothing that
// asks the OS for "any port" can land on one, and holds the right to each
// number with an advisory file lock (shared by every process of every package
// that uses this package, released by the OS when a process dies), so no two
// tests are given the same one while a lease is held. A number something else
// already listens on is skipped. What remains is a program outside the tests
// that binds exactly a leased number in the time between lease and use.
//
// Only tests import this package.
package porttest

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/strongo/cli-helpers/daemonlifecycle"
)

const (
	// First is the first leasable port. The range ends below the lowest
	// ephemeral range of Linux, macOS and Windows.
	First = 20000
	// Count is the number of leasable ports.
	Count = 4000
)

// next spreads concurrent leases of one process over the range, so they do
// not all contend for the first slot.
var next atomic.Uint32

// lockDir is where the lease files live: one directory per user and machine,
// shared by every test process.
func lockDir() string {
	return filepath.Join(os.TempDir(), "ovdb-test-ports")
}

// Lease returns a TCP port on the loopback addresses that no other lease
// holds, until the end of the test (t.Cleanup releases it). Register the
// cleanup that stops a server using the port after calling Lease, so the
// server is stopped first.
func Lease(t testing.TB) int {
	t.Helper()
	return LeaseRun(t, 1)
}

// LeaseRun returns the first of n consecutive ports that are all leased until
// the end of the test: for a test that needs a port and the one after it (the
// port-in-use guidance offers "start --port N+1").
func LeaseRun(t testing.TB, n int) int {
	t.Helper()
	if n < 1 || n > Count/2 {
		t.Fatalf("porttest: cannot lease %d consecutive ports", n)
	}
	dir := lockDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("porttest: %v", err)
	}
	span := Count - n + 1
	start := int((uint32(os.Getpid())*2654435761 + next.Add(uint32(n))*7919) % uint32(span))
	for i := 0; i < span; i++ {
		port := First + (start+i)%span
		if files, ok := leaseSlots(dir, port, n); ok {
			t.Cleanup(func() {
				for _, file := range files {
					_ = daemonlifecycle.Unlock(file)
					_ = file.Close()
				}
			})
			return port
		}
	}
	t.Fatalf("porttest: no %d consecutive free ports to lease in %d-%d", n, First, First+Count-1)
	return 0
}

// leaseSlots locks the n files of ports port..port+n-1 and checks that
// nothing is listening on them; on any failure it releases what it took.
func leaseSlots(dir string, port, n int) ([]*os.File, bool) {
	var files []*os.File
	release := func() {
		for _, file := range files {
			_ = daemonlifecycle.Unlock(file)
			_ = file.Close()
		}
	}
	for p := port; p < port+n; p++ {
		file, err := os.OpenFile(filepath.Join(dir, "port-"+strconv.Itoa(p)+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			release()
			return nil, false
		}
		locked, err := daemonlifecycle.TryLock(file)
		if err != nil || !locked {
			_ = file.Close()
			release()
			return nil, false
		}
		files = append(files, file)
		if !free(p) {
			release()
			return nil, false
		}
	}
	return files, true
}

// free reports whether nothing answers on either loopback address of port and
// 127.0.0.1:port can be bound, as the runtime's own check does.
func free(port int) bool {
	for _, host := range []string{"127.0.0.1", "::1"} {
		dialer := net.Dialer{Timeout: 300 * time.Millisecond}
		if conn, err := dialer.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port))); err == nil {
			_ = conn.Close()
			return false
		}
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}
