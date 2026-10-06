# Original HTTP source definitions

`ovdb-http-source/1` is a closed, preparatory metadata contract, embedded as
`source_definition` in an existing publisher manifest. A paired database
descriptor must carry the identical definition. The publisher checker reads
original Git objects and returns a manifest byte pin classified
`publisher-definition-verified` with `dynamic-unpinned` input verification.
It never fetches a URL or emits immutable-input `SourceRights` for this profile.
Existing manifests and `ovdb-data-rights/1` keep their existing behaviour;
combining the HTTP definition with that profile or a representation contract is
refused. Manifest `licences.data` links the upstream terms without an inferred
SPDX or output licence. Linked rights are declarations, not legal clearance.

`rights.declaration` is the one complete linked declaration (`name` and `url`)
shared by the database and every recordset. Manifest and materialized descriptor
`licences.data` must equal that whole normalized declaration; this version
supports no recordset overrides or replacement text. Descriptors list every
native recordset exactly once with matching materialized terms. Authored
`sourceRights`, `providerReads` or definition-verification evidence is refused.

The definition identifies the original provider independently of an executor.
`providerSourceId` binds `provider:<provider.id>/<native recordset name>` to an
exact declared resource. Native names must match the manifest, and `entity`
must match its existing `recordset_entities` mapping (or the same native name).
`fieldMapping` maps native decoder field names to modeled field names;
`keyFields` names native fields. `context` describes explicit model context,
without synthesizing source rows. Decoder names describe a versioned contract;
this schema neither implements nor verifies a decoder or its semantics.

All resources require GET, a public HTTPS URL without credentials or query
parameters, bounded read size/time, live mode, `retention: none`, no-store and
redirect refusal. This is static URL validation; execution must still enforce
DNS/address guards, CORS/auth, redirects and no-store across every runtime and
consumer route. Current-response parsing in bounded RAM requires the runtime
contract; no durable source rows, bodies, caches, snapshots or fixtures are
authorized by this declaration. Explicit user authorization is required for
any later retention exception, which this version cannot express.

Declaration, terms and attribution links use the same public host/path policy
as resources. Queries, credentials, IP literals and local/reserved hosts are
refused. Simple nonempty fragments such as `#reuse` are allowed for notices;
resource URLs retain the stricter exact-address policy. These are static checks,
with no link fetch or DNS/authenticity certification. The exported schema checks
structure and link shape; the Go parser also enforces the public-target policy.

Every admission gate is required and fixed to `blocked`; status is `blocked`
and `executionEnabled` is false. `RequireExecution` always refuses, including
after caller mutation. This version has no active/admitted state. A future
version requires actual runtime/providerReads/consumer acceptance, fresh
semantic review, source-rights integration review and product-specific paid
applicability clearance. No pass state can be obtained by editing a flag.

The publisher's `repo.PrepareDynamicSourceRight` helper rechecks original Git
objects, requires a clean complete repository verdict, then verifies the same
manifest revision, byte count and SHA-256 again before returning a detached
source-data terms inventory. Its explicit native recordset and trusted executor
identity bind the publisher definition to a local runtime collection. It carries
the linked declaration, attribution, original free-source link, restructuring
disclosure and one `provider` pin of the manifest metadata. It does not fetch a
resource, produce immutable input pins or populate the preparatory manifest's
`SourceRights`; `RequireExecution` continues to refuse. Exact native-to-local
binding, semantics, rights, no-retention and paid/free-only approval remain
independent gates before any operator uses the prepared record.

The paired server capability is the optional
`openvaultdb-go/server.ProviderReadProfile.sourceRight` field: it verifies exact
mounted terms/identity and the full rights digest before supplying those notices
to discovery and query responses. Library deployment comes first. The OVDB CLI
must adopt the independently reviewed, CI-published `openvaultdb-go` tag before
its existing `--provider-read-profiles` loader accepts that field; no development
replacement or manual tag is part of this metadata preparation change.
`readEvidence` advertises the required `ovdb-provider-read/1` boundary and
executor-observed classification; it does not create a read receipt or claim
that runtime/consumer support exists. Executor-scoped legacy rights IDs and
expected-server checks remain unchanged. Admitted provider/executor bindings,
canonical receipt digests and observation/usage validation remain runtime work.

`ecb-daily.example.json` contains declarations only, without rate data. It
describes the exact daily XML resource separately from ECB historical XML.
The candidate identity `provider:ecb/FxReferenceQuote` awaits namespace-owner
acceptance and final model mapping review. The authored preparatory model is
[`model/ecb-daily.modelspec.hcl`](model/ecb-daily.modelspec.hcl), module `ecb`,
entity `FxReferenceQuote`, with [meanings](model/ecb-daily.meaning.yaml).
Native `time`, `currency` and `rate` are strings and map to identically named
model properties; the meaning of `time` is reference date, not publication instant. `rate`
preserves the positive lexical decimal in quote units per 1 EUR. EUR base and
indicative-reference context add no fabricated EUR row or derived cross-rates.
Runtime validation must reject malformed dates/decimals, duplicate date/currency
keys, unexpected XML namespaces/structure, DTDs and entities. Attribution,
original free-source link and restructuring disclosure accompany results;
paid notice applicability remains blocked. The example is neither published
catalog admission nor proof that any query path works.
