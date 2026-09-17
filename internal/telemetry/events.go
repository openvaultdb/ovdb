// Package telemetry is ovdb's opt-in usage statistics (spec/features/
// telemetry-consent, decision 0009): the closed event set, the consent
// state stored in config.yaml, the forced-off conditions evaluated in the
// sending process, channel detection and a small synchronous PostHog (EU)
// batch sender. Nothing is sent unless a person turned it on.
//
// Every outbound telemetry request originates here. Events are values with
// unexported fields, built only by the constructors below, which map every
// input onto an allowlisted enum, boolean or number — so no database id,
// path, URL, query, error message or other user-entered text can reach the
// wire (REQ:closed-event-set, REQ:never-collected).
package telemetry

import (
	"regexp"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/strongo/cli-helpers/skillsync/cobracmd"

	"github.com/openvaultdb/ovdb/internal/envelope"
)

// Name is one event of the closed set.
type Name string

// The closed event set (REQ:closed-event-set). Adding one is a spec change.
const (
	OnboardingStarted         Name = "onboarding_started"
	OnboardingOptionSelected  Name = "onboarding_option_selected"
	EngineSelected            Name = "engine_selected"
	DatabaseCreated           Name = "database_created"
	ExistingDatabaseConnected Name = "existing_database_connected"
	ServerStarted             Name = "server_started"
	DemoInstalled             Name = "demo_installed"
	DemoOpened                Name = "demo_opened"
	SkillInstalled            Name = "skill_installed"
	ExploreDataSelected       Name = "explore_data_selected"
	TelemetryConsentChanged   Name = "telemetry_consent_changed"
	OnboardingCompleted       Name = "onboarding_completed"
	OnboardingError           Name = "onboarding_error"
)

// Names lists every event, in the spec's order.
var Names = []Name{
	OnboardingStarted, OnboardingOptionSelected, EngineSelected, DatabaseCreated,
	ExistingDatabaseConnected, ServerStarted, DemoInstalled, DemoOpened, SkillInstalled,
	ExploreDataSelected, TelemetryConsentChanged, OnboardingCompleted, OnboardingError,
}

// Other replaces any value outside an enum.
const Other = "other"

// Options are the onboarding choices, also used as error and completion
// steps.
var Options = []string{"demo", "create", "connect", "server", "browse", "explore", "skills", "settings"}

// Engines are the storage engine ids ovdb offers.
var Engines = []string{"ingitdb", "sqlite", "firestore", "mysql", "postgres"}

// Skills are the skills ovdb installs.
var Skills = []string{"openvaultdb", "todo-demo"}

// Harnesses are the skillsync harness ids skill installs target
// (cobracmd.DefaultHarnesses, the list `ovdb skills install --harness`
// accepts); a custom --dir is "other".
var Harnesses = func() []string {
	ids := make([]string, 0, len(cobracmd.DefaultHarnesses))
	for _, h := range cobracmd.DefaultHarnesses {
		ids = append(ids, h.ID)
	}
	return ids
}()

// Targets are the Explore data choices.
var Targets = []string{"datatug_cli", "datatug_web"}

// ConsentStates are the telemetry_consent_changed states (enabled only:
// turning it off sends nothing).
var ConsentStates = []string{StateEnabled}

// Event is one closed event. Build it with the constructors.
type Event struct {
	name                                                      Name
	option, engine, skill, harness, target, state, step, code string
	success, portIsDefault, alreadyInstalled, datatugFound    *bool
	durationMS                                                *int64
}

// Name is the event's name.
func (e Event) Name() Name { return e.name }

func enum(value string, allowed []string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, a := range allowed {
		if value == a {
			return a
		}
	}
	return Other
}

func flag(b bool) *bool { return &b }

func millis(d time.Duration) *int64 {
	ms := d.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	return &ms
}

func errorCode(code string) string {
	for _, c := range envelope.Codes {
		if code == string(c) {
			return code
		}
	}
	return string(envelope.Internal)
}

// NewOnboardingStarted is onboarding_started.
func NewOnboardingStarted() Event { return Event{name: OnboardingStarted} }

// NewOptionSelected is onboarding_option_selected.
func NewOptionSelected(option string) Event {
	return Event{name: OnboardingOptionSelected, option: enum(option, Options)}
}

// NewEngineSelected is engine_selected.
func NewEngineSelected(engine string) Event {
	return Event{name: EngineSelected, engine: enum(engine, Engines)}
}

// NewDatabaseCreated is database_created.
func NewDatabaseCreated(engine string, success bool, d time.Duration) Event {
	return Event{name: DatabaseCreated, engine: enum(engine, Engines), success: flag(success), durationMS: millis(d)}
}

// NewDatabaseConnected is existing_database_connected.
func NewDatabaseConnected(engine string, success bool, d time.Duration) Event {
	return Event{name: ExistingDatabaseConnected, engine: enum(engine, Engines), success: flag(success), durationMS: millis(d)}
}

// NewServerStarted is server_started.
func NewServerStarted(success, portIsDefault bool, d time.Duration) Event {
	return Event{name: ServerStarted, success: flag(success), portIsDefault: flag(portIsDefault), durationMS: millis(d)}
}

// NewDemoInstalled is demo_installed.
func NewDemoInstalled(success, alreadyInstalled bool) Event {
	return Event{name: DemoInstalled, success: flag(success), alreadyInstalled: flag(alreadyInstalled)}
}

// NewDemoOpened is demo_opened.
func NewDemoOpened(success bool) Event { return Event{name: DemoOpened, success: flag(success)} }

// NewSkillInstalled is skill_installed.
func NewSkillInstalled(skill, harness string, success bool) Event {
	return Event{name: SkillInstalled, skill: enum(skill, Skills), harness: enum(harness, Harnesses), success: flag(success)}
}

// NewExploreDataSelected is explore_data_selected.
func NewExploreDataSelected(target string, datatugFound bool) Event {
	return Event{name: ExploreDataSelected, target: enum(target, Targets), datatugFound: flag(datatugFound)}
}

// NewConsentEnabled is telemetry_consent_changed, which is only ever sent
// for turning telemetry on.
func NewConsentEnabled() Event {
	return Event{name: TelemetryConsentChanged, state: StateEnabled}
}

// NewOnboardingCompleted is onboarding_completed.
func NewOnboardingCompleted(step string) Event {
	return Event{name: OnboardingCompleted, step: enum(step, Options)}
}

// NewOnboardingError is onboarding_error; err's envelope code is the only
// thing taken from it.
func NewOnboardingError(step string, err error) Event {
	code := string(envelope.Internal)
	if e := envelope.As(err); e != nil {
		code = string(e.Code)
	}
	return Event{name: OnboardingError, step: enum(step, Options), code: errorCode(code)}
}

// Wire is an event as the web page posts it (POST /api/local/v1/telemetry/
// events). It is decoded, then rebuilt through the constructors, so its
// strings never reach the wire unchecked.
type Wire struct {
	Event            string `json:"event"`
	Option           string `json:"option,omitempty"`
	Engine           string `json:"engine,omitempty"`
	Skill            string `json:"skill,omitempty"`
	Harness          string `json:"harness,omitempty"`
	Target           string `json:"target,omitempty"`
	Step             string `json:"step,omitempty"`
	ErrorCode        string `json:"error_code,omitempty"`
	Success          bool   `json:"success,omitempty"`
	PortIsDefault    bool   `json:"port_is_default,omitempty"`
	AlreadyInstalled bool   `json:"already_installed,omitempty"`
	DataTugFound     bool   `json:"datatug_found,omitempty"`
	DurationMS       int64  `json:"duration_ms,omitempty"`
}

// FromWire rebuilds w through the constructors; false for an unknown event.
func FromWire(w Wire) (Event, bool) {
	d := time.Duration(w.DurationMS) * time.Millisecond
	switch Name(w.Event) {
	case OnboardingStarted:
		return NewOnboardingStarted(), true
	case OnboardingOptionSelected:
		return NewOptionSelected(w.Option), true
	case EngineSelected:
		return NewEngineSelected(w.Engine), true
	case DatabaseCreated:
		return NewDatabaseCreated(w.Engine, w.Success, d), true
	case ExistingDatabaseConnected:
		return NewDatabaseConnected(w.Engine, w.Success, d), true
	case ServerStarted:
		return NewServerStarted(w.Success, w.PortIsDefault, d), true
	case DemoInstalled:
		return NewDemoInstalled(w.Success, w.AlreadyInstalled), true
	case DemoOpened:
		return NewDemoOpened(w.Success), true
	case SkillInstalled:
		return NewSkillInstalled(w.Skill, w.Harness, w.Success), true
	case ExploreDataSelected:
		return NewExploreDataSelected(w.Target, w.DataTugFound), true
	case TelemetryConsentChanged:
		return NewConsentEnabled(), true
	case OnboardingCompleted:
		return NewOnboardingCompleted(w.Step), true
	case OnboardingError:
		return Event{name: OnboardingError, step: enum(w.Step, Options), code: errorCode(w.ErrorCode)}, true
	}
	return Event{}, false
}

// IPPlaceholder is sent as $ip on every event: PostHog records a property
// $ip in place of the connection's address (posthog.com tutorials,
// web-redact-properties "Hiding customer IP address"). The address still
// reaches PostHog's servers; the project's "Discard client IP data" setting
// keeps it out of stored events (a release precondition).
const IPPlaceholder = "0.0.0.0"

// Meta is what every event carries besides its own properties.
type Meta struct {
	Channel   Channel
	Version   string
	InstallID string
}

var (
	semverPattern    = regexp.MustCompile(`^v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*)?)$`)
	installIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// Version is v as sent: a semver, or "dev" for anything else.
func Version(v string) string {
	if m := semverPattern.FindStringSubmatch(strings.TrimSpace(v)); m != nil {
		return m[1]
	}
	return "dev"
}

// ValidInstallID reports whether id is an install id ovdb generated.
func ValidInstallID(id string) bool { return installIDPattern.MatchString(id) }

// Properties is e's allowlisted property map, with meta. PostHog's
// distinct_id is the install id, and GeoIP enrichment is disabled per event.
func (e Event) Properties(meta Meta) map[string]any {
	channel := meta.Channel
	if !channel.valid() {
		channel = ChannelCLI
	}
	p := map[string]any{
		"channel":                 string(channel),
		"ovdb_version":            Version(meta.Version),
		"os":                      goruntime.GOOS,
		"arch":                    goruntime.GOARCH,
		"$geoip_disable":          true,
		"$ip":                     IPPlaceholder,
		"$process_person_profile": false,
	}
	if ValidInstallID(meta.InstallID) {
		p["install_id"] = meta.InstallID
		p["distinct_id"] = meta.InstallID
	}
	set := func(key, value string) {
		if value != "" {
			p[key] = value
		}
	}
	set("option", e.option)
	set("engine", e.engine)
	set("skill", e.skill)
	set("harness", e.harness)
	set("target", e.target)
	set("state", e.state)
	set("step", e.step)
	set("error_code", e.code)
	for key, value := range map[string]*bool{"success": e.success, "port_is_default": e.portIsDefault,
		"already_installed": e.alreadyInstalled, "datatug_found": e.datatugFound} {
		if value != nil {
			p[key] = *value
		}
	}
	if e.durationMS != nil {
		p["duration_ms"] = *e.durationMS
	}
	return p
}
