package manifest

import (
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// ManifestFormat is the format a manifest declares.
const ManifestFormat = "ovdb-manifest/draft-1"

// Form is the way a manifest names its model.
type Form string

const (
	// FormOwn: the model and the meaning file are files of the publisher's
	// repository (model.modelspec or model.hcl is present).
	FormOwn Form = "own"
	// FormShared: the model and the meaning graph are published in other
	// repositories and named by pinned addresses.
	FormShared Form = "shared"
)

// Manifest is what a manifest says, for a caller that goes on to read the files
// it names. Strings are as written; a field that is missing or not text is "".
// Use them when the findings say nothing is wrong.
type Manifest struct {
	Form Form

	ID, Title, Description string
	URL                    string // the canonical url
	Homepage               string

	DeploymentURL, Engine, Discovery, RecordsetPage string

	// The files an own manifest names, relative to the repository root.
	ModelSpecPath, ModelHCLPath, MeaningFile string
	// ModelAddress and MeaningAddress as written, and the pins they carry.
	ModelAddress, ModelRef, MeaningAddress, MeaningRef string
	// ModelRepository and ModelModule are the parts of a model address
	// (host/org/repo and the module name); MeaningRepository of a meaning address.
	ModelRepository, ModelModule, MeaningRepository string
	GraphID, GraphAddress                           string

	LicenceModel, LicenceMeaning, LicenceData string

	PublisherName, PublisherURL, PublisherRepository string

	Recordsets        []string
	RecordsetsPartial bool
}

// isText is the references' isText: text that is not blank by JavaScript's trim().
func isText(n *Node) bool { return n != nil && n.Kind == kindString && !rules.IsBlank(n.Text) }

func text(n *Node) string {
	if n != nil && n.Kind == kindString {
		return n.Text
	}
	return ""
}

// where is the line of a finding about field key of m: the value's, or the map's.
func where(m *Node, key string) int {
	if m == nil {
		return 1
	}
	return fieldLine(m, key)
}

// manifestChecker holds one manifest while it is judged.
type manifestChecker struct {
	c   *collector
	m   *Node
	out Manifest
}

// CheckManifest judges a manifest. path names it in the findings.
func CheckManifest(doc []byte, path string, profile Profile) (Manifest, []Finding) {
	c := &collector{document: path}
	if tooBig(c, doc) {
		return Manifest{}, c.result()
	}
	root := readDocument(c, doc, 0)
	if root == nil {
		return Manifest{}, c.result()
	}
	if root.Kind != kindMap {
		c.add("manifest-shape", root.Line, "is not a mapping: write the manifest as keys and values (format, id, title, ...)")
		return Manifest{}, c.result()
	}
	k := &manifestChecker{c: c, m: root}
	k.check()
	return k.out, c.result()
}

// required adds "<label> is required" unless n is text that ok accepts.
func (k *manifestChecker) required(parent *Node, key, label, hint string, ok func(string) bool) {
	n := parent.Field(key)
	if n == nil || n.Kind != kindString || !ok(n.Text) {
		k.c.add("manifest-required", where(parent, key), "%s is required: %s", label, hint)
	}
}

// urlField judges a URL the manifest publishes: missing, null and "" are
// "required", anything else is refused unless it passes check.
func (k *manifestChecker) urlField(parent *Node, key, label string, check func(string) (rules.URL, error)) (rules.URL, bool) {
	n := parent.Field(key)
	if n == nil || n.Kind == kindNull || (n.Kind == kindString && n.Text == "") {
		k.c.add("manifest-required", where(parent, key), "%s is required: write a public https URL", label)
		return rules.URL{}, false
	}
	if n.Kind != kindString {
		k.c.add("manifest-url", n.Line, "%s is not a URL (%s): write a public https URL as text", label, describe(n))
		return rules.URL{}, false
	}
	u, err := check(n.Text)
	if err != nil {
		k.c.add("manifest-url", n.Line, "%s %s", label, err.Error())
		return rules.URL{}, false
	}
	return u, true
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
	m, out := k.m, &k.out
	c := k.c
	if f := m.Field("format"); f == nil || f.Kind != kindString || f.Text != ManifestFormat {
		c.add("manifest-format", where(m, "format"), "format must be %s, got %s: write format: %s", ManifestFormat, describe(m.Field("format")), ManifestFormat)
	}
	for _, field := range []string{"id", "title", "description"} {
		if !isText(m.Field(field)) {
			c.add("manifest-required", where(m, field), "%s is required: write some text", field)
		}
	}
	out.ID, out.Title, out.Description = text(m.Field("id")), text(m.Field("title")), text(m.Field("description"))

	canonical, canonicalOK := k.urlField(m, "url", "url", canonicalURL)
	out.URL = text(m.Field("url"))
	if hp := m.Field("homepage"); hp != nil {
		if hp.Kind == kindString && hp.Text != "" {
			if err := rules.Homepage(hp.Text); err != nil {
				c.add("manifest-homepage", hp.Line, "homepage %s", err.Error())
			}
		} else {
			c.add("manifest-homepage", hp.Line, "homepage is not a URL (%s): write an https URL of at most 200 characters, or leave homepage out when the database has no website", describe(hp))
		}
		out.Homepage = text(hp)
	}

	deployment := m.Field("deployment")
	_, _ = k.urlField(deployment, "url", "deployment.url", publicURL)
	out.DeploymentURL = text(deployment.Field("url"))
	if e := deployment.Field("engine"); e == nil || e.Kind != kindString || !rules.IsEngine(e.Text) {
		c.add("manifest-engine", where(deployment, "engine"), "deployment.engine is required: a letter, then letters, digits and . _ + - (40 characters at most)")
	}
	out.Engine = text(deployment.Field("engine"))
	discovery, discoveryOK := k.urlField(deployment, "discovery", "deployment.discovery", publicURL)
	out.Discovery = text(deployment.Field("discovery"))
	if canonicalOK && discoveryOK && discovery.Host != canonical.Host {
		c.add("manifest-discovery", where(deployment, "discovery"), "deployment.discovery must be on the same origin as url (https://%s), not https://%s: serve the discovery document from the canonical host", canonical.Host, discovery.Host)
	}
	if page := deployment.Field("recordset_page"); page != nil {
		_, _ = k.urlField(deployment, "recordset_page", "deployment.recordset_page", templateURL)
		out.RecordsetPage = text(page)
	}

	k.model()

	k.required(m.Field("publisher"), "name", "publisher.name", "write the publisher's name", func(s string) bool { return !rules.IsBlank(s) })
	_, _ = k.urlField(m.Field("publisher"), "url", "publisher.url", publicURL)
	out.PublisherName, out.PublisherURL = text(m.Field("publisher").Field("name")), text(m.Field("publisher").Field("url"))
	out.PublisherRepository = text(m.Field("publisher").Field("repository"))

	licences := m.Field("licences")
	k.required(licences, "data", "licences.data", "write an SPDX licence id such as MIT or CC0-1.0", rules.IsLicenceID)
	out.LicenceData = text(licences.Field("data"))
	out.LicenceModel, out.LicenceMeaning = text(licences.Field("model")), text(licences.Field("meaning"))

	k.recordsets()
}

// model judges model and meaning, which the manifest writes in one of two forms
// that never mix: own model files (model.modelspec or model.hcl present), or a
// shared model named by pinned addresses.
func (k *manifestChecker) model() {
	m, out, c := k.m, &k.out, k.c
	model, meaning := m.Field("model"), m.Field("meaning")
	graph := meaning.Field("graph")
	if hcl := model.Field("hcl"); hcl != nil && (hcl.Kind != kindString || !rules.IsRepositoryPath(hcl.Text) || !strings.HasSuffix(hcl.Text, ".modelspec.hcl")) {
		c.add("manifest-model", hcl.Line, "model.hcl must be a relative path inside the repository (no .., no leading /, no . or empty segments, no glob) ending in .modelspec.hcl, got %s", describe(hcl))
	}
	out.ModelHCLPath = text(model.Field("hcl"))
	out.Form = FormShared
	if model.Field("modelspec") != nil || model.Field("hcl") != nil {
		out.Form = FormOwn
	}
	addr := model.Field("address")
	out.ModelAddress, out.MeaningAddress = text(addr), text(meaning.Field("address"))
	pathRule := func(s string) bool { return rules.IsRepositoryPath(s) }
	if out.Form == FormOwn {
		k.required(model, "modelspec", "model.modelspec", "write the path of the model's JSON file, relative to the repository root", pathRule)
		out.ModelSpecPath = text(model.Field("modelspec"))
		if parsed, ok := parseModelAddress(out.ModelAddress); addr != nil && !ok {
			c.add("manifest-model", addr.Line, "model.address must be modelspec://github.com/<org>/<repository>/<module>, this repository and the module name, without ?ref= (a model in another repository is named by model.address alone, with ?ref= and no local model files), got %s", describe(addr))
		} else if addr != nil {
			out.ModelRepository, out.ModelModule, out.ModelRef = parsed.Repository, parsed.Module, parsed.Ref
		}
		if a := meaning.Field("address"); a != nil {
			c.add("manifest-form", a.Line, "meaning.address is only for a shared model (model.address with ?ref= and no local model files); a manifest with its own model files has its own meaning file and names its graph by meaning.graph.address: remove meaning.address")
		}
		if p := m.Field("recordsets_partial"); p != nil {
			c.add("manifest-form", p.Line, "recordsets_partial is only for a shared model; a manifest with its own model files lists every ModelSpec entity: remove recordsets_partial")
		}
		k.required(meaning, "file", "meaning.file", "write the path of the meaning file, relative to the repository root", pathRule)
		out.MeaningFile = text(meaning.Field("file"))
		k.required(graph, "id", "meaning.graph.id", "write the graph's registry id", func(s string) bool { return !rules.IsBlank(s) })
		k.required(graph, "address", "meaning.graph.address", "write the graph's meaning:// address", func(s string) bool { return !rules.IsBlank(s) && strings.HasPrefix(s, "meaning://") })
		k.required(m.Field("licences"), "model", "licences.model", "write an SPDX licence id", rules.IsLicenceID)
		k.required(m.Field("licences"), "meaning", "licences.meaning", "write an SPDX licence id", rules.IsLicenceID)
	} else {
		k.sharedModel(addr, meaning, graph)
	}
	out.GraphID, out.GraphAddress = text(graph.Field("id")), text(graph.Field("address"))
	if p := m.Field("recordsets_partial"); p != nil && p.Kind == kindBool {
		out.RecordsetsPartial = p.Text == "true"
	}
}

// sharedModel judges the shared form: no local files, both addresses pinned.
func (k *manifestChecker) sharedModel(addr, meaning, graph *Node) {
	m, out, c := k.m, &k.out, k.c
	if addr == nil {
		c.add("manifest-model", where(m.Field("model"), "address"), "model must name the model by local files (model.modelspec and model.hcl) or, for a model published in another repository, by model.address with ?ref=<40 hex>")
	} else {
		parsed, ok := parseModelAddress(text(addr))
		switch {
		case !ok:
			c.add("manifest-model", addr.Line, "model.address must be modelspec://github.com/<org>/<repository>/<module>?ref=<40 hex> for a model in another repository, got %s", describe(addr))
		case parsed.Ref == "":
			c.add("manifest-model", addr.Line, "model.address must carry ?ref=<40 hex> when the model is in another repository (the pin says which commit is read): add ?ref= and the commit")
		}
		if ok {
			out.ModelRepository, out.ModelModule, out.ModelRef = parsed.Repository, parsed.Module, parsed.Ref
		}
	}
	a := meaning.Field("address")
	if a == nil {
		c.add("manifest-meaning", where(meaning, "address"), "meaning.address is required when model.address names a model in another repository (the meaning graph is then shared too): write meaning://github.com/<org>/<repository>?ref=<40 hex>")
	} else {
		parsed, ok := parseGraphAddress(text(a))
		switch {
		case !ok:
			c.add("manifest-meaning", a.Line, "meaning.address must be meaning://github.com/<org>/<repository>?ref=<40 hex>, got %s", describe(a))
		case parsed.Ref == "":
			c.add("manifest-meaning", a.Line, "meaning.address must carry ?ref=<40 hex> (the pin says which commit of the meaning graph is read): add ?ref= and the commit")
		}
		if ok {
			out.MeaningRepository, out.MeaningRef = parsed.Repository, parsed.Ref
		}
	}
	k.required(meaning, "file", "meaning.file", "write the path of the file of the graph, in the graph's repository, that binds the model", func(s string) bool { return rules.IsRepositoryPath(s) })
	out.MeaningFile = text(meaning.Field("file"))
	k.required(graph, "id", "meaning.graph.id", "write the MeaningGraph registry id", func(s string) bool { return !rules.IsBlank(s) })
	if ga := graph.Field("address"); ga != nil && (!isText(ga) || !strings.HasPrefix(ga.Text, "meaning://")) {
		c.add("manifest-meaning", ga.Line, "meaning.graph.address, when given, must be the graph's meaning:// address without a pin, got %s", describe(ga))
	}
	for _, field := range []string{"model", "meaning"} {
		if l := m.Field("licences").Field(field); l != nil && (l.Kind != kindString || !rules.IsLicenceID(l.Text)) {
			c.add("manifest-licence", l.Line, "licences.%s, when given, must be an SPDX-shaped licence id such as MIT or CC0-1.0, got %s", field, describe(l))
		}
	}
	if p := m.Field("recordsets_partial"); p != nil && p.Kind != kindBool {
		c.add("manifest-form", p.Line, "recordsets_partial must be true or false, got %s", describe(p))
	}
}

// recordsets judges the list of recordset names: a non-empty list of text.
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
		return
	}
	for _, item := range list.Items {
		k.out.Recordsets = append(k.out.Recordsets, item.Text)
	}
}
