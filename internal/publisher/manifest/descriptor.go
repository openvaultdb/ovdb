package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// DescriptorFormat is the format a database descriptor declares.
const DescriptorFormat = "ovdb-database/draft-1"

// descriptorFormatPrefix says that a document is a database descriptor, of this version or another: the manifests are ovdb-manifest/..., the descriptors
// ovdb-database/..., and a document that says it is the second is never judged as the first.
const descriptorFormatPrefix = "ovdb-database/"

// maxDescriptorDepth is how deep the JSON of a descriptor may nest. The Directory's JSON.parse reads any depth until the stack of its runtime ends.
const maxDescriptorDepth = 64

// IsDescriptor reports whether a publish entry is a database descriptor and not a manifest: a JSON object whose format is text beginning ovdb-database/.
// The Directory knows the descriptor by the field of the registry's record that names it (database_manifest) and reads it with JSON.parse and nothing
// else; a repository has no record, so the format, which the descriptor must carry in any case, is what tells it apart, and it is read as JSON.parse reads
// it: the last of a repeated key counts, an escape in the text is the character it spells (ovdb-database\/draft-1), and no rule of YAML applies (a tab,
// a long number or a U+2028 in a string is JSON). A document that is not JSON is read as YAML, and a YAML mapping with such a format is a descriptor too,
// so that Descriptor says it is not valid JSON; any other document is a manifest, and its own findings say what is wrong.
func IsDescriptor(doc []byte) bool {
	if len(doc) > MaxDocumentBytes {
		return false
	}
	if format, ok := jsonFormat(doc); ok {
		return strings.HasPrefix(format, descriptorFormatPrefix)
	}
	// Not JSON. Reading a manifest as YAML here would parse it twice (it is parsed in full when it is judged), so only a document that holds the words is
	// read: a YAML descriptor that spells them with an escape is judged as a manifest, and is refused as one, as a YAML descriptor is refused in any case.
	if !bytes.Contains(doc, []byte("ovdb-database")) {
		return false
	}
	root, err := parseYAML(doc)
	if err != nil || root == nil || root.Kind != kindMap {
		return false
	}
	f := root.Field("format")
	return f != nil && f.Kind == kindString && strings.HasPrefix(f.Text, descriptorFormatPrefix)
}

// jsonFormat reads a JSON document that is an object, as JSON.parse reads it, and returns the top-level format when it is text ("" when it is not text or
// is missing). ok is false when the document is not one JSON object (invalid JSON, or another JSON value, or text after the value). It reads
// token by token, so it has no limit of depth: a descriptor nested deeper than Descriptor reads is still recognised, and Descriptor then refuses it.
func jsonFormat(doc []byte) (format string, ok bool) {
	d := json.NewDecoder(bytes.NewReader(doc))
	d.UseNumber() // 1e999 is a number to JSON.parse
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return "", false
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return "", false
		}
		value, err := d.Token()
		if err != nil {
			return "", false
		}
		if delim, nested := value.(json.Delim); nested {
			// Skip a nested value by its brackets: no limit of depth, which a decoded value would have.
			for depth := 1; depth > 0 && (delim == '[' || delim == '{'); {
				t, err := d.Token()
				if err != nil {
					return "", false
				}
				if inner, ok := t.(json.Delim); ok {
					if inner == '[' || inner == '{' {
						depth++
					} else {
						depth--
					}
				}
			}
		}
		if key == "format" { // the last of a repeated key counts; a value that is not text is no format
			text, _ := value.(string)
			format = text
		}
	}
	// The closing bracket, and only white space after it, as JSON.parse has it (space, tab, line feed, carriage return).
	if t, err := d.Token(); err != nil || t != json.Delim('}') || len(bytes.Trim(doc[d.InputOffset():], " \t\n\r")) != 0 {
		return "", false
	}
	return format, true
}

// A descriptor's judged facts, as written.
type Descriptor struct {
	// Read is true when the document was read as a JSON object.
	Read bool

	ID, LocalID, ServerID, ServerDBBaseURL, APIURL, Discovery Fact[string]
}

// Descriptor judges the database descriptor at path against the manifest that it goes with, judged before it (paired, at pairedPath), as the Directory's
// databaseDescriptorProblems and the check of the manifest's id against the descriptor's localId do for a record. The record's url is the manifest's url.
// The Directory stops at the problems of the manifest before it looks at the descriptor, and so does this: pass a manifest that was judged without findings.
// What belongs to the registry alone (the claims of server ids, the record key) is not judged.
func (j *Judge) Descriptor(doc []byte, path string, paired Manifest, pairedPath string) (Descriptor, []Finding) {
	c := newCollector(path, j.b)
	var out Descriptor
	if tooBig(c, doc) {
		return out, c.findings
	}
	var value any
	if err := decodeJSON(doc, &value); err != nil {
		c.add("descriptor-json", jsonErrorLine(doc, err), "is not valid JSON: %s: write the descriptor as one JSON object", plain(err.Error()))
		return out, c.findings
	}
	object, ok := value.(map[string]any)
	if !ok {
		c.add("descriptor-json", 1, "is not a JSON object: write the descriptor as one JSON object")
		return out, c.findings
	}
	out.Read = true
	at := func(key string) int { return keyLine(doc, key) }

	if format, _ := object["format"].(string); format != DescriptorFormat {
		c.add("descriptor-format", at("format"), "format must be %s, got %s: write format: %s", DescriptorFormat, describeJSON(object["format"]), DescriptorFormat)
	}
	text := func(key string) (string, bool) {
		s, ok := object[key].(string)
		if !ok || s == "" {
			c.add("descriptor-required", at(key), "%s is required: write some text", key)
			return "", false
		}
		return s, true
	}
	id, idOK := text("id")
	localID, localOK := text("localId")
	serverID, serverOK := text("serverId")
	dbBase, dbBaseOK := text("serverDbBaseUrl")
	api, apiOK := text("apiUrl")

	// id is the record's url, which is the manifest's.
	url := paired.URL.Value
	if paired.URL.Usable() && object["id"] != url {
		c.add("descriptor-id", at("id"), "id is %s, but the url of %s is %s: they must be the same text", describeJSON(object["id"]), rules.Quote(pairedPath), rules.Quote(url))
	}
	if localOK && !rules.IsLocalID(localID) {
		c.add("descriptor-local-id", at("localId"), "localId %s must be lower-case letters, digits and single hyphens, beginning with a letter, at most %d characters", rules.Quote(localID), rules.MaxLocalIDLength)
	}
	if idOK {
		if err := rules.GlobalDatabaseID(id); err != nil {
			c.add("descriptor-id", at("id"), "id %s", err.Error())
		} else {
			out.ID = Fact[string]{Present: true, Valid: true, Value: id, Line: at("id")}
		}
	}
	public := func(key, value string, present bool) (rules.URL, bool) {
		if !present {
			return rules.URL{}, false
		}
		u, err := rules.ParsePublicHTTPSURL(value)
		if err != nil {
			c.add("descriptor-url", at(key), "%s %s", key, err.Error())
			return rules.URL{}, false
		}
		return u, true
	}
	server, serverGood := public("serverId", serverID, serverOK)
	base, baseGood := public("serverDbBaseUrl", dbBase, dbBaseOK)
	apiURL, apiGood := public("apiUrl", api, apiOK)
	if serverGood {
		out.ServerID = Fact[string]{Present: true, Valid: true, Value: serverID, Line: at("serverId")}
	}
	if baseGood {
		out.ServerDBBaseURL = Fact[string]{Present: true, Valid: true, Value: dbBase, Line: at("serverDbBaseUrl")}
	}
	if apiGood {
		out.APIURL = Fact[string]{Present: true, Valid: true, Value: api, Line: at("apiUrl")}
	}
	if localOK && rules.IsLocalID(localID) {
		out.LocalID = Fact[string]{Present: true, Valid: true, Value: localID, Line: at("localId")}
	}

	// The origin rules are the Directory's only when the server id is a public URL and the local id is one (the same condition, as it is written there).
	discovery := descriptorDiscovery(object)
	if serverGood && out.LocalID.Valid {
		if baseGood && base.Host != server.Host {
			c.add("descriptor-origin", at("serverDbBaseUrl"), "serverDbBaseUrl must share serverId's origin https://%s, not https://%s: serve the database from the same host", server.Host, base.Host)
		}
		if apiGood && apiURL.Host != server.Host {
			c.add("descriptor-origin", at("apiUrl"), "apiUrl must share serverId's origin https://%s, not https://%s: serve the API from the same host", server.Host, apiURL.Host)
		}
		line := at("discovery")
		if discovery == nil {
			c.add("descriptor-discovery", at("deployment"), "deployment.discovery is required: write the discovery URL under deployment")
		} else {
			if u, err := rules.ParsePublicHTTPSURL(*discovery); err != nil {
				c.add("descriptor-discovery", line, "deployment.discovery %s", err.Error())
			} else {
				out.Discovery = Fact[string]{Present: true, Valid: true, Value: *discovery, Line: line}
				if u.Host != server.Host {
					c.add("descriptor-discovery", line, "deployment.discovery must share serverId's origin https://%s, not https://%s", server.Host, u.Host)
				}
			}
			// The manifest's discovery, as written; the Directory compares the two as text and whether or not the descriptor's is a public URL.
			if paired.Discovery.Usable() && paired.Discovery.Value != *discovery {
				c.add("descriptor-discovery", line, "deployment.discovery is %s, but %s says %s: write the same URL in both", rules.Quote(*discovery), rules.Quote(pairedPath), rules.Quote(paired.Discovery.Value))
			}
		}
	}

	// The manifest's id is the descriptor's localId (the record key only stands in for a localId that is not written).
	if localValue, written := object["localId"]; written && localValue != nil && paired.ID.Usable() && localValue != paired.ID.Value {
		mc := newCollector(pairedPath, j.b)
		mc.add("manifest-id", paired.ID.Line, "id is %s, but the descriptor localId is %s: write the same id in both", rules.Quote(paired.ID.Value), describeJSON(localValue))
		c.findings = append(c.findings, mc.findings...)
	}
	return out, c.findings
}

// descriptorDiscovery is deployment.discovery of a descriptor when it is text, as JavaScript's descriptor.deployment?.discovery reads it: only a JSON
// object has such a key; anything else, and a value that is not text, is nothing.
func descriptorDiscovery(object map[string]any) *string {
	deployment, ok := object["deployment"].(map[string]any)
	if !ok {
		return nil
	}
	s, ok := deployment["discovery"].(string)
	if !ok {
		return nil
	}
	return &s
}

// decodeJSON reads one JSON value as JavaScript's JSON.parse does, except that it refuses a document nested more than maxDescriptorDepth levels, which
// is a recorded bound of this check (the reference reads any depth).
func decodeJSON(doc []byte, into *any) error {
	depth := 0
	inString, escaped := false, false
	for _, b := range doc {
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
		case b == '"':
			inString = true
		case b == '[' || b == '{':
			if depth++; depth > maxDescriptorDepth {
				return errDescriptorDepth
			}
		case b == ']' || b == '}':
			depth--
		}
	}
	d := json.NewDecoder(bytes.NewReader(doc))
	d.UseNumber() // JavaScript reads 1e999 as Infinity; a float64 would refuse it
	if err := d.Decode(into); err != nil {
		return err
	}
	// Only white space may follow the value, as JSON.parse has it (space, tab, line feed, carriage return).
	if len(bytes.Trim(doc[d.InputOffset():], " \t\n\r")) != 0 {
		return errTrailing
	}
	return nil
}

type descriptorError string

func (e descriptorError) Error() string { return string(e) }

const (
	errDescriptorDepth descriptorError = "the JSON is nested more than 64 levels deep, which is more than this check reads"
	errTrailing        descriptorError = "text after the JSON value"
)

// describeJSON says what a JSON value is, quoted when it is text.
func describeJSON(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case string:
		return rules.Quote(v)
	case bool:
		return "a boolean, not text"
	case json.Number:
		return "a number, not text"
	case []any:
		return "a list, not text"
	}
	return "a mapping, not text"
}

// keyLine is the line of the first key called key in a JSON document, or 1: where a reader looks first.
func keyLine(doc []byte, key string) int {
	needle := []byte(`"` + key + `"`)
	at := bytes.Index(doc, needle)
	if at < 0 {
		return 1
	}
	return 1 + bytes.Count(doc[:at], []byte("\n"))
}

// jsonErrorLine is the line of a JSON syntax error, or 1.
func jsonErrorLine(doc []byte, err error) int {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) && syntax.Offset > 0 && int(syntax.Offset) <= len(doc) {
		return 1 + bytes.Count(doc[:syntax.Offset], []byte("\n"))
	}
	return 1
}
