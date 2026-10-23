package repo

import (
	"slices"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// A missing declaration is a source error. An external declaration or ambiguous
// inherited origin is an unavailable field proof, never an invented empty set.
type modelDiagnostic struct {
	Code, Reference, Field string
	Origins                []string
}

const maxEffectiveFieldWork = 100000

type modelOrigin struct{ Kind, Declaration, Field string }

func (s *modelSpec) issue(rule, text string) {
	if len(s.issues) < maxModelIssues {
		s.issues = append(s.issues, modelIssue{rule, text})
	}
}

func sortedDeclarations(declarations map[string]*entityInfo) []string {
	names := make([]string, 0, len(declarations))
	for name := range declarations {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// localReference keeps the authored spelling. A self qualifier is resolved only
// against the actual source module name; unknown qualifiers remain unresolved.
func (s modelSpec) localReference(reference string) (name string, local, valid bool) {
	parts := strings.Split(reference, ".")
	if len(parts) > 2 {
		return "", false, false
	}
	for _, part := range parts {
		if !rules.ModelSourceName(part) {
			return "", false, false
		}
	}
	if len(parts) == 1 {
		return parts[0], true, true
	}
	return parts[1], s.module != "" && parts[0] == s.module, true
}

func (s *modelSpec) checkReference(reference, kind, where string, declarations map[string]*entityInfo) {
	if len(s.issues) >= maxModelIssues {
		return
	}
	name, local, valid := s.localReference(reference)
	if !valid {
		s.issue(RuleModelProperty, where+" reference "+rules.Quote(reference)+" must be a bare name or a module and name joined by one dot")
		return
	}
	if local {
		if _, exists := declarations[name]; !exists {
			s.issue(RuleModelProperty, where+" references "+kind+" "+rules.ModelLabel(reference)+", which the model does not have")
		}
	}
}

// All source declarations are checked before any dataset traversal, including
// unused components and references hidden by an own-field override.
func (s *modelSpec) judgeSource() {
	seen := map[string]string{}
	groups := []struct {
		kind         string
		declarations map[string]*entityInfo
	}{{s.spelling.record, s.records}, {"component", s.components}, {"enum", s.enums}}
	if s.componentVocabulary {
		s.issue(RuleModelVocabulary, "component members are always fields, not properties, in either ModelSpec vocabulary")
	}
	for _, group := range groups {
		for _, name := range sortedDeclarations(group.declarations) {
			info := group.declarations[name]
			s.size += len(name) + 96
			if prior, exists := seen[name]; exists {
				s.issue(RuleModelEntity, "concept "+rules.Quote(name)+" is declared as both "+prior+" and "+group.kind)
			}
			seen[name] = group.kind
			if info.badName {
				s.issue(RuleModelEntity, group.kind+" name "+rules.Quote(name)+" must be nonblank, contain no dot and not be a reserved concept word")
			}
			if info.badObject {
				s.issue(RuleModelEntity, group.kind+" "+rules.Quote(name)+" must be an object")
				continue
			}
			if info.noProps {
				s.issue(RuleModelEntity, group.kind+" "+rules.Quote(name)+" "+s.spelling.members+" must be an object")
				continue
			}
			if group.kind == "enum" {
				continue
			}
			if group.kind == s.spelling.record {
				if info.badUse {
					s.issue(RuleModelEntity, s.spelling.record+" "+rules.Quote(name)+" use must be a list of strings")
				}
				for _, ref := range info.use {
					s.checkReference(ref, "component", s.spelling.record+" "+rules.ModelLabel(name)+" use", s.components)
					s.size += len(ref) + 16
				}
			}
			for field := range info.names {
				s.size += len(field) + 48
			}
			for _, field := range sortedProperties(info.props) {
				p := info.props[field]
				s.size += len(field) + len(p.text) + len(p.enum) + 96
				if len(s.issues) >= maxModelIssues {
					continue
				}
				at := rules.ModelLabel(name) + "." + rules.ModelLabel(field)
				switch p.kind {
				case propBadName:
					s.issue(RuleModelProperty, s.spelling.member+" name "+rules.Quote(at)+" must be nonblank")
				case propBadType:
					s.issue(RuleModelProperty, at+" has type "+rules.Quote(p.text)+", which is not a type name (a word, optionally a list: string, int, datetime[])")
				case propNeither:
					s.issue(RuleModelProperty, at+" has neither a type nor "+s.spelling.aRef+" nor a component")
				case propShape:
					s.issue(RuleModelProperty, at+" must have exactly one of type, "+s.spelling.ref+" and component")
				case propReference:
					s.checkReference(p.text, s.spelling.record, at, s.records)
				case propComponent:
					s.checkReference(p.text, "component", at, s.components)
				}
				if p.badEnum {
					s.issue(RuleModelProperty, at+" enum must be a string reference or inline values list")
				}
				if p.enumReference {
					s.checkReference(p.enum, "enum", at, s.enums)
				}
			}
		}
	}
	for _, issue := range s.issues {
		s.size += len(issue.text) + 32
	}
}

func sortedProperties(properties map[string]propInfo) []string {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Compose only top-level record uses. Nested component values remain one field;
// repeated aliases of one source component never create a second origin.
func (s *modelSpec) composeFields() {
	s.properties = make(map[string]map[string]struct{}, len(s.records))
	s.inherited = map[string]map[string]string{}
	s.incomplete = map[string][]modelDiagnostic{}
	fieldWork := 0
	for _, record := range s.entities {
		info := s.records[record]
		fields := info.names
		if len(info.use) == 0 {
			s.properties[record] = fields
			continue
		}
		fields = make(map[string]struct{}, len(info.names))
		for name := range info.names {
			fields[name] = struct{}{}
		}
		origins := map[string]map[string]bool{}
		used := map[string]bool{}
	uses:
		for _, reference := range info.use {
			name, local, valid := s.localReference(reference)
			if !valid {
				continue
			} // already a source error
			if !local {
				s.incomplete[record] = append(s.incomplete[record], modelDiagnostic{Code: "field-set-unavailable", Reference: reference})
				continue
			}
			component, exists := s.components[name]
			if !exists || component == nil {
				continue
			} // source error
			if used[name] {
				continue
			}
			used[name] = true
			for field := range component.names {
				fieldWork++
				if fieldWork > maxEffectiveFieldWork {
					s.incomplete[record] = append(s.incomplete[record], modelDiagnostic{Code: "field-view-limit"})
					break uses
				}
				if _, own := info.names[field]; own {
					continue
				}
				if origins[field] == nil {
					origins[field] = map[string]bool{}
				}
				origins[field][name] = true
			}
		}
		for _, field := range sortedOriginFields(origins) {
			by := origins[field]
			sources := make([]string, 0, len(by))
			for origin := range by {
				sources = append(sources, origin)
			}
			slices.Sort(sources)
			if len(sources) > 1 {
				s.incomplete[record] = append(s.incomplete[record], modelDiagnostic{Code: "ambiguous-inherited-field", Field: field, Origins: sources})
				continue
			}
			fields[field] = struct{}{}
			if s.inherited[record] == nil {
				s.inherited[record] = map[string]string{}
			}
			s.inherited[record][field] = sources[0]
			s.size += len(field) + len(sources[0]) + 64
		}
		s.properties[record] = fields
		for _, diagnostic := range s.incomplete[record] {
			s.size += len(diagnostic.Reference) + len(diagnostic.Field) + len(diagnostic.Origins)*32 + 96
		}
	}
}

func sortedOriginFields(origins map[string]map[string]bool) []string {
	names := make([]string, 0, len(origins))
	for name := range origins {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (s modelSpec) fieldOrigin(record, field string) (modelOrigin, bool) {
	if source := s.records[record]; source != nil {
		if _, own := source.names[field]; own {
			return modelOrigin{"record", record, field}, true
		}
	}
	if component, exists := s.inherited[record][field]; exists {
		return modelOrigin{"component", component, field}, true
	}
	return modelOrigin{}, false
}

func (s modelSpec) fieldCapability(record string) string {
	var reasons []string
	for _, d := range s.incomplete[record] {
		if d.Reference != "" {
			reasons = append(reasons, d.Code+": "+rules.Quote(d.Reference))
		} else if d.Field != "" {
			reasons = append(reasons, d.Code+": "+rules.Quote(d.Field)+" from "+names(d.Origins))
		} else {
			reasons = append(reasons, d.Code)
		}
	}
	return strings.Join(reasons, "; ")
}

// Reference closure walks declaration identities once. It preserves unresolved
// references without inventing foreign targets and terminates component cycles.
func (s modelSpec) recordReferenceClosure(record string) (targets, unavailable []string) {
	known, missing := map[string]bool{}, map[string]bool{}
	type pending struct{ kind, name string }
	queue := []pending{{"record", record}}
	seen := map[pending]bool{}
	for len(queue) > 0 {
		last := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[last] {
			continue
		}
		seen[last] = true
		declarations := s.components
		if last.kind == "record" {
			declarations = s.records
		}
		declaration := declarations[last.name]
		if declaration == nil {
			continue
		}
		references := declaration.props
		if last.kind == "record" {
			for _, d := range s.incomplete[last.name] {
				if d.Reference != "" {
					missing[d.Reference] = true
				} else {
					reason := d.Code + ": " + rules.ModelLabel(last.name)
					if d.Field != "" {
						reason += "." + rules.ModelLabel(d.Field)
					}
					missing[reason] = true
				}
			}
			references = map[string]propInfo{}
			for field := range s.properties[last.name] {
				origin, _ := s.fieldOrigin(last.name, field)
				source := s.records[origin.Declaration]
				if origin.Kind == "component" {
					source = s.components[origin.Declaration]
				}
				if p, ok := source.props[field]; ok {
					references[field] = p
				}
			}
		}
		for _, p := range references {
			if p.kind != propReference && p.kind != propComponent {
				continue
			}
			name, local, valid := s.localReference(p.text)
			if !valid {
				continue
			}
			if !local {
				missing[p.text] = true
				continue
			}
			if p.kind == propReference {
				known[name] = true
			} else {
				queue = append(queue, pending{"component", name})
			}
		}
	}
	for name := range known {
		targets = append(targets, name)
	}
	for ref := range missing {
		unavailable = append(unavailable, ref)
	}
	slices.Sort(targets)
	slices.Sort(unavailable)
	return
}
