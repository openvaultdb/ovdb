package telemetry

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Consent states (REQ:opt-in-state). The stored empty state is not_asked.
const (
	StateNotAsked = "not_asked"
	StateEnabled  = "enabled"
	StateDisabled = "disabled"
)

// Channel is the interface an event or a decision came from.
type Channel string

// Channels (REQ:channel-detection).
const (
	ChannelCLI   Channel = "cli"
	ChannelTUI   Channel = "tui"
	ChannelWeb   Channel = "web"
	ChannelAgent Channel = "agent"
)

func (c Channel) valid() bool {
	return c == ChannelCLI || c == ChannelTUI || c == ChannelWeb || c == ChannelAgent
}

// ParseChannel is c when it is a channel, otherwise cli.
func ParseChannel(c string) Channel {
	if channel := Channel(c); channel.valid() {
		return channel
	}
	return ChannelCLI
}

// Consent is the telemetry section of config.yaml.
type Consent struct {
	State     string `yaml:"state,omitempty"`
	DecidedAt string `yaml:"decided_at,omitempty"`
	Channel   string `yaml:"channel,omitempty"`
	// InstallID exists only while enabled: created on enable, removed on
	// disable.
	InstallID string `yaml:"install_id,omitempty"`
}

// EffectiveState is c's state, not_asked when unset or unrecognised.
func (c Consent) EffectiveState() string {
	if c.State == StateEnabled || c.State == StateDisabled {
		return c.State
	}
	return StateNotAsked
}

// LoadConsent reads the telemetry section of <home>/config.yaml; a missing
// file is not_asked.
func LoadConsent(home string) (Consent, error) {
	var file struct {
		Telemetry Consent `yaml:"telemetry"`
	}
	data, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return Consent{}, nil
	}
	if err != nil {
		return Consent{}, err
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		return Consent{}, err
	}
	return file.Telemetry, nil
}

// NewInstallID is a random UUID v4, unrelated to anything on the machine.
func NewInstallID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Reasons telemetry does not send. The forced-off ones name the variable
// that forced it (REQ:sender-process-decides).
const (
	ReasonEnvOVDB      = "OVDB_TELEMETRY"
	ReasonDoNotTrack   = "DO_NOT_TRACK"
	ReasonCI           = "CI"
	ReasonUnavailable  = "unavailable"
	ReasonNotAsked     = StateNotAsked
	ReasonDisabled     = StateDisabled
	ReasonConfigBroken = "config_unreadable"
)

// EnvTelemetry is OVDB's own switch; only "0" means anything (off). No
// variable can turn telemetry on (REQ:enable-requires-a-person).
const EnvTelemetry = "OVDB_TELEMETRY"

// ciFlags are CI variables set to a boolean-like value: any value but
// empty, "0" or "false" counts. The first five are specscore-cli's
// (internal/telemetry/optout.go), which matches only the literal "true";
// OVDB widens that match (review F5) and adds TF_BUILD (Azure Pipelines),
// TRAVIS and APPVEYOR.
var ciFlags = []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "TF_BUILD", "TRAVIS", "APPVEYOR"}

// ciPresence are CI variables holding a URL, version or build number:
// being set at all counts.
var ciPresence = []string{"JENKINS_URL", "TEAMCITY_VERSION", "BITBUCKET_BUILD_NUMBER", "CODEBUILD_BUILD_ID"}

// OptOutVariables lists every variable ForcedOff reads.
func OptOutVariables() []string {
	return append(append([]string{EnvTelemetry, "DO_NOT_TRACK"}, ciFlags...), ciPresence...)
}

// truthy is any value but empty, "0" or "false" (any case).
func truthy(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "0" && !strings.EqualFold(value, "false")
}

// ForcedOff is the forced-off reason in the environment getenv reads, or ""
// (the opt-out precedence of specscore-cli's ResolveOptOut, rungs 2 and 3:
// explicit variables, then CI). DO_NOT_TRACK counts when set to anything
// but "0" or "false" (telemetry-consent States table).
func ForcedOff(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if strings.TrimSpace(getenv(EnvTelemetry)) == "0" {
		return ReasonEnvOVDB
	}
	if truthy(getenv("DO_NOT_TRACK")) {
		return ReasonDoNotTrack
	}
	for _, key := range ciFlags {
		if truthy(getenv(key)) {
			return ReasonCI
		}
	}
	for _, key := range ciPresence {
		if strings.TrimSpace(getenv(key)) != "" {
			return ReasonCI
		}
	}
	return ""
}

// agentMarkers are variables known agent harnesses set in the processes
// they run; agentPrefixes match whole families (CODEX_*).
var (
	agentMarkers  = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CURSOR_AGENT", "GEMINI_CLI", "OPENCODE", "AMP_AGENT", "CLINE_ACTIVE", "COPILOT_AGENT"}
	agentPrefixes = []string{"CODEX_"}
)

// DetectChannel is agent when a known agent harness variable is present,
// otherwise cli (REQ:channel-detection). environ lists KEY=VALUE pairs,
// os.Environ when nil.
func DetectChannel(getenv func(string) string, environ func() []string) Channel {
	if getenv == nil {
		getenv = os.Getenv
	}
	if environ == nil {
		environ = os.Environ
	}
	for _, key := range agentMarkers {
		if getenv(key) != "" {
			return ChannelAgent
		}
	}
	for _, pair := range environ() {
		for _, prefix := range agentPrefixes {
			if strings.HasPrefix(pair, prefix) {
				return ChannelAgent
			}
		}
	}
	return ChannelCLI
}

// Decision is whether this process sends, and why not.
type Decision struct {
	Consent Consent
	State   string
	Sending bool
	Reason  string // "" when Sending
}

// Decide evaluates consent in home for the process whose environment getenv
// reads, with key the build's PostHog key.
func Decide(home string, getenv func(string) string, key string) Decision {
	consent, err := LoadConsent(home)
	d := Decision{Consent: consent, State: consent.EffectiveState()}
	switch {
	case err != nil:
		d.Reason = ReasonConfigBroken
	case ForcedOff(getenv) != "":
		d.Reason = ForcedOff(getenv)
	case d.State == StateNotAsked:
		d.Reason = ReasonNotAsked
	case d.State == StateDisabled:
		d.Reason = ReasonDisabled
	case key == "":
		d.Reason = ReasonUnavailable
	case !ValidInstallID(consent.InstallID):
		// Enabled by hand without an id: nothing to attribute events to.
		d.Reason = ReasonConfigBroken
	default:
		d.Sending = true
	}
	return d
}
