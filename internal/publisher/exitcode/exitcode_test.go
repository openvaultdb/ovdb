package exitcode

import (
	"errors"
	"fmt"
	"testing"
)

var errBase = errors.New("no such directory")

func TestUsage(t *testing.T) {
	if Usage(nil) != nil {
		t.Fatal("Usage(nil) is not nil")
	}
	err := Usage(errBase)
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 2 || UsageCode != 2 {
		t.Fatalf("Usage carries %v, want exit code 2", coded)
	}
	if err.Error() != errBase.Error() {
		t.Errorf("Error() = %q, want %q", err.Error(), errBase.Error())
	}
	if !errors.Is(err, errBase) {
		t.Error("the wrapped error is not reachable with errors.Is")
	}
	var typed *Error
	if !errors.As(fmt.Errorf("check: %w", err), &typed) || typed.ExitCode() != 2 {
		t.Error("the exit code does not survive wrapping")
	}
}

func TestUsagef(t *testing.T) {
	err := Usagef("repository %q: %w", "x", errBase)
	if err.Error() != `repository "x": no such directory` || !errors.Is(err, errBase) {
		t.Errorf("Usagef = %v", err)
	}
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 2 {
		t.Error("Usagef does not carry exit code 2")
	}
}
