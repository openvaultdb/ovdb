package main

import (
	"os"
	"sort"
	"testing"

	"github.com/strongo/cli-helpers/cliinstall"
	"github.com/strongo/cli-helpers/selfupdate"
	"gopkg.in/yaml.v3"
)

// This file proves, offline, that ovdb's own .goreleaser.yaml still
// publishes exactly what the cliinstall catalog's "ovdb" entry expects: the
// same release repository, no tag prefix, GoReleaser's own archive-naming
// default (so the entry leaves AssetName nil), the flat "checksums.txt"
// name, the same supported platform set, and the same Homebrew cask token
// and operating systems (cli-install#req:catalog-identity-single-source):
// "Each consumer MUST carry an offline test asserting that its own
// .goreleaser.y*ml archive name template, checksum name template,
// platforms, release repository and tag prefix match its catalog entry, so
// drift fails that CLI's CI rather than a user's install."
//
// A mismatch here means someone changed ovdb's release naming, tag
// scheme or cask without updating strongo/cli-helpers' catalog_ovdb.go
// (or the other way around) — exactly the drift this test exists to catch
// before a user's `<other-cli> install ovdb` or `ovdb self-update`
// resolves a broken asset URL.

// goreleaserConfig is only the subset of .goreleaser.yaml this test needs;
// every other key is ignored by yaml.v3's default decoding.
type goreleaserConfig struct {
	Builds []struct {
		GOOS   []string `yaml:"goos"`
		GOARCH []string `yaml:"goarch"`
		Ignore []struct {
			GOOS   string `yaml:"goos"`
			GOARCH string `yaml:"goarch"`
		} `yaml:"ignore"`
	} `yaml:"builds"`
	Archives []struct {
		NameTemplate string `yaml:"name_template"`
	} `yaml:"archives"`
	Checksum struct {
		NameTemplate string `yaml:"name_template"`
	} `yaml:"checksum"`
	Release struct {
		GitHub struct {
			Owner string `yaml:"owner"`
			Name  string `yaml:"name"`
		} `yaml:"github"`
	} `yaml:"release"`
	HomebrewCasks []struct {
		Name       string `yaml:"name"`
		Repository struct {
			Owner string `yaml:"owner"`
			Name  string `yaml:"name"`
		} `yaml:"repository"`
	} `yaml:"homebrew_casks"`
}

func loadGoreleaserConfig(t *testing.T) goreleaserConfig {
	t.Helper()
	data, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	var cfg goreleaserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yaml: %v", err)
	}
	return cfg
}

// goreleaserPlatforms expands cfg's builds x goos/goarch matrix minus its
// ignore entries into the exact platform set GoReleaser publishes.
func goreleaserPlatforms(t *testing.T, cfg goreleaserConfig) map[selfupdate.Platform]bool {
	t.Helper()
	if len(cfg.Builds) != 1 {
		t.Fatalf(".goreleaser.yaml builds = %d entries, this test assumes exactly one", len(cfg.Builds))
	}
	build := cfg.Builds[0]
	ignored := make(map[selfupdate.Platform]bool, len(build.Ignore))
	for _, ig := range build.Ignore {
		ignored[selfupdate.Platform{GOOS: ig.GOOS, GOARCH: ig.GOARCH}] = true
	}
	platforms := make(map[selfupdate.Platform]bool)
	for _, goos := range build.GOOS {
		for _, goarch := range build.GOARCH {
			p := selfupdate.Platform{GOOS: goos, GOARCH: goarch}
			if ignored[p] {
				continue
			}
			platforms[p] = true
		}
	}
	return platforms
}

func TestOvdbCatalogEntryMatchesGoreleaser(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	entry, ok := cliinstall.ByID(ovdbCatalogID)
	if !ok {
		t.Fatalf("cliinstall.ByID(%q) not found", ovdbCatalogID)
	}

	t.Run("release repository", func(t *testing.T) {
		gotRepo := cfg.Release.GitHub.Owner + "/" + cfg.Release.GitHub.Name
		if gotRepo != entry.Repository {
			t.Errorf(".goreleaser.yaml release repository = %q, catalog entry Repository = %q", gotRepo, entry.Repository)
		}
	})

	t.Run("no tag prefix", func(t *testing.T) {
		// ovdb's repository publishes only ovdb releases (single-product),
		// so both the release workflow's plain "vX.Y.Z" tags and the
		// catalog entry's TagPrefix agree on "every release belongs to this
		// binary".
		if entry.TagPrefix != "" {
			t.Errorf("catalog entry TagPrefix = %q, want empty for ovdb's single-product repository", entry.TagPrefix)
		}
	})

	t.Run("archive naming matches the library's GoReleaser default", func(t *testing.T) {
		if len(cfg.Archives) != 1 {
			t.Fatalf(".goreleaser.yaml archives = %d entries, this test assumes exactly one", len(cfg.Archives))
		}
		const wantTemplate = "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
		if got := cfg.Archives[0].NameTemplate; got != wantTemplate {
			t.Errorf(".goreleaser.yaml archive name_template = %q, want %q (the shape the library's default AssetName assumes)", got, wantTemplate)
		}
		// The catalog entry leaves AssetName nil specifically because this
		// template equals GoReleaser's own convention, which
		// selfupdate.Config's default AssetName already implements
		// ("<binary>_<version>_<os>_<arch>.tar.gz", ".zip" on windows).
		if entry.AssetName != nil {
			t.Error("catalog entry overrides AssetName, but .goreleaser.yaml uses the library's own default naming — override is now unnecessary or archive naming has drifted")
		}
	})

	t.Run("checksums naming", func(t *testing.T) {
		if got := cfg.Checksum.NameTemplate; got != "checksums.txt" {
			t.Errorf(".goreleaser.yaml checksum name_template = %q, want checksums.txt", got)
		}
		if entry.ChecksumsName == nil {
			t.Fatal("catalog entry ChecksumsName is nil, but .goreleaser.yaml publishes a flat checksums.txt, not the library's per-version default")
		}
		if got := entry.ChecksumsName("ovdb", "1.2.3"); got != "checksums.txt" {
			t.Errorf("catalog entry ChecksumsName(...) = %q, want checksums.txt", got)
		}
	})

	t.Run("supported platforms", func(t *testing.T) {
		want := goreleaserPlatforms(t, cfg)
		got := make(map[selfupdate.Platform]bool, len(entry.SupportedPlatforms))
		for _, p := range entry.SupportedPlatforms {
			got[p] = true
		}
		if len(got) != len(want) {
			t.Fatalf("catalog entry SupportedPlatforms = %v, want exactly %d entries matching .goreleaser.yaml", entry.SupportedPlatforms, len(want))
		}
		for p := range want {
			if !got[p] {
				t.Errorf("catalog entry SupportedPlatforms is missing %+v, published by .goreleaser.yaml", p)
			}
		}
		for p := range got {
			if !want[p] {
				t.Errorf("catalog entry SupportedPlatforms has %+v, not published by .goreleaser.yaml", p)
			}
		}
	})

	t.Run("homebrew cask token and OS support", func(t *testing.T) {
		if len(cfg.HomebrewCasks) != 1 {
			t.Fatalf(".goreleaser.yaml homebrew_casks = %d entries, this test assumes exactly one", len(cfg.HomebrewCasks))
		}
		cask := cfg.HomebrewCasks[0]
		// Homebrew strips a "homebrew-" prefix from the tap repository name
		// when resolving `brew install --cask <owner>/tap/<name>` — the tap
		// repository here is "homebrew-tap", addressed as "tap".
		wantToken := cask.Repository.Owner + "/tap/" + cask.Name
		if entry.CaskToken != wantToken {
			t.Errorf("catalog entry CaskToken = %q, want %q from .goreleaser.yaml homebrew_casks", entry.CaskToken, wantToken)
		}
		// GoReleaser's homebrew_casks always ship for every goos the
		// archives cover except windows (Homebrew casks are macOS/Linux
		// only), independent of an explicit per-cask OS list in this
		// config, so this is checked against the archive platform set
		// rather than a .goreleaser.yaml field.
		wantOS := map[string]bool{}
		for p := range goreleaserPlatforms(t, cfg) {
			if p.GOOS != "windows" {
				wantOS[p.GOOS] = true
			}
		}
		gotOS := make(map[string]bool, len(entry.CaskOS))
		for _, os := range entry.CaskOS {
			gotOS[os] = true
		}
		if len(gotOS) != len(wantOS) {
			t.Fatalf("catalog entry CaskOS = %v, want exactly %v", entry.CaskOS, wantOS)
		}
		for os := range wantOS {
			if !gotOS[os] {
				t.Errorf("catalog entry CaskOS is missing %q", os)
			}
		}
	})
}

// TestOvdbCatalogEntryManagerMatchesSelfUpdate proves ovdb's own self-update
// Config (built from the catalog entry) still declares the executable
// `brew upgrade --cask ovdb` manager this file documented directly before
// the migration to cliinstall.ByID — i.e. that the catalog is truly the
// single source cliinstall's REQ: catalog-identity-single-source requires,
// not a second, independently-drifting copy.
func TestOvdbCatalogEntryManagerMatchesSelfUpdate(t *testing.T) {
	entry, ok := cliinstall.ByID(ovdbCatalogID)
	if !ok {
		t.Fatalf("cliinstall.ByID(%q) not found", ovdbCatalogID)
	}
	ids := cliinstall.IDs()
	sort.Strings(ids)
	found := false
	for _, id := range ids {
		if id == ovdbCatalogID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("cliinstall.IDs() = %v, want it to include %q", ids, ovdbCatalogID)
	}
	if entry.ID != ovdbCatalogID {
		t.Errorf("entry.ID = %q, want %q", entry.ID, ovdbCatalogID)
	}
	cfg := entry.Config("1.2.3")
	if !reflectDeepEqualManagers(cfg.Managers, newSelfUpdateConfig("1.2.3").Managers) {
		t.Errorf("entry.Config(...).Managers = %+v, want newSelfUpdateConfig(...).Managers = %+v", cfg.Managers, newSelfUpdateConfig("1.2.3").Managers)
	}
}

func reflectDeepEqualManagers(a, b []selfupdate.Manager) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name ||
			a[i].UpgradeCommand != b[i].UpgradeCommand ||
			a[i].UpgradeExecutable != b[i].UpgradeExecutable ||
			len(a[i].UpgradeArgs) != len(b[i].UpgradeArgs) {
			return false
		}
		for j := range a[i].UpgradeArgs {
			if a[i].UpgradeArgs[j] != b[i].UpgradeArgs[j] {
				return false
			}
		}
	}
	return true
}
