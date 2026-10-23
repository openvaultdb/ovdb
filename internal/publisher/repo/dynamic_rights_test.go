package repo

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

func syntheticDynamicRepository(t *testing.T) *Memory {
	t.Helper()
	m := httpDefinitionRepository(t)
	for _, name := range []string{"ovdb.yaml", "ovdb-database.json"} {
		n := m.Nodes[name]
		var document map[string]any
		if err := json.Unmarshal(n.Content, &document); err != nil {
			t.Fatal(err)
		}
		d := document["source_definition"].(map[string]any)
		d["provider"] = map[string]any{"id": "synthetic", "name": "Synthetic provider", "url": "https://example.org/"}
		for native, value := range d["recordsets"].(map[string]any) {
			value.(map[string]any)["providerSourceId"] = "provider:synthetic/" + native
		}
		rights := d["rights"].(map[string]any)
		declaration := map[string]any{"name": "Synthetic terms", "url": "https://example.org/terms"}
		rights["declaration"] = declaration
		rights["terms"] = []any{map[string]any{"text": "Synthetic terms", "url": "https://example.org/terms"}}
		rights["attribution"] = map[string]any{"text": "Synthetic provider", "url": "https://example.org/"}
		rights["freeSource"].(map[string]any)["text"] = "Synthetic original free source"
		rights["transformations"] = []any{"Synthetic XML restructured into rows"}
		document["licences"].(map[string]any)["data"] = declaration
		if recordsets, ok := document["recordsets"].([]any); ok && name == "ovdb-database.json" {
			for _, value := range recordsets {
				value.(map[string]any)["licences"] = map[string]any{"data": declaration}
			}
		}
		n.Content = rightsEncoded(document)
		m.Nodes[name] = n
	}
	return m
}

func TestPrepareDynamicSourceRightPreservesNoticesAndRefusal(t *testing.T) {
	m := syntheticDynamicRepository(t)
	identity := license.Identity{ServerID: "synthetic-proxy", DatabaseID: "local", Recordset: "daily"}
	for _, profile := range []manifest.Profile{manifest.Publisher, manifest.Directory} {
		right, err := PrepareDynamicSourceRight(m, Options{Profile: profile}, "Album", identity)
		if err != nil {
			t.Fatal(err)
		}
		result := Check(m, Options{Profile: profile})
		d := result.Manifest.SourceDefinition.Value
		if right.Source != identity || right.SourceID != identity.SourceID() || right.Declaration != d.Rights.Declaration.Normalized() ||
			right.EvidenceOrigin != "publisher-definition-verified" || !reflect.DeepEqual(*right.Attribution, d.Rights.Attribution) ||
			!reflect.DeepEqual(*right.FreeSource, d.Rights.FreeSource) || !reflect.DeepEqual(right.Transformations, d.Rights.Transformations) ||
			!reflect.DeepEqual(right.Pins, []license.Pin{result.Manifest.SourceDefinitionEvidence.Manifest}) {
			t.Fatal("metadata changed during preparation", right)
		}
		right.Attribution.Text = "mutated"
		right.Transformations[0] = "mutated"
		if d.Rights.Attribution.Text == "mutated" || d.Rights.Transformations[0] == "mutated" || d.RequireExecution() == nil || len(result.Manifest.SourceRights) != 0 {
			t.Fatal("preparation mutated canonical metadata or enabled execution")
		}
	}
}

func TestPrepareDynamicSourceRightRejectsInvalidInputs(t *testing.T) {
	valid := license.Identity{ServerID: "synthetic-proxy", DatabaseID: "local", Recordset: "daily"}
	for _, identity := range []license.Identity{{}, {ServerID: "proxy", DatabaseID: "local"}, {ServerID: "bad\nidentity", DatabaseID: "local", Recordset: "daily"}, {ServerID: "proxy", DatabaseID: strings.Repeat("a", 257), Recordset: "daily"}} {
		if _, err := PrepareDynamicSourceRight(syntheticDynamicRepository(t), publisher(), "Album", identity); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
	if _, err := PrepareDynamicSourceRight(syntheticDynamicRepository(t), publisher(), "missing", valid); err == nil {
		t.Fatal("unknown native recordset accepted")
	}
	m := syntheticDynamicRepository(t)
	n := m.Nodes["ovdb-database.json"]
	n.Content = []byte(strings.Replace(string(n.Content), `"name":"Synthetic terms"`, `"name":"Contradictory terms"`, 1))
	m.Nodes["ovdb-database.json"] = n
	if _, err := PrepareDynamicSourceRight(m, publisher(), "Album", valid); err == nil {
		t.Fatal("unclean publisher accepted")
	}
}

// A non-Git Reader is an explicitly trusted seam. Simulate it violating the
// immutable-read contract after Check to prove preparation refuses stale pins.
type driftingDynamicReader struct {
	Reader
	fault string
	heads int
}

func (r *driftingDynamicReader) Head() (string, error) {
	r.heads++
	if r.heads == 3 {
		if r.fault == "head unavailable" {
			return "", errors.New("synthetic read failure")
		}
		if r.fault == "revision changed" {
			return strings.Repeat("b", 40), nil
		}
	}
	return r.Reader.Head()
}

func (r *driftingDynamicReader) Blob(name string, limit int) ([]byte, error) {
	data, err := r.Reader.Blob(name, limit)
	if r.heads == 3 && name == "ovdb.yaml" {
		switch r.fault {
		case "blob unavailable":
			return nil, errors.New("synthetic read failure")
		case "size changed":
			return append(bytes.Clone(data), ' '), nil
		case "digest changed":
			copy := bytes.Clone(data)
			copy[0] = ' '
			return copy, nil
		}
	}
	return data, err
}

func TestPrepareDynamicSourceRightRefusesReadDrift(t *testing.T) {
	for fault, expected := range map[string]string{
		"head unavailable": "identity changed", "revision changed": "identity changed",
		"blob unavailable": "bytes unavailable", "size changed": "bytes changed", "digest changed": "bytes changed",
	} {
		t.Run(fault, func(t *testing.T) {
			r := &driftingDynamicReader{Reader: syntheticDynamicRepository(t), fault: fault}
			_, err := PrepareDynamicSourceRight(r, publisher(), "Album", license.Identity{ServerID: "proxy", DatabaseID: "local", Recordset: "daily"})
			if err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatal("drift was accepted or refused for the wrong reason", err)
			}
		})
	}
}
