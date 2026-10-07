package manifest

import (
	"encoding/json"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/ovdb/internal/publisher/datarights"
)

func licenseProfile(p Profile) license.Profile {
	if p == Publisher {
		return license.Publisher
	}
	return license.Directory
}

// nodeValue keeps wrong/null types visible to the closed declaration parser.
func nodeValue(n *Node) any {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case kindString:
		return n.Text
	case kindNull:
		return nil
	case kindBool:
		return n.Text == "true"
	case kindNumber:
		return json.Number(n.Text)
	case kindSeq:
		out := make([]any, 0, len(n.Items))
		for _, v := range n.Items {
			out = append(out, nodeValue(v))
		}
		return out
	default:
		out := map[string]any{}
		for key, v := range n.Fields {
			out[key] = nodeValue(v)
		}
		return out
	}
}
func nodeJSON(n *Node) []byte { data, _ := json.Marshal(nodeValue(n)); return data }

func (k *manifestChecker) dataDeclaration() {
	n := k.m.Field("licences").Field("data")
	if n == nil || n.Kind == kindString {
		k.out.LicenceData = k.text(field{parent: k.m.Field("licences"), key: "data", label: "licences.data", required: true, rule: "manifest-licence", hint: "write an SPDX licence id such as MIT or CC0-1.0", problem: dataLicenceProblem})
		if k.out.LicenceData.Usable() {
			d, err := license.ParseJSON(nodeJSON(n), licenseProfile(k.profile))
			if err == nil {
				k.out.DataDeclaration = found(n, true, d)
			}
		}
		return
	}
	d, err := license.ParseJSON(nodeJSON(n), licenseProfile(k.profile))
	k.out.DataDeclaration = found(n, err == nil, d)
	k.out.LicenceData = found(n, false, "")
	if err != nil {
		k.c.add("manifest-licence", n.Line, "licences.data: %s", plain(err.Error()))
	}
}
func (k *manifestChecker) dataRights() {
	n := k.m.Field("data_rights")
	if n == nil {
		return
	}
	p, err := datarights.ParseProfile(nodeJSON(n), licenseProfile(k.profile), k.out.Recordsets.Value)
	k.out.DataRights = found(n, err == nil, p)
	if err != nil {
		k.c.add("manifest-data-rights", n.Line, "data_rights: %s", plain(err.Error()))
	}
}

// HasDataRights detects authored data_rights, including an invalid declaration.
// Immutable repository checking uses it to avoid replacement-object downgrade.
func HasDataRights(doc []byte) bool {
	root, err := parseYAML(doc)
	return err == nil && root != nil && root.Field("data_rights") != nil
}

func (j *Judge) descriptorRights(object map[string]any, paired Manifest, out *Descriptor, c *collector) {
	value, present := object["data_rights"]
	if !present {
		if paired.DataRights.Present {
			c.add("descriptor-data-rights", 1, "descriptor must carry the manifest data_rights profile")
		}
		return
	}
	data, _ := json.Marshal(value)
	profile, err := datarights.ParseProfile(data, licenseProfile(j.profile), paired.Recordsets.Value)
	if err != nil {
		c.add("descriptor-data-rights", 1, "data_rights: %s", plain(err.Error()))
		return
	}
	if !paired.DataRights.Usable() {
		c.add("descriptor-data-rights", 1, "descriptor data_rights requires the same manifest profile")
		return
	}
	expected, _ := json.Marshal(paired.DataRights.Value)
	actual, _ := json.Marshal(profile)
	if string(expected) != string(actual) {
		c.add("descriptor-data-rights", 1, "descriptor and manifest must retain identical raw declarations and pins")
	}
	licences, ok := object["licences"].(map[string]any)
	if !ok {
		c.add("descriptor-data-rights", 1, "materialized licences.data is required")
		return
	}
	raw, _ := json.Marshal(licences["data"])
	db, err := license.ParseJSON(raw, licenseProfile(j.profile))
	if err != nil || !paired.DataDeclaration.Usable() || db.Normalized() != paired.DataDeclaration.Value.Normalized() {
		c.add("descriptor-data-rights", 1, "materialized database licences.data must equal the manifest")
		return
	}
	records, ok := object["recordsets"].([]any)
	if !ok {
		c.add("descriptor-data-rights", 1, "recordsets must retain existing native names")
		return
	}
	seen := map[string]bool{}
	for _, value := range records {
		record, ok := value.(map[string]any)
		if !ok {
			c.add("descriptor-data-rights", 1, "recordset must be an object")
			continue
		}
		name, ok := record["name"].(string)
		if !ok || seen[name] {
			c.add("descriptor-data-rights", 1, "recordset names must be unique native names")
			continue
		}
		seen[name] = true
		expected := db
		if override, ok := profile.Recordsets[name]; ok {
			expected = override
		}
		terms, ok := record["licences"].(map[string]any)
		if !ok {
			c.add("descriptor-data-rights", 1, "recordset licences.data is required in the rights profile")
			continue
		}
		encoded, _ := json.Marshal(terms["data"])
		declared, err := license.ParseJSON(encoded, licenseProfile(j.profile))
		if err != nil || declared.Normalized() != expected.Normalized() {
			c.add("descriptor-data-rights", 1, "recordset terms must equal the effective whole declaration")
		}
	}
	if len(seen) != len(paired.Recordsets.Value) {
		c.add("descriptor-data-rights", 1, "descriptor recordsets must match the manifest native names")
	}
	for _, name := range paired.Recordsets.Value {
		if !seen[name] {
			c.add("descriptor-data-rights", 1, "descriptor is missing a manifest native recordset")
		}
	}
	out.DataRights = profile
}
