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
	RuleModelName     = "repo-model-name"     // model.name is not the module of the model file
	RuleModelAddress  = "repo-model-address"  // the module of model.address is not the model file's
	RuleRecordsets    = "repo-recordsets"     // recordsets are not the entities of the model
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
	module := ""
	if f.haveModel {
		module = c.model(path, m, f.model)
	}
	if f.haveMeaning {
		wants := manifest.MeaningWants{File: m.MeaningFile.Value, GraphID: m.GraphID, Licence: m.LicenceMeaning, Module: module}
		if f.haveHCL {
			wants.ModelHCL = m.ModelHCL.Value
		}
		c.res.Findings = append(c.res.Findings, c.j.Meaning(f.meaning, wants)...)
	}
}

// model reads the model file and holds the manifest to it: model.name and the module of model.address are its module, and the recordsets are its
// entities. It returns the module, or "" when the file declares none that can be read.
func (c *checker) model(path string, m manifest.Manifest, data []byte) string {
	file := m.ModelSpec.Value
	spec, err := readModel(data)
	switch {
	case errors.Is(err, errDepth):
		c.add(file, RuleModelDepth, 0, "is nested more than %d levels deep, which is more than this check reads", maxJSONDepth)
		return ""
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		c.add(file, RuleModelJSON, lineAt(data, len(data)), "is not a ModelSpec JSON file: it is empty or ends before the JSON value does")
		return ""
	case err != nil:
		c.add(file, RuleModelJSON, jsonLine(data, err), "is not a ModelSpec JSON file: %s", ascii(err.Error()))
		return ""
	}
	if spec.module == "" {
		c.add(file, RuleModelModule, 0, "has no module.name that is a ModelSpec module name (a letter, then letters, digits and _)")
	}
	if !spec.hasEntities {
		c.add(file, RuleModelEntities, 0, "has no entities (an object of ModelSpec entities)")
	}
	if name := m.ModelName; name.Usable() && spec.module != "" && name.Value != spec.module {
		c.add(path, RuleModelName, name.Line, "model.name is %s, but %s is module %s", rules.Quote(name.Value), rules.Quote(file), rules.Quote(spec.module))
	}
	if address := m.ModelAddress; address.Usable() && spec.module != "" && address.Value.Module != spec.module {
		c.add(path, RuleModelAddress, address.Line, "model.address names module %s, but %s is module %s: write the address of this repository with that module", rules.Quote(address.Value.Module), rules.Quote(file), rules.Quote(spec.module))
	}
	if recordsets := m.Recordsets; recordsets.Usable() && spec.hasEntities {
		missing := slices.DeleteFunc(slices.Clone(spec.entities), func(e string) bool { return slices.Contains(recordsets.Value, e) })
		extra := slices.DeleteFunc(slices.Clone(recordsets.Value), func(r string) bool { return slices.Contains(spec.entities, r) })
		slices.Sort(extra)
		if len(missing) > 0 {
			c.add(path, RuleRecordsets, recordsets.Line, "recordsets lacks the ModelSpec entities of %s: %s", rules.Quote(file), names(missing))
		}
		if len(extra) > 0 {
			c.add(path, RuleRecordsets, recordsets.Line, "recordsets names things that are not ModelSpec entities of %s: %s", rules.Quote(file), names(slices.Compact(extra)))
		}
	}
	return spec.module
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
