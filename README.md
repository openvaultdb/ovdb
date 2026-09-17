# ovdb

`ovdb` is the OpenVaultDB command-line interface — the canonical
developer/admin tool for creating, running, and operating an OpenVaultDB
instance: user-owned, portable databases with pluggable storage engines
(SQLite, inGitDB, ...).

The reference implementation and Go libraries backing this CLI live at
[`github.com/openvaultdb/openvaultdb-go`](https://github.com/openvaultdb/openvaultdb-go).

## Install

Via Homebrew (macOS/Linux):

```sh
brew install --cask openvaultdb/tap/ovdb
```

Via `go install` (requires a Go toolchain):

```sh
go install github.com/openvaultdb/ovdb@latest
```

## Usage

```sh
ovdb --help
```

- **`ovdb init`** — create a database manifest (`--id`, `--engine`,
  `--schema-mode`, `--path`, `--out`).
- **`ovdb serve`** — run the OpenVaultDB HTTP API server over the manifests
  in `--dir` and/or listed with `--manifest`. With `--data-dir`, new
  databases can also be created at runtime.
- **`ovdb status`** — show the status of a running `ovdb serve` instance.
- **`ovdb databases`** — list, and (`databases create`) create, databases on
  a running server.
- **`ovdb token`** — manage revocable, scoped API tokens against a running
  server (create, list, revoke).
- **`ovdb cloud`** — sign in to OpenVaultDB Cloud through a browser, inspect
  the current login, revoke it, or list safe database registration metadata
  (`login`, `status`, `logout`, `databases`). Credentials use the operating
  system keyring by default. Plaintext storage requires the explicit
  `--insecure-storage` flag.
- **`ovdb self-update`** — check for or install a newer CLI release (alias:
  `ovdb update`). Homebrew-managed installs confirm, then run
  `brew upgrade --cask ovdb` directly without a shell; manual installs are
  checksum-verified and replaced atomically. `--yes` skips confirmation,
  `--dry-run` shows the exact action without executing it, and `--version`
  pins are supported only for manual installs because Homebrew does not
  guarantee arbitrary historical cask releases. Built on
  `github.com/strongo/cli-helpers/selfupdate`; its release identity (GitHub
  repository, supported platforms, flat `checksums.txt` naming, and the
  executable `brew upgrade --cask ovdb` manager) comes from ovdb's own entry
  in `cli-helpers`' compiled-in `cliinstall` catalog
  (`cliinstall.ByID("ovdb").Config(...)`), the single source every other
  fleet CLI's own `install ovdb` also resolves releases from.

```sh
ovdb self-update --check
ovdb self-update --check --format json
ovdb self-update
ovdb self-update --yes
ovdb self-update --dry-run
# Manual installs only:
ovdb self-update --version v0.3.0 # github.com/openvaultdb/ovdb release
```

### Owner token and access policies

For a manifest with no declared access policies (every manifest `ovdb`
supports today), the owner token behaves exactly as before: full,
unrestricted access to everything.

`openvaultdb-go` also supports databases that declare access policies (a
layered ACL, not yet exposed by any `ovdb` manifest or flag). On those
databases the owner token still satisfies every admin/capability check —
`ovdb token create|list|revoke`, runtime `databases create`, and the
`--auth` capability gate all keep working for the owner exactly as before —
but the database's own declared access policies are still evaluated against
every request, including the owner's. In other words, the owner token stops
meaning "bypass all data rules" and starts meaning "administrative
authority, plus whatever the declared policies allow"; a policy an owner
declares on a database's data can restrict what even that owner's requests
may read or write. Legacy manifests, which never declare policies, are
unaffected either way.

### Cloud database catalogue

`ovdb cloud databases list` (or `ls`) lists registrations in every accessible
Space. Use `--space personal` for the personal Space, `--space <id>` for one
Space, and `--json` for machine-readable data. `ovdb cloud databases get <id>`
shows one registration. These commands need the explicit `databases:read`
scope, which grants registration metadata only—not records or credentials. If
you signed in before this scope was requested, run `ovdb cloud login` again.

Run `ovdb <command> --help` for the full flag reference of any subcommand,
or `ovdb version` for build version/commit/date.

## Development

```sh
go build -o ovdb .
go test ./...
```
