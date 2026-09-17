package setup

import (
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// ApplyTelemetryChange records a person's telemetry decision in config.yaml
// (telemetry-consent#REQ:opt-in-state): enabling needs ConfirmedByUser and
// creates the install id; disabling always works and removes it. channel is
// the deciding interface. The caller must be the home's single writer, as
// for ApplyConfigChange. changed is false when the state already matched.
func ApplyTelemetryChange(dirs paths.Dirs, change telemetry.Change, channel telemetry.Channel, now time.Time) (consent telemetry.Consent, changed bool, err error) {
	switch change.State {
	case telemetry.StateEnabled:
		if !change.ConfirmedByUser {
			return consent, false, TelemetryConfirmationRequired()
		}
	case telemetry.StateDisabled:
	default:
		return consent, false, envelope.New(envelope.InvalidArgument, uicopy.T("telemetry.failed", nil)).
			WithReason(uicopy.T("telemetry.invalid_state", map[string]string{"state": change.State}))
	}
	config, err := LoadConfig(dirs.Home)
	if err != nil {
		return consent, false, err
	}
	current := config.Telemetry
	if current.EffectiveState() == change.State && (change.State == telemetry.StateDisabled || telemetry.ValidInstallID(current.InstallID)) {
		return current, false, nil
	}
	next := telemetry.Consent{State: change.State, DecidedAt: now.UTC().Format(time.RFC3339), Channel: string(channel)}
	if change.State == telemetry.StateEnabled {
		next.InstallID = telemetry.NewInstallID()
	}
	config.Telemetry = next
	data, err := yaml.Marshal(config)
	if err != nil {
		return consent, false, err
	}
	if err := paths.WriteFilePrivate(filepath.Join(dirs.Home, ConfigFile), data); err != nil {
		return consent, false, err
	}
	return next, true, nil
}

// TelemetryConfirmationRequired is enabling without the person's yes.
func TelemetryConfirmationRequired() *envelope.Error {
	return envelope.New(envelope.ConfirmationRequired, uicopy.T("telemetry.failed", nil)).
		WithReason(uicopy.T("telemetry.enable.confirm_needed", nil)).
		WithNext(envelope.Next{Label: uicopy.T("telemetry.next.ask_person", nil), Command: telemetry.EnableCommand})
}
