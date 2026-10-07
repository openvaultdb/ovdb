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
	// GraphAddress is meaning.graph.address of the manifest: the address of this graph, which a reference to a concept of this graph may spell out.
	GraphAddress Fact[string]
	Licence      Fact[string] // licences.meaning of the manifest
	// Module is the module the model file declares, and "" when it declares none that can be read. ModelHCL is model.hcl, and "" when
	// it is not a regular file of the commit: the models: entry is judged only when both are known, as the checker does.
	Module, ModelHCL string
	// Model is what the bindings of the concepts are held to: the entities of the model file and the names of their properties. It is nil when the
	// model file is not one the Directory reads (it has refused it, and reads no binding).
	Model *ModelFacts
	// EntryFile says what the models: entry names when it is not a regular file of the commit ("is missing", "is a symlink"), and "" when it is one. The
	// Directory reads the entry whether or not the manifest writes model.hcl; without model.hcl it is the only thing that names the model's source file.
	EntryFile func(path string) string
}

// ModelFacts are the entities of a model file and, for each, the names of its properties.
type ModelFacts struct {
	Entities map[string]map[string]struct{}
}

// Meaning judges the meaning file of an own-form manifest: it is YAML that the reader reads, a mapping with a concepts list, whose id and license are the
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
	// The Directory reads the concepts of the meaning file and refuses a file with no concepts list (a mapping, text, null or nothing there
	// all fail its Array.isArray); the Chinook checker never looks at them.
	if concepts := root.Field("concepts"); concepts == nil || concepts.Kind != kindSeq {
		c.add("meaning-concepts", fieldLine(root, "concepts"), "has no concepts list: a MeaningGraph file lists its concepts under concepts:, got %s", describe(concepts))
	} else {
		c.concepts(concepts, w)
	}
	for _, same := range []struct {
		key, label string
		fact       Fact[string]
	}{{"id", "meaning.graph.id", w.GraphID}, {"license", "licences.meaning", w.Licence}} {
		if got := root.Field(same.key); same.fact.Usable() && (got == nil || got.Kind != kindString || got.Text != same.fact.Value) {
			c.add("meaning-"+same.key, fieldLine(root, same.key), "%s is %s in the manifest, but the %s of this file is %s", same.label, rules.Quote(same.fact.Value), same.key, describe(got))
		}
	}
	if w.Module != "" {
		c.modelsEntry(root, w)
	}
	return c.findings
}

// modelsEntry holds the models: entry of the module to the checker's rule: spelled with letters, digits and . _ / - only, no leading
// slash, no empty segment and no trailing slash, joined to the directory of the meaning file it must stay inside the repository, it ends in .modelspec.hcl,
// and it is model.hcl; without model.hcl it must be a regular file of the commit (the Directory reads the entry whether or not model.hcl is written).
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
	} else if !strings.HasSuffix(resolved, ".modelspec.hcl") {
		c.add("meaning-models", entry.Line, "the %s model %s must be a .modelspec.hcl file (the model's source)", w.Module, rules.Quote(resolved))
	} else if w.ModelHCL != "" {
		if resolved != w.ModelHCL {
			c.add("meaning-hcl", entry.Line, "model.hcl is %s, but the models: entry for %s is %s", rules.Quote(w.ModelHCL), rules.Quote(w.Module), rules.Quote(resolved))
		}
	} else if what := w.EntryFile; what != nil {
		if found := what(resolved); found != "" {
			c.add("meaning-model-file", entry.Line, "the %s model %s %s: the models: entry must name a regular file of the repository", w.Module, rules.Quote(resolved), found)
		}
	}
}

// entrySpelling is the checker's /^[A-Za-z0-9_.\/-]+$/ without a leading slash, an empty segment or, because JavaScript's path.join keeps
// the trailing slash that Go's drops, a trailing slash.
func entrySpelling(s string) bool {
	return !strings.HasPrefix(s, "/") && !strings.HasSuffix(s, "/") && !strings.Contains(s, "//") && allChars(s, func(c byte) bool { return c == '.' || c == '/' || c == '-' || c == '_' || isAlnum(c) })
}
