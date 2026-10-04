package manifest

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// OVDBMd is what OVDB.md says, for a caller that goes on to read the manifests.
// Each field is a Fact (see Fact): absent, present and usable, or present and
// not usable.
type OVDBMd struct {
	// Read is true when the front matter was read as a mapping. When it is
	// false nothing was read, every fact below is the zero Fact, and a finding
	// says why: an absent fact then does not mean the key is not written.
	Read bool
	// Version is ovdb: usable when it is the number 1, and then Value is "1".
	Version Fact[string]
	// Publish is publish: usable when it is a non-empty list in which every
	// entry is an explicit ./ path inside the repository. Its Value is the
	// entries without the ./, as a set in first-seen order.
	Publish Fact[[]string]
	// Entries are the usable entries of publish, without the ./, as a set in
	// first-seen order, whether or not the others are usable: what the Directory
	// holds as the manifests that OVDB.md lists. Lists asks it.
	Entries []string
	// Repeated are the usable entries that publish writes more than once, in
	// first-seen order. The Directory profile accepts a repeated entry.
	Repeated []string
}

// Lists reports whether OVDB.md lists the manifest at path (without ./).
func (m OVDBMd) Lists(path string) bool {
	for _, p := range m.Entries {
		if p == path {
			return true
		}
	}
	return false
}

// frontMatter returns the text between the opening --- line and the first closing
// --- line of doc, and false when doc has none. It is the regular expression
// /^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/ of the references, written out: the
// text starts with ---, a line end, the shortest text that is followed by a line
// end, ---, and a line end or the end of the document.
func frontMatter(doc []byte) ([]byte, bool) {
	rest, ok := bytes.CutPrefix(doc, []byte("---"))
	if !ok {
		return nil, false
	}
	start := 0
	switch {
	case bytes.HasPrefix(rest, []byte("\r\n")):
		start = 2
	case bytes.HasPrefix(rest, []byte("\n")):
		start = 1
	default:
		return nil, false
	}
	body := rest[start:]
	for i := 0; i < len(body); i++ {
		k := i
		if body[k] == '\r' && k+1 < len(body) && body[k+1] == '\n' {
			k++
		}
		if body[k] != '\n' || !bytes.HasPrefix(body[k+1:], []byte("---")) {
			continue
		}
		end := k + 4
		if end == len(body) || body[end] == '\n' || (body[end] == '\r' && end+1 < len(body) && body[end+1] == '\n') {
			return body[:i], true
		}
	}
	return nil, false
}

// CheckOVDBMd judges OVDB.md: YAML front matter, ovdb: 1, and a publish list of
// explicit ./ paths. Whether a given manifest is listed is for Check or Lists.
func CheckOVDBMd(doc []byte, profile Profile) (OVDBMd, []Finding) {
	if !profile.known() {
		return OVDBMd{}, unknownProfile(profile)
	}
	b := newBudget()
	md, findings := checkOVDBMd(doc, b)
	return md, append(findings, b.notice("OVDB.md")...)
}

func checkOVDBMd(doc []byte, b *budget) (OVDBMd, []Finding) {
	c := newCollector("OVDB.md", b)
	var md OVDBMd
	if tooBig(c, doc) {
		return md, c.findings
	}
	text, ok := frontMatter(doc)
	if !ok {
		c.add("ovdbmd-frontmatter", 1, "has no YAML front matter between --- lines: start the file with a --- line, then ovdb: 1 and publish: [./ovdb.yaml], then a closing --- line")
		return md, c.findings
	}
	node := readDocument(c, text, 1)
	if node == nil {
		return md, c.findings
	}
	if node.Kind != kindMap {
		c.add("ovdbmd-frontmatter", node.Line+1, "front matter is not a mapping: write ovdb: 1 and publish: [./ovdb.yaml]")
		return md, c.findings
	}
	md.Read = true
	v := node.Field("ovdb")
	md.Version = found(v, v != nil && v.Kind == kindNumber && v.Text == "1", "1")
	lower(&md.Version, 1)
	if v == nil || v.Kind != kindNumber || v.Text != "1" {
		c.add("ovdbmd-version", fieldLine(node, "ovdb")+1, "ovdb must be the number 1, got %s: write ovdb: 1", describe(v))
	}
	list := node.Field("publish")
	if list == nil || list.Kind != kindSeq || len(list.Items) == 0 {
		c.add("ovdbmd-publish", fieldLine(node, "publish")+1, "publish must list at least one manifest path: write publish: [./ovdb.yaml]")
		md.Publish = found(list, false, []string(nil))
		lower(&md.Publish, 1)
		return md, c.findings
	}
	seen := map[string]bool{}
	repeated := map[string]bool{}
	allUsable := true
	for _, entry := range list.Items {
		if entry.Kind != kindString || !rules.IsPublishEntry(entry.Text) {
			allUsable = false
			c.add("ovdbmd-entry", entry.Line+1, "publish entry %s %s", describe(entry), entryProblem(entry))
			continue
		}
		path := entry.Text[2:]
		switch {
		case !seen[path]:
			seen[path] = true
			md.Entries = append(md.Entries, path)
		case !repeated[path]:
			repeated[path] = true
			md.Repeated = append(md.Repeated, path)
		}
	}
	md.Publish = found(list, allUsable, md.Entries)
	lower(&md.Publish, 1)
	return md, c.findings
}

// lower moves the line of a present fact down by the number of lines before the
// front matter's text.
func lower[T any](f *Fact[T], by int) {
	if f.Present {
		f.Line += by
	}
}

// entryProblem says what is wrong with a publish entry that is not an explicit
// ./ path: its length when that is the reason.
func entryProblem(entry *Node) string {
	if entry.Kind == kindString && strings.HasPrefix(entry.Text, "./") && len(entry.Text)-2 > rules.MaxPathLength {
		return fmt.Sprintf("is %d bytes; the path after ./ is at most %d: use a shorter path", len(entry.Text)-2, rules.MaxPathLength)
	}
	return "must be an explicit path starting with ./ inside the repository (letters, digits and . _ - / only; no glob, no .., no empty segment): write ./ovdb.yaml"
}

// fieldLine is the line of a key of a map, or the line of the map.
func fieldLine(n *Node, key string) int {
	if v := n.Field(key); v != nil {
		return v.Line
	}
	return n.Line
}

// describe says what a node holds, for a message: a short quoted piece of it.
func describe(n *Node) string {
	switch {
	case n == nil:
		return "nothing (the key is missing)"
	case n.Kind == kindNull:
		return "null"
	case n.Kind == kindString:
		return rules.Quote(n.Text)
	case n.Kind == kindNumber || n.Kind == kindBool:
		return rules.Quote(n.Text) + " (a " + kindName(n.Kind) + ", not text)"
	}
	return "a " + kindName(n.Kind)
}

func kindName(k kind) string { return kindNames[k] }

var kindNames = map[kind]string{kindNull: "null", kindString: "string", kindNumber: "number", kindBool: "boolean", kindMap: "mapping", kindSeq: "list"}
