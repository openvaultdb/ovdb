package skills

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

func TestTelemetryEvents(t *testing.T) {
	names := func(events []telemetry.Event) string {
		var out []string
		for _, e := range events {
			data, _ := json.Marshal(e.Properties(telemetry.Meta{}))
			out = append(out, string(e.Name())+string(data))
		}
		return strings.Join(out, "\n")
	}
	if got := TelemetryEvents(InstallRequest{Skill: "todo-demo", DryRun: true}, nil, nil); len(got) != 0 {
		t.Errorf("dry run: %s", names(got))
	}
	document := InstallDocument{Skill: "openvaultdb", Outcomes: []Outcome{
		{Target: Target{Harness: "claude", Dir: "/home/ann/.claude/skills/openvaultdb"}, Result: "added"},
		{Target: Target{Harness: "codex"}, Result: "conflict"},
		{Target: Target{Dir: "/home/ann/custom"}, Result: "added"},
	}}
	body, _ := json.Marshal(document)
	got := names(TelemetryEvents(InstallRequest{Skill: "openvaultdb"}, body, nil))
	for _, want := range []string{`"harness":"claude"`, `"harness":"codex"`, `"harness":"other"`, `"success":false`} {
		if !strings.Contains(got, want) {
			t.Errorf("events lack %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/home/ann") {
		t.Errorf("events carry a directory:\n%s", got)
	}
	failed := names(TelemetryEvents(InstallRequest{Skill: "todo-demo", Harnesses: []string{"claude,codex"}}, nil, envelope.New(envelope.Forbidden, "/home/ann")))
	if strings.Count(failed, "skill_installed") != 2 || !strings.Contains(failed, `"error_code":"forbidden"`) || strings.Contains(failed, "/home/ann") {
		t.Errorf("failure events:\n%s", failed)
	}
}
