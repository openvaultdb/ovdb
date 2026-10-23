//go:build windows

package runtime_test

import "syscall"

// WSAEADDRNOTAVAIL.
var ipv6UnavailableErrno = syscall.Errno(10049)
