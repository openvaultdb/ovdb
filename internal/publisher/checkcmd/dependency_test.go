package checkcmd

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/repo"
	"github.com/openvaultdb/ovdb/publisher/representation"
)

func marshalFixture(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func nativeCLIProvider(t *testing.T, revision string, input []byte) (*repo.Memory, *repo.Memory) {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile("../../../publisher/representation/testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var doc representation.Document
	_ = json.Unmarshal(read("contract.json"), &doc)
	doc.Format = representation.Format3
	c := &doc.Contracts[0]
	c.Execution = representation.NativeIdentifier
	c.Source.Namespace = c.Target.Namespace
	c.Bridge = representation.Bridge{}
	c.Target.Keys = representation.Reference{}
	dep := &repo.Memory{Nodes: map[string]repo.Node{}}
	for _, name := range []string{"source.modelspec.json", "core.meaning.json", "decision.md"} {
		dep.Nodes[name] = repo.Node{Kind: repo.File, Content: read(name)}
	}
	dep.Nodes["input/$records/rows.json"] = repo.Node{Kind: repo.File, Content: input}
	c.Source.Schema.Revision = revision
	c.Target.Binding.Meaning.Document.Revision = revision
	c.Decision.Document.Revision = revision
	c.Source.Data = &representation.Reference{Repository: c.Source.Schema.Repository, Revision: revision, Path: "input/$records/rows.json", SHA256: representation.Hash(input)}
	p := chinook(t)
	model := []byte(`{"modelspec":"1.0-draft","module":{"name":"geo"},"entities":{"Countries":{"key":["iso"],"properties":{"iso":{"type":"string","required":true}}}}}`)
	p.Nodes["model/chinook.modelspec.json"] = repo.Node{Kind: repo.File, Content: model}
	c.Target.Model = representation.Reference{Path: "model/chinook.modelspec.json", SHA256: representation.Hash(model)}
	binding := marshalFixture(t, map[string]any{"format": "meaning/draft-1", "id": "chinook", "license": "CC0-1.0", "models": map[string]string{"geo": "chinook.modelspec.hcl"}, "concepts": []any{map[string]any{"id": c.Target.Binding.Concept, "extends": "meaning://github.com/example/source/country?ref=" + revision, "bindings": []any{map[string]string{"model": "modelspec:///geo.Countries", "property": "iso", "role": "identifier"}}}}})
	p.Nodes["model/chinook.meaning.yaml"] = repo.Node{Kind: repo.File, Content: binding}
	c.Target.Binding.Document = representation.Reference{Path: "model/chinook.meaning.yaml", SHA256: representation.Hash(binding)}
	dataset := representation.Reference{Path: "native.sqlite", SHA256: representation.Hash([]byte("native bytes deliberately unread"))}
	receipt := marshalFixture(t, map[string]any{"native_key": map[string]any{"module": c.Target.Module, "entity": c.Target.Entity, "property": c.Target.Property, "namespace": c.Target.Namespace, "model": c.Target.Model, "binding": c.Target.Binding.Document, "dataset": dataset, "records": 0, "duplicates": 0}, "snapshot": map[string]any{"outputs": map[string]any{dataset.Path: map[string]string{"sha256": dataset.SHA256}}, "counts": map[string]int{c.Target.Entity: 0}}})
	provenance := representation.Reference{Path: "receipt.json", SHA256: representation.Hash(receipt)}
	p.Nodes[provenance.Path] = repo.Node{Kind: repo.File, Content: receipt}
	c.Native = &representation.Native{Dataset: dataset, Provenance: provenance}
	snapshot := marshalFixture(t, map[string]any{"generator": map[string]string{"repository": "https://github.com/example/generator", "revision": revision}, "artifacts": []representation.Reference{dataset, provenance, c.Target.Model, c.Target.Binding.Document}})
	c.Target.Snapshot = representation.Reference{Path: "snapshot.json", SHA256: representation.Hash(snapshot)}
	p.Nodes[c.Target.Snapshot.Path] = repo.Node{Kind: repo.File, Content: snapshot}
	attachment := marshalFixture(t, doc)
	p.Nodes["contract.json"] = repo.Node{Kind: repo.File, Content: attachment}
	manifest := map[string]any{"format": "ovdb-manifest/draft-1", "id": "chinook", "title": "CLI fixture", "description": "Bounded native proof", "url": "https://ovdb.example.com/chinook", "deployment": map[string]string{"url": "https://ovdb.example.com/chinook", "engine": "sqlite", "discovery": "https://ovdb.example.com/.well-known/openvaultdb"}, "model": map[string]string{"modelspec": c.Target.Model.Path, "hcl": "model/chinook.modelspec.hcl"}, "meaning": map[string]any{"file": c.Target.Binding.Document.Path, "graph": map[string]string{"id": "chinook", "address": "meaning://github.com/datatug/chinookdb"}}, "publisher": map[string]string{"name": "Fixture", "url": "https://github.com/datatug", "repository": "https://github.com/datatug/chinookdb"}, "licences": map[string]string{"data": "MIT AND Apache-2.0", "model": "MIT", "meaning": "CC0-1.0"}, "recordsets": []string{"Countries"}, "representation_contract": map[string]string{"path": "contract.json", "sha256": representation.Hash(attachment)}}
	p.Nodes["ovdb.yaml"] = repo.Node{Kind: repo.File, Content: marshalFixture(t, manifest)}
	return p, dep
}

func bindingAt(revision, path string) string {
	return "https://github.com/example/source@" + revision + "=" + path
}

func dependencyDeps(p repo.Reader, d repo.Reader, path string, opened *[]string) Deps {
	seams := deps(p)
	seams.Stat = func(name string) (fs.FileInfo, error) {
		if name == path || name == "/unused" || name == "/other" {
			return fs.Stat(dirs, "repo")
		}
		return fs.Stat(dirs, name)
	}
	seams.Open = func(name string) repo.Reader {
		*opened = append(*opened, name)
		if name == path {
			return d
		}
		return p
	}
	return seams
}

func TestDependencyCLISuccessRefusalsAndLiteralPath(t *testing.T) {
	rev := strings.Repeat("b", 40)
	path := "/literal checkout,with@and=equals"
	for _, cause := range []error{nil, repo.ErrCannotRun, repo.ErrOldGit, repo.TimeoutError{}, repo.ErrObjectMissing} {
		p, d := nativeCLIProvider(t, rev, []byte("raw"))
		d.Err = cause
		opened := []string{}
		seams := dependencyDeps(p, commitMemory{Memory: d, commit: rev}, path, &opened)
		args := []string{"check", "repo", "--json", "--dependency", bindingAt(rev, path), "--dependency", bindingAt(rev, path), "--dependency", bindingAt(strings.Repeat("c", 40), "/unused")}
		out, err := execute(t, seams, args...)
		want := 0
		if cause != nil {
			want = 1
			if !errors.Is(cause, repo.ErrObjectMissing) {
				want = 2
			}
		}
		if exitCode(err) != want || len(opened) != 2 || opened[1] != path {
			t.Fatalf("cause %v exit=%d opens=%v out=%s", cause, exitCode(err), opened, out)
		}
		if want == 0 && !strings.Contains(out, `"ok":true`) {
			t.Fatal(out)
		}
	}
	for _, kind := range []repo.Kind{repo.File, repo.Symlink, repo.Submodule, repo.Missing} {
		p, d := nativeCLIProvider(t, rev, []byte("raw"))
		d.Nodes["input/$records/rows.json"] = repo.Node{Kind: kind, Content: []byte("wrong hash")}
		opened := []string{}
		out, err := execute(t, dependencyDeps(p, commitMemory{Memory: d, commit: rev}, path, &opened), "check", "repo", "--dependency", bindingAt(rev, path))
		if exitCode(err) != 1 || !strings.Contains(out, "representation-source-data") {
			t.Fatalf("kind %v: %v %s", kind, err, out)
		}
	}
	p, d := nativeCLIProvider(t, rev, []byte("raw"))
	opened := []string{}
	for _, args := range [][]string{{"check", "repo"}, {"check", "repo", "--dependency", bindingAt(strings.Repeat("c", 40), path)}} {
		out, err := execute(t, dependencyDeps(p, commitMemory{Memory: d, commit: rev}, path, &opened), args...)
		if exitCode(err) != 1 || strings.Contains(out, "OK:") {
			t.Fatalf("missing/wrong binding: %v %s", err, out)
		}
	}
	// A keyed reader at the wrong HEAD refuses as a finding, rather than usage.
	_, err := execute(t, dependencyDeps(p, commitMemory{Memory: d, commit: strings.Repeat("c", 40)}, path, &opened), "check", "repo", "--dependency", bindingAt(rev, path))
	if exitCode(err) != 1 {
		t.Fatal(err)
	}
}

func TestDependencyCLIUsageAndLegacy(t *testing.T) {
	rev := strings.Repeat("a", 40)
	path := "/literal ,@= path"
	legacy := chinook(t)
	invalid := []string{"", bindingAt(rev, "relative"), bindingAt(rev, "~/checkout"), bindingAt(rev, ""), bindingAt(rev, "/bad\x00path"), bindingAt(rev, "/bad\npath"), bindingAt(rev, "/bad\u0085path"), bindingAt("main", path), bindingAt(strings.ToUpper(rev), path), "https://github.com/Example/source@" + rev + "=" + path, "https://github.com/example/source.git@" + rev + "=" + path, "https://github.com/example/source@" + rev + "@x=" + path, bindingAt(rev, "/missing"), bindingAt(rev, "/file")}
	for _, binding := range invalid {
		opened := []string{}
		seams := dependencyDeps(legacy, nil, path, &opened)
		if strings.HasSuffix(binding, "=/file") {
			seams.Stat = func(string) (fs.FileInfo, error) { return fs.Stat(dirs, "afile") }
		}
		out, err := execute(t, seams, "check", "repo", "--dependency", binding)
		if exitCode(err) != 2 || out != "" || len(opened) != 0 {
			t.Fatalf("invalid %q: code=%d opens=%v out=%s", binding, exitCode(err), opened, out)
		}
	}
	opened := []string{}
	seams := dependencyDeps(legacy, nil, path, &opened)
	out, err := execute(t, seams, "check", "repo", "--dependency", bindingAt(rev, path), "--dependency", bindingAt(rev, path))
	if err != nil || len(opened) != 1 || !strings.Contains(out, "OK:") {
		t.Fatalf("legacy: %v %v %s", err, opened, out)
	}
	_, err = execute(t, seams, "check", "repo", "--dependency", bindingAt(rev, path), "--dependency", bindingAt(rev, "/other"))
	if exitCode(err) != 2 {
		t.Fatal("conflicting bindings accepted")
	}
}

// Actual filesystem and isolated Git Reader use: source paths remain literal;
// mutating the checkout after commit neither changes bytes nor executes scripts.
func TestDependencyCLIRealFilesystem(t *testing.T) {
	rev := strings.Repeat("b", 40)
	_, d := nativeCLIProvider(t, rev, []byte("raw exact file"))
	root := t.TempDir()
	dependencyPath := filepath.Join(root, "literal checkout,with@and=equals")
	if err := os.Mkdir(dependencyPath, 0700); err != nil {
		t.Fatal(err)
	}
	makeRepo := func(dir string, nodes map[string]repo.Node) string {
		t.Helper()
		git := func(args ...string) string {
			cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false"}, args...)...)
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("git %v %v %s", args, err, out)
			}
			return strings.TrimSpace(string(out))
		}
		git("init", "--quiet", "-b", "main")
		for name, node := range nodes {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, node.Content, 0600); err != nil {
				t.Fatal(err)
			}
		}
		git("add", "-A")
		git("commit", "--quiet", "-m", "fixture")
		return git("rev-parse", "HEAD")
	}
	revision := makeRepo(dependencyPath, d.Nodes)
	p, _ := nativeCLIProvider(t, revision, []byte("raw exact file"))
	providerPath := filepath.Join(root, "provider")
	_ = os.Mkdir(providerPath, 0700)
	makeRepo(providerPath, p.Nodes)
	seams := deps(nil)
	real := Real()
	seams.Open, seams.Stat = real.Open, real.Stat
	out, err := execute(t, seams, "check", providerPath, "--json", "--dependency", bindingAt(revision, dependencyPath))
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("real reader %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dependencyPath, "input/$records/rows.json"), []byte("uncommitted wrong bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = execute(t, seams, "check", providerPath, "--json", "--dependency", bindingAt(revision, dependencyPath))
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("working tree influenced proof: %v %s", err, out)
	}
}

func TestLazyDependencyPreservesWorkingTreeSeam(t *testing.T) {
	memory := &repo.Memory{Untracked: map[string]bool{"uncommitted": true}}
	lazy := &lazyDependency{path: "/literal", open: func(string) repo.Reader { return memory }}
	if lazy.unrunnable() != nil || !lazy.Uncommitted("uncommitted") {
		t.Fatal("lazy reader seam")
	}
}

func TestTwoReferencedRevisionsOfOneDependency(t *testing.T) {
	revA, revB := strings.Repeat("b", 40), strings.Repeat("c", 40)
	p, metadata := nativeCLIProvider(t, revA, []byte("raw"))
	raw := p.Nodes["contract.json"].Content
	var doc representation.Document
	_ = json.Unmarshal(raw, &doc)
	doc.Contracts[0].Source.Data.Revision = revB
	raw = marshalFixture(t, doc)
	p.Nodes["contract.json"] = repo.Node{Kind: repo.File, Content: raw}
	var m map[string]any
	_ = json.Unmarshal(p.Nodes["ovdb.yaml"].Content, &m)
	m["representation_contract"] = map[string]string{"path": "contract.json", "sha256": representation.Hash(raw)}
	p.Nodes["ovdb.yaml"] = repo.Node{Kind: repo.File, Content: marshalFixture(t, m)}
	seams := deps(p)
	seams.Stat = func(string) (fs.FileInfo, error) { return fs.Stat(dirs, "repo") }
	opened := []string{}
	seams.Open = func(path string) repo.Reader {
		opened = append(opened, path)
		if path == "/a" {
			return commitMemory{Memory: metadata, commit: revA}
		}
		if path == "/b" {
			return commitMemory{Memory: metadata, commit: revB}
		}
		return p
	}
	out, err := execute(t, seams, "check", "repo", "--json", "--dependency", bindingAt(revA, "/a"), "--dependency", bindingAt(revB, "/b"))
	if err != nil || len(opened) != 3 || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("two revisions: %v %v %s", err, opened, out)
	}
}
