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

Every admission gate is required and fixed to `blocked`; status is `blocked`
and `executionEnabled` is false. `RequireExecution` always refuses, including
after caller mutation. This version has no active/admitted state. A future
version requires actual runtime/providerReads/consumer acceptance, fresh
semantic review, source-rights integration review and product-specific paid
applicability clearance. No pass state can be obtained by editing a flag.
`readEvidence` advertises the required `ovdb-provider-read/1` boundary and
executor-observed classification; it does not create a read receipt or claim
that runtime/consumer support exists. Executor-scoped legacy rights IDs and
expected-server checks remain unchanged. Admitted provider/executor bindings,
canonical receipt digests and observation/usage validation remain runtime work.

`ecb-daily.example.json` contains declarations only, without rate data. It
describes the exact daily XML resource separately from ECB historical XML.
The candidate identity `provider:ecb/FxReferenceQuote` awaits namespace-owner
acceptance and final model mapping review. Native `time`, `currency` and `rate`
are strings; `time` maps to reference date, not publication instant. `rate`
preserves the positive lexical decimal in quote units per 1 EUR. EUR base and
indicative-reference context add no fabricated EUR row or derived cross-rates.
Runtime validation must reject malformed dates/decimals, duplicate date/currency
keys, unexpected XML namespaces/structure, DTDs and entities. Attribution,
original free-source link and restructuring disclosure accompany results;
paid notice applicability remains blocked. The example is neither published
catalog admission nor proof that any query path works.
