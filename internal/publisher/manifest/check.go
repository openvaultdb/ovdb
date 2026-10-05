package manifest

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
	"github.com/openvaultdb/ovdb/publisher/representation"
)

// Profile says whose rules judge the documents.
type Profile int

const (
	// Directory is the OVDB Directory's own rules for OVDB.md and a manifest.
	Directory Profile = iota
	// Publisher is what a publisher's own check holds a repository to: every rule
	// of Directory, and the rules the Chinook checker adds (see the README).
	Publisher
)

// known reports whether the profile is one this package judges by. An unknown
// profile is never judged by another's rules: it is a finding, because the next
// profile is the stricter one.
func (p Profile) known() bool { return p == Directory || p == Publisher }

// RuleProfile is the rule of the finding for an unknown profile.
const RuleProfile = "profile-unknown"

func unknownProfile(profile Profile) []Finding {
	return []Finding{{Rule: RuleProfile, Severity: SeverityError, Document: "profile", Message: fmt.Sprintf("profile %d is not one this package knows: nothing was judged", int(profile))}}
}

// MaxDocumentBytes bounds each document, OVDB.md and a manifest, before it is
// parsed. The JavaScript references read files of up to many megabytes; no
// manifest is anywhere near this.
const MaxDocumentBytes = 256 << 10

// Result is what checking a pair of documents found out.
type Result struct {
	Profile  Profile
	Findings []Finding
	OVDBMd   OVDBMd
	Manifest Manifest
}

// OK reports whether nothing is wrong.
func (r Result) OK() bool { return len(r.Findings) == 0 }

// maxListed is how many publish entries a message names.
const maxListed = 5

// Check judges OVDB.md and the manifest that the Directory's record names by its
// path, which OVDB.md must list. Its findings are at most MaxFindings in all,
// whatever the documents, then the RuleCapped notice when some were left out.
func Check(ovdbMd []byte, manifestPath string, manifest []byte, profile Profile) Result {
	if !profile.known() {
		return Result{Profile: profile, Findings: unknownProfile(profile)}
	}
	b := newBudget()
	md, findings := checkOVDBMd(ovdbMd, b, profile)
	if md.Read && md.Publish.Present && len(md.Entries) > 0 && !md.Lists(manifestPath) {
		c := newCollector("OVDB.md", b)
		c.add("ovdbmd-unlisted", md.Publish.Line, "OVDB.md does not list %s in publish (it lists %s); the publisher has not opted this manifest in: add %s to publish", rules.Quote("./"+manifestPath), listed(md.Entries), rules.Quote("./"+manifestPath))
		findings = append(findings, c.findings...)
	}
	m, more := checkManifest(manifest, manifestPath, b, profile)
	findings = append(append(findings, more...), b.notice(manifestPath)...)
	return Result{Profile: profile, Findings: findings, OVDBMd: md, Manifest: m}
}

// listed names the first few entries, and how many more there are.
func listed(paths []string) string {
	if len(paths) == 0 {
		return "nothing"
	}
	shown := make([]string, 0, maxListed)
	for _, p := range paths[:min(len(paths), maxListed)] {
		shown = append(shown, rules.Quote("./"+p))
	}
	out := strings.Join(shown, ", ")
	if len(paths) > maxListed {
		out += fmt.Sprintf(" and %d more", len(paths)-maxListed)
	}
	return out
}

// readDocument reads a document with the strict reader, after the size bound. It
// returns nil and has added a finding when the document is refused. offset is
// the number of lines before the text in its document.
func readDocument(c *collector, text []byte, offset int) *Node {
	node, err := parseYAML(text)
	if err == nil {
		return node
	}
	rule, line := "yaml", 0
	var syntax *syntaxError
	if errors.As(err, &syntax) {
		rule = syntax.Rule
		if syntax.Line > 0 {
			line = syntax.Line + offset
		}
	}
	c.add(rule, line, "is not YAML that this tool reads: %s", plain(err.Error()))
	return nil
}

var unprintable = regexp.MustCompile(`[^\x20-\x7e]`)

// plain makes a message of the reader, which may repeat the text it read, safe to
// print: printable ASCII only, and not long.
func plain(message string) string {
	message = unprintable.ReplaceAllStringFunc(message, func(r string) string {
		q := rules.Quote(r)
		return q[1 : len(q)-1]
	})
	if len(message) > 300 {
		message = message[:300] + "..."
	}
	return message
}

// tooBig adds the finding for a document over the bound and reports whether it is.
func tooBig(c *collector, text []byte) bool {
	if len(text) <= MaxDocumentBytes {
		return false
	}
	c.add("document-size", 0, "is %d bytes; at most %d are read", len(text), MaxDocumentBytes)
	return true
}

// Judge judges the documents of one call that has several: OVDB.md and each
// manifest it lists. They share one budget of MaxFindings, so the cap is the
// call's; the findings come in the order the documents are judged, and Notice
// is the last. Check judges one manifest and needs none of this.
type Judge struct {
	b       *budget
	profile Profile
}

// NewJudge returns a Judge for the profile, or, for a profile this package does
// not know, no Judge and the finding that says so.
func NewJudge(profile Profile) (*Judge, []Finding) {
	if !profile.known() {
		return nil, unknownProfile(profile)
	}
	return &Judge{b: newBudget(), profile: profile}, nil
}

// OVDBMd judges OVDB.md.
func (j *Judge) OVDBMd(doc []byte) (OVDBMd, []Finding) { return checkOVDBMd(doc, j.b, j.profile) }

// Manifest judges the manifest at path.
func (j *Judge) Manifest(doc []byte, path string) (Manifest, []Finding) {
	return checkManifest(doc, path, j.b, j.profile)
}

// ManifestWithAttachment also returns the attachment judged from the same
// parsed manifest. A written but invalid attachment is present and unusable;
// a document the strict reader refuses yields no usable attachment.
func (j *Judge) ManifestWithAttachment(doc []byte, path string) (Manifest, Fact[*representation.Reference], []Finding) {
	return checkManifestWithAttachment(doc, path, j.b, j.profile)
}

// Report is a finding of the caller's about document, made the way the findings
// of the documents are: within the budget and MaxMessageBytes. It returns no
// finding when the budget is spent; Notice then says so.
func (j *Judge) Report(document, rule string, line int, format string, args ...any) []Finding {
	c := newCollector(document, j.b)
	c.add(rule, line, format, args...)
	return c.findings
}

// Notice is the finding that says findings were left out, or nothing.
func (j *Judge) Notice(document string) []Finding { return j.b.notice(document) }
