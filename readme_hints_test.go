package main

import (
	"os"
	"strings"
	"testing"
)

// The hints the README quotes are the ones ovdb prints: the README is held to
// the code, URL included.
func TestREADMEQuotesTheHintsOvdbPrints(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := strings.Join(strings.Fields(string(data)), " ")
	for _, goos := range []string{"windows", "linux"} {
		hint := systemPackageHint(goos, "")
		if !strings.Contains(readme, `"`+hint+`"`) {
			t.Errorf("the README does not quote the %s hint exactly as printed:\n%s", goos, hint)
		}
	}
}
