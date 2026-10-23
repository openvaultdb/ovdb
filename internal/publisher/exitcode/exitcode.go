// Package exitcode is the error type that the publisher commands return for a
// failure that is not a finding: bad usage, or an environment that cannot run
// the command. It carries exit code 2.
//
// The process exit code is chosen by main's commandExitCode, which passes
// through the ExitCode() of any error in the chain (errors.As) when it is in
// 1..255 and otherwise exits 1. So returning one of these errors from a command
// is all that is needed, and no existing command changes: they return plain
// errors and keep exiting 1.
//
// By the convention of the publisher commands, 0 means everything passed, 1
// means the check ran and found something wrong, and 2 means it could not run
// as asked.
package exitcode

import "fmt"

// UsageCode is the exit code for a usage or environment error.
const UsageCode = 2

// Error is an error that carries an exit code.
type Error struct {
	err  error
	code int
}

// Error returns the message of the wrapped error.
func (e *Error) Error() string { return e.err.Error() }

// Unwrap returns the wrapped error, so errors.Is and errors.As see through it.
func (e *Error) Unwrap() error { return e.err }

// ExitCode returns the process exit code for the error.
func (e *Error) ExitCode() int { return e.code }

// Usage wraps err as a usage or environment error: exit code 2. It returns nil
// for a nil error.
func Usage(err error) error {
	if err == nil {
		return nil
	}
	return &Error{err: err, code: UsageCode}
}

// Usagef is Usage of fmt.Errorf(format, args...); a %w verb wraps as usual.
func Usagef(format string, args ...any) error {
	return Usage(fmt.Errorf(format, args...))
}
