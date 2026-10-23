package repo

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

// The parity golden: repository.json, made by testdata/reference/generate.mjs. Each case is
// a repository as a list of operations on a base repository (the Chinook repository's own
// files), and the verdict of the Chinook checker on it, run without and with its
// --repository option ('1' accepts, '0' refuses, '-' not run).
type goldenCase struct {
	Group      string
	Name       string
	Ops        [][]json.RawMessage
	Repository *string
	Verdict    string
}

type golden struct {
	Format     string
	Reference  string
	Repository string
	Base       map[string]string
	Counts     struct{ Cases, Accepted, AcceptedWithRepository int }
	Cases      []goldenCase
}

func readGolden(t testing.TB) golden {
	t.Helper()
	raw, err := os.ReadFile("testdata/reference/repository.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]string
	digests, err := os.ReadFile("testdata/reference/digests.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(digests, &want); err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); want["repo/testdata/reference/repository.json"] != hex.EncodeToString(sum[:]) {
		t.Error("repository.json does not match its digest in digests.json: it was edited by hand, or generate.mjs was not run; run `node internal/publisher/repo/testdata/reference/generate.mjs`")
	}
	var g golden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.Format != "ovdb-publisher-repository/1" || g.Counts.Cases != len(g.Cases) {
		t.Fatalf("golden: format %q, %d cases of %d", g.Format, len(g.Cases), g.Counts.Cases)
	}
	return g
}

// op decodes the arguments of an operation: strings, and integers.
func op(t testing.TB, raw []json.RawMessage) (name string, args []string) {
	t.Helper()
	for i, r := range raw {
		var s string
		if err := json.Unmarshal(r, &s); err != nil {
			var n int
			if err := json.Unmarshal(r, &n); err != nil {
				t.Fatalf("operation %s: argument %d is neither text nor a number", raw[0], i)
			}
			s = strconv.Itoa(n)
		}
		if i == 0 {
			name = s
		} else {
			args = append(args, s)
		}
	}
	return name, args
}

// model is the repository of a case as git will hold it: what the commit holds, what only the
// working tree or the index holds, and where the check is run.
type model struct {
	tracked   map[string]Node
	untracked map[string]string
	dirty     map[string]string
	breaks    [][3]string // op, target, how
	location  string
}

func (m *model) put(path string, node Node) {
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		delete(m.tracked, strings.Join(parts[:i], "/"))
	}
	for key := range m.tracked {
		if strings.HasPrefix(key, path+"/") {
			delete(m.tracked, key)
		}
	}
	m.tracked[path] = node
}

func (m *model) text(t testing.TB, path string) string {
	t.Helper()
	node, ok := m.tracked[path]
	if !ok {
		t.Fatalf("no file at %s", path)
	}
	return string(node.Content)
}

// build applies the operations of a case to the base repository, as generate.mjs does.
func build(t testing.TB, g golden, ops [][]json.RawMessage) *model {
	t.Helper()
	m := &model{tracked: map[string]Node{}, untracked: map[string]string{}, dirty: map[string]string{}, location: "normal"}
	for path, text := range g.Base {
		m.tracked[path] = Node{Kind: File, Content: []byte(text)}
	}
	for _, raw := range ops {
		name, a := op(t, raw)
		switch name {
		case "file":
			m.put(a[0], Node{Kind: File, Content: []byte(a[1])})
		case "remove":
			for key := range m.tracked {
				if key == a[0] || strings.HasPrefix(key, a[0]+"/") {
					delete(m.tracked, key)
				}
			}
		case "move":
			node := m.tracked[a[0]]
			delete(m.tracked, a[0])
			m.put(a[1], node)
		case "exec":
			node := m.tracked[a[0]]
			node.Kind = Executable
			m.tracked[a[0]] = node
		case "symlink":
			m.put(a[0], Node{Kind: Symlink, Content: []byte(a[1])})
		case "submodule":
			m.put(a[0], Node{Kind: Submodule})
		case "edit":
			before := m.text(t, a[0])
			if !strings.Contains(before, a[1]) {
				t.Fatalf("%s has no %q", a[0], a[1])
			}
			m.tracked[a[0]] = Node{Kind: File, Content: []byte(strings.Replace(before, a[1], a[2], 1))}
		case "pad":
			n, _ := strconv.Atoi(a[2])
			m.tracked[a[0]] = Node{Kind: File, Content: []byte(m.text(t, a[0]) + "\n" + a[1] + strings.Repeat("x", n) + "\n")}
		case "worktree", "staged":
			m.untracked[a[0]] = a[1]
		case "ignored":
			m.untracked[a[0]] = a[1]
			m.tracked[".gitignore"] = Node{Kind: File, Content: []byte(a[0] + "\n")}
		case "dirty":
			m.dirty[a[0]] = a[1]
		case "many":
			n, _ := strconv.Atoi(a[1])
			for i := range n {
				m.tracked[strings.TrimPrefix(fmt.Sprintf("%s/f%05d", a[0], i), "/")] = Node{Kind: File}
			}
		case "manifests":
			n, _ := strconv.Atoi(a[0])
			var paths []string
			for i := range n {
				paths = append(paths, fmt.Sprintf("./m/%02d.yaml", i))
			}
			m.tracked["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [" + strings.Join(paths, ", ") + "]\n---\n")}
			for _, p := range paths {
				m.tracked[p[2:]] = Node{Kind: File, Content: []byte(m.text(t, "ovdb.yaml"))}
			}
		case "copy":
			m.tracked[a[1]] = m.tracked[a[0]]
		case "padjson":
			n, _ := strconv.Atoi(a[1])
			var object map[string]any
			if err := json.Unmarshal([]byte(m.text(t, a[0])), &object); err != nil {
				t.Fatalf("%s: %v", a[0], err)
			}
			object["_pad"] = strings.Repeat("x", n)
			padded, _ := json.Marshal(object)
			m.tracked[a[0]] = Node{Kind: File, Content: padded}
		case "bytes":
			raw, err := base64.StdEncoding.DecodeString(a[1])
			if err != nil {
				t.Fatalf("bytes %s: %v", a[0], err)
			}
			m.put(a[0], Node{Kind: File, Content: raw})
		case "nest":
			n, _ := strconv.Atoi(a[1])
			text := regexp.MustCompile(`\}\s*$`).ReplaceAllLiteralString(m.text(t, a[0]), ",\"_deep\":"+strings.Repeat("[", n)+strings.Repeat("]", n)+"}\n")
			m.tracked[a[0]] = Node{Kind: File, Content: []byte(text)}
		case "entities": // a model file whose entities are e0, e1 ... in base 36,
			n, _ := strconv.Atoi(a[1])
			var object map[string]any
			if err := json.Unmarshal([]byte(m.text(t, a[0])), &object); err != nil {
				t.Fatalf("%s: %v", a[0], err)
			}
			entities := make(map[string]any, n)
			for i := 0; i < n; i++ {
				entities["e"+strconv.FormatInt(int64(i), 36)] = map[string]any{"properties": map[string]any{"id": map[string]any{"type": "int"}}}
			}
			object["entities"] = entities
			changed, _ := json.Marshal(object)
			m.tracked[a[0]] = Node{Kind: File, Content: changed}
		case "recordsets":
			n, _ := strconv.Atoi(a[1])
			var list strings.Builder
			list.WriteString("recordsets:\n")
			for i := 0; i < n; i++ {
				list.WriteString("  - e" + strconv.FormatInt(int64(i), 36) + "\n")
			}
			m.tracked[a[0]] = Node{Kind: File, Content: []byte(regexp.MustCompile(`recordsets:\n(  - .*\n)+`).ReplaceAllLiteralString(m.text(t, a[0]), list.String()))}
		case "break", "break-tree":
			m.breaks = append(m.breaks, [3]string{name, a[0], a[1]})
		case "state":
			m.location = a[0]
		default:
			t.Fatalf("unknown operation %q", name)
		}
	}
	return m
}

// memory is the model in memory: the Reader that the checker's repository is held to.
func (m *model) memory() *Memory {
	mem := &Memory{Nodes: m.tracked, Untracked: map[string]bool{}}
	for path := range m.untracked {
		mem.Untracked[path] = true
	}
	switch m.location {
	case "bare":
		mem.Err = ErrBare
	case "unborn":
		mem.Err = ErrNoCommit
	case "subdirectory":
		mem.Err = ErrSubdirectory
	case "not-a-repository":
		mem.Err = errors.New("fatal: not a git repository")
	case "alternates-gone":
		mem.Err = ErrAlternates
	case "partial-blob":
		mem.BrokenBlobs = map[string]error{}
		for path := range m.tracked {
			mem.BrokenBlobs[path] = ErrPartialClone
		}
	case "partial-tree":
		mem.BrokenDirs = map[string]error{"": ErrPartialClone}
	}
	for _, b := range m.breaks {
		err := ErrObjectMissing
		if b[2] != "missing" {
			err = ErrObjectCorrupt
		}
		if b[2] == "other" { // git reads the bytes of the other object, as the file's own
			mem.Nodes[b[1]] = Node{Kind: File, Content: []byte("other\n")}
			continue
		}
		if b[0] == "break" {
			if mem.BrokenBlobs == nil {
				mem.BrokenBlobs = map[string]error{}
			}
			mem.BrokenBlobs[b[1]] = err
		} else {
			if mem.BrokenDirs == nil {
				mem.BrokenDirs = map[string]error{}
			}
			mem.BrokenDirs[b[1]] = err
		}
	}
	return mem
}

// stricterKinds are the ways in which Check refuses a repository that the Chinook checker accepts, each with
// the reason; the README has the same kinds with a count for each, and a test holds the two together.
// A refusal of a repository that the checker accepts is a kind by the rule of its first finding, or the test fails.
var stricterKinds = map[string]string{
	"meaning-concepts":          "The meaning file has no concepts list (the key is missing, or is null, a mapping or text): the Directory refuses it (directory.mjs, parseMeaningFile) and the Chinook checker never reads the concepts.",
	"meaning-concept":           "A concept of the meaning file has a shape the Directory refuses (validateConcept, meaning.mjs): no text id, an id that is not lower-case words joined by single hyphens, labels that are not short plain strings, extends or values-of that is not text, bindings that are not a list of mappings with a role from the list; the checker never reads the concepts.",
	"meaning-binding":           "A binding of a concept names a model that is not this repository's, another module, an entity or a property that the model lacks, or has a role other than entity and no property: the Directory refuses it (directory.mjs 728-754); the checker never reads the concepts.",
	"meaning-chain":             "The extends chain or the values-of of a concept of the meaning file, inside its own graph, returns to a concept already in the chain, is longer than 50 concepts, or names a concept the graph does not have: the Directory refuses it (meaning.mjs 195-226); the checker never reads the concepts.",
	"meaning-concept-duplicate": "A concept id is declared twice in the meaning file: the Directory refuses it; the checker never reads the concepts.",
	RuleModelVersion:            "The model file has no \"modelspec\" version that is text: the Directory refuses it (parseModelSpec, modelspec.mjs); the checker reads only the module and the names of the entities. Or it has an identifier that is neither 1.0-draft nor 1.0-draft-2 and a key of the current vocabulary (records, fields, record): the checker and the Directory take any text as the identifier and read the earlier keys only.",
	RuleModelVocabulary:         "The model file has the identifier 1.0-draft and a key of the current vocabulary (records, fields, record), or the identifier 1.0-draft-2 and a key of the earlier (entities, properties, entity): ModelSpec's own reader refuses it; the checker and the Directory read the earlier keys only and never look for the others.",
	RuleModelRemoved:            "The model file has a top-level collections, recordsets, projections or migrations, which ModelSpec removed or reserved with no content: ModelSpec's own reader refuses it, whichever identifier the file has; the checker and the Directory never read those keys.",
	RuleModelEntity:             "An entity of the model file has a name that is not an identifier, or no properties: the Directory refuses it (parseModelSpec); the checker reads only the names of the entities.",
	RuleModelProperty:           "A property of the model file has a name that is not an identifier, a type that is not a type name, neither a type nor an entity, or references an entity the model lacks: the Directory refuses it (parseModelSpec); the checker never reads the properties.",
	RuleCase:                    "Two names in a directory on the path of a file that is judged differ only in case, so they are one file on a case-insensitive file system; the checker reads the exact name and accepts.",
	RuleTreeName:                "A directory on the path of a file that is judged has an entry whose name is empty or . or .. or .git, or has a slash, a backslash or a control character; the checker never lists a directory.",
	RuleTreeLimit:               "A directory on the path of a file that is judged has more than 50000 entries; the checker asks git about one path and has no bound.",
	RuleManifests:               "OVDB.md lists more than 32 manifests; the checker judges every one.",
	RuleEntitiesLimit:           "The model file has an entities object of more than 10000 entities (MaxEntities); the checker compares each recordset with each entity, so 20000 recordsets against 330000 entities took it 6 seconds.",
	"yaml-encoding":             "The reader refuses a file that is not UTF-8 text (a byte that is not UTF-8, a NUL character); the checker's library reads a file as UTF-8, replaces the bytes it cannot decode and goes on.",
	RuleModelDepth:              "The model file nests arrays and objects more than 100 levels deep (the top object is the first level); JSON.parse has no bound.",
	"yaml":                      "The reader accepts a subset of YAML and refuses a structure it cannot place (here a flow collection used as a key); the checker's library reads it.",
	"yaml-anchor":               "The reader refuses anchors and aliases (& and *) and merge keys (<<): it reads a document once, as written, and expanding references is how a small file becomes a large one.",
	"yaml-character":            "The reader refuses characters that YAML 1.2 does not allow in text, among them the C1 controls such as U+0085; the checker's library reads them into a string.",
	"yaml-directive":            "The reader refuses a %YAML or %TAG directive; the checker's library follows it.",
	"yaml-documents":            "The reader refuses a document end marker (`...`) and a second document; the checker's library reads the first document and ignores what follows.",
	"yaml-escape":               "The reader refuses a double-quoted escape that is not a character, such as half of a surrogate pair (\\ud83c); the checker's library accepts it.",
	"yaml-key":                  "The reader refuses a key that YAML reads as a number, a boolean or null (2024, true, null) and wants it in quotes; the checker's library accepts it as a key.",
	"yaml-limit":                "The reader refuses collections nested more than 64 levels deep (63 is read); the checker's library reads any depth.",
	"yaml-line-ending":          "The reader refuses a carriage return that is not part of CRLF; the checker's library reads it as a line break.",
	"yaml-number":               "The reader refuses numbers it cannot hold exactly or that are not finite: hexadecimal and octal numbers, .inf, .nan, and integers beyond 2^53; the checker's library reads them as numbers.",
	"yaml-tab":                  "The reader refuses a tab where YAML allows it but whose reading differs between parsers (after a colon, in indentation).",
	"yaml-tag":                  "The reader refuses tags (!, !!), which the checker's library resolves; it reads plain values only.",
	"yaml-unsupported":          "The reader refuses constructs outside its subset: explicit keys (`? key`), and a quoted value written over more than one line, which a YAML tool writes back for any long string; the checker's library reads both.",
	RulePartial:                 "A partial clone (--filter=blob:none or --filter=tree:0) that lacks an object the commit needs: the checker's git fetches the object from the remote, which this check never does (a repository that a remote can make run a command must not be asked to); the message says to check a full clone or to fetch the files first.",
	RuleFileSize:                "A file that a manifest names (the model file or the meaning file) of more than 4194304 bytes (MaxFileBytes) is refused; the checker reads files of up to 16 MiB.",
	RuleSubdirectory:            "The directory is inside a repository and not its top; the checker reads it as if it were the top, with a note, and the Directory reads OVDB.md at the top.",
	"document-size":             "OVDB.md or a manifest of more than 262144 bytes is refused before it is read; the checker reads files of up to 16 MiB.",
}

// replay runs Check on every case in memory and returns what it found: for each case the findings without and with the
// --repository option (nil with when the checker was not run with it), and the stricter kind of the case, or "".
type replayed struct {
	c        goldenCase
	plain    manifest.Result
	with     *manifest.Result
	stricter string
}

func replay(t testing.TB, g golden) []replayed {
	t.Helper()
	var out []replayed
	for _, c := range g.Cases {
		mem := build(t, g, c.Ops).memory()
		r := replayed{c: c, plain: Check(mem, Options{Profile: manifest.Publisher})}
		if c.Repository != nil {
			w := Check(mem, Options{Profile: manifest.Publisher, Repository: c.Repository})
			r.with = &w
		}
		for i, result := range []*manifest.Result{&r.plain, r.with} {
			if result == nil {
				continue
			}
			want := c.Verdict[i] == '1'
			switch {
			case result.OK() && !want:
				t.Errorf("%s: %s: Check accepts a repository that the Chinook checker refuses (run %d)", c.Group, c.Name, i)
			case !result.OK() && want && r.stricter == "":
				r.stricter = result.Findings[0].Rule
			}
		}
		out = append(out, r)
	}
	return out
}

func TestEveryCaseOfTheGoldenIsJudgedLikeTheChecker(t *testing.T) {
	g := readGolden(t)
	counts := map[string]int{}
	var agree int
	for _, r := range replay(t, g) {
		if r.stricter == "" {
			agree++
			continue
		}
		if _, ok := stricterKinds[r.stricter]; !ok {
			t.Errorf("%s: %s: Check is stricter than the checker, and %q is not a recorded kind: %s", r.c.Group, r.c.Name, r.stricter, r.plain.Findings[0].Message)
		}
		counts[r.stricter]++
	}
	for kind := range stricterKinds {
		if counts[kind] == 0 {
			t.Errorf("the stricter kind %q is recorded and no case of the golden shows it", kind)
		}
	}
	t.Logf("%d cases: %d agree, %d stricter %v", len(g.Cases), agree, len(g.Cases)-agree, counts)
}

// Every case that the checker accepts and Check refuses is one of the kinds, and every case a
// kind lists is there for a reason that its README row gives: the table of the README is the table here.
func TestReadmeCountsTheStricterKinds(t *testing.T) {
	g := readGolden(t)
	counts := map[string]int{}
	for _, r := range replay(t, g) {
		if r.stricter != "" {
			counts[r.stricter]++
		}
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(string(readme), "\n") {
		if cells := strings.Split(line, "|"); len(cells) == 5 && strings.HasPrefix(strings.TrimSpace(cells[1]), "`") && strings.Trim(strings.TrimSpace(cells[2]), "0123456789") == "" {
			rows[strings.Trim(strings.TrimSpace(cells[1]), "`")] = strings.TrimSpace(cells[2]) + "|" + strings.TrimSpace(cells[3])
		}
	}
	for kind, reason := range stricterKinds {
		want := fmt.Sprintf("%d|%s", counts[kind], reason)
		if rows[kind] != want {
			t.Errorf("README row of %q is %q, want %q", kind, rows[kind], want)
		}
	}
	for kind := range rows {
		if _, ok := stricterKinds[kind]; !ok {
			t.Errorf("README has a row for %q, which is not a recorded kind", kind)
		}
	}
}

// Each finding of a replay is within the bounds of package manifest, and an accepted case has none.
func TestFindingsOfTheGoldenAreBounded(t *testing.T) {
	for _, r := range replay(t, readGolden(t)) {
		for _, result := range []*manifest.Result{&r.plain, r.with} {
			if result == nil {
				continue
			}
			assertBounded(t, result.Findings)
		}
	}
}

func assertBounded(t testing.TB, findings []manifest.Finding) {
	t.Helper()
	if len(findings) > manifest.MaxFindings+1 {
		t.Errorf("%d findings", len(findings))
	}
	for i, f := range findings {
		if len(f.Message) > manifest.MaxMessageBytes || strings.Trim(f.Message, " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~") != "" {
			t.Errorf("message %q is not printable ASCII of at most %d bytes", f.Message, manifest.MaxMessageBytes)
		}
		if f.Rule == manifest.RuleCapped && i != len(findings)-1 {
			t.Errorf("the capped notice is at %d of %d", i, len(findings))
		}
		if f.Rule == "" || f.Severity != manifest.SeverityError || f.Document == "" || f.Line < 0 {
			t.Errorf("finding %+v is not well formed", f)
		}
	}
}
