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
