package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/selfupdate"

	"github.com/openvaultdb/ovdb/internal/libguard"
)

// The guards of this file hold ovdb to the values github.com/strongo/cli-helpers
// can produce, for the commands this package builds from it. skillsync's
// actions, and the AI agents it knows, are guarded in internal/setup/skills.

// Every failure kind selfupdate defines maps to ovdb's two-code contract
// (exit 1, the command's own prefix) in all three commands. ovdb's mappers
// switch on kinds only to name the ones cli-install added; every other kind,
// including one a later release adds, takes the default branch. This test
// lists the kinds from the library's source and runs each through the
// mappers, so a kind that ovdb should treat differently (a new exit code, say)
// is a decision made here and not an accident of the default branch.
func TestEveryFailureKindMapsToTheTwoCodeContract(t *testing.T) {
	names := libguard.ConstNamesWithPrefix(libguard.Source(t, "github.com/strongo/cli-helpers/selfupdate"), "Kind")
	if len(names) < 15 {
		t.Fatalf("read %d selfupdate failure kinds %v; the guard no longer finds them, update libguard.ConstNamesWithPrefix's use here", len(names), names)
	}
	mappers := []struct {
		command string
		fail    func(error) error
	}{
		{"install", installErrors{}.Failure},
		{"upgrade", upgradeErrors{}.Failure},
		{"self-update", selfUpdateErrors{}.Failure},
	}
	for i, name := range names {
		kind := selfupdate.FailureKind(i)
		if kind.String() == "unknown" {
			t.Errorf("selfupdate defines %s but FailureKind(%d).String() is unknown: the kinds are no longer a contiguous iota list, so this guard cannot enumerate them", name, i)
			continue
		}
		failure := &selfupdate.Failure{Kind: kind, Err: errors.New("it failed")}
		for _, mapper := range mappers {
			err := mapper.fail(failure)
			if err == nil || !strings.HasPrefix(err.Error(), mapper.command+": ") || commandExitCode(err) != 1 {
				t.Errorf("%s: kind %s (%s) maps to %v with exit %d, want a %q-prefixed error and exit 1", mapper.command, name, kind, err, commandExitCode(err), mapper.command)
			}
		}
	}
}

// ovdb install is documented, in its help and in the README, as listing the
// fleet CLIs relevant to OpenVaultDB: ingitdb and datatug. That list is the
// library's host-to-target matrix, so a release of the library that makes
// another CLI relevant to ovdb changes what `ovdb install` prints with no
// change in ovdb; this fails when it does, to update those words.
func TestInstallRelevantListMatchesTheDocumentation(t *testing.T) {
	out, err := runInstall(t, "--format", "json")
	var document struct {
		Targets []struct {
			Name string `json:"name"`
		} `json:"targets"`
	}
	if err != nil || json.Unmarshal([]byte(out), &document) != nil {
		t.Fatalf("install --format json = %q, %v", out, err)
	}
	var relevant []string
	for _, target := range document.Targets {
		relevant = append(relevant, target.Name)
	}
	if got, want := strings.Join(relevant, ","), "ingitdb,datatug"; got != want {
		t.Errorf("the CLIs relevant to ovdb are %s, but the README (Installing related CLIs) names %s: update the README and this test", got, want)
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range relevant {
		if !bytes.Contains(readme, []byte("`"+name+"`")) {
			t.Errorf("the README does not name the relevant CLI %s", name)
		}
	}
}
