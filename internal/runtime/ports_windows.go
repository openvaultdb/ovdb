//go:build windows

package runtime

import (
	"errors"
	"syscall"
)

// Winsock error numbers; syscall.EADDRINUSE on Windows is an invented value
// that bind errors never carry.
const (
	wsaeAddrInUse      syscall.Errno = 10048
	wsaeAddrNotAvail   syscall.Errno = 10049
	wsaeAfNoSupport    syscall.Errno = 10047
	wsaeProtoNoSupport syscall.Errno = 10043
)

func isAddrInUse(err error) bool { return errors.Is(err, wsaeAddrInUse) }

// isIPv6Unavailable matches a host without IPv6 or without ::1. A reserved
// port range fails with WSAEACCES instead and stays port_unavailable.
func isIPv6Unavailable(err error) bool {
	return errors.Is(err, wsaeAddrNotAvail) || errors.Is(err, wsaeAfNoSupport) ||
		errors.Is(err, wsaeProtoNoSupport)
}
