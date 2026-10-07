package pinchain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GitRuntime admits one absolute executable before any repository operation.
// Its unexported state prevents callers from supplying an unadmitted PATH name.
type GitRuntime struct {
	path    string
	version string
}

// AdmitGit resolves Git exactly once. Git >=2.45 is required for
// GIT_NO_LAZY_FETCH; an older or unrecognized executable fails closed.
func AdmitGit(ctx context.Context) (*GitRuntime, error) {
	return admitGit(ctx, exec.LookPath, gitCommand)
}

func admitGit(ctx context.Context, lookup func(string) (string, error), run func(context.Context, string, string, ...string) ([]byte, error)) (*GitRuntime, error) {
	path, err := lookup("git")
	if err != nil {
		return nil, fmt.Errorf("git runtime unavailable")
	}
	path, err = absolutePath(path)
	if err != nil {
		return nil, fmt.Errorf("git executable path unavailable")
	}
	output, err := run(ctx, path, "", "version")
	if err != nil {
		return nil, fmt.Errorf("git version unavailable")
	}
	match := gitVersion.FindStringSubmatch(strings.TrimSpace(string(output)))
	if match == nil {
		return nil, fmt.Errorf("git version unrecognized; require >=2.45")
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	if major < 2 || major == 2 && minor < 45 {
		return nil, fmt.Errorf("git runtime too old; require >=2.45")
	}
	return &GitRuntime{path: path, version: strings.TrimSpace(string(output))}, nil
}

var absolutePath = filepath.Abs

var gitVersion = regexp.MustCompile(`^git version ([0-9]{1,3})\.([0-9]{1,3})\.[0-9]+(?:\.[0-9]+|\.windows\.[0-9]+| \(Apple Git-[0-9]+\))?$`)

// Executable and Version describe this admitted runtime, not source permission.
func (g *GitRuntime) Executable() string { return g.path }
func (g *GitRuntime) Version() string    { return g.version }

func (g *GitRuntime) ready() bool { return g != nil && filepath.IsAbs(g.path) && g.version != "" }

// Validate reproduces the fixed baseline using only this admitted executable.
func (g *GitRuntime) Validate(ctx context.Context, repositories map[string]string) (*Receipt, error) {
	if !g.ready() {
		return nil, fmt.Errorf("git runtime not admitted")
	}
	return validateRepositories(ctx, repositories, g.ReadArtifact)
}

// ReadArtifact verifies immutable metadata identity and bytes using local Git.
// It never follows a URL, lazy-fetches an object or grants read permission.
func (g *GitRuntime) ReadArtifact(ctx context.Context, dir string, a Artifact) ([]byte, error) {
	if !g.ready() {
		return nil, fmt.Errorf("git runtime not admitted")
	}
	if !objectID.MatchString(a.Commit) || !objectID.MatchString(a.Blob) || a.Bytes < 1 || a.Bytes > maxMetadataBytes {
		return nil, fmt.Errorf("invalid metadata artifact pin")
	}
	data, err := readGitWithCommand(a, func(args ...string) ([]byte, error) { return gitCommand(ctx, g.path, dir, args...) })
	if err != nil {
		return nil, err
	}
	if len(data) != a.Bytes || digest(data) != a.SHA256 {
		return nil, fmt.Errorf("metadata artifact bytes drift")
	}
	return data, nil
}

func gitCommand(ctx context.Context, executable, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := []string{"--no-replace-objects"}
	if dir != "" {
		command = append(command, "-C", dir)
	}
	cmd := exec.CommandContext(ctx, executable, append(command, args...)...)
	// Caller GIT_* variables must not redirect the selected local repository or
	// override the no-fetch environment. Disable machine-level configuration.
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(value), "GIT_") {
			env = append(env, value)
		}
	}
	cmd.Env = append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	output := &boundedOutput{}
	cmd.Stdout = output
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("local Git object verification failed")
	}
	return output.buffer.Bytes(), nil
}
