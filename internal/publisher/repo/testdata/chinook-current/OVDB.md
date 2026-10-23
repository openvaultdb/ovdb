---
ovdb: 1
publish: [./ovdb.yaml, ./ovdb-database.json]
---
# OpenVaultDB publisher manifest

This repository publishes the Chinook sample database to the
[OpenVaultDB](https://github.com/openvaultdb) Directory.

The list above explicitly opts both manifests into Directory ingestion. Paths
are relative to the repository root, never a glob. `ovdb.yaml` preserves the
publisher's `ovdb-manifest/draft-1` input; `ovdb-database.json` is the generated
public database descriptor.
[`ovdb.yaml`](ovdb.yaml) describes one database: its canonical identity, the
live deployment, the ModelSpec model, the MeaningGraph meaning file, the
publisher and the licences.

The public database identity is [`https://demodb.dev/chinook/`](https://demodb.dev/chinook/).
The descriptor is generated from the provider manifest and native schema, and
is also served by the shared server at
[`https://demodb.dev/ovdb/db/chinook/ovdb-database.json`](https://demodb.dev/ovdb/db/chinook/ovdb-database.json).

An optional top-level `homepage` is the publisher's own page for the database,
an https URL of at most 200 characters; the Directory publishes it in its index, and its site may show it as "Website".

A manifest names its ModelSpec model in one of two ways: by local files
(`model.modelspec` and `model.hcl`), or, when the model lives in another
repository, by `model.address` pinned with `?ref=<40 hex>` and no local files
(the Directory accepts both forms; see [`examples/hoster/`](examples/hoster/)).
This repository carries its own files and the model's address,
`modelspec://github.com/demo-db/chinook/chinook`, registered in the ModelSpec
registry, with no ref because the files are in the same repository. The
manifest format is a draft (`ovdb-manifest/draft-1`).
