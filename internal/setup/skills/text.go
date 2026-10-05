package skills

import (
	"slices"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
)

// States are the Target states, in the order this package defines them. Every
// one has a "skills.state.<state>" entry in copy/en.json
// (TestEveryStateAndResultHasText).
var States = []string{StateNotInstalled, StateInstalled, StateUpdateAvailable, StateChanged, StateNotOVDB, StateAdoptable, StateRecoveryPending, StateRecordUnusable}

// plainText is the copy at key when there is such an entry and it is a plain
// label: one that needs no parameters, so a value that merely spells the name
// of another entry ("line") cannot print one with its {placeholders} showing.
func plainText(key string) (string, bool) {
	if !uicopy.Has(key) {
		return "", false
	}
	text := uicopy.T(key, nil)
	return text, !strings.Contains(text, "{")
}

// ResultText is what an install result reads as in a line: the copy for
// "skills.result.<result>". A result is a skillsync action, which this build
// may not know (a newer library, or a newer server answering an older client);
// that one is shown as it came, not as a panic of uicopy.T.
func ResultText(result string) string {
	return ResultTextFor(result, false)
}

// ResultTextFor is ResultText, worded as a plan for a dry run: an adopted
// folder has not been taken over yet.
func ResultTextFor(result string, dryRun bool) string {
	if dryRun && result == "adopted" {
		return uicopy.T("skills.adopted.planned", nil)
	}
	if text, ok := plainText("skills.result." + result); ok {
		return text
	}
	return result
}

// StateText is what a target state reads as: "skills.state.<state>", or the
// state as it came when this build has no text for it.
func StateText(state string) string {
	if text, ok := plainText("skills.state." + state); ok {
		return text
	}
	return state
}

// ConsentText is the note the consent step shows under an agent whose copy of
// the skill is in this state, or "" when the state needs none (or is not one).
func ConsentText(state string) string {
	if !slices.Contains(States, state) {
		return ""
	}
	if text, ok := plainText("skills.consent." + state); ok {
		return text
	}
	return ""
}

// AdoptedBackupText says where the copy that was already there is kept, or
// in a dry run that one would be.
func AdoptedBackupText(dryRun bool, outcome Outcome) string {
	if dryRun || outcome.BackupPath == "" {
		return uicopy.T("skills.adopted.backup_planned", nil)
	}
	return uicopy.T("skills.adopted.backup", map[string]string{"path": outcome.BackupPath})
}

// ResultLine is one target of an install as the TUI and web console list it:
// the agent's name and folder, and for an adopted folder where the copy that
// was there is kept.
func ResultLine(outcome Outcome) string {
	if outcome.Result == "adopted" && outcome.BackupPath != "" {
		return uicopy.T("skills.adopted.line", map[string]string{"name": outcome.Name, "path": outcome.Dir, "backup": outcome.BackupPath})
	}
	return uicopy.T("skills.result.line", map[string]string{"name": outcome.Name, "path": outcome.Dir})
}
