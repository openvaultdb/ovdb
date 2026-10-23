package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

func modelOnlyManifest(doc string) string {
	start, end := strings.Index(doc, "meaning:\n"), strings.Index(doc, "publisher:\n")
	return strings.Replace(doc[:start]+doc[end:], "  meaning: CC0-1.0\n", "", 1)
}

type meaningReadObserver struct {
	*Memory
	blobs []string
}

func (r *meaningReadObserver) Blob(path string, limit int) ([]byte, error) {
	r.blobs = append(r.blobs, path)
	return r.Memory.Blob(path, limit)
}

func TestOptionalMeaningOwnRepositoryStillReadsModel(t *testing.T) {
	for _, profile := range []manifest.Profile{manifest.Directory, manifest.Publisher} {
		p := goodRepository()
		p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(modelOnlyManifest(ownManifest))}
		// An unrelated unreadable semantic file cannot affect a model-only check.
		p.BrokenBlobs = map[string]error{meaningPth: errors.New("semantic file must not be opened")}
		observer := &meaningReadObserver{Memory: p}
		got := Check(observer, Options{Profile: profile})
		if !got.OK() {
			t.Fatalf("profile %d: %v", profile, got.Findings)
		}
		if !slices.Contains(observer.blobs, modelPath) || slices.Contains(observer.blobs, meaningPth) {
			t.Fatalf("wrong file boundary: %v", observer.blobs)
		}
		if got.Manifest.GraphID.Present || got.Manifest.GraphAddress.Present || got.Manifest.MeaningAddress.Present || got.Manifest.MeaningFile.Present {
			t.Fatal("model-only check fabricated semantic evidence")
		}
	}
	for _, path := range []string{modelPath, hclPath} {
		for _, kind := range []Kind{Missing, Symlink, Submodule} {
			t.Run(fmt.Sprintf("%s/%s", path, kind), func(t *testing.T) {
				p := goodRepository()
				p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(modelOnlyManifest(ownManifest))}
				p.Nodes[path] = Node{Kind: kind}
				got := Check(p, publisher())
				if !slices.Contains(rulesOf(got), RuleFile) {
					t.Fatalf("model file protection lost: %v", got.Findings)
				}
			})
		}
	}
	for _, tc := range []struct{ data, rule string }{
		{"{", RuleModelJSON},
		{strings.Replace(goodModel, `"chinook"`, `"other"`, 1), RuleModelAddress},
		{strings.Replace(goodModel, `"Artist"`, `"Extra"`, 1), RuleRecordsets},
	} {
		p := goodRepository()
		p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(modelOnlyManifest(ownManifest))}
		p.Nodes[modelPath] = Node{Kind: File, Content: []byte(tc.data)}
		if got := Check(p, publisher()); !slices.Contains(rulesOf(got), tc.rule) {
			t.Fatalf("model content check lost: %v", got.Findings)
		}
	}
}

func TestOptionalMeaningSharedKeepsCurrentDependencyBoundary(t *testing.T) {
	// Draft-1's legacy shared check validates manifest pins only. It has never
	// resolved external metadata without an attachment; optional meaning does not
	// claim that this is a complete shared-model content or admission check.
	for _, doc := range []string{sharedManifest, modelOnlyManifest(sharedManifest)} {
		p := &Memory{Nodes: map[string]Node{"OVDB.md": {Kind: File, Content: []byte(goodMD)}, "ovdb.yaml": {Kind: File, Content: []byte(doc)}}}
		observer := &meaningReadObserver{Memory: p}
		if got := Check(observer, publisher()); !got.OK() {
			t.Fatalf("legacy shared form refused: %v", got.Findings)
		}
		for _, path := range observer.blobs {
			if path != "OVDB.md" && path != "ovdb.yaml" {
				t.Fatalf("legacy shared check opened %s", path)
			}
		}
		bad := strings.Replace(doc, "?ref=8c9e62ed6641c0a00faa3867167d928af4c44b06", "?ref=main", 1)
		p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(bad)}
		if got := Check(p, publisher()); !slices.Contains(rulesOf(got), "manifest-model") {
			t.Fatalf("model pin protection lost: %v", got.Findings)
		}
	}
}

func TestOptionalMeaningStillChecksSuppliedSemanticFiles(t *testing.T) {
	for _, kind := range []Kind{Missing, Symlink, Submodule} {
		p := goodRepository()
		p.Nodes[meaningPth] = Node{Kind: kind}
		if got := Check(p, publisher()); !slices.Contains(rulesOf(got), RuleFile) {
			t.Fatalf("supplied meaning file protection lost: %v", got.Findings)
		}
	}
	for _, tc := range []struct{ text, rule string }{
		{strings.Replace(goodMeaning, "id: chinook", "id: other", 1), "meaning-id"},
		{strings.Replace(goodMeaning, "license: CC0-1.0", "license: MIT", 1), "meaning-license"},
		{strings.Replace(goodMeaning, "chinook.modelspec.hcl", "other.modelspec.hcl", 1), "meaning-hcl"},
		{strings.Replace(goodMeaning, "concepts: []", "concepts:\n  - id: broken\n    bindings:\n      - model: modelspec:///chinook.Missing\n        role: entity", 1), "meaning-binding"},
		{strings.Replace(goodMeaning, "concepts: []", "concepts:\n  - id: broken\n    extends: missing", 1), "meaning-chain"},
	} {
		p := goodRepository()
		p.Nodes[meaningPth] = Node{Kind: File, Content: []byte(tc.text)}
		if got := Check(p, publisher()); !slices.Contains(rulesOf(got), tc.rule) {
			t.Fatalf("want %s for supplied semantics, got %v", tc.rule, got.Findings)
		}
	}
}

func TestOptionalMeaningDoesNotRelaxRepresentationEligibility(t *testing.T) {
	for _, format := range []string{manifest.ManifestFormat, manifest.ManifestFormatDraft2} {
		for _, shared := range []bool{false, true} {
			p, deps, ref := defaultNativeFixture(t, "real-ror", []byte("exact offline proof"))
			var doc map[string]any
			if err := json.Unmarshal(p.Nodes["ovdb.yaml"].Content, &doc); err != nil {
				t.Fatal(err)
			}
			delete(doc, "meaning")
			doc["format"] = format
			if shared {
				doc["model"] = map[string]any{"address": "modelspec://github.com/example/shared/ror?ref=" + strings.Repeat("b", 40)}
			}
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: raw}
			got := Check(p, Options{Profile: manifest.Publisher, Dependencies: deps})
			if !slices.Contains(rulesOf(got), "representation-manifest-link") {
				t.Fatalf("model-only attachment acquired eligibility: %v", got.Findings)
			}
			if calls := deps[DependencyKey{ref.Repository, ref.Revision}].(*sourceReader).blobCalls; calls != 0 {
				t.Fatalf("ineligible metadata opened raw data %d times", calls)
			}
		}
	}
}
