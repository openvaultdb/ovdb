// Package skills is the AI agent skills service (spec/features/ai-agent-skills,
// capabilities 20 and 21): the two Agent Skills embedded from skills/, where
// each AI agent (harness) keeps them, and installing one skill with
// github.com/strongo/cli-helpers/skillsync.
//
// Each skill is its own skillsync bundle, PluginIdentity{openvaultdb, <skill>},
// under the shared CLI Identity{openvaultdb, ovdb}, so installing one never
// changes the other or skills other tools own (REQ:install-with-skillsync).
// Harness names, directories and discovery are cobracmd.DefaultHarnesses.
//
// Nothing here decides consent: callers write only after a person chose a
// skill and its targets (REQ:explicit-consent-to-install). Directories are
// resolved by whoever runs the interface — the CLI and TUI from their own
// environment, the server for the web console — and a server checks what a
// client sent with CheckTarget (REQ:install-targets-restricted).
package skills

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/strongo/cli-helpers/skillsync"
	"github.com/strongo/cli-helpers/skillsync/cobracmd"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/redact"
	embedded "github.com/openvaultdb/ovdb/skills"
)

// Skill ids, as `ovdb skills install <id>` names them.
const (
	Storage = "openvaultdb"
	Todo    = "todo-demo"
)

// Next actions presentations act on in place (envelope.Next.Action).
const (
	// ActionInstall opens the consent step for the skill whose id ends the
	// entry's command.
	ActionInstall = "install_skill"
	// ActionSkills opens AI agent skills.
	ActionSkills = "skills"
)

// content is the embedded skills; tests swap in an older release.
var content fs.FS = embedded.FS

// Target states: how an installed skill compares with this build's.
const (
	StateNotInstalled    = "not_installed"
	StateInstalled       = "installed"
	StateUpdateAvailable = "update_available"
)

// Publisher is the skillsync publisher of the CLI and of every bundle.
const Publisher = "openvaultdb"

// CLI is the skillsync identity of ovdb, recorded as each bundle's supplier.
var CLI = skillsync.Identity{Publisher: Publisher, Name: "ovdb"}

// Definition is one embedded skill.
type Definition struct {
	ID string
	// Dir is the skill's directory, in skills/ and in a harness's skills
	// directory.
	Dir        string
	nameKey    string
	purposeKey string
	exampleKey string
}

// Definitions are the skills, in the order interfaces list them.
var Definitions = []Definition{
	{ID: Storage, Dir: "openvaultdb", nameKey: "skills.storage.name", purposeKey: "skills.storage.purpose", exampleKey: "skills.storage.example"},
	{ID: Todo, Dir: "openvaultdb-todo-demo", nameKey: "skills.todo.name", purposeKey: "skills.todo.purpose", exampleKey: "skills.todo.example"},
}

// Find is the definition for id.
func Find(id string) (Definition, bool) {
	for _, d := range Definitions {
		if d.ID == id {
			return d, true
		}
	}
	return Definition{}, false
}

// Plugin is the skill's own skillsync identity.
func (d Definition) Plugin() skillsync.PluginIdentity {
	return skillsync.PluginIdentity{Publisher: Publisher, Name: d.ID}
}

// harnessNames are the people-facing names of cobracmd.DefaultHarnesses.
var harnessNames = map[string]string{
	"claude": "Claude Code", "cursor": "Cursor", "codex": "Codex", "deepseek": "DeepSeek",
	"agents": "Shared agents folder", "copilot": "GitHub Copilot", "gemini": "Gemini CLI",
	"antigravity": "Antigravity", "opencode": "OpenCode", "cline": "Cline", "roo": "Roo Code",
	"kiro": "Kiro", "windsurf": "Windsurf", "junie": "Junie",
}

// alwaysListed harnesses are shown even when not found, so a person sees
// where the common agents would get the skill (the example copy in
// REQ:skills-offered-at-the-right-moment); other harnesses appear once found.
var alwaysListed = []string{"claude", "codex"}

// HarnessName is harness id's people-facing name.
func HarnessName(id string) string {
	if name, ok := harnessNames[id]; ok {
		return name
	}
	return id
}

// Env is where one interface resolves harness directories: the person's home
// and environment variables of the process that runs the interface
// (local-server-and-web-console#REQ:client-values-and-mismatch).
type Env struct {
	Home   string
	Getenv func(string) string
}

// EnvFrom resolves the home from getenv (HOME, or USERPROFILE on Windows),
// falling back to os.UserHomeDir.
func EnvFrom(getenv func(string) string) (Env, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	home := getenv("HOME")
	if goruntime.GOOS == "windows" {
		home = getenv("USERPROFILE")
	}
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return Env{}, err
		}
	}
	home, err := filepath.Abs(home)
	return Env{Home: home, Getenv: getenv}, err
}

// Target is one place a skill can be installed: a harness's skills directory,
// or, for the CLI's --dir, any directory under the home.
type Target struct {
	Harness string `json:"harness,omitempty"`
	Name    string `json:"name"`
	// SkillsDir is the harness's skills directory.
	SkillsDir string `json:"skills_dir"`
	// Dir is where this skill is, or would be, installed.
	Dir       string `json:"dir"`
	Detected  bool   `json:"detected"`
	Installed bool   `json:"installed"`
	// State is StateNotInstalled, StateInstalled or StateUpdateAvailable.
	State string `json:"state"`
}

// Skill is one skill in the skills document.
type Skill struct {
	ID      string `json:"id"`
	Dir     string `json:"dir"`
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	Example string `json:"example"`
	// Command installs it; interfaces show it next to Install.
	Command string   `json:"command"`
	Targets []Target `json:"targets"`
	// InstalledFor lists the harnesses it is installed for.
	InstalledFor []string `json:"installed_for"`
}

// Document is the body of GET /api/local/v1/skills and `ovdb skills list
// --json`.
type Document struct {
	Schema int             `json:"schema"`
	Skills []Skill         `json:"skills"`
	Next   []envelope.Next `json:"next"`
}

// Harness is the cobracmd harness named by id or one of its aliases.
func Harness(id string) (cobracmd.Harness, bool) { return harnessByID(id) }

func harnessByID(id string) (cobracmd.Harness, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, h := range cobracmd.DefaultHarnesses {
		if h.ID == id || slices.Contains(h.Aliases, id) {
			return h, true
		}
	}
	return cobracmd.Harness{}, false
}

func (e Env) target(h cobracmd.Harness, d Definition) Target {
	dir := h.SkillsDir(e.Home, e.Getenv)
	t := Target{Harness: h.ID, Name: HarnessName(h.ID), SkillsDir: dir, Dir: filepath.Join(dir, d.Dir),
		Detected: h.Present(e.Home, e.Getenv)}
	t.setState(stateOf(dir, d))
	return t
}

func (t *Target) setState(state string) {
	t.State, t.Installed = state, state != StateNotInstalled
}

// stateOf compares d in skillsDir with this build's copy through a skillsync
// dry run, which reads and never writes: unchanged is installed, an update
// is an older OVDB's copy.
func stateOf(skillsDir string, d Definition) string {
	if !installed(skillsDir, d) {
		return StateNotInstalled
	}
	cfg, err := Build{}.config(d)
	if err != nil {
		return StateInstalled
	}
	report, err := skillsync.Sync(context.Background(), cfg, skillsync.Options{Dir: skillsDir, DryRun: true})
	if err != nil {
		return StateInstalled
	}
	for _, change := range report.Changes {
		if change.Name == d.Dir && change.Action == skillsync.Updated {
			return StateUpdateAvailable
		}
	}
	return StateInstalled
}

// installed reports whether d is installed in skillsDir: skillsync records
// the skill's plugin and the skill's folder is there.
func installed(skillsDir string, d Definition) bool {
	status, err := skillsync.ReadStatus(skillsDir)
	if err != nil || !status.Installed {
		return false
	}
	if _, ok := status.Plugins[d.Plugin().String()]; !ok {
		return false
	}
	info, err := os.Stat(filepath.Join(skillsDir, d.Dir, "SKILL.md"))
	return err == nil && info.Mode().IsRegular()
}

// Targets are the harnesses shown for d: every one found, plus Claude Code and
// Codex, in cobracmd.DefaultHarnesses order.
func (e Env) Targets(d Definition) []Target {
	var targets []Target
	for _, h := range cobracmd.DefaultHarnesses {
		t := e.target(h, d)
		if t.Detected || t.Installed || slices.Contains(alwaysListed, h.ID) {
			targets = append(targets, t)
		}
	}
	return targets
}

// Describe is skill d with its targets.
func (e Env) Describe(d Definition) Skill {
	skill := Skill{ID: d.ID, Dir: d.Dir, Name: uicopy.T(d.nameKey, nil), Purpose: uicopy.T(d.purposeKey, nil),
		Example: uicopy.T(d.exampleKey, nil), Command: "ovdb skills install " + d.ID, Targets: e.Targets(d), InstalledFor: []string{}}
	for _, t := range skill.Targets {
		if t.Installed {
			skill.InstalledFor = append(skill.InstalledFor, t.Harness)
		}
	}
	return skill
}

// Inspect is the skills document for e (capability 20). It only reads.
func Inspect(e Env) Document {
	doc := Document{Schema: envelope.Schema, Next: []envelope.Next{}}
	for _, d := range Definitions {
		skill := e.Describe(d)
		doc.Skills = append(doc.Skills, skill)
		if len(skill.InstalledFor) == 0 {
			doc.Next = append(doc.Next, envelope.Next{Label: uicopy.T(installLabelKey(d.ID), nil), Command: skill.Command, Action: ActionInstall})
		}
	}
	return doc
}

func installLabelKey(id string) string {
	if id == Todo {
		return "skills.next.install_todo"
	}
	return "skills.next.install_storage"
}

// InstallNext is the next entry that offers installing skill id, for the
// Results of other actions (todo-demo#REQ:demo-next-actions).
func InstallNext(id string) envelope.Next {
	return envelope.Next{Label: uicopy.T(installLabelKey(id), nil), Command: "ovdb skills install " + id, Action: ActionInstall}
}

// Installed is the status document's skills field group: each skill with the
// harnesses it is installed for (first-run-onboarding#REQ:status-command).
type Installed struct {
	ID           string   `json:"id"`
	InstalledFor []string `json:"installed_for"`
	// UpdateAvailableFor lists the harnesses whose copy an older ovdb
	// installed; `ovdb skills install <id>` updates it.
	UpdateAvailableFor []string `json:"update_available_for"`
}

// Status lists every skill and where it is installed, reading only
// skillsync's markers.
func Status(e Env) []Installed {
	out := []Installed{}
	for _, d := range Definitions {
		entry := Installed{ID: d.ID, InstalledFor: []string{}, UpdateAvailableFor: []string{}}
		for _, h := range cobracmd.DefaultHarnesses {
			switch stateOf(h.SkillsDir(e.Home, e.Getenv), d) {
			case StateUpdateAvailable:
				entry.UpdateAvailableFor = append(entry.UpdateAvailableFor, h.ID)
				entry.InstalledFor = append(entry.InstalledFor, h.ID)
			case StateInstalled:
				entry.InstalledFor = append(entry.InstalledFor, h.ID)
			}
		}
		out = append(out, entry)
	}
	return out
}

// RequestTarget is one target an install request names: a harness id with the
// skills directory the client resolved, or, from the CLI's --dir, a directory
// alone.
type RequestTarget struct {
	Harness   string `json:"harness,omitempty"`
	SkillsDir string `json:"skills_dir"`
}

// InstallRequest is the body of POST /api/local/v1/skills/install. The web
// console sends Harnesses, which the server resolves itself; the CLI and TUI
// send Targets they resolved. A console session may not name a directory.
type InstallRequest struct {
	Skill     string          `json:"skill"`
	Harnesses []string        `json:"harnesses,omitempty"`
	Targets   []RequestTarget `json:"targets,omitempty"`
	DryRun    bool            `json:"dry_run,omitempty"`
}

// Outcome is what installing did in one target.
type Outcome struct {
	Target
	// Result is added, updated, unchanged or conflict (skillsync's actions).
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
}

// InstallDocument is the body of POST /api/local/v1/skills/install and the
// --json output of `ovdb skills install`.
type InstallDocument struct {
	Schema int    `json:"schema"`
	Skill  string `json:"skill"`
	Dir    string `json:"dir"`
	Name   string `json:"name"`
	DryRun bool   `json:"dry_run,omitempty"`
	// AlreadyUpToDate is set when no target changed.
	AlreadyUpToDate bool            `json:"already_up_to_date"`
	Outcomes        []Outcome       `json:"targets"`
	Next            []envelope.Next `json:"next"`
}

func installFailed(d Definition) string {
	return uicopy.T("skills.install.failed", map[string]string{"name": uicopy.T(d.nameKey, nil)})
}

// Resolve checks request's skill and turns what it names into targets:
// Targets as sent, Harnesses resolved in e. Without either it is every
// harness found, or Claude Code when none is (cobracmd's discovery).
func (e Env) Resolve(request InstallRequest) (Definition, []RequestTarget, error) {
	d, ok := Find(request.Skill)
	if !ok {
		return d, nil, UnknownSkill(request.Skill)
	}
	targets := slices.Clone(request.Targets)
	for _, raw := range request.Harnesses {
		for _, name := range strings.Split(raw, ",") {
			if strings.EqualFold(strings.TrimSpace(name), "all") {
				for _, h := range cobracmd.DefaultHarnesses {
					targets = append(targets, RequestTarget{Harness: h.ID, SkillsDir: h.SkillsDir(e.Home, e.Getenv)})
				}
				continue
			}
			h, ok := harnessByID(name)
			if !ok {
				return d, nil, unknownHarness(d, name)
			}
			targets = append(targets, RequestTarget{Harness: h.ID, SkillsDir: h.SkillsDir(e.Home, e.Getenv)})
		}
	}
	if len(request.Targets) == 0 && len(request.Harnesses) == 0 {
		for _, h := range cobracmd.DefaultHarnesses {
			if h.Present(e.Home, e.Getenv) {
				targets = append(targets, RequestTarget{Harness: h.ID, SkillsDir: h.SkillsDir(e.Home, e.Getenv)})
			}
		}
		if len(targets) == 0 {
			h := cobracmd.DefaultHarnesses[0]
			targets = append(targets, RequestTarget{Harness: h.ID, SkillsDir: h.SkillsDir(e.Home, e.Getenv)})
		}
	}
	// One target per directory, keeping the first.
	seen := map[string]bool{}
	return d, slices.DeleteFunc(targets, func(t RequestTarget) bool {
		key := filepath.Clean(t.SkillsDir)
		if seen[key] {
			return true
		}
		seen[key] = true
		return false
	}), nil
}

// UnknownSkill is the invalid_argument for a skill id that doesn't exist.
func UnknownSkill(id string) *envelope.Error {
	return envelope.New(envelope.InvalidArgument, uicopy.T("skills.install.failed_generic", nil)).
		WithReason(uicopy.T("skills.unknown", map[string]string{"skill": id})).
		WithNext(envelope.Next{Label: uicopy.T("skills.next.install_storage", nil), Command: "ovdb skills install " + Storage},
			envelope.Next{Label: uicopy.T("skills.next.install_todo", nil), Command: "ovdb skills install " + Todo})
}

func unknownHarness(d Definition, name string) *envelope.Error {
	ids := make([]string, len(cobracmd.DefaultHarnesses))
	for i, h := range cobracmd.DefaultHarnesses {
		ids[i] = h.ID
	}
	return envelope.New(envelope.InvalidArgument, installFailed(d)).
		WithReason(uicopy.T("skills.unknown_harness", map[string]string{"harness": name, "harnesses": strings.Join(ids, ", ")})).
		WithNext(envelope.Next{Label: uicopy.T("skills.next.list", nil), Command: "ovdb skills list"})
}

// CheckTarget is the server's check of a target a client resolved
// (REQ:install-targets-restricted): a harness target must match that
// harness's layout (<root>/<config folder>/skills, or <root>/skills for a
// harness whose config root a variable can move); a target without a harness,
// the CLI's --dir, must be under home.
func CheckTarget(d Definition, home string, t RequestTarget) error {
	dir := t.SkillsDir
	if dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return outsideLayout(d, dir)
	}
	if t.Harness == "" {
		return CheckUnderHome(d, home, dir)
	}
	h, ok := harnessByID(t.Harness)
	if !ok {
		return unknownHarness(d, t.Harness)
	}
	if filepath.Base(dir) != "skills" {
		return outsideLayout(d, dir)
	}
	root := filepath.Dir(dir)
	if h.ConfigEnv != "" || strings.HasSuffix(root, string(filepath.Separator)+h.ConfigRel) {
		return nil
	}
	return outsideLayout(d, dir)
}

func outsideLayout(d Definition, dir string) *envelope.Error {
	return envelope.New(envelope.InvalidArgument, installFailed(d)).
		WithReason(uicopy.T("skills.dir_not_harness", map[string]string{"path": dir})).
		WithNext(envelope.Next{Label: uicopy.T("skills.next.list", nil), Command: "ovdb skills list"})
}

// CheckUnderHome refuses a --dir that is not a directory inside home, so an
// install never writes to system or other people's folders.
func CheckUnderHome(d Definition, home, dir string) error {
	refused := envelope.New(envelope.InvalidArgument, installFailed(d)).
		WithReason(uicopy.T("skills.dir_outside_home", map[string]string{"path": dir, "home": home})).
		WithNext(envelope.Next{Label: uicopy.T("skills.next.harness", nil), Command: "ovdb skills install " + d.ID + " --harness claude"})
	if !filepath.IsAbs(dir) || home == "" {
		return refused
	}
	canonicalDir, err := skillsync.ValidateTarget(filepath.Clean(dir))
	if err != nil {
		return refused
	}
	canonicalHome, err := skillsync.ValidateTarget(home)
	if err != nil {
		canonicalHome = filepath.Clean(home)
	}
	rel, err := filepath.Rel(canonicalHome, canonicalDir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return refused
	}
	return nil
}

// describeRequest is the target t in e, for plans and results.
func (e Env) describeRequest(d Definition, t RequestTarget) Target {
	target := Target{Harness: t.Harness, Name: HarnessName(t.Harness), SkillsDir: t.SkillsDir, Dir: filepath.Join(t.SkillsDir, d.Dir)}
	target.setState(stateOf(t.SkillsDir, d))
	if t.Harness == "" {
		target.Name = t.SkillsDir
	}
	if info, err := os.Stat(filepath.Dir(t.SkillsDir)); err == nil && info.IsDir() {
		target.Detected = true
	}
	return target
}

// Plan describes targets for d without writing: what an interface shows
// before the person decides.
func (e Env) Plan(d Definition, targets []RequestTarget) []Target {
	out := make([]Target, len(targets))
	for i, t := range targets {
		out[i] = e.describeRequest(d, t)
	}
	return out
}

// Build is the ovdb build installing: its version and VCS revision.
type Build struct {
	Version  string
	Revision string
}

const unknownRevision = "0000000000000000000000000000000000000000"

func (b Build) config(d Definition) (skillsync.Config, error) {
	bundle, err := oneSkill(content, d.Dir)
	if err != nil {
		return skillsync.Config{}, err
	}
	digest, err := skillsync.Digest(bundle)
	if err != nil {
		return skillsync.Config{}, err
	}
	revision := b.Revision
	if revision == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					revision = setting.Value
				}
			}
		}
	}
	if len(revision) != 40 {
		revision = unknownRevision
	}
	version := strings.TrimPrefix(b.Version, "v")
	if _, err := skillsync.CompareVersions(version, version); err != nil {
		version = "0.0.0"
	}
	current := b.Version
	if _, err := skillsync.CompareVersions(current, current); err != nil {
		current = "dev"
	}
	embeddedBundle, err := skillsync.EmbeddedBundle(skillsync.BundleDescriptor{
		Plugin: d.Plugin(),
		Source: skillsync.Source{Repository: "github.com/openvaultdb/ovdb", Path: "skills/" + d.Dir, Revision: revision, Version: version, Digest: digest},
	}, bundle)
	if err != nil {
		return skillsync.Config{}, err
	}
	return skillsync.Config{CLI: CLI, CurrentVersion: current, Bundles: []skillsync.Bundle{embeddedBundle}}, nil
}

// Install installs skill d into each target with one skillsync.Sync per
// target and only d's bundle (capability 21). Targets must already be checked.
// Every target is attempted; a failure in any makes the whole install fail,
// naming each outcome.
func (b Build) Install(ctx context.Context, e Env, d Definition, targets []RequestTarget, dryRun bool) (InstallDocument, error) {
	doc := InstallDocument{Schema: envelope.Schema, Skill: d.ID, Dir: d.Dir, Name: uicopy.T(d.nameKey, nil), DryRun: dryRun, AlreadyUpToDate: true}
	cfg, err := b.config(d)
	if err != nil {
		return doc, envelope.New(envelope.Internal, installFailed(d)).WithReason(redact.String(err.Error()))
	}
	var failures []string
	for _, t := range targets {
		outcome := Outcome{Target: e.describeRequest(d, t)}
		report, err := skillsync.Sync(ctx, cfg, skillsync.Options{Dir: t.SkillsDir, DryRun: dryRun})
		switch {
		case err != nil:
			outcome.Result, outcome.Reason = string(skillsync.Conflict), redact.String(err.Error())
			failures = append(failures, uicopy.T("skills.install.target_failed", map[string]string{"path": outcome.Dir, "reason": outcome.Reason}))
		default:
			outcome.Result = string(skillsync.Unchanged)
			for _, change := range report.Changes {
				if change.Name == d.Dir {
					outcome.Result, outcome.Reason = string(change.Action), change.Reason
				}
			}
			if outcome.Result == string(skillsync.Conflict) {
				failures = append(failures, uicopy.T("skills.install.target_conflict", map[string]string{"path": outcome.Dir}))
			}
		}
		if outcome.Result != string(skillsync.Unchanged) {
			doc.AlreadyUpToDate = false
		}
		if !dryRun {
			outcome.setState(stateOf(t.SkillsDir, d))
		}
		doc.Outcomes = append(doc.Outcomes, outcome)
	}
	if len(failures) > 0 {
		return doc, envelope.New(envelope.StorageUnavailable, installFailed(d)).
			WithReason(strings.Join(failures, " ")).
			WithNext(envelope.Next{Label: uicopy.T("skills.next.list", nil), Command: "ovdb skills list"})
	}
	doc.Next = installedNext(d)
	return doc, nil
}

func installedNext(d Definition) []envelope.Next {
	if d.ID == Todo {
		return []envelope.Next{
			{Label: uicopy.T("skills.next.try_todo", nil)},
			{Label: uicopy.T("demo.next.open_app", nil), Command: "ovdb demo open", Action: "open_app"},
			{Label: uicopy.T("next.done", nil), Action: "done"},
		}
	}
	return []envelope.Next{
		{Label: uicopy.T("skills.next.try_storage", nil)},
		{Label: uicopy.T("skills.next.see_skills", nil), Command: "ovdb skills list", Action: ActionSkills},
		{Label: uicopy.T("next.done", nil), Action: "done"},
	}
}

// oneSkill is root with only directory name visible, so a bundle holds
// exactly one skill.
func oneSkill(root fs.FS, name string) (fs.FS, error) {
	if _, err := fs.Stat(root, name+"/SKILL.md"); err != nil {
		return nil, fmt.Errorf("embedded skill %s: %w", name, err)
	}
	return onlyDir{root: root, name: name}, nil
}

type onlyDir struct {
	root fs.FS
	name string
}

func (o onlyDir) visible(name string) bool {
	return name == "." || name == o.name || strings.HasPrefix(name, o.name+"/")
}

func (o onlyDir) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) || !o.visible(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if name != "." {
		return o.root.Open(name)
	}
	file, err := o.root.Open(name)
	if err != nil {
		return nil, err
	}
	return &rootDir{File: file, o: o}, nil
}

func (o onlyDir) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) || !o.visible(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	entries, err := fs.ReadDir(o.root, name)
	if err != nil || name != "." {
		return entries, err
	}
	return slices.DeleteFunc(entries, func(e fs.DirEntry) bool { return e.Name() != o.name }), nil
}

// rootDir is the root directory of an onlyDir, listing only its skill.
type rootDir struct {
	fs.File
	o    onlyDir
	read bool
}

func (r *rootDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if r.read {
		if n > 0 {
			return nil, io.EOF
		}
		return nil, nil
	}
	r.read = true
	return r.o.ReadDir(".")
}
