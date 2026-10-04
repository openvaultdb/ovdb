package localserver

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
	embedded "github.com/openvaultdb/ovdb/skills"
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

// skillsHome lays out the server's home as the v0.21.0 fixtures in
// testdata/v0.21.0 were recorded: Claude Code holds a copy of the bundled
// skill that OVDB did not install (adoptable since skillsync v0.26.0), Codex
// holds a file of someone else's, Cursor is found with no skill folder.
func skillsHome(t *testing.T, f *fixture) (home string, bundled []byte) {
	t.Helper()
	bundled, err := fs.ReadFile(embedded.FS, "openvaultdb/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	home = skills.Canonical(f.userHome)
	for dir, text := range map[string][]byte{
		filepath.Join(home, ".claude", "skills", "openvaultdb", "SKILL.md"): bundled,
		filepath.Join(home, ".codex", "skills", "openvaultdb", "SKILL.md"):  []byte("mine"),
		filepath.Join(home, ".cursor", "skills", ".keep"):                   nil,
	} {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir, text, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, bundled
}

// golden is the answer the real v0.21.0 server gave for the same request on
// the same layout, with the home folder as {{home}}.
func golden(t *testing.T, name, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "v0.21.0", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "{{home}}", filepath.ToSlash(home))
}

// A client built before adoption existed (v0.21.0, or a console page loaded
// from it) says nothing about adoption, and the server answers it exactly as
// v0.21.0 did, byte for byte: the folder is not_ovdb in the list, installing is
// refused as already_exists, and nothing is touched. It must not be sent the
// state "adoptable" or the result "adopted", for which it has no text (the
// CLI panicked on the result after the folder was already taken over).
func TestSkillsAPIAnswersAnOlderClientAsBefore(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	home, bundled := skillsHome(t, f)
	if runtimeGOOSIsWindows() {
		t.Skip("the recorded answers are POSIX paths")
	}
	post := func(body string) *httptest.ResponseRecorder {
		return f.do(t, request{method: http.MethodPost, path: "/api/local/v1/skills/install", body: body, bearer: testSecret})
	}

	if rec := f.do(t, request{path: "/api/local/v1/skills", bearer: testSecret}); rec.Code != http.StatusOK || rec.Body.String() != golden(t, "get-skills.json", home) {
		t.Errorf("GET skills = %d %s\nwant %s", rec.Code, rec.Body, golden(t, "get-skills.json", home))
	}
	for body, want := range map[string]string{
		`{"skill":"openvaultdb","harnesses":["claude"]}`:                "post-claude.json",
		`{"skill":"openvaultdb","harnesses":["claude","codex"]}`:        "post-claude-codex.json",
		`{"skill":"openvaultdb","harnesses":["claude"],"dry_run":true}`: "post-dryrun.json",
	} {
		if rec := post(body); rec.Code != http.StatusConflict || rec.Body.String() != golden(t, want, home) {
			t.Errorf("POST %s = %d %s\nwant %s", body, rec.Code, rec.Body, golden(t, want, home))
		}
	}
	if kept, _ := os.ReadFile(filepath.Join(home, ".claude", "skills", "openvaultdb", "SKILL.md")); string(kept) != string(bundled) {
		t.Error("a request that did not ask for adoption changed the folder")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", ".cli-helpers-skills-adopted-backup")); !os.IsNotExist(err) {
		t.Errorf("a backup was made for a request that did not ask for adoption: %v", err)
	}
}

// skillsync v0.26.0 adopts a folder that is already the bundled skill, and a
// client that says it understands that gets it: GET lists the target as
// adoptable with ?adoptable=1, POST adopts it only with "adopt": true (the
// console's ticked box, which a session may send; the CLI's resolved targets
// too) and answers 201 with result "adopted" and backup_path, the folder that
// holds the copy that was there.
func TestSkillsAdoptionThroughTheAPI(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	session := f.signIn(t)
	home, bundled := skillsHome(t, f)
	claudeSkills := filepath.Join(home, ".claude", "skills")

	var document skills.Document
	rec := f.do(t, request{path: "/api/local/v1/skills?adoptable=1", cookie: session})
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET skills = %d %s", rec.Code, rec.Body)
	}
	if target := document.Skills[0].Targets[0]; target.Harness != "claude" || target.State != skills.StateAdoptable || target.Installed || len(document.Skills[0].InstalledFor) != 0 {
		t.Errorf("target = %+v", target)
	}
	if !strings.Contains(rec.Body.String(), `"state":"adoptable"`) {
		t.Errorf("GET skills = %s", rec.Body)
	}

	install := func(body string) *httptest.ResponseRecorder {
		return f.do(t, request{method: http.MethodPost, path: "/api/local/v1/skills/install", body: body, cookie: session, header: sameOrigin(testHost)})
	}
	if rec := install(`{"skill":"openvaultdb","harnesses":["claude"]}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"already_exists"`) {
		t.Fatalf("without adopt = %d %s", rec.Code, rec.Body)
	}
	rec = install(`{"skill":"openvaultdb","harnesses":["claude"],"adopt":true}`)
	var installed skills.InstallDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &installed); err != nil || rec.Code != http.StatusCreated || len(installed.Outcomes) != 1 {
		t.Fatalf("POST install = %d %s", rec.Code, rec.Body)
	}
	outcome := installed.Outcomes[0]
	if outcome.Result != "adopted" || outcome.State != skills.StateInstalled || installed.AlreadyUpToDate {
		t.Errorf("outcome = %+v", outcome)
	}
	if !strings.HasPrefix(outcome.BackupPath, claudeSkills+string(filepath.Separator)) || !strings.Contains(rec.Body.String(), `"backup_path":`) {
		t.Errorf("backup path = %q in %s", outcome.BackupPath, rec.Body)
	}
	if kept, err := os.ReadFile(filepath.Join(outcome.BackupPath, "SKILL.md")); err != nil || string(kept) != string(bundled) {
		t.Errorf("backup = %q, %v", kept, err)
	}
}

// An install that fails for one agent must still say what it did to the
// others (it said only the failure, before and since adoption): the folder
// that was taken over, with where its backup is, and the one that was added.
// The failure keeps its code and status, and the outcomes are in the body's
// targets and in the reason's words.
func TestSkillsFailedInstallNamesWhatChanged(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	home, _ := skillsHome(t, f)
	// A dry run says it as a plan, and writes nothing.
	dry := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/skills/install", bearer: testSecret,
		body: `{"skill":"openvaultdb","harnesses":["claude","cursor","codex"],"adopt":true,"dry_run":true}`})
	plan := envelope.Decode(dry.Body.Bytes())
	if dry.Code != http.StatusConflict || plan == nil || !strings.Contains(plan.Reason, "Claude Code would change: "+filepath.Join(home, ".claude", "skills", "openvaultdb")+" (already there; OVDB would take it over). A backup of your copy would be kept.") ||
		!strings.Contains(plan.Reason, "Cursor would change: "+filepath.Join(home, ".cursor", "skills", "openvaultdb")+" (added).") || len(plan.Targets) == 0 {
		t.Fatalf("dry run = %d %s", dry.Code, dry.Body)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "skills", "openvaultdb")); !os.IsNotExist(err) {
		t.Errorf("a dry run wrote the Cursor folder: %v", err)
	}
	rec := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/skills/install", bearer: testSecret,
		body: `{"skill":"openvaultdb","harnesses":["claude","cursor","codex"],"adopt":true}`})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	failure := envelope.Decode(rec.Body.Bytes())
	if failure == nil || failure.Code != envelope.AlreadyExists {
		t.Fatalf("body = %s", rec.Body)
	}
	var outcomes []skills.Outcome
	if err := json.Unmarshal(failure.Targets, &outcomes); err != nil || len(outcomes) != 3 {
		t.Fatalf("targets = %s, %v", failure.Targets, err)
	}
	results := map[string]string{}
	for _, o := range outcomes {
		results[o.Harness] = o.Result
	}
	if results["claude"] != "adopted" || results["cursor"] != "added" || results["codex"] != "conflict" {
		t.Errorf("results = %v", results)
	}
	backup := outcomes[0].BackupPath
	if backup == "" || !strings.HasPrefix(backup, filepath.Join(home, ".claude", "skills")+string(filepath.Separator)) {
		t.Errorf("claude backup = %q", backup)
	}
	for _, want := range []string{"codex/skills/openvaultdb already exists", "Before it stopped, Claude Code changed: " + filepath.Join(home, ".claude", "skills", "openvaultdb") + " (already there, now managed by OVDB). Your copy is kept at " + backup, "Before it stopped, Cursor changed: " + filepath.Join(home, ".cursor", "skills", "openvaultdb") + " (added)."} {
		if !strings.Contains(failure.Reason, strings.ReplaceAll(want, "/", string(filepath.Separator))) {
			t.Errorf("reason lacks %q:\n%s", want, failure.Reason)
		}
	}
}

type postResponse struct {
	code int
	body []byte
}

func runtimeGOOSIsWindows() bool { return runtime.GOOS == "windows" }
