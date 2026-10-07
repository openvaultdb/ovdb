package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

func syntheticProviderFixture(t *testing.T, response ...serveHTTPTransport) (string, serveDeps, *atomic.Int32, license.SourceRight, server.ProviderReadProfile) {
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
	// Fabricated operator admission facts; the pin covers definition metadata,
	// never the changing upstream XML bytes or an inferred rights decision.
	right.EvidenceOrigin = "publisher-definition-verified"
	right.Pins = []license.Pin{{Role: "provider", Repository: "https://github.com/synthetic/provider", Revision: strings.Repeat("c", 40), Path: "ovdb.yaml", SHA256: strings.Repeat("a", 64), Bytes: 42}}
	right.Attribution = &license.Notice{Text: "Synthetic provider", URL: "https://example.org/"}
	right.FreeSource = &license.Notice{Text: "Synthetic original is free", URL: manifest.ECBDailyURL}
	right.Transformations = []string{"Synthetic XML restructured into rows"}
	digest, err := providerreads.RightsDigest(*right)
	if err != nil {
		t.Fatal(err)
	}
	profile := server.ProviderReadProfile{Collection: "daily", SourceRight: right, Binding: providerreads.Binding{
		ProviderSourceID: "provider:synthetic/FxReferenceQuote", RightsSourceID: right.SourceID,
		ResourceID: "ecb-daily", DefinitionDigest: strings.Repeat("a", 64), DecoderDigest: strings.Repeat("b", 64), RightsDigest: digest,
	}}
	calls := new(atomic.Int32)
	deps := defaultServeDeps()
	deps.mountFile = func(path string) (*core.Database, error) {
		m, err := manifest.Load(path)
		if err != nil {
			return nil, err
		}
		driver, err := dalgo2http.NewDB(dalgo2http.Config{Mode: dalgo2http.ModeLive,
			Collections: []dalgo2http.Collection{{Name: "daily", URLTemplate: manifest.ECBDailyURL, Decoder: dalgo2http.DecoderECBEuroFXRef, KeyField: "currency", Timeout: 10 * time.Second, ClientSideFilter: true}},
			Client: &http.Client{Transport: serveHTTPTransport(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.String() != manifest.ECBDailyURL || r.Header.Get("Cache-Control") != "no-store, no-cache" || r.Header.Get(server.ProviderExecutionIDHeader) != "" {
					t.Fatal("provider evidence changed the fixed no-store request")
				}
				if len(response) > 0 {
					return response[0](r)
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
	body, err := json.Marshal(map[string]server.ProviderReadProfile{"ecb": profile})
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
			// Database discovery includes its inherited terms entry as well as
			// the profile's recordset notice inventory, both frozen before serving.
			db, err := mount.File(path)
			if err != nil {
				t.Fatal(err)
			}
			databaseRight, err := db.SourceRight("synthetic-cli", nil, "")
			_ = db.Close()
			if err != nil || databaseRight == nil {
				t.Fatal("missing database terms", err)
			}
			expectedDiscovery := []license.SourceRight{*databaseRight, right}
			args := []string{"--manifest", path, "--read-only", "--server-id", "synthetic-cli"}
			if optIn {
				args = append(args, "--provider-read-profiles", writeProviderProfiles(t, profile))
			}
			output, err := serveRun(t, deps, args, func(h http.Handler) {
				meta := serveHTTPRequest(h, "GET", "/v1/databases/ecb", "", nil)
				if meta.Code != 200 || strings.Contains(meta.Body.String(), providerreads.Format) != optIn || calls.Load() != 0 {
					t.Fatalf("discovery format/admission triggered a read: %d %s", meta.Code, meta.Body)
				}
				if optIn {
					var discovery struct {
						Rights    []license.SourceRight `json:"sourceRights"`
						Retention string                `json:"retention"`
					}
					if meta.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(meta.Body.Bytes(), &discovery) != nil || discovery.Retention != "none" || !equalSyntheticJSON(discovery.Rights, expectedDiscovery) {
						t.Fatal("discovery changed full notices or retention")
					}
				}
				for _, humanPath := range []string{"/ovdb/dbs/ecb", "/ovdb/dbs/ecb/collections/daily"} {
					page := serveHTTPRequest(h, "GET", humanPath, "", nil)
					body := page.Body.String()
					if page.Code != 200 || calls.Load() != 0 || !strings.Contains(body, "Retention: none") || !strings.Contains(body, "do not grant an output license") || strings.Contains(body, "001.23000") {
						t.Fatalf("human page read upstream or lost retention: %s: %d", humanPath, page.Code)
					}
					for _, notice := range []string{"Attribution:", "Synthetic provider", "Original free source:", "Synthetic original is free", "Transformations", "Synthetic XML restructured into rows"} {
						if strings.Contains(body, notice) != optIn {
							t.Fatalf("human page admission mismatch: %s: %q", humanPath, notice)
						}
					}
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
					// Freeze every expected admission field before sending the request;
					// response metadata must never supply the expected nonce or rights.
					plan := frozenSyntheticProviderPlan(t, right, profile)
					headers := http.Header{server.ProviderExecutionIDHeader: {plan.Execution.ID}}
					before := calls.Load()
					w := serveHTTPRequest(h, "POST", route.path, route.body, headers)
					var result struct {
						Rights   []license.SourceRight `json:"sourceRights"`
						Used     []string              `json:"usedSourceIds"`
						Evidence json.RawMessage       `json:"providerReads"`
						Records  json.RawMessage       `json:"records"`
					}
					if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &result) != nil || calls.Load() != before+1 {
						t.Fatalf("query/evidence made extra reads: %d %s", w.Code, w.Body)
					}

					if !optIn {
						if len(result.Evidence) != 0 {
							t.Fatal("legacy CLI emitted unconfigured provider binding")
						}
						assertSyntheticProviderRows(t, result.Records, route.empty)
						continue
					}
					observed, err := providerreads.Decode(result.Evidence)
					if err != nil {
						t.Fatal(err)
					}
					metadata := providerreads.Metadata{SourceRights: result.Rights, UsedSourceIDs: result.Used, ProviderReads: &observed}
					if err := providerreads.ValidateMetadata(metadata, plan, []string{right.SourceID}); err != nil {
						t.Fatal(err)
					}
					if !equalSyntheticJSON(result.Rights, plan.SourceRights) {
						t.Fatal("full detached source notice inventory changed")
					}
					assertSyntheticProviderRows(t, result.Records, route.empty)
					assertSyntheticProviderTamperingRefused(t, metadata, plan, right.SourceID)
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
				before := calls.Load()
				w := serveHTTPRequest(h, "POST", "/v1/databases/ecb/query", `{"collection":"daily"}`, http.Header{"OVDB-Page-Size": {"1"}})
				if w.Code != 422 || calls.Load() != before || len(snapshotFiles(t, temp)) != 0 {
					t.Fatal("opt-in bypassed no-retention refusal")
				}
			})
			if err != nil || len(snapshotFiles(t, temp)) != 0 || strings.Contains(output, "001.23000") || strings.Contains(output, "g:Envelope") {
				t.Fatal(err)
			}
		})
	}
}

func TestServeHTTPProviderReadsRefusesInvalidAdmissionBeforeRead(t *testing.T) {
	isolatedServeTemp(t)
	for name, change := range map[string]func(*server.ProviderReadProfile){
		"rights digest":           func(p *server.ProviderReadProfile) { p.Binding.RightsDigest = strings.Repeat("d", 64) },
		"rights source":           func(p *server.ProviderReadProfile) { p.Binding.RightsSourceID = "ovdb:other/ecb/daily" },
		"collection":              func(p *server.ProviderReadProfile) { p.Collection = "undeclared" },
		"definition digest":       func(p *server.ProviderReadProfile) { p.Binding.DefinitionDigest = strings.Repeat("d", 64) },
		"invalid decoder digest":  func(p *server.ProviderReadProfile) { p.Binding.DecoderDigest = "invalid" },
		"missing inventory":       func(p *server.ProviderReadProfile) { p.SourceRight = nil },
		"missing attribution":     func(p *server.ProviderReadProfile) { p.SourceRight.Attribution = nil },
		"missing free source":     func(p *server.ProviderReadProfile) { p.SourceRight.FreeSource = nil },
		"wrong free source":       func(p *server.ProviderReadProfile) { p.SourceRight.FreeSource.URL = "https://example.org/other.xml" },
		"missing transformations": func(p *server.ProviderReadProfile) { p.SourceRight.Transformations = nil },
		"contradictory terms":     func(p *server.ProviderReadProfile) { p.SourceRight.Declaration.Text = "Replacement terms" },
		"wrong executor":          func(p *server.ProviderReadProfile) { p.SourceRight.Source.ServerID = "other" },
		"wrong database":          func(p *server.ProviderReadProfile) { p.SourceRight.Source.DatabaseID = "other" },
		"missing pin":             func(p *server.ProviderReadProfile) { p.SourceRight.Pins = nil },
		"moving revision":         func(p *server.ProviderReadProfile) { p.SourceRight.Pins[0].Revision = "main" },
	} {
		t.Run(name, func(t *testing.T) {
			path, deps, calls, _, profile := syntheticProviderFixture(t)
			change(&profile)
			// A self-consistent digest cannot compensate for missing notices or
			// contradictory mounted facts. Test that shape/identity boundary too.
			if profile.SourceRight != nil && name != "rights digest" {
				digest, err := providerreads.RightsDigest(*profile.SourceRight)
				if err != nil {
					t.Fatal(err)
				}
				profile.Binding.RightsDigest = digest
			}
			_, err := serveRun(t, deps, []string{"--manifest", path, "--server-id", "synthetic-cli", "--provider-read-profiles", writeProviderProfiles(t, profile)}, func(http.Handler) { t.Fatal("served invalid admission") })
			if err == nil || calls.Load() != 0 {
				t.Fatal("invalid admission started or read")
			}
		})
	}
	for _, retention := range []string{"permitted", "unknown"} {
		path, deps, calls, _, profile := syntheticProviderFixture(t)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(body), "retention: none", "retention: "+retention, 1)), 0600); err != nil {
			t.Fatal(err)
		}
		_, err = serveRun(t, deps, []string{"--manifest", path, "--server-id", "synthetic-cli", "--provider-read-profiles", writeProviderProfiles(t, profile)}, func(http.Handler) { t.Fatal("served retained HTTP source") })
		if err == nil || calls.Load() != 0 {
			t.Fatal("retention change admitted")
		}
	}
}

func TestServeHTTPProviderProfilesConfigFailsBeforeMount(t *testing.T) {
	deps := defaultServeDeps()
	deps.mountFile = func(string) (*core.Database, error) {
		t.Fatal("mounted before invalid config refusal")
		return nil, nil
	}
	for _, body := range []string{"null", "{}", `{"ecb":{"unknown":true}}`, `{"ecb":{"collection":"daily","binding":{}}}`, `{} {}`, strings.Repeat(" ", providerreads.MaxMetadataBytes+1)} {
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

func cloneSyntheticAdmission[T any](t *testing.T, value T) T {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var detached T
	if err := json.Unmarshal(body, &detached); err != nil {
		t.Fatal(err)
	}
	return detached
}

func equalSyntheticJSON(left, right any) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && string(a) == string(b)
}

func frozenSyntheticProviderPlan(t *testing.T, right license.SourceRight, profile server.ProviderReadProfile) providerreads.Plan {
	t.Helper()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	plan := cloneSyntheticAdmission(t, providerreads.Plan{
		Execution:    providerreads.Execution{ID: hex.EncodeToString(nonce[:]), Mode: "proxy", ExecutorID: "synthetic-cli"},
		Bindings:     []providerreads.Binding{profile.Binding},
		Requests:     []providerreads.Request{{ResourceID: profile.Binding.ResourceID, Method: "GET", UpstreamURL: manifest.ECBDailyURL, Params: map[string]any{}}},
		SourceRights: []license.SourceRight{right}, MaxReads: new(1), MaxMetadataBytes: providerreads.MaxMetadataBytes,
	})
	if _, err := providerreads.NewCollector(plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func assertSyntheticProviderRows(t *testing.T, raw json.RawMessage, empty bool) {
	t.Helper()
	var records []struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatal(err)
	}
	if empty && len(records) != 0 || !empty && (len(records) != 1 || records[0].Data["rate"] != "001.23000" || records[0].Data["time"] != "2037-02-03") {
		t.Fatal("native result changed")
	}
}

// Every mutation is gated before records are decoded or released. This is a
// consumer check against pre-request admission, not authenticity verification.
func assertSyntheticProviderTamperingRefused(t *testing.T, metadata providerreads.Metadata, plan providerreads.Plan, sourceID string) {
	t.Helper()
	for name, change := range map[string]func(*providerreads.Metadata){
		"nonce":    func(m *providerreads.Metadata) { m.ProviderReads.Execution.ID = strings.Repeat("f", 32) },
		"executor": func(m *providerreads.Metadata) { m.ProviderReads.Execution.ExecutorID = "other" },
		"definition": func(m *providerreads.Metadata) {
			m.ProviderReads.Bindings[0].DefinitionDigest = strings.Repeat("d", 64)
		},
		"decoder":         func(m *providerreads.Metadata) { m.ProviderReads.Bindings[0].DecoderDigest = strings.Repeat("d", 64) },
		"rights digest":   func(m *providerreads.Metadata) { m.ProviderReads.Bindings[0].RightsDigest = strings.Repeat("d", 64) },
		"request digest":  func(m *providerreads.Metadata) { m.ProviderReads.Reads[0].RequestDigest = strings.Repeat("d", 64) },
		"missing rights":  func(m *providerreads.Metadata) { m.SourceRights = nil },
		"attribution":     func(m *providerreads.Metadata) { m.SourceRights[0].Attribution.Text = "replacement" },
		"free source":     func(m *providerreads.Metadata) { m.SourceRights[0].FreeSource.URL = "https://example.org/other.xml" },
		"transformations": func(m *providerreads.Metadata) { m.SourceRights[0].Transformations = nil },
		"terms":           func(m *providerreads.Metadata) { m.SourceRights[0].Declaration.URL = "https://example.org/other-terms" },
		"pin":             func(m *providerreads.Metadata) { m.SourceRights[0].Pins[0].Revision = strings.Repeat("d", 40) },
		"legacy usage":    func(m *providerreads.Metadata) { m.UsedSourceIDs = nil },
		"usage":           func(m *providerreads.Metadata) { m.ProviderReads.Usage = nil },
	} {
		changed := cloneSyntheticAdmission(t, metadata)
		change(&changed)
		if providerreads.ValidateMetadata(changed, plan, []string{sourceID}) == nil {
			t.Fatalf("admitted altered %s", name)
		}
	}
}

func TestServeHTTPProviderReadsNonceAndOperationsRefusedBeforeRead(t *testing.T) {
	temp := isolatedServeTemp(t)
	path, deps, calls, right, profile := syntheticProviderFixture(t)
	plan := frozenSyntheticProviderPlan(t, right, profile)
	_, err := serveRun(t, deps, []string{"--manifest", path, "--read-only", "--server-id", "synthetic-cli", "--provider-read-profiles", writeProviderProfiles(t, profile)}, func(h http.Handler) {
		for name, values := range map[string][]string{
			"empty": {""}, "short": {strings.Repeat("a", 31)}, "long": {strings.Repeat("a", 33)}, "nonhex": {strings.Repeat("g", 32)},
			"uppercase": {strings.Repeat("A", 32)}, "space": {" " + plan.Execution.ID}, "duplicate": {plan.Execution.ID, plan.Execution.ID},
			"coalesced": {plan.Execution.ID + "," + plan.Execution.ID},
		} {
			for _, route := range []struct{ path, body string }{{"/v1/databases/ecb/query", `{"collection":"daily"}`}, {"/v1/databases/ecb/dtql", "from: {name: daily}\n"}} {
				w := serveHTTPRequest(h, "POST", route.path, route.body, http.Header{server.ProviderExecutionIDHeader: values})
				if w.Code != 400 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"invalid_execution_id"`) || strings.Contains(w.Body.String(), plan.Execution.ID) {
					t.Fatalf("invalid %s accepted/reflected", name)
				}
			}
		}
		for _, request := range []struct {
			method, path, body string
			extra              http.Header
			status             int
		}{
			{"POST", "/v1/databases/ecb/query", `{"collection":"daily"}`, http.Header{"OVDB-Page-Size": {"1"}}, 422},
			{"POST", "/v1/databases/ecb/query", `{"collection":"daily"}`, http.Header{"OVDB-Page-Token": {""}}, 422},
			{"POST", "/v1/databases/ecb/query", `{"collection":"daily"}`, http.Header{"OVDB-Page-Close": {"true"}}, 422},
			{"POST", "/v1/databases/ecb/query", `{"collection":"daily","snapshotToken":""}`, nil, 422},
			{"GET", "/v1/databases/ecb/records/daily/AAA", "", nil, 501},
			{"PUT", "/v1/databases/ecb/records/daily/AAA", `{"data":{"rate":"secret-synthetic"}}`, nil, 403},
			{"PATCH", "/v1/databases/ecb/records/daily/AAA", `{"data":{"rate":"secret-synthetic"}}`, nil, 403},
			{"DELETE", "/v1/databases/ecb/records/daily/AAA", "", nil, 403},
			{"POST", "/v1/databases/ecb/query", `{"collection":"undeclared"}`, nil, 422},
			{"POST", "/v1/databases/other/query", `{"collection":"daily"}`, nil, 404},
		} {
			headers := request.extra.Clone()
			if headers == nil {
				headers = http.Header{}
			}
			headers.Set(server.ProviderExecutionIDHeader, plan.Execution.ID)
			w := serveHTTPRequest(h, request.method, request.path, request.body, headers)
			if w.Code != request.status || request.path != "/v1/databases/other/query" && w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "secret-synthetic") {
				t.Fatalf("%s %s: status %d, expected %d", request.method, request.path, w.Code, request.status)
			}
		}
		if calls.Load() != 0 || len(snapshotFiles(t, temp)) != 0 {
			t.Fatal("refusal fetched or retained data")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestServeHTTPProviderReadsConcurrentIndependentPlans(t *testing.T) {
	isolatedServeTemp(t)
	path, deps, calls, right, profile := syntheticProviderFixture(t)
	// Freeze both plans before either request; one returns no rows.
	plans := []providerreads.Plan{frozenSyntheticProviderPlan(t, right, profile), frozenSyntheticProviderPlan(t, right, profile)}
	if plans[0].Execution.ID == plans[1].Execution.ID {
		t.Fatal("nonce collision")
	}
	_, err := serveRun(t, deps, []string{"--manifest", path, "--server-id", "synthetic-cli", "--provider-read-profiles", writeProviderProfiles(t, profile)}, func(h http.Handler) {
		var wg sync.WaitGroup
		responses := make([]*httptest.ResponseRecorder, 2)
		for i := range plans {
			wg.Go(func() {
				body := `{"collection":"daily"}`
				if i == 1 {
					body = `{"collection":"daily","where":[{"field":"currency","op":"==","value":"ZZZ"}]}`
				}
				responses[i] = serveHTTPRequest(h, "POST", "/v1/databases/ecb/query", body, http.Header{server.ProviderExecutionIDHeader: {plans[i].Execution.ID}})
			})
		}
		wg.Wait()
		observations := make([]string, 2)
		for i, w := range responses {
			var metadata providerreads.Metadata
			if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &metadata) != nil {
				t.Fatal("concurrent query failed")
			}
			if err := providerreads.ValidateMetadata(metadata, plans[i], []string{right.SourceID}); err != nil {
				t.Fatal(err)
			}
			if providerreads.ValidateMetadata(metadata, plans[1-i], []string{right.SourceID}) == nil {
				t.Fatal("cross-execution evidence accepted")
			}
			if len(metadata.ProviderReads.Reads) != 1 || len(metadata.ProviderReads.Usage) != 1 {
				t.Fatal("missing read/usage")
			}
			observations[i] = metadata.ProviderReads.Reads[0].ObservationID
			var response struct {
				Records json.RawMessage `json:"records"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			assertSyntheticProviderRows(t, response.Records, i == 1)
		}
		if calls.Load() != 2 || observations[0] == "" || observations[0] == observations[1] {
			t.Fatal("concurrent reads were reused or mixed")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestServeHTTPProviderReadsErrorDoesNotExposePayload(t *testing.T) {
	temp := isolatedServeTemp(t)
	var logs strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	path, deps, calls, right, profile := syntheticProviderFixture(t, serveHTTPTransport(func(*http.Request) (*http.Response, error) {
		// Malformed synthetic source carrying sentinel values must be sanitized
		// before reaching either the HTTP response or server/command logs.
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/xml"}}, Body: io.NopCloser(strings.NewReader(`<invalid secret="secret-synthetic-rate-001.23000">`))}, nil
	}))
	plan := frozenSyntheticProviderPlan(t, right, profile)
	output, err := serveRun(t, deps, []string{"--manifest", path, "--server-id", "synthetic-cli", "--provider-read-profiles", writeProviderProfiles(t, profile)}, func(h http.Handler) {
		w := serveHTTPRequest(h, "POST", "/v1/databases/ecb/query", `{"collection":"daily"}`, http.Header{server.ProviderExecutionIDHeader: {plan.Execution.ID}})
		if w.Code == 200 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), `"records"`) || strings.Contains(w.Body.String(), `"providerReads"`) || strings.Contains(w.Body.String(), plan.Execution.ID) {
			t.Fatal("failed source released rows or evidence")
		}
		for _, value := range []string{"secret-synthetic", "001.23000", "<invalid"} {
			if strings.Contains(w.Body.String(), value) || strings.Contains(logs.String(), value) {
				t.Fatal("payload value escaped into error/log")
			}
		}
	})
	if err != nil || calls.Load() != 1 || len(snapshotFiles(t, temp)) != 0 || strings.Contains(output, "001.23000") {
		t.Fatal("failed source retained data or leaked command output", err)
	}
}
