// Command ovdb is the OpenVaultDB CLI — the canonical developer/admin
// interface for managing, exploring, validating and operating OpenVaultDB
// instances. `ovdb serve` runs the local API server.
package main

import (
	"context"
	"os"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"
	"github.com/strongo/buildinfo"
	"github.com/strongo/buildinfo/fangcmd"
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

	root.AddCommand(
		newServeCmd(),
		newInitCmd(),
		newStatusCmd(),
		newDatabasesCmd(),
		newTokenCmd(),
	)

	if err := fang.Execute(context.Background(), root, fangOpts...); err != nil {
		os.Exit(1)
	}
}
