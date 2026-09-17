package client

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/datapath"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/runtime"
)

func testLocal(t *testing.T) (*Local, *bytes.Buffer) {
	base := t.TempDir()
	var notices bytes.Buffer
	return &Local{
		Dirs:    paths.Dirs{Home: filepath.Join(base, "home"), Runtime: filepath.Join(base, "run"), Data: filepath.Join(base, "data")},
		Version: "1.0.0", Port: 6832, Notices: &notices,
	}, &notices
}

func TestPureReadsWithoutServer(t *testing.T) {
	t.Parallel()
	l, _ := testLocal(t)
	ctx := context.Background()
	for name, read := range map[string]func(context.Context) ([]byte, error){"server": l.Server, "status": l.Status, "config": l.Config, "home": l.Home} {
		body, err := read(ctx)
		if err != nil || !strings.HasPrefix(string(body), `{"schema":1,`) {
			t.Errorf("%s = %s, %v", name, body, err)
		}
	}
	if _, err := os.Stat(l.Dirs.Runtime); !os.IsNotExist(err) {
		t.Errorf("a pure read created the runtime directory: %v", err)
	}
}

func TestConnectWithoutServer(t *testing.T) {
	t.Parallel()
	l, _ := testLocal(t)
	l.Command = func(int) *exec.Cmd {
		t.Fatal("--no-start started the server")
		return nil
	}
	_, err := l.Connect(context.Background(), true)
	if e := envelope.As(err); e == nil || e.Code != envelope.ServerNotRunning || e.Next[0].Command != "ovdb server start" {
		t.Errorf("--no-start: %v", err)
	}
}

func TestUnreadableRecordIsANotice(t *testing.T) {
	t.Parallel()
	l, notices := testLocal(t)
	if _, err := runtime.PrepareDirs(l.Dirs, "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.Dirs.Runtime, runtime.RecordFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := l.Server(context.Background())
	if err != nil || !strings.Contains(string(body), `"not_running"`) || !strings.Contains(notices.String(), "Ignoring server.json") {
		t.Errorf("Server = %s, %v; notices %q", body, err, notices.String())
	}
}

func TestVersionNotice(t *testing.T) {
	t.Parallel()
	got := VersionNotice("0.7.0", "0.8.0")
	if want := "OVDB server is running version 0.7.0; restart it to use version 0.8.0: ovdb server restart"; got != want {
		t.Errorf("VersionNotice = %q", got)
	}
}

// REQ:version-mismatch-notice: an older server without an endpoint is
// server_version_mismatch with the restart command, not "nothing here";
// the same server's own not_found (a missing database) stays not_found.
func TestUnknownEndpointOnOtherVersionIsVersionMismatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/databases/gone") {
			envelope.Write(w, envelope.New(envelope.NotFound, "Couldn't remove the database"))
			return
		}
		envelope.Write(w, envelope.New(envelope.NotFound, uicopy.T("api.not_found", nil)))
	}))
	defer server.Close()
	port, _ := strconv.Atoi(server.URL[strings.LastIndex(server.URL, ":")+1:])
	l, _ := testLocal(t)
	for _, tc := range []struct {
		serverVersion, path string
		want                envelope.Code
	}{
		{"0.9.0", DatabasesPath, envelope.ServerVersionMismatch},
		{"1.0.0", DatabasesPath, envelope.NotFound},
		{"0.9.0", DatabasesPath + "/gone", envelope.NotFound},
	} {
		c := l.newClient(runtime.State{Running: true, Record: &runtime.Record{Port: port}, Secret: "s", Whoami: &runtime.Whoami{Version: tc.serverVersion}})
		response, err := c.Do(context.Background(), http.MethodGet, tc.path, nil)
		e := envelope.As(err)
		if e == nil || e.Code != tc.want {
			t.Errorf("%s on %s = %v", tc.path, tc.serverVersion, err)
			continue
		}
		if tc.want == envelope.ServerVersionMismatch {
			if e.Next[0].Command != "ovdb server restart" || envelope.Decode(response.Body).Code != envelope.ServerVersionMismatch {
				t.Errorf("mismatch = %+v body %s", e, response.Body)
			}
		}
	}
}

// A record's path prefers the server's full nested key (openvaultdb-go
// v0.6.2+) and still composes it from an older server's short key.
func TestRecordPath(t *testing.T) {
	t.Parallel()
	items, _ := datapath.Parse("/lists/to-buy/items")
	for key, want := range map[string]string{
		"lists/to-buy/items/milk":  "/lists/to-buy/items/milk",
		"items/milk":               "/lists/to-buy/items/milk",
		"lists/to-buy/items/a%2Fb": "/lists/to-buy/items/a%2Fb",
		"items/a%2Fb":              "/lists/to-buy/items/a%2Fb",
	} {
		if got := RecordPath(items, key).String(); got != want {
			t.Errorf("RecordPath(%q) = %q, want %q", key, got, want)
		}
	}
	root, _ := datapath.Parse("/notes")
	if got := RecordPath(root, "notes/x").String(); got != "/notes/x" {
		t.Errorf("root collection = %q", got)
	}
}

// The v0.6.2 invalid_key /v1 code is invalid_argument with what to do.
func TestMapV1InvalidKey(t *testing.T) {
	t.Parallel()
	path, _ := datapath.Parse("/lists/to-buy/items/x")
	e := MapV1(http.StatusBadRequest, []byte(`{"error":{"code":"invalid_key","message":"invalid key segment \"..\""}}`),
		DataOp{Verb: "get", Database: "todo", Path: path, Suffix: " --db todo"}).Envelope
	if e.Code != envelope.InvalidArgument || !strings.Contains(e.Reason, "invalid key segment") || len(e.Next) != 2 ||
		e.Next[1].Command != "ovdb list /lists/to-buy/items --db todo" {
		t.Errorf("invalid_key = %+v", e)
	}
}
