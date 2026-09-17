package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openvaultdb/openvaultdb-go/pkg/auth"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/setup"
)

const owner = "owner"

type fixture struct {
	dirs     paths.Dirs
	data     *server.Server
	registry *setup.Registry
	service  *Service
	seedErr  error
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{dirs: paths.Dirs{Home: filepath.Join(base, "home"), Runtime: filepath.Join(base, "run"), Data: filepath.Join(base, "data")}}
	for _, dir := range []string{f.dirs.Home, f.dirs.Runtime} {
		if err := paths.EnsurePrivateDir(dir); err != nil {
			t.Fatal(err)
		}
	}
	f.data = server.New("test", nil, server.WithAuth(&auth.Config{OwnerToken: owner}))
	registry, err := setup.OpenRegistry(f.dirs, f.data, nil, setup.RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	registry.MountAll(context.Background())
	f.registry = registry
	f.service = &Service{Dirs: f.dirs, Registry: registry, Seed: f.seed,
		Now: func() time.Time { return time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC) }}
	return f
}

func (f *fixture) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+owner)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.data.Handler().ServeHTTP(recorder, request)
	return recorder
}

// seed is the local server's seeder in miniature: one batch through /v1.
func (f *fixture) seed(_ context.Context, id string, ops []Op) error {
	if f.seedErr != nil {
		return f.seedErr
	}
	type op struct {
		Op   string         `json:"op"`
		Key  string         `json:"key"`
		Data map[string]any `json:"data"`
	}
	var batch struct {
		Ops []op `json:"ops"`
	}
	for _, o := range ops {
		batch.Ops = append(batch.Ops, op{"insert", strings.TrimPrefix(o.Path, "/"), o.Data})
	}
	body, _ := json.Marshal(batch)
	request := httptest.NewRequest(http.MethodPost, "/v1/databases/"+id+"/batch", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+owner)
	recorder := httptest.NewRecorder()
	f.data.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		return errors.New(recorder.Body.String())
	}
	return nil
}

type item struct {
	Key  string         `json:"key"`
	Data map[string]any `json:"data"`
}

func (f *fixture) items(t *testing.T, id, list string) map[string]map[string]any {
	t.Helper()
	response := f.do(t, http.MethodPost, "/v1/databases/"+id+"/query", `{"collection":"items","parent":"lists/`+list+`"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("query %s: %d %s", list, response.Code, response.Body)
	}
	var body struct{ Records []item }
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, r := range body.Records {
		out[r.Key[strings.LastIndex(r.Key, "/")+1:]] = r.Data
	}
	return out
}

func TestInspectBeforeAndAfterInstall(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dirs := paths.Dirs{Home: filepath.Join(base, "home"), Data: filepath.Join(base, "data")}
	data := dirs.Data
	doc := Inspect(dirs, []setup.Database{{ID: "notes", Engine: setup.EngineInGitDB, Location: filepath.Join(data, "notes")}})
	if doc.Installed || doc.Location != filepath.Join(data, "demos", "todo") || doc.AppPath != "/apps/todo/" ||
		len(doc.Next) != 1 || doc.Next[0].Command != "ovdb demo install --yes" {
		t.Errorf("not installed = %+v", doc)
	}
	databases := []setup.Database{
		{ID: "todo-demo", Engine: setup.EngineInGitDB, Location: filepath.Join(data, "demos", "todo-demo"), State: setup.MountMounted},
		{ID: "todo", Engine: setup.EngineInGitDB, Location: filepath.Join(data, "todo")},
	}
	// Folders alone make nothing a demo (review F1).
	if doc := Inspect(dirs, databases); doc.Installed {
		t.Errorf("unrecorded database taken for the demo: %+v", doc)
	}
	if err := setup.RecordDemo(dirs.Home, setup.DemoRecord{App: App, Database: "todo-demo", Location: filepath.Join(data, "demos", "todo-demo")}); err != nil {
		t.Fatal(err)
	}
	doc = Inspect(dirs, databases)
	if !doc.Installed || doc.Database != "todo-demo" || doc.State != setup.MountMounted {
		t.Errorf("installed = %+v", doc)
	}
	var commands []string
	for _, n := range doc.Next {
		commands = append(commands, n.Label+"|"+n.Command)
	}
	if !slices.Equal(commands, []string{"Open TODO app|ovdb demo open", "Install TODO AI skill (ask the person first)|ovdb skills install todo-demo", "Done|"}) {
		t.Errorf("next = %v", commands)
	}
	// The record names a place: the same id elsewhere is not the demo.
	moved := []setup.Database{{ID: "todo-demo", Engine: setup.EngineInGitDB, Location: filepath.Join(data, "todo-demo")}}
	if doc := Inspect(dirs, moved); doc.Installed {
		t.Errorf("same id elsewhere taken for the demo: %+v", doc)
	}
}

// Review F1: a user's database in a folder named demos is not the demo, and
// installing the demo still installs it.
func TestUserDatabaseInADemosFolderIsNotTheDemo(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	notes := filepath.Join(f.dirs.Data, "work", "demos", "notes")
	if _, err := f.registry.Create(setup.CreateRequest{ID: "notes", Engine: setup.EngineInGitDB, Path: notes}); err != nil {
		t.Fatal(err)
	}
	if response := f.do(t, http.MethodPut, "/v1/databases/notes/records/customers/acme", `{"data":{"name":"ACME"}}`); response.Code >= 300 {
		t.Fatalf("add customer: %d %s", response.Code, response.Body)
	}
	list, _ := f.registry.List()
	if doc := Inspect(f.dirs, list); doc.Installed {
		t.Fatalf("notes taken for the demo: %+v", doc)
	}
	if status := setup.NewDemoStatus(f.dirs, list); status.Installed {
		t.Errorf("status = %+v", status)
	}
	doc, err := f.service.Install(context.Background(), InstallRequest{})
	if err != nil || doc.AlreadyInstalled || doc.Database != "todo" {
		t.Fatalf("install = %+v, %v", doc, err)
	}
	if buy := f.items(t, "todo", "to-buy"); len(buy) != 3 {
		t.Errorf("to-buy = %v", buy)
	}
}

// Review F2: installs run one at a time, so every one returns only once the
// lists exist, however many arrive together.
func TestConcurrentInstallsReturnOnceTheListsExist(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	seed := f.service.Seed
	f.service.Seed = func(ctx context.Context, id string, ops []Op) error {
		time.Sleep(150 * time.Millisecond)
		return seed(ctx, id, ops)
	}
	var wg sync.WaitGroup
	results := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doc, err := f.service.Install(context.Background(), InstallRequest{})
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			// What an agent does right after exit 0.
			results <- fmt.Sprintf("%v %d", doc.AlreadyInstalled, len(f.items(t, "todo", "to-buy")))
		}()
	}
	wg.Wait()
	close(results)
	fresh := 0
	for result := range results {
		switch result {
		case "false 3":
			fresh++
		case "true 3":
		default:
			t.Errorf("install = %s", result)
		}
	}
	if fresh != 1 {
		t.Errorf("%d installs created the demo, want 1", fresh)
	}
}

// Review F5: the demo's own folder, left behind by `ovdb databases remove`, is
// served again with its data; someone else's files there are still refused.
func TestReinstallAfterRemoveReconnectsTheFolder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if _, err := f.service.Install(context.Background(), InstallRequest{}); err != nil {
		t.Fatal(err)
	}
	if response := f.do(t, http.MethodPut, "/v1/databases/todo/records/lists/to-buy/items/tea", `{"data":{"title":"Tea","done":false}}`); response.Code >= 300 {
		t.Fatalf("add Tea: %d %s", response.Code, response.Body)
	}
	if _, err := f.registry.Remove(context.Background(), "todo"); err != nil {
		t.Fatal(err)
	}
	if list, _ := f.registry.List(); Inspect(f.dirs, list).Installed {
		t.Fatal("still installed after remove")
	}
	doc, err := f.service.Install(context.Background(), InstallRequest{})
	if err != nil || !doc.AlreadyInstalled || doc.Database != "todo" || doc.State != setup.MountMounted {
		t.Fatalf("reinstall = %+v, %v", doc, err)
	}
	if buy := f.items(t, "todo", "to-buy"); buy["tea"]["title"] != "Tea" || len(buy) != 4 {
		t.Errorf("to-buy after reconnect = %v", buy)
	}
	if list, _ := f.registry.List(); !Inspect(f.dirs, list).Installed {
		t.Error("not installed after reconnect")
	}
}

// todo-demo AC:fresh-install-creates-data and AC:reinstall-keeps-changes.
func TestInstallIsIdempotentAndKeepsChanges(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	doc, err := f.service.Install(context.Background(), InstallRequest{})
	if err != nil {
		t.Fatal(err)
	}
	location := filepath.Join(f.dirs.Data, "demos", "todo")
	if !doc.Installed || doc.AlreadyInstalled || doc.Database != "todo" || doc.Location != location || doc.State != setup.MountMounted {
		t.Fatalf("install = %+v", doc)
	}
	buy := f.items(t, "todo", "to-buy")
	for _, id := range []string{"milk", "bananas", "coffee"} {
		if buy[id]["done"] != false || buy[id]["added_at"] == nil {
			t.Errorf("to-buy %s = %v", id, buy[id])
		}
	}
	if buy["milk"]["title"] != "Milk" || len(buy) != 3 {
		t.Errorf("to-buy = %v", buy)
	}
	if watch := f.items(t, "todo", "to-watch"); len(watch) != 2 || watch["the-matrix"]["title"] != "The Matrix" || watch["interstellar"]["title"] != "Interstellar" {
		t.Errorf("to-watch = %v", watch)
	}
	if response := f.do(t, http.MethodGet, "/v1/databases/todo/records/lists/to-watch", ""); !strings.Contains(response.Body.String(), `"title":"To watch"`) {
		t.Errorf("list record = %s", response.Body)
	}
	if _, err := os.Stat(filepath.Join(location, ".git")); err != nil {
		t.Errorf("no Git history: %v", err)
	}

	if response := f.do(t, http.MethodPut, "/v1/databases/todo/records/lists/to-buy/items/tea", `{"data":{"title":"Tea","done":false}}`); response.Code >= 300 {
		t.Fatalf("add Tea: %d %s", response.Code, response.Body)
	}
	if response := f.do(t, http.MethodDelete, "/v1/databases/todo/records/lists/to-buy/items/coffee", ""); response.Code >= 300 {
		t.Fatalf("delete coffee: %d %s", response.Code, response.Body)
	}
	for _, request := range []InstallRequest{{}, {ID: "todo"}, {ID: "todo", Path: location}} {
		again, err := f.service.Install(context.Background(), request)
		if err != nil || !again.AlreadyInstalled || again.Database != "todo" {
			t.Errorf("install %+v again = %+v, %v", request, again, err)
		}
	}
	buy = f.items(t, "todo", "to-buy")
	if buy["tea"]["title"] != "Tea" || buy["coffee"] != nil {
		t.Errorf("reinstall changed data: %v", buy)
	}
}

// todo-demo AC:conflicting-todo-refused, and a demo folder with other files.
func TestInstallRefusesConflicts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if _, err := f.registry.Create(setup.CreateRequest{ID: "todo", Engine: setup.EngineInGitDB, Path: filepath.Join(f.dirs.Data, "todo")}); err != nil {
		t.Fatal(err)
	}
	before, _ := f.registry.List()
	_, err := f.service.Install(context.Background(), InstallRequest{})
	e := envelope.As(err)
	if e == nil || e.Code != envelope.AlreadyExists || e.Message != "Couldn't install the TODO demo" ||
		len(e.Next) == 0 || e.Next[0].Command != "ovdb demo install --id todo-demo" {
		t.Fatalf("conflict = %#v", err)
	}
	if after, _ := f.registry.List(); len(after) != len(before) {
		t.Errorf("databases changed: %+v", after)
	}
	if _, err := os.Stat(filepath.Join(f.dirs.Data, "demos")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("demos folder created: %v", err)
	}

	// The suggested id installs.
	doc, err := f.service.Install(context.Background(), InstallRequest{ID: "todo-demo"})
	if err != nil || doc.Database != "todo-demo" || doc.Location != filepath.Join(f.dirs.Data, "demos", "todo-demo") {
		t.Fatalf("install --id todo-demo = %+v, %v", doc, err)
	}
	// With no id, the installed demo is found under its other id.
	if again, err := f.service.Install(context.Background(), InstallRequest{}); err != nil || !again.AlreadyInstalled || again.Database != "todo-demo" {
		t.Errorf("install again = %+v, %v", again, err)
	}

	// A demos/<id> folder with someone else's files.
	other := filepath.Join(f.dirs.Data, "demos", "mine")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Install(context.Background(), InstallRequest{ID: "mine"})
	if e := envelope.As(err); e == nil || e.Code != envelope.LocationNotEmpty || !strings.Contains(e.Reason, other) {
		t.Errorf("folder not empty = %#v", err)
	}
	if entries, _ := os.ReadDir(other); len(entries) != 1 {
		t.Errorf("folder changed: %v", entries)
	}
}

func TestInstallUndoesAFailedSeed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.seedErr = errors.New("disk full")
	_, err := f.service.Install(context.Background(), InstallRequest{})
	if e := envelope.As(err); e == nil || e.Code != envelope.StorageUnavailable || !strings.Contains(e.Reason, "disk full") {
		t.Fatalf("seed failure = %#v", err)
	}
	if list, _ := f.registry.List(); len(list) != 0 {
		t.Errorf("still registered: %+v", list)
	}
	if _, err := os.Stat(filepath.Join(f.dirs.Data, "demos", "todo")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("demo folder left behind: %v", err)
	}
	f.seedErr = nil
	if doc, err := f.service.Install(context.Background(), InstallRequest{}); err != nil || doc.AlreadyInstalled {
		t.Errorf("install after a failed one = %+v, %v", doc, err)
	}
}

func TestInstallValidatesTheNameAndPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.service.Install(context.Background(), InstallRequest{ID: "bad name"})
	if e := envelope.As(err); e == nil || e.Code != envelope.InvalidArgument || e.Message != "Couldn't install the TODO demo" ||
		e.Next[0].Command != "ovdb demo install --id <name>" {
		t.Errorf("bad name = %#v", err)
	}
	// A relative path is not a naming problem (review F9).
	_, err = f.service.Install(context.Background(), InstallRequest{ID: "pathy", Path: "rel/dir"})
	if e := envelope.As(err); e == nil || e.Code != envelope.InvalidArgument || len(e.Next) != 1 || e.Next[0].Command != "ovdb demo install --yes" {
		t.Errorf("relative path = %#v", err)
	}
}
