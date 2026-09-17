package client

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/runtime"
)

func TestConnectWithoutServer(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dirs := paths.Dirs{Home: filepath.Join(base, "home"), Runtime: filepath.Join(base, "run")}

	_, err := Connect(context.Background(), Options{Dirs: dirs, NoStart: true, Start: func(context.Context) (runtime.StartResult, error) {
		t.Fatal("--no-start started the server")
		return runtime.StartResult{}, nil
	}})
	if e := envelope.As(err); e == nil || e.Code != envelope.ServerNotRunning || e.Next[0].Command != "ovdb server start" {
		t.Errorf("--no-start: %v", err)
	}

	var notices bytes.Buffer
	failure := envelope.New(envelope.Forbidden, "no")
	_, err = Connect(context.Background(), Options{Dirs: dirs, Notices: &notices, Start: func(context.Context) (runtime.StartResult, error) {
		return runtime.StartResult{Warnings: []string{"Warning: open dir"}}, failure
	}})
	if !errors.Is(err, failure) || notices.String() != "Warning: open dir\n" {
		t.Errorf("failed auto-start: err %v, notices %q", err, notices.String())
	}
}

func TestVersionNotice(t *testing.T) {
	t.Parallel()
	got := VersionNotice("0.7.0", "0.8.0")
	if want := "OVDB server is running version 0.7.0; restart it to use version 0.8.0: ovdb server restart"; got != want {
		t.Errorf("VersionNotice = %q", got)
	}
	if !strings.Contains(NotRunning().Message, "isn't running") {
		t.Error("NotRunning message")
	}
}
