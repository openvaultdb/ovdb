package repo

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/ovdb/internal/publisher/datarights"
)

// These regression probes were supplied by the independent PR review.
func TestIndependentRightsProfileRejectsCaseAliases(t *testing.T) {
	m := rightsRepository(t)
	var doc map[string]json.RawMessage
	_ = json.Unmarshal(m.Nodes["ovdb.yaml"].Content, &doc)
	profile := strings.Replace(string(doc["data_rights"]), `"format":`, `"DATABASE":{"text":"shadow declaration"},"format":`, 1)
	parsed, err := datarights.ParseProfile([]byte(profile), license.Publisher, []string{"Album", "Artist"})
	if err == nil {
		t.Fatalf("accepted unknown DATABASE alias as declaration: %+v", parsed.Database)
	}
}

func TestIndependentRightsProvenanceRejectsCaseShadow(t *testing.T) {
	raw := `{"format":"ovdb-source-rights/1","attribution":{"text":"canonical source"},"Attribution":{"text":"shadow source"},"transformations":[],"inputs":[],"terms":[]}`
	parsed, err := datarights.ParseProvenance([]byte(raw))
	if err == nil {
		t.Fatalf("accepted alias and shadowed attribution: %+v", parsed.Attribution)
	}
}

func TestIndependentFullCheckRejectsProfileCaseAlias(t *testing.T) {
	m := rightsRepository(t)
	for _, path := range []string{"ovdb.yaml", "ovdb-database.json"} {
		var doc map[string]json.RawMessage
		_ = json.Unmarshal(m.Nodes[path].Content, &doc)
		profile := strings.Replace(string(doc["data_rights"]), `"format":`, `"DATABASE":{"text":"shadow declaration"},"format":`, 1)
		doc["data_rights"] = json.RawMessage(profile)
		if path == "ovdb.yaml" {
			doc["licences"] = json.RawMessage(`{"data":{"text":"shadow declaration"},"model":"MIT","meaning":"CC0-1.0"}`)
		} else {
			doc["licences"] = json.RawMessage(`{"data":{"text":"shadow declaration"}}`)
			doc["recordsets"] = json.RawMessage(`[{"name":"Album","licences":{"data":{"text":"Album-only terms"}}},{"name":"Artist","licences":{"data":{"text":"shadow declaration"}}}]`)
		}
		m.Nodes[path] = Node{Kind: File, Content: rightsEncoded(doc)}
	}
	result := Check(m, publisher())
	if result.OK() || len(result.Manifest.SourceRights) != 0 {
		t.Fatalf("accepted non-contract profile and produced %d verified sources: %+v", len(result.Manifest.SourceRights), result.Manifest.SourceRights)
	}
	t.Logf("refused: %+v", result.Findings)
}

func TestIndependentRejectsMultilineInvalidUTF8Provenance(t *testing.T) {
	raw := []byte(`{"format":"ovdb-source-rights/1","attribution":{"text":"bad`)
	raw = append(raw, 0xff)
	raw = append(raw, []byte(`"},"transformations":[],"inputs":[],"terms":[]}`)...)
	parsed, err := datarights.ParseProvenance(raw)
	if err == nil {
		t.Fatalf("invalid UTF-8 repaired into accepted terms: %q", parsed.Attribution.Text)
	}
}

func TestIndependentProvenanceRejectsNestedUnsafeURLAlias(t *testing.T) {
	raw := []byte(`{"format":"ovdb-source-rights/1","attribution":{"text":"canonical source","URL":"javascript:alert(1)"},"transformations":[],"inputs":[],"terms":[]}`)
	p, err := datarights.ParseProvenance(raw)
	if err == nil {
		t.Fatalf("accepted unsafe attribution URL alias: %+v", p.Attribution)
	}
}

func TestIndependentFullCheckRejectsNestedUnsafeURLAlias(t *testing.T) {
	m := rightsRepository(t)
	raw := []byte(`{"format":"ovdb-source-rights/1","attribution":{"text":"canonical source","URL":"javascript:alert(1)"},"transformations":[],"inputs":[],"terms":[]}`)
	m.Nodes["metadata/provenance.json"] = Node{Kind: File, Content: raw}
	for _, path := range []string{"ovdb.yaml", "ovdb-database.json"} {
		var doc map[string]any
		_ = json.Unmarshal(m.Nodes[path].Content, &doc)
		doc["data_rights"].(map[string]any)["provenance"] = rightsRef("metadata/provenance.json", raw)
		m.Nodes[path] = Node{Kind: File, Content: rightsEncoded(doc)}
	}
	r := Check(m, publisher())
	if r.OK() || len(r.Manifest.SourceRights) != 0 {
		t.Fatalf("unsafe nested URL accepted as verified: %+v", r.Manifest.SourceRights)
	}
}

func TestIndependentFullCheckRejectsAttributionCaseShadow(t *testing.T) {
	m := rightsRepository(t)
	raw := []byte(`{"format":"ovdb-source-rights/1","attribution":{"text":"canonical source"},"Attribution":{"text":"shadow source"},"transformations":[],"inputs":[],"terms":[]}`)
	m.Nodes["metadata/provenance.json"] = Node{Kind: File, Content: raw}
	for _, path := range []string{"ovdb.yaml", "ovdb-database.json"} {
		var doc map[string]any
		_ = json.Unmarshal(m.Nodes[path].Content, &doc)
		doc["data_rights"].(map[string]any)["provenance"] = rightsRef("metadata/provenance.json", raw)
		m.Nodes[path] = Node{Kind: File, Content: rightsEncoded(doc)}
	}
	r := Check(m, publisher())
	if r.OK() || len(r.Manifest.SourceRights) != 0 {
		t.Fatalf("shadowed attribution produced evidence: %+v", r.Manifest.SourceRights)
	}
}

func TestInvalidUTF8RightsDescriptorCannotProduceEvidence(t *testing.T) {
	m := rightsRepository(t)
	n := m.Nodes["ovdb-database.json"]
	n.Content = []byte(strings.Replace(string(n.Content), `{`, `{"description":"ordinary descriptive text",`, 1))
	m.Nodes["ovdb-database.json"] = n
	if r := Check(m, publisher()); !r.OK() {
		t.Fatal("control descriptor was refused:", r.Findings)
	}
	n.Content = []byte(strings.Replace(string(n.Content), "ordinary descriptive text", "bad\xfftext", 1))
	m.Nodes["ovdb-database.json"] = n
	r := Check(m, publisher())
	if r.OK() || len(r.Manifest.SourceRights) != 0 {
		t.Fatalf("invalid UTF-8 descriptor produced evidence: %+v", r.Manifest.SourceRights)
	}
}

func TestIndependentExactDuplicatesStayRefused(t *testing.T) {
	for _, raw := range []string{
		`{"format":"ovdb-source-rights/1","attribution":{"text":"one","text":"two"},"transformations":[],"inputs":[],"terms":[]}`,
		`{"format":"ovdb-source-rights/1","attribution":{"text":"credit"},"transformations":[],"inputs":[],"terms":[],"terms":[]}`,
	} {
		if _, err := datarights.ParseProvenance([]byte(raw)); err == nil {
			t.Fatalf("exact duplicate accepted: %s", raw)
		}
	}
}

func TestIndependentCaseAliasCannotBypassMaterializedMismatch(t *testing.T) {
	m := rightsRepository(t)
	var doc map[string]json.RawMessage
	_ = json.Unmarshal(m.Nodes["ovdb.yaml"].Content, &doc)
	doc["data_rights"] = json.RawMessage(strings.Replace(string(doc["data_rights"]), `"format":`, `"DATABASE":{"text":"different terms"},"format":`, 1))
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: rightsEncoded(doc)}
	r := Check(m, publisher())
	if r.OK() || len(r.Manifest.SourceRights) > 0 {
		t.Fatal("stale materialization accepted or disclosed evidence")
	}
	t.Logf("mismatch rejected: %v", r.Findings)
}
