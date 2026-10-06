package repo

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

const goodDescriptor = `{
  "format": "ovdb-database/draft-1",
  "id": "https://chinookdb.com/ovdb/dbs/chinook",
  "localId": "chinook",
  "serverId": "https://chinookdb.com/ovdb",
  "serverDbBaseUrl": "https://chinookdb.com/ovdb/db/chinook",
  "apiUrl": "https://chinookdb.com/ovdb/api",
  "deployment": {"discovery": "https://chinookdb.com/.well-known/openvaultdb"}
}
`

func withDescriptor(descriptor string, md string) *Memory {
	m := goodRepository()
	m.Nodes["ovdb-database.json"] = Node{Kind: File, Content: []byte(descriptor)}
	m.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte(md)}
	return m
}

const (
	mdManifestFirst   = "---\novdb: 1\npublish:\n  - ./ovdb.yaml\n  - ./ovdb-database.json\n---\n"
	mdDescriptorFirst = "---\novdb: 1\npublish:\n  - ./ovdb-database.json\n  - ./ovdb.yaml\n---\n"
)

// A database descriptor that OVDB.md lists beside a manifest is judged as a descriptor, in either order, and the manifest is still the first manifest.
func TestADescriptorIsJudgedBesideItsManifest(t *testing.T) {
	for _, md := range []string{mdManifestFirst, mdDescriptorFirst} {
		for _, p := range []manifest.Profile{manifest.Publisher, manifest.Directory} {
			r := Check(withDescriptor(goodDescriptor, md), Options{Profile: p})
			if !r.OK() || !r.Manifest.Read || r.Manifest.ID.Value != "chinook" || len(r.OVDBMd.Entries) != 2 || r.Descriptors != 1 {
				t.Errorf("%v: %+v", p, r)
			}
		}
	}
}

func TestADescriptorThatIsWrongIsRefusedAtItsOwnPath(t *testing.T) {
	r := Check(withDescriptor(strings.Replace(goodDescriptor, `"localId": "chinook"`, `"localId": "Chinook"`, 1), mdManifestFirst), publisher())
	rules := rulesOf(r)
	if len(rules) != 2 || rules[0] != "descriptor-local-id" && rules[1] != "descriptor-local-id" {
		t.Fatalf("findings %v", r.Findings)
	}
	for _, f := range r.Findings {
		if f.Rule == "descriptor-local-id" && (f.Document != "ovdb-database.json" || f.Line != 4) {
			t.Errorf("finding %+v", f)
		}
		if f.Rule == "manifest-id" && f.Document != "ovdb.yaml" {
			t.Errorf("finding %+v", f)
		}
	}
	r = Check(withDescriptor(`{"format": "ovdb-database/draft-1",}`, mdManifestFirst), publisher())
	only(t, r, "descriptor-json", "ovdb-database.json", 1, "is not valid JSON")
	r = Check(withDescriptor(`{"format": "ovdb-database/draft-1", "id": 7}`, mdManifestFirst), publisher())
	if len(r.Findings) < 2 || r.Findings[0].Document != "ovdb-database.json" {
		t.Errorf("findings %v", r.Findings)
	}
}

// The Directory reads the descriptor only after the manifest is right.
func TestADescriptorIsNotJudgedWhenItsManifestIsRefused(t *testing.T) {
	m := withDescriptor(`{"format": "ovdb-database/draft-1", "id": 7}`, mdManifestFirst)
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(strings.Replace(ownManifest, "title: Chinook music store\n", "", 1))}
	r := Check(m, publisher())
	for _, f := range r.Findings {
		if f.Document == "ovdb-database.json" {
			t.Errorf("a finding about the descriptor: %+v", f)
		}
	}
	if len(r.Findings) == 0 {
		t.Error("the manifest has no title")
	}
}

// A repository has no record that says which manifest a descriptor goes with, so it lists one of each.
func TestADescriptorGoesWithExactlyOneManifest(t *testing.T) {
	r := Check(withDescriptor(goodDescriptor, "---\novdb: 1\npublish: [./ovdb-database.json]\n---\n"), publisher())
	only(t, r, RuleDescriptor, "OVDB.md", 3, "lists the database descriptor \"./ovdb-database.json\" and no manifest")
	m := withDescriptor(goodDescriptor, "---\novdb: 1\npublish: [./ovdb.yaml, ./other.yaml, ./ovdb-database.json]\n---\n")
	m.Nodes["other.yaml"] = Node{Kind: File, Content: []byte(ownManifest)}
	r = Check(m, publisher())
	if len(r.Findings) != 1 || r.Findings[0].Rule != RuleDescriptor || !strings.Contains(r.Findings[0].Message, "2 manifests and 1 database descriptors") {
		t.Errorf("findings %v", r.Findings)
	}
	m = withDescriptor(goodDescriptor, "---\novdb: 1\npublish: [./ovdb.yaml, ./ovdb-database.json, ./second.json]\n---\n")
	m.Nodes["second.json"] = Node{Kind: File, Content: []byte(goodDescriptor)}
	r = Check(m, publisher())
	if len(r.Findings) != 1 || r.Findings[0].Rule != RuleDescriptor || !strings.Contains(r.Findings[0].Message, "1 manifests and 2 database descriptors") {
		t.Errorf("findings %v", r.Findings)
	}
}

// A listed entry that cannot be read is a manifest that cannot be read, whatever it would have been.
func TestADescriptorEntryThatIsNotAFileIsReportedAsAnEntry(t *testing.T) {
	m := withDescriptor(goodDescriptor, mdManifestFirst)
	delete(m.Nodes, "ovdb-database.json")
	r := Check(m, publisher())
	only(t, r, RuleManifest, "OVDB.md", 5, "must be a tracked regular file")
}

// A manifest does not need the origin of its discovery to be the origin of its url when a descriptor goes with it; without one it does.
func TestTheOriginRuleOfTheDiscoveryIsWaivedForADescriptor(t *testing.T) {
	other := "https://cloud.openvaultdb.com/.well-known/openvaultdb"
	m := withDescriptor(strings.NewReplacer(`"serverId": "https://chinookdb.com/ovdb"`, `"serverId": "https://cloud.openvaultdb.com/ovdb"`,
		`"serverDbBaseUrl": "https://chinookdb.com/ovdb/db/chinook"`, `"serverDbBaseUrl": "https://cloud.openvaultdb.com/ovdb/db/chinook"`,
		`"apiUrl": "https://chinookdb.com/ovdb/api"`, `"apiUrl": "https://cloud.openvaultdb.com/ovdb/api"`,
		"https://chinookdb.com/.well-known/openvaultdb", other).Replace(goodDescriptor), mdManifestFirst)
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(strings.Replace(ownManifest, "discovery: https://chinookdb.com/.well-known/openvaultdb", "discovery: "+other, 1))}
	if r := Check(m, publisher()); !r.OK() {
		t.Errorf("with a descriptor: %v", r.Findings)
	}
	// The discovery of an attachment judges the manifest the same way, so it does not stop on what the descriptor waives.
	if state, _, findings := discoverAttachment(m, manifest.Publisher); state != attachmentAbsent || len(findings) != 0 {
		t.Errorf("discovery with a descriptor: %v, %v", state, findings)
	}
	m.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte(goodMD)}
	if r := Check(m, publisher()); len(r.Findings) != 1 || r.Findings[0].Rule != "manifest-discovery" {
		t.Errorf("without one: %v", r.Findings)
	}
	if state, _, findings := discoverAttachment(m, manifest.Publisher); state != attachmentIndeterminate || len(findings) != 1 {
		t.Errorf("discovery without one: %v, %v", state, findings)
	}
}

// The discovery of an attachment skips a descriptor, which has none and is not a manifest.
func TestDiscoveringAnAttachmentSkipsTheDescriptor(t *testing.T) {
	state, _, findings := discoverAttachment(withDescriptor(goodDescriptor, mdManifestFirst), manifest.Publisher)
	if state != attachmentAbsent || len(findings) != 0 {
		t.Errorf("state %v, findings %v", state, findings)
	}
}

// A descriptor is JSON, and is recognised and judged as JSON whatever the strict YAML reader would say of its text: tabs, a repeated key, a number over 2^53, a
// U+2028 in a string, a slash written \/, and a nesting that the YAML reader would refuse as too deep.
func TestADescriptorIsReadAsJSONNotAsYAML(t *testing.T) {
	tabs := strings.ReplaceAll(goodDescriptor, "  ", "\t")
	for name, text := range map[string]string{
		"tabs":          tabs,
		"a repeat":      strings.Replace(goodDescriptor, `"localId"`, `"localId": "x", "localId"`, 1),
		"a big number":  strings.Replace(goodDescriptor, `"apiUrl"`, `"big": 12345678901234567890123, "apiUrl"`, 1),
		"a U+2028":      strings.Replace(goodDescriptor, `"apiUrl"`, "\"s\": \"a\u2028b\", \"apiUrl\"", 1),
		"an escaped /":  strings.Replace(goodDescriptor, `"ovdb-database/draft-1"`, `"ovdb-database\/draft-1"`, 1),
		"an escaped o":  strings.Replace(goodDescriptor, `"ovdb-database/draft-1"`, `"\u006fvdb-database/draft-1"`, 1),
		"1e999":         strings.Replace(goodDescriptor, `"apiUrl"`, `"n": 1e999, "apiUrl"`, 1),
		"a lone escape": strings.Replace(goodDescriptor, `"apiUrl"`, `"s": "\ud800", "apiUrl"`, 1),
	} {
		if r := Check(withDescriptor(text, mdManifestFirst), publisher()); !r.OK() {
			t.Errorf("%s: %v", name, r.Findings)
		}
	}
	// Nested 70 deep: recognised as a descriptor, and refused by the bound of this check, which is recorded (the Directory reads any depth).
	deep := strings.Replace(goodDescriptor, `"apiUrl"`, `"d": `+strings.Repeat("[", 70)+strings.Repeat("]", 70)+`, "apiUrl"`, 1)
	r := Check(withDescriptor(deep, mdManifestFirst), publisher())
	only(t, r, "descriptor-json", "ovdb-database.json", 1, "nested more than 64 levels deep")
	deep = strings.Replace(goodDescriptor, `"apiUrl"`, `"d": `+strings.Repeat("[", 9000)+strings.Repeat("]", 9000)+`, "apiUrl"`, 1)
	only(t, Check(withDescriptor(deep, mdManifestFirst), publisher()), "descriptor-json", "ovdb-database.json", 1, "nested more than 64 levels deep")
	// Past 10000 levels Go's JSON decoder gives up, and the document is read as YAML, whose own bound refuses it as a manifest (recorded in the README).
	deep = strings.Replace(goodDescriptor, `"apiUrl"`, `"d": `+strings.Repeat("[", 10500)+strings.Repeat("]", 10500)+`, "apiUrl"`, 1)
	only(t, Check(withDescriptor(deep, mdManifestFirst), publisher()), "yaml-limit", "ovdb-database.json", 7, "nested more than 64 levels deep")
}

// attachedWithDescriptor is the repository of a valid representation contract (the captured real-ror fixture and its raw-data proof) with a database descriptor
// beside the manifest, whose server and discovery are on another host than the identity (so that the waiver is needed).
func attachedWithDescriptor(t *testing.T) (*Memory, DependencyReaders) {
	t.Helper()
	p, deps, _ := defaultNativeFixture(t, "real-ror", []byte("raw exact bytes"))
	data, _ := p.Blob("ovdb.yaml", MaxFileBytes)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	deployment := m["deployment"].(map[string]any)
	deployment["discovery"] = "https://cloud.example.net/.well-known/openvaultdb"
	m["deployment"] = deployment
	data, _ = json.Marshal(m)
	p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: data}
	descriptor, _ := json.Marshal(map[string]any{
		"format": "ovdb-database/draft-1", "id": m["url"], "localId": m["id"], "serverId": "https://cloud.example.net/ovdb",
		"serverDbBaseUrl": "https://cloud.example.net/ovdb/db/x", "apiUrl": "https://cloud.example.net/ovdb/api",
		"deployment": map[string]any{"discovery": "https://cloud.example.net/.well-known/openvaultdb"},
	})
	p.Nodes["ovdb-database.json"] = Node{Kind: File, Content: descriptor}
	p.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte(mdManifestFirst)}
	return p, deps
}

func TestAValidAttachmentWithADescriptor(t *testing.T) {
	p, deps := attachedWithDescriptor(t)
	if r := Check(p, Options{Profile: manifest.Publisher, Dependencies: deps}); !r.OK() || r.Descriptors != 1 {
		t.Fatalf("%v", r.Findings)
	}
	delete(p.Nodes, "ovdb-database.json")
	p.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte(goodMD)}
	if r := Check(p, Options{Profile: manifest.Publisher, Dependencies: deps}); r.OK() {
		t.Error("without the descriptor the discovery is on another origin than the url")
	}
}

// The same through git, whose reader runs the pass that looks for an attachment first.
func TestRealGitAValidAttachmentWithADescriptor(t *testing.T) {
	realGit(t)
	p, deps := attachedWithDescriptor(t)
	dir := buildReal(t, &model{tracked: p.Nodes, location: "normal"})
	r := Check(NewGit(ExecRunner{Dir: dir}), Options{Profile: manifest.Publisher, Dependencies: deps})
	if !r.OK() || r.Descriptors != 1 {
		t.Fatalf("%v", r.Findings)
	}
	finished(t)
}
