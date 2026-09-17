package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// The lifecycle endpoints of the local API.
const (
	WhoamiPath   = "/api/local/v1/whoami"
	ShutdownPath = "/api/local/v1/server/shutdown"
)

// Whoami is the body of GET /api/local/v1/whoami.
type Whoami struct {
	Schema     int    `json:"schema"`
	InstanceID string `json:"instance_id"`
	Version    string `json:"version"`
}

// BaseURL is the address clients use: always 127.0.0.1, never a name that
// needs resolving (REQ:client-values-and-mismatch).
func BaseURL(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) }

var probeClient = NewHTTPClient(2 * time.Second)

// NewHTTPClient returns a client for the local API that never follows a
// redirect: the local API does not redirect, and following one would resend
// the bearer secret to wherever an impostor points.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Probe calls the authenticated whoami on port. Sending the secret to an
// impostor is harmless: every start writes a new secret, and a server that
// holds this port is by construction not the one the secret belongs to.
func Probe(ctx context.Context, port int, secret string) (*Whoami, error) {
	body, err := call(ctx, http.MethodGet, port, WhoamiPath, secret)
	if err != nil {
		return nil, err
	}
	var whoami Whoami
	if err := json.Unmarshal(body, &whoami); err != nil || whoami.InstanceID == "" {
		return nil, fmt.Errorf("whoami on port %d: not an OVDB server", port)
	}
	return &whoami, nil
}

func requestShutdown(ctx context.Context, port int, secret string) error {
	_, err := call(ctx, http.MethodPost, port, ShutdownPath, secret)
	return err
}

func call(ctx context.Context, method string, port int, path, secret string) ([]byte, error) {
	var payload io.Reader
	if method != http.MethodGet {
		payload = bytes.NewReader([]byte("{}"))
	}
	request, err := http.NewRequestWithContext(ctx, method, BaseURL(port)+path, payload)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := probeClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK { // including any redirect
		return nil, fmt.Errorf("%s %s on port %d: %s", method, path, port, response.Status)
	}
	return body, nil
}
