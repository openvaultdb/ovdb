package repo

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// The tests of ExecRunner run the test binary itself as the git program: with OVDB_FAKE_GIT set, init
// runs fakeGit and exits before any test does. No other program is needed, and none is run.
func init() {
	if mode := os.Getenv("OVDB_FAKE_GIT"); mode != "" {
		fakeGit(mode)
		os.Exit(0)
	}
}

func fakeGit(mode string) {
	var n, code int
	switch mode {
	case "args":
		fmt.Println(strings.Join(os.Args[1:], "\n"))
		for _, kv := range os.Environ() {
			if strings.HasPrefix(strings.ToUpper(kv), "GIT_") || strings.HasPrefix(kv, "LC_ALL=") {
				fmt.Println("ENV", kv)
			}
		}
	case "sleep":
		time.Sleep(time.Minute)
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("noise\n", 2000), "the last line\n")
		os.Exit(3)
	default:
		if _, err := fmt.Sscanf(mode, "outsleep:%d", &n); err == nil {
			fmt.Print(strings.Repeat("a", n)) // all of it fits in the pipe: git has nothing more to write, and does not exit
			time.Sleep(time.Minute)
		} else if _, err := fmt.Sscanf(mode, "out:%d", &n); err == nil {
			fmt.Print(strings.Repeat("a", n))
		} else if _, err := fmt.Sscanf(mode, "exit:%d", &code); err == nil {
			fmt.Fprint(os.Stderr, "boom\n  \nfatal: it failed \x1b[31m\n")
			os.Exit(code)
		}
	}
}

func runner(t *testing.T, mode string) ExecRunner {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVDB_FAKE_GIT", mode)
	return ExecRunner{Git: exe, Dir: "/some/dir"}
}

func TestExecRunnerRunsGitInTheDirectoryWithACleanEnvironment(t *testing.T) {
	t.Setenv("GIT_DIR", "/elsewhere")
	t.Setenv("git_work_tree", "/elsewhere")
	t.Setenv("GIT_CONFIG_GLOBAL", "/home/someone/.gitconfig")
	out, err := runner(t, "args").Run([]string{"cat-file", "blob", "x:y"}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if !slices.Equal(lines[:4], []string{"-C", "/some/dir", "cat-file", "blob"}) || lines[4] != "x:y" {
		t.Errorf("arguments %q", lines)
	}
	env := lines[5:]
	for _, want := range []string{"ENV GIT_CONFIG_NOSYSTEM=1", "ENV GIT_CONFIG_GLOBAL=" + os.DevNull, "ENV GIT_TERMINAL_PROMPT=0", "ENV GIT_NO_LAZY_FETCH=1", "ENV GIT_OPTIONAL_LOCKS=0", "ENV LC_ALL=C"} {
		if !slices.Contains(env, want) {
			t.Errorf("environment lacks %q: %q", want, env)
		}
	}
	for _, kv := range env {
		if strings.Contains(kv, "elsewhere") || strings.Contains(kv, "someone") {
			t.Errorf("the environment of the caller leaked: %q", kv)
		}
	}
}

func TestExecRunnerStopsGitAtTheLimit(t *testing.T) {
	r := runner(t, "out:100")
	if out, err := r.Run(nil, 100); err != nil || len(out) != 100 {
		t.Errorf("at the limit: %d bytes, %v", len(out), err)
	}
	if out, err := r.Run(nil, 99); !errors.Is(err, ErrTooLarge) || out != nil {
		t.Errorf("over the limit: %d bytes, %v", len(out), err)
	}
	r = runner(t, "out:3000000")
	if _, err := r.Run(nil, 1000); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a large output: %v", err)
	}
}

func TestExecRunnerReportsAFailure(t *testing.T) {
	_, err := runner(t, "exit:128").Run(nil, 100)
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 128 || exit.Stderr != "fatal: it failed \x1b[31m" {
		t.Fatalf("err = %#v", err)
	}
	if strings.Contains(err.Error(), "\x1b") || !strings.HasPrefix(err.Error(), "git exited with status 128: fatal: it failed") {
		t.Errorf("message %q", err)
	}
	// What git says on standard error is read up to a bound, and the last line of what was kept is reported.
	_, err = runner(t, "stderr").Run(nil, 100)
	if !errors.As(err, &exit) || exit.Code != 3 || len(exit.Stderr) > 100 || len(exit.Full) != maxSmall || !strings.HasPrefix(exit.Full, "noise\n") {
		t.Errorf("err = %#v", err)
	}
	// A git that prints nothing and succeeds.
	if out, err := runner(t, "none").Run(nil, 100); err != nil || len(out) != 0 {
		t.Errorf("%q, %v", out, err)
	}
}

func TestExecRunnerStopsGitThatTakesTooLong(t *testing.T) {
	r := runner(t, "sleep")
	r.Timeout = 300 * time.Millisecond
	start := time.Now()
	if _, err := r.Run(nil, 100); err == nil || !strings.Contains(err.Error(), "did not finish in 300ms") {
		t.Errorf("err = %v", err)
	}
	if time.Since(start) > 20*time.Second {
		t.Error("the call did not stop")
	}
}

func TestExecRunnerWithoutGit(t *testing.T) {
	_, err := ExecRunner{Git: "/nonexistent/git", Dir: "."}.Run(nil, 100)
	if err == nil || !errors.Is(err, ErrCannotRun) || !strings.Contains(err.Error(), "git could not be run: ") {
		t.Errorf("err = %v", err)
	}
}

func TestGitEnvironmentDropsEveryGitVariable(t *testing.T) {
	env := gitEnv([]string{"PATH=/bin", "GIT_DIR=x", "git_index_file=y", "GITHUB_TOKEN=z", "HOME=/h"})
	if !slices.Equal(env[:3], []string{"PATH=/bin", "GITHUB_TOKEN=z", "HOME=/h"}) {
		t.Errorf("env = %q", env)
	}
}

// A git that has written all it has and does not exit is stopped as soon as its output is over the limit, not when the call times out.
func TestExecRunnerStopsGitAtOnceWhenItsOutputIsOverTheLimit(t *testing.T) {
	r := runner(t, "outsleep:100")
	r.Timeout = 30 * time.Second
	start := time.Now()
	if _, err := r.Run(nil, 10); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v", err)
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("the call took %s: git was left running until the timeout", took)
	}
}
