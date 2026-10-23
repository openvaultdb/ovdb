package repo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/internal/publisher/rules"
	"github.com/openvaultdb/ovdb/publisher/representation"
)

func TestGitCommittedRecordsPaths(t *testing.T) {
	g, run, replies := headed(t)
	for _, path := range []string{"$records", "$records/item.json", "assertions/$records/item.json", "$records/$records/item.json", "Assertions/$records/Item.json"} {
		replies["ls-tree -z "+commit+":"+path+" --"] = reply{}
		replies["cat-file blob "+commit+":"+path] = reply{out: "committed"}
		if _, err := g.Entries(path); err != nil {
			t.Errorf("Entries(%q): %v", path, err)
		}
		if data, err := g.Blob(path, 9); err != nil || string(data) != "committed" {
			t.Errorf("Blob(%q): %q, %v", path, data, err)
		}
		if rules.IsRepositoryPath(path) || g.Uncommitted(path) {
			t.Errorf("legacy manifest/working-tree grammar accepted %q", path)
		}
	}
	before := len(run.calls)
	for _, path := range []string{"$HOME/x", "$recordsx/x", "embedded$dollar/x", "$records/item$.json", "${HOME}/x", "$(echo)/x", "%24records/x", "$Records/x", "x/$records/../y", "x/./$records/y", "x//$records/y", "/$records/x", "$records/x/", ".git/$records/x", "$records/.GIT/x", "https://host/$records/x", "$records/a:b", "$records/a*b", "$records/a?b", "$records/a[b", "$records/a\\b", "$records/a b", "$records/a\x00b", "$records/a\nb", "$records/a\rb", "$records/" + strings.Repeat("x", rules.MaxPathLength)} {
		if _, err := g.Entries(path); err != ErrMalformed {
			t.Errorf("Entries(%q) = %v", path, err)
		}
		if _, err := g.Blob(path, 1); err != ErrMalformed {
			t.Errorf("Blob(%q) = %v", path, err)
		}
	}
	if len(run.calls) != before {
		t.Fatal("invalid path reached git")
	}
}

func TestRealGitCommittedRecordsPinnedBytes(t *testing.T) {
	realGit(t)
	const path = "assertions/$records/item.json"
	dir := buildReal(t, &model{tracked: map[string]Node{path: {Kind: File, Content: []byte("committed")}}, location: "normal"})
	r := NewGit(ExecRunner{Dir: dir})
	pin, err := r.Head()
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "update-ref", "refs/heads/main", git(t, dir, nil, "commit-tree", "-m", "new head", git(t, dir, nil, "mktree")))
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, err := r.Blob(path, 9); err != nil || string(data) != "committed" {
		t.Fatalf("read at %s = %q, %v", pin, data, err)
	}
	if _, err := r.Blob(path, 8); err != ErrTooLarge {
		t.Fatalf("oversize blob: %v", err)
	}
	if entries, err := r.Entries("assertions/$records"); err != nil || len(entries) != 1 || entries[0] != (Entry{"item.json", File}) {
		t.Fatalf("pinned entries: %v, %v", entries, err)
	}
}

func TestRealGitRecordsRepresentation(t *testing.T) {
	realGit(t)
	const path = "spec/research/public-data-fabric/assertions/$records/d1-edge-chinook-country.json"
	for _, name := range []string{"regular", "symlink", "submodule parent", "symlink parent", "case mismatch", "wrong hash", "wrong revision"} {
		t.Run(name, func(t *testing.T) {
			p, d, _ := contractFixture(t)
			decision := d.Nodes["decision.md"]
			delete(d.Nodes, "decision.md")
			d.Nodes[path] = decision
			switch name {
			case "symlink":
				d.Nodes[path] = Node{Kind: Symlink, Content: []byte("decision.md")}
			case "submodule parent", "symlink parent":
				delete(d.Nodes, path)
				kind := Submodule
				if name == "symlink parent" {
					kind = Symlink
				}
				d.Nodes["spec/research/public-data-fabric/assertions/$records"] = Node{Kind: kind, Content: []byte("outside")}
			case "case mismatch":
				delete(d.Nodes, path)
				d.Nodes[strings.Replace(path, "$records", "$Records", 1)] = decision
			}
			dependency := NewGit(ExecRunner{Dir: buildReal(t, &model{tracked: d.Nodes, location: "normal"})})
			pin, err := dependency.Head()
			if err != nil {
				t.Fatal(err)
			}
			var doc representation.Document
			if err := json.Unmarshal(p.Nodes["contract.json"].Content, &doc); err != nil {
				t.Fatal(err)
			}
			c := &doc.Contracts[0]
			doc.Format = representation.Format2
			c.Execution = representation.LabelBridge
			c.Source.Schema.Revision = pin
			c.Target.Binding.Meaning.Document.Revision = pin
			c.Decision.Document.Revision = pin
			c.Decision.Document.Path = path
			binding := p.Nodes[c.Target.Binding.Document.Path]
			binding.Content = []byte(strings.ReplaceAll(string(binding.Content), strings.Repeat("a", 40), pin))
			p.Nodes[c.Target.Binding.Document.Path] = binding
			c.Target.Binding.Document.SHA256 = representation.Hash(binding.Content)
			if name == "wrong hash" {
				c.Decision.Document.SHA256 = strings.Repeat("0", 64)
			}
			if name == "wrong revision" {
				c.Decision.Document.Revision = strings.Repeat("0", 40)
			}
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			p.Nodes["contract.json"] = Node{Kind: File, Content: data}
			provider := NewGit(ExecRunner{Dir: buildReal(t, &model{tracked: p.Nodes, location: "normal"})})
			mdata := p.Nodes["ovdb.yaml"].Content
			m, _ := manifest.CheckManifest(mdata, "ovdb.yaml", manifest.Directory)
			deps := DependencyReaders{{Repository: c.Source.Schema.Repository, Revision: pin}: dependency}
			if name == "wrong revision" {
				deps[DependencyKey{Repository: c.Source.Schema.Repository, Revision: strings.Repeat("0", 40)}] = dependency
			}
			findings := CheckRepresentation(provider, m, representation.Reference{Path: "contract.json", SHA256: representation.Hash(data)}, deps)
			if (len(findings) == 0) != (name == "regular") {
				t.Fatalf("%s: %v", name, findings)
			}
		})
	}
}
