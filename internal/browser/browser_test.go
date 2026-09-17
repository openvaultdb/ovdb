package browser

import (
	"os/exec"
	"testing"
)

func TestCommandForPlatforms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		platform string
		name     string
		args     []string
	}{
		{"darwin", "open", []string{"http://x"}},
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", "http://x"}},
		{"linux", "xdg-open", []string{"http://x"}},
		{"freebsd", "xdg-open", []string{"http://x"}},
	}
	for _, tc := range cases {
		name, args := commandFor(tc.platform, "http://x")
		if name != tc.name || len(args) != len(tc.args) || args[len(args)-1] != tc.args[len(tc.args)-1] {
			t.Errorf("commandFor(%q) = %q %v, want %q %v", tc.platform, name, args, tc.name, tc.args)
		}
	}
}

func TestOpenUsesCommandAndStarts(t *testing.T) {
	var gotName string
	var gotArgs []string
	orig := Command
	defer func() { Command = orig }()
	Command = func(name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, args
		return exec.Command("true")
	}
	if err := Open("http://example.com"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	name, args := commandFor(goos, "http://example.com")
	if gotName != name {
		t.Errorf("Command called with %q, want %q", gotName, name)
	}
	if len(gotArgs) != len(args) {
		t.Errorf("Command args %v, want %v", gotArgs, args)
	}
}

func TestOpenReportsUnstartableCommand(t *testing.T) {
	orig := Command
	defer func() { Command = orig }()
	Command = func(string, ...string) *exec.Cmd {
		return exec.Command("ovdb-browser-test-binary-that-does-not-exist")
	}
	err := Open("http://example.com")
	if err == nil {
		t.Fatal("Open with a missing opener binary should return an error")
	}
}
