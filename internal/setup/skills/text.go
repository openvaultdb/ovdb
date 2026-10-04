package skills

import (
	uicopy "github.com/openvaultdb/ovdb/copy"
)

// States are the Target states, in the order this package defines them. Every
// one has a "skills.state.<state>" entry in copy/en.json
// (TestEveryStateAndResultHasText).
var States = []string{StateNotInstalled, StateInstalled, StateUpdateAvailable, StateChanged, StateNotOVDB, StateAdoptable}

// ResultText is what an install result reads as in a line: the copy for
// "skills.result.<result>". A result is a skillsync action, which this build
// may not know (a newer library, or a newer server answering an older client);
// that one is shown as it came, not as a panic of uicopy.T.
func ResultText(result string) string {
	if key := "skills.result." + result; uicopy.Has(key) {
		return uicopy.T(key, nil)
	}
	return result
}

// StateText is what a target state reads as: "skills.state.<state>", or the
// state as it came when this build has no text for it.
func StateText(state string) string {
	if key := "skills.state." + state; uicopy.Has(key) {
		return uicopy.T(key, nil)
	}
	return state
}

// ConsentText is the note the consent step shows under an agent whose copy of
// the skill is in this state, or "" when the state needs none.
func ConsentText(state string) string {
	if key := "skills.consent." + state; uicopy.Has(key) {
		return uicopy.T(key, nil)
	}
	return ""
}

// ResultLine is one target of an install as the TUI and web console list it:
// the agent's name and folder, and for an adopted folder where the copy that
// was there is kept.
func ResultLine(outcome Outcome) string {
	if outcome.Result == "adopted" && outcome.BackupPath != "" {
		return uicopy.T("skills.result.adopted_line", map[string]string{"name": outcome.Name, "path": outcome.Dir, "backup": outcome.BackupPath})
	}
	return uicopy.T("skills.result.line", map[string]string{"name": outcome.Name, "path": outcome.Dir})
}
