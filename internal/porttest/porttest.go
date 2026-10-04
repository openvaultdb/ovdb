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
// asks the OS for "any port" can land on one.
//
// The lease on TCP port N is a UDP socket bound to 127.0.0.1:N (and to [::1]:N
// where the machine has IPv6 loopback), held until the test ends. The UDP port
// space is the operating system's own table, so the lease is exclusive between
// every process on the machine that goes through Lease or Hold, whatever its
// user, TMPDIR or working directory (Linux network namespaces, which a
// container may have, are one table each), and the OS drops it when the
// process dies, however it dies. It does not stop a TCP listener on the same
// number (the TCP and UDP tables are separate), which is what lets the test's
// server bind the number. Go sets no SO_REUSEADDR or SO_REUSEPORT on a unicast
// UDP socket on any system (only on stream listeners and multicast sockets),
// so another Go program cannot share the number by accident; a program that
// asks for SO_REUSEADDR on UDP itself can, on Linux and macOS.
//
// What the lease does not guarantee: a program outside the tests (not using
// this package) that binds exactly the leased number for TCP between the lease
// and the server's own bind still wins, and a number something already listens
// on, or answers on, is skipped when leasing, not reserved against a listener
// that appears later.
//
// Only tests import this package.
package porttest

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

// hasIPv6Loopback reports whether [::1] can be bound on this machine.
var hasIPv6Loopback = sync.OnceValue(func() bool {
	conn, err := net.ListenPacket("udp6", "[::1]:0")
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
})

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
	span := Count - n + 1
	start := int((uint32(os.Getpid())*2654435761 + next.Add(uint32(n))*7919) % uint32(span))
	for i := 0; i < span; i++ {
		port := First + (start+i)%span
		var releases []func()
		held := true
		for p := port; p < port+n; p++ {
			release, ok := Hold(p)
			if !ok {
				held = false
				break
			}
			releases = append(releases, release)
		}
		if held {
			t.Cleanup(func() {
				for _, release := range releases {
					release()
				}
			})
			return port
		}
		for _, release := range releases {
			release()
		}
	}
	t.Fatalf("porttest: no %d consecutive free ports to lease in %d-%d", n, First, First+Count-1)
	return 0
}

// Hold takes the lease on one specific port, for a process that is not a Go
// test (or one that must name the number), and reports whether it got it: it
// does not if another holder has it or something listens on, or answers at,
// that TCP number. release gives the lease back; the OS gives it back anyway
// when the process ends.
func Hold(port int) (release func(), ok bool) {
	var conns []net.PacketConn
	release = func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}
	addrs := []struct{ network, addr string }{{"udp4", fmt.Sprintf("127.0.0.1:%d", port)}}
	if hasIPv6Loopback() {
		addrs = append(addrs, struct{ network, addr string }{"udp6", fmt.Sprintf("[::1]:%d", port)})
	}
	for _, a := range addrs {
		conn, err := net.ListenPacket(a.network, a.addr)
		if err != nil {
			release()
			return nil, false
		}
		conns = append(conns, conn)
	}
	if !free(port) {
		release()
		return nil, false
	}
	return release, true
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
