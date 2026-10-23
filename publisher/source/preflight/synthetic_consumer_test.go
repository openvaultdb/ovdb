package preflight

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"
	"github.com/openvaultdb/openvaultdb-go/pkg/providerreads"
)

type syntheticRow struct {
	Key  string            `json:"key"`
	Data map[string]string `json:"data"`
}
type syntheticConsumer struct {
	mu           sync.Mutex
	plan         providerreads.Plan
	used         []string
	accesses     int
	publications int
	rows         []syntheticRow
	disposed     bool
}

// Freeze independent inputs before issuing the request. None comes from its
// response, its used-source list, or the producer's generated nonce.
func syntheticNewConsumer(t *testing.T, rt *syntheticRuntime) *syntheticConsumer {
	t.Helper()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("nonce generation failed")
	}
	e := syntheticClone(t, rt.expected)
	return &syntheticConsumer{plan: providerreads.Plan{Execution: providerreads.Execution{ID: hex.EncodeToString(nonce[:]), Mode: "proxy", ExecutorID: rt.executor}, Bindings: []providerreads.Binding{e.Binding}, Requests: []providerreads.Request{{ResourceID: e.Binding.ResourceID, Method: "GET", UpstreamURL: manifest.ECBDailyURL, Params: map[string]any{}}}, SourceRights: []license.SourceRight{e.Right}, MaxReads: new(1), MaxMetadataBytes: providerreads.MaxMetadataBytes}, used: []string{e.Right.Source.SourceID()}}
}

func syntheticOuter(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) > 256<<10 || !utf8.Valid(raw) {
		return nil, errors.New("outer response bound")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("outer object required")
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return nil, errors.New("outer key malformed")
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("outer key required")
		}
		if _, ok = out[key]; ok {
			return nil, errors.New("duplicate outer key")
		}
		if key != "records" && key != "sourceRights" && key != "usedSourceIds" && key != "providerReads" {
			return nil, errors.New("unknown outer key")
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("null or invalid outer field")
		}
		// records remain opaque bytes until the independent metadata gate passes.
		out[key] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, errors.New("outer object incomplete")
	}
	if _, err := d.Token(); err != io.EOF || len(out) != 4 {
		return nil, errors.New("outer fields missing or trailing data")
	}
	return out, nil
}

// This private syntax check is consumer-harness behavior, not a library claim.
func syntheticMetadataSyntax(d *json.Decoder) error {
	tok, err := d.Token()
	if err != nil || tok == nil {
		return errors.New("null or invalid metadata")
	}
	switch tok {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return errors.New("duplicate metadata key")
			}
			seen[s] = true
			if err := syntheticMetadataSyntax(d); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	case json.Delim('['):
		for d.More() {
			if err := syntheticMetadataSyntax(d); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	}
	return nil
}

func syntheticStrict(raw []byte, out any) error {
	if err := syntheticMetadataSyntax(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.New("invalid closed metadata")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing metadata")
	}
	return nil
}

func (c *syntheticConsumer) accept(ctx context.Context, raw []byte) ([]syntheticRow, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disposed || ctx.Err() != nil {
		return nil, errors.New("consumer terminal")
	}
	outer, err := syntheticOuter(raw)
	if err != nil {
		return nil, err
	}
	if len(outer["sourceRights"])+len(outer["usedSourceIds"])+len(outer["providerReads"]) > c.plan.MaxMetadataBytes {
		return nil, errors.New("evidence bound")
	}
	var metadata providerreads.Metadata
	if err := syntheticStrict(outer["sourceRights"], &metadata.SourceRights); err != nil {
		return nil, err
	}
	if err := syntheticStrict(outer["usedSourceIds"], &metadata.UsedSourceIDs); err != nil {
		return nil, err
	}
	e, err := providerreads.Decode(outer["providerReads"])
	if err != nil {
		return nil, errors.New("closed provider evidence rejected")
	}
	metadata.ProviderReads = &e
	if err := providerreads.ValidateMetadata(metadata, c.plan, c.used); err != nil {
		return nil, errors.New("independent evidence rejected")
	}
	if len(e.Reads) != 1 || e.Reads[0].ReferenceDate != "2039-04-05" {
		return nil, errors.New("invented reference date mismatch")
	}
	fetched, err := time.Parse(time.RFC3339Nano, e.Reads[0].FetchedAt)
	if err != nil || fetched.Format("2006-01-02") == e.Reads[0].ReferenceDate {
		return nil, errors.New("fetch and reference date conflated")
	}
	if c.disposed || ctx.Err() != nil {
		return nil, errors.New("consumer terminal")
	}
	c.accesses++ // First permitted inspection of row content.
	var rows []syntheticRow
	if err := syntheticStrict(outer["records"], &rows); err != nil || len(rows) > 50 {
		return nil, errors.New("bounded native rows rejected")
	}
	c.rows = rows
	c.publications++
	return rows, nil
}
func (c *syntheticConsumer) dispose() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disposed = true
	c.rows = nil
}

func TestSyntheticConsumerTamperingBeforeRowAccess(t *testing.T) {
	f := syntheticPublisher(t)
	tr := &syntheticTransport{counts: &syntheticCounters{}, body: syntheticXML()}
	rt, err := syntheticAssemble(t, f, tr, nil)
	if err != nil {
		t.Fatal("assembly failed")
	}
	c := syntheticNewConsumer(t, rt)
	w := httptest.NewRecorder()
	rt.handler.ServeHTTP(w, syntheticRequest(context.Background(), c.plan.Execution.ID, "from: {name: daily}\n"))
	if w.Code != 200 {
		t.Fatal("positive query failed")
	}
	raw := bytes.Clone(w.Body.Bytes())
	var original map[string]json.RawMessage
	if json.Unmarshal(raw, &original) != nil {
		t.Fatal("response object malformed")
	}
	for _, fault := range []string{"nonce", "resource", "executor", "definition", "decoder", "rights digest", "rights", "used", "extra outer", "missing outer", "null outer", "duplicate outer", "extra evidence", "null evidence", "missing evidence", "duplicate evidence", "extra rights", "duplicate rights", "null rights", "oversized"} {
		t.Run(fault, func(t *testing.T) {
			object := syntheticClone(t, original)
			var e providerreads.Envelope
			if json.Unmarshal(object["providerReads"], &e) != nil {
				t.Fatal("evidence malformed")
			}
			switch fault {
			case "nonce":
				e.Execution.ID = strings.Repeat("f", 32)
			case "resource":
				e.Bindings[0].ResourceID = "other"
			case "executor":
				e.Execution.ExecutorID = "other"
			case "definition":
				e.Bindings[0].DefinitionDigest = strings.Repeat("c", 64)
			case "decoder":
				e.Bindings[0].DecoderDigest = strings.Repeat("c", 64)
			case "rights digest":
				e.Bindings[0].RightsDigest = strings.Repeat("c", 64)
			case "rights":
				object["sourceRights"] = json.RawMessage(`[]`)
			case "used":
				object["usedSourceIds"] = json.RawMessage(`[]`)
			case "extra outer":
				object["extra"] = json.RawMessage(`true`)
			case "missing outer":
				delete(object, "sourceRights")
			case "null outer":
				object["records"] = json.RawMessage(`null`)
			}
			if fault == "nonce" || fault == "resource" || fault == "executor" || fault == "definition" || fault == "decoder" || fault == "rights digest" {
				object["providerReads"] = encoded(t, e)
			}
			body := encoded(t, object)
			switch fault {
			case "duplicate outer":
				body = append([]byte(`{"records":[],`), body[1:]...)
			case "extra evidence":
				object["providerReads"] = append([]byte(`{"extra":true,`), object["providerReads"][1:]...)
				body = encoded(t, object)
			case "null evidence":
				object["providerReads"] = json.RawMessage(`null`)
				body = encoded(t, object)
			case "missing evidence":
				delete(object, "providerReads")
				body = encoded(t, object)
			case "duplicate evidence":
				object["providerReads"] = append([]byte(`{"format":"ovdb-provider-read/1",`), object["providerReads"][1:]...)
				body = encoded(t, object)
			case "oversized":
				body = bytes.Repeat([]byte(" "), (256<<10)+1)
			case "extra rights":
				object["sourceRights"] = append([]byte(`[{"extra":true,`), object["sourceRights"][2:]...)
				body = encoded(t, object)
			case "duplicate rights":
				object["sourceRights"] = append([]byte(`[{"sourceId":"other",`), object["sourceRights"][2:]...)
				body = encoded(t, object)
			case "null rights":
				object["sourceRights"] = json.RawMessage(`[null]`)
				body = encoded(t, object)
			}
			gate := &syntheticConsumer{plan: syntheticClone(t, c.plan), used: append([]string(nil), c.used...)}
			if _, err := gate.accept(context.Background(), body); err == nil || gate.accesses != 0 || gate.publications != 0 || gate.rows != nil {
				t.Fatal("tampering reached rows")
			}
		})
	}
	if _, err := c.accept(context.Background(), raw); err != nil || c.accesses != 1 {
		t.Fatal("positive consumer control failed")
	}
	c.dispose()
	if _, err := c.accept(context.Background(), raw); err == nil || c.rows != nil {
		t.Fatal("disposed consumer published")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	gate := &syntheticConsumer{plan: c.plan, used: c.used}
	if _, err := gate.accept(ctx, raw); err == nil || gate.accesses != 0 {
		t.Fatal("cancelled consumer inspected rows")
	}
}

func TestSyntheticConsumerOwnedDisposal(t *testing.T) {
	f := syntheticPublisher(t)
	tr := &syntheticTransport{counts: &syntheticCounters{}, body: syntheticXML()}
	rt, err := syntheticAssemble(t, f, tr, nil)
	if err != nil {
		t.Fatal("assembly failed")
	}
	c := syntheticNewConsumer(t, rt)
	w := httptest.NewRecorder()
	rt.handler.ServeHTTP(w, syntheticRequest(context.Background(), c.plan.Execution.ID, "from: {name: daily}\n"))
	if w.Code != 200 {
		t.Fatal("positive query failed")
	}
	disposed, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		c.dispose()
		close(disposed)
		if _, err := c.accept(context.Background(), w.Body.Bytes()); err == nil {
			t.Error("terminal disposal accepted response")
		}
	}()
	syntheticAwait(t, disposed, "consumer disposal")
	syntheticAwait(t, joined, "consumer joined")
	if c.accesses != 0 || c.publications != 0 || c.rows != nil {
		t.Fatal("publication after terminal disposal")
	}
	// An already valid RAM publication is released by disposal too; this does
	// not claim physical memory zeroization or recall a caller-owned copy.
	active := &syntheticConsumer{plan: syntheticClone(t, c.plan), used: append([]string(nil), c.used...)}
	if _, err := active.accept(context.Background(), w.Body.Bytes()); err != nil || active.publications != 1 {
		t.Fatal("publication positive control failed")
	}
	active.dispose()
	if active.rows != nil {
		t.Fatal("consumer retained owned rows after disposal")
	}
}

func TestSyntheticPreparedCopiesDoNotFollowMutation(t *testing.T) {
	f := syntheticPublisher(t)
	tr := &syntheticTransport{counts: &syntheticCounters{}, body: syntheticXML()}
	rt, err := syntheticAssemble(t, f, tr, nil)
	if err != nil {
		t.Fatal("assembly failed")
	}
	want := syntheticClone(t, rt.expected)
	f.proposal.Expected.Right.Pins[0].Path = "changed"
	f.proposal.Expected.Right.Attribution.Text = "changed"
	f.proposal.Expected.Right.Transformations[0] = "changed"
	f.proposal.Expected.Binding.RightsDigest = "changed"
	if !reflect.DeepEqual(want, rt.expected) {
		t.Fatal("prepared expectation aliases input")
	}
	c := syntheticNewConsumer(t, rt)
	w := httptest.NewRecorder()
	rt.handler.ServeHTTP(w, syntheticRequest(context.Background(), c.plan.Execution.ID, "from: {name: daily}\n"))
	if _, err := c.accept(context.Background(), w.Body.Bytes()); err != nil {
		t.Fatal("input mutation changed prepared runtime")
	}
}
