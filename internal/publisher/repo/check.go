package repo

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/internal/publisher/rules"
	"github.com/openvaultdb/ovdb/publisher/representation"
)

// MaxManifests is the most manifests of OVDB.md that are judged; the rest are one finding.
const MaxManifests = 32

// Options says what the check is given besides the repository.
type Options struct {
	// Repository, when set, is the repository the manifests must say they are in: each
	// publisher.repository must equal it exactly, as the Chinook checker's --repository does.
	Repository *string
	// Dependencies are explicitly provisioned immutable readers for attached
	// metadata and exact source-data proofs. No-attachment checks never open them.
	Dependencies DependencyReaders
	// Profile says whose rules judge the documents; see package manifest.
	Profile manifest.Profile
}

// The rules of the findings of this package (the documents' own rules are package manifest's).
const (
	RuleUnreadable   = "repo-unreadable"     // git cannot read the repository, or what it printed is not understood
	RuleNoCommit     = "repo-no-commit"      // an unborn branch
	RuleSubdirectory = "repo-subdirectory"   // not the top of the repository
	RuleBare         = "repo-bare"           // a bare repository
	RuleGitVersion   = "repo-git-version"    // a git older than MinGit
	RulePartial      = "repo-partial-clone"  // a partial clone that lacks an object the commit needs: the checker fetches it, this check does not
	RuleObjectGone   = "repo-object-missing" // an object of the commit is not in the repository
	RuleObjectBad    = "repo-object-corrupt" // an object of the commit is damaged
	RuleAlternates   = "repo-alternates"     // the objects are borrowed from a directory that is not there
	RuleFileSize     = "repo-file-size"      // a file a manifest names is larger than MaxFileBytes
	RuleOVDBMd       = "repo-ovdbmd"         // OVDB.md is not a tracked regular file
	RuleManifest     = "repo-manifest"       // a listed manifest is not a tracked regular file
	RuleFile         = "repo-file"           // a file a manifest names is not a tracked regular file
	RuleTreeLimit    = "repo-tree-limit"     // a directory with more entries, or a listing larger, than are read
	RuleTreeName     = "repo-tree-name"      // a name in a directory that no path of a repository may have
	RuleCase         = "repo-case-collision" // two names of a directory that differ only in case
	RuleManifests    = "repo-manifests-limit"
	RuleRepository   = "repo-repository" // publisher.repository is not the --repository given
	RuleDescriptor   = "repo-descriptor" // a database descriptor that does not go with exactly one manifest
)

// checker holds one check.
type checker struct {
	r               Reader
	j               *manifest.Judge
	res             manifest.Result
	dirs            map[string]dirResult
	seen            map[string]bool
	files           map[string]fileRead                // the result of reading each file that a manifest names, while there is room
	kept            int                                // the bytes in files
	sourceProofs    map[representation.Reference]error // raw-byte proofs, this repository check only
	original        Reader                             // original view of the same selected commit, when running legacy
	restartOriginal bool                               // an attachment appeared during legacy validation

	// The database descriptor of the repository, when OVDB.md lists one with exactly one manifest: the manifest it goes with, as judged.
	preread        map[string][]byte // the entries of OVDB.md read to tell descriptors from manifests, handed to the first read of each
	judgedFirst    bool              // c.res.Manifest is the first manifest judged
	pairedIndex    int               // the entry of OVDB.md that is the manifest a descriptor goes with, or -1
	pairedManifest manifest.Manifest
	pairedPath     string
	pairedClean    bool // the manifest was judged without findings, as the Directory needs before it reads the descriptor
}

// fileRead is what reading a file gave.
type fileRead struct {
	data []byte
	err  error
}

type dirResult struct {
	entries []Entry
	err     error
}

// Check judges the repository the Reader reads, at its HEAD commit as committed: OVDB.md
// is a tracked regular file; every manifest it lists is one, and is judged by
// manifest.Judge.Manifest, and every file such a manifest names (model.modelspec,
// model.hcl and meaning.file of an own-form manifest) is one; and publisher.repository
// is Options.Repository when that is set. Legacy manifests read their existing own
// model/meaning files. An attached manifest additionally requires structural
// metadata closure, manifest associations and exact format3 source-data byte proofs.
// All external reads use explicitly provided immutable dependencies.
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
	var discoveryFindings []manifest.Finding
	var originalForLegacy Reader
	if original, changed := OriginalObjects(r); changed {
		state, id, findings := discoverAttachment(original, o.Profile)
		if state != attachmentAbsent {
			r = original
			if state == attachmentIndeterminate {
				discoveryFindings = findings
			}
		} else {
			// Discovery pinned original before any mode decision. The legacy
			// view must inspect and validate that same ID, even if HEAD moves.
			r = AtCommit(r, id)
			if legacy, _, _ := discoverAttachment(r, o.Profile); legacy == attachmentPresent {
				r = original
			} else {
				originalForLegacy = original
			}
		}
	}
	c := &checker{r: r, original: originalForLegacy, j: j, res: manifest.Result{Profile: o.Profile}, dirs: map[string]dirResult{}, seen: map[string]bool{}, files: map[string]fileRead{}}
	c.run(o)
	if c.restartOriginal {
		// Discard every legacy manifest, finding and cache. Attached proofs
		// and their manifest associations come only from the original view.
		j, _ = manifest.NewJudge(o.Profile)
		c = &checker{r: originalForLegacy, j: j, res: manifest.Result{Profile: o.Profile}, dirs: map[string]dirResult{}, seen: map[string]bool{}, files: map[string]fileRead{}}
		c.run(o)
	}
	// A retry cannot erase the refusal that prevented confirmed absence. Reuse
	// the normal finding budget, and avoid repeating a persistent failure.
	for _, finding := range discoveryFindings {
		if !slices.Contains(c.res.Findings, finding) {
			c.add(finding.Document, finding.Rule, finding.Line, "%s", finding.Message)
		}
	}
	c.res.Findings = append(c.res.Findings, j.Notice("OVDB.md")...)
	return c.res
}

type attachmentState uint8

const (
	attachmentAbsent attachmentState = iota // every listed original manifest was valid and unattached
	attachmentPresent
	attachmentIndeterminate
)

// Discovery reads the original commit too: replacements must not hide a required
// attachment proof. Only OVDB.md and at most MaxManifests bounded manifests are
// read, never model/source/native data. Failure is never confirmed absence.
func discoverAttachment(r Reader, profile manifest.Profile) (attachmentState, string, []manifest.Finding) {
	j, _ := manifest.NewJudge(profile)
	c := &checker{r: r, j: j, dirs: map[string]dirResult{}, seen: map[string]bool{}, files: map[string]fileRead{}}
	head, err := r.Head()
	if err != nil {
		c.add("repository", ruleOf(err), 0, "cannot be read: %s", ascii(err.Error()))
		return attachmentIndeterminate, "", c.res.Findings
	}
	if !c.require("OVDB.md", RuleOVDBMd, 0, "OVDB.md", "OVDB.md") {
		return attachmentIndeterminate, head, c.res.Findings
	}
	data, ok := c.read("OVDB.md", "OVDB.md")
	if !ok {
		return attachmentIndeterminate, head, c.res.Findings
	}
	md, findings := j.OVDBMd(data)
	if len(findings) != 0 {
		return attachmentIndeterminate, head, findings
	}
	if len(md.Entries) > MaxManifests {
		c.add("OVDB.md", RuleManifests, md.EntryLines[MaxManifests], "OVDB.md lists %d manifests; at most %d are judged", len(md.Entries), MaxManifests)
		return attachmentIndeterminate, head, c.res.Findings
	}
	for i, path := range md.Entries {
		if !c.require("OVDB.md", RuleManifest, md.EntryLines[i], "publish entry "+rules.Quote("./"+path), path) {
			return attachmentIndeterminate, head, c.res.Findings
		}
		data, ok := c.read(path, path)
		if !ok {
			return attachmentIndeterminate, head, c.res.Findings
		}
		if manifest.IsDescriptor(data) {
			continue // a descriptor has no attachment, and is not judged as a manifest
		}
		attachment, err := manifest.RepresentationAttachment(data)
		if attachment != nil || err != nil {
			return attachmentPresent, head, nil
		}
		// RepresentationAttachment deliberately leaves malformed syntax to the
		// manifest judge. Only a successfully judged manifest proves absence.
		if _, findings := j.Manifest(data, path); len(findings) != 0 {
			return attachmentIndeterminate, head, findings
		}
	}
	return attachmentAbsent, head, nil
}

func (c *checker) add(document, rule string, line int, format string, args ...any) {
	c.res.Findings = append(c.res.Findings, c.j.Report(document, rule, line, format, args...)...)
}

func (c *checker) run(o Options) {
	if _, err := c.r.Head(); err != nil {
		c.add("repository", ruleOf(err), 0, "cannot be read: %s", ascii(err.Error()))
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
	descriptors := c.descriptors(md)
	c.j.DescriptorPaired(c.pairedIndex >= 0)
	for i, path := range md.Entries {
		if i == MaxManifests {
			c.add("OVDB.md", RuleManifests, md.EntryLines[i], "OVDB.md lists %d manifests; at most %d are judged", len(md.Entries), MaxManifests)
			return
		}
		if _, ok := descriptors[i]; ok {
			continue // judged with its manifest, below
		}
		c.manifest(o, i, path, md.EntryLines[i])
		if c.restartOriginal {
			return
		}
	}
	for i, doc := range descriptors {
		if c.pairedIndex >= 0 && c.pairedClean {
			_, findings := c.j.Descriptor(doc, md.Entries[i], c.pairedManifest, c.pairedPath)
			c.res.Findings = append(c.res.Findings, findings...)
		}
	}
}

// descriptors gives the entries of OVDB.md (of the first MaxManifests) that are database descriptors, with what they hold, as manifest.IsDescriptor tells them from manifests, and
// whether one goes with a manifest: a repository has no record to say which manifest a descriptor belongs to, so it lists one descriptor and one
// manifest, and anything else is the finding RuleDescriptor. It reads silently: an entry that cannot be read is judged, and its problem reported, as a
// manifest.
func (c *checker) descriptors(md manifest.OVDBMd) map[int][]byte {
	c.pairedIndex = -1
	found := map[int][]byte{}
	var manifests []int
	for i, path := range md.Entries {
		if i == MaxManifests {
			break
		}
		if kind, _, err := c.kind(path); err != nil || !kind.Regular() {
			manifests = append(manifests, i)
			continue
		}
		doc, err := c.r.Blob(path, manifest.MaxDocumentBytes+1)
		if err == nil && manifest.IsDescriptor(doc) {
			found[i] = doc
			continue
		}
		if err == nil {
			if c.preread == nil {
				c.preread = map[string][]byte{}
			}
			c.preread[path] = doc
		}
		manifests = append(manifests, i)
	}
	if len(found) == 0 {
		return found
	}
	first := -1
	for i := range found {
		if first < 0 || i < first {
			first = i
		}
	}
	switch {
	case len(manifests) == 0:
		c.add("OVDB.md", RuleDescriptor, md.EntryLines[first], "OVDB.md lists the database descriptor %s and no manifest: a descriptor goes with a manifest, so list the manifest beside it", rules.Quote("./"+md.Entries[first]))
	case len(manifests) > 1 || len(found) > 1:
		c.add("OVDB.md", RuleDescriptor, md.EntryLines[first], "OVDB.md lists %d manifests and %d database descriptors: a descriptor goes with exactly one manifest, and a repository lists one descriptor with one manifest", len(manifests), len(found))
	default:
		c.pairedIndex = manifests[0]
		return found
	}
	// Not paired: the descriptors are not judged (c.pairedIndex is -1), and what refused them is the finding above.
	return found
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
	if c.original != nil {
		if attachment, err := manifest.RepresentationAttachment(doc); attachment != nil || err != nil {
			c.restartOriginal = true
			return
		}
	}
	m, attachment, findings := c.j.ManifestWithAttachment(doc, path)
	if !c.judgedFirst {
		c.res.Manifest, c.judgedFirst = m, true
	}
	if i == c.pairedIndex {
		c.pairedManifest, c.pairedPath, c.pairedClean = m, path, len(findings) == 0
	}
	c.res.Findings = append(c.res.Findings, findings...)
	if r := m.PublisherRepository; o.Repository != nil && r.Usable() && r.Value != *o.Repository {
		c.add(path, RuleRepository, r.Line, "publisher.repository and --repository must be written the same, letter case included: the manifest has %s, --repository is %s", rules.Quote(r.Value), rules.Quote(*o.Repository))
	}
	if m.Form != manifest.FormOwn {
		c.attached(m, attachment, o.Dependencies)
		return
	}
	var own ownFiles
	for _, named := range []struct {
		label string
		fact  manifest.Fact[string]
		read  bool // the checker reads the file (the model file and the meaning file); it only looks at the kind of model.hcl
	}{{"model.modelspec", m.ModelSpec, true}, {"model.hcl", m.ModelHCL, false}, {"meaning.file", m.MeaningFile, true}} {
		subject := named.label + " " + rules.Quote(named.fact.Value)
		if !named.fact.Usable() || !c.require(path, RuleFile, named.fact.Line, subject, named.fact.Value) {
			continue
		}
		var data []byte
		var ok bool
		if named.read {
			data, ok = c.readable(path, named.fact.Line, subject, named.fact.Value)
		}
		switch named.label {
		case "model.modelspec":
			own.model, own.haveModel = data, ok
		case "model.hcl":
			own.haveHCL = true
		default:
			own.meaning, own.haveMeaning = data, ok
		}
	}
	c.content(path, m, own)
	c.attached(m, attachment, o.Dependencies)
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
	if doc, ok := c.preread[path]; ok {
		delete(c.preread, path)
		return doc, true
	}
	doc, err := c.r.Blob(path, manifest.MaxDocumentBytes+1)
	switch {
	case errors.Is(err, ErrTooLarge):
		c.add(document, "document-size", 0, "is more than %d bytes; at most %d are read", manifest.MaxDocumentBytes, manifest.MaxDocumentBytes)
	case err != nil:
		c.add(document, ruleOf(err), 0, "cannot be read at the commit: %s", ascii(err.Error()))
	}
	return doc, err == nil
}

// readable reads a file that a manifest names and that the checker reads (lines 345 and 347 refuse one that cannot be read, or is over 16 MiB; the
// bound here is MaxFileBytes), and adds the finding when it cannot be. A file is read once for the manifests that name it, as long as the
// files kept are few: they are at most maxKept bytes in all.
func (c *checker) readable(document string, line int, subject, path string) ([]byte, bool) {
	got, done := c.files[path]
	if !done {
		got.data, got.err = c.r.Blob(path, MaxFileBytes)
		if c.kept+len(got.data) <= maxKept {
			c.kept += len(got.data)
			c.files[path] = got
		}
	}
	switch {
	case errors.Is(got.err, ErrTooLarge):
		c.add(document, RuleFileSize, line, "%s must be at most %d bytes", subject, MaxFileBytes)
	case got.err != nil:
		c.add(document, ruleOf(got.err), line, "%s cannot be read at the commit: %s", subject, ascii(got.err.Error()))
	}
	return got.data, got.err == nil
}

// maxKept is the most bytes of the files named by manifests that are kept for the next manifest that names them.
const maxKept = 8 << 20

// ruleOf is the rule of a finding about an error that a Reader gave.
func ruleOf(err error) string {
	for _, known := range []struct {
		err  error
		rule string
	}{
		{ErrNoCommit, RuleNoCommit}, {ErrSubdirectory, RuleSubdirectory}, {ErrBare, RuleBare}, {ErrOldGit, RuleGitVersion},
		{ErrPartialClone, RulePartial}, {ErrObjectMissing, RuleObjectGone}, {ErrObjectCorrupt, RuleObjectBad}, {ErrAlternates, RuleAlternates},
		{ErrTooLarge, RuleTreeLimit}, {errName, RuleTreeName}, {errCase, RuleCase},
	} {
		if errors.Is(err, known.err) {
			return known.rule
		}
	}
	return RuleUnreadable
}

// tree adds the finding for a directory that cannot be listed, once.
func (c *checker) tree(document string, line int, err error) {
	if c.seen[err.Error()] {
		return
	}
	c.seen[err.Error()] = true
	reason := ascii(err.Error())
	if errors.Is(err, errName) || errors.Is(err, errCase) {
		reason = err.Error() // made here, with names quoted by rules.Quote
	}
	c.add(document, ruleOf(err), line, "cannot list the files of the commit: %s", reason)
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

// attached completes structural closure and then the separate required data
// stage. Neither establishes canonical semantic admission or runtime success.
func (c *checker) attached(m manifest.Manifest, attachment manifest.Fact[*representation.Reference], dependencies DependencyReaders) {
	if !attachment.Usable() {
		return
	}
	// Count findings through a separate helper result rather than the capped slice:
	// once the shared budget is exhausted, absence of a stored finding is not success.
	problems, doc := c.attachedMetadata(m, *attachment.Value, dependencies)
	for _, f := range problems {
		c.add(f.Document, f.Rule, f.Line, "%s", f.Message)
	}
	if len(problems) != 0 {
		return
	}
	if c.sourceProofs == nil {
		c.sourceProofs = map[representation.Reference]error{}
	}
	proof := verifySourceData(doc, dependencies, c.sourceProofs)
	for _, f := range proof.Findings {
		c.add(f.Document, f.Rule, f.Line, "%s", f.Message)
	}
}

func (c *checker) attachedMetadata(m manifest.Manifest, a representation.Reference, dependencies DependencyReaders) ([]manifest.Finding, *representation.Document) {
	j, _ := manifest.NewJudge(manifest.Publisher)
	helper := &checker{r: c.r, j: j, dirs: c.dirs, seen: map[string]bool{}, files: c.files, kept: c.kept}
	doc := helper.representation(m, a, dependencies)
	c.kept = helper.kept
	return append(helper.res.Findings, j.Notice(a.Path)...), doc
}
