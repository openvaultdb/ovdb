package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/strongo/cli-helpers/cliinstall/cobracmd"
	"github.com/strongo/cli-helpers/selfupdate"
	selfcliui "github.com/strongo/cli-helpers/selfupdate/cliui"
)

// cli-helpers v0.26.0 classifies a copy of ovdb inside a directory the
// operating system's package manager owns (/usr/bin, /bin, /usr/sbin,
// /nix/store, ... on macOS and Linux) as managed by "the system package
// manager": self-update and upgrade redirect to it with a hint instead of
// replacing the binary in place, which at v0.21.0 they did ("manual"). The
// tests below run that through ovdb's own configuration and commands with no
// real executable, PATH or network, because installing ovdb under /usr/bin to
// see it needs root; what they do not run is a binary actually located there.

// systemPackageManagerName is the name the library gives that manager.
const systemPackageManagerName = "the system package manager"

// skipWithoutPOSIXSystemDirs skips on Windows, where the system directories
// are %SystemRoot% and %ProgramFiles%, not /usr/bin.
func skipWithoutPOSIXSystemDirs(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the system package directories on Windows are %SystemRoot% and %ProgramFiles%")
	}
}

func TestClassifyOvdbInSystemPackageDirectories(t *testing.T) {
	skipWithoutPOSIXSystemDirs(t)
	managers := newSelfUpdateConfig("1.0.0").Managers
	for path, want := range map[string]selfupdate.InstallMethod{
		"/usr/bin/ovdb":                       selfupdate.Managed,
		"/usr/sbin/ovdb":                      selfupdate.Managed,
		"/bin/ovdb":                           selfupdate.Managed,
		"/nix/store/abc123-ovdb-1.0/bin/ovdb": selfupdate.Managed,
		// Where a person puts a binary themselves stays eligible for self-update.
		"/usr/local/bin/ovdb":       selfupdate.Manual,
		"/home/someone/go/bin/ovdb": selfupdate.Manual,
		"/home/someone/bin/ovdb":    selfupdate.Manual,
		// A sibling that only starts with the same characters is not inside.
		"/usr/binx/ovdb": selfupdate.Ambiguous,
	} {
		detection := selfupdate.Classify(path, managers)
		if detection.Method != want {
			t.Errorf("Classify(%s) = %s, want %s", path, detection.Method, want)
			continue
		}
		if want != selfupdate.Managed {
			continue
		}
		if detection.Manager == nil || detection.Manager.Name != systemPackageManagerName ||
			detection.Manager.UpgradeHint == "" || detection.Manager.UpgradeCommand != "" || detection.Manager.CanExecuteUpgrade() {
			t.Errorf("Classify(%s).Manager = %+v, want the redirect-only system package manager with a hint and no command", path, detection.Manager)
		}
	}
	// The Homebrew cask ovdb ships through still wins over the system check.
	if brew := selfupdate.Classify("/opt/homebrew/Caskroom/ovdb/1.0.0/ovdb", managers); brew.Method != selfupdate.Managed || brew.Manager == nil || brew.Manager.Name != "Homebrew" {
		t.Errorf("Homebrew copy = %+v", brew)
	}
}

// releasesServer answers every request with one newer release and counts what
// was asked for, so a test can see that nothing was downloaded.
func releasesServer(t *testing.T, latest string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte(`[{"tag_name":"` + latest + `","prerelease":false,"draft":false}]`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

// self-update's own library calls, with the detection the running binary would
// get under /usr/bin: the update is redirected, nothing is downloaded, and the
// text and JSON outputs carry the hint, not an empty "Run:" command.
func TestSelfUpdateOfASystemPackageCopyRedirects(t *testing.T) {
	skipWithoutPOSIXSystemDirs(t)
	srv, requested := releasesServer(t, "v1.1.0")
	cfg := newSelfUpdateConfig("1.0.0")
	cfg.ReleasesAPIURL, cfg.HTTPClient = srv.URL, srv.Client()
	detection := selfupdate.Classify("/usr/bin/ovdb", cfg.Managers)

	outcome, err := cfg.UpdateAt(context.Background(), detection, selfupdate.Options{
		Confirm: func(string) (bool, error) { t.Error("a redirect asked for confirmation"); return false, nil },
	})
	if err != nil || outcome.Action != selfupdate.ActionRedirected {
		t.Fatalf("UpdateAt = %+v, %v; want a redirect", outcome, err)
	}
	for _, path := range requested() {
		if strings.Contains(path, "/download/") || strings.HasSuffix(path, ".tar.gz") || strings.HasSuffix(path, "checksums.txt") {
			t.Errorf("a redirect downloaded %s", path)
		}
	}

	var text bytes.Buffer
	selfcliui.WriteOutcome(&text, &text, cfg, outcome)
	if got := text.String(); !strings.HasPrefix(got, "ovdb is managed by the system package manager. Update it with ") || strings.Contains(got, "Run:") {
		t.Errorf("text = %q", got)
	}

	var jsonOut bytes.Buffer
	if err := selfcliui.WriteOutcomeJSON(&jsonOut, outcome); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(jsonOut.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document["action"] != "redirected" || document["manager"] != systemPackageManagerName || document["hint"] == "" || document["hint"] == nil {
		t.Errorf("JSON = %s", jsonOut.String())
	}
	if _, has := document["command"]; has {
		t.Errorf("JSON has a command for a manager that has none: %s", jsonOut.String())
	}

	// self-update --check: the verdict says an update exists, and the next step
	// and the JSON name the hint (upgrade_hint) rather than a command.
	result, err := cfg.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var next bytes.Buffer
	selfcliui.WriteNextStep(&next, cfg, detection, "ovdb self-update")
	if got := next.String(); !strings.HasPrefix(got, "ovdb was installed via the system package manager. Update it with ") || strings.Contains(got, "Run the following") {
		t.Errorf("next step = %q", got)
	}
	var check bytes.Buffer
	if err := selfcliui.WriteCheckJSON(&check, cfg, result, detection); err != nil {
		t.Fatal(err)
	}
	document = nil
	if err := json.Unmarshal(check.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document["install_method"] != "managed" || document["manager"] != systemPackageManagerName || document["upgrade_hint"] == nil || document["upgrade_command"] != nil {
		t.Errorf("check JSON = %s", check.String())
	}
}

// upgrade ovdb on a copy under /usr/bin, through ovdb's own upgrade command:
// the report says it is managed by the system package manager and shows the
// hint (text and --format json), applying it redirects and downloads nothing,
// and --check keeps ovdb's exit code 1 for an available upgrade.
func TestUpgradeOfASystemPackageCopyRedirects(t *testing.T) {
	skipWithoutPOSIXSystemDirs(t)
	run := func(t *testing.T, args ...string) (stdout string, requested []string, err error) {
		t.Helper()
		srv, asked := releasesServer(t, "v1.1.0")
		options := upgradeOptions("1.0.0")
		options.HostConfig.ReleasesAPIURL, options.HostConfig.HTTPClient = srv.URL, srv.Client()
		options.Env = fakeUpgradeEnv()
		options.Interactive = func() bool { return false }
		options.DetectHost = func() (selfupdate.Detection, error) {
			return selfupdate.Classify("/usr/bin/ovdb", options.HostConfig.Managers), nil
		}
		cmd := cobracmd.NewUpgrade(options)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err = cmd.Execute()
		return out.String(), asked(), err
	}

	text, _, err := run(t, "ovdb", "--check")
	if err == nil || commandExitCode(err) != 1 || !strings.Contains(err.Error(), "update available (1.0.0 -> 1.1.0)") {
		t.Errorf("--check err = %v, want the exit-1 finding of an available update", err)
	}
	if !strings.Contains(text, "managed by the system package manager — update it with ") || strings.Contains(text, "run: ") {
		t.Errorf("--check text = %q", text)
	}

	jsonText, _, _ := run(t, "ovdb", "--check", "--format", "json")
	if !strings.Contains(jsonText, `"hint":"`) || strings.Contains(jsonText, `"command":`) || !strings.Contains(jsonText, `"action":"redirected"`) ||
		!strings.Contains(jsonText, `"install_method":"managed"`) || !strings.Contains(jsonText, `"manager":"`+systemPackageManagerName+`"`) {
		t.Errorf("--check JSON = %q", jsonText)
	}

	applied, requested, err := run(t, "ovdb", "--yes")
	if err != nil {
		t.Fatalf("upgrade ovdb --yes: %v\n%s", err, applied)
	}
	if !strings.Contains(applied, "managed by the system package manager") {
		t.Errorf("--yes output = %q, want the redirect", applied)
	}
	for _, path := range requested {
		if strings.Contains(path, "/download/") || strings.HasSuffix(path, ".tar.gz") {
			t.Errorf("upgrade of a system-managed copy downloaded %s", path)
		}
	}
}
