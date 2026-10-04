package porttest

import (
	"net"
	"strconv"
	"testing"
)

// Leases taken one after the other, in the private range, are all different,
// can be bound by whoever holds them, and a held number is refused to anyone
// asking for it (as another process would be: the lock is the OS's, per open
// file).
func TestLeasesAreExclusiveAndUsable(t *testing.T) {
	seen := map[int]bool{}
	for i := 0; i < 64; i++ {
		port := Lease(t)
		if port < First || port >= First+Count {
			t.Fatalf("port %d is outside the private range", port)
		}
		if seen[port] {
			t.Fatalf("port %d leased twice", port)
		}
		seen[port] = true
		if files, ok := leaseSlots(lockDir(), port, 1); ok {
			for _, file := range files {
				_ = file.Close()
			}
			t.Fatalf("port %d can be leased again while held", port)
		}
	}
	for port := range seen {
		listener, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Errorf("leased port %d: %v", port, err)
			continue
		}
		_ = listener.Close()
	}
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
		if seen[port] {
			t.Errorf("port %d leased twice at once", port)
		}
		seen[port] = true
	}
}

// LeaseRun gives consecutive ports, and a number something listens on is not
// leased.
func TestLeaseRunIsConsecutiveAndSkipsWhatIsListenedOn(t *testing.T) {
	first := LeaseRun(t, 3)
	if first < First || first+2 >= First+Count {
		t.Fatalf("ports %d.. are outside the private range", first)
	}
	busy, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(first+3))
	if err != nil {
		t.Skipf("neighbouring port taken: %v", err)
	}
	defer func() { _ = busy.Close() }()
	if free(first + 3) {
		t.Error("free reports a listened-on port as free")
	}
	if !free(first) {
		t.Error("free reports an idle leased port as busy")
	}
}
