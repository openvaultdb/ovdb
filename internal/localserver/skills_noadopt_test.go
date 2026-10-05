package localserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/setup/skills"
	"github.com/openvaultdb/ovdb/internal/setup/skills/skillstest"
)

// Consent is bound to the folders the person was shown. Two agents have a copy
// of the skill that OVDB could take over; the request names one of them, so the
// other is refused and left untouched, for the console (by harness) and for
// the CLI and TUI (by folder). A session may not name a directory.
func TestSkillsAPIAdoptsOnlyTheFoldersTheRequestNames(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"console harnesses", "cli folders"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			session := f.signIn(t)
			home, bundled := skillsHome(t, f)
			cursorFolder := filepath.Join(home, ".cursor", "skills", "openvaultdb")
			if err := os.MkdirAll(cursorFolder, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cursorFolder, "SKILL.md"), bundled, 0o600); err != nil {
				t.Fatal(err)
			}
			claudeFolder := filepath.Join(home, ".claude", "skills", "openvaultdb")
			var rec *httptest.ResponseRecorder
			if how == "console harnesses" {
				rec = f.do(t, request{method: http.MethodPost, path: "/api/local/v1/skills/install", cookie: session, header: sameOrigin(testHost),
					body: `{"skill":"openvaultdb","harnesses":["claude","cursor"],"adopt_harnesses":["claude"]}`})
			} else {
				rec = f.do(t, request{method: http.MethodPost, path: "/api/local/v1/skills/install", bearer: testSecret,
					body: fmt.Sprintf(`{"skill":"openvaultdb","harnesses":["claude","cursor"],"adopt_dirs":[%q]}`, claudeFolder)})
			}
			if rec.Code != http.StatusConflict {
				t.Fatalf("POST = %d %s, want the refusal of the folder nobody agreed to", rec.Code, rec.Body)
			}
			body := rec.Body.String()
			if !strings.Contains(body, `"result":"adopted"`) || !strings.Contains(body, `"result":"conflict"`) {
				t.Errorf("body = %s", body)
			}
			if _, err := os.Stat(filepath.Join(home, ".claude", "skills", ".cli-helpers-skills-adopted-backup")); err != nil {
				t.Errorf("the named folder was not adopted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, ".cursor", "skills", ".cli-helpers-skills-adopted-backup")); !os.IsNotExist(err) {
				t.Errorf("the folder nobody named was adopted: %v", err)
			}
			if got, _ := os.ReadFile(filepath.Join(cursorFolder, "SKILL.md")); string(got) != string(bundled) {
				t.Error("the refused folder changed")
			}
		})
	}

	t.Run("a session names no directory", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		session := f.signIn(t)
		home, _ := skillsHome(t, f)
		rec := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/skills/install", cookie: session, header: sameOrigin(testHost),
			body: fmt.Sprintf(`{"skill":"openvaultdb","harnesses":["claude"],"adopt_dirs":[%q]}`, filepath.Join(home, ".claude", "skills", "openvaultdb"))})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "adopt_dirs") {
			t.Errorf("POST = %d %s", rec.Code, rec.Body)
		}
	})
}

// A folder with an interrupted install is its own state, with ovdb's reason, for
// a client that asks (?adoptable=1&recovery=1). A client that does not is
// answered as before the state existed (not_ovdb, no new field) and keeps the
// whole listing; so does a document without a pending recovery.
func TestSkillsAPITellsAPendingRecoveryOnlyToAClientThatKnowsIt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	home, _ := skillsHome(t, f)
	claudeSkills := filepath.Join(home, ".claude", "skills")

	get := func(query string) (int, string) {
		rec := f.do(t, request{path: "/api/local/v1/skills" + query, bearer: testSecret})
		return rec.Code, rec.Body.String()
	}
	// No journal yet: the document carries no state_reason for a client that
	// did not ask for it (the codex copy is another's folder, with a reason).
	if code, body := get("?adoptable=1"); code != http.StatusOK || strings.Contains(body, "state_reason") {
		t.Fatalf("without a journal: %d %s", code, body)
	}
	if code, body := get("?adoptable=1&recovery=1"); code != http.StatusOK || !strings.Contains(body, `"state_reason":"unmanaged target`) {
		t.Fatalf("a client that asked: %d %s", code, body)
	}

	skillstest.PutPendingRecovery(t, claudeSkills)
	// A client that did not ask is answered as before the state existed: the
	// listing stays, the folder is not_ovdb, and no new field is sent.
	for _, query := range []string{"", "?adoptable=1"} {
		code, body := get(query)
		if code != http.StatusOK || strings.Contains(body, "recovery_pending") || strings.Contains(body, "state_reason") ||
			!strings.Contains(body, `"state":"not_ovdb"`) || !strings.Contains(body, `"id":"todo-demo"`) {
			t.Errorf("GET %q = %d %s, want the old answer with the whole listing", query, code, body)
		}
	}
	code, body := get("?adoptable=1&recovery=1")
	var document skills.Document
	if err := json.Unmarshal([]byte(body), &document); err != nil || code != http.StatusOK {
		t.Fatalf("GET = %d %s", code, body)
	}
	if target := document.Skills[0].Targets[0]; target.Harness != "claude" || target.State != skills.StateRecoveryPending || target.StateReason == "" || target.Installed {
		t.Errorf("target = %+v", target)
	}

	// An install that does not ask for adoption finishes the recovery and
	// refuses the folder, as the same request would without a journal.
	rec := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/skills/install", bearer: testSecret, body: `{"skill":"openvaultdb","harnesses":["claude"]}`})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "wasn't installed by OVDB") {
		t.Errorf("POST = %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Lstat(filepath.Join(claudeSkills, ".cli-helpers-skills-recovery.json")); !os.IsNotExist(err) {
		t.Errorf("the journal was not recovered: %v", err)
	}
	if code, body := get("?adoptable=1"); code != http.StatusOK || !strings.Contains(body, `"state":"not_ovdb"`) {
		t.Errorf("after recovery: %d %s", code, body)
	}
}
