# Scoped representation attachments

`ovdb.yaml` is proposed to optionally attach provider-local execution metadata
after the canonical companion validators land:

```yaml
representation_contract:
  path: model/representations.json
  sha256: <SHA256 of those exact committed bytes>
```

`schema.json` is the closed `ovdb-representation-contract/1` schema. `Parse`
checks that schema and bounded strict JSON. `Check` additionally checks immutable
reference closure, source/target ModelSpec property datatypes, the target's actual
MeaningGraph identifier binding and canonical meaning pin, physical bridge columns,
exact raw-label uniqueness and native target-key membership. Every referenced file
has an exact SHA256. Unknown versions/policies, JSON duplicate keys, path escapes,
URLs masquerading as paths and mutable revisions are refused.

An own-provider reference has only `path` and `sha256`; the outer Directory record
supplies its immutable repository/commit. External references additionally require
`repository` and `revision` (full 40 lowercase hex). They cannot name the provider
itself, so no attachment embeds a self-commit or creates a circular hash. A source
schema, canonical meaning and decision provenance are immutable external files.
Target models/bindings/snapshots/key indexes/bridge exports are provider-local.
The resolver must read regular committed files at the exact external pin. The proposed `repo.CheckRepresentation` helper accepts explicit offline dependency
readers; their HEAD must equal the requested revision, and no fetch occurs.
Default `ovdb publisher check` remains the exact closed legacy validator and
rejects the proposed attachment until canonical companion support lands. No
second manifest profile or permissive fallback is introduced.

Bridge data is a bounded table export:

```json
{"table":"CustomerCountries","rows":[{"raw_label":"USA","target_key":"US"}]}
```

The contract identifies the physical raw-label/target-key columns in its provider
ModelSpec, with a separately named serving identity column when present. The export
uses canonical interchange keys `raw_label` and `target_key`; generators must verify
these values equal their physical table columns byte-for-byte. Labels retain their
UTF8 bytes, case and spaces. The native target key index is separately bounded:
`{"namespace":"iso-3166-1-alpha-2","keys":["US"]}`. Duplicate raw labels are
ineligible, even if their targets coincide. Missing keys are refused. An unmatched
exact input remains an exception (zero targets), never a normalized guess. No
expression or transform language is defined: version one permits only identity,
UTF8 byte equality and zero-or-one cardinality.

The hashed snapshot must include `generator: {repository, revision}` and
`artifacts: [{path, sha256}]`, including the exact bridge export and key index.
It may retain its existing release/input hashes, counts, resource and licence
receipt. Structural checking proves those links and membership in the referenced
index; provider tests and independent review must prove the derived index/export
against native source data and its generator. A checksum alone cannot establish
source provenance or semantic truth.

The decision's immutable document and named scope are provenance for the dedicated
accepted reconciliation. This package does not parse free prose, repeat a decision
or create an acceptance registry. A successful attachment helper check is structural validation,
**not semantic acceptance or production eligibility**. Independent semantic/contract
review and canonical registry/Directory publication admission remain mandatory.

The synthetic fixture under `testdata` is a provider/consumer interchange example,
not an accepted demo mapping. No fixture publisher is production-authorized.

## Directory and apps integration handoff

The existing Directory owner must extend `manifestProblems` with the same closed
optional path/hash mapping. At the Directory-pinned provider commit, read a regular
attachment, verify its byte hash, validate version/schema and resolve all references
through already trusted canonical immutable model/meaning/source dependencies.
Reject checksum or property/binding/namespace/revision failures before indexing.
Expose the checked attachment path/hash/provider commit with the generated database
record, without duplicating meanings or loading global native data into metadata.
The current seams are `scripts/lib/directory.mjs`'s manifest validation, provider
file loading, canonical property/binding checks and final database index emission.
This OVDB change does not implement those Directory edits.

Apps must validate the same schema and all checked canonical linkage, then match
an input's exact source repository/schema revision/module/entity/property/datatype/
namespace before suggesting a bridge. `Lookup` illustrates exact matching and
zero-or-one exceptions; equivalent source properties do not inherit this scope.
Binding labels are insufficient: existing `match.labels` is case-insensitive.
`match.codes.*` is reusable only for an already accepted identifier representation;
observed country labels must not be relabeled as codes to fit it. For ROR, require
the independently accepted user contract and use explicit bounded lookup rather
than downloading the global ID set into discovery metadata. The native ROR `id`
and serving identity remain different fields.

Production eligibility is blocked until the Directory validator/index, app reader,
provider generation/native-data proof and independent review land at exact pins.
No service deployment or runtime native-id support is supplied by this package.

Validation includes wrong source property/revision/namespace, case/space changes,
mutable pins, altered hashes, missing keys, duplicate labels, path/URL escapes,
unknown versions and unresolved dependencies. Legacy manifests remain compatible.

## Canonical companion ownership and order

The current canonical provider checker is `demo-db/chinook`, verified at remote
main `26e852cca00101f53a84ef8ee1f1ae389067f5cf` on 2026-10-05. Its
`scripts/lib/ovdb-manifest.mjs` has a closed allowlist without this field. The
historical Go parity references remain `openvaultdb/directory` at
`e8db5488db31d3f63865e404acef487c33cf35df` and `datatug/chinookdb` at
`79e7bb0b1d6f0666dce465874990dec64348331f`; this staged change leaves their
parity tests and every existing default publisher rule intact.

Before wiring default success, the designated Directory owner must land canonical
attachment shape/file/schema/reference/index support, and the canonical
`demo-db/chinook` publisher checker owner must adopt faithful checked support.
Then the OVDB tooling owner can update exact reference pins/regenerate parity
fixtures and wire `ParseAttachment`/`CheckRepresentation` into the default check.
Apps admission and immutable provider/model/meaning/decision generation reviews
remain additional dependencies. This branch changes none of those sibling repos
or their owners' publication queue.
