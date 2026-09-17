package setup

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// TelemetryOutcome is a recorded telemetry decision.
type TelemetryOutcome struct {
	Consent telemetry.Consent
	Changed bool // false when the state already matched
	// Backup is where an unreadable config.yaml was saved before a disable
	// rewrote it.
	Backup string
}

// ApplyTelemetryChange is ChangeTelemetry without the backup path.
func ApplyTelemetryChange(dirs paths.Dirs, change telemetry.Change, channel telemetry.Channel, now time.Time) (telemetry.Consent, bool, error) {
	outcome, err := ChangeTelemetry(dirs, change, channel, now)
	return outcome.Consent, outcome.Changed, err
}

// ChangeTelemetry records a person's telemetry decision in config.yaml
// (telemetry-consent#REQ:opt-in-state): enabling needs ConfirmedByUser and
// creates the install id; disabling always works and removes it. channel is
// the deciding interface. The caller must be the home's single writer, as
// for ApplyConfigChange.
//
// Disabling never fails on an unreadable config.yaml (review F7): the file
// is copied, byte for byte, next to itself first; then only its telemetry
// key is replaced when it is still a YAML mapping, or it is rewritten with
// just the disabled state when it is not YAML at all.
func ChangeTelemetry(dirs paths.Dirs, change telemetry.Change, channel telemetry.Channel, now time.Time) (TelemetryOutcome, error) {
	switch change.State {
	case telemetry.StateEnabled:
		if !change.ConfirmedByUser {
			// Local API callers read JSON: agent-directed.
			return TelemetryOutcome{}, TelemetryConfirmationRequired(true)
		}
	case telemetry.StateDisabled:
	default:
		return TelemetryOutcome{}, envelope.New(envelope.InvalidArgument, uicopy.T("telemetry.failed", nil)).
			WithReason(uicopy.T("telemetry.invalid_state", map[string]string{"state": change.State}))
	}
	path := filepath.Join(dirs.Home, ConfigFile)
	next := telemetry.Consent{State: change.State, DecidedAt: now.UTC().Format(time.RFC3339), Channel: string(channel)}
	config, err := LoadConfig(dirs.Home)
	if err != nil {
		if change.State != telemetry.StateDisabled {
			return TelemetryOutcome{}, err
		}
		return disableUnreadable(path, next, now)
	}
	current := config.Telemetry
	if current.EffectiveState() == change.State && (change.State == telemetry.StateDisabled || telemetry.ValidInstallID(current.InstallID)) {
		return TelemetryOutcome{Consent: current}, nil
	}
	if change.State == telemetry.StateEnabled {
		next.InstallID = telemetry.NewInstallID()
	}
	config.Telemetry = next
	data, err := yaml.Marshal(config)
	if err != nil {
		return TelemetryOutcome{}, err
	}
	if err := paths.WriteFilePrivate(path, data); err != nil {
		return TelemetryOutcome{}, err
	}
	return TelemetryOutcome{Consent: next, Changed: true}, nil
}

// disableUnreadable backs up an unreadable config.yaml and writes disabled
// into it, keeping whatever YAML structure is still there.
func disableUnreadable(path string, disabled telemetry.Consent, now time.Time) (TelemetryOutcome, error) {
	original, err := os.ReadFile(path)
	if err != nil {
		return TelemetryOutcome{}, err
	}
	backup := path + ".unreadable-" + now.UTC().Format("20060102T150405Z")
	if err := paths.WriteFilePrivate(backup, original); err != nil {
		return TelemetryOutcome{}, err
	}
	var consentNode yaml.Node
	if err := consentNode.Encode(disabled); err != nil {
		return TelemetryOutcome{}, err
	}
	var document yaml.Node
	data, _ := yaml.Marshal(map[string]telemetry.Consent{"telemetry": disabled})
	if yaml.Unmarshal(original, &document) == nil && len(document.Content) == 1 && document.Content[0].Kind == yaml.MappingNode {
		root := document.Content[0]
		replaced := false
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "telemetry" {
				root.Content[i+1] = &consentNode
				replaced = true
			}
		}
		if !replaced {
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "telemetry"}, &consentNode)
		}
		if kept, err := yaml.Marshal(&document); err == nil {
			data = kept
		}
	}
	if err := paths.WriteFilePrivate(path, data); err != nil {
		return TelemetryOutcome{}, err
	}
	return TelemetryOutcome{Consent: disabled, Changed: true, Backup: backup}, nil
}

// TelemetryConfirmationRequired is enabling without the person's yes. Only
// the agent-directed (JSON) form names the relay flag, and says it may be
// passed only after the person said yes.
func TelemetryConfirmationRequired(forAgents bool) *envelope.Error {
	reason := uicopy.T("telemetry.enable.confirm_needed", nil)
	if forAgents {
		reason += " " + telemetry.AgentGuidance()
	}
	return envelope.New(envelope.ConfirmationRequired, uicopy.T("telemetry.failed", nil)).
		WithReason(reason).
		WithNext(envelope.Next{Label: uicopy.T("telemetry.next.enable", nil), Command: telemetry.EnableCommand})
}
