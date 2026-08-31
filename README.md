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

Run `ovdb <command> --help` for the full flag reference of any subcommand,
or `ovdb version` for build version/commit/date.

## Development

```sh
go build -o ovdb .
go test ./...
```
