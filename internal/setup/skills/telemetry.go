package skills

import (
	"encoding/json"
	"strings"

	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// TelemetryEvents are the skill_installed events for one install request
// and its result (telemetry-consent#REQ:closed-event-set): one per target,
// with the harness id only, never a directory. A dry run reports nothing; a
// failure reports each requested harness as unsuccessful plus
// onboarding_error.
func TelemetryEvents(request InstallRequest, body []byte, err error) []telemetry.Event {
	if request.DryRun {
		return nil
	}
	if err != nil {
		var events []telemetry.Event
		for _, harness := range requestedHarnesses(request) {
			events = append(events, telemetry.NewSkillInstalled(request.Skill, harness, false))
		}
		return append(events, telemetry.NewOnboardingError("skills", err))
	}
	var document InstallDocument
	if json.Unmarshal(body, &document) != nil {
		return nil
	}
	events := make([]telemetry.Event, 0, len(document.Outcomes))
	for _, outcome := range document.Outcomes {
		events = append(events, telemetry.NewSkillInstalled(document.Skill, outcome.Harness, outcome.Result != "conflict"))
	}
	return events
}

func requestedHarnesses(request InstallRequest) []string {
	var out []string
	for _, target := range request.Targets {
		out = append(out, target.Harness)
	}
	for _, raw := range request.Harnesses {
		for _, name := range strings.Split(raw, ",") {
			out = append(out, strings.TrimSpace(name))
		}
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}
