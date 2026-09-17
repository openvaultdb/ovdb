// Package uicopy is the single source of every user-facing string ovdb's
// CLI, TUI and web console show. It embeds copy/en.json so a Go binary needs
// no separate copy file at runtime, and the Vite build in web/ imports the
// same en.json directly, so both surfaces render one catalogue.
//
// See spec/features/configuration-parity#REQ:copy-catalogue.
package uicopy

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed en.json
var catalogueFS embed.FS

// catalogue maps a copy key to its English template. Templates may contain
// "{name}" placeholders, substituted by T.
var catalogue = mustLoadCatalogue()

func mustLoadCatalogue() map[string]string {
	data, err := catalogueFS.ReadFile("en.json")
	if err != nil {
		// en.json is embedded at compile time; a read failure here means the
		// build is broken, not that a caller passed bad input.
		panic(fmt.Sprintf("uicopy: reading embedded en.json: %v", err))
	}
	var out map[string]string
	if err := json.Unmarshal(data, &out); err != nil {
		panic(fmt.Sprintf("uicopy: parsing embedded en.json: %v", err))
	}
	return out
}

// T renders the copy catalogue entry named by key, replacing each
// "{name}" placeholder in the template with params[name]. A placeholder with
// no matching entry in params is left in place, and every extra entry in
// params is ignored, so callers may pass a shared params map.
//
// T panics when key is absent from copy/en.json. Every call site passes a
// string literal, so a missing key is a build-time defect: copy_ast_test.go
// walks the module for every "uicopy.T(\"...\")" literal and fails the build
// naming the missing key, so this panic should never fire outside that test
// demonstrating the failure.
func T(key string, params map[string]string) string {
	template, ok := catalogue[key]
	if !ok {
		panic(fmt.Sprintf("uicopy: missing copy key %q", key))
	}
	for name, value := range params {
		template = strings.ReplaceAll(template, "{"+name+"}", value)
	}
	return template
}
