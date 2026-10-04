package manifest

import (
	"fmt"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// ManifestFormat is the format a manifest declares.
const ManifestFormat = "ovdb-manifest/draft-1"

// Form is the way a manifest names its model.
type Form string

const (
	// FormOwn: the model and the meaning file are files of the publisher's
	// repository (model.modelspec or model.hcl is written).
	FormOwn Form = "own"
	// FormShared: the model and the meaning graph are published in other
	// repositories and named by pinned addresses.
	FormShared Form = "shared"
)

// Manifest is what a manifest says, for a caller that goes on to read the files
// it names. Every field the Directory reads is a Fact (see Fact), and the README
// lists each with where the Directory reads it. A value is as written, never
// trimmed or coerced: a number or a boolean where text is wanted is a written,
// unusable fact.
type Manifest struct {
	// Read is true when the document was read as a mapping. When it is false
	// nothing was read, every fact is the zero Fact, and a finding says why: an
	// absent fact then does not mean the key is not written.
	Read bool
	// Form is the form the manifest is written in: own when model.modelspec or
	// model.hcl is written (even blank), else shared.
	Form Form

	Format, ID, Title, Description Fact[string]
	URL, Homepage                  Fact[string] // url is the canonical one

	DeploymentURL, Engine, Discovery, RecordsetPage Fact[string]

	// The files an own manifest names, relative to the repository root;
	// MeaningFile is a file of the graph's repository in the shared form.
	ModelSpec, ModelHCL, MeaningFile Fact[string]
	// ModelName is model.name, which the Directory compares with the module.
	ModelName Fact[string]
	// ModelAddress and MeaningAddress as written and as parsed.
	ModelAddress, MeaningAddress Fact[Address]
	GraphID, GraphAddress        Fact[string]

	LicenceModel, LicenceMeaning, LicenceData Fact[string]

	PublisherName, PublisherURL, PublisherRepository Fact[string]

	Recordsets        Fact[[]string]
	RecordsetsPartial Fact[bool]
}

// isText is the references' isText: text that is not blank by JavaScript's trim().
func isText(n *Node) bool { return n != nil && n.Kind == kindString && !rules.IsBlank(n.Text) }

// where is the line of a finding about field key of m: the value's, or the map's.
func where(m *Node, key string) int {
	if m == nil {
		return 1
	}
	return fieldLine(m, key)
}

// manifestChecker holds one manifest while it is judged.
type manifestChecker struct {
	c       *collector
	m       *Node
	out     Manifest
	profile Profile
	// The URLs that the checks of a profile compare, as parsed (the zero URL when the field is not usable).
	canonical, deployed, discovery, page rules.URL
}

// CheckManifest judges a manifest. path names it in the findings.
func CheckManifest(doc []byte, path string, profile Profile) (Manifest, []Finding) {
	if !profile.known() {
		return Manifest{}, unknownProfile(profile)
	}
	b := newBudget()
	m, findings := checkManifest(doc, path, b, profile)
	return m, append(findings, b.notice(path)...)
}

func checkManifest(doc []byte, path string, b *budget, profile Profile) (Manifest, []Finding) {
	c := newCollector(path, b)
	if tooBig(c, doc) {
		return Manifest{}, c.findings
	}
	root := readDocument(c, doc, 0)
	if root == nil {
		return Manifest{}, c.findings
	}
	if root.Kind != kindMap {
		c.add("manifest-shape", root.Line, "is not a mapping: write the manifest as keys and values (format, id, title, ...)")
		return Manifest{}, c.findings
	}
	k := &manifestChecker{c: c, m: root, profile: profile}
	k.out.Read = true
	k.check()
	if profile == Publisher {
		k.publisher()
	}
	return k.out, c.findings
}

// A problem function returns what is wrong with a text, or "" when it is fine.
type problem func(string) string

func pathProblem(s string) string {
	switch {
	case rules.IsRepositoryPath(s):
		return ""
	case len(s) > rules.MaxPathLength:
		return fmt.Sprintf("is %d bytes; a path is at most %d", len(s), rules.MaxPathLength)
	}
	return "must be a relative path inside the repository (no .., no leading /, no . or empty segments, no glob, no backslash or space)"
}

func hclProblem(s string) string {
	if d := pathProblem(s); d != "" {
		return d
	}
	if !strings.HasSuffix(s, ".modelspec.hcl") {
		return "must end in .modelspec.hcl"
	}
	return ""
}

func moduleProblem(s string) string {
	if isModuleName(s) {
		return ""
	}
	return "must be a module name: a letter or _, then letters, digits and _"
}

func licenceProblem(s string) string {
	switch {
	case rules.IsLicenceID(s):
		return ""
	case len(s) > rules.MaxLicenceLength:
		return fmt.Sprintf("is %d bytes; a licence id is at most %d", len(s), rules.MaxLicenceLength)
	}
	return "must be an SPDX-shaped licence id such as MIT or CC0-1.0"
}

func engineProblem(s string) string {
	if rules.IsEngine(s) {
		return ""
	}
	return fmt.Sprintf("must be a letter, then letters, digits and . _ + - (at most %d characters)", rules.MaxEngineLength)
}

func graphAddressProblem(s string) string {
	if strings.HasPrefix(s, "meaning://") {
		return ""
	}
	return "must be the graph's meaning:// address"
}

func repositoryURLProblem(s string) string {
	if _, ok := rules.RepositoryKey(s); ok {
		return ""
	}
	if len(s) > rules.MaxRepositoryLength {
		return fmt.Sprintf("is %d bytes; a repository URL is at most %d", len(s), rules.MaxRepositoryLength)
	}
	return "must be the https URL of a repository on github.com: https://github.com/<org>/<repository> (no trailing slash, .git, port, query or fragment)"
}

// field is a text field of the manifest.
type field struct {
	parent *Node
	key    string
	label  string
	// required: absent, null, blank and not-text are manifest-required. When it
	// is false they are refused, as written, under rule.
	required bool
	rule     string  // the rule of a written value that is refused
	hint     string  // what to write
	problem  problem // what else is wrong with a written text; nil for nothing
}

// text judges a field and returns its Fact.
func (k *manifestChecker) text(f field) Fact[string] {
	n := f.parent.Field(f.key)
	if n == nil {
		if f.required {
			k.c.add("manifest-required", where(f.parent, f.key), "%s is required: %s", f.label, f.hint)
		}
		return Fact[string]{}
	}
	if n.Kind != kindString || rules.IsBlank(n.Text) {
		if f.required {
			k.c.add("manifest-required", n.Line, "%s is required: %s, got %s", f.label, f.hint, describe(n))
		} else {
			k.c.add(f.rule, n.Line, "%s, when given, must be text: %s, got %s", f.label, f.hint, describe(n))
		}
		return found(n, false, "")
	}
	if f.problem != nil {
		if d := f.problem(n.Text); d != "" {
			k.c.add(f.rule, n.Line, "%s %s, got %s: %s", f.label, d, rules.Quote(n.Text), f.hint)
			return found(n, false, "")
		}
	}
	return found(n, true, n.Text)
}

// urlField judges a URL the manifest publishes: absent, null and "" are
// "required" (when it is), anything else is refused unless it passes check.
func (k *manifestChecker) urlField(parent *Node, key, label string, required bool, check func(string) (rules.URL, error)) (rules.URL, Fact[string]) {
	n := parent.Field(key)
	if n == nil || n.Kind == kindNull || (n.Kind == kindString && n.Text == "") {
		if n == nil && !required {
			return rules.URL{}, Fact[string]{}
		}
		k.c.add("manifest-required", where(parent, key), "%s is required: write a public https URL", label)
		return rules.URL{}, found(n, false, "")
	}
	if n.Kind != kindString {
		k.c.add("manifest-url", n.Line, "%s is not a URL (%s): write a public https URL as text", label, describe(n))
		return rules.URL{}, found(n, false, "")
	}
	u, err := check(n.Text)
	if err != nil {
		k.c.add("manifest-url", n.Line, "%s %s", label, err.Error())
		return rules.URL{}, found(n, false, "")
	}
	return u, found(n, true, n.Text)
}

func publicURL(s string) (rules.URL, error)   { return rules.ParsePublicHTTPSURL(s) }
func templateURL(s string) (rules.URL, error) { return rules.ParsePublicHTTPSURLTemplate(s) }

// canonicalURL is a public https URL without a trailing slash, with ovdb as a
// complete path segment or as a subdomain.
func canonicalURL(s string) (rules.URL, error) {
	u, err := rules.ParsePublicHTTPSURL(s)
	if err != nil {
		return u, err
	}
	if strings.HasSuffix(u.Path, "/") {
		return u, &rules.Problem{Rule: "canonical-url", Detail: "must not have a trailing slash: remove the last /"}
	}
	if !hasOvdbMarker(u) {
		return u, &rules.Problem{Rule: "canonical-url", Detail: "must have ovdb as a complete path segment or as a subdomain (https://acme.com/ovdb/sales or https://ovdb.acme.com/sales)"}
	}
	return u, nil
}

func (k *manifestChecker) check() {
	m, out, c := k.m, &k.out, k.c
	f := m.Field("format")
	out.Format = found(f, f != nil && f.Kind == kindString && f.Text == ManifestFormat, ManifestFormat)
	if !out.Format.Valid {
		c.add("manifest-format", where(m, "format"), "format must be %s, got %s: write format: %s", ManifestFormat, describe(f), ManifestFormat)
	}
	somewhat := func(key string) Fact[string] {
		return k.text(field{parent: m, key: key, label: key, required: true, hint: "write some text"})
	}
	out.ID, out.Title, out.Description = somewhat("id"), somewhat("title"), somewhat("description")

	canonical, urlFact := k.urlField(m, "url", "url", true, canonicalURL)
	out.URL, k.canonical = urlFact, canonical
	if hp := m.Field("homepage"); hp != nil {
		valid := false
		if hp.Kind == kindString && hp.Text != "" {
			if err := rules.Homepage(hp.Text); err != nil {
				c.add("manifest-homepage", hp.Line, "homepage %s", err.Error())
			} else {
				valid = true
			}
		} else {
			c.add("manifest-homepage", hp.Line, "homepage is not a URL (%s): write an https URL of at most 200 characters, or leave homepage out when the database has no website", describe(hp))
		}
		out.Homepage = found(hp, valid, hp.Text)
	}

	deployment := m.Field("deployment")
	k.deployed, out.DeploymentURL = k.urlField(deployment, "url", "deployment.url", true, publicURL)
	out.Engine = k.text(field{parent: deployment, key: "engine", label: "deployment.engine", required: true, rule: "manifest-engine", hint: "write the engine your deployment runs, such as postgres", problem: engineProblem})
	discovery, discoveryFact := k.urlField(deployment, "discovery", "deployment.discovery", true, publicURL)
	out.Discovery, k.discovery = discoveryFact, discovery
	if urlFact.Valid && discoveryFact.Valid && discovery.Host != canonical.Host {
		c.add("manifest-discovery", where(deployment, "discovery"), "deployment.discovery must be on the same origin as url (https://%s), not https://%s: serve the discovery document from the canonical host", canonical.Host, discovery.Host)
	}
	k.page, out.RecordsetPage = k.urlField(deployment, "recordset_page", "deployment.recordset_page", false, templateURL)

	k.model()

	publisher := m.Field("publisher")
	out.PublisherName = k.text(field{parent: publisher, key: "name", label: "publisher.name", required: true, hint: "write the publisher's name"})
	_, out.PublisherURL = k.urlField(publisher, "url", "publisher.url", true, publicURL)
	repository := field{parent: publisher, key: "repository", label: "publisher.repository", rule: "manifest-publisher", hint: "write the repository that carries this manifest, or leave publisher.repository out", problem: repositoryURLProblem}
	if k.profile == Publisher { // the Chinook checker requires it
		repository.required, repository.hint = true, "write the https URL of the repository that carries this manifest"
	}
	out.PublisherRepository = k.text(repository)

	licences := m.Field("licences")
	out.LicenceData = k.text(field{parent: licences, key: "data", label: "licences.data", required: true, rule: "manifest-licence", hint: "write an SPDX licence id such as MIT or CC0-1.0", problem: licenceProblem})

	k.recordsets()
}

// model judges model and meaning, which the manifest writes in one of two forms
// that never mix: own model files (model.modelspec or model.hcl written), or a
// shared model named by pinned addresses.
func (k *manifestChecker) model() {
	m, out, c := k.m, &k.out, k.c
	model, meaning := m.Field("model"), m.Field("meaning")
	graph := meaning.Field("graph")
	licences := m.Field("licences")
	out.ModelHCL = k.text(field{parent: model, key: "hcl", label: "model.hcl", rule: "manifest-model", hint: "write the path of the model's source, ending in .modelspec.hcl", problem: hclProblem})
	out.ModelName = k.text(field{parent: model, key: "name", label: "model.name", rule: "manifest-model", hint: "write the ModelSpec module's name, or leave model.name out", problem: moduleProblem})
	out.Form = FormShared
	if model.Field("modelspec") != nil || model.Field("hcl") != nil {
		out.Form = FormOwn
	}
	if out.Form == FormOwn {
		out.ModelSpec = k.text(field{parent: model, key: "modelspec", label: "model.modelspec", required: true, rule: "manifest-model", hint: "write the path of the model's JSON file, relative to the repository root", problem: pathProblem})
		out.ModelAddress = k.address(model, "model.address", parseModelAddress, "modelspec://github.com/<org>/<repository>/<module>, this repository and the module name, without ?ref= (a model in another repository is named by model.address alone, with ?ref= and no local model files)", noPin)
		if a := meaning.Field("address"); a != nil {
			c.add("manifest-form", a.Line, "meaning.address is only for a shared model (model.address with ?ref= and no local model files); a manifest with its own model files has its own meaning file and names its graph by meaning.graph.address: remove meaning.address")
			out.MeaningAddress = found(a, false, Address{})
		}
		if p := m.Field("recordsets_partial"); p != nil {
			c.add("manifest-form", p.Line, "recordsets_partial is only for a shared model; a manifest with its own model files lists every ModelSpec entity: remove recordsets_partial")
			out.RecordsetsPartial = found(p, false, false)
		}
		out.MeaningFile = k.text(field{parent: meaning, key: "file", label: "meaning.file", required: true, rule: "manifest-meaning", hint: "write the path of the meaning file, relative to the repository root", problem: pathProblem})
		out.GraphAddress = k.text(field{parent: graph, key: "address", label: "meaning.graph.address", required: true, rule: "manifest-meaning", hint: "write the graph's meaning:// address", problem: graphAddressProblem})
		out.LicenceModel = k.text(field{parent: licences, key: "model", label: "licences.model", required: true, rule: "manifest-licence", hint: "write an SPDX licence id", problem: licenceProblem})
		out.LicenceMeaning = k.text(field{parent: licences, key: "meaning", label: "licences.meaning", required: true, rule: "manifest-licence", hint: "write an SPDX licence id", problem: licenceProblem})
	} else {
		k.sharedModel(model, meaning, graph, licences)
	}
	out.GraphID = k.text(field{parent: graph, key: "id", label: "meaning.graph.id", required: true, hint: "write the graph's registry id"})
}

const (
	noPin   = false
	needPin = true
)

// address judges an address field: it parses as format says, names a repository
// on a host the Directory reads, spells it in lower case, and carries a pin when
// the form wants one and none when it wants none.
func (k *manifestChecker) address(parent *Node, label string, parse func(string) (Address, bool), format string, pin bool) Fact[Address] {
	key := strings.TrimPrefix(strings.TrimPrefix(label, "model."), "meaning.")
	n := parent.Field(key)
	if n == nil {
		return Fact[Address]{}
	}
	c := k.c
	if n.Kind != kindString || len(n.Text) > rules.MaxURLLength {
		if n.Kind == kindString {
			c.add("manifest-"+family(label), n.Line, "%s is %d bytes; at most %d", label, len(n.Text), rules.MaxURLLength)
		} else {
			c.add("manifest-"+family(label), n.Line, "%s must be %s, got %s", label, format, describe(n))
		}
		return found(n, false, Address{})
	}
	parsed, ok := parse(n.Text)
	if !ok {
		c.add("manifest-"+family(label), n.Line, "%s must be %s, got %s", label, format, describe(n))
		return found(n, false, Address{})
	}
	valid := true
	if _, ok := rules.RepositoryKey("https://" + parsed.Repository); !ok {
		valid = false
		if len(parsed.Repository) > rules.MaxRepositoryLength-len("https://") {
			c.add("manifest-"+family(label), n.Line, "%s names a repository of %d bytes; a repository is at most %d", label, len(parsed.Repository), rules.MaxRepositoryLength-len("https://"))
		} else {
			c.add("manifest-"+family(label), n.Line, "%s must name a repository on github.com, as github.com/<org>/<repository>, got %s", label, rules.Quote(parsed.Repository))
		}
	} else if parsed.Repository != lowerASCII(parsed.Repository) {
		valid = false
		c.add("manifest-"+family(label), n.Line, "%s must be written in lower case (host, organisation and repository; a module name is case-sensitive), got %s", label, rules.Quote(parsed.Repository))
	}
	switch {
	case pin && parsed.Ref == "":
		valid = false
		c.add("manifest-"+family(label), n.Line, "%s must carry ?ref=<40 hex> (the pin says which commit is read): add ?ref= and the commit", label)
	case !pin && parsed.Ref != "":
		valid = false
		c.add("manifest-"+family(label), n.Line, "%s must not carry ?ref= when the model's files are in the same repository: remove ?ref=", label)
	}
	return found(n, valid, parsed)
}

func family(label string) string {
	if strings.HasPrefix(label, "meaning.") {
		return "meaning"
	}
	return "model"
}

// sharedModel judges the shared form: no local files, both addresses pinned.
func (k *manifestChecker) sharedModel(model, meaning, graph, licences *Node) {
	m, out, c := k.m, &k.out, k.c
	if model.Field("address") == nil {
		c.add("manifest-model", where(model, "address"), "model must name the model by local files (model.modelspec and model.hcl) or, for a model published in another repository, by model.address with ?ref=<40 hex>")
	}
	out.ModelAddress = k.address(model, "model.address", parseModelAddress, "modelspec://github.com/<org>/<repository>/<module>?ref=<40 hex> for a model in another repository", needPin)
	if meaning.Field("address") == nil {
		c.add("manifest-meaning", where(meaning, "address"), "meaning.address is required when model.address names a model in another repository (the meaning graph is then shared too): write meaning://github.com/<org>/<repository>?ref=<40 hex>")
	}
	out.MeaningAddress = k.address(meaning, "meaning.address", parseGraphAddress, "meaning://github.com/<org>/<repository>?ref=<40 hex>", needPin)
	out.MeaningFile = k.text(field{parent: meaning, key: "file", label: "meaning.file", required: true, rule: "manifest-meaning", hint: "write the path of the file of the graph, in the graph's repository, that binds the model", problem: pathProblem})
	out.GraphAddress = k.text(field{parent: graph, key: "address", label: "meaning.graph.address", rule: "manifest-meaning", hint: "write the graph's meaning:// address without a pin, or leave it out", problem: graphAddressProblem})
	out.LicenceModel = k.text(field{parent: licences, key: "model", label: "licences.model", rule: "manifest-licence", hint: "write an SPDX licence id such as MIT or CC0-1.0, or leave it out", problem: licenceProblem})
	out.LicenceMeaning = k.text(field{parent: licences, key: "meaning", label: "licences.meaning", rule: "manifest-licence", hint: "write an SPDX licence id such as MIT or CC0-1.0, or leave it out", problem: licenceProblem})
	if p := m.Field("recordsets_partial"); p != nil {
		if p.Kind != kindBool {
			c.add("manifest-form", p.Line, "recordsets_partial must be true or false, got %s", describe(p))
		}
		out.RecordsetsPartial = found(p, p.Kind == kindBool, p.Text == "true")
	}
}

// recordsets judges the list of recordset names: a non-empty list of text, each
// name once.
func (k *manifestChecker) recordsets() {
	list := k.m.Field("recordsets")
	good := list != nil && list.Kind == kindSeq && len(list.Items) > 0
	if good {
		for _, item := range list.Items {
			good = good && isText(item)
		}
	}
	if !good {
		k.c.add("manifest-recordsets", where(k.m, "recordsets"), "recordsets must be a non-empty list of names: write recordsets: with one name per line, each a ModelSpec entity")
		k.out.Recordsets = found(list, false, []string(nil))
		return
	}
	names := make([]string, len(list.Items))
	seen := map[string]bool{}
	for i, item := range list.Items {
		names[i] = item.Text
		if seen[item.Text] {
			good = false
			k.c.add("manifest-recordsets", item.Line, "recordsets lists %s twice: list each name once", rules.Quote(item.Text))
		}
		seen[item.Text] = true
	}
	k.out.Recordsets = found(list, good, names)
}
