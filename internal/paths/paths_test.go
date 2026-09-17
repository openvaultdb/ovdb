package paths

import (
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/daemonlifecycle"
)

func envOf(values map[string]string) Getenv {
	return func(key string) string { return values[key] }
}

func TestResolveOverrides(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dirs, err := Resolve(envOf(map[string]string{
		EnvHome:       filepath.Join(base, "home"),
		EnvRuntimeDir: filepath.Join(base, "run"),
		EnvDataHome:   filepath.Join(base, "data"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Dirs{Home: filepath.Join(base, "home"), Runtime: filepath.Join(base, "run"), Data: filepath.Join(base, "data")}
	if dirs != want {
		t.Errorf("Resolve = %+v, want %+v", dirs, want)
	}
	env := strings.Join(dirs.Env(), "\n")
	for _, entry := range []string{EnvHome + "=" + want.Home, EnvRuntimeDir + "=" + want.Runtime, EnvDataHome + "=" + want.Data} {
		if !strings.Contains(env, entry) {
			t.Errorf("Env() lacks %q", entry)
		}
	}
}

func TestResolveDefaults(t *testing.T) {
	t.Parallel()
	dirs, err := Resolve(envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	config, _ := os.UserConfigDir()
	cache, _ := os.UserCacheDir()
	userHome, _ := os.UserHomeDir()
	if dirs.Home != filepath.Join(config, "ovdb") {
		t.Errorf("Home = %s", dirs.Home)
	}
	// AC:runtime-files-private-and-local: runtime state is under the user
	// cache directory, which on Windows is LocalAppData, not the roaming one.
	if dirs.Runtime != filepath.Join(cache, "ovdb", "run") {
		t.Errorf("Runtime = %s", dirs.Runtime)
	}
	if goruntime.GOOS == "windows" {
		if local := os.Getenv("LocalAppData"); local != "" && !strings.HasPrefix(dirs.Runtime, local) {
			t.Errorf("Runtime %s is not under LocalAppData %s", dirs.Runtime, local)
		}
	}
	if dirs.Data != filepath.Join(userHome, "ovdb") {
		t.Errorf("Data = %s", dirs.Data)
	}
}

func TestResolveMakesRelativeAbsolute(t *testing.T) {
	t.Parallel()
	dirs, err := Resolve(envOf(map[string]string{EnvHome: "rel", EnvRuntimeDir: "run", EnvDataHome: "data"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{dirs.Home, dirs.Runtime, dirs.Data} {
		if !filepath.IsAbs(dir) {
			t.Errorf("%s is not absolute", dir)
		}
	}
}

func TestEnsurePrivateDirCreatesOwnerOnlyChain(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dir := filepath.Join(base, "a", "b", "run")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	for _, created := range []string{filepath.Join(base, "a"), filepath.Join(base, "a", "b"), dir} {
		if err := daemonlifecycle.ValidateOwnerOnly(created); err != nil {
			t.Errorf("%s: %v", created, err)
		}
	}
	// Idempotent once private.
	if err := EnsurePrivateDir(dir); err != nil {
		t.Errorf("second EnsurePrivateDir: %v", err)
	}
}

// AC:existing-dirs-not-chmodded (the paths half): an existing 0755
// directory is reported, never changed.
func TestEnsurePrivateDirLeavesExistingDirAlone(t *testing.T) {
	t.Parallel()
	if goruntime.GOOS == "windows" {
		t.Skip("POSIX modes; Windows ACL validation is covered by daemonlifecycle")
	}
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := EnsurePrivateDir(dir)
	if !errors.Is(err, ErrNotPrivate) {
		t.Fatalf("EnsurePrivateDir = %v, want ErrNotPrivate", err)
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode changed to %o", info.Mode().Perm())
	}
	if fix := PrivacyFix(dir); fix != "chmod 700 "+dir {
		t.Errorf("PrivacyFix = %q", fix)
	}
}

func TestEnsurePrivateDirRejectsFile(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(file); err == nil {
		t.Error("EnsurePrivateDir on a file succeeded")
	}
}

func TestWriteFilePrivate(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "run")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "secret")
	for _, content := range []string{"first", "second"} {
		if err := WriteFilePrivate(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != content {
			t.Fatalf("read back %q, %v; want %q", got, err, content)
		}
		if err := daemonlifecycle.ValidateOwnerOnly(path); err != nil {
			t.Errorf("owner-only: %v", err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
	if err := WriteFilePrivate(filepath.Join(dir, "missing", "x"), nil); err == nil {
		t.Error("write into a missing directory succeeded")
	}
}
