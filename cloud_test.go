package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	openvaultdbcloud "github.com/openvaultdb/openvaultdb-go/cloud"
	"github.com/strongo/deviceauth"
)

func TestCloudCommandLoginStatusLogoutJourney(t *testing.T) {
	const accessToken = "ovdb_test_access_token"
	var revoked atomic.Bool
	var failRevoke atomic.Bool
	var tokenExchanges atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth/device/code":
			if r.Method != http.MethodPost {
				t.Errorf("device code method = %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse device form: %v", err)
			}
			if got := r.Form.Get("client_id"); got != cloudClientID {
				t.Errorf("client_id = %q", got)
			}
			if got := r.Form.Get("scope"); got != cloudScope {
				t.Errorf("scope = %q", got)
			}
			for name, want := range map[string]string{
				"device_name":    "test-device",
				"os":             "test-os",
				"arch":           "test-arch",
				"client_version": "9.8.7",
			} {
				if got := r.Form.Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			writeTestJSON(t, w, map[string]any{
				"device_code":               "device-secret",
				"user_code":                 "BCDF-GHJK",
				"verification_uri":          serverURL(r) + "/device",
				"verification_uri_complete": serverURL(r) + "/device?user_code=BCDF-GHJK",
				"expires_in":                60,
				"interval":                  1,
			})
		case "/oauth/token":
			tokenExchanges.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse token form: %v", err)
			}
			if got := r.Form.Get("device_code"); got != "device-secret" {
				t.Errorf("device_code = %q", got)
			}
			writeTestJSON(t, w, map[string]any{
				"access_token": accessToken,
				"token_type":   "Bearer",
				"expires_in":   31_536_000,
				"scope":        cloudScope,
			})
		case "/oauth/userinfo":
			if r.Header.Get("Authorization") != "Bearer "+accessToken || revoked.Load() {
				w.WriteHeader(http.StatusUnauthorized)
				writeTestJSON(t, w, map[string]string{"error": "invalid_token"})
				return
			}
			writeTestJSON(t, w, cloudIdentity{
				Subject:   "firebase-user-1",
				Email:     "alex@example.com",
				Name:      "Alex",
				ClientID:  cloudClientID,
				Scope:     cloudScope,
				ExpiresAt: time.Now().Add(365 * 24 * time.Hour).UTC().Format(time.RFC3339),
			})
		case "/oauth/revoke":
			if failRevoke.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				writeTestJSON(t, w, map[string]string{
					"error":             "temporarily_unavailable",
					"error_description": "try again",
				})
				return
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse revoke form: %v", err)
			}
			if got := r.Form.Get("token"); got != accessToken {
				t.Errorf("revoke token = %q", got)
			}
			revoked.Store(true)
			w.WriteHeader(http.StatusOK)
		case "/device":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("device page"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	configDir := t.TempDir()
	var openedURL string
	deps := cloudDependencies{
		httpClient: server.Client(),
		login:      deviceauth.Login,
		openBrowser: func(rawURL string) error {
			openedURL = rawURL
			return nil
		},
		deviceInfo: func() deviceauth.DeviceInfo {
			return deviceauth.DeviceInfo{
				Name: "test-device", OS: "test-os", Arch: "test-arch", ClientVersion: "9.8.7",
			}
		},
		userConfigDir: func() (string, error) { return configDir, nil },
	}

	stdout, stderr, err := executeCloudCommand(
		deps,
		"login",
		"--host", server.URL,
		"--insecure-storage",
	)
	if err != nil {
		t.Fatalf("login: %v\nstderr: %s", err, stderr)
	}
	if openedURL != server.URL+"/device?user_code=BCDF-GHJK" {
		t.Fatalf("opened URL = %q", openedURL)
	}
	if tokenExchanges.Load() != 1 {
		t.Fatalf("token exchanges = %d", tokenExchanges.Load())
	}
	for _, text := range []string{
		"Copy this one-time code: BCDF-GHJK",
		"Logged in to " + strings.TrimPrefix(server.URL, "http://"),
		"Alex <alex@example.com>",
	} {
		if !strings.Contains(stdout, text) {
			t.Errorf("login output does not contain %q:\n%s", text, stdout)
		}
	}
	if !strings.Contains(stderr, "access token unencrypted") {
		t.Errorf("login stderr has no insecure-storage warning: %s", stderr)
	}

	baseURL, err := normalizeCloudURL(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := selectCloudStore(baseURL, true, deps.userConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := selection.store.Load()
	if err != nil {
		t.Fatalf("load saved credential: %v", err)
	}
	if credential.AccessToken != accessToken || credential.AccountID != "firebase-user-1" {
		t.Fatalf("saved credential = %#v", credential)
	}
	info, err := os.Stat(selection.description)
	if err != nil {
		t.Fatalf("stat credential: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %o", info.Mode().Perm())
	}

	stdout, stderr, err = executeCloudCommand(
		deps,
		"status",
		"--host", server.URL,
		"--insecure-storage",
	)
	if err != nil {
		t.Fatalf("status: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Logged in as: Alex <alex@example.com>") ||
		!strings.Contains(stdout, "Scopes: account:read") {
		t.Fatalf("unexpected status output:\n%s", stdout)
	}

	failRevoke.Store(true)
	_, _, err = executeCloudCommand(
		deps,
		"logout",
		"--host", server.URL,
		"--insecure-storage",
	)
	if err == nil || !strings.Contains(err.Error(), "local credential retained") {
		t.Fatalf("failed revoke error = %v", err)
	}
	if _, err := selection.store.Load(); err != nil {
		t.Fatalf("credential was not retained after failed revoke: %v", err)
	}

	failRevoke.Store(false)
	stdout, stderr, err = executeCloudCommand(
		deps,
		"logout",
		"--host", server.URL,
		"--insecure-storage",
	)
	if err != nil {
		t.Fatalf("logout: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Logged out of") || !revoked.Load() {
		t.Fatalf("logout did not revoke: output=%q revoked=%t", stdout, revoked.Load())
	}
	if _, err := selection.store.Load(); !errors.Is(err, deviceauth.ErrCredentialNotFound) {
		t.Fatalf("credential still exists after logout: %v", err)
	}
}

func TestCloudDeviceInfo(t *testing.T) {
	if cloudScope != "account:read databases:read" {
		t.Fatalf("cloud scope = %q, want explicit account and database metadata scopes", cloudScope)
	}
	previousVersion := appVersion
	appVersion = "1.2.3"
	t.Cleanup(func() { appVersion = previousVersion })

	info := cloudDeviceInfo()
	if info.Name == "" || info.OS == "" || info.Arch == "" || info.ClientVersion != "1.2.3" {
		t.Fatalf("cloud device info = %+v", info)
	}
}

func TestCloudDatabasesListUsesStoredCredentialAndEscapesTable(t *testing.T) {
	const accessToken = "catalogue-token"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer "+accessToken {
			t.Errorf("Authorization = %q", got)
		}
		if got := request.URL.Query().Get("space"); got != "personal" {
			t.Errorf("space = %q", got)
		}
		if got := request.URL.Query().Get("pageSize"); got != "100" {
			t.Errorf("pageSize = %q", got)
		}
		switch requests.Add(1) {
		case 1:
			if got := request.URL.Query().Get("pageToken"); got != "" {
				t.Errorf("first page token = %q", got)
			}
			writeTestJSON(t, w, map[string]any{
				"databases": []openvaultdbcloud.Database{{
					ID: "personal_db", Name: "Personal\x1b[2J database", SpaceID: "personal_space",
					SpaceType: "personal", Provider: "server", Status: "active",
				}},
				"nextPageToken": "second_page",
			})
		case 2:
			if got := request.URL.Query().Get("pageToken"); got != "second_page" {
				t.Errorf("second page token = %q", got)
			}
			writeTestJSON(t, w, map[string]any{
				"databases": []openvaultdbcloud.Database{{
					ID: "shared_db", Name: "Shared database", SpaceID: "team_space", SpaceName: "Team",
					SpaceType: "team", Provider: "github", Status: "onboarding",
				}},
			})
		default:
			t.Errorf("unexpected catalogue request %d", requests.Load())
		}
	}))
	defer server.Close()
	deps := catalogueTestDependencies(server, t.TempDir())
	storeCloudCredential(t, deps, server.URL, accessToken, []string{"account:read", "databases:read"})

	stdout, stderr, err := executeCloudCommand(deps, "databases", "--space", "personal", "--host", server.URL, "--insecure-storage")
	if err != nil {
		t.Fatalf("databases: %v\nstderr: %s", err, stderr)
	}
	if requests.Load() != 2 {
		t.Fatalf("catalogue requests = %d, want 2", requests.Load())
	}
	for _, wanted := range []string{"ID", "personal_db", "shared_db", "\\x1b[2J", "Team"} {
		if !strings.Contains(stdout, wanted) {
			t.Errorf("table does not contain %q:\n%s", wanted, stdout)
		}
	}
	if strings.Contains(stdout, "\x1b") {
		t.Fatalf("table includes raw terminal escape: %q", stdout)
	}
}

func TestCloudDatabasesJSONAndGetResponseErrors(t *testing.T) {
	const accessToken = "catalogue-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer "+accessToken {
			t.Errorf("Authorization = %q", got)
		}
		switch request.URL.Path {
		case "/api/databases":
			writeTestJSON(t, w, map[string]any{"databases": []openvaultdbcloud.Database{{
				ID: "database_1", Name: "Database", SpaceID: "space", SpaceType: "team", Provider: "server", Status: "active",
			}}})
		case "/api/databases/database_1":
			w.WriteHeader(http.StatusForbidden)
			writeTestJSON(t, w, map[string]string{
				"error":             "insufficient_scope",
				"error_description": "untrusted \x1b[2J response text",
			})
		case "/api/databases/database_2":
			writeTestJSON(t, w, map[string]any{"database": openvaultdbcloud.Database{
				ID: "database_2", Name: "Second database", SpaceID: "space", SpaceType: "team", Provider: "github", Status: "active",
			}})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	deps := catalogueTestDependencies(server, t.TempDir())
	storeCloudCredential(t, deps, server.URL, accessToken, []string{"account:read", "databases:read"})

	stdout, stderr, err := executeCloudCommand(deps, "databases", "list", "--json", "--host", server.URL, "--insecure-storage")
	if err != nil {
		t.Fatalf("databases list --json: %v\nstderr: %s", err, stderr)
	}
	var databases []openvaultdbcloud.Database
	if err := json.Unmarshal([]byte(stdout), &databases); err != nil || len(databases) != 1 || databases[0].ID != "database_1" {
		t.Fatalf("JSON stdout = %q, databases = %#v, error = %v", stdout, databases, err)
	}
	stdout, stderr, err = executeCloudCommand(deps, "databases", "get", "database_2", "--json", "--host", server.URL, "--insecure-storage")
	if err != nil {
		t.Fatalf("databases get --json: %v\nstderr: %s", err, stderr)
	}
	var database openvaultdbcloud.Database
	if err := json.Unmarshal([]byte(stdout), &database); err != nil || database.ID != "database_2" {
		t.Fatalf("JSON stdout = %q, database = %#v, error = %v", stdout, database, err)
	}

	_, _, err = executeCloudCommand(deps, "databases", "get", "database_1", "--host", server.URL, "--insecure-storage")
	if err == nil || !strings.Contains(err.Error(), "run 'ovdb cloud login'") {
		t.Fatalf("get error = %v", err)
	}
	if strings.Contains(err.Error(), "\x1b") || strings.Contains(err.Error(), "untrusted") {
		t.Fatalf("get error leaks remote control text: %q", err)
	}
}

func TestCloudDatabasesRejectsOldScopeAndPaginationLoops(t *testing.T) {
	t.Run("old credential", func(t *testing.T) {
		var called atomic.Bool
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Store(true) }))
		defer server.Close()
		deps := catalogueTestDependencies(server, t.TempDir())
		storeCloudCredential(t, deps, server.URL, "old-token", []string{"account:read"})
		_, _, err := executeCloudCommand(deps, "databases", "list", "--host", server.URL, "--insecure-storage")
		if err == nil || !strings.Contains(err.Error(), "lacks databases:read") || !strings.Contains(err.Error(), "ovdb cloud login") {
			t.Fatalf("old credential error = %v", err)
		}
		if called.Load() {
			t.Fatal("old credential made a catalogue request")
		}
	})

	t.Run("repeated page token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeTestJSON(t, w, map[string]any{"databases": []openvaultdbcloud.Database{}, "nextPageToken": "again"})
		}))
		defer server.Close()
		deps := catalogueTestDependencies(server, t.TempDir())
		storeCloudCredential(t, deps, server.URL, "token", []string{"account:read", "databases:read"})
		_, _, err := executeCloudCommand(deps, "databases", "list", "--host", server.URL, "--insecure-storage")
		if err == nil || !strings.Contains(err.Error(), "repeated page token") {
			t.Fatalf("pagination error = %v", err)
		}
	})

	t.Run("empty list", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeTestJSON(t, w, map[string]any{"databases": []openvaultdbcloud.Database{}})
		}))
		defer server.Close()
		deps := catalogueTestDependencies(server, t.TempDir())
		storeCloudCredential(t, deps, server.URL, "token", []string{"account:read", "databases:read"})
		stdout, stderr, err := executeCloudCommand(deps, "databases", "list", "--host", server.URL, "--insecure-storage")
		if err != nil {
			t.Fatalf("empty list: %v\nstderr: %s", err, stderr)
		}
		if strings.TrimSpace(stdout) != "No cloud databases found." {
			t.Fatalf("empty list output = %q", stdout)
		}
	})
}

func catalogueTestDependencies(server *httptest.Server, configDir string) cloudDependencies {
	return cloudDependencies{
		httpClient:    server.Client(),
		userConfigDir: func() (string, error) { return configDir, nil },
	}
}

func storeCloudCredential(t *testing.T, deps cloudDependencies, host, accessToken string, scopes []string) {
	t.Helper()
	baseURL, err := normalizeCloudURL(host)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := selectCloudStore(baseURL, true, deps.userConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := selection.store.Save(deviceauth.Credential{AccessToken: accessToken, TokenType: "Bearer", Scopes: scopes}); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeCloudURL(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "https://cloud.openvaultdb.com/", want: "https://cloud.openvaultdb.com"},
		{raw: "http://127.0.0.1:8787", want: "http://127.0.0.1:8787"},
		{raw: "http://localhost:8787", want: "http://localhost:8787"},
		{raw: "http://cloud.openvaultdb.com", wantErr: true},
		{raw: "https://cloud.openvaultdb.com/path", wantErr: true},
		{raw: "https://cloud.openvaultdb.com?token=no", wantErr: true},
		{raw: "cloud.openvaultdb.com", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			got, err := normalizeCloudURL(test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("normalizeCloudURL(%q) = %q, want error", test.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeCloudURL(%q): %v", test.raw, err)
			}
			if got.String() != test.want {
				t.Fatalf("normalizeCloudURL(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func executeCloudCommand(deps cloudDependencies, args ...string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	cmd := newCloudCmdWithDependencies(deps)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func serverURL(r *http.Request) string {
	return fmt.Sprintf("http://%s", r.Host)
}
