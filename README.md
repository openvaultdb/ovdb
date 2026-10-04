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
  databases can also be created at runtime. Add `--read-only` to reject all
  data and token mutations, including owner-token requests. Open `/ovdb/` in
  a browser for the generic server page, then browse `/ovdb/dbs/` and each
  database profile. Use `--public-url https://your.example` when a reverse
  proxy provides the externally reachable origin for connection URLs.
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
  fleet CLI's own `install ovdb` also resolves releases from. A copy of
  `ovdb` inside a directory the operating system's package manager owns
  counts as managed by the system package manager: `self-update` and
  `upgrade` do not replace it in place, they say how to update it, and
  `--format json` carries that as `hint` (`upgrade_hint` with `--check`).
  The directories are the library's own list for each operating system:
  - macOS: `/usr/bin`, `/usr/sbin`, `/usr/libexec`, `/bin`, `/sbin`, `/System`, `/nix/store`, `/run/current-system`
  - Linux: `/usr/bin`, `/usr/sbin`, `/usr/lib`, `/usr/lib64`, `/usr/libexec`, `/usr/share`, `/bin`, `/sbin`, `/lib`, `/lib64`, `/nix/store`, `/run/current-system`
  - Windows: `%SystemRoot%`, `%ProgramFiles%`, `%ProgramFiles(x86)%`

  A copy anywhere else (`/usr/local/bin`, `~/go/bin`, `~/bin`, a Homebrew
  directory, ...) is classified as before. On Windows `ovdb` is published as a
  zip, so a copy unzipped under `%ProgramFiles%` is classified as managed too,
  and the hint it prints ("Windows Update, or the installer (MSI/EXE) that
  originally placed it there") does not describe it: update such a copy by
  downloading the new zip from the releases page and replacing the files.
- **`ovdb skills`** — list the AI agent skills and where each AI agent keeps
  them (`skills list`), and install one for the agents found on this computer
  (`skills install <openvaultdb|todo-demo>`, which asks first). A folder of
  that name that is already there and already is the skill (its `SKILL.md`
  names it and it holds no file the skill doesn't ship) is adopted: `ovdb`
  takes it over after keeping a backup of your copy, and says where. A folder
  that is anything else is left as it is. See
  [AI agent skills](#ai-agent-skills) below.
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

`ovdb install --all` lists every CLI in the catalogue (`sneat` and `specscore`
among them), not only those relevant to ovdb, and any of them can be installed
by name. `--dir` places a CLI in a directory of your choice, except the
directories an operating system package manager, Homebrew or Snap owns
(`/usr/bin` is refused, for example).

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

### AI agent skills

`ovdb skills install <skill>` installs into each AI agent found (or the ones
named with `--harness`, or one folder with `--dir`). A skill folder that is
already there, was not installed by `ovdb` and already is the skill (its
`SKILL.md` names it and it holds no file the skill does not ship; its other
bytes may differ) is **adopted**: `ovdb` keeps a backup of it in
`.cli-helpers-skills-adopted-backup` inside that agent's skills folder, puts
the skill there, and manages it from then on. The question before an install
says which folders are taken over. The terminal UI and the web console offer
such a folder unticked.

For scripts and API clients (`--json` is the body of the local API):

- `skills list --json` and `GET /api/local/v1/skills?adoptable=1` report such
  a target as `"state":"adoptable"` (not installed). Without
  `?adoptable=1` the API reports `"state":"not_ovdb"`, the state every version
  before adoption knows for a folder `ovdb` did not install.
- `skills install` and `POST /api/local/v1/skills/install` adopt only when the
  request says `"adopt":true` (the CLI sends it when its plan has a folder to
  take over, after you agreed; the console when you ticked such an agent). A
  request without it is answered as it was before adoption: `already_exists`
  (exit 1, HTTP 409), nothing touched. Two narrow cases remain until the
  skills library can switch adoption off: a folder that becomes adoptable in
  the instant between `ovdb`'s check and the install, and a skills folder in
  which an earlier install was interrupted (a leftover
  `.cli-helpers-skills-recovery.json`). In both, the folder can be taken
  over, with a backup, by a request that did not ask; `ovdb` v0.21.0 and
  older then stop with an error after printing "Installed".
- A script that runs `ovdb skills install <skill> --yes` on such a folder
  used to get exit 1 with `already_exists`. It now takes the folder over,
  keeps a backup and exits 0: `--yes` is the consent, there is no separate
  flag.
- An adoption is a success: exit 0, HTTP 201, `"result":"adopted"`, and
  `"backup_path"` on that target (absent in a dry run, which reports the
  plan).
- An install that fails for some targets still reports the ones that did
  change: the failure's `reason` says which and where a backup is, and the
  error carries them as `targets`, the same list a success has. The code and
  exit status are the failure's.

### Read-only servers

For a public, query-only deployment, start the legacy server with
`ovdb serve --read-only`. It rejects every mutating `/v1` request before
authentication or route side effects, including database creation, token
creation/revocation, OAuth authorization/token exchanges, and owner-token
writes. Successful reads continue normally.

The local OVDB server persists the same mode in its owner-only configuration:

```sh
ovdb config set server.read_only true
ovdb server restart
```

This also refuses local database registry mutations (create, connect, reload,
remove) and demo installation. Server lifecycle, configuration, telemetry,
skills, and project-context operations remain available because they do not
mutate database data; use `ovdb config set server.read_only false` followed by
a restart to leave read-only mode.

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

The packages under `internal/publisher` (the groundwork of a future
`ovdb publisher check`) are held to exactly 100% statement coverage by a gate
scoped to them, `cmd/covergate`, run by the `publisher-coverage` job of
`.github/workflows/ci.yml`; how to run it, and the rules those packages
implement and how they are proved against their JavaScript references, are in
[internal/publisher/rules/README.md](internal/publisher/rules/README.md).
