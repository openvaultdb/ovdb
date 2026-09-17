//go:build !windows

package runtime_test

import "syscall"

var ipv6UnavailableErrno = syscall.EADDRNOTAVAIL
