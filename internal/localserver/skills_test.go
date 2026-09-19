package localserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
)

// ai-agent-skills through the local API (capabilities 20 and 21).
// AC:web-cannot-target-arbitrary-dir: a console session can install only into
// harnesses the server found, never name a directory; the CLI's resolved
// targets must match a harness layout or lie under the home.
func TestSkillsEndpoints(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	session := f.signIn(t)
	post := func(body, cookie, bearer string) *postResponse {
		r := request{method: http.MethodPost, path: "/api/local/v1/skills/install", body: body, bearer: bearer}
		if cookie != "" {
			r.cookie, r.header = cookie, sameOrigin(testHost)
		}
		rec := f.do(t, r)
		return &postResponse{code: rec.Code, body: rec.Body.Bytes()}
	}
	if err := os.MkdirAll(filepath.Join(f.userHome, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}

	rec := f.do(t, request{path: "/api/local/v1/skills", cookie: session})
	var document skills.Document
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET skills = %d %s", rec.Code, rec.Body)
	}
	canonicalHome := skills.Canonical(f.userHome)
	claudeSkills := filepath.Join(canonicalHome, ".claude", "skills")
	codexSkills := filepath.Join(canonicalHome, ".codex", "skills")
	wantTargets := []skills.Target{
		{Harness: "claude", Name: "Claude Code", SkillsDir: claudeSkills, Dir: filepath.Join(claudeSkills, "openvaultdb-todo-demo"), Detected: true, State: skills.StateNotInstalled},
		{Harness: "codex", Name: "Codex", SkillsDir: codexSkills, Dir: filepath.Join(codexSkills, "openvaultdb-todo-demo"), State: skills.StateNotInstalled},
	}
	if got := document.Skills[1].Targets; !slices.Equal(got, wantTargets) {
		t.Errorf("targets = %+v", got)
	}

	outside := filepath.Join(t.TempDir(), "x")
	for name, body := range map[string]string{
		"dir field":         `{"skill":"todo-demo","dir":"` + filepath.ToSlash(outside) + `"}`,
		"targets":           `{"skill":"todo-demo","targets":[{"harness":"claude","skills_dir":"` + filepath.ToSlash(outside) + `"}]}`,
		"harness not found": `{"skill":"todo-demo","harnesses":["codex"]}`,
		"unknown skill":     `{"skill":"nope","harnesses":["claude"]}`,
		// encoding/json matches field names in any case (review F1).
		"Targets":       `{"skill":"openvaultdb","Targets":[{"harness":"claude","skills_dir":"` + filepath.ToSlash(outside) + `/skills"}]}`,
		"TARGETS":       `{"skill":"todo-demo","TARGETS":[{"skills_dir":"` + filepath.ToSlash(outside) + `"}]}`,
		"null targets":  `{"skill":"todo-demo","targets":null,"Targets":[{"skills_dir":"` + filepath.ToSlash(outside) + `"}]}`,
		"Dir":           `{"skill":"todo-demo","harnesses":["claude"],"Dir":"` + filepath.ToSlash(outside) + `"}`,
		"skills_dir":    `{"skill":"todo-demo","harnesses":["claude"],"Skills_Dir":"` + filepath.ToSlash(outside) + `"}`,
		"unknown field": `{"skill":"todo-demo","harnesses":["claude"],"home":"` + filepath.ToSlash(outside) + `"}`,
	} {
		response := post(body, session, "")
		if response.code != http.StatusBadRequest || envelope.Decode(response.body) == nil || envelope.Decode(response.body).Code != envelope.InvalidArgument {
			t.Errorf("session %s = %d %s", name, response.code, response.body)
		}
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Errorf("a refused request wrote %s", outside)
	}
	if _, err := os.Stat(filepath.Join(f.userHome, ".codex")); !os.IsNotExist(err) {
		t.Error("a refused request wrote Codex's folder")
	}

	response := post(`{"skill":"todo-demo","harnesses":["claude"]}`, session, "")
	var installed skills.InstallDocument
	if err := json.Unmarshal(response.body, &installed); err != nil || response.code != http.StatusCreated || installed.Outcomes[0].Result != "added" {
		t.Fatalf("session install = %d %s", response.code, response.body)
	}
	if _, err := os.Stat(filepath.Join(f.userHome, ".claude", "skills", "openvaultdb-todo-demo", "SKILL.md")); err != nil {
		t.Error(err)
	}
	if again := post(`{"skill":"todo-demo","harnesses":["claude"]}`, session, ""); again.code != http.StatusOK || !strings.Contains(string(again.body), `"already_up_to_date":true`) {
		t.Errorf("second install = %d %s", again.code, again.body)
	}
	status := f.do(t, request{path: "/api/local/v1/status", cookie: session})
	if !strings.Contains(status.Body.String(), `"skills":[{"id":"openvaultdb","installed_for":[],"update_available_for":[],"changed_for":[]},{"id":"todo-demo","installed_for":["claude"],"update_available_for":[],"changed_for":[]}]`) {
		t.Errorf("status skills = %s", status.Body)
	}

	// The CLI (instance secret) sends what it resolved: a harness layout
	// anywhere (CODEX_HOME=/y), or a --dir under the home; nothing else.
	codexHome := filepath.Join(t.TempDir(), "y", "skills")
	target, _ := json.Marshal(skills.InstallRequest{Skill: skills.Storage, Targets: []skills.RequestTarget{{Harness: "codex", SkillsDir: codexHome}}})
	if response := post(string(target), "", testSecret); response.code != http.StatusCreated {
		t.Errorf("codex target = %d %s", response.code, response.body)
	}
	for _, refused := range []skills.RequestTarget{{SkillsDir: outside}, {Harness: "cursor", SkillsDir: outside}} {
		body, _ := json.Marshal(skills.InstallRequest{Skill: skills.Storage, Targets: []skills.RequestTarget{refused}})
		if response := post(string(body), "", testSecret); response.code != http.StatusBadRequest {
			t.Errorf("target %+v = %d %s", refused, response.code, response.body)
		}
	}
	if response := post(`{"skill":"openvaultdb","dir":"/etc/x"}`, "", testSecret); response.code != http.StatusBadRequest {
		t.Errorf("dir field from the CLI credential = %d", response.code)
	}
}

type postResponse struct {
	code int
	body []byte
}
