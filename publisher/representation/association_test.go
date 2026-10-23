package representation

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const geoOriginalHash = "d564721b80809537424d17b2bf2276697593a3cb380ea020f33c070e1ff0e7ab"

func associationFixture(t *testing.T) (*Document, Context, map[Reference][]byte) {
	t.Helper()
	data, ctx, assets := realFixture(t, "native-geonames")
	doc, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return doc, ctx, assets
}
func TestActualNativeGeoAssociation(t *testing.T) {
	input, ctx, assets := realFixture(t, "native-geonames")
	d, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	c := d.Contracts[0]
	if c.Execution != NativeIdentifier {
		t.Fatal("fixture must use actual native execution")
	}
	original := Reference{Path: "source/generation-snapshot.json", SHA256: geoOriginalHash}
	bytes := assets[original]
	if Hash(bytes) != geoOriginalHash || len(bytes) != 7941 {
		t.Fatal("original snapshot bytes changed")
	}
	var source map[string]any
	if err := numberJSON(bytes, &source); err != nil {
		t.Fatal(err)
	}
	outputs := source["outputs"].(map[string]any)
	if len(outputs["chunks"].([]any)) != 5 {
		t.Fatal("actual unrelated chunks array lost")
	}
	seen := map[string]int{}
	total := len(input)
	old := ctx.Resolve
	ctx.Resolve = func(r Reference) ([]byte, error) {
		if r.Path == c.Native.Dataset.Path || strings.Contains(r.Path, "keys") || strings.Contains(r.Path, ".gz") {
			t.Fatal("bulk data/keyset/chunk resolve", r.Path)
		}
		b, err := old(r)
		if err == nil {
			seen[r.Path]++
			total += len(b)
		}
		return b, err
	}
	checked, err := Check(input, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if seen[original.Path] != 1 || len(seen) != 8 || total > MaxDocumentBytes {
		t.Fatal("original closure/metadata budget", seen, total)
	}
	value, err := LookupNative(checked.Contracts[0], c.Source, ctx, "US", func(req NativeLookupRequest) ([]string, error) {
		if req.Repository != ctx.Repository || req.Revision != ctx.Revision || req.Snapshot != c.Target.Snapshot || req.Model != c.Target.Model || req.Dataset != c.Native.Dataset || req.Property != "iso" || req.Limit != 2 {
			t.Fatal("wrong native request", req)
		}
		return []string{req.Raw}, nil
	})
	if err != nil || value == nil || *value != "US" {
		t.Fatal(value, err)
	}
	t.Logf("native Geo fixture checked %d metadata bytes; no production user scope admitted", total)
}

// Rebind changed test receipts to the same explicit metadata DAG. Unmodified
// original values retain the actual original bytes/hash. Changes are intentional
// test inputs, never publication claims.
func associationCase(t *testing.T, mutate func(map[string]any, map[string]any, map[string]any)) ([]byte, Context) {
	t.Helper()
	d, ctx, assets := associationFixture(t)
	c := &d.Contracts[0]
	oldProof, oldMetadata := c.Native.Provenance, c.Target.Snapshot
	oldSource := Reference{Path: "source/generation-snapshot.json", SHA256: geoOriginalHash}
	var original, receipt, metadata map[string]any
	if err := numberJSON(assets[oldSource], &original); err != nil {
		t.Fatal(err)
	}
	if err := numberJSON(assets[oldProof], &receipt); err != nil {
		t.Fatal(err)
	}
	if err := numberJSON(assets[oldMetadata], &metadata); err != nil {
		t.Fatal(err)
	}
	canonical, _ := json.Marshal(original)
	mutate(original, receipt, metadata)
	newCanonical, _ := json.Marshal(original)
	sourceBytes := assets[oldSource]
	if string(canonical) != string(newCanonical) {
		sourceBytes = newCanonical
	}
	source := oldSource
	source.SHA256 = Hash(sourceBytes)
	assets[source] = sourceBytes
	if a, ok := receipt["snapshot_association"].(map[string]any); ok {
		if ref, ok := a["source"].(map[string]any); ok && ref["path"] == oldSource.Path && ref["sha256"] == oldSource.SHA256 {
			ref["sha256"] = source.SHA256
		}
	}
	proof, _ := json.Marshal(receipt)
	c.Native.Provenance.SHA256 = Hash(proof)
	assets[c.Native.Provenance] = proof
	for _, raw := range metadata["artifacts"].([]any) {
		ref := raw.(map[string]any)
		if ref["path"] == oldSource.Path && ref["sha256"] == oldSource.SHA256 {
			ref["sha256"] = source.SHA256
		}
		if ref["path"] == oldProof.Path && ref["sha256"] == oldProof.SHA256 {
			ref["sha256"] = c.Native.Provenance.SHA256
		}
	}
	later, _ := json.Marshal(metadata)
	c.Target.Snapshot.SHA256 = Hash(later)
	assets[c.Target.Snapshot] = later
	data, _ := json.Marshal(d)
	return data, ctx
}
func associationSource(r map[string]any) map[string]any {
	return r["snapshot_association"].(map[string]any)["source"].(map[string]any)
}
func embeddedSnapshot(r map[string]any) map[string]any { return r["snapshot"].(map[string]any) }
func TestAssociationClosedScopeAndClosure(t *testing.T) {
	tests := map[string]func(map[string]any, map[string]any, map[string]any){
		"null association":  func(_, r, _ map[string]any) { r["snapshot_association"] = nil },
		"array association": func(_, r, _ map[string]any) { r["snapshot_association"] = []any{} },
		"unknown field":     func(_, r, _ map[string]any) { r["snapshot_association"].(map[string]any)["fallback"] = true },
		"association alias": func(_, r, _ map[string]any) { r["SNAPSHOT_ASSOCIATION"] = r["snapshot_association"] },
		"selector alias":    func(_, r, _ map[string]any) { r["snapshot_association"].(map[string]any)["OUTPUT_KEY"] = "sqlite" },
		"missing source":    func(_, r, _ map[string]any) { delete(r["snapshot_association"].(map[string]any), "source") },
		"source alias":      func(_, r, _ map[string]any) { associationSource(r)["SHA256"] = geoOriginalHash },
		"external source":   func(_, r, _ map[string]any) { associationSource(r)["repository"] = "https://github.com/example/source" },
		"mutable source":    func(_, r, _ map[string]any) { associationSource(r)["revision"] = "main" },
		"wrong hash":        func(_, r, _ map[string]any) { associationSource(r)["sha256"] = strings.Repeat("c", 64) },
		"path escape":       func(_, r, _ map[string]any) { associationSource(r)["path"] = "../original.json" },
		"URL path":          func(_, r, _ map[string]any) { associationSource(r)["path"] = "https://example.com/original.json" },
		"receipt cycle":     func(_, r, _ map[string]any) { associationSource(r)["path"] = "source/native/geonames_countries.json" },
		"later cycle":       func(_, r, _ map[string]any) { associationSource(r)["path"] = "source/artifact-snapshot.json" },
		"dataset cycle":     func(_, r, _ map[string]any) { associationSource(r)["path"] = "geonames.sqlite" },
		"missing artifact": func(_, _, m map[string]any) {
			out := []any{}
			for _, r := range m["artifacts"].([]any) {
				if r.(map[string]any)["path"] != "source/generation-snapshot.json" {
					out = append(out, r)
				}
			}
			m["artifacts"] = out
		},
		"embedded differs": func(_, r, _ map[string]any) { embeddedSnapshot(r)["extra"] = true },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			b, ctx := associationCase(t, mutate)
			if _, err := Check(b, ctx); err == nil {
				t.Fatal("invalid association admitted")
			}
		})
	}
	for _, selector := range []any{nil, 1, "", "sqlite/file", "outputs.sqlite", "/outputs/sqlite", "$HOME", "*", "..", strings.Repeat("x", 129), "missing", "chunks", "SQLite", " sqlite", "sqlite "} {
		b, ctx := associationCase(t, func(_, r, _ map[string]any) { r["snapshot_association"].(map[string]any)["output_key"] = selector })
		if _, err := Check(b, ctx); err == nil {
			t.Fatal("invalid literal selector admitted", selector)
		}
	}
	// Explicit malformed association never falls back to otherwise valid ROR
	// filename-keyed evidence. Absence still uses that reviewed unchanged route.
	d, ctx, assets := nativeFixture(t)
	c := &d.Contracts[0]
	old := c.Native.Provenance
	var r map[string]any
	_ = numberJSON(assets[old], &r)
	r["snapshot_association"] = nil
	proof, _ := json.Marshal(r)
	c.Native.Provenance.SHA256 = Hash(proof)
	assets[c.Native.Provenance] = proof
	oldMeta := c.Target.Snapshot
	later := []byte(strings.ReplaceAll(string(assets[oldMeta]), old.SHA256, c.Native.Provenance.SHA256))
	c.Target.Snapshot.SHA256 = Hash(later)
	assets[c.Target.Snapshot] = later
	b, _ := json.Marshal(d)
	if _, err := Check(b, ctx); err == nil {
		t.Fatal("failed explicit association fell back")
	}
}
func TestAssociationDescriptorAndCounts(t *testing.T) {
	tests := map[string]func(map[string]any){
		"missing selected output": func(s map[string]any) { delete(s["outputs"].(map[string]any), "sqlite") },
		"selected array":          func(s map[string]any) { s["outputs"].(map[string]any)["sqlite"] = []any{} },
		"wrong file": func(s map[string]any) {
			s["outputs"].(map[string]any)["sqlite"].(map[string]any)["file"] = "other.sqlite"
		},
		"missing file": func(s map[string]any) { delete(s["outputs"].(map[string]any)["sqlite"].(map[string]any), "file") },
		"wrong hash": func(s map[string]any) {
			s["outputs"].(map[string]any)["sqlite"].(map[string]any)["sha256"] = strings.Repeat("d", 64)
		},
		"file alias": func(s map[string]any) {
			s["outputs"].(map[string]any)["sqlite"].(map[string]any)["FILE"] = "geonames.sqlite"
		},
		"hash alias":       func(s map[string]any) { s["outputs"].(map[string]any)["sqlite"].(map[string]any)["SHA256"] = "x" },
		"outputs alias":    func(s map[string]any) { s["OUTPUTS"] = s["outputs"] },
		"counts alias":     func(s map[string]any) { s["COUNTS"] = s["counts"] },
		"missing count":    func(s map[string]any) { delete(s["counts"].(map[string]any), "geonames_countries") },
		"null count":       func(s map[string]any) { s["counts"].(map[string]any)["geonames_countries"] = nil },
		"changed count":    func(s map[string]any) { s["counts"].(map[string]any)["geonames_countries"] = 253 },
		"fractional count": func(s map[string]any) { s["counts"].(map[string]any)["geonames_countries"] = json.Number("252.5") },
		"exponent count":   func(s map[string]any) { s["counts"].(map[string]any)["geonames_countries"] = json.Number("252e0") },
		"negative count":   func(s map[string]any) { s["counts"].(map[string]any)["geonames_countries"] = -1 },
		"overflow count": func(s map[string]any) {
			s["counts"].(map[string]any)["geonames_countries"] = json.Number("9223372036854775808")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			b, ctx := associationCase(t, func(o, r, _ map[string]any) { mutate(o); r["snapshot"] = o })
			if _, err := Check(b, ctx); err == nil {
				t.Fatal("invalid selected original admitted")
			}
		})
	}
	// A correct decoy cannot substitute for the explicitly selected descriptor.
	b, ctx := associationCase(t, func(o, r, _ map[string]any) {
		outputs := o["outputs"].(map[string]any)
		outputs["decoy"] = outputs["sqlite"]
		outputs["sqlite"] = map[string]any{"file": "wrong", "sha256": "wrong"}
		r["snapshot"] = o
	})
	if _, err := Check(b, ctx); err == nil {
		t.Fatal("decoy used as implicit association")
	}
}
func TestAssociationExactNumbers(t *testing.T) {
	for _, pair := range [][2]string{{"9007199254740993", "9007199254740992"}, {"1.0000000000000001", "1"}, {"1e1", "10"}, {"1e309", "1e309"}} {
		b, ctx := associationCase(t, func(o, r, _ map[string]any) {
			o["precision"] = json.Number(pair[0])
			embeddedSnapshot(r)["precision"] = json.Number(pair[1])
		})
		if _, err := Check(b, ctx); err == nil {
			t.Fatal("numeric coercion/overflow admitted", pair)
		}
	}
	for _, token := range []string{"9007199254740993", "1.0000000000000001", "1e1"} {
		b, ctx := associationCase(t, func(o, r, _ map[string]any) { o["precision"] = json.Number(token); r["snapshot"] = o })
		if _, err := Check(b, ctx); err != nil {
			t.Fatal("exact numeric token rejected", token, err)
		}
	}
	// Key order/whitespace changes do not alter parsed equality or numeric tokens.
	b, ctx := associationCase(t, func(_, _, _ map[string]any) {})
	if _, err := Check(b, ctx); err != nil {
		t.Fatal(err)
	}
}
func TestAssociationOriginalResolverFailures(t *testing.T) {
	for _, name := range []string{"unresolved", "hash", "oversize", "duplicate", "malformed", "array"} {
		t.Run(name, func(t *testing.T) {
			d, ctx, _ := associationFixture(t)
			old := ctx.Resolve
			ctx.Resolve = func(r Reference) ([]byte, error) {
				if r.Path == "source/generation-snapshot.json" {
					switch name {
					case "unresolved":
						return nil, errors.New("missing")
					case "hash":
						return []byte(`{}`), nil
					case "oversize":
						return []byte(strings.Repeat(" ", MaxArtifactBytes+1)), nil
					case "duplicate":
						return []byte(`{"outputs":{},"outputs":{}}`), nil
					case "malformed":
						return []byte(`[`), nil
					case "array":
						return []byte(`[]`), nil
					}
				}
				return old(r)
			}
			b, _ := json.Marshal(d)
			if _, err := Check(b, ctx); err == nil {
				t.Fatal("invalid original resolved")
			}
		})
	}
	// Exercise strict original parsing after a deliberately correct byte hash,
	// rather than only detecting a resolver's bad byte checksum.
	for _, raw := range []string{`{"outputs":{},"outputs":{}}`, `[`, strings.Repeat(" ", MaxDocumentBytes+1), `[]`} {
		d, ctx, assets := associationFixture(t)
		c := &d.Contracts[0]
		oldProof, oldMeta := c.Native.Provenance, c.Target.Snapshot
		var receipt map[string]any
		_ = numberJSON(assets[oldProof], &receipt)
		associationSource(receipt)["sha256"] = Hash([]byte(raw))
		proof, _ := json.Marshal(receipt)
		c.Native.Provenance.SHA256 = Hash(proof)
		assets[c.Native.Provenance] = proof
		meta := []byte(strings.ReplaceAll(string(assets[oldMeta]), geoOriginalHash, Hash([]byte(raw))))
		meta = []byte(strings.ReplaceAll(string(meta), oldProof.SHA256, c.Native.Provenance.SHA256))
		c.Target.Snapshot.SHA256 = Hash(meta)
		assets[c.Target.Snapshot] = meta
		assets[Reference{Path: "source/generation-snapshot.json", SHA256: Hash([]byte(raw))}] = []byte(raw)
		b, _ := json.Marshal(d)
		if _, err := Check(b, ctx); err == nil {
			t.Fatal("unchecked original accepted")
		}
	}
}

func TestAssociationReaderRejectsUnvalidatedInputs(t *testing.T) {
	d, ctx, assets := associationFixture(t)
	c := d.Contracts[0]
	var receipt map[string]any
	if err := numberJSON(assets[c.Native.Provenance], &receipt); err != nil {
		t.Fatal(err)
	}
	a := receipt["snapshot_association"]
	if err := checkSnapshotAssociation(a, assets[c.Native.Provenance], c, ctx, []byte(`[]`), 252); err == nil {
		t.Fatal("invalid later metadata accepted")
	}
	if err := checkSnapshotAssociation(a, []byte(`[`), c, ctx, assets[c.Target.Snapshot], 252); err == nil {
		t.Fatal("invalid embedded receipt accepted")
	}
	// The public checker catches this alias on the embedded object first. The
	// original reader also protects its consumed root fields independently.
	data, next := associationCase(t, func(o, r, _ map[string]any) { o["OUTPUTS"] = o["outputs"]; r["snapshot"] = o })
	doc, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	c = doc.Contracts[0]
	proof, err := next.Resolve(c.Native.Provenance)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := next.Resolve(c.Target.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err = numberJSON(proof, &parsed); err != nil {
		t.Fatal(err)
	}
	if err = checkSnapshotAssociation(parsed["snapshot_association"], proof, c, next, metadata, 252); err == nil {
		t.Fatal("original alias accepted")
	}
}
func TestLegacyFilenameCountRemainsInteger(t *testing.T) {
	for _, count := range []any{1.5, json.Number("1e1"), json.Number("9223372036854775808")} {
		data, ctx := mutatedNativeReceipt(t, func(r map[string]any) {
			r["snapshot"].(map[string]any)["counts"].(map[string]any)["organizations"] = count
		})
		if _, err := Check(data, ctx); err == nil {
			t.Fatal("legacy non-int64 count admitted")
		}
	}
}
