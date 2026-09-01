package main

import (
	"fmt"
	"testing"
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
