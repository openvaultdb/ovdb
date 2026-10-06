package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dal-go/dalgo2http"
	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"
	"github.com/openvaultdb/openvaultdb-go/pkg/mount"
	"github.com/openvaultdb/openvaultdb-go/pkg/providerreads"
	"github.com/openvaultdb/openvaultdb-go/pkg/schema"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"
)

func syntheticProviderFixture(t *testing.T) (string, serveDeps, *int, license.SourceRight, server.ProviderReadProfile) {
	t.Helper()
	path := writeManifest(t, t.TempDir(), "ecb.yaml", strings.Replace(serveHTTPManifest, "retention: none", `retention: none, license: {url: "https://example.org/synthetic-terms"}`, 1))
	// Production mount freezes the independently expected rights without a read.
	db, err := mount.File(path)
	if err != nil {
		t.Fatal(err)
	}
	right, err := db.SourceRight("synthetic-cli", nil, "daily")
	_ = db.Close()
	if err != nil || right == nil {
		t.Fatal(right, err)
	}
	digest, err := providerreads.RightsDigest(*right)
	if err != nil {
		t.Fatal(err)
	}
	profile := server.ProviderReadProfile{Collection: "daily", Binding: providerreads.Binding{
		ProviderSourceID: "provider:synthetic/FxReferenceQuote", RightsSourceID: right.SourceID,
		ResourceID: "ecb-daily", DefinitionDigest: strings.Repeat("a", 64), DecoderDigest: strings.Repeat("b", 64), RightsDigest: digest,
	}}
	calls := new(int)
	deps := defaultServeDeps()
	deps.mountFile = func(path string) (*core.Database, error) {
		m, err := manifest.Load(path)
		if err != nil {
			return nil, err
		}
		driver, err := dalgo2http.NewDB(dalgo2http.Config{Mode: dalgo2http.ModeLive,
			Collections: []dalgo2http.Collection{{Name: "daily", URLTemplate: manifest.ECBDailyURL, Decoder: dalgo2http.DecoderECBEuroFXRef, KeyField: "currency", Timeout: 10 * time.Second, ClientSideFilter: true}},
			Client: &http.Client{Transport: serveHTTPTransport(func(r *http.Request) (*http.Response, error) {
				*calls++
				if r.URL.String() != manifest.ECBDailyURL || r.Header.Get("Cache-Control") != "no-store, no-cache" {
					t.Fatal("provider evidence changed the fixed no-store request")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/xml"}, "Etag": {`"synthetic"`}}, Body: io.NopCloser(strings.NewReader(serveHTTPXML))}, nil
			})}})
		if err != nil {
			return nil, err
		}
		return core.Open(m, driver, []schema.Mode{schema.ModeStrict}, "")
	}
	return path, deps, calls, *right, profile
}

func writeProviderProfiles(t *testing.T, profile server.ProviderReadProfile) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"ecb": map[string]any{"collection": profile.Collection, "binding": profile.Binding}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "synthetic-admission.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestServeHTTPProviderReadsExplicitOptIn(t *testing.T) {
	for _, optIn := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "opt-in"}[optIn], func(t *testing.T) {
			temp := isolatedServeTemp(t)
			path, deps, calls, right, profile := syntheticProviderFixture(t)
			args := []string{"--manifest", path, "--read-only", "--server-id", "synthetic-cli"}
			if optIn {
				args = append(args, "--provider-read-profiles", writeProviderProfiles(t, profile))
			}
			_, err := serveRun(t, deps, args, func(h http.Handler) {
				meta := serveHTTPRequest(h, "GET", "/v1/databases/ecb", "", nil)
				if meta.Code != 200 || strings.Contains(meta.Body.String(), providerreads.Format) != optIn || *calls != 0 {
					t.Fatalf("discovery format/admission triggered a read: %d %s", meta.Code, meta.Body)
				}
				for _, route := range []struct {
					path, body string
					empty      bool
				}{
					{"/v1/databases/ecb/query", `{"collection":"daily"}`, false},
					{"/v1/databases/ecb/query", `{"collection":"daily","where":[{"field":"currency","op":"==","value":"ZZZ"}]}`, true},
					{"/v1/databases/ecb/dtql", "from: {name: daily}\n", false},
					{"/v1/databases/ecb/dtql", "from: {name: daily}\nwhere: {op: '==', left: {field: currency}, right: {value: ZZZ}}\n", true},
				} {
					before := *calls
					w := serveHTTPRequest(h, "POST", route.path, route.body, nil)
					var result struct {
						Rights   []license.SourceRight `json:"sourceRights"`
						Used     []string              `json:"usedSourceIds"`
						Evidence json.RawMessage       `json:"providerReads"`
						Records  []struct {
							Data map[string]any `json:"data"`
						} `json:"records"`
					}
					if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &result) != nil || *calls != before+1 {
						t.Fatalf("query/evidence made extra reads: %d %s", w.Code, w.Body)
					}
					if route.empty && len(result.Records) != 0 || !route.empty && (len(result.Records) != 1 || result.Records[0].Data["rate"] != "001.23000") {
						t.Fatal("native result changed")
					}
					if !optIn {
						if len(result.Evidence) != 0 {
							t.Fatal("legacy CLI emitted unconfigured provider binding")
						}
						continue
					}
					observed, err := providerreads.Decode(result.Evidence)
					if err != nil {
						t.Fatal(err)
					}
					// v0.18.0 chooses execution ID during the read. This checks
					// wire integrity, not independently frozen caller-ID admission.
					plan := providerreads.Plan{Execution: providerreads.Execution{ID: observed.Execution.ID, Mode: "proxy", ExecutorID: "synthetic-cli"}, Bindings: []providerreads.Binding{profile.Binding},
						Requests: []providerreads.Request{{ResourceID: profile.Binding.ResourceID, Method: "GET", UpstreamURL: manifest.ECBDailyURL, Params: map[string]any{}}}, SourceRights: []license.SourceRight{right}, MaxReads: new(1), MaxMetadataBytes: providerreads.MaxMetadataBytes}
					metadata := providerreads.Metadata{SourceRights: result.Rights, UsedSourceIDs: result.Used, ProviderReads: &observed}
					if err := providerreads.ValidateMetadata(metadata, plan, []string{right.SourceID}); err != nil {
						t.Fatal(err)
					}
					if len(observed.Reads) != 1 || len(observed.Usage) != 1 {
						t.Fatal("missing actual read/usage for result, including zero rows")
					}
					read := observed.Reads[0]
					hash := sha256.Sum256([]byte(serveHTTPXML))
					if read.SHA256 != hex.EncodeToString(hash[:]) || read.Bytes != int64(len(serveHTTPXML)) || read.ReferenceDate != "2037-02-03" || read.ETag != `"synthetic"` || strings.Contains(string(result.Evidence), "001.23000") || strings.Contains(string(result.Evidence), "g:Envelope") {
						t.Fatal("provider evidence mismatch or source bytes in metadata")
					}
					metadata.SourceRights = nil
					if providerreads.ValidateMetadata(metadata, plan, []string{right.SourceID}) == nil {
						t.Fatal("wire validator admitted altered response rights")
					}
				}
				before := *calls
				w := serveHTTPRequest(h, "POST", "/v1/databases/ecb/query", `{"collection":"daily"}`, http.Header{"OVDB-Page-Size": {"1"}})
				if w.Code != 422 || *calls != before || len(snapshotFiles(t, temp)) != 0 {
					t.Fatal("opt-in bypassed no-retention refusal")
				}
			})
			if err != nil || len(snapshotFiles(t, temp)) != 0 {
				t.Fatal(err)
			}
		})
	}
}

func TestServeHTTPProviderReadsRefusesInvalidAdmissionBeforeRead(t *testing.T) {
	isolatedServeTemp(t)
	path, deps, calls, _, profile := syntheticProviderFixture(t)
	for _, change := range []func(*server.ProviderReadProfile){
		func(p *server.ProviderReadProfile) { p.Binding.RightsDigest = strings.Repeat("c", 64) },
		func(p *server.ProviderReadProfile) { p.Binding.RightsSourceID = "ovdb:other/ecb/daily" },
		func(p *server.ProviderReadProfile) { p.Collection = "undeclared" },
	} {
		invalid := profile
		change(&invalid)
		_, err := serveRun(t, deps, []string{"--manifest", path, "--server-id", "synthetic-cli", "--provider-read-profiles", writeProviderProfiles(t, invalid)}, func(http.Handler) { t.Fatal("served invalid admission") })
		if err == nil || *calls != 0 {
			t.Fatal("invalid admission started or read")
		}
	}
}

func TestServeHTTPProviderProfilesConfigFailsBeforeMount(t *testing.T) {
	deps := defaultServeDeps()
	deps.mountFile = func(string) (*core.Database, error) {
		t.Fatal("mounted before invalid config refusal")
		return nil, nil
	}
	for _, body := range []string{"null", "{}", `{"ecb":{"unknown":true}}`, `{} {}`, strings.Repeat(" ", providerreads.MaxMetadataBytes+1)} {
		path := filepath.Join(t.TempDir(), "bad-config.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := serveRun(t, deps, []string{"--manifest", "must-not-mount.yaml", "--provider-read-profiles", path}, func(http.Handler) { t.Fatal("served invalid config") })
		if err == nil {
			t.Fatal("accepted invalid config")
		}
	}
}
