# Scoped representation attachments

`ovdb.yaml` is proposed to optionally attach provider-local execution metadata
after the canonical companion validators land:

```yaml
representation_contract:
  path: model/representations.json
  sha256: <SHA256 of those exact committed bytes>
```

`schema.json`, `schema2.json`, and `schema3.json` are separate closed schemas.
`Parse` selects the exact declared format and checks bounded strict JSON.
`Check` additionally checks immutable
reference closure, source/target ModelSpec property datatypes, the target's actual
MeaningGraph identifier binding and canonical meaning pin, physical bridge columns,
exact raw-label uniqueness and native target-key membership. Every referenced file
has an exact SHA256. Unknown versions/policies, JSON duplicate keys, consumed field aliases, unpaired surrogate
escapes, multiple YAML documents, path escapes,
URLs masquerading as paths and mutable revisions are refused.

An own-provider reference has only `path` and `sha256`; the outer Directory record
supplies its immutable repository/commit. External references additionally require
`repository` and `revision` (full 40 lowercase hex). They cannot name the provider
itself, so no attachment embeds a self-commit or creates a circular hash. A source
schema, canonical meaning and decision provenance are immutable external files.
Target models/bindings/snapshots/key indexes/bridge exports are provider-local.
The resolver must read regular committed files at the exact external pin. The
opt-in `repo.CheckRepresentation` helper accepts explicit offline dependency
readers keyed by both repository and full revision; their HEAD must equal the
requested revision, and no fetch occurs. It remains metadata-only.
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

## Exact source data in contract format 3

`schema3.json` has canonical schema ID
`https://openvaultdb.com/schemas/representation-contract-3.json`. Format 3
contains only `native-identifier` contracts and requires an external
`source.data` reference with repository, 40-hex revision, path, and SHA256.
Formats 1 and 2 retain their original schemas and reject this extra member.
The typed descriptor is part of exact source identity; metadata `Check` verifies
its syntax but never passes it to `Context.Resolve`.

`repo.VerifySourceData` is a separate opt-in stage for a structurally checked
format 3 document. It selects an explicitly supplied `(repository, revision)`
reader, checks its pinned HEAD and tracked regular-file mode, and hashes at
most 5 MiB of raw committed bytes per distinct reference. It reads references
sequentially and caches proof only during one call. A successful byte proof
does not validate JSON rows or grant semantic or production admission.
Default publisher validation and CLI dependency provisioning remain closed
until the Directory and current Chinook companions land.

Validation includes wrong source property/revision/namespace, case/space changes, JSON field aliases,
invalid Unicode scalar escapes and ignored trailing YAML documents,
mutable pins, altered hashes, missing keys, duplicate labels, path/URL escapes,
unknown versions and unresolved dependencies. Legacy manifests remain compatible.

## Canonical companion ownership and order

The current canonical provider checker is `demo-db/chinook`, verified at remote
main `26e852cca00101f53a84ef8ee1f1ae389067f5cf` on 2026-10-05. Its
`scripts/lib/ovdb-manifest.mjs` has a closed allowlist without this field. The
historical Go parity references remain `openvaultdb/directory` at
`087067483686865b13cb76511ff86f7364ea47ff` (moved from `e8db548` by the pin slice A0a; see `internal/publisher/manifest/testdata/reference/drift.json`) and `datatug/chinookdb` at
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

## Explicit native execution in contract format 2

`schema2.json` has canonical schema ID
`https://openvaultdb.com/schemas/representation-contract-2.json`, distinct from
format1, so consumers can register both schemas together. The closed
`ovdb-representation-contract/2` format adds a required `execution`
discriminator. `label-bridge` keeps the reviewed bridge, key-index and collision
checks. `native-identifier` forbids both `bridge` and `target.keys`; it requires:

```json
"native": {
  "dataset": {"path": "ror.sqlite", "sha256": "<assembled native data hash>"},
  "provenance": {"path": "source/validation.json", "sha256": "<receipt hash>"}
}
```

The target must actually declare the required single-property native key in its
ModelSpec. Its exact source and target namespaces must agree. An optional
`native.serving_identity_column` must be a distinct existing field. Snapshot,
model, binding, canonical meaning, user source schema and decision hashes/pins
remain checked. The closed format1 schema and existing fixtures remain unchanged;
format1 accepts no new execution fields. There is no inference from missing
bridge fields, version fallback or second default publisher profile. Format2
permits the literal canonical inGitDB `$records` path component; all other dollar
components, traversal, URLs, absolute paths and percent encoding are rejected.

`native.dataset` is a logical data-artifact descriptor, rather than a metadata
file reference. `Check` never sends it to the metadata resolver, fetches its
bytes or loads a native key corpus. The generation snapshot binds its path/hash
alongside the exact model, binding and provenance receipt. Ordered published
chunks may represent that assembled artifact. Offline publication can separately
stream-hash the assembled bytes; discovery must remain within its metadata budget.

The existing provider generation receipt is extended with this closed section:

```json
"native_key": {
  "module": "ror", "entity": "organizations", "property": "id",
  "namespace": "ROR:URL",
  "model": {"path": "model/ror.modelspec.json", "sha256": "<hash>"},
  "binding": {"path": "model/ror.meaning.yaml", "sha256": "<hash>"},
  "dataset": {"path": "ror.sqlite", "sha256": "<hash>"},
  "records": 141528, "duplicates": 0
}
```

The checker verifies exact scope/ref association, nonnegative counts, zero
claimed duplicates, the original `snapshot.outputs[dataset.path].sha256`, and
original `snapshot.counts[entity]`. The receipt preserves that original embedded
source-generation snapshot. A later metadata snapshot adds immutable `generator`
and `artifacts` entries for the data, model, binding and extended receipt; the
receipt must never embed that later snapshot, avoiding circular content hashes.
Provider generation/tests and independent publication review must establish these
claims against the source and native dataset. They are generation evidence, not a
second acceptance registry, semantic verdict or proof from a checksum. Empty
native datasets are expressible and yield unmatched lookup results.

`LookupNative` issues an explicit `NativeLookupRequest` carrying the outer provider
repository/revision, model/snapshot/dataset hashes, exact target property/namespace,
raw input and `Limit: 2`. Its data reader must execute this keyed query against
that immutable artifact with the caller's time/byte/cancellation bounds. Zero
rows are unmatched; two or more rows are ambiguous; a single returned native key
must equal the original UTF8 bytes. The statically typed reader returns string
keys; a wire adapter must reject nonstring values before returning. A bounded
response cannot establish global source uniqueness. ROR format/checksum validity,
NULL/empty/invalid distinctions, preserved status warnings and affiliation/location
grain belong to the reviewed user contract and app execution. This helper never
substitutes a serving ID, trims values or certifies affiliation truth.

The real GeoNames fixture exercises the corrected native country namespace and
unchanged canonical decision path. The real ROR fixture exercises actual
`organizations.id`, its required key and canonical binding, plus explicitly
proposed additive receipt/snapshot linkage. Both remain staged fixtures without
production eligibility. Directory, current demo-db/chinook checker, OVDB parity
adoption and app admission companions remain prerequisites.

A later provider packaging or wrapper revision needs explicit independent
carry-forward review tying its unchanged semantic source, model, binding and
native data to the accepted decision's original pins. Structural validation at a
new outer provider commit does not inherit that semantic acceptance. Packaging
proof and source identity evidence must be reviewed before canonical admission;
any semantic change requires the dedicated specialist decision.

## Original descriptor association in native receipts

A provider whose original snapshot uses named descriptors rather than filename
keys can add this optional closed field to the generation receipt:

```json
"snapshot_association": {
  "source": {"path": "source/generation-snapshot.json", "sha256": "<original hash>"},
  "output_key": "sqlite"
}
```

The existing `native_key` and embedded original `snapshot` remain intact. Presence
selects the descriptor branch; absence preserves the reviewed filename-keyed ROR
branch. A malformed explicit association fails, with no fallback. The source ref
has only path/hash, resolves as bounded provider-local metadata at the outer
immutable commit, and must appear with the same hash in the later metadata
snapshot's artifacts. It must differ from the receipt, later metadata snapshot
and logical dataset. No dataset, chunk or native keyset is read by this check.

The original file is hash-checked and strictly parsed, including duplicate-key,
Unicode, depth and byte checks. Its parsed object must equal the embedded snapshot
while preserving numeric tokens rather than rounding through float64. Key order
and whitespace can differ; equivalent numeric spellings such as `1` and `1.0`
are conservatively rejected rather than normalized. The source bytes retain their
exact original hash. `output_key` is one literal ASCII key of 1–128 bytes using
letters, digits, underscores or hyphens; it has no path, expression, wildcard or
transform meaning. Only original `outputs[output_key]` is selected. Its descriptor
must have the exact consumed `file` and `sha256` fields matching the native data.
Unrelated outputs, including ordered chunks arrays, stay original source evidence.
Original `counts[entity]` must be a nonnegative int64 integer token equal to
`native_key.records`; fractional, exponent and overflow counts are refused.

The later metadata snapshot binds the unchanged dataset, model, binding,
per-entity receipt and exact original snapshot. Provider generation/review proves
actual key uniqueness and packaging reconstruction; this association checks their
structural links and grants no semantic acceptance. Canonical consumers must adopt
the reviewed helper pin before accepting this receipt shape. Both contract schema
files, existing label bridges, default publisher closure and parity remain unchanged.
The native-geonames fixture uses real original/provider metadata and an expressly
hypothetical test input/decision; it is not an eligible user mapping.
