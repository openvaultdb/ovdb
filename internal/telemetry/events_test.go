package telemetry_test

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// Hostile inputs from AC:allowlist-enforced, fed into every string a
// constructor or the web page's wire format accepts.
var hostile = []string{
	"secret-db",
	"/home/ann/private",
	"postgres://ann:hunter2@db.internal:5432/payroll?sslmode=disable",
	"open /home/ann/private/notes.sqlite: permission denied",
	`SELECT * FROM "salaries"`,
	"ann@example.com",
	"github.com/ann/private-repo",
	"ingitdb\n/home/ann",
	"DEMO ",
}

var commonKeys = []string{"$ip", "channel", "ovdb_version", "os", "arch", "install_id", "distinct_id", "$geoip_disable", "$process_person_profile"}

var eventKeys = map[telemetry.Name][]string{
	telemetry.OnboardingStarted:         {},
	telemetry.OnboardingOptionSelected:  {"option"},
	telemetry.EngineSelected:            {"engine"},
	telemetry.DatabaseCreated:           {"engine", "success", "duration_ms"},
	telemetry.ExistingDatabaseConnected: {"engine", "success", "duration_ms"},
	telemetry.ServerStarted:             {"success", "port_is_default", "duration_ms"},
	telemetry.DemoInstalled:             {"success", "already_installed"},
	telemetry.DemoOpened:                {"success"},
	telemetry.SkillInstalled:            {"skill", "harness", "success"},
	telemetry.ExploreDataSelected:       {"target", "datatug_found"},
	telemetry.TelemetryConsentChanged:   {"state"},
	telemetry.OnboardingCompleted:       {"step"},
	telemetry.OnboardingError:           {"step", "error_code"},
}

func codes() []string {
	out := []string{}
	for _, c := range envelope.Codes {
		out = append(out, string(c))
	}
	return out
}

// enums is the allowed value set per string property.
var enums = map[string][]string{
	"option":     append(slices.Clone(telemetry.Options), telemetry.Other),
	"step":       append(slices.Clone(telemetry.Options), telemetry.Other),
	"engine":     append(slices.Clone(telemetry.Engines), telemetry.Other),
	"skill":      append(slices.Clone(telemetry.Skills), telemetry.Other),
	"harness":    append(slices.Clone(telemetry.Harnesses), telemetry.Other),
	"target":     append(slices.Clone(telemetry.Targets), telemetry.Other),
	"state":      telemetry.ConsentStates,
	"error_code": codes(),
	"channel":    {"cli", "tui", "web", "agent"},
	"os":         {"linux", "darwin", "windows", "freebsd", "openbsd", "netbsd"},
	"arch":       {"amd64", "arm64", "386", "arm"},
}

var (
	versionValue   = regexp.MustCompile(`^(dev|\d+\.\d+\.\d+(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?)$`)
	installIDValue = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// constructors registers every exported function of package telemetry that
// returns an Event; TestEveryConstructorIsRegistered fails when one is
// missing, so a new event or constructor cannot skip the allowlist test.
var constructors = map[string]any{
	"NewOnboardingStarted":   telemetry.NewOnboardingStarted,
	"NewOptionSelected":      telemetry.NewOptionSelected,
	"NewEngineSelected":      telemetry.NewEngineSelected,
	"NewDatabaseCreated":     telemetry.NewDatabaseCreated,
	"NewDatabaseConnected":   telemetry.NewDatabaseConnected,
	"NewServerStarted":       telemetry.NewServerStarted,
	"NewDemoInstalled":       telemetry.NewDemoInstalled,
	"NewDemoOpened":          telemetry.NewDemoOpened,
	"NewSkillInstalled":      telemetry.NewSkillInstalled,
	"NewExploreDataSelected": telemetry.NewExploreDataSelected,
	"NewConsentEnabled":      telemetry.NewConsentEnabled,
	"NewOnboardingCompleted": telemetry.NewOnboardingCompleted,
	"NewOnboardingError":     telemetry.NewOnboardingError,
	"FromWire":               telemetry.FromWire,
}

// TestEveryConstructorIsRegistered reads the package source for exported
// functions whose first result is Event (review F9).
func TestEveryConstructorIsRegistered(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || fn.Type.Results == nil {
				continue
			}
			if ident, ok := fn.Type.Results.List[0].Type.(*ast.Ident); !ok || ident.Name != "Event" {
				continue
			}
			found++
			if _, ok := constructors[fn.Name.Name]; !ok {
				t.Errorf("%s returns Event but is not in constructors: the allowlist test would skip it", fn.Name.Name)
			}
		}
	}
	if found != len(constructors) {
		t.Errorf("source has %d Event constructors, registry %d", found, len(constructors))
	}
}

// everyEvent calls every registered constructor by reflection with input
// in every string (including every string field of a Wire, once per event
// name), both booleans, a negative duration and an error carrying input.
func everyEvent(input string) []telemetry.Event {
	var events []telemetry.Event
	for name, constructor := range constructors {
		fn := reflect.ValueOf(constructor)
		for _, flag := range []bool{true, false} {
			for _, wireName := range wireNames(fn.Type()) {
				args := make([]reflect.Value, fn.Type().NumIn())
				for i := range args {
					args[i] = argument(fn.Type().In(i), input, flag, wireName)
				}
				out := fn.Call(args)
				if len(out) == 2 && !out[1].Bool() {
					panic(name + " refused " + wireName)
				}
				events = append(events, out[0].Interface().(telemetry.Event))
			}
		}
	}
	return events
}

var (
	durationType = reflect.TypeOf(time.Duration(0))
	errorType    = reflect.TypeOf((*error)(nil)).Elem()
	wireType     = reflect.TypeOf(telemetry.Wire{})
)

// wireNames is every event name for a function taking a Wire, else one
// empty entry.
func wireNames(fn reflect.Type) []string {
	for i := 0; i < fn.NumIn(); i++ {
		if fn.In(i) == wireType {
			names := []string{}
			for _, name := range telemetry.Names {
				names = append(names, string(name))
			}
			return names
		}
	}
	return []string{""}
}

func argument(typ reflect.Type, input string, flag bool, wireName string) reflect.Value {
	switch {
	case typ == durationType:
		return reflect.ValueOf(-5 * time.Second)
	case typ == errorType:
		if flag {
			return reflect.ValueOf(error(envelope.New(envelope.Code(input), input).WithReason(input)))
		}
		return reflect.ValueOf(errors.New(input))
	case typ == wireType:
		w := reflect.New(wireType).Elem()
		for i := 0; i < w.NumField(); i++ {
			switch w.Field(i).Kind() {
			case reflect.String:
				w.Field(i).SetString(input)
			case reflect.Bool:
				w.Field(i).SetBool(flag)
			case reflect.Int64:
				w.Field(i).SetInt(-1)
			}
		}
		w.FieldByName("Event").SetString(wireName)
		return w
	case typ.Kind() == reflect.String:
		return reflect.ValueOf(input).Convert(typ)
	case typ.Kind() == reflect.Bool:
		return reflect.ValueOf(flag)
	}
	panic("no hostile argument for " + typ.String())
}

func TestEveryEventCarriesOnlyAllowlistedKeysAndValues(t *testing.T) {
	seen := map[telemetry.Name]bool{}
	for _, input := range hostile {
		for _, meta := range []telemetry.Meta{
			{Channel: telemetry.Channel(input), Version: input, InstallID: input},
			{Channel: telemetry.ChannelWeb, Version: "v1.2.3-rc.1", InstallID: telemetry.NewInstallID()},
		} {
			events := everyEvent(input)
			payload := telemetry.Payload("phc_test", events, meta)
			for _, secret := range hostile {
				// "DEMO " normalises to nothing it contains; every other
				// hostile string must be absent entirely.
				if secret != "DEMO " && strings.Contains(string(payload), secret) {
					t.Fatalf("payload contains %q: %s", secret, payload)
				}
			}
			var body struct {
				APIKey string `json:"api_key"`
				Batch  []struct {
					Event      string         `json:"event"`
					Properties map[string]any `json:"properties"`
				} `json:"batch"`
			}
			if err := json.Unmarshal(payload, &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Batch) != len(events) {
				t.Fatalf("batch has %d events, want %d", len(body.Batch), len(events))
			}
			for _, e := range body.Batch {
				name := telemetry.Name(e.Event)
				allowed, ok := eventKeys[name]
				if !ok {
					t.Fatalf("event %q is not in the closed set", e.Event)
				}
				seen[name] = true
				for key, value := range e.Properties {
					if !slices.Contains(commonKeys, key) && !slices.Contains(allowed, key) {
						t.Errorf("%s: key %q is not allowlisted", name, key)
					}
					checkValue(t, name, key, value)
				}
				if e.Properties["$geoip_disable"] != true {
					t.Errorf("%s: GeoIP enrichment not disabled", name)
				}
			}
		}
	}
	for _, name := range telemetry.Names {
		if !seen[name] {
			t.Errorf("event %s never marshalled", name)
		}
	}
}

func checkValue(t *testing.T, name telemetry.Name, key string, value any) {
	t.Helper()
	switch key {
	case "success", "port_is_default", "already_installed", "datatug_found", "$geoip_disable", "$process_person_profile":
		if _, ok := value.(bool); !ok {
			t.Errorf("%s.%s = %v, want a boolean", name, key, value)
		}
	case "duration_ms":
		if n, ok := value.(float64); !ok || n < 0 || n != float64(int64(n)) {
			t.Errorf("%s.%s = %v, want a non-negative integer", name, key, value)
		}
	case "$ip":
		if value != telemetry.IPPlaceholder {
			t.Errorf("%s.$ip = %v, want the placeholder", name, value)
		}
	case "ovdb_version":
		if s, _ := value.(string); !versionValue.MatchString(s) {
			t.Errorf("%s.%s = %v, want a semver or dev", name, key, value)
		}
	case "install_id", "distinct_id":
		if s, _ := value.(string); !installIDValue.MatchString(s) {
			t.Errorf("%s.%s = %v, want an install id", name, key, value)
		}
	default:
		s, ok := value.(string)
		if !ok || !slices.Contains(enums[key], s) {
			t.Errorf("%s.%s = %v, not an allowlisted enum value", name, key, value)
		}
	}
}

func TestEnumsKeepKnownValues(t *testing.T) {
	p := telemetry.NewDatabaseCreated("SQLite", true, 42*time.Millisecond).Properties(telemetry.Meta{Channel: telemetry.ChannelAgent, Version: "0.4.1"})
	if p["engine"] != "sqlite" || p["success"] != true || p["duration_ms"] != int64(42) || p["channel"] != "agent" || p["ovdb_version"] != "0.4.1" {
		t.Fatalf("properties = %v", p)
	}
	if _, ok := p["install_id"]; ok {
		t.Fatal("install_id without a valid id")
	}
	e := telemetry.NewOnboardingError("create", envelope.New(envelope.PortInUse, "Port 6832 is in use by /home/ann"))
	if p := e.Properties(telemetry.Meta{}); p["error_code"] != "port_in_use" || p["step"] != "create" {
		t.Fatalf("error properties = %v", p)
	}
	if _, ok := telemetry.FromWire(telemetry.Wire{Event: "database_deleted"}); ok {
		t.Fatal("unknown wire event accepted")
	}
}
