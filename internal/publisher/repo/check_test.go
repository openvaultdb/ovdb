package repo

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

const ownManifest = `format: ovdb-manifest/draft-1
id: chinook
title: Chinook music store
description: A sample database.
homepage: https://chinookdb.com/
url: https://chinookdb.com/ovdb/dbs/chinook
deployment:
  url: https://cloud.openvaultdb.com/ovdb/dbs/chinook
  engine: sqlite
  discovery: https://chinookdb.com/.well-known/openvaultdb
  recordset_page: https://cloud.openvaultdb.com/ovdb/dbs/chinook/collections/{name}
model:
  address: modelspec://github.com/datatug/chinookdb/chinook
  modelspec: model/chinook.modelspec.json
  hcl: model/chinook.modelspec.hcl
meaning:
  file: model/chinook.meaning.yaml
  graph:
    id: chinook
    address: meaning://github.com/datatug/chinookdb
publisher:
  name: DataTug
  url: https://github.com/datatug
  repository: https://github.com/datatug/chinookdb
licences:
  data: MIT
  model: MIT
  meaning: CC0-1.0
recordsets:
  - Album
  - Artist
`

const sharedManifest = `format: ovdb-manifest/draft-1
id: chinook-acme
title: Chinook at Acme
description: Hosted by Acme.
url: https://ovdb.acme.io/dbs/chinook
deployment:
  url: https://cloud.acme.io/ovdb/dbs/chinook
  engine: postgres
  discovery: https://ovdb.acme.io/.well-known/openvaultdb
model:
  address: modelspec://github.com/datatug/chinookdb/chinook?ref=8c9e62ed6641c0a00faa3867167d928af4c44b06
meaning:
  address: meaning://github.com/datatug/chinookdb?ref=8c9e62ed6641c0a00faa3867167d928af4c44b06
  file: model/chinook.meaning.yaml
  graph:
    id: chinook
publisher:
  name: Acme
  url: https://github.com/acme
  repository: https://github.com/acme/chinook-hosting
licences:
  data: MIT
recordsets:
  - Album
`

const (
	goodMD     = "---\novdb: 1\npublish: [./ovdb.yaml]\n---\n# Title\n"
	ownRepo    = "https://github.com/datatug/chinookdb"
	modelPath  = "model/chinook.modelspec.json"
	hclPath    = "model/chinook.modelspec.hcl"
	meaningPth = "model/chinook.meaning.yaml"
)

// goodRepository is a repository that holds everything the own manifest names.
func goodRepository() *Memory {
	m := &Memory{Nodes: map[string]Node{}, Untracked: map[string]bool{}}
	for path, text := range map[string]string{"OVDB.md": goodMD, "ovdb.yaml": ownManifest, modelPath: "{}", hclPath: "module", meaningPth: "id: chinook"} {
		m.Nodes[path] = Node{Kind: File, Content: []byte(text)}
	}
	return m
}

func publisher() Options { return Options{Profile: manifest.Publisher} }

func rulesOf(r manifest.Result) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Rule)
	}
	return out
}

func only(t *testing.T, r manifest.Result, rule, document string, line int, message string) manifest.Finding {
	t.Helper()
	if len(r.Findings) != 1 {
		t.Fatalf("findings = %v, want one of %s", r.Findings, rule)
	}
	f := r.Findings[0]
	if f.Rule != rule || f.Document != document || f.Line != line || !strings.Contains(f.Message, message) {
		t.Errorf("finding = %+v, want rule %s in %s at %d with %q", f, rule, document, line, message)
	}
	assertBounded(t, r.Findings)
	return f
}

func TestAGoodRepository(t *testing.T) {
	r := Check(goodRepository(), publisher())
	if !r.OK() || r.Profile != manifest.Publisher || !r.OVDBMd.Read || !r.Manifest.Read || r.Manifest.Form != manifest.FormOwn {
		t.Fatalf("result = %+v", r)
	}
	if !slices.Equal(r.OVDBMd.Entries, []string{"ovdb.yaml"}) || r.OVDBMd.EntryLines[0] != 3 {
		t.Errorf("entries %v at %v", r.OVDBMd.Entries, r.OVDBMd.EntryLines)
	}
	same := ownRepo
	if r := Check(goodRepository(), Options{Profile: manifest.Publisher, Repository: &same}); !r.OK() {
		t.Errorf("with the repository: %v", r.Findings)
	}
	// The profile is the caller's: the Directory profile's rules judge a manifest that the Publisher's refuse.
	m := goodRepository()
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(strings.Replace(ownManifest, "  repository: https://github.com/datatug/chinookdb\n", "", 1))}
	if r := Check(m, Options{Profile: manifest.Directory}); !r.OK() || r.Profile != manifest.Directory {
		t.Errorf("Directory profile: %v", r.Findings)
	}
	if r := Check(m, publisher()); r.OK() {
		t.Error("the Publisher profile accepts a manifest without publisher.repository")
	}
}

func TestAnUnknownProfileJudgesNothing(t *testing.T) {
	r := Check(goodRepository(), Options{Profile: 7})
	if r.Profile != 7 || len(r.Findings) != 1 || r.Findings[0].Rule != manifest.RuleProfile {
		t.Errorf("result = %+v", r)
	}
}

func TestARepositoryThatCannotBeRead(t *testing.T) {
	for _, c := range []struct {
		err  error
		rule string
	}{
		{ErrNoCommit, RuleNoCommit},
		{ErrSubdirectory, RuleSubdirectory},
		{ErrBare, RuleBare},
		{&ExitError{Code: 128, Stderr: "fatal: not a git repository\x1b[31m"}, RuleUnreadable},
		{ErrMalformed, RuleUnreadable},
	} {
		m := goodRepository()
		m.Err = c.err
		r := Check(m, publisher())
		f := only(t, r, c.rule, "repository", 0, "cannot be read: ")
		if r.OVDBMd.Read || r.Manifest.Read {
			t.Errorf("%v: nothing should be read", c.err)
		}
		if strings.Contains(f.Message, "\x1b") {
			t.Errorf("%q has an escape", f.Message)
		}
	}
}

func TestOVDBMdMustBeATrackedRegularFile(t *testing.T) {
	for _, c := range []struct {
		name    string
		change  func(m *Memory)
		message string
	}{
		{"missing", func(m *Memory) { delete(m.Nodes, "OVDB.md") }, "but it is missing"},
		{"in the working tree only", func(m *Memory) { delete(m.Nodes, "OVDB.md"); m.Untracked["OVDB.md"] = true }, "but it is missing (it is in the working tree or the index but not committed"},
		{"a directory", func(m *Memory) { delete(m.Nodes, "OVDB.md"); m.Nodes["OVDB.md/inner"] = Node{Kind: File} }, "but it is a directory"},
		{"a symlink", func(m *Memory) { m.Nodes["OVDB.md"] = Node{Kind: Symlink, Content: []byte("ovdb.yaml")} }, "but it is a symlink"},
		{"a submodule", func(m *Memory) { m.Nodes["OVDB.md"] = Node{Kind: Submodule} }, "but it is a submodule"},
		{"another kind", func(m *Memory) { m.Nodes["OVDB.md"] = Node{Kind: Other} }, "but it is not a regular file"},
		{"in another case", func(m *Memory) { m.Nodes["ovdb.md"] = m.Nodes["OVDB.md"]; delete(m.Nodes, "OVDB.md") }, `but it is missing (the commit has "ovdb.md", which differs in case)`},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := goodRepository()
			c.change(m)
			r := Check(m, publisher())
			only(t, r, RuleOVDBMd, "OVDB.md", 0, "OVDB.md must be a tracked regular file, "+c.message)
			if r.OVDBMd.Read {
				t.Error("OVDB.md was read")
			}
		})
	}
	m := goodRepository()
	m.Nodes["OVDB.md"] = Node{Kind: Executable, Content: []byte(goodMD)}
	if r := Check(m, publisher()); !r.OK() {
		t.Errorf("an executable OVDB.md: %v", r.Findings)
	}
}

func TestEveryListedManifestMustBeATrackedRegularFile(t *testing.T) {
	m := goodRepository()
	m.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish:\n  - ./ovdb.yaml\n  - ./other.yaml\n---\n")}
	m.Nodes["other.yaml"] = Node{Kind: Symlink, Content: []byte("ovdb.yaml")}
	r := Check(m, publisher())
	only(t, r, RuleManifest, "OVDB.md", 5, `publish entry "./other.yaml" must be a tracked regular file, but it is a symlink`)
	if !r.Manifest.Read {
		t.Error("the manifest that is a file was not judged")
	}
}

func TestEveryFileAnOwnManifestNamesMustBeATrackedRegularFile(t *testing.T) {
	for _, c := range []struct {
		path, label string
		line        int
	}{{modelPath, "model.modelspec", 14}, {hclPath, "model.hcl", 15}, {meaningPth, "meaning.file", 17}} {
		for name, change := range map[string]func(m *Memory){
			"missing":      func(m *Memory) { delete(m.Nodes, c.path) },
			"a symlink":    func(m *Memory) { m.Nodes[c.path] = Node{Kind: Symlink} },
			"a submodule":  func(m *Memory) { m.Nodes[c.path] = Node{Kind: Submodule} },
			"a directory":  func(m *Memory) { delete(m.Nodes, c.path); m.Nodes[c.path+"/x"] = Node{Kind: File} },
			"working tree": func(m *Memory) { delete(m.Nodes, c.path); m.Untracked[c.path] = true },
		} {
			t.Run(c.label+" "+name, func(t *testing.T) {
				m := goodRepository()
				change(m)
				r := Check(m, publisher())
				f := only(t, r, RuleFile, "ovdb.yaml", c.line, fmt.Sprintf("%s %q must be a tracked regular file, but it is ", c.label, c.path))
				if name == "working tree" && !strings.Contains(f.Message, "in the working tree") {
					t.Error(f.Message)
				}
			})
		}
	}
	m := goodRepository()
	m.Nodes[modelPath] = Node{Kind: Executable}
	if r := Check(m, publisher()); !r.OK() {
		t.Errorf("an executable model file: %v", r.Findings)
	}
}

func TestASharedManifestNamesNoFileOfThisRepository(t *testing.T) {
	m := goodRepository()
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(sharedManifest)}
	delete(m.Nodes, meaningPth)
	if r := Check(m, publisher()); !r.OK() || r.Manifest.Form != manifest.FormShared {
		t.Errorf("findings: %v", r.Findings)
	}
}

func TestAPathBelowSomethingThatIsNotADirectoryIsMissing(t *testing.T) {
	for kind, want := range map[Kind]string{File: "is a regular file, not a directory", Symlink: "is a symlink, not a directory", Submodule: "is a submodule, not a directory"} {
		m := goodRepository()
		for path := range m.Nodes {
			if strings.HasPrefix(path, "model/") {
				delete(m.Nodes, path)
			}
		}
		m.Nodes["model"] = Node{Kind: kind}
		r := Check(m, publisher())
		if len(r.Findings) != 3 || !strings.Contains(r.Findings[0].Message, `"model" `+want) {
			t.Errorf("%v: %v", kind, r.Findings)
		}
	}
}

func TestRepositoryOptionIsComparedExactly(t *testing.T) {
	for _, c := range []struct {
		value string
		ok    bool
	}{{ownRepo, true}, {"https://github.com/other/chinookdb", false}, {"https://github.com/DataTug/chinookdb", false}, {ownRepo + "/", false}, {"", false}} {
		r := Check(goodRepository(), Options{Profile: manifest.Publisher, Repository: &c.value})
		if r.OK() != c.ok {
			t.Errorf("%q: findings %v", c.value, r.Findings)
		}
		if !c.ok {
			only(t, r, RuleRepository, "ovdb.yaml", 24, "publisher.repository must be ")
		}
	}
	// A publisher.repository the manifest rules refuse is not compared: its own finding says what is wrong.
	m := goodRepository()
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(strings.Replace(ownManifest, "repository: https://github.com/datatug/chinookdb", "repository: https://example.com/x", 1))}
	other := "https://github.com/other/other"
	r := Check(m, Options{Profile: manifest.Publisher, Repository: &other})
	if slices.Contains(rulesOf(r), RuleRepository) || r.OK() {
		t.Errorf("findings: %v", r.Findings)
	}
}

func TestSeveralManifestsAreJudgedInTheOrderListedUnderOneBudget(t *testing.T) {
	m := goodRepository()
	m.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [./b.yaml, ./a.yaml, ./c.yaml]\nextra: 1\n---\n")}
	m.Nodes["a.yaml"] = Node{Kind: File, Content: []byte("- a\n")}
	m.Nodes["b.yaml"] = Node{Kind: File, Content: []byte(ownManifest)}
	delete(m.Nodes, "c.yaml")
	r := Check(m, publisher())
	var docs []string
	for _, f := range r.Findings {
		if len(docs) == 0 || docs[len(docs)-1] != f.Document {
			docs = append(docs, f.Document)
		}
	}
	if !slices.Equal(docs, []string{"OVDB.md", "a.yaml", "OVDB.md"}) || !r.Manifest.Read || r.Manifest.ID.Value != "chinook" {
		t.Errorf("documents in order %v, manifest %+v", docs, r.Manifest.ID)
	}
	if got := rulesOf(r); got[0] != "ovdbmd-keys" || got[len(got)-1] != RuleManifest {
		t.Errorf("rules %v", got)
	}
}

func TestTheFindingsOfManyManifestsAreCappedOnce(t *testing.T) {
	m := goodRepository()
	var names []string
	for i := range 30 {
		name := fmt.Sprintf("m%02d.yaml", i)
		names = append(names, "./"+name)
		m.Nodes[name] = Node{Kind: File, Content: []byte(strings.Replace(ownManifest, "format:", "k0: 1\nk1: 1\nk2: 1\nk3: 1\nformat:", 1))}
	}
	// The last three are not in the repository: their findings come after the budget is spent.
	for _, name := range names[27:] {
		delete(m.Nodes, name[2:])
	}
	m.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [" + strings.Join(names, ", ") + "]\n---\n")}
	r := Check(m, publisher())
	last := r.Findings[len(r.Findings)-1]
	if len(r.Findings) != manifest.MaxFindings+1 || last.Rule != manifest.RuleCapped || last.Document != "OVDB.md" {
		t.Fatalf("%d findings, last %v", len(r.Findings), last)
	}
	if !strings.Contains(last.Message, "more findings are not shown") || slices.Contains(rulesOf(r), RuleManifest) {
		t.Errorf("notice %q, rules %v", last.Message, rulesOf(r))
	}
	assertBounded(t, r.Findings)
	if n := strings.Count(strings.Join(rulesOf(r), " "), manifest.RuleCapped); n != 1 {
		t.Errorf("%d notices", n)
	}
}

func TestMoreThanMaxManifestsAreNotJudged(t *testing.T) {
	m := goodRepository()
	var names []string
	for i := range MaxManifests + 3 {
		name := fmt.Sprintf("m%02d.yaml", i)
		names = append(names, "./"+name)
		m.Nodes[name] = Node{Kind: File, Content: []byte(ownManifest)}
	}
	m.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish:\n  - " + strings.Join(names, "\n  - ") + "\n---\n")}
	r := Check(m, publisher())
	only(t, r, RuleManifests, "OVDB.md", 3+MaxManifests+1, fmt.Sprintf("lists %d manifests; at most %d are judged", MaxManifests+3, MaxManifests))
}

// override is a Memory whose answers a test replaces.
type override struct {
	*Memory
	entries func(dir string) ([]Entry, error)
	blob    func(path string, limit int) ([]byte, error)
}

func (o override) Entries(dir string) ([]Entry, error) {
	if o.entries != nil {
		return o.entries(dir)
	}
	return o.Memory.Entries(dir)
}

func (o override) Blob(path string, limit int) ([]byte, error) {
	if o.blob != nil {
		return o.blob(path, limit)
	}
	return o.Memory.Blob(path, limit)
}

func TestADirectoryThatCannotBeListed(t *testing.T) {
	for _, c := range []struct {
		err  error
		rule string
	}{{ErrTooLarge, RuleTreeLimit}, {ErrMalformed, RuleUnreadable}, {&ExitError{Code: 128, Stderr: "fatal"}, RuleUnreadable}} {
		m := override{Memory: goodRepository(), entries: func(string) ([]Entry, error) { return nil, c.err }}
		r := Check(m, publisher())
		only(t, r, c.rule, "OVDB.md", 0, "cannot list the files of the commit: ")
	}
	// The top directory is listed; the directory of the model files is not, and is reported once for the three files.
	m := override{Memory: goodRepository(), entries: func(dir string) ([]Entry, error) {
		if dir == "model" {
			return nil, ErrTooLarge
		}
		return goodRepository().Entries(dir)
	}}
	r := Check(m, publisher())
	if len(r.Findings) != 1 || r.Findings[0].Rule != RuleTreeLimit || r.Findings[0].Document != "ovdb.yaml" || r.Findings[0].Line != 14 {
		t.Errorf("findings: %v", r.Findings)
	}
}

func TestANameNoPathMayHave(t *testing.T) {
	for _, name := range []string{"", ".", "..", ".git", ".GIT", "a/b", `a\b`, "a\nb", "a\x00b", "a\x7fb", "a\tb"} {
		m := goodRepository()
		m.Nodes["x"] = Node{Kind: File}
		r := Check(override{Memory: m, entries: func(dir string) ([]Entry, error) {
			entries, err := m.Entries(dir)
			return append(entries, Entry{Name: name, Kind: File}), err
		}}, publisher())
		f := only(t, r, RuleTreeName, "OVDB.md", 0, "in the top of the repository")
		if len(f.Message) > manifest.MaxMessageBytes {
			t.Error(f.Message)
		}
	}
	// A name in a directory below the top is named by its path.
	m := goodRepository()
	m.Nodes["model/we\\ird"] = Node{Kind: File}
	only(t, Check(m, publisher()), RuleTreeName, "ovdb.yaml", 14, `in "model"`)
	// A name that is fine, even an odd one.
	m = goodRepository()
	m.Nodes["a b-é.txt"] = Node{Kind: File}
	if r := Check(m, publisher()); !r.OK() {
		t.Errorf("findings: %v", r.Findings)
	}
}

func TestNamesThatDifferOnlyInCase(t *testing.T) {
	m := goodRepository()
	m.Nodes["Ovdb.YAML"] = Node{Kind: File}
	only(t, Check(m, publisher()), RuleCase, "OVDB.md", 0, `"Ovdb.YAML" and "ovdb.yaml"`)
	// Only the directories of the paths that are judged are looked at.
	m = goodRepository()
	m.Nodes["docs/A"], m.Nodes["docs/a"] = Node{Kind: File}, Node{Kind: File}
	if r := Check(m, publisher()); !r.OK() {
		t.Errorf("findings: %v", r.Findings)
	}
	m.Nodes["model/A"], m.Nodes["model/a"] = Node{Kind: File}, Node{Kind: File}
	only(t, Check(m, publisher()), RuleCase, "ovdb.yaml", 14, `"A" and "a" in "model"`)
}

func TestADocumentThatCannotBeRead(t *testing.T) {
	read := errors.New("object is missing")
	for path, document := range map[string]string{"OVDB.md": "OVDB.md", "ovdb.yaml": "ovdb.yaml"} {
		m := override{Memory: goodRepository(), blob: func(p string, limit int) ([]byte, error) {
			if p == path {
				return nil, read
			}
			return goodRepository().Blob(p, limit)
		}}
		r := Check(m, publisher())
		only(t, r, RuleUnreadable, document, 0, "cannot be read at the commit: object is missing")
	}
	big := override{Memory: goodRepository(), blob: func(p string, limit int) ([]byte, error) {
		if limit != manifest.MaxDocumentBytes+1 {
			t.Errorf("limit = %d", limit)
		}
		return nil, ErrTooLarge
	}}
	only(t, Check(big, publisher()), "document-size", "OVDB.md", 0, "is more than 262144 bytes")
}

func TestADocumentOfExactlyOneByteOverTheBoundIsRefusedByTheManifestRules(t *testing.T) {
	m := goodRepository()
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(ownManifest + "# " + strings.Repeat("x", manifest.MaxDocumentBytes-len(ownManifest)-2+1) + "\n")}
	only(t, Check(m, publisher()), "document-size", "ovdb.yaml", 0, "bytes; at most 262144 are read")
}
