---
ovdb: 1
publish:
  - ./publisher/source/ecb-daily/ovdb.yaml
  - ./publisher/source/ecb-daily/ovdb-database.json
---

OpenVaultDB authors the listed preparatory metadata and database descriptor.
The original data provider is the European Central Bank; the source remains its
publicly hosted daily HTTP XML resource. ECB does not author or endorse this Git
metadata. This repository stores no provider XML, rates, snapshots or responses.

The shared Cloud service addresses in the descriptor are proposed inactive
addresses, not live ECB routes or accepted executor rights identities. Directory
remains inactive. Source format `ovdb-http-source/1` disables execution, and all
B1–B4 gates remain open. Publishing or checking these files admits no execution.
The linked ECB terms and complete notices are preserved without asserting a
licence grant. CC0 applies only to our existing model and meaning metadata.
Independent semantic, artifact, executor/binding and rights inventory acceptance
is still required. Cloud PR25 landing and Cloud Run tests remain deferred.
