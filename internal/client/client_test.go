package client

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
