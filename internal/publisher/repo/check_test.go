package repo

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/internal/publisher/rules"
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

const (
	goodModel   = `{"modelspec": "1.0-draft", "module": {"name": "chinook"}, "entities": {"Album": {"properties": {"Id": {"type": "int"}}}, "Artist": {"properties": {"Id": {"type": "int"}}}}}`
	goodMeaning = "id: chinook\nlicense: CC0-1.0\nmodels:\n  chinook: chinook.modelspec.hcl\nconcepts: []\n"
)

// goodRepository is a repository that holds everything the own manifest names.
func goodRepository() *Memory {
	m := &Memory{Nodes: map[string]Node{}, Untracked: map[string]bool{}}
	for path, text := range map[string]string{"OVDB.md": goodMD, "ovdb.yaml": ownManifest, modelPath: goodModel, hclPath: "module", meaningPth: goodMeaning} {
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
		{ErrOldGit, RuleGitVersion},
		{ErrPartialClone, RulePartial},
		{ErrObjectMissing, RuleObjectGone},
		{ErrObjectCorrupt, RuleObjectBad},
		{ErrAlternates, RuleAlternates},
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
			f := only(t, r, RuleOVDBMd, "OVDB.md", 0, "OVDB.md must be a tracked regular file, "+c.message)
			if c.name == "missing" && f.Message != "OVDB.md must be a tracked regular file, but it is missing" {
				t.Errorf("a path that is nowhere has no note: %q", f.Message)
			}
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
	m.Nodes[modelPath] = Node{Kind: Executable, Content: []byte(goodModel)}
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
			f := only(t, r, RuleRepository, "ovdb.yaml", 24, "publisher.repository and --repository must be written the same, letter case included: ")
			// Both spellings are in the message, so a publisher sees the difference at once.
			if !strings.HasSuffix(f.Message, "the manifest has "+rules.Quote(ownRepo)+", --repository is "+rules.Quote(c.value)) {
				t.Errorf("message %q", f.Message)
			}
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

func TestADocumentOrADirectoryWhoseObjectCannotBeRead(t *testing.T) {
	for _, err := range []error{ErrPartialClone, ErrObjectMissing, ErrObjectCorrupt, ErrAlternates} {
		rule := ruleOf(err)
		m := goodRepository()
		m.BrokenBlobs = map[string]error{"OVDB.md": err}
		only(t, Check(m, publisher()), rule, "OVDB.md", 0, "cannot be read at the commit: ")
		m = goodRepository()
		m.BrokenBlobs = map[string]error{"ovdb.yaml": err}
		only(t, Check(m, publisher()), rule, "ovdb.yaml", 0, "cannot be read at the commit: ")
		m = goodRepository()
		m.BrokenDirs = map[string]error{"": err}
		only(t, Check(m, publisher()), rule, "OVDB.md", 0, "cannot list the files of the commit: ")
		m = goodRepository()
		m.BrokenDirs = map[string]error{"model": err}
		r := Check(m, publisher())
		if len(r.Findings) != 1 || r.Findings[0].Rule != rule || r.Findings[0].Document != "ovdb.yaml" {
			t.Errorf("a directory of the model files: %v", r.Findings)
		}
	}
}

// Checker lines 345 and 347: a file that a manifest names, and that the checker reads, must be readable and at most 16 MiB; here at
// most MaxFileBytes. The checker only looks at the kind of model.hcl.
func TestAFileAManifestNamesMustBeReadable(t *testing.T) {
	for _, c := range []struct {
		path, label string
		line        int
		read        bool
	}{{modelPath, "model.modelspec", 14, true}, {meaningPth, "meaning.file", 17, true}, {hclPath, "model.hcl", 15, false}} {
		m := goodRepository()
		m.BrokenBlobs = map[string]error{c.path: ErrObjectCorrupt}
		r := Check(m, publisher())
		if !c.read {
			if !r.OK() {
				t.Errorf("%s is not read: %v", c.label, r.Findings)
			}
		} else {
			only(t, r, RuleObjectBad, "ovdb.yaml", c.line, fmt.Sprintf("%s %q cannot be read at the commit: ", c.label, c.path))
		}
		m = goodRepository()
		m.Nodes[c.path] = Node{Kind: File, Content: make([]byte, MaxFileBytes+1)}
		r = Check(m, publisher())
		if c.read {
			only(t, r, RuleFileSize, "ovdb.yaml", c.line, fmt.Sprintf("%s %q must be at most %d bytes", c.label, c.path, MaxFileBytes))
		} else if !r.OK() {
			t.Errorf("%s is not read: %v", c.label, r.Findings)
		}
		// Exactly the bound: the model file is read to its end; the meaning file is read, and is longer than a meaning file may be.
		switch c.path {
		case modelPath:
			m.Nodes[c.path] = Node{Kind: File, Content: padded(goodModel, MaxFileBytes)}
		case meaningPth:
			m.Nodes[c.path] = Node{Kind: File, Content: padded(goodMeaning, MaxFileBytes)}
		default:
			m.Nodes[c.path] = Node{Kind: File, Content: make([]byte, MaxFileBytes)}
		}
		r = Check(m, publisher())
		if c.path == meaningPth {
			only(t, r, "document-size", c.path, 0, "bytes; at most 262144 are read")
		} else if !r.OK() {
			t.Errorf("%s of exactly the bound: %v", c.label, r.Findings)
		}
	}
}

// The mutant that required the named files of the first manifest only survived: a second own-form manifest names a file that is not there.
func TestTheFilesOfEveryOwnFormManifestAreRequired(t *testing.T) {
	m := goodRepository()
	m.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [./ovdb.yaml, ./two.yaml]\n---\n")}
	m.Nodes["two.yaml"] = Node{Kind: File, Content: []byte(strings.Replace(ownManifest, "model/chinook.modelspec.json", "model/other.modelspec.json", 1))}
	only(t, Check(m, publisher()), RuleFile, "two.yaml", 14, `model.modelspec "model/other.modelspec.json" must be a tracked regular file, but it is missing`)
	m.Nodes["model/other.modelspec.json"] = Node{Kind: File}
	m.BrokenBlobs = map[string]error{"model/other.modelspec.json": ErrObjectMissing}
	only(t, Check(m, publisher()), RuleObjectGone, "two.yaml", 14, "cannot be read at the commit")
}

// A file that several manifests name is read once.
func TestAFileSeveralManifestsNameIsReadOnce(t *testing.T) {
	m := goodRepository()
	var names []string
	for i := range 5 {
		name := fmt.Sprintf("m%d.yaml", i)
		names = append(names, "./"+name)
		m.Nodes[name] = Node{Kind: File, Content: []byte(ownManifest)}
	}
	m.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [" + strings.Join(names, ", ") + "]\n---\n")}
	reads := map[string]int{}
	r := Check(override{Memory: m, blob: func(p string, limit int) ([]byte, error) {
		reads[p]++
		return m.Blob(p, limit)
	}}, publisher())
	if !r.OK() || reads[modelPath] != 1 || reads[meaningPth] != 1 || reads[hclPath] != 0 || reads["m3.yaml"] != 1 {
		t.Errorf("findings %v, reads %v", r.Findings, reads)
	}
	m.BrokenBlobs = map[string]error{modelPath: ErrObjectMissing}
	if r := Check(m, publisher()); len(r.Findings) != 5 {
		t.Errorf("each manifest has its finding: %v", r.Findings)
	}
}

// Names that differ only in case are found in either order, and the two are named in the order the listing has them.
func TestCaseCollisionsAreFoundInBothOrders(t *testing.T) {
	for _, names := range [][2]string{{"Ab", "aB"}, {"aB", "Ab"}} {
		r := Check(override{Memory: goodRepository(), entries: func(dir string) ([]Entry, error) {
			entries, err := goodRepository().Entries(dir)
			if dir == "" {
				entries = append(entries, Entry{names[0], File}, Entry{names[1], File})
			}
			return entries, err
		}}, publisher())
		only(t, r, RuleCase, "OVDB.md", 0, fmt.Sprintf("%q and %q", names[0], names[1]))
	}
}

// padded is text followed by a YAML or JSON-safe tail so that it is exactly n bytes: a comment line after YAML, and spaces after JSON (white space).
func padded(text string, n int) []byte {
	return append([]byte(text), bytes.Repeat([]byte(" "), n-len(text))...)
}

// lineOf is the line of the first line of text that has the needle.
func lineOf(text, needle string) int {
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	return 0
}

// withManifest is a good repository whose manifest has an edit.
func withManifest(find, replace string) (*Memory, string) {
	m := goodRepository()
	text := strings.Replace(ownManifest, find, replace, 1)
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(text)}
	return m, text
}

func TestTheModelFileMustBeAModelSpec(t *testing.T) {
	for name, c := range map[string]struct {
		model string
		rule  string
		line  int
		text  string
	}{
		"empty":                         {"", RuleModelJSON, 1, "is not a ModelSpec JSON file: it is empty or ends before the JSON value does"},
		"cut short":                     {`{"modelspec": "1.0-draft", "module": {"name": "chinook"}, "entities": {`, RuleModelJSON, 1, "is not a ModelSpec JSON file: it is empty or ends before the JSON value does"},
		"cut short in a value":          {"{\n\"a\":", RuleModelJSON, 2, "is not a ModelSpec JSON file: it is empty or ends before the JSON value does"},
		"cut short in a string":         {"{\n\"a\":\"x", RuleModelJSON, 2, "is not a ModelSpec JSON file: it is empty or ends before the JSON value does"},
		"cut short after a number":      {"{\"a\":1", RuleModelJSON, 1, "is not a ModelSpec JSON file: it is empty or ends before the JSON value does"},
		"cut short in a list":           {"{\"a\":[1,2\n", RuleModelJSON, 2, "is not a ModelSpec JSON file: it is empty or ends before the JSON value does"},
		"not JSON":                      {"{\n  \"module\": x\n}", RuleModelJSON, 2, "is not a ModelSpec JSON file: invalid character 'x'"},
		"a byte order mark":             {"\xef\xbb\xbf" + goodModel, RuleModelJSON, 1, "is not a ModelSpec JSON file"},
		"a list":                        {`[]`, RuleModelJSON, 0, "is not a ModelSpec JSON file: it must be a JSON object"},
		"text after the value":          {goodModel + "\n{}", RuleModelJSON, 0, "text after the JSON value"},
		"nested too deep":               {`{"x":` + strings.Repeat("[", 100) + strings.Repeat("]", 100) + "}", RuleModelDepth, 0, "is nested more than 100 levels deep"},
		"too many entities":             {manyEntities(MaxEntities + 1), RuleEntitiesLimit, 0, "has an entities object of more than 10000 entities, which is more than this check reads"},
		"no module":                     {`{"modelspec": "1.0-draft", "entities": {"Album": {"properties": {"Id": {"type": "int"}}}, "Artist": {"properties": {"Id": {"type": "int"}}}}}`, RuleModelModule, 0, "has no module.name that is a ModelSpec module name"},
		"a module name that is not one": {`{"modelspec": "1.0-draft", "module": {"name": "_x"}, "entities": {"Album": {"properties": {"Id": {"type": "int"}}}, "Artist": {"properties": {"Id": {"type": "int"}}}}}`, RuleModelModule, 0, "has no module.name"},
		"no entities":                   {`{"modelspec": "1.0-draft", "module": {"name": "chinook"}}`, RuleModelEntities, 0, "has no entities (an object of ModelSpec entities)"},
		"entities a list":               {`{"modelspec": "1.0-draft", "module": {"name": "chinook"}, "entities": []}`, RuleModelEntities, 0, "has no entities"},
		// The Directory's parseModelSpec (modelspec.mjs 92-118): what the model file says beyond its module and the names of its entities.
		"no version":                                {strings.Replace(goodModel, `"modelspec": "1.0-draft", `, "", 1), RuleModelVersion, 0, `has no "modelspec" version`},
		"a version that is a number":                {strings.Replace(goodModel, `"1.0-draft"`, "1", 1), RuleModelVersion, 0, `has no "modelspec" version`},
		"an entity with no properties":              {modelWith(`"Artist": {"properties": {}}`), RuleModelEntity, 0, "entity Artist has no properties"},
		"an entity that is null":                    {modelWith(`"Artist": null`), RuleModelEntity, 0, "entity Artist has no properties"},
		"an entity that is a list":                  {modelWith(`"Artist": []`), RuleModelEntity, 0, "entity Artist has no properties"},
		"properties that are a list":                {modelWith(`"Artist": {"properties": []}`), RuleModelEntity, 0, "entity Artist has no properties"},
		"properties repeated, the last is empty":    {modelWith(`"Artist": {"properties": {"Id": {"type": "int"}}, "properties": {}}`), RuleModelEntity, 0, "entity Artist has no properties"},
		"a property name that is not an identifier": {modelWith(`"Artist": {"properties": {"Na-me": {"type": "int"}}}`), RuleModelProperty, 0, `property name "Artist.Na-me" must be an identifier`},
		"a type that is not a type name":            {modelWith(`"Artist": {"properties": {"Name": {"type": "not a type"}}}`), RuleModelProperty, 0, `Artist.Name has type "not a type", which is not a type name`},
		"a list of lists":                           {modelWith(`"Artist": {"properties": {"Name": {"type": "int[][]"}}}`), RuleModelProperty, 0, `has type "int[][]"`},
		"a property with no type and no entity":     {modelWith(`"Artist": {"properties": {"Name": {}}}`), RuleModelProperty, 0, "Artist.Name has neither a type nor an entity"},
		"a property that is a number":               {modelWith(`"Artist": {"properties": {"Name": 5}}`), RuleModelProperty, 0, "Artist.Name has neither a type nor an entity"},
		"a property that is a list":                 {modelWith(`"Artist": {"properties": {"Name": [{"type": "int"}]}}`), RuleModelProperty, 0, "Artist.Name has neither a type nor an entity"},
		"a type that is a number":                   {modelWith(`"Artist": {"properties": {"Name": {"type": 5}}}`), RuleModelProperty, 0, "Artist.Name has neither a type nor an entity"},
		"a reference to an entity the model lacks":  {modelWith(`"Artist": {"properties": {"Name": {"entity": "Nope"}}}`), RuleModelProperty, 0, "Artist.Name references entity Nope, which the model does not have"},
		"a reference that is a number":              {modelWith(`"Artist": {"properties": {"Name": {"entity": 5}}}`), RuleModelProperty, 0, "Artist.Name has neither a type nor an entity"},
	} {
		m := goodRepository()
		m.Nodes[modelPath] = Node{Kind: File, Content: []byte(c.model)}
		f := only(t, Check(m, publisher()), c.rule, modelPath, c.line, c.text)
		_ = f
		if c.rule == RuleModelJSON && strings.Contains(c.text, "invalid character") && c.line != 2 {
			t.Errorf("%s: line %d", name, c.line)
		}
	}
	// An entity whose name is not an identifier is refused as the Directory refuses it, and the recordsets, which list Artist, no longer match the entities.
	m0 := goodRepository()
	m0.Nodes[modelPath] = Node{Kind: File, Content: []byte(strings.Replace(goodModel, `"Artist"`, `"Art-ist"`, 1))}
	if r := Check(m0, publisher()); !slices.Equal(rulesOf(r), []string{RuleModelEntity, RuleRecordsets, RuleRecordsets}) || !strings.Contains(r.Findings[0].Message, `entity name "Art-ist" must be an identifier`) {
		t.Errorf("findings %v", r.Findings)
	}
	// A model with neither: two findings, in the order of the checker's lines.
	m := goodRepository()
	m.Nodes[modelPath] = Node{Kind: File, Content: []byte(`{}`)}
	if r := Check(m, publisher()); !slices.Equal(rulesOf(r), []string{RuleModelVersion, RuleModelModule, RuleModelEntities}) {
		t.Errorf("findings %v", r.Findings)
	}
}

func TestTheManifestMustAgreeWithTheModelFile(t *testing.T) {
	// model.name and the module of model.address agree with each other (a rule of the manifest) and not with the file: both are findings.
	m, text := withManifest("  address: modelspec://github.com/datatug/chinookdb/chinook", "  name: Other\n  address: modelspec://github.com/datatug/chinookdb/Other")
	r := Check(m, publisher())
	if len(r.Findings) != 2 || r.Findings[0].Rule != RuleModelName || r.Findings[1].Rule != RuleModelAddress || r.Findings[0].Line != lineOf(text, "name: Other") ||
		!strings.Contains(r.Findings[0].Message, `model.name is "Other", but "model/chinook.modelspec.json" is module "chinook"`) {
		t.Errorf("findings %v", r.Findings)
	}
	// Written in another case than the module of the file, both are wrong: a name is compared as it is spelled.
	m, _ = withManifest("  address: modelspec://github.com/datatug/chinookdb/chinook", "  name: Chinook\n  address: modelspec://github.com/datatug/chinookdb/Chinook")
	r = Check(m, publisher())
	if len(r.Findings) != 2 || r.Findings[0].Rule != RuleModelName || r.Findings[1].Rule != RuleModelAddress || !strings.Contains(r.Findings[0].Message, `model.name is "Chinook"`) || !strings.Contains(r.Findings[1].Message, `module "Chinook"`) {
		t.Errorf("findings %v", r.Findings)
	}
	m, _ = withManifest("  address: modelspec", "  name: chinook\n  address: modelspec")
	if r := Check(m, publisher()); !r.OK() {
		t.Errorf("model.name that is the module: %v", r.Findings)
	}
	m, text = withManifest("datatug/chinookdb/chinook\n", "datatug/chinookdb/Other\n")
	only(t, Check(m, publisher()), RuleModelAddress, "ovdb.yaml", lineOf(text, "modelspec://"), `model.address names module "Other", but "model/chinook.modelspec.json" is module "chinook"`)
	// A model.name or model.address that the manifest rules refuse is not compared (their finding is made), and no module is nothing to compare with.
	m, _ = withManifest("  address: modelspec", "  name: 5x\n  address: modelspec")
	if r := Check(m, publisher()); len(r.Findings) != 1 || r.Findings[0].Rule != "manifest-model" {
		t.Errorf("findings %v", r.Findings)
	}
	m, _ = withManifest("  address: modelspec://github.com/datatug/chinookdb/chinook", "  name: Other\n  address: modelspec://github.com/datatug/chinookdb/Other")
	m.Nodes[modelPath] = Node{Kind: File, Content: []byte(`{"modelspec": "1.0-draft", "entities": {"Album": {"properties": {"Id": {"type": "int"}}}, "Artist": {"properties": {"Id": {"type": "int"}}}}}`)}
	only(t, Check(m, publisher()), RuleModelModule, modelPath, 0, "has no module.name")
}

// A native recordset name is the entity that recordset_entities says it is (the Directory's rule, 1c7e126): the entities are all there once, through their
// own names or through the mapping, and a name that maps nowhere, or two that map to one entity, are what the Directory refuses.
func TestRecordsetsAreTheEntitiesThroughTheMapping(t *testing.T) {
	mapped := "  - Album\n  - dbo.Artist\nrecordset_entities:\n  dbo.Artist: Artist\n"
	m, _ := withManifest("  - Album\n  - Artist\n", mapped)
	if r := Check(m, publisher()); !r.OK() {
		t.Errorf("a mapped native name: %v", r.Findings)
	}
	if r := Check(m, Options{Profile: manifest.Directory}); !r.OK() {
		t.Errorf("a mapped native name, the Directory profile: %v", r.Findings)
	}
	m, _ = withManifest("  - Album\n  - Artist\n", "  - Album\n  - dbo.Artist\n")
	if r := Check(m, publisher()); len(r.Findings) != 2 {
		t.Errorf("a native name nothing maps: %v", r.Findings)
	}
	m, text := withManifest("  - Album\n  - Artist\n", "  - Album\n  - dbo.Artist\n  - Artist\nrecordset_entities:\n  dbo.Artist: Artist\n")
	only(t, Check(m, publisher()), RuleRecordsets, "ovdb.yaml", lineOf(text, "- Album"), "recordset_entities maps more than one native recordset to the same ModelSpec entity")
	m, _ = withManifest("  - Album\n  - Artist\n", "  - Album\n  - dbo.Artist\nrecordset_entities:\n  dbo.Artist: Nothing\n")
	if r := Check(m, publisher()); len(r.Findings) != 2 || !strings.Contains(r.Findings[0].Message, `lacks the ModelSpec entities of "model/chinook.modelspec.json": "Artist"`) || !strings.Contains(r.Findings[1].Message, `are not ModelSpec entities of "model/chinook.modelspec.json": "dbo.Artist"`) {
		t.Errorf("a name mapped to an entity that is not there: %v", r.Findings)
	}
	// A mapping that the manifest rules refuse is reported once, by the manifest: the names are not judged against a mapping that was thrown away.
	m, _ = withManifest("  - Album\n  - Artist\n", "  - Album\n  - dbo.Artist\nrecordset_entities:\n  - Artist\n")
	if r := Check(m, publisher()); len(r.Findings) != 1 || r.Findings[0].Rule != "manifest-recordsets" {
		t.Errorf("findings %v", r.Findings)
	}
	// A name that maps nowhere is told where to map it.
	m, _ = withManifest("  - Album\n  - Artist\n", "  - Album\n  - Order Details\n")
	if r := Check(m, publisher()); len(r.Findings) != 2 || !strings.Contains(r.Findings[1].Message, "map each to its entity under recordset_entities") {
		t.Errorf("findings %v", r.Findings)
	}
}

func TestRecordsetsAreTheEntitiesOfTheModelFile(t *testing.T) {
	m, text := withManifest("  - Artist\n", "")
	only(t, Check(m, publisher()), RuleRecordsets, "ovdb.yaml", lineOf(text, "- Album"), `recordsets lacks the ModelSpec entities of "model/chinook.modelspec.json": "Artist"`)
	m, text = withManifest("  - Artist\n", "  - Artist\n  - Extra\n")
	only(t, Check(m, publisher()), RuleRecordsets, "ovdb.yaml", lineOf(text, "- Album"), `recordsets names things that are not ModelSpec entities of "model/chinook.modelspec.json": "Extra"`)
	var many strings.Builder
	for i := range 7 {
		fmt.Fprintf(&many, "  - More%d\n", i)
	}
	m, text = withManifest("  - Artist\n", "  - Artist\n"+many.String())
	if f := only(t, Check(m, publisher()), RuleRecordsets, "ovdb.yaml", lineOf(text, "- Album"), `"More0", "More1", "More2", "More3", "More4" and 2 more`); len(f.Message) > manifest400 {
		t.Error(f.Message)
	}
	m.Nodes[modelPath] = Node{Kind: File, Content: []byte(`{"modelspec": "1.0-draft", "module": {"name": "chinook"}, "entities": {"Album": {"properties": {"Id": {"type": "int"}}}, "Artist": {"properties": {"Id": {"type": "int"}}}, "__proto__": {"properties": {"Id": {"type": "int"}}}}}`)}
	if r := Check(m, publisher()); len(r.Findings) != 2 || r.Findings[0].Rule != RuleRecordsets || r.Findings[1].Rule != RuleRecordsets {
		t.Errorf("findings %v", r.Findings)
	}
	// A name is compared as it is spelled, in both directions: album for Album is a recordset that lacks Album and names a thing that is not an entity.
	m, _ = withManifest("  - Album\n", "  - album\n")
	if r := Check(m, publisher()); len(r.Findings) != 2 || !strings.Contains(r.Findings[0].Message, `lacks the ModelSpec entities of "model/chinook.modelspec.json": "Album"`) || !strings.Contains(r.Findings[1].Message, `names things that are not ModelSpec entities of "model/chinook.modelspec.json": "album"`) {
		t.Errorf("another case in the recordsets: %v", r.Findings)
	}
	m, _ = withManifest("  - Artist\n", "  - Artist\n  - album\n")
	if r := Check(m, publisher()); len(r.Findings) != 1 || !strings.Contains(r.Findings[0].Message, `names things that are not ModelSpec entities of "model/chinook.modelspec.json": "album"`) {
		t.Errorf("another case, one name too many: %v", r.Findings)
	}
	m, _ = withManifest("  - Artist\n", "  - Zed\n  - Artist\n  - Extra\n")
	if f := Check(m, publisher()).Findings; len(f) != 1 || !strings.Contains(f[0].Message, `: "Extra", "Zed"; if they are`) {
		t.Errorf("the names that are not entities are listed in order: %v", f)
	}
	// Both ways at once, and the recordsets of a manifest that the rules refuse are not compared.
	m, _ = withManifest("  - Artist\n", "  - Extra\n")
	if r := Check(m, publisher()); len(r.Findings) != 2 || !strings.Contains(r.Findings[0].Message, "lacks") || !strings.Contains(r.Findings[1].Message, "names things") {
		t.Errorf("findings %v", r.Findings)
	}
	m, _ = withManifest("  - Artist\n", "  - a/b\n")
	if r := Check(m, publisher()); len(r.Findings) != 1 || r.Findings[0].Rule != "manifest-recordsets" {
		t.Errorf("findings %v", r.Findings)
	}
}

const manifest400 = 400

func TestTheMeaningFileMustAgreeWithTheManifest(t *testing.T) {
	m := goodRepository()
	m.Nodes[meaningPth] = Node{Kind: File, Content: []byte("id: other\nlicense: CC0-1.0\nmodels:\n  chinook: chinook.modelspec.hcl\nconcepts: []\n")}
	only(t, Check(m, publisher()), "meaning-id", meaningPth, 1, `meaning.graph.id is "chinook" in the manifest, but the id of this file is "other"`)
	m.Nodes[meaningPth] = Node{Kind: File, Content: []byte("- a\n")}
	only(t, Check(m, publisher()), "meaning-shape", meaningPth, 1, "is not a MeaningGraph file")
	m.Nodes[meaningPth] = Node{Kind: File}
	only(t, Check(m, publisher()), "meaning-shape", meaningPth, 1, "is not a MeaningGraph file")
	m.Nodes[meaningPth] = Node{Kind: File, Content: []byte("id: chinook\nlicense: CC0-1.0\nmodels:\n  chinook: other.modelspec.hcl\nconcepts: []\n")}
	only(t, Check(m, publisher()), "meaning-hcl", meaningPth, 4, `model.hcl is "model/chinook.modelspec.hcl", but the models: entry for "chinook" is "model/other.modelspec.hcl"`)
	// The models: entry is judged when the module is known and model.hcl is a file: with no readable model there is no module, and with
	// no model.hcl the manifest rules and the kind finding speak.
	noModules := "id: chinook\nlicense: CC0-1.0\nconcepts: []\n"
	m.Nodes[meaningPth] = Node{Kind: File, Content: []byte(noModules)}
	m.Nodes[modelPath] = Node{Kind: File, Content: []byte("{")}
	only(t, Check(m, publisher()), RuleModelJSON, modelPath, 1, "is not a ModelSpec JSON file")
	m = goodRepository()
	m.Nodes[meaningPth] = Node{Kind: File, Content: []byte(noModules)}
	delete(m.Nodes, hclPath)
	only(t, Check(m, publisher()), RuleFile, "ovdb.yaml", 15, "model.hcl")
	// The model file is not read when it cannot be, and the meaning file still is.
	m = goodRepository()
	m.BrokenBlobs = map[string]error{modelPath: ErrObjectMissing}
	m.Nodes[meaningPth] = Node{Kind: File, Content: []byte("id: nope\nlicense: CC0-1.0\nconcepts: []\n")}
	r := Check(m, publisher())
	if !slices.Equal(rulesOf(r), []string{RuleObjectGone, "meaning-id"}) {
		t.Errorf("findings %v", r.Findings)
	}
}

// Recordsets and entities are held to MaxRecordsets and MaxEntities, and compared in linear time: the review's worst case, 20,000 recordsets against a
// model of 330,000 entities (3.8 MiB), took the checker 6.3 seconds, and 32 such manifests about four minutes.
func TestWhatOneCheckCostsIsBounded(t *testing.T) {
	list := func(n int) string {
		var b strings.Builder
		b.WriteString("  - Album\n")
		for i := 1; i < n; i++ {
			b.WriteString("  - e" + strconv.FormatInt(int64(i), 36) + "\n")
		}
		return b.String()
	}
	m, _ := withManifest("  - Album\n  - Artist\n", list(MaxEntities+1))
	m.Nodes[modelPath] = Node{Kind: File, Content: []byte(manifestWithEntities(MaxEntities + 1))}
	only(t, Check(m, publisher()), RuleEntitiesLimit, modelPath, 0, "has an entities object of more than 10000 entities")
	m2, text := withManifest("  - Album\n  - Artist\n", list(MaxRecordsets+1))
	m2.Nodes[modelPath] = Node{Kind: File, Content: []byte(manifestWithEntities(MaxEntities))}
	only(t, Check(m2, publisher()), RuleRecordsetsLimit, "ovdb.yaml", lineOf(text, "- Album"), "recordsets lists 10001 names, which is more than the 10000 this check reads")
	// An entities object that a later one replaces is read too, and the message says which kind of thing it found.
	sup, _ := withManifest("  - Album\n  - Artist\n", "  - Album\n  - Artist\n")
	sup.Nodes[modelPath] = Node{Kind: File, Content: []byte(`{"modelspec":"1.0-draft","module":{"name":"chinook"},"entities":` + manyEntitiesObject(MaxEntities+1) + `,"entities":{"Album":{"properties":{"Id":{"type":"int"}}},"Artist":{"properties":{"Id":{"type":"int"}}}}}`)}
	only(t, Check(sup, publisher()), RuleEntitiesLimit, modelPath, 0, "a repeated entities member is read as the last, but each of them is read")
	// The reviewer's worst case is refused at once, and the most that is accepted takes a moment for 32 manifests.
	start := time.Now()
	worst, _ := withManifest("  - Album\n  - Artist\n", list(20000))
	worst.Nodes[modelPath] = Node{Kind: File, Content: []byte(manifestWithEntities(330000))}
	if r := Check(worst, publisher()); len(r.Findings) == 0 || r.Findings[0].Rule != RuleEntitiesLimit {
		t.Errorf("findings %v", r.Findings)
	}
	full, _ := withManifest("  - Album\n  - Artist\n", list(MaxRecordsets))
	full.Nodes[modelPath] = Node{Kind: File, Content: []byte(manifestWithEntities(MaxEntities))}
	var paths []string
	for i := range 32 {
		p := fmt.Sprintf("m/%02d.yaml", i)
		full.Nodes[p] = full.Nodes["ovdb.yaml"]
		paths = append(paths, "./"+p)
	}
	full.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [" + strings.Join(paths, ", ") + "]\n---\n")}
	Check(full, publisher())
	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("the worst case and 32 manifests of %d recordsets took %v, want under 20s (linear time: well under one)", MaxRecordsets, took)
	}
}

// manifestWithEntities is a model whose entities are Album and the ones listed by list in TestWhatOneCheckCostsIsBounded: e1, e2 ... in base 36.
func manifestWithEntities(n int) string {
	var b strings.Builder
	b.WriteString(`{"modelspec":"1.0-draft","module":{"name":"chinook"},"entities":{"Album":{"properties":{"Id":{"type":"int"}}}`)
	entity := `{"properties":{"id":{"type":"int"}}}`
	if n > MaxEntities {
		entity = `{}` // refused for their number before they are looked at, and 330000 of the other kind are more than MaxFileBytes
	}
	for i := 1; i < n; i++ {
		b.WriteString(`,"e` + strconv.FormatInt(int64(i), 36) + `":` + entity)
	}
	b.WriteString("}}")
	return b.String()
}

// finished records nothing for a test that skipped or failed, and the real-git check holds a test to that.
func TestFinishedRecordsOnlyATestThatRanToItsEnd(t *testing.T) {
	t.Run("skipped", func(t *testing.T) {
		t.Cleanup(func() { finished(t) }) // runs after the skip below
		t.Skip("skipped on purpose")
	})
	realGitFinishedMu.Lock()
	defer realGitFinishedMu.Unlock()
	if realGitFinished[t.Name()+"/skipped"] {
		t.Error("a skipped test was recorded as finished")
	}
}

// manyEntitiesObject is the JSON object of n entities e0, e1 ... in base 36.
// modelWith is a model file that has the entities Album and Artist of goodModel and one more, written as the argument says (a member of entities, by
// its text), so that a case of the rules of parseModelSpec has one thing wrong and the recordsets of the manifest are not what it is about: the manifest
// of goodRepository lists Album and Artist, so the added entity is listed by the recordsets of the case's own manifest where it matters.
func modelWith(entity string) string {
	return `{"modelspec": "1.0-draft", "module": {"name": "chinook"}, "entities": {"Album": {"properties": {"Id": {"type": "int"}}}, "Artist": {"properties": {"Id": {"type": "int"}}}, ` + entity + `}}`
}

func manyEntitiesObject(n int) string {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"e` + strconv.FormatInt(int64(i), 36) + `":{"properties":{"id":{"type":"int"}}}`)
	}
	b.WriteString("}")
	return b.String()
}

// The reason of a failure is cut at 200 bytes by ascii, and the advice is last: no error of the reader is longer, so none loses its advice.
func TestNoErrorOfTheReaderIsCutByAscii(t *testing.T) {
	for _, err := range []error{ErrNoCommit, ErrBare, ErrSubdirectory, ErrPartialClone, ErrObjectMissing, ErrObjectCorrupt, ErrAlternates, ErrOldGit, ErrMalformed, ErrCannotRun} {
		if got := ascii(err.Error()); got != err.Error() {
			t.Errorf("ascii changes %q to %q", err, got)
		}
	}
}

// A quote is printable ASCII and stays a quote; a backslash is shown as one.
func TestAsciiLeavesQuotesRaw(t *testing.T) {
	if got := ascii(`exec: "git": not found`); got != `exec: "git": not found` {
		t.Errorf("quotes: %q", got)
	}
	if got := ascii(`a\b"c`); got != `a\\b"c` {
		t.Errorf("backslash: %q", got)
	}
	if got := ascii("tab\there\u00e9"); got != `tab\there\u00e9` {
		t.Errorf("escapes: %q", got)
	}
}

// The expensive arrangement of a hostile repository: MaxManifests manifests, each naming its own model file of the most that one may be (MaxFileBytes), each
// refused for about 340,000 properties that have neither a type nor an entity. What a reading of such a file keeps is held for every manifest that names the
// file; before it was bounded it was about 68 MB a file (a measured 833 MB at 12 files, some 2.2 GB at 32), though a check shows 101 findings at most. The
// peak heap above its start is held to modelCheckPeak (measured: 195 MiB, and 1553 MiB with the issues of a file not bounded), and the time to hostileModelsTime.
func TestManyDistinctHostileModelFilesCostABoundedAmount(t *testing.T) {
	const files = MaxManifests
	m := goodRepository()
	var paths []string
	for i := range files {
		var b strings.Builder
		fmt.Fprintf(&b, `{"modelspec":"1.0-draft","module":{"name":"chinook"},"entities":{"Album":{"properties":{"Id":{"type":"int"}}},"Artist":{"properties":{"f%d":0`, i)
		for p := 0; b.Len() < MaxFileBytes-64; p++ {
			b.WriteString(`,"p` + strconv.Itoa(p) + `":0`)
		}
		b.WriteString("}}}}")
		model := fmt.Sprintf("model/%02d.modelspec.json", i)
		m.Nodes[model] = Node{Kind: File, Content: []byte(b.String())}
		manifestPath := fmt.Sprintf("m/%02d.yaml", i)
		m.Nodes[manifestPath] = Node{Kind: File, Content: []byte(strings.Replace(ownManifest, "model/chinook.modelspec.json", model, 1))}
		paths = append(paths, "./"+manifestPath)
	}
	m.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [" + strings.Join(paths, ", ") + "]\n---\n")}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak atomic.Uint64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		var s runtime.MemStats
		for {
			runtime.ReadMemStats(&s)
			if s.HeapAlloc > peak.Load() {
				peak.Store(s.HeapAlloc)
			}
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	start := time.Now()
	r := Check(m, publisher())
	took := time.Since(start)
	close(stop)
	<-done

	if r.OK() || len(r.Findings) == 0 || r.Findings[0].Rule != RuleModelProperty {
		t.Fatalf("a hostile model file must be refused: %v", rulesOf(r))
	}
	assertBounded(t, r.Findings)
	if took > hostileModelsTime {
		t.Errorf("%d manifests naming %d distinct model files of %d bytes took %v, want under %v", files, files, MaxFileBytes, took, hostileModelsTime)
	}
	grew := int64(peak.Load()) - int64(before.HeapAlloc)
	t.Logf("%d distinct hostile model files: %v, the heap grew by %d MiB at its peak", files, took, grew>>20)
	if grew > modelCheckPeak {
		t.Errorf("the heap grew by %d MiB while %d distinct hostile model files were read, want at most %d MiB", grew>>20, files, modelCheckPeak>>20)
	}
}

// modelCheckPeak is the most that the heap may grow by while the check of hostile model files runs: the files the check holds (maxKept), the reading of one file
// at a time (about 70 MB for a file of the most it may be) and what a check keeps of each, which is bounded; not the sum of the readings.
const modelCheckPeak = 400 << 20

// hostileModelsTime is the bound on the time of the same check: 128 MiB of JSON is read in all, each file once (what they have in common is not shared, they
// differ), so it is the cost of the reading, which is 17 s under -race on a loaded laptop and a few seconds without; the bound is a tripwire for a change
// that makes it grow, not an estimate.
const hostileModelsTime = 90 * time.Second
