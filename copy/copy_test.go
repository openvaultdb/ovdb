package uicopy

import (
	"strings"
	"testing"
)

func TestTRendersAKnownKey(t *testing.T) {
	got := T("home.menu.try_demo", nil)
	if want := "Try a demo"; got != want {
		t.Fatalf("T(...) = %q, want %q", got, want)
	}
}

func TestTSubstitutesPlaceholders(t *testing.T) {
	got := T("console.placeholder.body", map[string]string{"name": "ovdb"})
	if !strings.Contains(got, "ovdb") {
		t.Fatalf("T(...) = %q, want it to contain the substituted param", got)
	}
	if strings.Contains(got, "{name}") {
		t.Fatalf("T(...) = %q, still contains an unsubstituted placeholder", got)
	}
}

func TestTLeavesUnknownPlaceholdersInPlace(t *testing.T) {
	// console.placeholder.body has one placeholder, {name}; passing no
	// params must leave it verbatim rather than blanking it out.
	got := T("console.placeholder.body", nil)
	if !strings.Contains(got, "{name}") {
		t.Fatalf("T(...) = %q, want the unsubstituted placeholder preserved", got)
	}
}

func TestTPanicsOnMissingKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("T(...) with an unknown key did not panic")
		}
	}()
	T("this.key.does.not.exist", nil)
}
