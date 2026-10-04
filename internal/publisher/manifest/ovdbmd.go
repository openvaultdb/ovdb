package manifest

import (
	"bytes"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// OVDBMd is what OVDB.md says, for a caller that goes on to read the manifests.
type OVDBMd struct {
	// Valid is true when the front matter is read and has the shape of a list of
	// manifests: Publish is only meaningful then. It does not say that every
	// entry is a good path: see the findings.
	Valid bool
	// Publish are the manifest paths of the entries that are explicit ./ paths,
	// without the ./, in document order.
	Publish []string
	// PublishLine is the line of the publish list.
	PublishLine int
}

// Lists reports whether OVDB.md lists the manifest at path (without ./).
func (m OVDBMd) Lists(path string) bool {
	for _, p := range m.Publish {
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
	c := &collector{document: "OVDB.md"}
	var md OVDBMd
	if tooBig(c, doc) {
		return md, c.result()
	}
	text, ok := frontMatter(doc)
	if !ok {
		c.add("ovdbmd-frontmatter", 1, "has no YAML front matter between --- lines: start the file with a --- line, then ovdb: 1 and publish: [./ovdb.yaml], then a closing --- line")
		return md, c.result()
	}
	node := readDocument(c, text, 1)
	if node == nil {
		return md, c.result()
	}
	if node.Kind != kindMap {
		c.add("ovdbmd-frontmatter", node.Line+1, "front matter is not a mapping: write ovdb: 1 and publish: [./ovdb.yaml]")
		return md, c.result()
	}
	if v := node.Field("ovdb"); v == nil || v.Kind != kindNumber || v.Text != "1" {
		c.add("ovdbmd-version", fieldLine(node, "ovdb")+1, "ovdb must be the number 1, got %s: write ovdb: 1", describe(v))
	}
	list := node.Field("publish")
	if list == nil || list.Kind != kindSeq || len(list.Items) == 0 {
		c.add("ovdbmd-publish", fieldLine(node, "publish")+1, "publish must list at least one manifest path: write publish: [./ovdb.yaml]")
		return md, c.result()
	}
	md.Valid, md.PublishLine = true, list.Line+1
	for _, entry := range list.Items {
		if entry.Kind != kindString || !rules.IsPublishEntry(entry.Text) {
			c.add("ovdbmd-entry", entry.Line+1, "publish entry %s must be an explicit path starting with ./ inside the repository (letters, digits and . _ - / only; no glob, no .., no empty segment): write ./ovdb.yaml", describe(entry))
			continue
		}
		md.Publish = append(md.Publish, entry.Text[2:])
	}
	return md, c.result()
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
