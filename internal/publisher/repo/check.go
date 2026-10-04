package repo

import (
	"errors"
	"fmt"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// MaxManifests is the most manifests of OVDB.md that are judged; the rest are one finding.
const MaxManifests = 32

// Options says what the check is given besides the repository.
type Options struct {
	// Repository, when set, is the repository the manifests must say they are in: each
	// publisher.repository must equal it exactly, as the Chinook checker's --repository does.
	Repository *string
	// Profile says whose rules judge the documents; see package manifest.
	Profile manifest.Profile
}

// The rules of the findings of this package (the documents' own rules are package manifest's).
const (
	RuleUnreadable   = "repo-unreadable"     // git cannot read the repository, or what it printed is not understood
	RuleNoCommit     = "repo-no-commit"      // an unborn branch
	RuleSubdirectory = "repo-subdirectory"   // not the top of the repository
	RuleBare         = "repo-bare"           // a bare repository
	RuleOVDBMd       = "repo-ovdbmd"         // OVDB.md is not a tracked regular file
	RuleManifest     = "repo-manifest"       // a listed manifest is not a tracked regular file
	RuleFile         = "repo-file"           // a file a manifest names is not a tracked regular file
	RuleTreeLimit    = "repo-tree-limit"     // a directory with more entries, or a listing larger, than are read
	RuleTreeName     = "repo-tree-name"      // a name in a directory that no path of a repository may have
	RuleCase         = "repo-case-collision" // two names of a directory that differ only in case
	RuleManifests    = "repo-manifests-limit"
	RuleRepository   = "repo-repository" // publisher.repository is not the --repository given
)

// checker holds one check.
type checker struct {
	r    Reader
	j    *manifest.Judge
	res  manifest.Result
	dirs map[string]dirResult
	seen map[string]bool
}

type dirResult struct {
	entries []Entry
	err     error
}

// Check judges the repository the Reader reads, at its HEAD commit as committed: OVDB.md
// is a tracked regular file; every manifest it lists is one, and is judged by
// manifest.Judge.Manifest, and every file such a manifest names (model.modelspec,
// model.hcl and meaning.file of an own-form manifest) is one; and publisher.repository
// is Options.Repository when that is set. It reads no file but OVDB.md and the manifests.
//
// The result is the shape of manifest.Check's: the findings are at most
// manifest.MaxFindings, each message at most manifest.MaxMessageBytes, in the order
// the repository, OVDB.md, and each manifest in the order OVDB.md lists them; the
// Manifest is the first one listed that could be read. More than MaxManifests listed
// are judged only as far as the first MaxManifests.
func Check(r Reader, o Options) manifest.Result {
	j, bad := manifest.NewJudge(o.Profile)
	if j == nil {
		return manifest.Result{Profile: o.Profile, Findings: bad}
	}
	c := &checker{r: r, j: j, res: manifest.Result{Profile: o.Profile}, dirs: map[string]dirResult{}, seen: map[string]bool{}}
	c.run(o)
	c.res.Findings = append(c.res.Findings, j.Notice("OVDB.md")...)
	return c.res
}

func (c *checker) add(document, rule string, line int, format string, args ...any) {
	c.res.Findings = append(c.res.Findings, c.j.Report(document, rule, line, format, args...)...)
}

func (c *checker) run(o Options) {
	if _, err := c.r.Head(); err != nil {
		rule := RuleUnreadable
		switch {
		case errors.Is(err, ErrNoCommit):
			rule = RuleNoCommit
		case errors.Is(err, ErrSubdirectory):
			rule = RuleSubdirectory
		case errors.Is(err, ErrBare):
			rule = RuleBare
		}
		c.add("repository", rule, 0, "cannot be read: %s", ascii(err.Error()))
		return
	}
	if !c.require("OVDB.md", RuleOVDBMd, 0, "OVDB.md", "OVDB.md") {
		return
	}
	doc, ok := c.read("OVDB.md", "OVDB.md")
	if !ok {
		return
	}
	md, findings := c.j.OVDBMd(doc)
	c.res.OVDBMd = md
	c.res.Findings = append(c.res.Findings, findings...)
	for i, path := range md.Entries {
		if i == MaxManifests {
			c.add("OVDB.md", RuleManifests, md.EntryLines[i], "OVDB.md lists %d manifests; at most %d are judged", len(md.Entries), MaxManifests)
			return
		}
		c.manifest(o, i, path, md.EntryLines[i])
	}
}

// manifest judges the i-th manifest that OVDB.md lists, at path, and the files it names.
func (c *checker) manifest(o Options, i int, path string, line int) {
	if !c.require("OVDB.md", RuleManifest, line, "publish entry "+rules.Quote("./"+path), path) {
		return
	}
	doc, ok := c.read(path, path)
	if !ok {
		return
	}
	m, findings := c.j.Manifest(doc, path)
	if i == 0 {
		c.res.Manifest = m
	}
	c.res.Findings = append(c.res.Findings, findings...)
	if r := m.PublisherRepository; o.Repository != nil && r.Usable() && r.Value != *o.Repository {
		c.add(path, RuleRepository, r.Line, "publisher.repository must be %s, the repository this manifest is in", rules.Quote(*o.Repository))
	}
	if m.Form != manifest.FormOwn {
		return
	}
	for _, named := range []struct {
		label string
		fact  manifest.Fact[string]
	}{{"model.modelspec", m.ModelSpec}, {"model.hcl", m.ModelHCL}, {"meaning.file", m.MeaningFile}} {
		if named.fact.Usable() {
			c.require(path, RuleFile, named.fact.Line, named.label+" "+rules.Quote(named.fact.Value), named.fact.Value)
		}
	}
}

// require says whether path is a tracked regular file of the commit, and when it is not,
// adds the finding to document: subject must be one.
func (c *checker) require(document, rule string, line int, subject, path string) bool {
	kind, note, err := c.kind(path)
	if err != nil {
		c.tree(document, line, err)
		return false
	}
	if kind.Regular() {
		return true
	}
	if kind == Missing && note == "" && c.r.Uncommitted(path) {
		note = "it is in the working tree or the index but not committed, and only the commit is checked"
	}
	if note != "" {
		note = " (" + note + ")"
	}
	c.add(document, rule, line, "%s must be a tracked regular file, but it is %s%s", subject, kind, note)
	return false
}

// read reads a document of at most manifest.MaxDocumentBytes; a document over it is read
// as far as its size shows, and the document is judged by its size alone.
func (c *checker) read(document, path string) ([]byte, bool) {
	doc, err := c.r.Blob(path, manifest.MaxDocumentBytes+1)
	switch {
	case errors.Is(err, ErrTooLarge):
		c.add(document, "document-size", 0, "is more than %d bytes; at most %d are read", manifest.MaxDocumentBytes, manifest.MaxDocumentBytes)
	case err != nil:
		c.add(document, RuleUnreadable, 0, "cannot be read at the commit: %s", ascii(err.Error()))
	}
	return doc, err == nil
}

// tree adds the finding for a directory that cannot be listed, once.
func (c *checker) tree(document string, line int, err error) {
	if c.seen[err.Error()] {
		return
	}
	c.seen[err.Error()] = true
	rule := RuleUnreadable
	switch {
	case errors.Is(err, ErrTooLarge):
		rule = RuleTreeLimit
	case errors.Is(err, errName):
		rule = RuleTreeName
	case errors.Is(err, errCase):
		rule = RuleCase
	}
	reason := ascii(err.Error())
	if errors.Is(err, errName) || errors.Is(err, errCase) {
		reason = err.Error() // made here, with names quoted by rules.Quote
	}
	c.add(document, rule, line, "cannot list the files of the commit: %s", reason)
}

var (
	errName = errors.New("a name that no path of a repository may have")
	errCase = errors.New("names that differ only in case")
)

// entries lists a directory of the commit once, and refuses a listing with a name a
// path may not have (empty, ., .., .git, a slash, a backslash or a control character) or with two
// names that differ only in case, which are one file on a case-insensitive file system.
func (c *checker) entries(dir string) ([]Entry, error) {
	if done, ok := c.dirs[dir]; ok {
		return done.entries, done.err
	}
	entries, err := c.r.Entries(dir)
	if err == nil {
		err = judgeNames(dir, entries)
	}
	c.dirs[dir] = dirResult{entries, err}
	return entries, err
}

func judgeNames(dir string, entries []Entry) error {
	where := "the top of the repository"
	if dir != "" {
		where = rules.Quote(dir)
	}
	folded := map[string]string{}
	for _, e := range entries {
		if unsafeName(e.Name) {
			return fmt.Errorf("%w: %s in %s", errName, rules.Quote(e.Name), where)
		}
		if other, dup := folded[strings.ToLower(e.Name)]; dup {
			return fmt.Errorf("%w: %s and %s in %s", errCase, rules.Quote(other), rules.Quote(e.Name), where)
		}
		folded[strings.ToLower(e.Name)] = e.Name
	}
	return nil
}

func unsafeName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.EqualFold(name, ".git") {
		return true
	}
	return strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '/' || r == '\\' })
}

// kind says what a path is in the commit, without following a symlink or entering a
// submodule: a path below one is Missing. The note says why when that is not plain.
func (c *checker) kind(path string) (Kind, string, error) {
	dir, rest := "", path
	for {
		name, below, nested := strings.Cut(rest, "/")
		entries, err := c.entries(dir)
		if err != nil {
			return Missing, "", err
		}
		kind, note := lookup(entries, name)
		switch {
		case kind == Missing:
			return Missing, note, nil
		case !nested:
			return kind, "", nil
		}
		dir, rest = strings.TrimPrefix(dir+"/"+name, "/"), below
		if kind != Directory {
			return Missing, rules.Quote(dir) + " is " + kind.String() + ", not a directory", nil
		}
	}
}

// lookup finds name among entries, exactly; the note of a Missing one names the entry that differs in case.
func lookup(entries []Entry, name string) (Kind, string) {
	for _, e := range entries {
		if e.Name == name {
			return e.Kind, ""
		}
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name, name) {
			return Missing, "the commit has " + rules.Quote(e.Name) + ", which differs in case"
		}
	}
	return Missing, ""
}
