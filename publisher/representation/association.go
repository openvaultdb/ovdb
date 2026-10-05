package representation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// numberJSON keeps numeric tokens intact after the shared bounded strict checks.
// Equality never rounds an original/embedded snapshot through float64.
func numberJSON(data []byte, out any) error {
	var raw json.RawMessage
	if err := strictJSON(data, MaxDocumentBytes, &raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(out)
}
func checkSnapshotAssociation(raw any, receipt []byte, c Contract, ctx Context, metadata []byte, records int64) error {
	association, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("snapshot_association must be a closed object")
	}
	if err := exactKeys(association, []string{"source", "output_key"}, true); err != nil {
		return err
	}
	source, ok := association["source"].(map[string]any)
	if !ok {
		return fmt.Errorf("snapshot_association source must be an own reference")
	}
	if err := exactKeys(source, []string{"path", "sha256"}, true); err != nil {
		return err
	}
	pathValue, pathOK := source["path"].(string)
	hash, hashOK := source["sha256"].(string)
	key, keyOK := association["output_key"].(string)
	if !pathOK || !hashOK || !path(pathValue) || !hex(hash, 64) || !keyOK || len(key) == 0 || len(key) > 128 || strings.Trim(key, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return fmt.Errorf("invalid literal snapshot association ref/selector")
	}
	if pathValue == c.Native.Provenance.Path || pathValue == c.Target.Snapshot.Path || pathValue == c.Native.Dataset.Path {
		return fmt.Errorf("original snapshot cannot name receipt, later metadata or dataset")
	}
	ref := Reference{Path: pathValue, SHA256: hash}
	var later struct {
		Artifacts []Reference `json:"artifacts"`
	}
	if err := exactSnapshot(metadata, &later); err != nil {
		return err
	}
	found := false
	for _, artifact := range later.Artifacts {
		if artifact == ref {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("original snapshot must match later metadata artifact closure")
	}
	originalBytes, err := read(ref, ctx)
	if err != nil {
		return err
	}
	var original, embedded map[string]any
	if err = numberJSON(originalBytes, &original); err != nil {
		return err
	}
	if err = numberJSON(receipt, &embedded); err != nil {
		return err
	}
	if !reflect.DeepEqual(original, embedded["snapshot"]) {
		return fmt.Errorf("embedded snapshot differs from exact original values/number tokens")
	}
	if err = exactKeys(original, []string{"outputs", "counts"}, false); err != nil {
		return err
	}
	outputs, _ := original["outputs"].(map[string]any)
	selected, ok := outputs[key].(map[string]any)
	if !ok {
		return fmt.Errorf("selected original output must be a descriptor object")
	}
	if err = exactKeys(selected, []string{"file", "sha256"}, false); err != nil {
		return err
	}
	if selected["file"] != c.Native.Dataset.Path || selected["sha256"] != c.Native.Dataset.SHA256 {
		return fmt.Errorf("selected original file/hash differs from native data")
	}
	counts, _ := original["counts"].(map[string]any)
	count, ok := counts[c.Target.Entity].(json.Number)
	if !ok {
		return fmt.Errorf("original entity count must exist as an integer")
	}
	value, err := count.Int64()
	if err != nil || value < 0 || value != records {
		return fmt.Errorf("original entity count differs or is not a nonnegative int64")
	}
	return nil
}
