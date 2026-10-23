# Offline ECB runtime preflight

This preparatory command verifies local original Git metadata and a proposed
public-free policy. It has no provider transport, listener, mount, billing hook
or activation path. Its output cannot be loaded as `--provider-read-profiles`.
Every receipt leaves `executionEnabled:false`, Directory inactive, B1–B4 open,
and deployed runtime enforcement unproved.

Run from the OVDB repository with six complete local repositories:

```sh
go run ./publisher/source/preflight/cmd/ecb-preflight \
  -ovdb /path/to/ovdb \
  -modelspec-registry /path/to/modelspec-registry \
  -meaning-registry /path/to/meaning-registry \
  -meaning-core /path/to/meaninggraph-core \
  -directory /path/to/directory \
  -decoder /path/to/dalgo2http \
  -proposal /path/to/reviewed-proposal.json
```

The compiled command exits **2** for a verified blocked disposition and **1**
for invalid/unavailable metadata or output failure. `go run` reports that
nonzero program status through its own exit status. No execution success status
is emitted. Missing publisher metadata is reported explicitly as
`blocked-publisher-artifact-missing`; it is not an admission failure hidden by a
green receipt.

A proposal without a publisher pin contains only authored proposed metadata:

```json
{
  "executor": {
    "serverId": "proposed-proxy",
    "databaseId": "ecb",
    "recordset": "daily"
  },
  "policy": {
    "zeroFee": true,
    "anonymous": ["filter", "projection", "limit", "transient-display"],
    "paying": ["filter", "projection", "limit", "transient-display"],
    "fields": ["time", "currency", "rate"],
    "maxRows": 50
  }
}
```

These capabilities are an offline policy precondition, not account routing,
query enforcement or proof of zero billable reservations. Fields and capability
lists are exact and ordered; the maximum native result limit is 1–100. History,
export, joins, conversion, historical lookup and paid AI are outside this
profile. Actual refusal before I/O/billing, aliases and paying-account access
need later selecting-product tests and the independent sink audit.

Git is resolved once to an absolute path. A recognized version >=2.45 is
required before repository operations, and every pinchain/publisher subprocess
uses that path with replacement objects disabled and lazy fetching suppressed.
The fixed embedded 12-artifact chain is reproduced; a supplied receipt does not
replace verification. Caller Git environment variables and machine-level Git
configuration cannot redirect the selected repositories. Missing objects refuse.

The [original metadata acceptance fixture](metadata/README.md) now freezes the
reviewed publisher manifest and paired descriptor, full independent rights and
binding inventory, and the explicitly chosen planned executor identity. These
are offline metadata expectations; B1 runtime admission remains open.
`publisher` contains its local `directory` and exact `artifact` fields
(`role: publisher-manifest`, repository slug,
commit/path/blob/SHA-256/byte count). `expected` must contain the independently
frozen `binding` and full `right` inventory. Root must accept those trusted
inputs before invocation; supplying a flag or writing JSON conveys no authority.
Do not manufacture an accepted publisher from retained rate data or invent a
repository name. An arbitrary source definition is not a publisher manifest.

The verifier requires the original full publisher/paired-descriptor check,
exact model/meaning paths and bytes from the fixed baseline, and the same parsed
source definition, including the complete linked declaration and notices. It
uses `PrepareDynamicSourceRight`, compares the entire independently expected
inventory, and checks provider/executor identity and all canonical digests.
The fixed pin-list SHA-256, standalone definition SHA-256, and whole publisher
manifest SHA-256 are three different values. Only the genuine latter value can
bind the candidate `DefinitionDigest`.

Even with such a publisher, `blocked-candidate-metadata-verified` is only an
offline preparation disposition. `ovdb-http-source/1` still refuses execution.
Original publisher acceptance, code review, selected deployed path/sink/free
policy proof, deliberate controlled-admission transition, bounded live checks,
real browser/DataTug journeys and separately reviewed public activation remain
required. Keep provider payloads, result bodies, rates, HAR and payload
screenshots out of retained artifacts.

Synthetic tests exercise strict proposal parsing, old/unknown-Git refusal,
fixed-executable use despite PATH changes, original Git objects, missing promisor
objects with a discriminating fetch trap, publisher/notice/digest drift and real
`server.NewChecked` startup without a provider read. The existing CLI synthetic
provider tests cover populated/filtered-zero/error paths; seeing pinned decoder
test names is not evidence of running them. Every new package is included in
the existing full publisher race/atomic coverage command and 100% statement gate.
