package setup

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"
	"github.com/openvaultdb/openvaultdb-go/pkg/mount"
	"github.com/openvaultdb/openvaultdb-go/pkg/schema"
)

// mountProbe mounts a manifest for engine in mode and returns the modes the
// engine reports as supported: all of them when the mount succeeds in each.
func mountProbe(t *testing.T, engine string, mode schema.Mode) error {
	t.Helper()
	dir := t.TempDir()
	var storage string
	switch engine {
	case EngineFirestore:
		storage = "  firestore:\n    project: probe\n"
	case EnginePostgres, EngineMySQL:
		storage = ""
	default:
		storage = "  path: " + filepath.ToSlash(filepath.Join(dir, "data")) + "\n"
	}
	yaml := "database:\n  id: probe\n  schema_mode: " + string(mode) + "\nstorage:\n  engine: " + engine + "\n" + storage
	if mode == schema.ModeStrict {
		yaml += "schemas:\n  collections:\n    items:\n      fields:\n        title: {type: string}\n"
	}
	path := filepath.Join(dir, "probe.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Load(path); err != nil {
		t.Fatalf("%s: the manifest parser rejects %s: %v", engine, yaml, err)
	}
	db, err := mount.FileWithOptions(path, mount.Options{CatalogueDir: dir, SkipGitIdentity: true})
	if db != nil {
		_ = db.Close()
	}
	return err
}

// AC:catalogue-matches-binary: the catalogue has exactly the engines the
// linked openvaultdb-go mounts, with the schema modes each mount accepts.
func TestCatalogueMatchesBinary(t *testing.T) {
	t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:1") // Firestore mounts without credentials or a network call

	// The mount names every engine it supports when given one it does not.
	err := mountProbe(t, "no-such-engine", schema.ModeSchemaless)
	supported := regexp.MustCompile(`supported: ([a-z, ]+)\)`).FindStringSubmatch(errString(err))
	if supported == nil {
		t.Fatalf("mount error does not list supported engines: %v", err)
	}
	mountable := strings.Split(supported[1], ", ")
	slices.Sort(mountable)
	var catalogue []string
	for _, engine := range Engines() {
		catalogue = append(catalogue, engine.ID)
	}
	sorted := slices.Sorted(slices.Values(catalogue))
	if !slices.Equal(sorted, mountable) || !slices.Equal(sorted, []string{"firestore", "ingitdb", "mysql", "postgres", "sqlite"}) {
		t.Errorf("catalogue %v, mount supports %v", sorted, mountable)
	}

	// Schema modes: probed through a real mount where the engine needs no
	// running server. PostgreSQL and MySQL connect before checking the mode,
	// so their strict-only support is asserted from the mount's own error.
	for _, engine := range Engines() {
		var modes []string
		for _, mode := range []schema.Mode{schema.ModeStrict, schema.ModePartial, schema.ModeSchemaless} {
			err := mountProbe(t, engine.ID, mode)
			var incompatible *core.ModeCompatibilityError
			switch {
			case err == nil:
				modes = append(modes, string(mode))
			case errors.As(err, &incompatible):
			case engine.ID == EnginePostgres || engine.ID == EngineMySQL:
				// No server: the mount fails on the connection string first.
				if !strings.Contains(err.Error(), "DSN not set") {
					t.Errorf("%s %s: unexpected mount error %v", engine.ID, mode, err)
				}
			default:
				t.Errorf("%s %s: unexpected mount error %v", engine.ID, mode, err)
			}
		}
		if engine.ID == EnginePostgres || engine.ID == EngineMySQL {
			modes = []string{"strict"}
		}
		if !slices.Equal(modes, engine.SchemaModes) {
			t.Errorf("%s: catalogue modes %v, mount accepts %v", engine.ID, engine.SchemaModes, modes)
		}
		if guided := engine.ID == EngineInGitDB || engine.ID == EngineSQLite; guided != (engine.Setup == SetupGuided) || guided != engine.Pinned {
			t.Errorf("%s: setup %s pinned %v", engine.ID, engine.Setup, engine.Pinned)
		}
		if engine.Setup == SetupManifest && (len(engine.ManifestSteps) != 3 || engine.ManifestSteps[0].Command != "ovdb init --engine "+engine.ID+" --id <name>") {
			t.Errorf("%s manifest steps = %+v", engine.ID, engine.ManifestSteps)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// AC:pinned-then-alphabetical.
func TestCatalogueOrderAndFilter(t *testing.T) {
	t.Parallel()
	ids := func(engines []Engine) []string {
		var out []string
		for _, engine := range engines {
			out = append(out, engine.ID)
		}
		return out
	}
	if got := ids(Engines()); !slices.Equal(got, []string{"ingitdb", "sqlite", "firestore", "mysql", "postgres"}) {
		t.Errorf("order = %v", got)
	}
	for filter, want := range map[string][]string{
		"sql":       {"sqlite", "mysql", "postgres"},
		"SQL":       {"sqlite", "mysql", "postgres"},
		"git":       {"ingitdb"},
		"google":    {"firestore"},
		"":          {"ingitdb", "sqlite", "firestore", "mysql", "postgres"},
		"no match!": nil,
	} {
		if got := ids(FilterEngines(Engines(), filter)); !slices.Equal(got, want) {
			t.Errorf("filter %q = %v, want %v", filter, got, want)
		}
	}
}
