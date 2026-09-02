package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openvaultdb/openvaultdb-go/demo"
)

func TestValidateDemoSessionRejectsWrongOwnerAndUnsafeProxy(t *testing.T) {
	s := demo.Session{SessionID: "s", OwnerUserID: "owner", SpaceID: "space", SpaceType: "group", DatabaseID: demoDatabaseID, ExpiresAt: time.Now().Add(time.Minute), ProxyURL: "https://proxy.example", AppURL: "https://listus.app", TunnelToken: "token"}
	if err := validateDemoSession(s, "other"); err == nil {
		t.Fatal("wrong owner accepted")
	}
	s.OwnerUserID = "owner"
	s.ProxyURL = "http://proxy.example"
	if err := validateDemoSession(s, "owner"); err == nil {
		t.Fatal("insecure proxy accepted")
	}
}

func TestValidateDemoManifestRejectsUnsafeBackendsBeforeMount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ovdb.yaml")
	if err := os.WriteFile(path, []byte("database:\n  id: demo-sneat-space\n  schema_mode: schemaless\nstorage:\n  engine: firestore\n  firestore:\n    project: should-not-connect\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateDemoManifest(path); err == nil {
		t.Fatal("remote engine accepted")
	}
	if err := os.Symlink(path, filepath.Join(dir, "linked.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := validateDemoManifest(filepath.Join(dir, "linked.yaml")); err == nil {
		t.Fatal("manifest symlink accepted")
	}
}

func TestValidateDemoAddrRejectsPublicAndMalformedAddresses(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:6832", "example.com:6832", "6832", "127.0.0.1"} {
		if err := validateDemoAddr(addr); err == nil {
			t.Errorf("validateDemoAddr(%q) unexpectedly succeeded", addr)
		}
	}
	if err := validateDemoAddr("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
}

func TestListusDemoURLDoesNotAcceptControlCharacters(t *testing.T) {
	if _, err := listusDemoURL("space\nsecret"); err == nil {
		t.Fatal("control character accepted")
	}
	got, err := listusDemoURL("space_1")
	if err != nil || got != "https://listus.app/space/group/space_1/lists" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRestrictedDemoHandlerDoesNotExposeAdminOrAuth(t *testing.T) {
	h := restrictedDemoHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, path := range []string{"/authorize", "/token", "/v1/tokens", "/v1/databases", "/v1/databases/other/records/x"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: got %d", path, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/databases/demo-sneat-space/records/lists/do:demo", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("allowed record route got %d", w.Code)
	}
}
