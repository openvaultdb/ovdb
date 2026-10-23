package skills

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/skillsync"
	"github.com/strongo/cli-helpers/skillsync/cobracmd"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/libguard"
)

// These tests are the guard for the values this package takes from
// github.com/strongo/cli-helpers/skillsync: the actions it reports (added,
// updated, ..., adopted), the reason it gives for a copy that was edited, and
// the AI agents (harnesses) it knows. They exist because uicopy.T panics on a
// missing key and copy_ast_test.go can check only literal keys, so
// "skills.result."+action went unchecked when skillsync v0.26.0 added
// "adopted": `ovdb skills install` crashed on it. A library bump that adds a
// value now fails here, naming what to add, before it can reach a person.

// wantState is the state of a skill for each action skillsync can plan for
// it, and the reason where the action alone doesn't decide. A new action
// skillsync defines has no row here until someone decides what it means.
var wantState = []struct {
	action skillsync.Action
	reason string
	// adoptable is Change.Adoptable: a conflict NoAdopt caused for a folder
	// the library would otherwise adopt.
	adoptable bool
	state     string
}{
	{skillsync.Added, "", false, StateNotInstalled},
	{skillsync.Updated, "", false, StateUpdateAvailable},
	{skillsync.Unchanged, "", false, StateInstalled},
	{skillsync.Adopted, "", false, StateAdoptable},
	{skillsync.Conflict, modifiedTarget, false, StateChanged},
	{skillsync.Conflict, "unmanaged target", true, StateAdoptable},
	{skillsync.Conflict, "unmanaged target", false, StateNotOVDB},
	{skillsync.Conflict, "unmanaged target: openvaultdb/.DS_Store is not part of this skill's bundle", false, StateNotOVDB},
	{skillsync.Removed, "", false, StateNotOVDB},
}

// Every action skillsync defines has text for "skills.result.<action>" and a
// row in wantState that stateFor honours, so the library gaining one fails
// here instead of panicking `ovdb skills install` for a person.
func TestEverySkillsyncActionIsHandled(t *testing.T) {
	actions := libguard.StringConstsOfType(libguard.Source(t, "github.com/strongo/cli-helpers/skillsync"), "Action")
	if len(actions) < 6 {
		t.Fatalf("read %d skillsync actions %v; the guard no longer finds them, update librarySource or stringConstsOfType", len(actions), actions)
	}
	var covered []string
	for _, row := range wantState {
		covered = append(covered, string(row.action))
		if got := stateFor(skillsync.Change{Action: row.action, Reason: row.reason, Adoptable: row.adoptable}); got != row.state {
			t.Errorf("stateFor(%s, %q, adoptable=%v) = %s, want %s", row.action, row.reason, row.adoptable, got, row.state)
		}
	}
	for _, action := range actions {
		key := "skills.result." + action
		if !uicopy.Has(key) {
			t.Errorf("skillsync defines the action %q, which has no text: add %q to copy/en.json (every surface words a result from it)", action, key)
		}
		if !slices.Contains(covered, action) {
			t.Errorf("skillsync defines the action %q, which this package has no state for: decide what stateFor returns for it and add a row to wantState", action)
		}
	}
	for _, action := range covered {
		if !slices.Contains(actions, action) {
			t.Errorf("wantState names the action %q, which skillsync no longer defines: remove it, and its copy and case if nothing else uses it", action)
		}
	}
}

// ovdb tells a skill the person edited from one it can't take over by the
// reason skillsync gives, a string. The library must still say it.
func TestSkillsyncStillReportsAModifiedTarget(t *testing.T) {
	if !libguard.HasStringLiteral(libguard.Source(t, "github.com/strongo/cli-helpers/skillsync"), modifiedTarget) {
		t.Errorf("skillsync no longer has the conflict reason %q: a skill the person edited would be shown as someone else's; update modifiedTarget in skills.go", modifiedTarget)
	}
}

// Every state this package reports has the text each surface shows for it,
// and States lists exactly the State constants.
func TestEveryStateAndResultHasText(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "skills.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value := spec.(*ast.ValueSpec)
			if len(value.Names) == 1 && strings.HasPrefix(value.Names[0].Name, "State") && len(value.Values) == 1 {
				if lit, ok := value.Values[0].(*ast.BasicLit); ok {
					text, _ := strconv.Unquote(lit.Value)
					declared = append(declared, text)
				}
			}
		}
	}
	sort.Strings(declared)
	listed := slices.Clone(States)
	sort.Strings(listed)
	if !slices.Equal(declared, listed) {
		t.Errorf("States = %v, but skills.go declares the states %v: keep them equal", listed, declared)
	}
	for _, state := range States {
		if !uicopy.Has("skills.state." + state) {
			t.Errorf("the state %q has no text: add skills.state.%s to copy/en.json", state, state)
		}
	}
	// A value this build has no text for is shown as it came, never a panic.
	if got := ResultText("not-an-action"); got != "not-an-action" {
		t.Errorf("ResultText(unknown) = %q", got)
	}
	if got := StateText("not-a-state"); got != "not-a-state" {
		t.Errorf("StateText(unknown) = %q", got)
	}
	if got := ConsentText("not-a-state"); got != "" {
		t.Errorf("ConsentText(unknown) = %q", got)
	}
	// A value that spells another entry of the same prefix is still shown as
	// it came: an entry that needs parameters would print its {placeholders},
	// and consent texts are for the states only.
	for _, value := range []string{"line", "question", "install_for"} {
		if got := ResultText(value); got != value {
			t.Errorf("ResultText(%q) = %q, want the value as it came", value, got)
		}
		if got := ConsentText(value); got != "" {
			t.Errorf("ConsentText(%q) = %q", value, got)
		}
	}
	if got := ResultTextFor("adopted", true); got != uicopy.T("skills.adopted.planned", nil) || got == ResultText("adopted") {
		t.Errorf("a dry run says %q for an adopted folder", got)
	}
	if got, want := ResultText(string(skillsync.Adopted)), uicopy.T("skills.result.adopted", nil); got != want {
		t.Errorf("ResultText(adopted) = %q, want %q", got, want)
	}
}

// Every AI agent skillsync knows has the name people see for it; a harness
// added by the library would otherwise be listed by its id.
func TestEveryHarnessHasAName(t *testing.T) {
	var ids []string
	for _, h := range cobracmd.DefaultHarnesses {
		ids = append(ids, h.ID)
		if _, ok := harnessNames[h.ID]; !ok {
			t.Errorf("skillsync knows the AI agent %q, which has no people-facing name: add it to harnessNames in skills.go", h.ID)
		}
	}
	for id := range harnessNames {
		if !slices.Contains(ids, id) {
			t.Errorf("harnessNames names %q, which skillsync no longer knows: remove it", id)
		}
	}
}

// The read path asks the library whether a folder would be adopted
// (Change.Adoptable of a dry run with NoAdopt) and the install path hands it
// the decision (NoAdopt); a library without them would compile ovdb into
// classifying the folder itself again.
func TestSkillsyncStillHasTheAdoptionOptions(t *testing.T) {
	if _, ok := reflect.TypeOf(skillsync.Options{}).FieldByName("NoAdopt"); !ok {
		t.Error("skillsync.Options has no NoAdopt: the install path can no longer leave the adoption decision to the library")
	}
	if _, ok := reflect.TypeOf(skillsync.Change{}).FieldByName("Adoptable"); !ok {
		t.Error("skillsync.Change has no Adoptable: the read path can no longer tell an adoptable folder from another's")
	}
}
