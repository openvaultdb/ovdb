package preflight

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/openvaultdb/ovdb/publisher/source"
	"github.com/openvaultdb/ovdb/publisher/source/pinchain"
)

// Current byte expectations deliberately carry no historical revision or
// acceptance claim. checkCurrentMetadata binds them to the checkout's HEAD.
func currentMetadataInventory(t *testing.T) []pinchain.Artifact {
	t.Helper()
	var inventory struct {
		Format    string              `json:"format"`
		Artifacts []pinchain.Artifact `json:"artifacts"`
	}
	if err := json.Unmarshal(bytesAt(t, "metadata/ecb-daily.current-artifacts.json"), &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.Format != "ovdb-ecb-current-metadata-inventory/1" || len(inventory.Artifacts) != 4 {
		t.Fatal("current metadata inventory changed")
	}
	for i, role := range []string{"publisher-index", "publisher-manifest", "paired-descriptor", "source-definition"} {
		if a := inventory.Artifacts[i]; a.Role != role || a.Repository != "openvaultdb/ovdb" || a.Commit != "" {
			t.Fatal("current byte inventory asserted historical acceptance", a)
		}
	}
	return inventory.Artifacts
}

func checkMetadataBytes(t *testing.T, data []byte, a pinchain.Artifact) {
	t.Helper()
	blob := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(data))), data...))
	if len(data) != a.Bytes || hash(data) != a.SHA256 || fmt.Sprintf("%x", blob) != a.Blob {
		t.Fatal("metadata bytes differ from exact inventory", a.Path)
	}
}

func TestHistoricalECBMetadataCopiesMatchOriginalPins(t *testing.T) {
	var inventory struct{ Artifacts []pinchain.Artifact }
	if err := json.Unmarshal(bytesAt(t, "metadata/ecb-daily.original-artifacts.json"), &inventory); err != nil {
		t.Fatal(err)
	}
	for _, a := range inventory.Artifacts[1:] {
		checkMetadataBytes(t, bytesAt(t, filepath.Join("metadata/historical", filepath.Base(a.Path))), a)
	}
}

func TestCurrentECBTermsInventoryRemainsBlocked(t *testing.T) {
	var definition *source.Definition
	for _, a := range currentMetadataInventory(t) {
		data := bytesAt(t, filepath.Join("../../..", a.Path))
		checkMetadataBytes(t, data, a)
		if a.Role == "publisher-index" {
			continue
		}
		if a.Role != "source-definition" {
			var wrapped struct {
				Definition json.RawMessage `json:"source_definition"`
			}
			if err := json.Unmarshal(data, &wrapped); err != nil {
				t.Fatal(err)
			}
			data = wrapped.Definition
		}
		d, err := source.Parse(data)
		if err != nil {
			t.Fatal("current blocked definition refused", a.Path, err)
		}
		if definition != nil && !reflect.DeepEqual(definition, d) {
			t.Fatal("current example/manifest/descriptor definitions diverged")
		}
		definition = d
		if d.RequireExecution() == nil || d.Admission.ExecutionEnabled || d.Admission.Status != "blocked" {
			t.Fatal("current terms update admitted execution")
		}
		if len(d.Rights.Terms) != 2 || d.Rights.Terms[0].URL != "https://www.ecb.europa.eu/services/using-our-site/disclaimer/html/index.en.html" || d.Rights.Terms[1].URL != "https://www.ecb.europa.eu/stats/ecb_statistics/governance_and_quality_framework/html/usage_policy.en.html" {
			t.Fatal("copyright and statistics reuse policy inventory lost")
		}
	}
}
