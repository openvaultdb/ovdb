// Package paths resolves where ovdb keeps its configuration, runtime state
// and data, and creates those directories owner-only — but only the ones it
// creates itself. Every caller receives a Dirs value instead of reading the
// environment, so tests inject temporary directories and run in parallel.
//
// See spec/features/local-server-and-web-console#REQ:owner-only-state and the
// Locations table there. The legacy `ovdb cloud` commands keep resolving
// os.UserConfigDir() on their own.
package paths

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/strongo/cli-helpers/daemonlifecycle"
)

// Environment variables that override the default locations.
const (
	EnvHome       = "OVDB_HOME"
	EnvRuntimeDir = "OVDB_RUNTIME_DIR" // hidden: tests and diagnostics only
	EnvDataHome   = "OVDB_DATA_HOME"
)

// Dirs are the three locations one ovdb installation uses.
type Dirs struct {
	Home    string `json:"home"`    // configuration: config.yaml, databases/, auth.json
	Runtime string `json:"runtime"` // server.json, home.lock, secret, server.log
	Data    string `json:"data"`    // new databases and demos
}

// Getenv looks up an environment variable; os.Getenv satisfies it.
type Getenv func(string) string

// Resolve returns the locations for env, making each absolute. Defaults are
// os.UserConfigDir()/ovdb, os.UserCacheDir()/ovdb/run (LocalAppData on
// Windows, never the roaming profile) and ~/ovdb.
func Resolve(env Getenv) (Dirs, error) {
	home, err := resolve(env(EnvHome), func() (string, error) {
		dir, err := os.UserConfigDir()
		return filepath.Join(dir, "ovdb"), err
	})
	if err != nil {
		return Dirs{}, fmt.Errorf("resolve OVDB home: %w", err)
	}
	runtimeDir, err := resolve(env(EnvRuntimeDir), func() (string, error) {
		dir, err := os.UserCacheDir()
		return filepath.Join(dir, "ovdb", "run"), err
	})
	if err != nil {
		return Dirs{}, fmt.Errorf("resolve OVDB runtime directory: %w", err)
	}
	data, err := resolve(env(EnvDataHome), func() (string, error) {
		dir, err := os.UserHomeDir()
		return filepath.Join(dir, "ovdb"), err
	})
	if err != nil {
		return Dirs{}, fmt.Errorf("resolve OVDB data home: %w", err)
	}
	return Dirs{Home: home, Runtime: runtimeDir, Data: data}, nil
}

func resolve(override string, fallback func() (string, error)) (string, error) {
	path := override
	if path == "" {
		var err error
		if path, err = fallback(); err != nil {
			return "", err
		}
	}
	return filepath.Abs(path)
}

// Env returns the variables that make a child process resolve exactly d.
func (d Dirs) Env() []string {
	return []string{EnvHome + "=" + d.Home, EnvRuntimeDir + "=" + d.Runtime, EnvDataHome + "=" + d.Data}
}

// ErrNotPrivate reports an existing directory other accounts can access.
var ErrNotPrivate = errors.New("directory is accessible to other users")

// EnsurePrivateDir makes dir exist. Every directory it creates on the way
// (dir and any missing parents) is made owner-only and validated; a directory
// that already existed is never changed. If dir already existed and is not
// owner-only it returns an error wrapping ErrNotPrivate — the caller decides
// whether that is a warning (OVDB home) or a refusal (runtime directory).
func EnsurePrivateDir(dir string) error {
	missing, err := missingAncestors(dir)
	if err != nil {
		return err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		created := missing[i]
		if err := os.Mkdir(created, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := daemonlifecycle.ProtectOwnerOnly(created); err != nil {
			return err
		}
		if err := daemonlifecycle.ValidateOwnerOnly(created); err != nil {
			return err
		}
	}
	if len(missing) > 0 {
		return nil
	}
	if err := daemonlifecycle.ValidateOwnerOnly(dir); err != nil {
		return fmt.Errorf("%s: %w (%v)", dir, ErrNotPrivate, err)
	}
	return nil
}

// missingAncestors lists dir and each parent that does not exist yet,
// deepest first.
func missingAncestors(dir string) ([]string, error) {
	var missing []string
	for current := filepath.Clean(dir); ; {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("%s is not a directory", current)
			}
			return missing, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return missing, nil
		}
		current = parent
	}
}

// PrivacyFix is the command that makes dir owner-only, runnable as printed
// in this platform's shell: PowerShell on Windows, a POSIX shell elsewhere.
func PrivacyFix(dir string) string {
	return privacyFix(goruntime.GOOS, dir)
}

func privacyFix(goos, dir string) string {
	if goos == "windows" {
		return "icacls " + QuoteArg(goos, dir) + ` /inheritance:r /grant:r "$($env:USERNAME):(OI)(CI)F"`
	}
	return "chmod 700 " + QuoteArg(goos, dir)
}

// QuoteArg quotes s as one shell argument when it needs quoting: single
// quotes in both PowerShell (” escapes a quote) and POSIX shells ('\”
// does).
func QuoteArg(goos, s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-:") == "" {
		return s
	}
	if goos == "windows" {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// WriteFilePrivate atomically replaces path with data as an owner-only
// file: it writes a temporary file in the same directory, protects and
// validates it, syncs it and renames it over path.
func WriteFilePrivate(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = daemonlifecycle.ProtectOwnerOnlyFile(tmp); err != nil {
		return err
	}
	if err = daemonlifecycle.ValidateOwnerOnlyFile(tmp); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
