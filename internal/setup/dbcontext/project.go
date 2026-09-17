// Package dbcontext is the one resolver for "which database and path does
// this command act on" (decision 0008,
// spec/features/database-context-navigation REQ:use-sets-scoped-context and
// REQ:context-lookup).
//
// The client side (CLI, TUI) knows its working directory, so it computes the
// project root and the walk-up candidates here and sends them; the server
// stores and reads contexts under <OVDB home>/contexts, keyed by a hash of
// the canonical directory, and applies the same ladder with Resolve.
package dbcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
)

// Canonical is dir as an absolute path with symlinks resolved, so the same
// project reached through a link has one context. A path that cannot be
// resolved (it no longer exists) is only made absolute and clean.
func Canonical(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = filepath.Clean(dir)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs
}

// Lookup is where a command runs: the directories to search for a project
// context, nearest first, and the root `ovdb use` writes to.
type Lookup struct {
	// Dirs runs from the working directory up to the Git working-tree root
	// when inside one, otherwise to the top of the file system.
	Dirs []string
	// Root is the nearest Git working-tree root, or the working directory.
	Root string
}

// Find walks up from cwd (canonicalised). A directory holding `.git` — a
// folder in a main checkout, a file in a linked worktree or submodule — is a
// working-tree root, so every linked worktree is its own project. Git is
// detected from these markers alone; the git binary is not needed.
func Find(cwd string) Lookup {
	dir := Canonical(cwd)
	var dirs []string
	for current := dir; ; {
		dirs = append(dirs, current)
		if isGitRoot(current) {
			return Lookup{Dirs: dirs, Root: current}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return Lookup{Dirs: dirs, Root: dir}
}

func isGitRoot(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// Key is the file name a project directory's context is stored under.
func Key(dir string) string { return key(goruntime.GOOS, dir) }

// key hashes dir. Windows and macOS paths ignore case by default (C:\Work
// and c:\work, ~/Shop and ~/shop are one directory), and os.Getwd returns
// the case as typed, so they are folded first.
func key(goos, dir string) string {
	if goos == "windows" || goos == "darwin" {
		dir = strings.ToLower(dir)
	}
	sum := sha256.Sum256([]byte(dir))
	return hex.EncodeToString(sum[:16])
}
