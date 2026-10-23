//go:build !windows

package runtime

import (
	"errors"
	"syscall"
)

func isAddrInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }

// isIPv6Unavailable matches a host or container without IPv6 or without ::1.
func isIPv6Unavailable(err error) bool {
	return errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EAFNOSUPPORT) ||
		errors.Is(err, syscall.EPROTONOSUPPORT)
}
