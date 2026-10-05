package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
	embedded "github.com/openvaultdb/ovdb/skills"
)

// skillsLocal is a client whose home holds, for Claude Code and Codex, a copy of
// the storage skill OVDB could take over, or none.
func skillsLocal(t *testing.T, claudeCopy, codexCopy bool) (*Local, string) {
	t.Helper()
	home := t.TempDir()
	l, _ := testLocal(t)
	l.Getenv = func(name string) string {
		if name == "HOME" || name == "USERPROFILE" {
			return home
		}
		return ""
	}
	bundled, err := embedded.FS.ReadFile("openvaultdb/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for dir, put := range map[string]bool{".claude": claudeCopy, ".codex": codexCopy} {
		if err := os.MkdirAll(filepath.Join(home, dir, "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		if put {
			if err := os.MkdirAll(filepath.Join(home, dir, "skills", "openvaultdb"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, dir, "skills", "openvaultdb", "SKILL.md"), bundled, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	env, _ := l.SkillsEnv()
	return l, env.Home
}

// The request names the folders the plan lists as already there, and nothing
// else: never the blanket "adopt". A client that always sent adopt (or a list
// that is not the plan's) takes over folders the person was not shown.
func TestPlanNamesOnlyTheFoldersThePlanListsAsAlreadyThere(t *testing.T) {
	t.Parallel()
	l, _ := skillsLocal(t, false, false)
	plan, err := l.PlanSkill(skills.InstallRequest{Skill: skills.Storage, Harnesses: []string{"claude", "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Request.Adopt || len(plan.Request.AdoptDirs) != 0 || len(plan.Request.AdoptHarnesses) != 0 {
		t.Errorf("a plan with nothing already there asks for adoption: %+v", plan.Request)
	}

	l, home := skillsLocal(t, true, false)
	plan, err = l.PlanSkill(skills.InstallRequest{Skill: skills.Storage, Harnesses: []string{"claude", "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(skills.Canonical(home), ".claude", "skills", "openvaultdb")}
	if plan.Request.Adopt || !slices.Equal(plan.Request.AdoptDirs, want) {
		t.Errorf("request = %+v, want only %v", plan.Request, want)
	}
}

// A caller that showed a person its own list (the TUI's consent step) sends it,
// even when it is empty: a folder that became adoptable after the screen was
// drawn is not among them, and a fresh plan does not add it.
func TestPlanKeepsTheListACallerShowedAPerson(t *testing.T) {
	t.Parallel()
	l, _ := skillsLocal(t, true, true)
	plan, err := l.PlanSkill(skills.InstallRequest{Skill: skills.Storage, Harnesses: []string{"claude", "codex"}, AdoptDirs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Request.Adopt || len(plan.Request.AdoptDirs) != 0 {
		t.Errorf("request = %+v, want the empty list kept", plan.Request)
	}
	shown := []string{"/shown/openvaultdb"}
	plan, err = l.PlanSkill(skills.InstallRequest{Skill: skills.Storage, Harnesses: []string{"claude"}, AdoptDirs: shown})
	if err != nil || !slices.Equal(plan.Request.AdoptDirs, shown) {
		t.Errorf("request = %+v, %v", plan.Request, err)
	}
}

// A server older than this one refuses the new field "adopt_dirs" as unknown;
// the person is told to restart it, as for "adopt".
func TestAdoptDirsOnAnOlderServerIsVersionMismatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if strings.Contains(string(data), `"adopt_dirs"`) {
			envelope.Write(w, envelope.New(envelope.InvalidArgument, "Bad request").WithReason(`json: unknown field "adopt_dirs"`))
			return
		}
		envelope.WriteJSON(w, http.StatusOK, map[string]any{"schema": 1})
	}))
	defer server.Close()
	port, _ := strconv.Atoi(server.URL[strings.LastIndex(server.URL, ":")+1:])
	l, _ := testLocal(t)
	c := l.newClient(runtime.State{Running: true, Record: &runtime.Record{Port: port}, Secret: "s", Whoami: &runtime.Whoami{Version: "0.28.0"}})
	_, err := l.postInstall(context.Background(), c, skills.InstallRequest{Skill: skills.Storage, AdoptDirs: []string{"/x/openvaultdb"}})
	if e := envelope.As(err); e == nil || e.Code != envelope.ServerVersionMismatch || e.Next[0].Command != "ovdb server restart" {
		t.Errorf("adopt_dirs on an older server = %v", err)
	}
}
