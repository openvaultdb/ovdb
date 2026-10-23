package localserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/setup"
)

// A write to a connected inGitDB repository fails in Git when the person has
// no name and email at all; the data API then says so, with no secret or
// internal error text, instead of openvaultdb-go's bare 500.
func TestWriteWithoutGitIdentityExplainsTheFix(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "none"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	f := newFixture(t)
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, setup.InGitDBDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if exec.Command("git", "-C", repo, "var", "GIT_AUTHOR_IDENT").Run() == nil {
		t.Skip("git works out an identity on this machine without configuration")
	}
	body, _ := json.Marshal(setup.ConnectRequest{ID: "notes", Engine: setup.EngineInGitDB, Path: repo})
	rec := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/databases/connect", bearer: testSecret, body: string(body)})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `git config --global user.name`) {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body)
	}

	rec = f.do(t, request{method: http.MethodPut, path: "/v1/databases/notes/records/items/milk", bearer: testSecret, body: `{"data":{"title":"Milk"}}`})
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"`+setup.GitIdentityMissingCode+`"`) ||
		!strings.Contains(rec.Body.String(), "can't save changes in") {
		t.Errorf("write = %d %s", rec.Code, rec.Body)
	}
	if out, _ := exec.Command("git", "-C", repo, "config", "--local", "--get", "user.email").Output(); len(out) != 0 {
		t.Errorf("OVDB set an identity in the repository: %s", out)
	}
	// Reads are served as they are.
	if rec := f.do(t, request{method: http.MethodPost, path: "/v1/databases/notes/query", bearer: testSecret, body: `{"collection":"items"}`}); rec.Code == http.StatusServiceUnavailable {
		t.Errorf("query = %d %s", rec.Code, rec.Body)
	}
}

// F9: only git's own missing-identity failure becomes git_identity_missing;
// any other internal error on a Git-backed database stays what it is.
func TestGitIdentityFailureIsRecognisedOnlyFromGit(t *testing.T) {
	t.Parallel()
	identity := "dalgo2ingitdb: create transaction commit: exit status 128: Author identity unknown\n\n*** Please tell me who you are."
	for text, want := range map[string]bool{
		identity: true,
		"dalgo2ingitdb: create transaction commit: exit status 128: fatal: empty ident name (for <a@b>) not allowed": true,
		"write items/$records/a.yaml: no space left on device":                                                       false,
		"": false,
	} {
		if got := gitIdentityFailure(text); got != want {
			t.Errorf("gitIdentityFailure(%q) = %v, want %v", text, got, want)
		}
	}

	// The data server's logged error reaches the request that caused it.
	slot := &loggedError{}
	logger := slog.New(captureErrors(slog.NewTextHandler(io.Discard, nil)))
	logger.ErrorContext(context.WithValue(context.Background(), loggedErrorKey{}, slot), "internal server error", slog.Any("error", errors.New(identity)))
	logger.ErrorContext(context.Background(), "internal server error", slog.Any("error", errors.New("other request")))
	if !gitIdentityFailure(slot.text()) {
		t.Errorf("captured = %q", slot.text())
	}
}
