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

// Installs really interrupted in the middle, with this tree's binary and a
// second build of it whose copy of the skills library (cli-helpers v0.27.0) is
// patched in a scratch copy, used through go build -modfile with a replace
// directive (the module and go.mod stay as they are), to exit at one of its own
// transaction boundaries (its transactionBoundary hook) when OVDB_CRASH_AT names
// it. It runs only when OVDB_CRASH_BIN is that binary; CI skips it. The servers
// are started by the test and stopped by it.
//
//   - "publish": the skill folder was replaced and the state is not written
//     (strongo/cli-helpers#45: every later install is refused);
//   - "state": the state is written and the journal is not cleaned up (the next
//     install finishes it).
func TestRealInterruptedInstalls(t *testing.T) {
	crashBin := os.Getenv("OVDB_CRASH_BIN")
	if crashBin == "" {
		t.Skip("OVDB_CRASH_BIN not set: a build of this tree whose skillsync exits at OVDB_CRASH_AT (go build -modfile, see the test)")
	}
	bundled, err := fs.ReadFile(embedded.FS, "openvaultdb/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []string{"publish", "state"} {
		t.Run(at, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "user")
			folder := filepath.Join(home, ".claude", "skills", "openvaultdb")
			if err := os.MkdirAll(folder, 0o755); err != nil {
				t.Fatal(err)
			}
			mine := string(bundled) + "\nMy own line.\n"
			if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte(mine), 0o644); err != nil {
				t.Fatal(err)
			}
			env := append(stubbedEnvironment(os.Environ()),
				"HOME="+home, "USERPROFILE="+home, "OVDB_HOME="+filepath.Join(base, "home"), "OVDB_RUNTIME_DIR="+filepath.Join(base, "run"),
				"OVDB_DATA_HOME="+filepath.Join(base, "data"), fmt.Sprintf("OVDB_PORT=%d", porttest.Lease(t)), "OVDB_NON_INTERACTIVE=1", "OVDB_PREVIEW=1")
			run := func(extra []string, binary string, args ...string) (string, int) {
				cmd := exec.Command(binary, args...)
				cmd.Env = append(append([]string{}, env...), extra...)
				out, err := cmd.CombinedOutput()
				code := 0
				if exitErr, ok := err.(*exec.ExitError); ok {
					code = exitErr.ExitCode()
				} else if err != nil {
					t.Fatalf("%s %v: %v", binary, args, err)
				}
				return string(out), code
			}
			t.Cleanup(func() { run(nil, ovdbBinPath, "server", "stop", "--json") })
			if out, code := run([]string{"OVDB_CRASH_AT=" + at}, crashBin, "server", "start", "--json"); code != 0 {
				t.Fatalf("server start: %d %s", code, out)
			}
			out, code := run(nil, ovdbBinPath, "skills", "install", "openvaultdb", "--harness", "claude", "--yes")
			t.Logf("the install the server died in (exit %d):\n%s", code, out)
			if code == 0 {
				t.Fatal("the install of a server that exits mid-install succeeded")
			}
			if _, err := os.Stat(filepath.Join(home, ".claude", "skills", ".cli-helpers-skills-recovery.json")); err != nil {
				t.Fatalf("no journal was left by the crash: %v", err)
			}
			if out, code := run(nil, ovdbBinPath, "server", "start", "--json"); code != 0 {
				t.Fatalf("restart: %d %s", code, out)
			}
			list, _ := run(nil, ovdbBinPath, "skills", "list")
			if !strings.Contains(list, "interrupted install") {
				t.Errorf("list does not say the install was interrupted:\n%s", list)
			}
			dry, dryCode := run(nil, ovdbBinPath, "skills", "install", "openvaultdb", "--harness", "claude", "--dry-run")
			if dryCode != 1 || !strings.Contains(dry, "was interrupted") {
				t.Errorf("dry run (exit %d):\n%s", dryCode, dry)
			}
			install, installCode := run(nil, ovdbBinPath, "skills", "install", "openvaultdb", "--harness", "claude", "--yes")
			t.Logf("the install after the crash (exit %d):\n%s", installCode, install)
			flat := strings.Join(strings.Fields(install), " ")
			switch at {
			case "publish":
				backups, _ := filepath.Glob(filepath.Join(home, ".claude", "skills", ".cli-helpers-skills-adopted-backup", "*", "openvaultdb", "SKILL.md"))
				if installCode != 1 || !strings.Contains(flat, "issues/45") || len(backups) != 1 || !strings.Contains(strings.ReplaceAll(flat, " ", ""), filepath.Dir(backups[0])) {
					t.Errorf("stuck install (exit %d, backups %v):\n%s", installCode, backups, install)
				}
				if kept, _ := os.ReadFile(backups[0]); string(kept) != mine {
					t.Errorf("the backup does not hold the person's copy: %q", kept)
				}
			case "state":
				if installCode != 0 {
					t.Errorf("the install did not finish the interrupted one (exit %d):\n%s", installCode, install)
				}
			}
		})
	}
}
