package cli_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/core"

	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/setup"
)

type captureMounter struct{ db *core.Database }

func (c *captureMounter) Mount(db *core.Database) error                { c.db = db; return nil }
func (c *captureMounter) UnmountContext(context.Context, string) error { return nil }

// Temporary diagnostic: why inGitDB writes fail on Windows CI.
func TestDiagInGitDBWriteWindows(t *testing.T) {
	base := t.TempDir()
	dirs := paths.Dirs{Home: filepath.Join(base, "home"), Runtime: filepath.Join(base, "run"), Data: filepath.Join(base, "data")}
	for _, dir := range []string{dirs.Home, dirs.Runtime, dirs.Data} {
		if err := paths.EnsurePrivateDir(dir); err != nil {
			t.Fatal(err)
		}
	}
	m := &captureMounter{}
	registry, err := setup.OpenRegistry(dirs, m, t.Logf, setup.RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Create(setup.CreateRequest{ID: "notes", Engine: "ingitdb", Path: filepath.Join(dirs.Data, "notes")}); err != nil {
		t.Fatal(err)
	}
	key, _ := core.ParseKeyPath("items/x")
	_, err = m.db.Apply(context.Background(), []core.Op{{Op: "set", Key: key, Data: map[string]any{"title": "x"}}}, "")
	t.Errorf("apply error: %v", err)
}
