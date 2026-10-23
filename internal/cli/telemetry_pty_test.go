//go:build !windows

package cli_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/creack/pty"

	"github.com/openvaultdb/ovdb/internal/cli"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// runWithTerminal runs ovdb with a real pseudo-terminal as stdin that has
// answer already typed into it.
func (e *env) runWithTerminal(answer string, args ...string) result {
	e.t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		e.t.Skipf("no pseudo-terminal: %v", err)
	}
	defer func() { _ = ptmx.Close(); _ = tty.Close() }()
	if _, err := ptmx.WriteString(answer); err != nil {
		e.t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	root := newRoot(e.app)
	root.SetArgs(args)
	root.SetIn(tty)
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	code := 0
	if err := root.ExecuteContext(context.Background()); err != nil {
		cli.Render(err, args, &stdout, &stderr)
		code = 1
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// Review F3: in a terminal a person answers the prompt; under a detected
// agent harness a terminal is not a person, so --confirmed-by-user is
// required even there.
func TestTelemetryEnableAgentWithTerminalNeedsFlag(t *testing.T) {
	e := telemetryEnv(t, "http://127.0.0.1:9")
	e.vars["CLAUDECODE"] = "1"
	agent := e.runWithTerminal("y\n", "telemetry", "enable")
	if agent.code != 1 || e.telemetryStatus().Telemetry.State != telemetry.StateNotAsked {
		t.Fatalf("agent with a terminal enabled telemetry: %+v", agent)
	}

	delete(e.vars, "CLAUDECODE")
	person := e.runWithTerminal("y\n", "telemetry", "enable")
	if status := e.telemetryStatus().Telemetry; person.code != 0 || status.State != telemetry.StateEnabled || status.Channel != "cli" {
		t.Fatalf("person with a terminal: %+v, status %+v", person, status)
	}
}
