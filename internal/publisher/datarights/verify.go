package datarights

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// Context uses only immutable objects explicitly supplied by the caller.
type Context struct {
	Repository, Revision, ManifestPath string
	ManifestBytes                      []byte
	Source                             license.Identity
	Profile                            license.Profile
	Read                               func(Reference) ([]byte, error)
}

// Verify checks byte pins before parsing, then returns normalized evidence.
// Materialized licences.data is required even when all recordsets have overrides.
func Verify(p Profile, materialized license.Declaration, names []string, ctx Context) ([]license.SourceRight, error) {
	if ctx.Profile != license.Directory && ctx.Profile != license.Publisher {
		return nil, fmt.Errorf("unknown publication profile")
	}
	if !rules.IsRepositoryPath(ctx.ManifestPath) || len(ctx.ManifestBytes) == 0 {
		return nil, fmt.Errorf("provider manifest bytes and path are required")
	}
	if err := rules.GlobalDatabaseID(ctx.Source.DatabaseID); err != nil {
		return nil, fmt.Errorf("invalid database identity")
	}
	if p.Format != Format || p.Recordsets == nil {
		return nil, fmt.Errorf("invalid data rights profile")
	}
	known := map[string]bool{}
	for _, name := range names {
		if known[name] {
			return nil, fmt.Errorf("duplicate native recordset name")
		}
		known[name] = true
	}
	for name := range p.Recordsets {
		if !known[name] {
			return nil, fmt.Errorf("override names an unlisted native recordset")
		}
	}
	if err := p.Provenance.Validate(); err != nil {
		return nil, err
	}
	if _, ok := rules.RepositoryKey(ctx.Repository); !ok {
		return nil, fmt.Errorf("invalid provider repository")
	}
	if _, err := rules.ParsePublicHTTPSURL(ctx.Source.ServerID); err != nil {
		return nil, fmt.Errorf("invalid server identity")
	}
	if ctx.Source.Recordset != "" {
		return nil, fmt.Errorf("verification requires database identity")
	}
	if ctx.Read == nil || ctx.Repository == "" || !fullRevision.MatchString(ctx.Revision) {
		return nil, fmt.Errorf("immutable provider context is required")
	}
	if err := materialized.Validate(ctx.Profile); err != nil {
		return nil, err
	}
	load := func(ref Reference, role string) ([]byte, license.Pin, error) {
		if err := ref.Validate(); err != nil {
			return nil, license.Pin{}, err
		}
		data, err := ctx.Read(ref)
		if err != nil {
			return nil, license.Pin{}, err
		}
		sum := sha256.Sum256(data)
		if int64(len(data)) != ref.Bytes || hex.EncodeToString(sum[:]) != ref.SHA256 {
			return nil, license.Pin{}, fmt.Errorf("%s byte count or SHA256 mismatch", role)
		}
		repository, revision := ref.Repository, ref.Revision
		if repository == "" {
			repository, revision = ctx.Repository, ctx.Revision
		}
		return data, license.Pin{Role: role, Repository: repository, Revision: revision, Path: ref.Path, SHA256: ref.SHA256, Bytes: ref.Bytes}, nil
	}
	var server *license.Declaration
	var serverPin *license.Pin
	if p.Server != nil {
		// A server artifact must be separate, with no child descriptor pins/cycle.
		if p.Server.Repository == "" && p.Server.Path == ctx.ManifestPath {
			return nil, fmt.Errorf("server artifact cannot be the provider manifest")
		}
		data, pin, err := load(*p.Server, "declaration")
		if err != nil {
			return nil, err
		}
		var doc map[string]json.RawMessage
		if err := Decode(data, &doc); err != nil {
			return nil, err
		}
		var format, id string
		_ = json.Unmarshal(doc["format"], &format)
		_ = json.Unmarshal(doc["id"], &id)
		var marker struct {
			Format string `json:"format"`
		}
		if err := Decode(doc["data_rights"], &marker); err != nil || marker.Format != Format {
			return nil, fmt.Errorf("server requires data_rights format marker only")
		}
		if format != "ovdb-server/draft-1" || id != ctx.Source.ServerID {
			return nil, fmt.Errorf("server format or identity mismatch")
		}
		if err := validateServerShape(doc); err != nil {
			return nil, err
		}
		var licences map[string]json.RawMessage
		if raw, supplied := doc["licences"]; supplied {
			if err := Decode(raw, &licences); err != nil {
				return nil, err
			}
			if licences == nil {
				return nil, fmt.Errorf("server licences must be an object")
			}
			for key := range licences {
				if key != "data" && key != "model" && key != "meaning" {
					return nil, fmt.Errorf("unknown server licence key")
				}
			}
		}
		if raw, ok := licences["data"]; ok {
			d, err := license.ParseJSON(raw, ctx.Profile)
			if err != nil {
				return nil, err
			}
			server = &d
		}
		serverPin = &pin
	}
	// Validate every override using the publication profile before Resolve's Directory normalization.
	if p.Database != nil {
		if err := p.Database.Validate(ctx.Profile); err != nil {
			return nil, err
		}
	}
	for _, d := range p.Recordsets {
		if err := d.Validate(ctx.Profile); err != nil {
			return nil, err
		}
	}
	// Resolve only refuses missing server identity or invalid declarations.
	// Those preconditions are checked above with the publication profile, which
	// is at least as strict as Resolve's Directory validation.
	db, _ := license.Resolve(ctx.Source, server, p.Database, nil)
	if db == nil || db.Declaration != materialized.Normalized() {
		return nil, fmt.Errorf("materialized database terms do not equal the effective raw declaration")
	}
	provenanceBytes, provenancePin, err := load(p.Provenance, "provenance")
	if err != nil {
		return nil, err
	}
	provenance, err := ParseProvenance(provenanceBytes)
	if err != nil {
		return nil, err
	}
	pins := []license.Pin{provenancePin}
	if serverPin != nil {
		pins = append(pins, *serverPin)
	}
	for _, entry := range []struct {
		role string
		refs []Reference
	}{{"input", provenance.Inputs}, {"terms", provenance.Terms}} {
		for _, ref := range entry.refs {
			_, pin, err := load(ref, entry.role)
			if err != nil {
				return nil, err
			}
			pins = append(pins, pin)
		}
	}
	providerHash := sha256.Sum256(ctx.ManifestBytes)
	provider := license.Pin{Role: "provider", Repository: ctx.Repository, Revision: ctx.Revision, Path: ctx.ManifestPath, SHA256: hex.EncodeToString(providerHash[:]), Bytes: int64(len(ctx.ManifestBytes))}
	out := make([]license.SourceRight, 0, len(names)+1)
	decorate := func(right *license.SourceRight) {
		right.EvidenceOrigin = "publisher-verified"
		right.Pins = append([]license.Pin{provider}, pins...)
		declarationPin := provider
		declarationPin.Role = "declaration"
		if right.DeclarationScope == license.ServerScope && serverPin != nil {
			declarationPin = *serverPin
		}
		if right.DeclarationScope != license.ServerScope {
			right.Pins = append(right.Pins, declarationPin)
		}
		right.Attribution = &provenance.Attribution
		right.FreeSource = provenance.FreeSource
		right.Transformations = append([]string{}, provenance.Transformations...)
		out = append(out, *right)
	}
	decorate(db)
	for _, name := range names {
		identity := ctx.Source
		identity.Recordset = name
		var raw *license.Declaration
		if d, ok := p.Recordsets[name]; ok {
			raw = &d
		}
		// The verified effective database declaration guarantees a nonnil
		// inherited result; overrides were validated before any evidence is emitted.
		right, _ := license.Resolve(identity, server, p.Database, raw)
		decorate(right)
	}
	out, _, err = license.Inventory(out)
	return out, err
}

// ServerIdentity verifies the author artifact pin before reading its identity.
func ServerIdentity(data []byte, ref Reference) (string, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != ref.Bytes || hex.EncodeToString(sum[:]) != ref.SHA256 {
		return "", fmt.Errorf("server byte count or SHA256 mismatch")
	}
	var doc map[string]json.RawMessage
	if err := Decode(data, &doc); err != nil {
		return "", err
	}
	var id string
	if err := json.Unmarshal(doc["id"], &id); err != nil {
		return "", err
	}
	return id, nil
}
func DescriptorServerID(data []byte) string {
	var doc map[string]json.RawMessage
	if err := Decode(data, &doc); err != nil {
		return ""
	}
	var id string
	_ = json.Unmarshal(doc["serverId"], &id)
	return id
}

// The authorship artifact may link to child databases, but never pins their
// bytes/revisions: pinned children would create a cycle back to this artifact.
func validateServerShape(doc map[string]json.RawMessage) error {
	allowed := map[string]bool{"format": true, "id": true, "title": true, "description": true, "homepage": true, "apiUrl": true, "databases": true, "licences": true, "data_rights": true, "schemaUrl": true}
	for key, raw := range doc {
		if !allowed[key] {
			return fmt.Errorf("unknown server descriptor key")
		}
		if key == "title" || key == "description" {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil || strings.TrimSpace(s) == "" {
				return fmt.Errorf("server descriptor text must be nonblank")
			}
		}
		if key == "homepage" || key == "apiUrl" || key == "schemaUrl" {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return err
			}
			if _, err := rules.ParsePublicHTTPSURL(s); err != nil {
				return fmt.Errorf("invalid server descriptor URL")
			}
		}
	}
	if raw, ok := doc["databases"]; ok {
		var databases []map[string]json.RawMessage
		if err := Decode(raw, &databases); err != nil || databases == nil {
			return fmt.Errorf("server databases must be an array")
		}
		for _, db := range databases {
			if len(db) != 5 {
				return fmt.Errorf("server child database links require five existing fields, without pins")
			}
			for _, key := range []string{"id", "localId", "serverDbBaseUrl", "manifestUrl", "apiUrl"} {
				var s string
				if err := json.Unmarshal(db[key], &s); err != nil {
					return fmt.Errorf("invalid child database link")
				}
				if key == "localId" {
					if !rules.IsLocalID(s) {
						return fmt.Errorf("invalid child localId")
					}
				} else if _, err := rules.ParsePublicHTTPSURL(s); err != nil {
					return fmt.Errorf("invalid child database URL")
				}
			}
		}
	}
	return nil
}
