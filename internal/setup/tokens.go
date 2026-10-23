package setup

import "fmt"

// Token scopes: the capability sets `ovdb token create --scope` names.
const (
	ScopeReadOnly  = "read-only"
	ScopeReadWrite = "read-write"
	ScopeCreateDB  = "create-db"
)

// ScopeCapabilities maps a token scope to its capabilities; "" is read-write.
func ScopeCapabilities(scope string) ([]string, error) {
	switch scope {
	case ScopeReadOnly:
		return []string{"records:read", "collections:read", "schema:read"}, nil
	case ScopeReadWrite, "":
		return []string{"records:read", "collections:read", "schema:read",
			"records:write", "records:delete"}, nil
	case ScopeCreateDB:
		// Deliberately NO records capabilities: a provisioning token can only
		// create databases; each created database gets its own scoped token.
		return []string{"databases:create"}, nil
	default:
		return nil, fmt.Errorf("unknown scope %q: use read-only, read-write or create-db", scope)
	}
}

// HasCreateDBCapability reports whether caps contains databases:create (a
// server-level token needs no database).
func HasCreateDBCapability(caps []string) bool {
	for _, c := range caps {
		if c == "databases:create" {
			return true
		}
	}
	return false
}
