package manifest

import (
	"encoding/json"
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
	out.SourceDefinition = d
}
