# ECB original metadata acceptance

The project decision for the planned shared OVDB proxy is an explicitly configured
`openvaultdb-cloud` executor, database `ecb`, local collection `daily`, source ID
`ovdb:openvaultdb-cloud/ecb/daily`. Root accepted this identity and the native
`provider:ecb/FxReferenceQuote` namespace on 2026-10-07. Resource binding is
`ecb-daily`. These are project configuration decisions; they imply no ECB
endorsement, legal clearance, deployed identity or active route. A request Host,
the descriptor's `serverId` URL and the cloud server's display/version argument
cannot select the rights executor identity.

The public descriptor convention remains `https://cloud.openvaultdb.com/ovdb`,
database `ecb`, API `https://cloud.openvaultdb.com/v1`. This lane does not configure
startup, CORS, billing or routing. Directory remains inactive, execution disabled,
B1–B4 open. `ovdb-http-source/1.RequireExecution` continues refusing every call.

`ecb-daily.proposal.json` freezes the complete detached SourceRight and Binding,
independently authored from the original documents reviewed at OVDB commit
`c72f1e711041a85ec67d6fe86f7621ae4bde302e`. No preparer output was copied to
construct these expectations. The rights digest uses the existing
`ovdb-rights-binding/1` RFC 8785 contract. The one provider pin binds the whole
publisher manifest, not returned source bytes. Attribution, free-source notice,
transformation disclosure, declaration scope and declaredAt are exact inventory.
The linked conditions are declarations, not a licence grant or paid entitlement.

`ecb-daily.original-artifacts.json` freezes the original OVDB.md index, publisher
manifest and paired descriptor with commit/path/blob/SHA-256/size. This inventory
is an offline review fixture, not a new admission format or runtime loader. The
existing 12-entry semantic/decoder baseline stays unchanged. `TestAccepted*`
checks every original pin with the admitted Git executable, runs the full
publisher/paired-descriptor checker at the pinned commit, compares the independently
authored expectation through preflight, and requires the blocked disposition.
Existing authored-metadata tests challenge missing/changed publisher, descriptor,
model and notices; strict runtime tests cover alias refusal before provider reads.

From the OVDB repository, a complete offline metadata check uses the existing
`ecb-preflight` command with all six local repositories and
`-proposal publisher/source/preflight/metadata/ecb-daily.proposal.json`.
`publisher.directory` is `.`; invoke from the OVDB repository, or change only that
local path in a private proposal copy. The compiled command must exit **2** and
emit `blocked-candidate-metadata-verified`, `executionEnabled:false`,
`directoryStatus:inactive`, B1–B4 and `runtimeEnforcement:unproved`. Exit 1 means
invalid or unavailable metadata. The proposal and receipt cannot be loaded by
`--provider-read-profiles`. The offline policy proposes maxRows 50 and identical
zero-fee capabilities for anonymous and paying accounts; enforcement remains B4.

Metadata acceptance does not complete B1's runtime admission. Deliberate controlled
admission, selected-path no-retention proof (B2), browser proof (B3), enforced free
routing (B4), real-request authority and public activation remain separate work.
The source stays live/unpinned; no provider XML, rates, result copies or payload
captures belong in these artifacts. The model retains required native strings
`time`, `currency`, `rate`; `(time,currency)` response grain, no stable key and
transient currency locator semantics are unchanged.
