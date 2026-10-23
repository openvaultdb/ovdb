This is the native-geonames directory with its ModelSpec JSON documents (the target model and the source schema)
in the current vocabulary (identifier `1.0-draft-2`: records, fields, record) instead of the earlier one
(`1.0-draft`: entities, properties, entity). The contract document is the one of native-geonames with only the hashes
changed: its own fields (entity, property) keep their names and meaning.

How it was made, so that it can be checked (TestCurrentFixturesAreTheEarlierOnesRenamed does): each ModelSpec
JSON file was rewritten with `modelspec rewrite --write` (modelspec-org/cli 0.2.0), and then every SHA-256 that
the rewritten bytes change was replaced, in dependency order, in every file that holds it (the receipts, the
snapshot, the decision, the contract and references.json). No other byte differs. The rewritten files are not
the bytes published at the commits that references.json names; these are test fixtures, not captures.
