// Command ovdb is the OpenVaultDB CLI — the canonical developer/admin
// interface for managing, exploring, validating and operating OpenVaultDB
// instances. `ovdb serve` runs the local API server.
package main

import (
	"context"
	"errors"
	"io"
	"os"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"
	"github.com/strongo/buildinfo"
	"github.com/strongo/buildinfo/fangcmd"

	"github.com/openvaultdb/ovdb/internal/cli"
	"github.com/openvaultdb/ovdb/internal/preview"
	"github.com/openvaultdb/ovdb/internal/publisher/checkcmd"
)

// appVersion is this build's bare semver, resolved once in main() from
// buildinfo.Get and threaded into commands (e.g. `serve`) that need to
// report it. It replaces the old hand-rolled `Version` var: buildinfo is
// now the single source of truth, stamped at link time by
// .goreleaser.yaml's ldflags (see github.com/strongo/buildinfo's doc
// comment) and falling back to runtime/debug.ReadBuildInfo() otherwise.
var appVersion string

// DefaultAddr is the default listen/connect address. Local-first: binds to
// loopback unless explicitly overridden ("6832" spells OVDB on a phone keypad).
const DefaultAddr = "127.0.0.1:6832"

func main() {
	info := buildinfo.Get("ovdb")
	appVersion = info.Version

	root := &cobra.Command{
		Use:           "ovdb",
		Short:         "OpenVaultDB — user-owned, portable databases with pluggable engines",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// fangcmd.Wire adds the `version` subcommand (printing info.Long()) and
	// returns the fang.Option(s) that make fang's --version/-v flag print
	// exactly info.Short() — the two surfaces are driven by this one Info
	// so they cannot disagree. fang.Execute already prints a styled error
	// to stderr on failure (see charm.land/fang/v2), so main only needs to
	// set the process exit code.
	fangOpts := fangcmd.Wire(root, info)

	app := addRootCommands(root, info.Version)

	// Agent-facing commands fail with the shared error envelope, printed by
	// cli.Render; every other error keeps fang's output.
	fangOpts = append(fangOpts, fang.WithErrorHandler(func(w io.Writer, styles fang.Styles, err error) {
		// A refused repository is already reported by `publisher check`'s own output; only the exit code (1) is left to say.
		if errors.Is(err, checkcmd.ErrRefused) {
			return
		}
		if !cli.Render(err, os.Args[1:], os.Stdout, w) {
			fang.DefaultErrorHandler(w, styles, err)
		}
	}))

	if code := executeRoot(root, app.FlushTelemetry, fangOpts...); code != 0 {
		os.Exit(code)
	}
}

// executeRoot is the executable's command/cleanup/telemetry/exit boundary.
// Only an error that owns pending cleanup can hold the process here; ordinary
// errors retain their existing bounded telemetry flush and immediate exit.
func executeRoot(root *cobra.Command, flush func(context.Context), opts ...fang.Option) int {
	err := fang.Execute(context.Background(), root, opts...)
	var owner interface{ WaitForCleanup() }
	if errors.As(err, &owner) {
		// Fang has already reported the bounded-drain failure. Keep the
		// process alive until its resource owner settles and cleans up.
		// A permanently non-cooperative handler can keep shutdown pending.
		owner.WaitForCleanup()
	}
	// After the command's output, whatever its outcome: at most 2 s, silent
	// (telemetry-consent#REQ:bounded-synchronous-sender).
	flush(context.Background())
	if err != nil {
		return commandExitCode(err)
	}
	return 0
}

// commandExitCode preserves an external command's nonzero process status when
// an operation such as Homebrew-backed self-update delegates to it. All other
// errors retain ovdb's existing general failure status. The portable process
// status range is 1..255; a missing, zero, negative, or wider value is not a
// valid failure status for os.Exit and falls back to 1.
func commandExitCode(err error) int {
	var exitCoder interface{ ExitCode() int }
	if errors.As(err, &exitCoder) {
		if code := exitCoder.ExitCode(); code > 0 && code <= 255 {
			return code
		}
	}
	return 1
}

func addRootCommands(root *cobra.Command, currentVersion string) *cli.App {
	app := &cli.App{Version: currentVersion}
	root.AddCommand(
		newCloudCmd(),
		newServeCmd(),
		newInitCmd(),
		newStatusCmd(app),
		newDatabasesCmd(app),
		newTokenCmd(app),
		newSelfUpdateCmd(currentVersion),
		newInstallCmd(),
		newUpgradeCmd(currentVersion),
		newPublisherCmd(),
	)
	app.AddCommands(root)
	// Only the unfinished bare-command TUI remains behind the preview gate.
	// Named user-facing commands and their help are always public.
	if preview.On() {
		root.RunE = app.RootRunE
	}
	return app
}
