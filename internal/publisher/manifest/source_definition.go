package manifest

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/ovdb/publisher/source"
)

func (k *manifestChecker) sourceDefinition() {
	n := k.m.Field("source_definition")
	if n == nil {
		return
	}
	d, err := source.Parse(nodeJSON(n))
	if err == nil {
		err = d.Matches(k.out.Recordsets.Value, k.out.RecordsetEntities.Value)
	}
	if err == nil {
		err = d.MatchesTerms(k.out.DataDeclaration.Value)
	}
	k.out.SourceDefinition = found(n, err == nil, d)
	if err != nil {
		k.c.add("manifest-source-definition", n.Line, "source_definition: %s", plain(err.Error()))
	}
	// Pinned input/representation contracts must never relabel a mutable read
	// as publisher-verified immutable data or authorize retained copies.
	for _, key := range []string{"data_rights", "representation_contract"} {
		if k.m.Field(key) != nil {
			k.c.add("manifest-source-definition", n.Line, "source_definition cannot use the immutable-input %s profile", key)
		}
	}
}

// HasSourceDefinition includes invalid declarations, preventing a replacement
// Git object from hiding an original dynamic definition during admission.
func HasSourceDefinition(data []byte) bool {
	n, err := parseYAML(data)
	return err == nil && n != nil && n.Field("source_definition") != nil
}

func (j *Judge) descriptorSourceDefinition(object map[string]any, paired Manifest, out *Descriptor, c *collector) {
	v, present := object["source_definition"]
	if !present && !paired.SourceDefinition.Present {
		return
	}
	if !present || !paired.SourceDefinition.Usable() {
		c.add("descriptor-source-definition", 1, "descriptor must carry the same usable manifest source_definition")
		return
	}
	b, _ := json.Marshal(v)
	d, err := source.Parse(b)
	if err != nil {
		c.add("descriptor-source-definition", 1, "source_definition: %s", plain(err.Error()))
		return
	}
	want, _ := json.Marshal(paired.SourceDefinition.Value)
	got, _ := json.Marshal(d)
	if string(want) != string(got) {
		c.add("descriptor-source-definition", 1, "descriptor and manifest source_definition must be identical")
		return
	}
	if err := j.httpDescriptorTerms(object, *d, paired.Recordsets.Value); err != nil {
		c.add("descriptor-source-definition", 1, "source_definition terms: %s", plain(err.Error()))
		return
	}
	out.SourceDefinition = d
}

// The preparatory profile has one complete linked declaration, inherited by
// every recordset. Materialized descriptor terms must agree with it exactly;
// recordset overrides and publisher-supplied execution evidence are unsupported.
func (j *Judge) httpDescriptorTerms(object map[string]any, d source.Definition, names []string) error {
	terms := func(object map[string]any) error {
		for key := range object {
			for _, reserved := range []string{"sourceRights", "sourceDefinitionEvidence", "providerReads"} {
				if strings.EqualFold(key, reserved) {
					return fmt.Errorf("execution evidence cannot be authored in a descriptor")
				}
			}
		}
		licences, ok := object["licences"].(map[string]any)
		if !ok {
			return fmt.Errorf("materialized licences.data is required")
		}
		b, _ := json.Marshal(licences["data"])
		declaration, err := license.ParseJSON(b, licenseProfile(j.profile))
		if err != nil {
			return err
		}
		return d.MatchesTerms(declaration)
	}
	if err := terms(object); err != nil {
		return err
	}
	records, ok := object["recordsets"].([]any)
	if !ok || len(records) != len(names) {
		return fmt.Errorf("descriptor recordsets must match manifest native names")
	}
	seen := map[string]bool{}
	for _, value := range records {
		record, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("recordset must be an object with native name and licences.data")
		}
		name, ok := record["name"].(string)
		if !ok || seen[name] || !slices.Contains(names, name) {
			return fmt.Errorf("recordset names must equal manifest native names exactly once")
		}
		seen[name] = true
		if err := terms(record); err != nil {
			return err
		}
	}
	return nil
}
