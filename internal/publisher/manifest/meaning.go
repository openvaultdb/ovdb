package manifest

import (
	"path"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// MeaningWants is what the manifest and the model file say that a meaning file must agree with: the checker's lines 445-468.
type MeaningWants struct {
	File    string       // the path of the meaning file, which names it in the findings and is what models: is relative to
	GraphID Fact[string] // meaning.graph.id of the manifest
	Licence Fact[string] // licences.meaning of the manifest
	// Module is the module the model file declares, and "" when it declares none that can be read. ModelHCL is model.hcl, and "" when
	// it is not a regular file of the commit: the models: entry is judged only when both are known, as the checker does.
	Module, ModelHCL string
}

// Meaning judges the meaning file of an own-form manifest: it is YAML that the reader reads, a mapping, whose id and license are the
// manifest's meaning.graph.id and licences.meaning, and whose models: entry for the module is the path of model.hcl, relative to the
// meaning file. A fact that the manifest rules refuse is not compared: its own finding says what is wrong. Its findings share the
// budget of the Judge and are about the meaning file, with the lines of the reader.
func (j *Judge) Meaning(doc []byte, w MeaningWants) []Finding {
	c := newCollector(w.File, j.b)
	if tooBig(c, doc) {
		return c.findings
	}
	root := readDocument(c, doc, 0)
	if root == nil {
		return c.findings
	}
	if root.Kind != kindMap {
		c.add("meaning-shape", root.Line, "is not a MeaningGraph file: it must be a mapping of keys and values (id, license, models, ...), got %s", describe(root))
		return c.findings
	}
	for _, same := range []struct {
		key, label string
		fact       Fact[string]
	}{{"id", "meaning.graph.id", w.GraphID}, {"license", "licences.meaning", w.Licence}} {
		if got := root.Field(same.key); same.fact.Usable() && (got == nil || got.Kind != kindString || got.Text != same.fact.Value) {
			c.add("meaning-"+same.key, fieldLine(root, same.key), "%s is %s in the manifest, but the %s of this file is %s", same.label, rules.Quote(same.fact.Value), same.key, describe(got))
		}
	}
	if w.Module != "" && w.ModelHCL != "" {
		c.modelsEntry(root, w)
	}
	return c.findings
}

// modelsEntry holds the models: entry of the module to the checker's rule: spelled with letters, digits and . _ / - only, no leading
// slash, no empty segment and no trailing slash, joined to the directory of the meaning file it must stay inside the repository, and it is model.hcl.
func (c *collector) modelsEntry(root *Node, w MeaningWants) {
	var entry *Node
	if models := root.Field("models"); models != nil {
		entry = models.Fields[w.Module] // only a mapping has fields
	}
	// The checker wants text that is not blank; a blank entry fails the spelling below, which every entry must pass, so it is not asked for twice.
	if entry == nil || entry.Kind != kindString {
		c.add("meaning-models", fieldLine(root, "models"), "has no models: entry for module %s: write models: {%s: <the path of model.hcl, relative to this file>}", rules.Quote(w.Module), w.Module)
		return
	}
	resolved := path.Join(path.Dir(w.File), entry.Text)
	// The checker also refuses a result that is .. or starts with ../, which rules.IsRepositoryPath refuses as it does any .. segment.
	if !entrySpelling(entry.Text) || !rules.IsRepositoryPath(resolved) {
		c.add("meaning-models", entry.Line, "models must name the ModelSpec module %s with a relative path that stays inside the repository (letters, digits and . _ / - only, no leading / or trailing /, no empty segment, no .. that leaves it), got %s", rules.Quote(w.Module), rules.Quote(entry.Text))
	} else if resolved != w.ModelHCL {
		c.add("meaning-hcl", entry.Line, "model.hcl is %s, but the models: entry for %s is %s", rules.Quote(w.ModelHCL), rules.Quote(w.Module), rules.Quote(resolved))
	}
}

// entrySpelling is the checker's /^[A-Za-z0-9_.\/-]+$/ without a leading slash, an empty segment or, because JavaScript's path.join keeps
// the trailing slash that Go's drops, a trailing slash.
func entrySpelling(s string) bool {
	return s != "" && !strings.HasPrefix(s, "/") && !strings.HasSuffix(s, "/") && !strings.Contains(s, "//") && allChars(s, func(c byte) bool { return c == '.' || c == '/' || c == '-' || c == '_' || isAlnum(c) })
}
