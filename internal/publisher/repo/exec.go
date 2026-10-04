package repo

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ExitError is a git that ran and failed.
type ExitError struct {
	Code   int
	Stderr string // the last line git printed on standard error
	Full   string // what git printed on standard error, up to 4096 bytes: to tell why it failed, never to print
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("git exited with status %d: %s", e.Code, ascii(e.Stderr))
}

// ExecRunner runs the git program in the repository at Dir.
type ExecRunner struct {
	Git     string        // the program; "git" when empty
	Dir     string        // the directory given to git -C
	Timeout time.Duration // how long one call may take; 30 seconds when zero
}

// gitEnv is the environment of every call: the caller's, without any GIT_ variable (they
// can point git at another repository), with no configuration of the machine and no
// prompt, no lazy fetch of a missing object from another repository, and no optional lock.
func gitEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+6)
	for _, kv := range environ {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
}

// bounded collects at most limit bytes. Past it, a writer that is not quiet fails and
// stops the process; a quiet one drops the rest.
type bounded struct {
	buf    []byte
	limit  int
	quiet  bool
	cancel context.CancelFunc
	over   bool
}

func (b *bounded) Write(p []byte) (int, error) {
	room := b.limit - len(b.buf)
	if len(p) <= room {
		b.buf = append(b.buf, p...)
		return len(p), nil
	}
	if b.quiet {
		b.buf = append(b.buf, p[:room]...)
		return len(p), nil
	}
	b.over = true
	b.cancel()
	return 0, ErrTooLarge
}

// Run runs git with the arguments.
func (r ExecRunner) Run(args []string, limit int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmp.Or(r.Timeout, 30*time.Second))
	defer cancel()
	cmd := exec.CommandContext(ctx, cmp.Or(r.Git, "git"), append([]string{"-C", r.Dir}, args...)...)
	cmd.Env = gitEnv(os.Environ())
	out := &bounded{limit: limit, cancel: cancel}
	stderr := &bounded{limit: maxSmall, quiet: true}
	cmd.Stdout, cmd.Stderr = out, stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case out.over:
		return nil, ErrTooLarge
	case ctx.Err() != nil:
		return nil, fmt.Errorf("%w: git did not finish in %s", ErrCannotRun, cmp.Or(r.Timeout, 30*time.Second))
	case errors.As(err, &exit):
		lines := strings.Split(strings.TrimSpace(string(stderr.buf)), "\n")
		return nil, &ExitError{Code: exit.ExitCode(), Stderr: lines[len(lines)-1], Full: string(stderr.buf)}
	case err != nil:
		return nil, fmt.Errorf("%w: %s", ErrCannotRun, ascii(err.Error()))
	}
	return out.buf, nil
}
