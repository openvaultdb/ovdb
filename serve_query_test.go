package main

// serve_query_test.go drives the real `ovdb serve` command in the test
// process (no listener, no child process): the command builds its server and
// hands it to a seam that, here, calls the handler directly. It proves what an
// operator gets from openvaultdb-go v0.13.0 through this binary: discovery of
// the query profile, a join over two collections, the PostgreSQL preview switch
// being off by default, and what a manifest mistake says. No test dials a
// server: the SQLite file is a temporary file, and the PostgreSQL mount is built
// over a driver that counts every call (and so proves none was made).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dal-go/dalgo/dal"
	"github.com/dal-go/dalgo/recordset"
	"github.com/spf13/cobra"

	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"
	"github.com/openvaultdb/openvaultdb-go/pkg/mount"
	"github.com/openvaultdb/openvaultdb-go/pkg/schema"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"
)

const shopManifest = `database:
  id: shop
  schema_mode: strict

storage:
  engine: sqlite
  path: ./shop.sqlite

schemas:
  collections:
    customers:
      fields:
        name: {type: string, required: true}
    orders:
      fields:
        customer_id: {type: string}
        total: {type: integer}
`

const postgresManifest = `database:
  id: ledger
  schema_mode: strict

storage:
  engine: postgres
  postgres:
    dsn_env: OVDB_TEST_NEVER_SET_DSN

schemas:
  collections:
    customers:
      fields:
        name: {type: string}
`

const mysqlManifest = `database:
  id: orders
  schema_mode: strict

storage:
  engine: mysql
  mysql:
    dsn_env: OVDB_TEST_NEVER_SET_DSN

schemas:
  collections:
    customers:
      fields:
        name: {type: string}
`

// serveRun runs `ovdb serve <args>` in this process. The command's own flags,
// mounting and options all run; the seam replaces only the listener, and calls
// use with the handler the command assembled while the databases are still
// mounted (the command closes them when it returns). It returns what the
// command printed and its error.
func serveRun(t *testing.T, deps serveDeps, args []string, use func(h http.Handler)) (string, error) {
	t.Helper()
	deps.run = func(_ *cobra.Command, srv *http.Server) error {
		use(srv.Handler)
		return nil
	}
	cmd := newServeCmdWith(deps)
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// call sends one request to the handler and returns the status and the body.
func call(t *testing.T, h http.Handler, method, target, contentType, body string) (int, string) {
	t.Helper()
	return callWith(t, h, method, target, contentType, body, nil)
}

// callWith is call with extra request headers.
func callWith(t *testing.T, h http.Handler, method, target, contentType, body string, headers map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func writeShop(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return writeManifest(t, dir, "shop.yaml", shopManifest)
}

func TestServeDiscoveryStatesTheQueryProfile(t *testing.T) {
	_, err := serveRun(t, defaultServeDeps(), []string{"--manifest", writeShop(t)}, func(handler http.Handler) {
		status, body := call(t, handler, http.MethodGet, "/.well-known/openvaultdb", "", "")
		if status != http.StatusOK {
			t.Fatalf("discovery = %d %s", status, body)
		}
		var doc struct {
			Databases []struct {
				ID           string          `json:"id"`
				Capabilities map[string]bool `json:"capabilities"`
			} `json:"databases"`
			Query struct {
				Endpoint string `json:"endpoint"`
				Features struct {
					Joins     []string `json:"joins"`
					Aggregate []string `json:"aggregates"`
				} `json:"features"`
				JoinEngines []string       `json:"joinEngines"`
				Limits      map[string]int `json:"limits"`
			} `json:"query"`
		}
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("discovery body: %v\n%s", err, body)
		}
		if doc.Query.Endpoint != "/v1/dtql" || strings.Join(doc.Query.Features.Joins, ",") != "inner,left" ||
			strings.Join(doc.Query.Features.Aggregate, ",") != "count,sum,avg,min,max" {
			t.Errorf("query block = %+v", doc.Query)
		}
		if strings.Join(doc.Query.JoinEngines, ",") != "sqlite,ingitdb" {
			t.Errorf("joinEngines = %v: a PostgreSQL mount is not a join engine by default", doc.Query.JoinEngines)
		}
		for name, want := range map[string]int{"maxSources": 8, "maxSubqueryDepth": 4, "maxLimit": 1000, "maxOffset": 10000, "maxResultRows": 1000, "timeoutMs": 10000} {
			if doc.Query.Limits[name] != want {
				t.Errorf("limits[%s] = %d, want %d", name, doc.Query.Limits[name], want)
			}
		}
		if len(doc.Databases) != 1 || doc.Databases[0].ID != "shop" || !doc.Databases[0].Capabilities["joins"] || !doc.Databases[0].Capabilities["aggregation"] {
			t.Errorf("databases = %+v", doc.Databases)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestServeAnswersAJoinAcrossTwoCollections(t *testing.T) {
	_, err := serveRun(t, defaultServeDeps(), []string{"--manifest", writeShop(t)}, func(handler http.Handler) {
		for _, w := range []struct{ path, data string }{
			{"customers/c1", `{"name":"Ada"}`},
			{"customers/c2", `{"name":"Grace"}`},
			{"orders/o1", `{"customer_id":"c1","total":10}`},
			{"orders/o2", `{"customer_id":"c1","total":20}`},
			{"orders/o3", `{"customer_id":"c2","total":5}`},
		} {
			if status, body := call(t, handler, http.MethodPut, "/v1/databases/shop/records/"+w.path, "application/json", `{"data":`+w.data+`}`); status != http.StatusNoContent {
				t.Fatalf("write %s = %d %s", w.path, status, body)
			}
		}
		status, body := call(t, handler, http.MethodPost, "/v1/dtql", "application/yaml", shopJoin)
		if status != http.StatusOK {
			t.Fatalf("join = %d %s", status, body)
		}
		var answer struct {
			Columns   []string `json:"columns"`
			Execution struct {
				Route string `json:"route"`
			} `json:"execution"`
			Records []struct {
				Key  *string        `json:"key"`
				Data map[string]any `json:"data"`
			} `json:"records"`
		}
		if err := json.Unmarshal([]byte(body), &answer); err != nil {
			t.Fatalf("join body: %v\n%s", err, body)
		}
		if strings.Join(answer.Columns, ",") != "id,customer,total" || len(answer.Records) != 3 || answer.Records[0].Key != nil ||
			answer.Records[0].Data["customer"] != "Ada" || answer.Records[2].Data["customer"] != "Grace" || answer.Records[1].Data["total"] != float64(20) {
			t.Errorf("columns %v, records %+v", answer.Columns, answer.Records)
		}
		if answer.Execution.Route == "" {
			t.Errorf("execution = %+v", answer.Execution)
		}

		// An aggregate over the join, one answer per customer.
		status, body = call(t, handler, http.MethodPost, "/v1/dtql", "application/yaml", shopGrouped)
		if status != http.StatusOK || !strings.Contains(body, `"revenue":30`) || !strings.Contains(body, `"revenue":5`) {
			t.Errorf("grouped = %d %s", status, body)
		}

		// first and last are not in the profile on any route.
		status, body = call(t, handler, http.MethodPost, "/v1/dtql", "application/yaml", shopFirst)
		if status != http.StatusBadRequest || !strings.Contains(body, "invalid_dtql") || !strings.Contains(body, "first") {
			t.Errorf("first = %d %s", status, body)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

const shopJoin = `from:
  database: shop
  name: orders
  alias: o
  joins:
    - type: inner
      from: {database: shop, name: customers, alias: c}
      on:
        - {left: {field: customer_id, source: o}, op: '==', right: {field: id, source: c}}
orderBy:
  - {field: id, source: o}
columns:
  - {field: id, source: o}
  - {field: name, source: c, as: customer}
  - {field: total, source: o}
`

const shopGrouped = `from:
  database: shop
  name: orders
  alias: o
  joins:
    - type: inner
      from: {database: shop, name: customers, alias: c}
      on:
        - {left: {field: customer_id, source: o}, op: '==', right: {field: id, source: c}}
groupBy:
  - {field: name, source: c}
orderBy:
  - {field: name, source: c}
columns:
  - {field: name, source: c}
  - {aggregate: {function: sum, args: [{field: total, source: o}]}, as: revenue}
`

const shopFirst = `from:
  database: shop
  name: orders
  alias: o
columns:
  - {aggregate: {function: first, args: [{field: total, source: o}]}, as: x}
`

const ledgerDocument = `from:
  database: ledger
  name: customers
  alias: c
columns:
  - {field: name, source: c}
`

// countingDB is a PostgreSQL driver that dials nothing: every call it gets is
// counted, and a method it does not list panics (nil embedded DB), so a
// structured query that reached the driver would show.
type countingDB struct {
	dal.DB
	queries int
}

func (c *countingDB) ExecuteQueryToRecordsReader(context.Context, dal.Query) (dal.RecordsReader, error) {
	c.queries++
	return nil, errors.New("the driver was reached")
}

func (c *countingDB) ExecuteQueryToRecordsetReader(context.Context, dal.Query, ...recordset.Option) (dal.RecordsetReader, error) {
	c.queries++
	return nil, errors.New("the driver was reached")
}

// fakeServerMount reads the manifest as the command does and opens it over a
// driver that dials nothing, as mount.File opens a postgres or mysql manifest
// over a connection. Any other engine goes to the real mount.
func fakeServerMount(driver *countingDB, mounted *int) func(string) (*core.Database, error) {
	return func(path string) (*core.Database, error) {
		m, err := manifest.Load(path)
		if err != nil {
			return nil, err
		}
		if m.Storage.Engine != "postgres" && m.Storage.Engine != "mysql" {
			return mount.File(path)
		}
		*mounted++
		return core.Open(m, driver, []schema.Mode{schema.ModeStrict}, "")
	}
}

func TestServePostgresQueriesAreOffByDefault(t *testing.T) {
	t.Setenv(core.PreviewPostgresQueriesEnv, "")
	if err := os.Unsetenv(core.PreviewPostgresQueriesEnv); err != nil {
		t.Fatal(err)
	}
	driver := &countingDB{}
	mounted := 0
	deps := defaultServeDeps()
	deps.mountFile = fakeServerMount(driver, &mounted)
	dir := t.TempDir()
	path := writeManifest(t, dir, "ledger.yaml", postgresManifest)

	_, err := serveRun(t, deps, []string{"--manifest", path}, func(handler http.Handler) {
		if mounted != 1 {
			t.Fatalf("the postgres manifest was mounted %d times", mounted)
		}
		if _, set := os.LookupEnv(core.PreviewPostgresQueriesEnv); set {
			t.Fatal("ovdb serve set the preview switch")
		}

		// The metadata says the database cannot be queried.
		status, body := call(t, handler, http.MethodGet, "/v1/databases/ledger", "", "")
		if status != http.StatusOK || !strings.Contains(body, `"query":false`) || !strings.Contains(body, `"dtql":false`) || !strings.Contains(body, `"joins":false`) {
			t.Errorf("metadata = %d %s", status, body)
		}
		// Every structured route answers with the library's refusal.
		const dtql = "from: {name: customers}\n"
		for name, request := range map[string]struct {
			method, target, contentType, body string
			headers                           map[string]string
		}{
			"per-database dtql": {http.MethodPost, "/v1/databases/ledger/dtql", "application/yaml", dtql, nil},
			"query":             {http.MethodPost, "/v1/databases/ledger/query", "application/json", `{"collection":"customers"}`, nil},
			"cross-database":    {http.MethodPost, "/v1/dtql", "application/yaml", "from: {database: ledger, name: customers}\n", nil},
			"paged dtql":        {http.MethodPost, "/v1/databases/ledger/dtql", "application/yaml", dtql, map[string]string{"OVDB-Page-Size": "10"}},
		} {
			status, body := callWith(t, handler, request.method, request.target, request.contentType, request.body, request.headers)
			if status != http.StatusNotImplemented || !strings.Contains(body, `"query_unsupported"`) || !strings.Contains(body, "postgres") {
				t.Errorf("%s = %d %s", name, status, body)
			}
		}
		if driver.queries != 0 {
			t.Errorf("the driver was reached %d times", driver.queries)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

// With the preview switch on (set by this test only, never by the command), a
// PostgreSQL mount hands a query of one collection to the driver and still
// refuses a relational document: postgres is not among the join engines of the
// server ovdb serve builds.
func TestServePostgresPreviewReachesTheDriverForOneCollectionOnly(t *testing.T) {
	t.Setenv(core.PreviewPostgresQueriesEnv, "1")
	driver := &countingDB{}
	mounted := 0
	deps := defaultServeDeps()
	deps.mountFile = fakeServerMount(driver, &mounted)
	_, err := serveRun(t, deps, []string{"--manifest", writeManifest(t, t.TempDir(), "ledger.yaml", postgresManifest)}, func(handler http.Handler) {
		status, body := call(t, handler, http.MethodPost, "/v1/databases/ledger/query", "application/json", `{"collection":"customers"}`)
		if status == http.StatusNotImplemented || driver.queries != 1 {
			t.Errorf("query of one collection = %d %s with %d driver calls, want the driver reached once", status, body, driver.queries)
		}
		status, body = call(t, handler, http.MethodPost, "/v1/dtql", "application/yaml", ledgerDocument)
		if status != http.StatusUnprocessableEntity || !strings.Contains(body, "join_engine_unsupported") || driver.queries != 1 {
			t.Errorf("relational document = %d %s with %d driver calls, want a refusal and no new call", status, body, driver.queries)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

// openvaultdb-go v0.9.0 had no rule for the field names of a write, so a record
// with a field named "due date" was stored on the default engine. v0.13.0
// refuses a top-level field that is not a plain name with 400 bad_request on
// every engine, before the adapter is called. Plain names, hyphens and dots
// are still written.
func TestServeRefusesAWriteWhoseFieldNameIsNotAPlainNameOnEveryEngine(t *testing.T) {
	ingitdbDir := t.TempDir()
	initGitRepo(t, ingitdbDir)
	for name, c := range map[string]struct{ manifest, collection string }{
		"sqlite":  {writeShop(t), "customers"},
		"ingitdb": {writeManifest(t, ingitdbDir, "todo.yaml", ingitdbManifest), "lists"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := serveRun(t, defaultServeDeps(), []string{"--manifest", c.manifest}, func(handler http.Handler) {
				db := map[string]string{"sqlite": "shop", "ingitdb": "todo"}[name]
				target := "/v1/databases/" + db + "/records/" + c.collection + "/x"
				for _, data := range []string{`{"due date":"2026-10-05"}`, `{"a;b":1}`, `{"name":"ok","due date":1}`} {
					status, body := call(t, handler, http.MethodPut, target, "application/json", `{"data":`+data+`}`)
					if status != http.StatusBadRequest || !strings.Contains(body, `"bad_request"`) {
						t.Errorf("PUT %s = %d %s, want 400 bad_request", data, status, body)
					}
				}
				status, body := call(t, handler, http.MethodPut, target, "application/json", `{"data":{"name":"ok"}}`)
				if status != http.StatusNoContent {
					t.Errorf("PUT of a plain name = %d %s, want 204", status, body)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServeMountMistakesAreNamedByTheLibrary(t *testing.T) {
	const head = "database:\n  id: bad\n  schema_mode: strict\n\nstorage:\n  engine: sqlite\n  path: ./bad.sqlite\n\n"
	for name, c := range map[string]struct{ manifest, want, absent string }{
		"empty":               {"# nothing\n", "the manifest is empty", ""},
		"wrong type":          {head + "schemas:\n  collections:\n    items:\n      fields: [title]\n", "line 12: a value of the wrong type", "title"},
		"two keys, one table": {head + "schemas:\n  collections:\n    \"\\\"Items\\\"\":\n      fields:\n        a: {type: string}\n    Items:\n      fields:\n        b: {type: string}\n", "conflicting collection names", ""},
		"bad variable name":   {"database:\n  id: pg\n  schema_mode: strict\n\nstorage:\n  engine: postgres\n  postgres:\n    dsn_env: \"BAD NAME\"\n\nschemas:\n  collections:\n    items:\n      fields:\n        a: {type: string}\n", "dsn_env", "BAD NAME"},
		"two documents":       {head + "---\nsecond: true\n", "exactly one YAML document", ""},
		"undecodable value":   {"database:\n  id: !!binary \"###\"\n", "could not be decoded", ""},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeManifest(t, dir, "bad.yaml", c.manifest)
			_, err := serveRun(t, defaultServeDeps(), []string{"--manifest", path}, func(http.Handler) { t.Error("the server must not run") })
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("serve = %v, want an error naming %q", err, c.want)
			}
			if err != nil && c.absent != "" && strings.Contains(err.Error(), c.absent) {
				t.Errorf("serve = %v: the error repeats %q from the manifest", err, c.absent)
			}
		})
	}
}

// A MySQL mount refuses every structured query whatever the preview switch
// says: the library's allow-list does not clear the engine. Run through the
// command, with the switch on, over a driver that counts calls.
func TestServeMySQLQueriesAreRefusedWhateverTheSwitchSays(t *testing.T) {
	t.Setenv(core.PreviewPostgresQueriesEnv, "1")
	driver := &countingDB{}
	mounted := 0
	deps := defaultServeDeps()
	deps.mountFile = fakeServerMount(driver, &mounted)
	_, err := serveRun(t, deps, []string{"--manifest", writeManifest(t, t.TempDir(), "orders.yaml", mysqlManifest)}, func(handler http.Handler) {
		if mounted != 1 {
			t.Fatalf("the mysql manifest was mounted %d times", mounted)
		}
		const dtql = "from: {name: customers}\n"
		for name, request := range map[string]struct {
			target, contentType, body string
			headers                   map[string]string
		}{
			"per-database dtql": {"/v1/databases/orders/dtql", "application/yaml", dtql, nil},
			"query":             {"/v1/databases/orders/query", "application/json", `{"collection":"customers"}`, nil},
			"cross-database":    {"/v1/dtql", "application/yaml", "from: {database: orders, name: customers}\n", nil},
			"paged dtql":        {"/v1/databases/orders/dtql", "application/yaml", dtql, map[string]string{"OVDB-Page-Size": "10"}},
		} {
			status, body := callWith(t, handler, http.MethodPost, request.target, request.contentType, request.body, request.headers)
			if status != http.StatusNotImplemented || !strings.Contains(body, `"query_unsupported"`) || !strings.Contains(body, "mysql") {
				t.Errorf("%s = %d %s, want 501 query_unsupported naming mysql", name, status, body)
			}
		}
		if driver.queries != 0 {
			t.Errorf("the driver was reached %d times", driver.queries)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestServeHelpSaysWhatPostgresAndMySQLMountsDo(t *testing.T) {
	for _, text := range []string{newServeCmd().Long, newInitCmd().Long} {
		for _, want := range []string{
			"preview", "off by default", core.PreviewPostgresQueriesEnv + "=1", "MySQL mounts refuse structured queries",
			// Both servers read the switch from their own environment.
			"ovdb serve, or the local server that ovdb databases connect uses", "which neither server lists",
			// What the preview does to the commands and routes an existing user has.
			"ovdb list and the web console's browse", "every document on /v1/dtql is relational", "a document of one plain collection is answered as before",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("help lacks %q:\n%s", want, text)
			}
		}
	}
}

// Every number in the help is the one the server enforces: the per-request
// limits are read from the discovery document of the real command, the
// concurrency and snapshot limits from the library's defaults, which the
// command does not change.
func TestServeHelpNamesTheQueryLimitsTheServerEnforces(t *testing.T) {
	var limits map[string]float64
	_, err := serveRun(t, defaultServeDeps(), []string{"--manifest", writeShop(t)}, func(handler http.Handler) {
		status, body := call(t, handler, http.MethodGet, "/.well-known/openvaultdb", "", "")
		var doc struct {
			Query struct {
				Limits map[string]float64 `json:"limits"`
			} `json:"query"`
		}
		if status != http.StatusOK || json.Unmarshal([]byte(body), &doc) != nil {
			t.Fatalf("discovery = %d %s", status, body)
		}
		limits = doc.Query.Limits
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(limits) != 13 {
		t.Fatalf("discovery lists %d limits, the help was written for 13: %v", len(limits), limits)
	}
	n := func(name string) int64 { return int64(limits[name]) }
	queue, snapshot := server.DefaultQueryLimits(), server.DefaultSnapshotLimits()
	for _, want := range []string{
		fmt.Sprintf("reads at most %d sources", n("maxSources")),
		fmt.Sprintf("with %d levels of subquery", n("maxSubqueryDepth")),
		fmt.Sprintf("takes limit up to %s and offset up to %s", grouped(n("maxLimit")), grouped(n("maxOffset"))),
		fmt.Sprintf("answers at most %s rows and %s.", grouped(n("maxResultRows")), mebibytes(n("maxResultBytes"))),
		fmt.Sprintf("runs for at most %d seconds", n("timeoutMs")/1000),
		fmt.Sprintf("reads at most %s rows and %s from its sources", grouped(n("maxSourceRows")), mebibytes(n("maxSourceBytes"))),
		fmt.Sprintf("holds at most %s rows and %s, and a grouping %s groups", grouped(n("maxInMemoryJoinRows")), mebibytes(n("maxInMemoryJoinBytes")), grouped(n("maxGroups"))),
		fmt.Sprintf("At most %d in-memory and %d database-side queries run at once", queue.InMemory, queue.Database),
		fmt.Sprintf("a paged /dtql snapshot is at most %s and %s rows, %d at a time", mebibytes(snapshot.Bytes), grouped(int64(snapshot.Rows)), snapshot.Slots),
		"GET /.well-known/openvaultdb lists the per-request limits as query.limits; the concurrency and snapshot limits are not listed there.",
	} {
		for _, text := range []string{newServeCmd().Long, newInitCmd().Long} {
			if !strings.Contains(text, want) {
				t.Errorf("help lacks %q:\n%s", want, text)
			}
		}
	}
}

func TestGroupedAndMebibytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 8: "8", 100: "100", 1000: "1,000", 10000: "10,000", 100000: "100,000", 1000000: "1,000,000"} {
		if got := grouped(n); got != want {
			t.Errorf("grouped(%d) = %q, want %q", n, got, want)
		}
	}
	if got := mebibytes(512 << 20); got != "512 MiB" {
		t.Errorf("mebibytes = %q", got)
	}
}

func TestReadmeSaysTheSameAsTheHelp(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(raw)
	for _, want := range []string{postgresPreviewHelp, queryLimitsHelp} {
		if !strings.Contains(readme, want) {
			t.Errorf("README.md lacks %q", want)
		}
	}
}

func TestServeFailsBeforeItRunsAnythingWhenAMountFails(t *testing.T) {
	// The seam is only the listener: a command that cannot mount its databases
	// returns the error and never asks to run.
	ran := false
	deps := defaultServeDeps()
	deps.run = func(*cobra.Command, *http.Server) error { ran = true; return nil }
	cmd := newServeCmdWith(deps)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--dir", filepath.Join(t.TempDir(), "missing")})
	if err := cmd.Execute(); err == nil || ran {
		t.Errorf("serve with a missing --dir = %v (ran %v)", err, ran)
	}
}

// fakeListener stands in for *http.Server: ListenAndServe blocks until
// Shutdown (or fails at once with listenErr), as the real one does.
type fakeListener struct {
	listenErr   error
	shutdownErr error
	done        chan struct{}
}

func newFakeListener() *fakeListener { return &fakeListener{done: make(chan struct{})} }

func (f *fakeListener) ListenAndServe() error {
	if f.listenErr != nil {
		return f.listenErr
	}
	<-f.done
	return http.ErrServerClosed
}

func (f *fakeListener) Shutdown(context.Context) error {
	close(f.done)
	return f.shutdownErr
}

func TestServeUntilReturnsTheListenerError(t *testing.T) {
	f := newFakeListener()
	f.listenErr = errors.New("address already in use")
	var out strings.Builder
	err := serveUntil(&out, "127.0.0.1:1", f, make(chan os.Signal))
	if err != f.listenErr || !strings.Contains(out.String(), "serving on http://127.0.0.1:1") || strings.Contains(out.String(), "shutting down") {
		t.Errorf("err = %v, out = %q", err, out.String())
	}
}

func TestServeUntilShutsDownOnASignal(t *testing.T) {
	for name, c := range map[string]struct {
		shutdownErr error
		wantErr     bool
	}{
		"clean":           {nil, false},
		"already closed":  {http.ErrServerClosed, false},
		"shutdown failed": {errors.New("requests did not finish"), true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeListener()
			f.shutdownErr = c.shutdownErr
			stop := make(chan os.Signal, 1)
			stop <- os.Interrupt
			var out strings.Builder
			err := serveUntil(&out, "127.0.0.1:1", f, stop)
			if (err != nil) != c.wantErr || !strings.Contains(out.String(), "shutting down...") {
				t.Errorf("err = %v, out = %q", err, out.String())
			}
		})
	}
}
