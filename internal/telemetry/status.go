package telemetry

import (
	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
)

// Status is the shared telemetry state every interface shows
// (REQ:parity-of-controls).
type Status struct {
	State string `json:"state"` // not_asked, enabled, disabled
	// Sending is whether the process that answered sends events now.
	Sending bool `json:"sending"`
	// Reason is why it does not send: a forced-off variable, unavailable,
	// not_asked or disabled.
	Reason     string `json:"reason,omitempty"`
	ReasonText string `json:"reason_text,omitempty"`
	// Available is false in a build without a PostHog key.
	Available      bool     `json:"available"`
	Provider       string   `json:"provider"`
	DecidedAt      string   `json:"decided_at,omitempty"`
	Channel        string   `json:"channel,omitempty"`
	HasInstallID   bool     `json:"has_install_id"`
	Collected      []string `json:"collected"`
	NeverCollected []string `json:"never_collected"`
}

// Document is the body of GET/PUT /api/local/v1/telemetry and the --json
// output of `ovdb telemetry status|enable|disable`.
type Document struct {
	Schema    int             `json:"schema"`
	Telemetry Status          `json:"telemetry"`
	Changed   *bool           `json:"changed,omitempty"`
	Next      []envelope.Next `json:"next"`
}

// Change is the body of PUT /api/local/v1/telemetry.
type Change struct {
	State string `json:"state"` // enabled or disabled
	// ConfirmedByUser must be true to enable: the person said yes
	// (REQ:enable-requires-a-person).
	ConfirmedByUser bool `json:"confirmed_by_user,omitempty"`
	// Channel is the deciding interface when this process writes
	// config.yaml itself (no server running). It is never sent: the server
	// derives it from the credential.
	Channel string `json:"-"`
}

// Collected is what is collected, as every interface lists it.
func Collected() []string {
	return []string{uicopy.T("telemetry.collected.steps", nil), uicopy.T("telemetry.collected.storage", nil), uicopy.T("telemetry.collected.version", nil),
		uicopy.T("telemetry.collected.network", nil)}
}

// NeverCollected is what is never collected.
func NeverCollected() []string {
	return []string{uicopy.T("telemetry.never.data", nil), uicopy.T("telemetry.never.queries", nil), uicopy.T("telemetry.never.typed", nil)}
}

// ReasonText is the sentence for reason, "" when it needs none.
func ReasonText(reason string) string {
	switch reason {
	case ReasonEnvOVDB:
		return uicopy.T("telemetry.reason.ovdb_telemetry", nil)
	case ReasonDoNotTrack:
		return uicopy.T("telemetry.reason.do_not_track", nil)
	case ReasonCI:
		return uicopy.T("telemetry.reason.ci", nil)
	case ReasonUnavailable:
		return uicopy.T("telemetry.unavailable", nil)
	case ReasonConfigBroken:
		return uicopy.T("telemetry.reason.config_unreadable", nil)
	}
	return ""
}

// EnableCommand is the command an agent runs only after the person agreed.
const EnableCommand = "ovdb telemetry enable --confirmed-by-user"

// NewDocument describes d for a process whose build has key.
func NewDocument(d Decision, available bool) Document {
	status := Status{
		State: d.State, Sending: d.Sending, Reason: d.Reason, ReasonText: ReasonText(d.Reason),
		Available: available, Provider: uicopy.T("telemetry.provider", nil),
		DecidedAt: d.Consent.DecidedAt, Channel: d.Consent.Channel, HasInstallID: d.Consent.InstallID != "",
		Collected: Collected(), NeverCollected: NeverCollected(),
	}
	if !available && status.ReasonText == "" {
		// Every interface says so, whatever the state.
		status.ReasonText = uicopy.T("telemetry.unavailable", nil)
	}
	next := []envelope.Next{}
	switch d.State {
	case StateEnabled:
		next = append(next, envelope.Next{Label: uicopy.T("telemetry.next.disable", nil), Command: "ovdb telemetry disable"})
	default:
		// Agents read next: turning it on is the person's answer to relay,
		// never an inference (decision 0009, point 7).
		next = append(next, envelope.Next{Label: uicopy.T("telemetry.next.ask_person", nil), Command: EnableCommand})
		if d.State == StateNotAsked {
			next = append(next, envelope.Next{Label: uicopy.T("telemetry.next.disable", nil), Command: "ovdb telemetry disable"})
		}
	}
	return Document{Schema: envelope.Schema, Telemetry: status, Next: next}
}
