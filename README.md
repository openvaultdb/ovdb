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
- **`ovdb install`** — list, show details for, and install fleet CLIs
  relevant to ovdb (`ingitdb`, `datatug`); see
  [Installing related CLIs](#installing-related-clis) below.
- **`ovdb upgrade`** — the fleet-wide counterpart to `self-update`: report
  or apply upgrades for every *installed* catalog CLI plus ovdb itself. No
  `update` alias — that alias stays reserved for `self-update` alone. `ovdb
  self-update` is exactly `ovdb upgrade ovdb`, built from the same
  `HostConfig`, so the two never disagree; see
  [Upgrading related CLIs](#upgrading-related-clis) below.

```sh
ovdb self-update --check
ovdb self-update --check --format json
ovdb self-update
ovdb self-update --yes
ovdb self-update --dry-run
# Manual installs only:
ovdb self-update --version v0.3.0 # github.com/openvaultdb/ovdb release
```

### Installing related CLIs

`ovdb install` lists the other fleet CLIs relevant to OpenVaultDB (currently
`ingitdb`, whose inGitDB engine `ovdb` runs directly, and `datatug`, which
queries a running `ovdb serve` database as an `openvaultdb` catalog), each
with its live installed status. `ovdb install <name>...` shows fuller
details and installs the named CLIs the same way `ovdb` itself was
installed: `brew install --cask` on a Homebrew host whose target publishes a
cask for the host OS, otherwise a checksum-verified direct release download
placed beside a manually installed `ovdb` or in the per-user bin directory.
Both are entirely offline and read-only until an install is actually
confirmed. Built on `github.com/strongo/cli-helpers/cliinstall`, whose
compiled-in catalog and host → target relevance texts are the single source
every other fleet CLI's own `install ovdb` also resolves from.

```sh
ovdb install
ovdb install --all --format json
ovdb install ingitdb --dry-run
ovdb install ingitdb datatug --yes
```

### Upgrading related CLIs

`ovdb upgrade` reports current/latest/verdict for every *installed* catalog
CLI plus ovdb itself (not merely the relevance matrix `install` lists — a
target that is relevant but not installed has nothing to upgrade).
`ovdb upgrade --all` and `ovdb upgrade <name>...` upgrade what the report
showed, after one confirmation. `--check` reports without applying
anything; `--dry-run` walks the same decision path without asking. ovdb
itself is always upgraded last, classified and versioned from its own
`self-update` configuration — never a `PATH` probe of its own binary — so
`ovdb self-update` and `ovdb upgrade ovdb` reach the exact same library
call and report the same verdict. Built on
`github.com/strongo/cli-helpers/cliinstall`'s `upgrade` command.

```sh
ovdb upgrade
ovdb upgrade --all --check --format json
ovdb upgrade --all --dry-run
ovdb upgrade ovdb --check   # identical outcome to `ovdb self-update --check`
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
