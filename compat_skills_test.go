package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/porttest"
	embedded "github.com/openvaultdb/ovdb/skills"
)

// Older ovdb binaries against this build's server, for the skills install and
// list. It runs only when OVDB_COMPAT_BINS names them ("v0.21.0=/path/ovdb,
// v0.28.0=/path/ovdb", built from those tags), because building old tags needs
// the network and a checkout; CI skips it. The server is started by the built
// binary of this tree and stopped by the test.
//
// What it holds: a client from before adoption (v0.21.0) is answered as it was,
// whatever the folders hold (already_exists, not_ovdb, no "adopted"); a client
// from v0.22.0 to v0.28.x that agreed to the take-over (--yes sends "adopt":
// true) gets it.
func TestOlderClientsAgainstThisServer(t *testing.T) {
	spec := os.Getenv("OVDB_COMPAT_BINS")
	if spec == "" {
		t.Skip("OVDB_COMPAT_BINS not set")
	}
	bundled, err := fs.ReadFile(embedded.FS, "openvaultdb/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range strings.Split(spec, ",") {
		version, bin, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("OVDB_COMPAT_BINS entry %q is not version=path", entry)
		}
		t.Run(version, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "user")
			for dir, files := range map[string]map[string]string{
				".claude": {"openvaultdb/SKILL.md": string(bundled)},
				".codex":  {"openvaultdb/SKILL.md": string(bundled), "openvaultdb/.DS_Store": "x"},
			} {
				for name, text := range files {
					path := filepath.Join(home, dir, "skills", filepath.FromSlash(name))
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			env := append(stubbedEnvironment(os.Environ()),
				"HOME="+home, "USERPROFILE="+home, "OVDB_HOME="+filepath.Join(base, "home"), "OVDB_RUNTIME_DIR="+filepath.Join(base, "run"),
				"OVDB_DATA_HOME="+filepath.Join(base, "data"), fmt.Sprintf("OVDB_PORT=%d", porttest.Lease(t)), "OVDB_NON_INTERACTIVE=1", "OVDB_PREVIEW=1")
			run := func(binary string, args ...string) (string, int) {
				cmd := exec.Command(binary, args...)
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				code := 0
				if exitErr, ok := err.(*exec.ExitError); ok {
					code = exitErr.ExitCode()
				} else if err != nil {
					t.Fatalf("%s %v: %v", binary, args, err)
				}
				return string(out), code
			}
			if out, code := run(ovdbBinPath, "server", "start", "--json"); code != 0 {
				t.Fatalf("server start: %d %s", code, out)
			}
			t.Cleanup(func() { run(ovdbBinPath, "server", "stop", "--json") })

			listText, _ := run(bin, "skills", "list")
			listJSON, _ := run(bin, "skills", "list", "--json")
			installText, textCode := run(bin, "skills", "install", "openvaultdb", "--harness", "claude", "--yes")
			t.Logf("%s skills list:\n%s\n%s skills list --json:\n%s\n%s skills install --yes (exit %d):\n%s", version, listText, version, listJSON, version, textCode, installText)
			if version == "v0.21.0" {
				if textCode != 1 || strings.Contains(installText, "adopted") || strings.Contains(listJSON, "adoptable") {
					t.Errorf("a v0.21.0 client was not answered as before: exit %d\n%s", textCode, installText)
				}
				if kept, _ := os.ReadFile(filepath.Join(home, ".claude", "skills", "openvaultdb", "SKILL.md")); string(kept) != string(bundled) {
					t.Error("the folder changed")
				}
				return
			}
			jsonOut, jsonCode := run(bin, "skills", "install", "openvaultdb", "--harness", "claude", "--yes", "--json")
			t.Logf("%s skills install --yes --json (exit %d):\n%s", version, jsonCode, jsonOut)
			if textCode != 0 || !strings.Contains(installText, "already there, now managed by OVDB") {
				t.Errorf("%s agreed to the take-over and did not get it: exit %d\n%s", version, textCode, installText)
			}
			if jsonCode != 0 || !strings.Contains(jsonOut, "unchanged") && !strings.Contains(jsonOut, "already_up_to_date") {
				t.Errorf("%s second install: exit %d\n%s", version, jsonCode, jsonOut)
			}
		})
	}
}
