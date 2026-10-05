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
// Each scenario crashes a server mid-install, edits what a person might have
// edited since, and then does what the advice shown says, asserting that the
// person ends up unstuck with their own copy intact:
//   - "publish": the folder was replaced and the state is not written
//     (strongo/cli-helpers#45);
//   - "state": the state is written and the journal is not cleaned up.
func TestRealInterruptedInstalls(t *testing.T) {
	crashBin := os.Getenv("OVDB_CRASH_BIN")
	if crashBin == "" {
		t.Skip("OVDB_CRASH_BIN not set: a build of this tree whose skillsync exits at OVDB_CRASH_AT (go build -modfile, see the test)")
	}
	bundled, err := fs.ReadFile(embedded.FS, "openvaultdb/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	mine := string(bundled) + "\nMy own line.\n"
	type scenario struct {
		name  string
		crash string
		// adoptable puts the person's own copy in the folder before the install.
		adoptable bool
		// afterCrash is what the person does to the folder before installing again.
		afterCrash func(t *testing.T, skills, folder string)
		// message is what the library says (and ovdb quotes) when installing again.
		message string
		// record: the state file is made unreadable after the crash too.
		record bool
		// follow does what the advice says.
		follow func(t *testing.T, skills string)
		// ends checks the person is unstuck with their copy intact.
		ends func(t *testing.T, run func(args ...string) (string, int), skills, folder string)
	}
	moveOut := func(t *testing.T, skills string, names ...string) {
		t.Helper()
		keep := t.TempDir()
		for _, name := range names {
			matches, _ := filepath.Glob(filepath.Join(skills, name))
			if len(matches) == 0 {
				t.Fatalf("nothing to move out: %s", name)
			}
			for _, m := range matches {
				if err := os.Rename(m, filepath.Join(keep, filepath.Base(m))); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	journal := func(t *testing.T, skills string) {
		moveOut(t, skills, ".cli-helpers-skills-recovery.json", ".cli-helpers-skills-txn-*")
	}
	edit := func(t *testing.T, skills, folder string) {
		if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte(mine), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scenarios := []scenario{
		{
			name: "lacks prior ownership", crash: "publish", adoptable: true, message: "lacks prior ownership", follow: journal,
			ends: func(t *testing.T, run func(...string) (string, int), skills, folder string) {
				if out, code := run("skills", "install", "openvaultdb", "--harness", "claude", "--yes"); code != 0 {
					t.Errorf("still stuck after the advice (exit %d):\n%s", code, out)
				}
				backups, _ := filepath.Glob(filepath.Join(skills, ".cli-helpers-skills-adopted-backup", "*", "openvaultdb", "SKILL.md"))
				found := false
				for _, b := range backups {
					if kept, _ := os.ReadFile(b); string(kept) == mine {
						found = true
					}
				}
				if !found {
					t.Errorf("the person's copy is in no backup %v", backups)
				}
			},
		},
		{
			name: "committed target differs", crash: "state", afterCrash: edit, message: "committed target openvaultdb differs", follow: journal,
			ends: func(t *testing.T, run func(...string) (string, int), skills, folder string) {
				list, _ := run("skills", "list")
				if !strings.Contains(list, "changed since install") {
					t.Errorf("after the advice the list says:\n%s", list)
				}
				if kept, _ := os.ReadFile(filepath.Join(folder, "SKILL.md")); string(kept) != mine {
					t.Errorf("the person's edit was lost: %q", kept)
				}
				out, code := run("skills", "install", "openvaultdb", "--harness", "claude", "--yes")
				if code != 1 || !strings.Contains(out, "was changed since OVDB installed it") {
					t.Errorf("the ordinary changed-copy refusal is expected (exit %d):\n%s", code, out)
				}
			},
		},
		{
			name: "added target is not transaction content", crash: "publish", afterCrash: edit, message: "openvaultdb", follow: journal,
			ends: func(t *testing.T, run func(...string) (string, int), skills, folder string) {
				if kept, _ := os.ReadFile(filepath.Join(folder, "SKILL.md")); string(kept) != mine {
					t.Errorf("the person's edit was lost: %q", kept)
				}
				if out, code := run("skills", "list"); code != 0 || strings.Contains(out, "interrupted") {
					t.Errorf("list (exit %d):\n%s", code, out)
				}
				if _, code := run("skills", "install", "openvaultdb", "--harness", "claude", "--yes"); code > 1 {
					t.Errorf("install exit %d", code)
				}
			},
		},
		{
			name: "backup changed after capture", crash: "state", adoptable: true, message: "backup openvaultdb changed after capture", follow: journal,
			afterCrash: func(t *testing.T, skills, folder string) {
				matches, _ := filepath.Glob(filepath.Join(skills, ".cli-helpers-skills-txn-*", "backup", "openvaultdb", "SKILL.md"))
				if len(matches) != 1 {
					t.Fatalf("no transaction backup to change: %v", matches)
				}
				if err := os.WriteFile(matches[0], []byte("changed"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			ends: func(t *testing.T, run func(...string) (string, int), skills, folder string) {
				if _, code := run("skills", "list"); code != 0 {
					t.Error("list fails after the advice")
				}
				if out, code := run("skills", "install", "openvaultdb", "--harness", "claude", "--yes"); code != 0 {
					t.Errorf("still stuck after the advice (exit %d):\n%s", code, out)
				}
			},
		},
		{
			// What the advice says: all three, at once.
			name: "journal and an unreadable record: all three out", crash: "state", afterCrash: edit, record: true, message: "parse",
			follow: func(t *testing.T, skills string) {
				moveOut(t, skills, ".cli-helpers-skills-sync.json", ".cli-helpers-skills-recovery.json", ".cli-helpers-skills-txn-*")
			},
			ends: func(t *testing.T, run func(...string) (string, int), skills, folder string) {
				if out, code := run("skills", "install", "openvaultdb", "--harness", "claude", "--yes"); code != 0 {
					t.Errorf("still stuck after the advice (exit %d):\n%s", code, out)
				}
				backups, _ := filepath.Glob(filepath.Join(skills, ".cli-helpers-skills-adopted-backup", "*", "openvaultdb", "SKILL.md"))
				if len(backups) != 1 {
					t.Fatalf("backups = %v", backups)
				}
				if kept, _ := os.ReadFile(backups[0]); string(kept) != mine {
					t.Errorf("the person's edit is not in the backup: %q", kept)
				}
			},
		},
		{
			// Found by running it: moving only the record leaves the journal's own refusal.
			name: "journal and an unreadable record: record first is not enough", crash: "state", afterCrash: edit, record: true, message: "parse",
			follow: func(t *testing.T, skills string) { moveOut(t, skills, ".cli-helpers-skills-sync.json") },
			ends: func(t *testing.T, run func(...string) (string, int), skills, folder string) {
				out, code := run("skills", "install", "openvaultdb", "--harness", "claude", "--yes")
				if code != 1 || !strings.Contains(out, "issues/45") {
					t.Errorf("expected the journal's own advice next (exit %d):\n%s", code, out)
				}
				journal(t, skills)
				if out, code := run("skills", "install", "openvaultdb", "--harness", "claude", "--yes"); code != 0 {
					t.Errorf("still stuck after both steps (exit %d):\n%s", code, out)
				}
			},
		},
		{
			// ...and moving only the journal leaves the record's.
			name: "journal and an unreadable record: journal first is not enough", crash: "state", afterCrash: edit, record: true, message: "parse", follow: journal,
			ends: func(t *testing.T, run func(...string) (string, int), skills, folder string) {
				out, code := run("skills", "install", "openvaultdb", "--harness", "claude", "--yes")
				if code != 1 || !strings.Contains(out, "can't be used") {
					t.Errorf("expected the record's own advice next (exit %d):\n%s", code, out)
				}
				moveOut(t, skills, ".cli-helpers-skills-sync.json")
				if out, code := run("skills", "install", "openvaultdb", "--harness", "claude", "--yes"); code != 0 {
					t.Errorf("still stuck after both steps (exit %d):\n%s", code, out)
				}
			},
		},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "user")
			skills := filepath.Join(home, ".claude", "skills")
			folder := filepath.Join(skills, "openvaultdb")
			if err := os.MkdirAll(skills, 0o755); err != nil {
				t.Fatal(err)
			}
			if sc.adoptable {
				if err := os.MkdirAll(folder, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte(mine), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			env := append(stubbedEnvironment(os.Environ()),
				"HOME="+home, "USERPROFILE="+home, "OVDB_HOME="+filepath.Join(base, "home"), "OVDB_RUNTIME_DIR="+filepath.Join(base, "run"),
				"OVDB_DATA_HOME="+filepath.Join(base, "data"), fmt.Sprintf("OVDB_PORT=%d", porttest.Lease(t)), "OVDB_NON_INTERACTIVE=1", "OVDB_PREVIEW=1")
			exe := func(extra []string, binary string, args ...string) (string, int) {
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
			run := func(args ...string) (string, int) { return exe(nil, ovdbBinPath, args...) }
			t.Cleanup(func() { run("server", "stop", "--json") })
			if out, code := exe([]string{"OVDB_CRASH_AT=" + sc.crash}, crashBin, "server", "start", "--json"); code != 0 {
				t.Fatalf("server start: %d %s", code, out)
			}
			if out, code := run("skills", "install", "openvaultdb", "--harness", "claude", "--yes"); code == 0 {
				t.Fatalf("the install of a server that exits mid-install succeeded:\n%s", out)
			}
			if _, err := os.Stat(filepath.Join(skills, ".cli-helpers-skills-recovery.json")); err != nil {
				t.Fatalf("no journal was left by the crash: %v", err)
			}
			if out, code := run("server", "start", "--json"); code != 0 {
				t.Fatalf("restart: %d %s", code, out)
			}
			if sc.afterCrash != nil {
				sc.afterCrash(t, skills, folder)
			}
			if sc.record {
				if err := os.WriteFile(filepath.Join(skills, ".cli-helpers-skills-sync.json"), []byte(`{"schema":2,"plug`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			list, _ := run("skills", "list")
			if !strings.Contains(list, "interrupted install") {
				t.Errorf("list does not say the install was interrupted:\n%s", list)
			}
			out, code := run("skills", "install", "openvaultdb", "--harness", "claude", "--yes")
			flat := strings.Join(strings.Fields(out), " ")
			t.Logf("install after the crash (exit %d):\n%s", code, out)
			if sc.crash == "state" && sc.afterCrash == nil && !sc.record {
				if code != 0 {
					t.Errorf("the install did not finish the interrupted one (exit %d):\n%s", code, out)
				}
				return
			}
			if code != 1 || !strings.Contains(flat, sc.message) {
				logs, _ := filepath.Glob(filepath.Join(base, "run", "*.log"))
				for _, l := range logs {
					data, _ := os.ReadFile(l)
					t.Logf("server log %s:\n%s", filepath.Base(l), data)
				}
				t.Fatalf("expected exit 1 quoting %q (exit %d):\n%s", sc.message, code, out)
			}
			wantAdvice := "issues/45"
			if sc.record {
				wantAdvice = ".cli-helpers-skills-sync.json"
			}
			if !strings.Contains(flat, wantAdvice) {
				t.Errorf("the advice shown lacks %q:\n%s", wantAdvice, out)
			}
			sc.follow(t, skills)
			sc.ends(t, run, skills, folder)
		})
	}
}
