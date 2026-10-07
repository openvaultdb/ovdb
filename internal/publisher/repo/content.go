package repo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// The rules of the findings about what the model file and the meaning file say (the meaning file's own are package manifest's, named meaning-*).
const (
	RuleModelJSON     = "repo-model-json"     // the model file is not JSON, or not an object
	RuleModelDepth    = "repo-model-depth"    // the model file nests deeper than maxJSONDepth
	RuleModelModule   = "repo-model-module"   // no module.name that is a module name
	RuleModelEntities = "repo-model-entities" // no entities object
	RuleModelVersion  = "repo-model-version"  // no "modelspec" version that is text
	RuleModelEntity   = "repo-model-entity"   // an entity whose name is not an identifier, or that has no properties
	RuleModelProperty = "repo-model-property" // a property whose name is not an identifier, whose type is not a type name, that has neither a type nor an entity, or that references an entity the model lacks
	RuleModelName     = "repo-model-name"     // model.name is not the module of the model file
	RuleModelAddress  = "repo-model-address"  // the module of model.address is not the model file's
	RuleRecordsets    = "repo-recordsets"     // recordsets are not the entities of the model
	RuleAddress       = "repo-address"        // meaning.graph.address or model.address does not name the repository that publisher.repository names (the Directory profile)

	RuleEntitiesLimit   = "repo-model-entities-limit" // the model file has more than MaxEntities entities
	RuleRecordsetsLimit = "repo-recordsets-limit"     // the manifest lists more than MaxRecordsets recordsets
)

// What one check may cost. The checker compares the recordsets of a manifest with the entities of its model by searching a list for each of them, so
// its time grows with the product of the two: 20,000 recordsets against a model of 330,000 entities (3.8 MiB) took it 6 seconds, and a repository
// may list 32 manifests. This check compares with sets, so its time is linear, but it holds the same two numbers, far above anything real (the
// reference model has eleven entities), so that what one check costs in time and memory is bounded by the size of the files it reads and
// by these. The entities limit is a recorded stricter kind; the recordsets limit is not (no repository that the checker accepts reaches it, because a
// manifest it accepts has the entities of its model as recordsets, and a model over MaxEntities is refused first).
const (
	MaxEntities   = 10000
	MaxRecordsets = 10000
)

// ownFiles are the files of an own-form manifest that the checker reads, as far as they could be: the model file and the meaning file, read, and
// whether model.hcl is a regular file.
type ownFiles struct {
	model, meaning         []byte
	haveModel, haveMeaning bool
	haveHCL                bool
}

// content holds what the model file and the meaning file say to what the manifest says (the checker's lines 408-468 and 535-539). It judges nothing of
// a file that was not read: its finding is already made.
func (c *checker) content(path string, m manifest.Manifest, f ownFiles) {
	if c.res.Profile == manifest.Directory {
		c.addresses(path, m)
	}
	module := ""
	var facts *manifest.ModelFacts
	if f.haveModel {
		module, facts = c.model(path, m, f.model)
	}
	if f.haveMeaning {
		wants := manifest.MeaningWants{File: m.MeaningFile.Value, GraphID: m.GraphID, Licence: m.LicenceMeaning, Module: module, Model: facts, GraphAddress: m.GraphAddress}
		if !m.ModelHCL.Present {
			wants.EntryFile = func(entry string) string { return c.entryFile(wants.File, entry) } // without model.hcl the entry is the only name of the model's source file
		}
		if f.haveHCL {
			wants.ModelHCL = m.ModelHCL.Value
		}
		// EntryFile may add a tree finding while Meaning runs; retain it before appending the meaning findings.
		findings := c.j.Meaning(f.meaning, wants)
		c.res.Findings = append(c.res.Findings, findings...)
	}
}

// entryFile says what a path is when it is not a regular file of the commit, for the models: entry of a meaning file: "is missing" (or "does not exist"),
// "is a symlink", and so on, and "" when it is one. A path that the tree cannot be read to gets its tree finding and is not judged here.
func (c *checker) entryFile(document, path string) string {
	kind, _, err := c.kind(path)
	if err != nil {
		c.tree(document, 0, err)
		return ""
	}
	if kind.Regular() {
		return ""
	}
	if kind == Missing {
		return "does not exist"
	}
	return "is " + kind.String()
}

// addresses holds meaning.graph.address and the own-form model.address to the repository that the record would name, as the Directory does: the record's
// repository is the one the manifest names (publisher.repository, directory.mjs 428) or, when the manifest names none, the one that --repository names (a hoster
// that runs the check on a repository says which it is); the graph is registered for it at the address that is derived from it (495-500), and the manifest's
// address is the registry's, and the own-form model.address names it (546). With neither there is no repository to compare with. The Publisher profile holds the
// same two facts in the manifest stage (manifest-meaning, manifest-model), with the Chinook checker's wording and, when it names a repository, publisher.repository only,
// so only the Directory profile needs it here.
func (c *checker) addresses(path string, m manifest.Manifest) {
	var own, from string
	switch {
	case m.PublisherRepository.Usable():
		own, _ = rules.CompareKey(m.PublisherRepository.Value) // a usable publisher.repository is a repository
		from = "publisher.repository"
	case m.PublisherRepository.Absent() && c.repository != nil:
		var ok bool
		if own, ok = rules.CompareKey(*c.repository); !ok {
			return
		}
		from = "--repository"
	default:
		return
	}
	if a := m.GraphAddress; a.Usable() && !manifest.GraphAddressNames(a.Value, own) {
		c.add(path, RuleAddress, a.Line, "meaning.graph.address must be meaning://%s (the host as written, the organisation and the repository in any case), the address of %s, got %s", own, from, rules.Quote(a.Value))
	}
	if a := m.ModelAddress; a.Usable() && a.Value.Repository != own {
		c.add(path, RuleAddress, a.Line, "model.address names %s, but %s is %s: a manifest with its own model files addresses its own repository", rules.Quote(a.Value.Repository), from, rules.Quote(own))
	}
}

// maxModelKept is the most that the readings of model files may hold together while a check runs.
const maxModelKept = 16 << 20

// modelRead is the result of reading a model file. A repository lists up to MaxManifests manifests that may name one file, and the file is the same for
// all of them, so it is read once.
type modelRead struct {
	spec modelSpec
	err  error
}

// model reads the model file and holds the manifest to it: model.name and the module of model.address are its module, and the recordsets are its
// entities. It returns the module, or "" when the file declares none that can be read, and what the bindings of a concept are held to when the file is a
// ModelSpec that the Directory reads (the Directory reads a concept's bindings only then).
func (c *checker) model(path string, m manifest.Manifest, data []byte) (string, *manifest.ModelFacts) {
	file := m.ModelSpec.Value
	if c.models == nil {
		c.models = map[string]modelRead{}
	}
	read, ok := c.models[file]
	if !ok {
		spec, err := readModel(data)
		read = modelRead{spec, err}
		// What a reading holds is bounded by the file (4 MiB of properties is some 17 MB of names), so what is held for the manifests that name the file
		// together is bounded too: a reading that does not fit is made again for the next manifest, one at a time.
		if c.modelKept+spec.size <= maxModelKept {
			c.modelKept += spec.size
			c.models[file] = read
		}
	}
	spec, err := read.spec, read.err
	switch {
	case errors.Is(err, errDepth):
		c.add(file, RuleModelDepth, 0, "is nested more than %d levels deep, which is more than this check reads", maxJSONDepth)
		return "", nil
	case errors.Is(err, errEntities):
		c.add(file, RuleEntitiesLimit, 0, "has an entities object of more than %d entities, which is more than this check reads (a repeated entities member is read as the last, but each of them is read)", MaxEntities)
		return "", nil
	case endsEarly(err):
		c.add(file, RuleModelJSON, lineAt(data, len(data)), "is not a ModelSpec JSON file: it is empty or ends before the JSON value does")
		return "", nil
	case err != nil:
		c.add(file, RuleModelJSON, jsonLine(data, err), "is not a ModelSpec JSON file: %s", ascii(err.Error()))
		return "", nil
	}
	// The Directory reports the version first (parseModelSpec), then the module, then the entities; the version is the first issue, if there is one.
	for _, issue := range spec.issues {
		if issue.rule == RuleModelVersion {
			c.add(file, issue.rule, 0, "%s", issue.text)
		}
	}
	if spec.module == "" {
		c.add(file, RuleModelModule, 0, "has no module.name that is a ModelSpec module name (a letter, then letters, digits and _)")
	}
	if !spec.hasEntities {
		c.add(file, RuleModelEntities, 0, "has no entities (an object of ModelSpec entities)")
	}
	for _, issue := range spec.issues {
		if issue.rule != RuleModelVersion {
			c.add(file, issue.rule, 0, "%s", issue.text)
		}
	}
	if name := m.ModelName; name.Usable() && spec.module != "" && name.Value != spec.module {
		c.add(path, RuleModelName, name.Line, "model.name is %s, but %s is module %s", rules.Quote(name.Value), rules.Quote(file), rules.Quote(spec.module))
	}
	if address := m.ModelAddress; address.Usable() && spec.module != "" && address.Value.Module != spec.module {
		c.add(path, RuleModelAddress, address.Line, "model.address names module %s, but %s is module %s: write the address of this repository with that module", rules.Quote(address.Value.Module), rules.Quote(file), rules.Quote(spec.module))
	}
	if recordsets := m.Recordsets; recordsets.Usable() && len(recordsets.Value) > MaxRecordsets {
		c.add(path, RuleRecordsetsLimit, recordsets.Line, "recordsets lists %d names, which is more than the %d this check reads", len(recordsets.Value), MaxRecordsets)
	} else if recordsets.Usable() && spec.hasEntities && (!m.RecordsetEntities.Present || m.RecordsetEntities.Valid) {
		// A recordset_entities that is itself wrong has been reported once by the manifest; judging the names against a mapping that was thrown away
		// would add findings about names that the mapping covers.
		// Each recordset is the entity that recordset_entities says, or the entity of its own name.
		mapped := make([]string, len(recordsets.Value))
		for i, r := range recordsets.Value {
			mapped[i] = r
			if entity, ok := m.RecordsetEntities.Value[r]; ok {
				mapped[i] = entity
			}
		}
		listed := make(map[string]bool, len(mapped))
		duplicate := false
		for _, e := range mapped {
			duplicate = duplicate || listed[e]
			listed[e] = true
		}
		missing := slices.DeleteFunc(slices.Clone(spec.entities), func(e string) bool { return listed[e] })
		var extra []string
		for i, e := range mapped {
			if _, ok := spec.set[e]; !ok {
				extra = append(extra, recordsets.Value[i])
			}
		}
		slices.Sort(extra)
		if len(missing) > 0 {
			c.add(path, RuleRecordsets, recordsets.Line, "recordsets lacks the ModelSpec entities of %s: %s", rules.Quote(file), names(missing))
		}
		if len(extra) > 0 {
			c.add(path, RuleRecordsets, recordsets.Line, "recordsets names things that are not ModelSpec entities of %s: %s; if they are the database's own names, map each to its entity under recordset_entities (name: Entity)", rules.Quote(file), names(extra))
		}
		if duplicate {
			c.add(path, RuleRecordsets, recordsets.Line, "recordset_entities maps more than one native recordset to the same ModelSpec entity; mappings must be one-to-one")
		}
	}
	if spec.module == "" || !spec.hasEntities || len(spec.issues) > 0 {
		return spec.module, nil
	}
	return spec.module, &manifest.ModelFacts{Entities: spec.properties}
}

// endsEarly reports whether err says that the data ended before the JSON value did: the decoder says it in three ways (EOF, unexpected EOF, and a syntax
// error whose text is "unexpected end of JSON input"), by the place it ended in, and they are one fault.
func endsEarly(err error) bool {
	var syntax *json.SyntaxError
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &syntax) && syntax.Error() == "unexpected end of JSON input"
}

// names lists the first few names, quoted, and how many more there are.
func names(list []string) string {
	shown := make([]string, 0, 5)
	for _, name := range list[:min(len(list), 5)] {
		shown = append(shown, rules.Quote(name))
	}
	out := strings.Join(shown, ", ")
	if len(list) > 5 {
		out += fmt.Sprintf(" and %d more", len(list)-5)
	}
	return out
}

// jsonLine is the line where the decoder says a syntax error is, or 0.
func jsonLine(data []byte, err error) int {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return lineAt(data, int(syntax.Offset))
	}
	return 0
}

// lineAt is the line of a byte offset of data.
func lineAt(data []byte, offset int) int {
	return 1 + bytes.Count(data[:min(max(offset, 0), len(data))], []byte("\n"))
}
