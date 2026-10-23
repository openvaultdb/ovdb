package repo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
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
	case "holder", "holder-exits":
		// A git that starts a process that holds git's output (it inherits both pipes), as a wrapper script that does not exec its sleeper does; then sleeps,
		// or exits at once.
		holder := exec.Command(os.Args[0])
		holder.Env = append(os.Environ(), "OVDB_FAKE_GIT=heartbeat")
		holder.Stdout, holder.Stderr = os.Stdout, os.Stderr
		if err := holder.Start(); err != nil {
			os.Exit(4)
		}
		if mode == "holder" {
			time.Sleep(time.Minute)
		}
	case "heartbeat":
		// The holder: it records its pid, then touches a file every 20 ms (so a test can tell that it is alive), and ends by itself after 30 seconds.
		_ = os.WriteFile(os.Getenv("OVDB_FAKE_HOLDER")+".pid", []byte(strconv.Itoa(os.Getpid())), 0o600)
		for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			_ = os.WriteFile(os.Getenv("OVDB_FAKE_HOLDER")+".beat", []byte(time.Now().String()), 0o600)
		}
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
	if _, err := r.Run(nil, 100); err == nil || !strings.Contains(err.Error(), "did not finish in 300ms") || !errors.Is(err, ErrCannotRun) || !errors.Is(err, ErrTimeout) || errors.Is(err, ErrOldGit) {
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

// holderOf is the fake git that leaves a holder behind, and a way to stop and check it: the test ends only when the holder is dead, as proved by its heartbeat
// no longer beating.
func holderOf(t *testing.T, mode string) ExecRunner {
	t.Helper()
	base := filepath.Join(t.TempDir(), "holder")
	t.Setenv("OVDB_FAKE_HOLDER", base)
	t.Cleanup(func() {
		raw, err := os.ReadFile(base + ".pid")
		if err != nil {
			t.Errorf("the holder never started: %v", err)
			return
		}
		pid, _ := strconv.Atoi(string(raw))
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
		beat := func() string { b, _ := os.ReadFile(base + ".beat"); return string(b) }
		time.Sleep(200 * time.Millisecond) // a write that was under way
		before := beat()
		time.Sleep(400 * time.Millisecond)
		if beat() != before {
			t.Errorf("the holder (pid %d) is still alive after the test", pid)
		}
	})
	r := runner(t, mode)
	r.WaitDelay = 300 * time.Millisecond
	return r
}

// runWithin runs the call and fails the test, instead of waiting, when it does not return in limit: a runner that waits for a holder of its pipes waits for
// as long as the holder lives.
func runWithin(t *testing.T, r ExecRunner, limit time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { _, err := r.Run(nil, 100); done <- err }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("the call did not return in %s: it waits for a process that holds git's output", limit)
		return nil
	}
}

// The bound of the timeout is a bound: a git that is killed at the timeout and has left a process that holds its output does not hold the call.
func TestExecRunnerTimeoutHoldsWhenGitLeavesAProcessHoldingItsOutput(t *testing.T) {
	r := holderOf(t, "holder")
	r.Timeout = 500 * time.Millisecond
	start := time.Now()
	if err := runWithin(t, r, 10*time.Second); !errors.Is(err, ErrTimeout) {
		t.Errorf("err = %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the call took %s for a timeout of 500ms", took)
	}
}

// A git that exits and leaves such a process is an answer that cannot be trusted to be whole: the call fails at once, as could-not-run.
func TestExecRunnerRefusesGitThatLeavesAProcessHoldingItsOutput(t *testing.T) {
	r := holderOf(t, "holder-exits")
	err := runWithin(t, r, 10*time.Second)
	if !errors.Is(err, ErrCannotRun) || errors.Is(err, ErrTimeout) || !strings.Contains(err.Error(), "left a process running") {
		t.Errorf("err = %v", err)
	}
}
