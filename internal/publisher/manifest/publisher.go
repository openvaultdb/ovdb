package manifest

import (
	"slices"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// The Publisher profile is every rule of Directory (it runs first) and what the
// Chinook checker (scripts/lib/ovdb-manifest.mjs) adds, as far as the two documents
// say it: the README lists each rule and the line of the checker that makes it, and
// which of its other refusals need files and are left to the caller. A fact that a
// rule here refuses is demoted: present, not usable.

// licenceIDs are the licence ids a manifest may use.
var licenceIDs = []string{
	"0BSD", "AGPL-3.0-only", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "CC-BY-4.0", "CC-BY-SA-4.0", "CC0-1.0",
	"GPL-2.0-only", "GPL-3.0-only", "ISC", "LGPL-3.0-only", "MIT", "MPL-2.0", "ODC-By-1.0", "ODbL-1.0", "PDDL-1.0", "Unlicense",
}

// discoveryPath is where the discovery document is, on the origin of the canonical url.
const discoveryPath = "/.well-known/openvaultdb"

// allowedKeys are the keys each mapping of a manifest may have; anything else is
// refused, so that a stray secret cannot ride along.
var allowedKeys = []struct {
	label string
	path  []string
	keys  []string
}{
	{"the manifest", nil, []string{"format", "id", "title", "description", "url", "deployment", "model", "meaning", "publisher", "licences", "recordsets", "recordsets_partial", "recordset_entities", "homepage", "representation_contract"}},
	{"deployment", []string{"deployment"}, []string{"url", "engine", "discovery", "recordset_page"}},
	{"model", []string{"model"}, []string{"modelspec", "hcl", "address", "name"}},
	{"meaning", []string{"meaning"}, []string{"file", "graph", "address"}},
	{"meaning.graph", []string{"meaning", "graph"}, []string{"id", "address"}},
	{"publisher", []string{"publisher"}, []string{"name", "url", "repository"}},
	{"licences", []string{"licences"}, []string{"model", "meaning", "data"}},
}

// demote makes a usable fact present and not usable, with no value. It is called on usable facts only (every call is under a Usable test), so
// it does not look at whether the fact is present.
func demote[T any](f *Fact[T]) {
	f.Valid = false
	var zero T
	f.Value = zero
}

// publisher judges the manifest by the rules that the Publisher profile adds.
func (k *manifestChecker) publisher() {
	m, out, c := k.m, &k.out, k.c
	for _, set := range allowedKeys {
		node := m
		for _, key := range set.path {
			node = node.Field(key)
		}
		if node == nil || node.Kind != kindMap {
			continue
		}
		for _, key := range node.Keys {
			if !slices.Contains(set.keys, key) {
				c.add("manifest-keys", node.Fields[key].Line, "unknown key %s in %s: only %s are read, so remove it (a stray secret must not ride along)", rules.Quote(key), set.label, strings.Join(set.keys, ", "))
			}
		}
	}

	if out.ID.Usable() && !rules.IsID(out.ID.Value) {
		c.add("manifest-id", out.ID.Line, "id must be lower-case letters, digits and single hyphens, at most %d characters, got %s", rules.MaxIDLength, rules.Quote(out.ID.Value))
		demote(&out.ID)
	}

	if out.Discovery.Usable() && k.discovery.Host == k.canonical.Host && k.discovery.Path != discoveryPath {
		c.add("manifest-discovery", out.Discovery.Line, "deployment.discovery must be https://%s%s: the discovery document is always at that path", k.canonical.Host, discoveryPath)
		demote(&out.Discovery)
	}
	if out.RecordsetPage.Usable() && out.DeploymentURL.Usable() && k.page.Host != k.deployed.Host {
		c.add("manifest-url", out.RecordsetPage.Line, "deployment.recordset_page must be on the origin of deployment.url (https://%s), not https://%s", k.deployed.Host, k.page.Host)
		demote(&out.RecordsetPage)
	}

	owner := k.publisherOwner()
	own := k.ownRepository()
	k.grammar(&out.ModelAddress, "model.address", own, out.Form == FormShared)
	k.grammar(&out.MeaningAddress, "meaning.address", own, true)
	if out.ModelName.Usable() && out.ModelName.Value[0] == '_' {
		c.add("manifest-model", out.ModelName.Line, "model.name, when given, must be a ModelSpec module name: a letter, then letters, digits and _, got %s", rules.Quote(out.ModelName.Value))
		demote(&out.ModelName)
	}
	if out.ModelName.Usable() && out.ModelAddress.Usable() && out.ModelName.Value != out.ModelAddress.Value.Module {
		c.add("manifest-model", out.ModelName.Line, "model.name is %s, but model.address names module %s: they must agree", rules.Quote(out.ModelName.Value), rules.Quote(out.ModelAddress.Value.Module))
		demote(&out.ModelName)
	}
	if out.PublisherRepository.Usable() && owner != "" && !strings.HasPrefix(out.PublisherRepository.Value, "https://github.com/"+owner+"/") {
		c.add("manifest-publisher", out.PublisherRepository.Line, "publisher.repository must belong to the owner in publisher.url (%s), got %s", rules.Quote(owner), rules.Quote(out.PublisherRepository.Value))
		demote(&out.PublisherRepository)
	}

	if out.Form == FormOwn {
		k.ownForm(own)
	} else if out.GraphAddress.Usable() && out.MeaningAddress.Usable() && out.GraphAddress.Value != "meaning://"+out.MeaningAddress.Value.Repository {
		c.add("manifest-meaning", out.GraphAddress.Line, "meaning.graph.address is %s, but meaning.address names meaning://%s: leave meaning.graph.address out or make it the unpinned address", rules.Quote(out.GraphAddress.Value), out.MeaningAddress.Value.Repository)
		demote(&out.GraphAddress)
	}
	if out.GraphID.Usable() && !isRegistryID(out.GraphID.Value) {
		c.add("manifest-meaning", out.GraphID.Line, "meaning.graph.id must be a MeaningGraph registry id: lower-case letters, digits and single hyphens, got %s", rules.Quote(out.GraphID.Value))
		demote(&out.GraphID)
	}
	for _, l := range []struct {
		label string
		fact  *Fact[string]
	}{{"licences.model", &out.LicenceModel}, {"licences.meaning", &out.LicenceMeaning}, {"licences.data", &out.LicenceData}} {
		if l.fact.Usable() && !slices.Contains(licenceIDs, l.fact.Value) && (l.label != "licences.data" || !dataConjunction(l.fact.Value)) {
			c.add("manifest-licence", l.fact.Line, "%s must be one of the known SPDX licence ids (%s), got %s", l.label, strings.Join(licenceIDs, ", "), rules.Quote(l.fact.Value))
			demote(l.fact)
		}
	}
}

// lowerASCII lower-cases A to Z and leaves every other byte as written. It is the
// whole of the case folding this package does where the Chinook checker lower-cases
// (meaning.graph.address against the repository). Go's strings.ToLower and
// JavaScript's toLowerCase disagree outside ASCII: U+0130 becomes plain i in Go and
// i with a combining dot in JavaScript, which was how Go accepted an address the
// checker refuses. The text it is compared with is ASCII (it is built from
// publisher.repository, which names github.com in A-Z a-z 0-9 . _ -), so a value that
// equals it after lowerASCII is itself ASCII, and JavaScript lower-cases an ASCII
// string to the same text: what Go accepts, the checker accepts. Where the checker
// folds a non-ASCII letter onto an ASCII one (the Kelvin sign onto k) Go refuses,
// which is a recorded kind.
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// publisherOwner is the owner of publisher.url when it is https://github.com/<owner>, else "".
func (k *manifestChecker) publisherOwner() string {
	f := &k.out.PublisherURL
	if !f.Usable() {
		return ""
	}
	owner, ok := strings.CutPrefix(f.Value, "https://github.com/")
	// allChars is false for an empty owner (the URL rule accepts https://github.com/) and for a slash (not a name character), so neither needs a check of its own.
	if ok && allChars(owner, isNameChar) {
		return owner
	}
	k.c.add("manifest-publisher", f.Line, "publisher.url must be https://github.com/<owner>, got %s", rules.Quote(f.Value))
	demote(f)
	return ""
}

// ownRepository is host/owner/repository of publisher.repository in lower case, or "".
func (k *manifestChecker) ownRepository() string {
	if !k.out.PublisherRepository.Usable() {
		return ""
	}
	key, _ := rules.CompareKey(k.out.PublisherRepository.Value)
	return key
}

// grammar holds a model or meaning address to the publisher's grammar: a module
// name of a letter and then letters, digits and _, and, in the shared form (notOwn),
// not the publisher's own repository.
func (k *manifestChecker) grammar(f *Fact[Address], label, own string, notOwn bool) {
	if !f.Usable() {
		return
	}
	switch {
	case f.Value.Module != "" && f.Value.Module[0] == '_':
		k.c.add("manifest-"+family(label), f.Line, "%s names module %s, which must be a ModelSpec module name: a letter, then letters, digits and _", label, rules.Quote(f.Value.Module))
	case notOwn && own != "" && f.Value.Repository == own:
		k.c.add("manifest-"+family(label), f.Line, "%s %s names this repository; a model or meaning file in the publisher's own repository is named by local files (model.modelspec and meaning.file), not by a pinned address", label, rules.Quote(f.Value.Text))
	default:
		return
	}
	demote(f)
}

// ownForm holds the own form to what the Chinook checker needs of the documents.
func (k *manifestChecker) ownForm(own string) {
	out, c := &k.out, k.c
	if out.ModelHCL.Absent() {
		c.add("manifest-required", where(k.m.Field("model"), "hcl"), "model.hcl is required with local model files: it is the model's source file, and must be the path in the meaning file's models: entry")
	}
	if out.ModelSpec.Usable() && !strings.HasSuffix(out.ModelSpec.Value, ".modelspec.json") {
		c.add("manifest-model", out.ModelSpec.Line, "model.modelspec %s must be a path ending in .modelspec.json", rules.Quote(out.ModelSpec.Value))
		demote(&out.ModelSpec)
	}
	if out.ModelAddress.Usable() && own != "" && out.ModelAddress.Value.Repository != own {
		c.add("manifest-model", out.ModelAddress.Line, "model.address must be modelspec://%s/<module>, this repository plus the module name, got %s", own, rules.Quote(out.ModelAddress.Value.Text))
		demote(&out.ModelAddress)
	}
	if out.GraphAddress.Usable() && own != "" && lowerASCII(out.GraphAddress.Value) != "meaning://"+own {
		c.add("manifest-meaning", out.GraphAddress.Line, "meaning.graph.address must be meaning://%s (in any case), derived from publisher.repository, got %s", own, rules.Quote(out.GraphAddress.Value))
		demote(&out.GraphAddress)
	}
}

// isRegistryID is lower-case letters and digits in groups separated by single hyphens.
func isRegistryID(s string) bool {
	for _, part := range strings.Split(s, "-") {
		if part == "" {
			return false
		}
		for j := 0; j < len(part); j++ {
			if c := part[j]; (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}
