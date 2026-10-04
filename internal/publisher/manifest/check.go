package manifest

import (
	"errors"
	"regexp"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// Profile says whose rules judge the documents.
type Profile int

const (
	// Directory is the OVDB Directory's own rules for OVDB.md and a manifest.
	Directory Profile = iota
)

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

// Check judges OVDB.md and the manifest that the Directory's record names by its
// path, which OVDB.md must list.
func Check(ovdbMd []byte, manifestPath string, manifest []byte, profile Profile) Result {
	md, findings := CheckOVDBMd(ovdbMd, profile)
	if md.Valid && !md.Lists(manifestPath) {
		c := &collector{document: "OVDB.md"}
		c.add("ovdbmd-unlisted", md.PublishLine, "OVDB.md does not list %s in publish (it lists %s); the publisher has not opted this manifest in: add %s to publish", rules.Quote("./"+manifestPath), listed(md.Publish), rules.Quote("./"+manifestPath))
		findings = append(findings, c.result()...)
	}
	m, more := CheckManifest(manifest, manifestPath, profile)
	return Result{Profile: profile, Findings: append(findings, more...), OVDBMd: md, Manifest: m}
}

func listed(paths []string) string {
	if len(paths) == 0 {
		return "nothing"
	}
	shown := make([]string, len(paths))
	for i, p := range paths {
		shown[i] = rules.Quote("./" + p)
	}
	return strings.Join(shown, ", ")
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
