package localserver

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dal-go/record"
	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/mount"
)

var requestID = regexp.MustCompile(`"requestId":"[0-9a-f]+"`)

// withoutRequestID drops the per-request id openvaultdb-go puts in access
// decisions, the one part two identical requests never share.
func withoutRequestID(body string) string { return requestID.ReplaceAllString(body, `"requestId":""`) }

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mountTest(t *testing.T, manifest string) *core.Database {
	t.Helper()
	db, err := mount.File(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// todoDatabase is a schemaless inGitDB database "todo".
func todoDatabase(t *testing.T) map[string]*core.Database {
	t.Helper()
	manifest := filepath.Join(t.TempDir(), "todo.yaml")
	writeTestFile(t, manifest, "database: {id: todo, schema_mode: schemaless}\nstorage: {engine: ingitdb, path: ./data}\n")
	return map[string]*core.Database{"todo": mountTest(t, manifest)}
}

// aclDatabase is a strict SQLite database "crm" with acl.enabled and a
// default-deny policy that lets role "reader" see only Irish customers.
func aclDatabase(t *testing.T) map[string]*core.Database {
	t.Helper()
	dir := t.TempDir()
	manifest := filepath.Join(dir, "crm.yaml")
	text := `database: {id: crm, schema_mode: strict}
storage: {engine: sqlite, path: crm.sqlite}
schemas:
  collections:
    customers:
      fields:
        name: {type: string}
        country: {type: string}
`
	writeTestFile(t, manifest, text)
	seed := mountTest(t, manifest)
	for id, country := range map[string]string{"01": "IE", "02": "US"} {
		if _, err := seed.Apply(context.Background(), []core.Op{{Op: "insert", Key: record.NewKeyWithID("customers", id),
			Data: map[string]any{"name": "Customer " + id, "country": country}}}, "seed"); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(dir, "policy.yaml"), `apiVersion: dtql.org/access/v1
kind: AccessPolicy
metadata:
  name: irish-readers
  visibility: private
target:
  database: crm
composition: dalgo-hierarchical-v1
default: deny
ruleSets:
  reader:
    - path: /customers
      rules:
        - id: read-irish
          effect: allow
          operations: [query, get]
          where:
            op: "=="
            left: {field: country}
            right: {value: IE}
          fields: [id, name]
bindings:
  roles:
    reader: [reader]
`)
	writeTestFile(t, manifest, text+"acl:\n  enabled: true\n  policies: [policy.yaml]\n")
	return map[string]*core.Database{"crm": mountTest(t, manifest)}
}

// Plan task 7 (S7): with acl.enabled, a console session forwarded as the
// owner must behave exactly like the instance secret, request for request.
// The test also records the answer to "do database policies bind the
// owner?" — see TestACLBindsTheOwner.
func TestOwnerForwardingMatchesInstanceSecretUnderACL(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(o *Options) { o.Databases = aclDatabase(t) })
	session := f.signIn(t)
	for _, r := range []request{
		{path: "/v1/databases"},
		{path: "/v1/databases/crm"},
		{path: "/v1/databases/crm/records/customers/01"},
		{path: "/v1/databases/crm/records/customers/02"},
		{path: "/v1/databases/crm/records/customers"},
		{method: http.MethodPost, path: "/v1/databases/crm/dtql", contentType: "application/yaml", body: "from: {name: customers}\n"},
		{path: "/v1/databases/crm/access/layers"},
		{method: http.MethodPut, path: "/v1/databases/crm/records/customers/03", body: `{"data":{"name":"Customer 03","country":"FR"}}`},
	} {
		asSecret, asSession := r, r
		asSecret.bearer = testSecret
		asSession.cookie = session
		if r.method != "" && r.method != http.MethodGet {
			asSession.header = sameOrigin(testHost)
		}
		// Writes run twice; compare the second pair so both see the same state.
		secret := f.do(t, asSecret)
		viaSession := f.do(t, asSession)
		if r.method == http.MethodPut {
			secret = f.do(t, asSecret)
		}
		if secret.Code != viaSession.Code || withoutRequestID(secret.Body.String()) != withoutRequestID(viaSession.Body.String()) {
			t.Errorf("%s %s: secret %d %s\nsession %d %s", r.method, r.path, secret.Code, secret.Body, viaSession.Code, viaSession.Body)
		}
	}
}

// TestACLBindsTheOwner records openvaultdb-go v0.5.1's answer: the owner
// token is administrative authority, and database policies still apply to
// its data reads (auth.Config.OwnerToken: "database policies still apply").
// A default-deny policy with no rule for the owner's (empty) role set hides
// every customer from the owner too — through the CLI's secret and the
// console alike: DTQL is 403 access_denied and a record GET is 404
// resource_unavailable.
func TestACLBindsTheOwner(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(o *Options) { o.Databases = aclDatabase(t) })
	rec := f.do(t, request{method: http.MethodPost, path: "/v1/databases/crm/dtql", contentType: "application/yaml",
		body: "from: {name: customers}\n", bearer: testSecret})
	get := f.do(t, request{path: "/v1/databases/crm/records/customers/01", bearer: testSecret})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "access_denied") ||
		get.Code != http.StatusNotFound || strings.Contains(get.Body.String(), "Customer 01") {
		t.Errorf("policies did not bind the owner: dtql %d %s; get %d %s", rec.Code, rec.Body, get.Code, get.Body)
	}
}
