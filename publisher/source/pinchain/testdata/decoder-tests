package dalgo2http

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dal-go/dalgo/dal"
	"github.com/dal-go/record"
)

// Invented dates, currency codes and rates; no copied source-data fixtures.
func syntheticECB(quotes string) string {
	return `<?xml version="1.0"?><g:Envelope xmlns:g="` + gesmesNamespace + `" xmlns="` + ecbNamespace + `"><g:subject>Invented example</g:subject><g:Sender><g:name>Synthetic sender</g:name></g:Sender><Cube><Cube time="2037-02-03">` + quotes + `</Cube></Cube></g:Envelope>`
}

func TestECBDailyDecoder(t *testing.T) {
	valid := syntheticECB(`<Cube currency="AAA" rate="001.23000"/><Cube currency="ZZZ" rate="0.00001"/>`)
	rows, err := decodeECBDaily([]byte(valid))
	if err != nil || len(rows) != 2 {
		t.Fatalf("decode: rows=%v err=%v", rows, err)
	}
	if rows[0]["time"] != "2037-02-03" || rows[0]["currency"] != "AAA" || rows[0]["rate"] != "001.23000" {
		t.Fatalf("native lexical fields: %#v", rows[0])
	}
	if len(rows[0]) != 3 {
		t.Fatalf("unexpected synthesized fields: %#v", rows[0])
	}
	if _, err = decodeECBDaily([]byte(strings.Repeat(" ", maxBodyBytes+1))); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("size: %v", err)
	}

	cases := map[string]string{
		"empty": "", "malformed": "<", "wrong root": "<Envelope/>", "two roots": valid + valid,
		"DTD": `<!DOCTYPE x>` + valid, "entity": strings.Replace(valid, "001.23000", "1&#46;23", 1),
		"foreign PI": `<?script run?>` + valid, "late PI": valid + `<?xml version="1.0"?>`,
		"wrong namespace":     strings.ReplaceAll(valid, ecbNamespace, "urn:foreign"),
		"root attr":           strings.Replace(valid, `xmlns:g=`, `bad="x" xmlns:g=`, 1),
		"namespaced attr":     strings.Replace(valid, `currency="AAA"`, `g:currency="AAA"`, 1),
		"duplicate attr":      strings.Replace(valid, `currency="AAA"`, `currency="AAA" currency="AAA"`, 1),
		"duplicate namespace": strings.Replace(valid, `xmlns:g=`, `xmlns:g="urn:x" xmlns:g=`, 1),
		"duplicate subject":   strings.Replace(valid, `<g:Sender>`, `<g:subject>Another</g:subject><g:Sender>`, 1),
		"subject attr":        strings.Replace(valid, `<g:subject>`, `<g:subject extra="x">`, 1),
		"duplicate sender":    strings.Replace(valid, `<Cube>`, `<g:Sender/><Cube>`, 1),
		"sender attr":         strings.Replace(valid, `<g:Sender>`, `<g:Sender extra="x">`, 1),
		"duplicate name":      strings.Replace(valid, `</g:Sender>`, `<g:name>Another</g:name></g:Sender>`, 1),
		"name attr":           strings.Replace(valid, `<g:name>`, `<g:name extra="x">`, 1),
		"duplicate cube":      strings.Replace(valid, `</g:Envelope>`, `<Cube/></g:Envelope>`, 1),
		"outer attr":          strings.Replace(valid, `<Cube>`, `<Cube time="2037-02-03">`, 1),
		"two dates":           strings.Replace(valid, `</Cube></Cube>`, `</Cube><Cube time="2037-02-04"/></Cube>`, 1),
		"date extra attr":     strings.Replace(valid, `time="2037-02-03"`, `time="2037-02-03" extra="x"`, 1),
		"missing date":        strings.Replace(valid, `time="2037-02-03"`, ``, 1),
		"short date":          strings.Replace(valid, `2037-02-03`, `2037-2-3`, 1),
		"invalid date":        strings.Replace(valid, `2037-02-03`, `2037-02-30`, 1),
		"duplicate quote":     syntheticECB(`<Cube currency="AAA" rate="1"/><Cube currency="AAA" rate="2"/>`),
		"bad currency":        syntheticECB(`<Cube currency="aaA" rate="1"/>`),
		"EUR quote":           syntheticECB(`<Cube currency="EUR" rate="1"/>`),
		"zero":                syntheticECB(`<Cube currency="AAA" rate="0.0000"/>`),
		"negative":            syntheticECB(`<Cube currency="AAA" rate="-1"/>`),
		"exponent":            syntheticECB(`<Cube currency="AAA" rate="1e2"/>`),
		"decimal whitespace":  syntheticECB(`<Cube currency="AAA" rate=" 1"/>`),
		"missing rate":        syntheticECB(`<Cube currency="AAA"/>`),
		"extra quote attr":    syntheticECB(`<Cube currency="AAA" rate="1" extra="x"/>`),
		"unknown child":       syntheticECB(`<Other/>`), "no quotes": syntheticECB(``),
		"too deep":        syntheticECB(`<Cube currency="AAA" rate="1"><Cube/></Cube>`),
		"unexpected text": syntheticECB(`text`), "outside text": valid + `text`,
		"empty envelope": `<g:Envelope xmlns:g="` + gesmesNamespace + `"/>`,
	}
	var many strings.Builder
	for i := 0; i <= maxECBRows; i++ {
		fmt.Fprintf(&many, `<Cube currency="%c%c%c" rate="1"/>`, 'A'+rune(i/676), 'A'+rune((i/26)%26), 'A'+rune(i%26))
	}
	cases["row limit"] = syntheticECB(many.String())
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeECBDaily([]byte(body)); !errors.Is(err, ErrInvalidXML) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func ecbCollection(url string) Collection {
	return Collection{Name: "daily", URLTemplate: url, Decoder: DecoderECBEuroFXRef, KeyField: "currency", ClientSideFilter: true, Timeout: time.Second, InsecureAllowLoopback: strings.HasPrefix(url, "http://127.0.0.1")}
}

func TestECBQueryLiveOnly(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Cache-Control") != "no-store, no-cache" || r.Header.Get("Pragma") != "no-cache" {
			t.Errorf("missing upstream no-store request")
		}
		w.Header().Set("Content-Type", "text/xml")
		w.Header().Set("Last-Modified", "Tue, 03 Feb 2037 16:00:00 GMT")
		w.Header().Set("ETag", "synthetic")
		if r.URL.RawQuery != "" {
			t.Errorf("fabricated upstream filter: %s", r.URL.RawQuery)
		}
		_, _ = fmt.Fprint(w, syntheticECB(`<Cube currency="AAA" rate="001.23000"/><Cube currency="ZZZ" rate="0.00001"/>`))
	}))
	defer srv.Close()
	coll := ecbCollection(srv.URL)
	db, err := NewDB(Config{Mode: ModeLive, Collections: []Collection{coll}})
	if err != nil {
		t.Fatal(err)
	}
	recorder := NewRecorder()
	reader, err := db.ExecuteQueryToRecordsReader(recorder.WithContext(context.Background()), newQuery("daily", dal.WhereField("currency", dal.Equal, "AAA"), 0))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if rec.Key().ID != "AAA" || rec.Data().(map[string]any)["rate"] != "001.23000" {
		t.Fatalf("record=%#v", rec.Data())
	}
	if _, err = reader.Next(); err != dal.ErrNoMoreRecords {
		t.Fatalf("end=%v", err)
	}
	prov, ok := recorder.Last()
	expectedBody := syntheticECB(`<Cube currency="AAA" rate="001.23000"/><Cube currency="ZZZ" rate="0.00001"/>`)
	if prov.UpstreamURL != srv.URL || prov.ContentType != "text/xml" || prov.LastModified != "Tue, 03 Feb 2037 16:00:00 GMT" || prov.ETag != "synthetic" || prov.Bytes != len(expectedBody) || prov.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(expectedBody))) {
		t.Fatalf("observation=%#v", prov)
	}
	if !ok || prov.Decoder != DecoderECBEuroFXRef || prov.BaseCurrency != "EUR" || prov.ReferenceDate != "2037-02-03" || prov.Source != SourceLive {
		t.Fatalf("provenance=%#v", prov)
	}
	cp, _ := dal.As[CapabilitiesProvider](db)
	cap, err := cp.Capabilities("daily")
	if err != nil || cap.SupportsGet || len(cap.Fields) != 0 || !cap.ClientSideFilter {
		t.Fatalf("capability=%#v err=%v", cap, err)
	}
	key := record.NewKeyWithID("daily", "AAA")
	if err = db.Get(context.Background(), record.NewRecordWithData(key, &map[string]any{})); !errors.Is(err, dal.ErrNotSupported) {
		t.Fatalf("Get=%v", err)
	}
	if _, err = db.Exists(context.Background(), key); !errors.Is(err, dal.ErrNotSupported) {
		t.Fatalf("Exists=%v", err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
	if path, err := Record(context.Background(), noCallClient(t), coll, nil, "unused-snapshot-dir"); path != "" || !errors.Is(err, dal.ErrNotSupported) {
		t.Fatalf("unauthorized Record: path=%q error=%v", path, err)
	}
	// A decoding failure returns no successful ECB response metadata.
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, `<invalid/>`) })
	if _, err = db.ExecuteQueryToRecordsReader(context.Background(), newQuery("daily", nil, 0)); !errors.Is(err, ErrInvalidXML) {
		t.Fatalf("malformed upstream=%v", err)
	}
}

func TestDecoderConfiguration(t *testing.T) {
	coll := ecbCollection("https://example.invalid/daily.xml")
	configs := []Config{
		{Collections: []Collection{coll}}, {Mode: ModeLive, Snapshots: fstest.MapFS{}, Collections: []Collection{coll}},
		{Mode: ModeSnapshot, Collections: []Collection{coll}}, {Mode: ModeLiveThenSnapshot, Collections: []Collection{coll}},
	}
	for _, cfg := range configs {
		if _, err := NewDB(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("mode %q=%v", cfg.Mode, err)
		}
	}
	invalid := []Collection{coll, coll, coll, coll, coll, coll, coll, coll, coll, coll, coll}
	invalid[0].Decoder = "unknown"
	invalid[1].RowsPath = "rows"
	invalid[2].KeyField = "rate"
	invalid[3].Params = map[string]Param{"currency": {Location: ParamQuery}}
	invalid[4].Timeout = 0
	invalid[5].URLTemplate = ":invalid"
	invalid[6].URLTemplate = "https:///missing-host"
	invalid[7].URLTemplate = "https://user@example.invalid/data"
	invalid[8].URLTemplate += "?unexpected=1"
	invalid[9].URLTemplate += "#fragment"
	invalid[10].Headers = map[string]string{"Authorization": "ECB_TEST_SECRET"}
	for _, c := range invalid {
		if _, err := NewDB(Config{Mode: ModeLive, Collections: []Collection{c}}); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("invalid decoder config=%v", err)
		}
	}
	for _, decode := range []Decoder{"", DecoderJSON} {
		if rows, err := decodeRows([]byte(`[{"id":"synthetic"}]`), Collection{Decoder: decode}); err != nil || len(rows) != 1 {
			t.Fatalf("json default: %v", err)
		}
	}
	text := `{"mode":"live","collections":[{"name":"daily","urlTemplate":"https://example.invalid/daily.xml","keyField":"currency","decoder":"ecb-eurofxref/1","timeout":"1s"}]}`
	cfg, err := LoadConfigJSON([]byte(text))
	if err != nil || cfg.Collections[0].Decoder != DecoderECBEuroFXRef {
		t.Fatalf("JSON decoder=%v err=%v", cfg, err)
	}
}

func TestObservationHeaderBound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", strings.Repeat("x", 1025))
		_, _ = fmt.Fprint(w, syntheticECB(`<Cube currency="AAA" rate="1"/>`))
	}))
	defer srv.Close()
	db, err := NewDB(Config{Mode: ModeLive, Collections: []Collection{ecbCollection(srv.URL)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecuteQueryToRecordsReader(context.Background(), newQuery("daily", nil, 0)); !errors.Is(err, ErrUpstreamClient) {
		t.Fatalf("overbound observation=%v", err)
	}
}
