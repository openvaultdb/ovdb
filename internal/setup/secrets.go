package setup

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"

	"github.com/openvaultdb/ovdb/internal/redact"
)

// minMaskedLength is the shortest variable value masked verbatim: shorter
// values would mask ordinary words in a message, and are no secret.
const minMaskedLength = 4

// EnvironmentNames are the environment variables m takes values from: every
// *_env field (dsn_env, token_env, …) and the defaults that apply when one
// is left out.
func EnvironmentNames(m *manifest.Manifest) []string {
	if m == nil {
		return nil
	}
	names := map[string]bool{}
	collectEnvFields(reflect.ValueOf(m), names)
	switch {
	case m.Storage.Engine == EnginePostgres:
		names[m.Storage.Postgres.DSNEnvVar()] = true
	case m.Storage.Engine == EngineMySQL:
		names[m.Storage.MySQL.DSNEnvVar()] = true
	case m.Storage.Engine == EngineInGitDB && m.Storage.InGitDB != nil && m.Storage.InGitDB.GitHub != nil:
		names[m.Storage.InGitDB.GitHub.TokenEnvVar()] = true
	}
	out := make([]string, 0, len(names))
	for name := range names {
		if name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// collectEnvFields adds the values of string fields whose YAML key ends in
// _env, anywhere in v.
func collectEnvFields(v reflect.Value, names map[string]bool) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			field := v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			key, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
			if strings.HasSuffix(key, "_env") && field.Type.Kind() == reflect.String {
				names[v.Field(i).String()] = true
				continue
			}
			collectEnvFields(v.Field(i), names)
		}
	case reflect.Map, reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Map {
			for _, key := range v.MapKeys() {
				collectEnvFields(v.MapIndex(key), names)
			}
			return
		}
		for i := range v.Len() {
			collectEnvFields(v.Index(i), names)
		}
	}
}

// EnvironmentValues are the values of m's named variables in getenv, the
// secrets a message about m must never show.
func EnvironmentValues(m *manifest.Manifest, getenv func(string) string) []string {
	var values []string
	for _, name := range EnvironmentNames(m) {
		if value := getenv(name); value != "" {
			values = append(values, value)
		}
	}
	return values
}

// maskValues replaces every occurrence of each value in text, as is and as
// Go quotes it, with redact.Mask; longest first, so one value inside another
// leaves no part behind.
func maskValues(text string, values []string) string {
	sorted := append([]string(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, value := range sorted {
		if len(value) < minMaskedLength {
			continue
		}
		quoted := strconv.Quote(value)
		text = strings.ReplaceAll(text, quoted[1:len(quoted)-1], redact.Mask)
		text = strings.ReplaceAll(text, value, redact.Mask)
	}
	return text
}
