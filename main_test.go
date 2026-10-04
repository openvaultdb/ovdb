package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/exitcode"
)

type processExitError int

func (e processExitError) Error() string { return "process failed" }
func (e processExitError) ExitCode() int { return int(e) }

func TestCommandExitCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "plain error", err: fmt.Errorf("failed"), want: 1},
		{name: "wrapped process status", err: fmt.Errorf("self-update: %w", processExitError(23)), want: 23},
		{name: "zero is invalid for an error", err: processExitError(0), want: 1},
		{name: "negative is invalid", err: processExitError(-1), want: 1},
		{name: "outside portable process range", err: processExitError(256), want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandExitCode(tc.err); got != tc.want {
				t.Errorf("commandExitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// The publisher commands (added in a later change) fail with exit code 2 for
// bad usage or an environment that cannot run them; commandExitCode passes the
// code of internal/publisher/exitcode through, wrapped or not, and the exit code
// of every other error is unchanged.
func TestCommandExitCodeUsageError(t *testing.T) {
	usage := exitcode.Usage(errors.New("no such directory"))
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "usage error", err: usage, want: 2},
		{name: "wrapped usage error", err: fmt.Errorf("publisher check: %w", usage), want: 2},
		{name: "joined with another error", err: errors.Join(errors.New("other"), usage), want: 2},
		{name: "formatted usage error", err: exitcode.Usagef("flag %q is not known", "--x"), want: 2},
		{name: "a finding is a plain error", err: errors.New("manifest has 3 problems"), want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandExitCode(tc.err); got != tc.want {
				t.Errorf("commandExitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
