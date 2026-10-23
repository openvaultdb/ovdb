package repo

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

// Exact publisher bytes at demo-db/chinook@bbb0ff3, independently of the older
// frozen comparator. Only metadata is retained; no database data is read.
func currentChinook(t *testing.T) *Memory {
	t.Helper()
	root := "testdata/chinook-current"
	raw, err := os.ReadFile(filepath.Join(root, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var provenance struct {
		Repository, Commit string
		Files              map[string]string
	}
	if err := json.Unmarshal(raw, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.Repository != "demo-db/chinook" || provenance.Commit != "bbb0ff328199377f17d3f29aba9bf8d2a6565833" || len(provenance.Files) != 6 {
		t.Fatal("unexpected publisher provenance")
	}
	r := &Memory{Nodes: map[string]Node{}}
	for name, digest := range provenance.Files {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
			t.Fatalf("changed source fixture %s", name)
		}
		r.Nodes[name] = Node{Kind: File, Content: data}
	}
	return r
}

func TestCurrentChinookWithAndWithoutMeaningsInBothManifestFormats(t *testing.T) {
	for _, format := range []string{manifest.ManifestFormat, manifest.ManifestFormatDraft2} {
		for _, profile := range []manifest.Profile{manifest.Directory, manifest.Publisher} {
			for _, modelOnly := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/model-only=%t", format, profile, modelOnly), func(t *testing.T) {
					r := currentChinook(t)
					doc := strings.Replace(string(r.Nodes["ovdb.yaml"].Content), manifest.ManifestFormat, format, 1)
					if modelOnly {
						doc = modelOnlyManifest(doc)
						r.BrokenBlobs = map[string]error{meaningPth: fmt.Errorf("model-only must not open meaning")}
					}
					r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(doc)}
					observer := &meaningReadObserver{Memory: r}
					got := Check(observer, Options{Profile: profile})
					if !got.OK() {
						t.Fatalf("current publisher refused: %v", got.Findings)
					}
					if len(got.Manifest.RecordTypes()) != 11 || !slices.Contains(observer.blobs, modelPath) {
						t.Fatal("current model was not read completely")
					}
					if slices.Contains(observer.blobs, meaningPth) == modelOnly {
						t.Fatalf("wrong semantic read boundary: %v", observer.blobs)
					}
					if modelOnly && (got.Manifest.GraphID.Present || got.Manifest.MeaningFile.Present) {
						t.Fatal("fabricated semantic facts")
					}
				})
			}
		}
	}
}

func TestDraft2ModelOnlyNativeNamesAndColumnsKeepModelChecks(t *testing.T) {
	for _, profile := range []manifest.Profile{manifest.Directory, manifest.Publisher} {
		r := currentChinook(t)
		doc := modelOnlyManifest(string(r.Nodes["ovdb.yaml"].Content))
		doc = strings.Replace(doc, manifest.ManifestFormat, manifest.ManifestFormatDraft2, 1)
		doc = strings.Replace(doc, "  - Artist\n", "  - name: \"music.Artists (live)\"\n    record_type: Artist\n    columns:\n      artist-id:\n        field: ArtistId\n", 1)
		r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(doc)}
		got := Check(r, Options{Profile: profile})
		if !got.OK() || !slices.Contains(got.Manifest.Recordsets.Value, "music.Artists (live)") {
			t.Fatalf("native name lost: %v", got.Findings)
		}
		r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(strings.Replace(doc, "field: ArtistId", "field: Absent", 1))}
		if got := Check(r, Options{Profile: profile}); !slices.Contains(rulesOf(got), RuleColumns) {
			t.Fatalf("missing field accepted: %v", got.Findings)
		}
	}
}

func TestDraft2SharedModelOnlyKeepsPinAndReadBoundary(t *testing.T) {
	for _, profile := range []manifest.Profile{manifest.Directory, manifest.Publisher} {
		r := currentChinook(t)
		doc := modelOnlyManifest(string(r.Nodes["ovdb.yaml"].Content))
		doc = strings.Replace(doc, manifest.ManifestFormat, manifest.ManifestFormatDraft2, 1)
		doc = strings.Replace(doc, "repository: https://github.com/demo-db/chinook", "repository: https://github.com/demo-db/chinook-hosting", 1)
		doc = strings.Replace(doc, "  address: modelspec://github.com/demo-db/chinook/chinook\n  modelspec: model/chinook.modelspec.json\n  hcl: model/chinook.modelspec.hcl\n", "  address: modelspec://github.com/demo-db/chinook/chinook?ref=bbb0ff328199377f17d3f29aba9bf8d2a6565833\n", 1)
		r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(doc)}
		observer := &meaningReadObserver{Memory: r}
		got := Check(observer, Options{Profile: profile})
		if !got.OK() || got.Manifest.Form != manifest.FormShared {
			t.Fatalf("shared model-only manifest refused: %v", got.Findings)
		}
		for _, path := range observer.blobs {
			if path != "OVDB.md" && path != "ovdb.yaml" && path != "ovdb-database.json" {
				t.Fatalf("shared manifest check read %s; it does not resolve foreign closure", path)
			}
		}
		r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(strings.Replace(doc, "?ref=bbb0ff328199377f17d3f29aba9bf8d2a6565833", "?ref=main", 1))}
		if got := Check(r, Options{Profile: profile}); !slices.Contains(rulesOf(got), "manifest-model") {
			t.Fatalf("mutable pin accepted: %v", got.Findings)
		}
	}
}
