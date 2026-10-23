These are staged integration fixtures, not admitted production metadata. Source,
model, MeaningGraph and decision files preserve their pinned bytes. The exact ROR
binding derives `research-organization` from core `organization` in
`identity.meaning.yaml`; a concept name is not guessed as a file path.

The original source/validation.json is retained as original-validation.json.
The proposed additive native_key section was computed against the existing full
SQLite (141528 rows and distinct native IDs), and the metadata snapshot adds the
acyclic artifact/generator linkage needed by the proposed helper. These additions
are not present at the provider source pin and require independently reviewed
provider publication. No full SQLite, source dump or native key corpus is bundled.

The ROR user decision source is hub PR91 f8093950122e000aaccffefb7c478adeaf275fbf,
subsequently root-landed at 17263dbacabdfe95e53fc3c6980177416bdab941; its source and
provider scope pins are unchanged. Its acceptance does not establish runtime or
publication eligibility.
