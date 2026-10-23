# internal/publisher/manifest

The pure judge of a publisher's two documents, `OVDB.md` and the manifest
(`ovdb.yaml`): bytes in, findings and facts out. It is the second piece of
`ovdb publisher check`, a command that checks what a publisher keeps in their
repository; `ovdb publisher check` (package `internal/publisher/checkcmd`) runs it through
package `repo`, and the `ovdb` binary links it.

No file, no git, no network: the caller reads the two documents and this package
says what is wrong with them and what the caller needs to know to go on (the
paths the documents name, the form of the manifest, the model and graph
addresses, the recordset names), so nobody reads them twice.

```go
result := manifest.Check(ovdbMd, "ovdb.yaml", manifestBytes, manifest.Directory)
for _, f := range result.Findings { fmt.Println(f) } // OVDB.md:3: ...
result.OVDBMd.Entries       // the manifest paths OVDB.md lists, without "./", a set in first-seen order
result.Manifest.Form        // manifest.FormOwn or manifest.FormShared
result.Manifest.PublisherRepository.Usable() // written, and a repository URL
```

`Check` judges the pair; `CheckOVDBMd` and `CheckManifest` judge one document
(the second also returns the `Manifest` facts). There are two profiles, `Directory`
(the OVDB Directory's own rules) and `Publisher` (those, and what the Chinook checker
adds: the rules a publisher's own check holds a repository to). An unknown `Profile` judges
nothing and returns one finding, `profile-unknown`: the next profile is the
stricter one, so an unknown one is never judged by the Directory's rules.

## Findings

A `Finding` has a stable `Rule` (match on that, never on the message), a
`Severity`, the `Document`, the `Line` and a `Message` that says what is wrong and
what to write.

- **Line.** The reader gives a node the line where its **value** starts (for a
  block mapping or list, its first entry); it does not expose the line of a key.
  So a finding about a key that is written names the line of its value, which for
  a block value is below the key (`extra:` on line 2 with a block that starts on
  line 5 is reported on line 5); a finding about a key that is not written names
  the first line of the mapping that lacks it, or 1. The place to change this is
  the reader, not this package.
- **Bounds.** At most `MaxFindings` (100) findings are reported by one call, whatever
  the number of documents: the documents of a `Check` share the budget. When there
  are more, the last finding is `findings-capped` and says how many were left out;
  the verdict does not change. A message is at most `MaxMessageBytes` (400) bytes of
  printable ASCII; the bound is enforced in the one function that makes findings, so
  no input makes a longer one. A piece of a document reaches a message only through
  `rules.Quote`, which cuts it to 40 bytes, and a message that names entries (as
  `ovdbmd-unlisted` does) names the first five and how many more there are.
- **Document.** `Finding.Document` is the name the caller gave. `String()` prints
  `document:line: message` and quotes a document name that is not plain printable
  ASCII (and cuts it), so a finding never prints a control character or a line break.
- **Reader messages.** The reader is written for meaning files. Its one message that
  says so is reworded in `yaml.go`, the only file that names the reader, and a test
  holds every YAML finding of the corpus free of the words "meaning file".

## Facts: absent, usable, not usable

The Directory refuses a field that is written and unusable where it accepts the
same field left out (`publisher.repository: ""` is refused; the key removed is
accepted). So a fact that said `""` for both would let a caller accept what the
Directory refuses. Every field of `Manifest` and `OVDBMd` is a `Fact[T]` with
three states that a caller must tell apart:

| State | `Present` | `Valid` | `Value` |
| --- | --- | --- | --- |
| absent: the key is not written | false | false | zero (the zero `Fact` is absent) |
| present and usable | true | true | the value, as written |
| present and not usable: wrong type, blank, or refused by the profile's rule | true | false | zero, never the refused text |

`Usable()`, `Absent()` and `Unusable()` ask the state; `Line` is where the value
starts. `Manifest.Read` and `OVDBMd.Read` say whether the document was read as a
mapping at all: when it is false nothing was read, every fact is the zero `Fact`,
and a finding says why. Values are as written, never trimmed or coerced: a number
or a boolean where text is wanted is a written, unusable fact. The Directory profile
refuses every written fact that is not usable, so for a document with no finding
every fact is absent or usable; a caller of another profile that does not judge a
field still sees the difference.

Under the `Publisher` profile a fact that a rule of the profile refuses is demoted the
same way (an `id` that is not a lower-case id, a licence outside the list, a
`publisher.url` that is not `https://github.com/<owner>`): `Valid` always means usable
by the profile that judged.

`OVDBMd.Publish` is `publish:` (usable when it is a non-empty list in which every
entry is an explicit `./` path), its `Value` the entries without `./` as a set in
first-seen order, as the Directory's `published` is. `OVDBMd.Entries` is the same set
of the usable entries even when others are not, which is what `Lists(path)` asks, and
`OVDBMd.Repeated` the entries written more than once (the Directory profile accepts a
repeat; a profile that refuses it keeps it as a finding from this fact).

### The fields the Directory reads

The Directory reads these fields of a manifest after `manifestProblems`, in the
record stage and in the stages of the own and the shared form (lines of
`scripts/lib/directory.mjs` at the pinned commit; the generator fails if a cited
line no longer mentions its field, and a test holds this table to the generator's
list). Each is carried by one fact; the last rows are what the Directory derives
from `model.address` and `meaning.address`, and from the two keys that decide the
form.

| Field | Where the Directory reads it | Fact |
| --- | --- | --- |
| `format` | directory.mjs 208, 324 | `Format` |
| `id` | directory.mjs 209, 438 | `ID` |
| `title` | directory.mjs 209 | `Title` |
| `description` | directory.mjs 209 | `Description` |
| `url` | directory.mjs 121, 216, 221, 222, 436 | `URL` |
| `homepage` | directory.mjs 258, 259, 826 | `Homepage` |
| `deployment.url` | directory.mjs 122, 217, 825 | `DeploymentURL` |
| `deployment.engine` | directory.mjs 218, 825 | `Engine` |
| `deployment.discovery` | directory.mjs 219, 221, 222, 324, 430 | `Discovery` |
| `deployment.recordset_page` | directory.mjs 123, 225, 791, 837 | `RecordsetPage` |
| `model.modelspec` | directory.mjs 201, 244, 523, 524, 528, 530, 536, 558, 575, 584 | `ModelSpec` |
| `model.hcl` | directory.mjs 201, 226, 549 | `ModelHCL` |
| `model.address` | directory.mjs 229, 230, 245, 552, 553, 555, 556, 560, 592, 609 | `ModelAddress` |
| `model.name` | directory.mjs 536, 687 | `ModelName` |
| `meaning.address` | directory.mjs 233, 234, 246, 593, 610 | `MeaningAddress` |
| `meaning.file` | directory.mjs 237, 248, 505, 519, 525, 535, 541, 543, 547, 549, 594, 669, 673, 680 | `MeaningFile` |
| `meaning.graph.id` | directory.mjs 238, 249, 439 | `GraphID` |
| `meaning.graph.address` | directory.mjs 239, 250, 516, 636 | `GraphAddress` |
| `licences.model` | directory.mjs 241, 251, 624 | `LicenceModel` |
| `licences.meaning` | directory.mjs 241, 252, 535, 637 | `LicenceMeaning` |
| `licences.data` | directory.mjs 262, 831 | `LicenceData` |
| `publisher.name` | directory.mjs 254 | `PublisherName` |
| `publisher.url` | directory.mjs 255 | `PublisherURL` |
| `publisher.repository` | directory.mjs 440, 441 | `PublisherRepository` |
| `recordsets` | directory.mjs 267, 268, 278, 465 | `Recordsets` |
| `recordsets_partial` | directory.mjs 242, 247, 472, 483, 484 | `RecordsetsPartial` |
| `recordset_entities` | directory.mjs 272, 273, 445, 446 | `RecordsetEntities` |
| `form` | directory.mjs 201, 207, 444 | `Form` |
| `model.address.repository` | directory.mjs 229, 245, 553, 592, 699 | `ModelAddress.Repository` |
| `model.address.module` | directory.mjs 229, 245, 553, 592, 699 | `ModelAddress.Module` |
| `model.address.ref` | directory.mjs 229, 245, 553, 592, 699 | `ModelAddress.Ref` |
| `meaning.address.repository` | directory.mjs 233, 593 | `MeaningAddress.Repository` |
| `meaning.address.ref` | directory.mjs 233, 593 | `MeaningAddress.Ref` |

`OVDB.md`: `ovdb` (`analyseDatabase`, `frontmatter.ovdb !== 1`) is `OVDBMd.Version`;
`publish` (`frontmatter.publish`, each entry, and `published.has(data.manifest)`) is
`OVDBMd.Publish`, `OVDBMd.Entries` and `OVDBMd.Repeated`.

The table is complete: the generator finds the lines by reading `directory.mjs`
(a line reads a field when it names the whole path, in a chain such as
`manifest.meaning?.graph?.address`, in `need(manifest.meaning?.graph, 'address', ...)`
or in a loop over field names), and a test holds this table to its list. Some fields
are read only to be required: `title` and `description` of the manifest (the
published ones are the record's), and `publisher.name`, `publisher.url` and
`deployment.discovery` (whose origin is compared once). A key the Directory never
reads, an unknown key, is not a fact.

### What is judged now, and what the facts leave to the caller

What the Directory refuses after `manifestProblems` falls in two groups. Those that
need nothing but the two documents are judged here, with a rule of their own, so
that a document with no finding is one the Directory can still accept (the
reference's verdicts of the corpus include them):

| Field | Refused here when | Rule |
| --- | --- | --- |
| `publisher.repository` | written and not the https URL of a repository on github.com (a blank, a number, a list, `.git`, a trailing slash, a third segment), at most 255 bytes (line 315: the record's repository is a repository key, so only a repository URL can equal it) | `manifest-publisher` |
| `recordsets` | a name listed twice (`checkRecordsets`) | `manifest-recordsets` |
| `model.name` | written and not a module name (a letter or `_`, then letters, digits and `_`): it must equal the module, which `parseModelSpec` makes an identifier (lines 402, 552) | `manifest-model` |
| `model.address` (own form) | not on a repository of github.com in lower case, or with `?ref=` (lines 420-425) | `manifest-model` |
| `model.address`, `meaning.address` (shared form) | not on a repository of github.com in lower case (`spelled`, line 464) | `manifest-model`, `manifest-meaning` |

Those that need a record, a registry or a file are left to the caller, which has
the facts: that `url`, `id` and `meaning.graph.id` equal the record's; that
`publisher.repository` and the addresses name the record's repository (or, in the
shared form, not it); that `model.name` equals the module of the ModelSpec; that the
licences equal the meaning file's or the registries'; that the files exist and
`model.hcl` is the meaning file's `models:` entry; the recordsets against the model's
entities.

## Profiles

A `Profile` says whose rules judge, and is an argument of every function, so that a
profile is added without changing a signature. There are two. `Publisher` runs every
rule of `Directory` first and adds what the Chinook checker adds; a fact that a rule of
`Publisher` refuses is demoted, present and not usable, like any other.

| | `Directory` | `Publisher` |
| --- | --- | --- |
| Whose | The OVDB Directory's own rules for `OVDB.md` and a manifest | The rules a publisher's own check holds a repository to: the Directory's, and the Chinook checker's |
| Reference | `scripts/lib/directory.mjs` of `openvaultdb/directory` | `scripts/lib/ovdb-manifest.mjs` of `datatug/chinookdb`, run on a repository |
| `OVDB.md` | YAML front matter with `ovdb: 1` and a non-empty `publish` list of `./` paths, which must list the manifest; unknown keys and a repeated entry are accepted | And no key but `ovdb` and `publish`, and no entry twice (`Repeated` is a finding) |
| Manifest keys | Unknown keys are accepted | Only the keys of the manifest, `deployment`, `model`, `meaning`, `meaning.graph`, `publisher` and `licences` that the checker allows |
| `id` | Text | Lower-case letters, digits and single hyphens, at most 80 characters |
| URLs | Public https; the canonical `url` is a global database identity (a trailing slash, several segments, canonical percent-encoded segments and a `.example` host are fine; no `ovdb` marker is asked); discovery on the host of `url` | And discovery is exactly `/.well-known/openvaultdb`, `deployment.recordset_page` is on the origin of `deployment.url`, `publisher.url` is `https://github.com/<owner>`, and every page the template makes is a public https URL |
| `publisher.repository` | Optional; when written, a github.com repository | Required, and owned by the owner of `publisher.url` |
| Addresses and names | A repository of github.com in lower case; own form without `?ref=`, shared form pinned | And a module name starts with a letter; `model.name` is the module of `model.address`; the own form's `model.address` is this repository, and the shared form's addresses are not |
| Own form | `model.modelspec` and `meaning.file` required | And `model.hcl` required, `model.modelspec` ends in `.modelspec.json`, `meaning.graph.address` is `publisher.repository` as an address |
| Shared form | `meaning.graph.address`, when given, is a `meaning://` address | And it is `meaning.address` without its pin |
| Licences | An SPDX-shaped id | One of 18 ids |
| `meaning.graph.id` | Text | Lower-case letters, digits and single hyphens |
| Recordsets | A non-empty list of native names, each once, bounded to 256 UTF-16 code units and free of path separators, dot segments and controls | Same native-name rules; `recordset_entities` maps names to distinct ModelSpec entity identifiers |

Both judge `homepage`, the engine, the form and the number of keys the same way.

### What the Publisher profile adds, and who decides it

Every rule the Chinook checker applies beyond the Directory's, with the lines of the
checker that make it (the generator fails if a line no longer holds its rule, and a test
holds this table to the generator's list). Who decides it: `documents`, the two
documents alone, so the Publisher profile makes it here with the rule of its own named;
`files`, a file at HEAD (a model, a meaning file, a manifest that OVDB.md lists), and
`input`, something the caller gives (the `--repository` option). Package `repo` (slice 3b-1) makes the
ones about the presence and readability of files and the `--repository` option, each by the rules of
its README; the ones that need the content of the model file or the meaning file are slice 3b-2's, made by package repo and by `Judge.Meaning` here.

| Rule | Who | Rule of Go | Lines |
| --- | --- | --- | --- |
| unknown keys at every level of a manifest | documents | `manifest-keys` | ovdb-manifest.mjs 277, 280, 281 |
| id is a lower-case id of at most 80 characters | documents | `manifest-id` | ovdb-manifest.mjs 286 |
| deployment.discovery is on the origin of url, at /.well-known/openvaultdb | documents | `manifest-discovery` | ovdb-manifest.mjs 315, 316 |
| deployment.recordset_page is on the origin of deployment.url | documents | `manifest-url` | ovdb-manifest.mjs 321 |
| publisher.url is https://github.com/<owner> | documents | `manifest-publisher` | ovdb-manifest.mjs 328 |
| publisher.repository is required, a github.com repository, owned by the owner of publisher.url | documents | `manifest-publisher` | ovdb-manifest.mjs 331, 332, 334 |
| model.address names a repository of github.com and a module that starts with a letter | documents | `manifest-model` | ovdb-manifest.mjs 82, 345, 346 |
| model.name is a module name that starts with a letter | documents | `manifest-model` | ovdb-manifest.mjs 348, 349 |
| model.name is the module of model.address (shared form; in the own form the checker gets the same through the model file) | documents | `manifest-model` | ovdb-manifest.mjs 515, 516 |
| shared form: neither address is the publisher's own repository | documents | `manifest-model`, `manifest-meaning` | ovdb-manifest.mjs 359, 360, 514, 528 |
| own form: model.hcl is required, model.modelspec ends in .modelspec.json | documents | `manifest-required`, `manifest-model` | ovdb-manifest.mjs 407, 416, 417 |
| own form: model.address is this repository (publisher.repository), without a pin | documents | `manifest-model` | ovdb-manifest.mjs 455, 456, 457, 458 |
| meaning.graph.id is a registry id (lower-case letters, digits, single hyphens) | documents | `manifest-meaning` | ovdb-manifest.mjs 466, 536 |
| own form: meaning.graph.address is publisher.repository as an address, in any case | documents | `manifest-meaning` | ovdb-manifest.mjs 505, 506, 507 |
| shared form: meaning.graph.address, when given, is meaning.address without its pin | documents | `manifest-meaning` | ovdb-manifest.mjs 539, 540 |
| licences are known SPDX atoms; data permits bounded conjunctions | documents | `manifest-licence` | ovdb-manifest.mjs 385, 388 |
| recordsets are names that look like ModelSpec entities | dropped | none (D0) | ovdb-manifest.mjs 554, 555 |
| every recordset page the template makes is a public https URL | documents | `manifest-recordsets` | ovdb-manifest.mjs 557, 558, 559, 560 |
| OVDB.md has no key but ovdb and publish | documents | `ovdbmd-keys` | ovdb-manifest.mjs 223, 224 |
| publish lists each manifest once | documents | `ovdbmd-duplicate` | ovdb-manifest.mjs 241, 242 |
| the repository can be read at HEAD (it is a git repository with a commit) | files | package repo: `repo-unreadable`, `repo-no-commit`, `repo-bare`, `repo-subdirectory`, `repo-git-version` | ovdb-manifest.mjs 211, 212 |
| OVDB.md is a tracked regular file | files | package repo: `repo-ovdbmd` | ovdb-manifest.mjs 213, 214 |
| OVDB.md can be read (and is not over 16 MB) | files | package repo: `document-size`, `repo-object-missing`, `repo-object-corrupt`, `repo-partial-clone`, `repo-alternates` | ovdb-manifest.mjs 219 |
| every manifest that OVDB.md lists is a tracked regular file | files | package repo: `repo-manifest` | ovdb-manifest.mjs 246, 247, 248 |
| every manifest that OVDB.md lists can be read (and is not over 16 MB) | files | package repo: `document-size`, `repo-object-missing`, `repo-object-corrupt`, `repo-partial-clone`, `repo-alternates` | ovdb-manifest.mjs 269, 271 |
| every manifest that OVDB.md lists is checked | files | package repo: `manifest.Judge` | ovdb-manifest.mjs 251 |
| every file a manifest names is a tracked regular file | files | package repo: `repo-file` | ovdb-manifest.mjs 420, 421 |
| every file a manifest names can be read (and is not over 16 MB) | files | package repo: `repo-file-size`, `repo-object-missing`, `repo-object-corrupt`, `repo-partial-clone`, `repo-alternates` | ovdb-manifest.mjs 371, 373 |
| the model file is JSON with a module name and entities | files | package repo: `repo-model-json`, `repo-model-depth`, `repo-model-module`, `repo-model-entities` | ovdb-manifest.mjs 437, 439, 442, 444 |
| own form: model.name is the module of the model file | files | package repo: `repo-model-name` | ovdb-manifest.mjs 449 |
| own form: the module of model.address is the model file's | files | package repo: `repo-model-address` | ovdb-manifest.mjs 456, 458 |
| the meaning file is YAML whose id and license are the manifest's | files | package manifest, `Judge.Meaning`: `meaning-shape`, `meaning-id`, `meaning-license`, and the reader's rules | ovdb-manifest.mjs 477, 480, 482, 484 |
| the meaning file's models: entry for the module is model.hcl | files | package manifest, `Judge.Meaning`: `meaning-models`, `meaning-hcl` | ovdb-manifest.mjs 488, 490, 496, 497 |
| own form: recordsets are exactly the model's entities | files | package repo: `repo-recordsets` | ovdb-manifest.mjs 564, 567, 568 |
| the optional attachment has a locally checked structural precheck; external closure remains partial | files | package repo: structural metadata associations and required format3 raw data proofs; no canonical admission | ovdb-manifest.mjs 573 |
| a JSON database descriptor uses its separate pinned schema | files | package manifest, `Judge.Descriptor`: the Directory's structural rules (`descriptor-*`); the pinned JSON schema that the Chinook companion runs with ajv is not run (see the descriptor section) | ovdb-manifest.mjs 587, 591, 597, 605, 607, 609 |
| publisher.repository is the repository the check is run in (the --repository option) | input | package repo: `repo-repository` | ovdb-manifest.mjs 335 |

Not judged, whatever the profile: that the named files are tracked regular files at HEAD
and of a size, and anything read through a registry. A required text is checked with
`rules.IsBlank`, never `strings.TrimSpace` (JavaScript's `trim()` and Go's differ on
U+FEFF and U+0085).

## Reading and limits

Both documents are read with the strict reader of
`github.com/meaninggraph/cli/pkg/meaning` (`ParseYAML`), through one file,
`yaml.go`, which is the only place that names it. It reads a subset of YAML 1.2
(no anchors, aliases, tags, merge keys, explicit keys, duplicate keys,
directives or second documents; numbers canonicalised; plain booleans only) and
claims to agree with the `yaml` npm package on every document it accepts.
Nothing is coerced: a number, a boolean or a null where the rules want text is a
finding, never read as the text it would print as.

| Bound | Value | Where it comes from |
| --- | --- | --- |
| `MaxDocumentBytes` | 256 KiB per document, checked before parsing (rule `document-size`) | this package; the references have no bound |
| `MaxFindings` | 100 per call | this package |
| `MaxMessageBytes` | 400 per message | this package |
| reader limits | 8 MiB file, nesting 64 | the reader |
| file paths (`model.modelspec`, `model.hcl`, `meaning.file`, a `publish` entry) | `rules.MaxPathLength`, 1024 bytes | package `rules` |
| addresses | `rules.MaxURLLength`, 2048 bytes; the repository in one, at most 247 bytes | this package |
| `publisher.repository` | `rules.MaxRepositoryLength`, 255 bytes | package `rules` |
| URL, id, engine, licence lengths | those of package `rules` | see its README |

A length refusal says the length and the limit, not that the field is missing.

## The proof, Directory profile

The rule is the one of package `rules`: **the Go function never accepts a pair of
documents that the JavaScript of the profile refuses**. The reference of the
`Directory` profile is `openvaultdb/directory@ec53d7539aafd23d006b4943acdd7a31f4eb9340`: `parseFrontmatter` and `manifestProblems` of
`scripts/lib/directory.mjs`, called as they are; the `OVDB.md` checks that
`analyseDatabase` keeps inline, and the record-stage refusals of the table above,
composed from the same expressions (the generator fails if the pinned file no
longer holds them as copied). The documents are read as the Directory reads them: as
UTF-8, by the `yaml` package with its default options. `datatug/chinookdb@79e7bb0b1d6f0666dce465874990dec64348331f` supplies real
documents and the test suites to mine.

`testdata/reference/generate.mjs` builds a corpus (`corpus.json`), the Directory's
verdict on each document (`directory.verdicts.json`), and the values its own code
derives for every field of the table above from each accepted manifest
(`directory.facts.json`, as the difference from the facts of the document's base).
`go test` reads them, applies the Go functions, starts no process and needs no
network. The corpus is **7401 manifests and 658 OVDB.md documents** (1032
KiB), stored as patches of whole lines against a few base documents (the real
Chinook manifest and `OVDB.md`, the hoster example, the Directory's own fixture,
JSON spellings of the manifests), with flags for CRLF, a byte-order mark, invalid
UTF-8 and padding to an exact size; Node v24.19.0 made the committed ones:

- every field removed, renamed, given each of ten wrong types, given each
  white-space and format character alone (U+0000 to U+3000 and the zero-width and
  byte-order characters, JavaScript's `trim()` and not Go's), and given an
  unknown sibling, in an own and a shared manifest;
- each URL field replaced by URLs mined from the two JavaScript test suites and
  by hand-written odd ones (ports, userinfo, dot segments, punycode, reserved
  suffixes, trailing slashes, a template where none is allowed, over-long);
- the forms mixed: every combination of the form-defining fields
  (`model.modelspec`, `model.hcl`, `model.address`, `meaning.address`,
  `meaning.file`), and up to two of the other toggles and removals;
- `publisher.repository`, `model.name`, `recordsets` and the addresses in every
  spelling the record stage judges, and paths, licences, engines and addresses at
  and over their bounds;
- the single-field edits of both test suites (`(m) => { ... }`) applied to the
  own and the shared form: **200 of 300** found were applicable alone;
- the YAML text mutated line by line and spelled differently (CRLF, a lone CR, a
  byte-order mark, a Latin-1 byte, `---`, `...`, directives, several documents,
  keys that are numbers, escapes, `.inf`, integers beyond 2^53, documents at, and
  over, 262144 bytes);
- about 400 `OVDB.md` documents: front matter edge cases, every `ovdb` and
  `publish` value shape, white space in and around an entry, repeated entries,
  entries at and over the length bound, paths that are and are not listed.

`TestReferenceDirectory` fails if any Go function accepts where the reference
refuses, if Go is stricter in a way that is not recorded (the shared kinds, in the
section on the reader below), if a recorded kind never happens in the corpus (but where
every place of the reader that makes it is shown to have none), or if this file's
numbers are stale (it checks the commits, the counts and every row of the tables). `TestFactsAgreeWithTheReference`
compares every fact of every document that both accept with what the Directory's
code derives, and holds the table of fields to the generator's. `TestGoldenDigests`
holds every golden of both slices to its SHA-256 in `digests.json`, so a hand edit of
a golden fails until `generate.mjs` is run again. `go test -v -run
'TestReferenceDirectory|TestFacts' ./internal/publisher/manifest` prints the numbers.

On the corpus: **7261 agree, 798 stricter, 0 unrecorded Go acceptances where the
Directory refuses**.

On the facts: 2212 manifests and 267 OVDB.md documents have their facts compared.

For every manifest the Go reader reads, accepted or refused, the presence of every field
is compared with the reference's parsed manifest (`TestPresenceAgreesWithTheReference`): the
facts that are `Present` are the fields the reference has, so a written value that is
refused can never become an absent fact. The presence of every field is compared on 5519 manifests, 3307 of them refused.

## The proof, Publisher profile

The rule is the same: **the Go function never accepts a pair of documents that the
reference of the profile refuses**. The reference is the Chinook checker of
`datatug/chinookdb` at its pinned commit, `reportOvdbManifest` of
`scripts/lib/ovdb-manifest.mjs`, called as it is. It reads a git repository at HEAD, so
each case is run on a repository: held in memory through the `files` object that the
checker's own tests use for their hundreds of cases (`problem`, `read`, `kind`), and
again on a real throwaway repository (`git init`, the files, `git commit`, the checker's
own `gitRepoFiles`) for a sample of 366 runs, one directory of its own each, removed
afterwards. The script fails if the two ever differ.

What the checker refuses falls in two groups, found by running each case on four
repositories. The *consistent* repository holds every file the documents name, with the
contents that agree with them (the model of the module that `model.address` or
`model.name` names, with the recordsets as its entities; a meaning file with the graph's
id, the meaning licence and the `models:` entry that is `model.hcl`): what the checker
still refuses is refused **by the two documents alone**, and is this slice's. The *bare*
repository holds only `OVDB.md` and the manifest; the *wrong* one has well formed files
that disagree with the manifest; the *broken* one has files that are not JSON and not YAML.
What these refuse and the consistent one does not **needs other files** and is
slice 3's. An `OVDB.md` case lists the real Chinook manifest under each entry, so that only
`OVDB.md` can be wrong. Of the 1389 manifests and 130 OVDB.md documents that the
checker accepts, these classes of refusal would follow from files (manifests, OVDB.md
documents):

- a tracked regular file (753 manifests, 130 OVDB.md documents)
- model.address against the model file (743 manifests, 130 OVDB.md documents)
- model.name against the model file (5 manifests, 0 OVDB.md documents)
- recordsets against the model (753 manifests, 130 OVDB.md documents)
- the meaning file against the manifest (753 manifests, 130 OVDB.md documents)
- the model file: JSON, module and entities (753 manifests, 130 OVDB.md documents)

The corpus is the one of the Directory profile, **7401 manifests and 658
OVDB.md documents**, judged again by this reference (it accepts 1389 manifests and
130 OVDB.md documents and refuses 6012 and 528), with the rows aimed at what
only this profile refuses: every manifest edit of the checker's own test suite that
applies on its own (the single-field edits of `scripts/test-model.mjs`, with bodies of
several lines too), each licence id in and out of the list and in other letter case in
each of the three fields, discovery on its own origin with another path and on other
origins, `recordset_page` on other origins, `publisher.url` and `publisher.repository`
with another owner, another host, a trailing slash, a path and a missing key, `id`
spellings, `model.hcl` and `model.modelspec` missing and mistyped, recordset names that
are not identifiers and pages that would be too long, an unknown key in every mapping and
a known key at the wrong level, names and addresses of the two forms, and `OVDB.md` with
unknown keys, and entries that repeat or nearly repeat.

On the corpus: **7506 agree, 486 stricter, 0 unrecorded Go acceptances where the Chinook
checker refuses**. The facts: under the Publisher profile, 929 manifests and 104
OVDB.md documents have their facts compared with those the reference derives
(`publisher.facts.json`), by the same code as the Directory's.

**Cross-profile.** Over the whole corpus of both goldens, the Publisher profile refuses
every one of the 5189 manifests, 366 OVDB.md documents and 5960 pairs (of 8717) that the
Directory profile refuses (`TestPublisherRefusesWhatTheDirectoryRefuses`: each manifest
and each OVDB.md alone, and in pairs with the real Chinook documents, under every path
the corpus names).

## The reader and the bounds, for both profiles

Both profiles read their documents with one reader, `github.com/meaninggraph/cli` (package `meaning`, `ParseYAML`, behind `yaml.go` of this
package), and bound the size of a document before it is read. What the reader or a bound refuses is a property of them, not of a profile,
and it is recorded once, here, for both: the Directory's rules (the lengths and the punycode rule) run first under the Publisher profile too,
so a kind in the first table is a kind of both profiles, with a count for each, and cannot be recorded for one profile and be missing for the
other. Only the kinds of rules that one profile alone has are listed apart, for the Publisher profile, in the last table of this section.

### Recorded differences: shared by both profiles

Go may refuse what the JavaScript accepts. Each kind is the rule of the first finding Go makes (a length refusal is named by what is long),
with its number of documents in the corpus under each profile; a document counts only if the profile's reference accepts it, so each kind is
real under that profile. Most are not ordinary manifests (a lone surrogate escape, a byte that is not UTF-8, nesting 64 deep); the exceptions
are `yaml-unsupported` (a manifest that a YAML tool has written back, with a long string folded over several lines in a quoted value, is
refused until the reader reads it, meaninggraph/cli#7; the message tells the publisher to write the value as a block scalar, `>-` or `|-`) and
`yaml` (a plain value that continues on the next line with a character such as `*` or `"` at its start, which the references read as part of the
value). A kind with no document under a profile (`yaml-limit` under the Publisher profile) has none because each place of the reader that makes
it has none there: see the table of places.

| Kind | Directory | Publisher | Why |
| --- | --- | --- | --- |
| `document-size` | 6 | 6 | A document over 262144 bytes (MaxDocumentBytes) is refused before it is read; the references read files of any size. |
| `length-address` | 7 | 7 | An address over 2048 bytes, or one that names a repository over 247 bytes; the references' address expressions have no bound. |
| `length-entry` | 2 | 4 | A publish entry whose path after ./ is over 1024 bytes; the references have no bound. |
| `length-path` | 8 | 6 | A file path over 1024 bytes in model.modelspec, model.hcl or meaning.file; the references have no bound. |
| `length-repository` | 4 | 2 | A publisher.repository over 255 bytes; the references have no bound. |
| `punycode` | 4 | 4 | A homepage host with an xn-- label that does not spell Latin-1 letters (see the README of package rules); Node accepts the label. |
| `url-length` | 5 | 2 | A URL longer than rules.MaxURLLength (2048 bytes) is refused; the reference has no bound. |
| `yaml` | 86 | 33 | The reader accepts a subset of YAML and refuses a structure it cannot place: a plain value that continues on the next line with a character such as * or " at its start, a flow collection used as a key, an explicit key or an entry with no value in a flow collection, and the other places of the table below; the references read them. |
| `yaml-anchor` | 68 | 56 | The reader refuses anchors and aliases (& and *): it reads a document once, as written, and expanding references is how a small file becomes a large one. |
| `yaml-character` | 25 | 12 | The reader refuses characters that YAML 1.2 does not allow in text, among them the C1 controls such as U+0085; the reference reads them into a string. |
| `yaml-directive` | 6 | 6 | The reader refuses a %YAML or %TAG directive; the reference follows it. |
| `yaml-documents` | 10 | 10 | The reader refuses a document end marker (`...`) and a second document; the reference reads the first document and ignores what follows. |
| `yaml-encoding` | 16 | 12 | The reader refuses a file that is not UTF-8 text (a Latin-1 byte, a NUL character); the reference, which reads a file as UTF-8, replaces the bytes it cannot decode and goes on. |
| `yaml-escape` | 40 | 18 | The reader refuses a double-quoted escape that is not a character, such as half of a surrogate pair (\ud83c); the reference accepts it. |
| `yaml-key` | 50 | 8 | The reader refuses a key that YAML reads as a number, a boolean or null (2024, true, null) and wants it in quotes; the reference accepts it as a key. |
| `yaml-limit` | 14 | 0 | The reader refuses collections nested more than 64 levels deep (63 is read); the reference reads any depth. |
| `yaml-line-ending` | 8 | 4 | The reader refuses a carriage return that is not part of CRLF; the reference reads it as a line break. |
| `yaml-number` | 76 | 6 | The reader refuses numbers it cannot hold exactly or that are not finite: hexadecimal and octal numbers, .inf, .nan, and integers beyond 2^53; the reference reads them as numbers. |
| `yaml-tab` | 92 | 74 | The reader refuses a tab where YAML allows it but whose reading differs between parsers (after a colon, in indentation). |
| `yaml-tag` | 104 | 94 | The reader refuses tags (!, !!), which the reference resolves; it reads plain values only. |
| `yaml-unsupported` | 167 | 113 | The reader refuses constructs outside its subset: explicit keys (`? key`), and a quoted value written over more than one line, which a YAML tool writes back for any long string (the message asks for a block scalar, `>-` or `|-`; meaninggraph/cli#7); the reference reads both. |

### The places of the reader

Every line of the reader (`yaml.go` and `yaml_flow.go` of `meaninggraph/cli`, at the version of `go.mod`) that makes a refusal is a row here, and,
where a function that several places call makes it (`resolvePlain` for numbers, `scanQuoted` and `unescape` for quoted values, `keyProblem`), one row
for each caller that an allowed key can reach (the quoted value of a block key is not one: `splitKey` drops the error, and the line is then no key
line). `TestReaderPlaces` reads the reader's source from the module that go builds (`go list -m -json`, which follows a `replace`; with the reader replaced
by a copy that has one more refusing line, `TestReaderSourceFollowsReplace` shows the table found out of date) and fails if a line that makes a
refusal is not a row, or a row is not such a line, so the table is complete for the version in use, and a new version fails it until it is read
again. The wrapper of this package, `parseYAML`, makes no refusal of its own, it rewords the reader's messages; the bound on a document's size
(`document-size`) is checked in `check.go`, before the reader runs.

A refusal belongs to the row of the line that made it and of the caller of that line, not to a family of the corpus and not to a fragment of its message.
The test copies the reader's module into a temporary directory, changes the one line of `yaml.go` that makes a `SyntaxError` so that the error carries the
lines of the reader that were running (the function that does it is in a file of its own, nothing moves), builds `testdata/chain` against the copy with
`go run -modfile` (the go tool does not let an overlay replace a file of the module cache, so a copy and a `replace` it is), and runs the whole corpus
through it: the reader that is built, instrumented in a copy; the repository's `go.mod` and sources are not touched, the shipped binary does not contain
any of it, and it needs the go tool and nothing from the network. The innermost line of a chain that is a row is the place; a row with a caller after `@`
is the place when the chain goes through the line of that caller, and the row of the same line without a caller when it does not. A refusal that
reaches no row, or a caller that has none, fails the test.

In the corpus each place has documents aimed at it, the family `reader place: <id>`: the real Chinook manifest (and the hoster example, and OVDB.md) with
one line replaced, and the same as an extra unknown key, which only the Directory accepts. The count of a place is of all the documents of the corpus that
the reader refuses there, whatever family they are from. In a cell, the first number is the number of those documents that the profile's reference accepts
(the kind is real there); the second is the number that it refuses too. A profile with no document of the first kind at a place says why, in a form that
the test checks: `evidence` means that the reference refuses every document of the corpus that the reader refuses at the place, which the second number
shows (a search, not a proof: a document that shows otherwise fails the test, and the search found fifteen such documents for the first version of this
table, all now in the corpus); `proof` is a claim that the test computes: the reader's bound on the size of a file (8 MiB) lies above this package's
(256 KiB), and a manifest that the Publisher profile accepts nests collections at most as deep as the longest path of `allowedKeys` plus one, far below 64.

| Place | Rule | Directory | Publisher | Why a profile has none |
| --- | --- | --- | --- | --- |
| `yaml.go:124` | yaml-limit | 0 / 0 | 0 / 0 | both profiles: proof: size |
| `yaml.go:148` | yaml-encoding | 4 / 2 | 4 / 2 | both profiles have documents |
| `yaml.go:171` | yaml-encoding | 8 / 0 | 6 / 2 | both profiles have documents |
| `yaml.go:173` | yaml-encoding | 4 / 8 | 2 / 10 | both profiles have documents |
| `yaml.go:178` | yaml-line-ending | 8 / 15 | 4 / 19 | both profiles have documents |
| `yaml.go:182` | yaml-character | 25 / 208 | 12 / 221 | both profiles have documents |
| `yaml.go:295` | yaml-tab | 8 / 58 | 4 / 62 | both profiles have documents |
| `yaml.go:320` | yaml-limit | 2 / 2 | 0 / 4 | Publisher: proof: depth |
| `yaml.go:332` | yaml-directive | 6 / 2 | 6 / 2 | both profiles have documents |
| `yaml.go:341` | yaml-tab | 4 / 0 | 4 / 0 | both profiles have documents |
| `yaml.go:345` | yaml-documents | 4 / 2 | 4 / 2 | both profiles have documents |
| `yaml.go:352` | yaml-documents | 6 / 4 | 6 / 4 | both profiles have documents |
| `yaml.go:365` | yaml | 0 / 9 | 0 / 9 | both profiles: evidence: the reference refuses the same layouts |
| `yaml.go:399` | yaml-unsupported | 8 / 0 | 4 / 4 | both profiles have documents |
| `yaml.go:436` | yaml-tab | 2 / 0 | 2 / 0 | both profiles have documents |
| `yaml.go:462` | yaml | 0 / 118 | 0 / 118 | both profiles: evidence: the reference refuses the same layouts |
| `yaml.go:464` | yaml-tab | 7 / 2 | 5 / 4 | both profiles have documents |
| `yaml.go:466` | yaml | 0 / 6 | 0 / 6 | both profiles: evidence: the reference refuses a dash where a key belongs |
| `yaml.go:473` | yaml | 8 / 118 | 0 / 126 | Publisher: evidence: the reference refuses a line that is no entry |
| `yaml.go:476` | yaml-duplicate-key | 0 / 109 | 0 / 109 | both profiles: evidence: a repeated key is an error of the reference too |
| `yaml.go:509` | yaml | 0 / 2 | 0 / 2 | both profiles: evidence: the reference refuses the same layouts |
| `yaml.go:511` | yaml-tab | 5 / 0 | 5 / 0 | both profiles have documents |
| `yaml.go:557` | yaml-tab | 4 / 0 | 2 / 2 | both profiles have documents |
| `yaml.go:563` | yaml-tab | 4 / 0 | 2 / 2 | both profiles have documents |
| `yaml.go:571` | yaml-anchor | 4 / 4 | 2 / 6 | both profiles have documents |
| `yaml.go:573` | yaml-tag | 8 / 0 | 4 / 4 | both profiles have documents |
| `yaml.go:578` | yaml-unsupported | 64 / 0 | 58 / 6 | both profiles have documents |
| `yaml.go:586` | yaml-tab | 46 / 0 | 44 / 2 | both profiles have documents |
| `yaml.go:594` | yaml-key | 24 / 0 | 0 / 24 | Publisher: evidence: no key that the checker allows reads as a number, a boolean or null, and it refuses the others |
| `yaml.go:628` | yaml-key | 4 / 8 | 0 / 12 | Publisher: evidence: the reference refuses a block key over 1024 characters too |
| `yaml.go:628@flow key` | yaml-key | 5 / 0 | 3 / 2 | both profiles have documents |
| `yaml.go:630` | yaml-anchor | 4 / 0 | 0 / 4 | Publisher: evidence: << is not a key that the checker allows |
| `yaml.go:630@flow key` | yaml-anchor | 2 / 2 | 0 / 4 | Publisher: evidence: << is not a key that the checker allows |
| `yaml.go:643` | yaml-tab | 4 / 0 | 2 / 2 | both profiles have documents |
| `yaml.go:646` | yaml-unsupported | 10 / 0 | 6 / 4 | both profiles have documents |
| `yaml.go:654` | yaml-anchor | 52 / 44 | 50 / 46 | both profiles have documents |
| `yaml.go:656` | yaml-tag | 90 / 0 | 86 / 4 | both profiles have documents |
| `yaml.go:658` | yaml | 0 / 24 | 0 / 24 | both profiles: evidence: the reference refuses a value that starts so |
| `yaml.go:661` | yaml-tab | 4 / 12 | 0 / 16 | Publisher: evidence: the reference refuses a value that starts so |
| `yaml.go:664` | yaml | 0 / 24 | 0 / 24 | both profiles: evidence: the reference refuses a value that starts so |
| `yaml.go:696` | yaml | 64 / 0 | 33 / 31 | both profiles have documents |
| `yaml.go:735` | yaml | 2 / 84 | 0 / 86 | Publisher: evidence: the reference refuses a colon and a space in a plain value |
| `yaml.go:784@block key` | yaml-number | 2 / 0 | 0 / 2 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:784@block value` | yaml-number | 7 / 3 | 0 / 10 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:784@flow key` | yaml-number | 2 / 0 | 0 / 2 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:784@flow value` | yaml-number | 2 / 3 | 0 / 5 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:788@block key` | yaml-number | 6 / 0 | 0 / 6 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:788@block value` | yaml-number | 12 / 44 | 4 / 52 | both profiles have documents |
| `yaml.go:788@flow key` | yaml-number | 4 / 0 | 0 / 4 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:788@flow value` | yaml-number | 6 / 6 | 2 / 10 | both profiles have documents |
| `yaml.go:790@block key` | yaml-number | 6 / 0 | 0 / 6 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:790@block value` | yaml-number | 13 / 44 | 0 / 57 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:790@flow key` | yaml-number | 4 / 0 | 0 / 4 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:790@flow value` | yaml-number | 4 / 6 | 0 / 10 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:794@block key` | yaml-number | 2 / 0 | 0 / 2 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:794@block value` | yaml-number | 2 / 3 | 0 / 5 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:794@flow key` | yaml-number | 2 / 0 | 0 / 2 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:794@flow value` | yaml-number | 2 / 3 | 0 / 5 | Publisher: evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1 |
| `yaml.go:825@block scalar header` | yaml | 0 / 10 | 0 / 10 | both profiles: evidence: the reference refuses text after the end of a value |
| `yaml.go:825@flow` | yaml | 2 / 8 | 0 / 10 | Publisher: evidence: the reference refuses text after the end of a value |
| `yaml.go:825@quoted value` | yaml | 0 / 12 | 0 / 12 | both profiles: evidence: the reference refuses text after the end of a value |
| `yaml.go:864@block value` | yaml-unsupported | 19 / 0 | 14 / 5 | both profiles have documents |
| `yaml.go:864@flow` | yaml-unsupported | 9 / 4 | 5 / 8 | both profiles have documents |
| `yaml.go:871@block value` | yaml-unsupported | 6 / 0 | 4 / 2 | both profiles have documents |
| `yaml.go:871@flow` | yaml-unsupported | 6 / 0 | 2 / 4 | both profiles have documents |
| `yaml.go:878@block value` | yaml-escape | 4 / 10 | 2 / 12 | both profiles have documents |
| `yaml.go:878@flow` | yaml-escape | 2 / 12 | 2 / 12 | both profiles have documents |
| `yaml.go:882@block value` | yaml-escape | 0 / 8 | 0 / 8 | both profiles: evidence: the reference refuses an escape without its digits |
| `yaml.go:882@flow` | yaml-escape | 0 / 12 | 0 / 12 | both profiles: evidence: the reference refuses an escape without its digits |
| `yaml.go:892@block value` | yaml-escape | 12 / 0 | 6 / 6 | both profiles have documents |
| `yaml.go:892@flow` | yaml-escape | 12 / 0 | 4 / 8 | both profiles have documents |
| `yaml.go:897@block value` | yaml-escape | 4 / 8 | 2 / 10 | both profiles have documents |
| `yaml.go:897@flow` | yaml-escape | 6 / 12 | 2 / 16 | both profiles have documents |
| `yaml_flow.go:14` | yaml-unsupported | 8 / 0 | 4 / 4 | both profiles have documents |
| `yaml_flow.go:20` | yaml-unsupported | 8 / 0 | 4 / 4 | both profiles have documents |
| `yaml_flow.go:51` | yaml-unsupported | 8 / 2 | 4 / 6 | both profiles have documents |
| `yaml_flow.go:169` | yaml | 0 / 4 | 0 / 4 | both profiles: evidence: the reference refuses a collection that is not closed |
| `yaml_flow.go:178` | yaml-unsupported | 2 / 0 | 2 / 0 | both profiles have documents |
| `yaml_flow.go:182` | yaml | 0 / 2 | 0 / 2 | both profiles: evidence: the reference refuses a continuation that is not indented |
| `yaml_flow.go:186` | yaml-unsupported | 2 / 0 | 2 / 0 | both profiles have documents |
| `yaml_flow.go:202` | yaml-anchor | 2 / 0 | 2 / 0 | both profiles have documents |
| `yaml_flow.go:204` | yaml-tag | 2 / 0 | 2 / 0 | both profiles have documents |
| `yaml_flow.go:206` | yaml | 0 / 8 | 0 / 8 | both profiles: evidence: the reference refuses an empty entry and the others |
| `yaml_flow.go:210` | yaml | 6 / 6 | 0 / 12 | Publisher: evidence: the reference refuses a value that starts so |
| `yaml_flow.go:240` | yaml-tab | 4 / 0 | 4 / 0 | both profiles have documents |
| `yaml_flow.go:254` | yaml-unsupported | 4 / 1 | 4 / 1 | both profiles have documents |
| `yaml_flow.go:256` | yaml | 0 / 10 | 0 / 10 | both profiles: evidence: the reference refuses text after a value |
| `yaml_flow.go:261` | yaml-limit | 12 / 6 | 0 / 18 | Publisher: proof: depth |
| `yaml_flow.go:286` | yaml-unsupported | 4 / 6 | 0 / 10 | Publisher: evidence: recordsets and publish are lists of text, so the checker refuses a pair in them |
| `yaml_flow.go:306` | yaml-duplicate-key | 0 / 6 | 0 / 6 | both profiles: evidence: a repeated key is an error of the reference too |
| `yaml_flow.go:310` | yaml-unsupported | 9 / 35 | 0 / 44 | Publisher: evidence: the checker refuses a null (a key with no value) for every key it allows |
| `yaml_flow.go:342` | yaml-anchor | 4 / 2 | 2 / 4 | both profiles have documents |
| `yaml_flow.go:344` | yaml-tag | 4 / 2 | 2 / 4 | both profiles have documents |
| `yaml_flow.go:346` | yaml | 4 / 36 | 0 / 40 | Publisher: evidence: no key that the checker allows starts with these characters |
| `yaml_flow.go:354` | yaml-key | 17 / 12 | 5 / 24 | both profiles have documents |

### The values that both readers read

Where the Go reader and the `yaml` package (which the references use, with its default options) both read a document, do they read the same values? The verdicts
are compared by the tests above, and the facts that the rules use; `TestValuesAgreeWithTheYamlPackage` compares every value of every document of the corpus,
the free text of `title` and `description` and every key that no rule reads included. `values.json` holds, for each document, a digest of what the `yaml`
package reads (`generate.mjs` makes it in a canonical form: null, a boolean, a number as the 16 hex digits of its IEEE double, a string with its length in bytes,
a sequence, a mapping with its keys in byte order); the test makes the digest of what the reader reads and compares them. The corpus has a family of
scalars for it (numbers in many spellings, the booleans and nulls of YAML 1.1 and 1.2, strings and escapes, block scalars with their indents and blank lines,
plain values over several lines, flow collections, nested block collections), in the manifests and in OVDB.md. Result: 6023 documents are read by both, 6018
of them with the same values, and 5 that differ only in the integer -0: the `yaml` package reads `-0` as the number -0 and the Go reader as 0 (its integers
are exact), which no rule can tell apart; the reader refuses 1164 documents that the yaml package reads (the kinds above), both refuse 811, and the reader
reads 0 that the yaml package refuses.

The page of every recordset (the template with the name written as one encoded path segment) is judged in both profiles, every bad name reported. The
Directory profile did not judge pages until ovdb#58: the Directory refuses such a manifest in `analyseDatabase`, after the files are read, and the probes could
not see it because a probe's reference verdict was `manifestProblems` alone; both were closed there.

### The database descriptor

A repository may publish, beside its manifest, a JSON database descriptor (`ovdb-database/draft-1`) that separates its global identity from its server: OVDB.md lists
it in `publish` next to the manifest. The Directory knows it by the field of the registry's record that names it (`database_manifest`); a repository has no record,
so `manifest.IsDescriptor` tells it from a manifest by its `format`, text beginning `ovdb-database/`, and a repository lists one descriptor with one manifest
(`repo-descriptor` otherwise: a descriptor and no manifest, several manifests or several descriptors, since nothing says which belongs to which). The rules are
the Directory's `databaseDescriptorProblems` and its check of the manifest's `id` against the descriptor's `localId`, for both profiles (`Judge.Descriptor`):

| Rule | Finding |
| --- | --- |
| a JSON object (the text is read as `JSON.parse` reads it) | `descriptor-json` |
| `format` is `ovdb-database/draft-1` | `descriptor-format` |
| `id`, `localId`, `serverId`, `serverDbBaseUrl`, `apiUrl` are non-empty text | `descriptor-required` |
| `id` is the url of the manifest, as text, and a global database identity (package `rules`, `GlobalDatabaseID`) | `descriptor-id` |
| `localId` is `^[a-z][a-z0-9-]{0,39}$` | `descriptor-local-id` |
| `serverId`, `serverDbBaseUrl`, `apiUrl` are public https URLs | `descriptor-url` |
| `serverDbBaseUrl`, `apiUrl` and `deployment.discovery` are on the host of `serverId`; `deployment.discovery` is a public https URL and the manifest's own text (these are asked only when `serverId` and `localId` are good, as the Directory asks them) | `descriptor-origin`, `descriptor-discovery` |
| the manifest's `id` is the descriptor's `localId` (the registry's record key stands in for a `localId` that is not written, which a repository has not) | `manifest-id` |

**What a pass assumes.** The Directory waives the discovery-origin rule when the registry's *record* names a descriptor (`database_manifest`), valid or not; this check
waives it when OVDB.md lists exactly one descriptor beside one manifest. So a repository with a good descriptor that its record does not name passes here and is
refused by the Directory, which then applies the origin rule. An offline check cannot know the record: this is one of the things it cannot know, and the comparison
with the record is slice A4.

**How the JSON is read.** A descriptor is recognised and judged as JSON (`encoding/json`), as `JSON.parse` reads it, and not through the strict YAML reader: tabs, a
repeated key (the last counts), numbers of any size (kept as text: only their kind matters), U+2028 and U+0085 in strings, and an escaped `\/` or `\u` in `format` are
JSON. Where Go's reading is not `JSON.parse`'s: (1) a lone surrogate escape (`\ud800`) becomes U+FFFD in Go and stays a lone surrogate in JavaScript, which no
verdict sees (the text is compared only with the manifest's, which the YAML reader keeps free of both, and the descriptor is refused where either shows in a field);
(2) a descriptor nested more than 64 levels is refused (`descriptor-depth`, recorded, with a case in `descriptor.json` and a repository test); (3) Go's JSON decoder stops at 10000 levels, so a document nested deeper than that is not recognised as JSON, is read as YAML,
and is refused as a manifest by the reader's own depth bound (`yaml-limit`); between 65 and 10000 levels the descriptor is recognised and refused by (2) (a case of
`repo/descriptor_test.go` holds each); (4) an invalid UTF-8 byte inside a string is U+FFFD in both.
A document that is not JSON but is a YAML mapping with such a `format` is still recognised, so that the finding says it is not valid JSON.

With a descriptor beside it a manifest is not held to having `deployment.discovery` on the origin of its `url` (the Directory's `databaseManifest` option); the
descriptor's rules tie the discovery to the server instead. The descriptor is read only after its manifest has no findings, as the Directory stops at the manifest's
problems first. What belongs to the registry alone (the claims, the server-id exceptions, the record key) is not judged here (slice A1).

`testdata/reference/descriptor.json` holds the pairs of a manifest and a descriptor with the verdict of the Directory's own code on each (`databaseDescriptorProblems`
is not exported, so the generator cuts its source from the pinned file between two anchors and runs it as it is). The Directory profile agrees on every pair; the Publisher
profile refuses every pair the Directory refuses and, beyond them, only by a rule of its own that the manifest of the pair breaks. Recorded differences: a descriptor nested
more than 64 levels deep is refused (`descriptor-depth`; the Directory reads any depth). **Not ported:** the Chinook companion also validates a descriptor against a pinned
JSON schema (`schemas/ovdb-database-draft-1.schema.json`, with ajv); the Directory leaves that to the provider, and this check does not run it, so a descriptor that breaks
only the schema is accepted here (a looser difference from the companion, under D0 the Directory being the reference).

### Recorded differences of the probes

A probe of `drift.probes.json` may name, in `recorded`, a stricter kind that Go is expected to differ by on it: a kind of the tables above (`yaml-tag`,
`yaml-character`, `url-length`), or one that no corpus document shows and so has no row there (`recordedProbeKinds` in `drift_test.go`). `TestDrift` fails
when Go refuses such a probe for another reason, or agrees with the reference.

| Kind | Why |
| --- | --- |
| `representation-hash-list` | A representation_contract whose sha256 is a list of one hash (also nested). JavaScript applies the Directory's regular expression to the text of any value and reads the list as that hash, so the Directory accepts the envelope and refuses the same document one step later, in the attachment, where the hash it computes does not equal the list. Go refuses it at once (`representation-attachment`), the safe direction. Not a slice to land. |

### Recorded differences: not yet ported (the Directory profile)

The Directory's checker has moved since the first reference was pinned, and Go has not yet ported every rule it added. Where that makes Go
refuse what the Directory accepts, no kind is recorded at the moment; where it makes Go accept what the
Directory refuses (no remaining looser corpus documents) it is recorded in `testdata/reference/drift.json` and by the test that holds the
corpus to it. These are not bounds and not choices. `testdata/reference/drift.json` is the whole list, in two classes (Go looser first), with the slice
that removes each entry; `TestDrift` fails when the list and what Go does disagree, in either direction, so an entry is removed in the pull request
that ports its rule. The rules of the Directory that the corpus does not reach are covered by `testdata/reference/drift.probes.json`: one manifest
for each, with the verdict of the reference at the pin.

`TestDrift` and the probes are of the **manifest stage**. The Directory's file stage (what `analyseDatabase` does after the manifest: the model file, the meaning
file, its concepts and bindings) has its own probes, `repo/testdata/reference/directory-stage.json`, and its looser cases are in the same `drift.json`
(`fileProbes`, slices F2 to F6); package repo's README says what they are and holds the table of every refusal of that stage. Until they were added, an empty
looser class here said nothing about the file stage.

The representation envelope and compound data licence probes now agree with the Directory: this implementation validates the optional envelope and accepts bounded compound data licences in both profiles. All probes remain committed, so `TestDrift` detects regressions.

### Recorded differences: the Publisher profile's own

Three kinds are made by rules that the Publisher profile alone has: the native recordset-name bound, and two about `meaning.graph.address` in the own form: the Directory's rule that the
address starts with the literal `meaning://` still applies (the Chinook checker accepts `MEANING://` and `Meaning://`: it compares the address in
lower case), and the address is compared with `publisher.repository` in ASCII case only (see below).

| Kind | Documents | Why |
| --- | --- | --- |
| `manifest-recordsets` | 2 | Not a bound: D0 (the lead's default; the Directory at its pin is the reference for both profiles). The Directory refuses a recordset name over 256 UTF-16 code units (nativeRecordsetNameProblem, 1c7e126); the Chinook checker, which read every name as an entity identifier, accepted it. |
| `graph-address-case` | 2 | An own-form meaning.graph.address is compared with the repository in ASCII case only (A to Z); the checker lower-cases with JavaScript's toLowerCase, which also folds non-ASCII letters, among them the Kelvin sign onto k. Go refuses what the checker accepts through such a fold, and never the other way round. |
| `graph-address-host-case` | 1 | The host of an own-form meaning.graph.address must be written in lower case, as the Directory's repositoryKey knows only that spelling (a record carries no other); the checker lower-cases the whole address, the host included, and accepts GitHub.com. |
| `graph-address-scheme` | 4 | An own-form meaning.graph.address must start with the literal meaning:// (a rule of the Directory); the checker only compares it in lower case and accepts MEANING:// or Meaning://. |


**Why the address is compared in ASCII case only.** The checker lower-cases the
address with JavaScript's `toLowerCase`, and Go's `strings.ToLower` is not the same
function outside ASCII: U+0130 (capital I with dot above) is `i` to Go and `i` with a
combining dot (two code points) to JavaScript, so a Go comparison with `strings.ToLower`
accepted `meaning://github.com/k\u0130tchen/sink` for the repository
`https://github.com/kitchen/sink`, which the checker refuses (found by the review of
slice 2b). The Go comparison now folds A to Z and nothing else. Its other side is built from
`publisher.repository`, which is ASCII, so a lower-cased ASCII string is the same in both
languages and the comparison is never looser than the checker's; it is stricter only where
JavaScript folds a non-ASCII letter onto an ASCII one (the Kelvin sign U+212A onto `k`),
the kind `graph-address-case`. The host is compared as written, not in ASCII case: the Directory's `repositoryKey` knows the host only as the literal it is listed under, so no record carries another spelling and `meaning://GitHub.com/org/repo` is refused whatever a registry says; the checker lower-cases the whole address and accepts it, which is the kind `graph-address-host-case`. The corpus holds U+0130, U+0131, U+017F, U+212A, U+00DF,
U+03A3 and U+03C2, a fullwidth Latin letter and a combining mark after an ASCII letter in
every field that either side compares or lower-cases (families `unicode: case` and
`unicode: kitchen`).

Every other call in `internal/publisher` (rules included) that can differ from
JavaScript outside ASCII, found by a search for `strings.ToLower`, `ToUpper`, `EqualFold`,
`Title`, `TrimSpace`, `Fields`, `Trim*`, `unicode`, `utf8`, `regexp` and the
`(?i)`, `\s`, `\w`, `\d`, `\b` classes, and what each does:

| Site | What it judges | The reference | Can Go be looser? |
| --- | --- | --- | --- |
| `publisher.go`, `lowerASCII` | `meaning.graph.address` against the repository | `toLowerCase` | No: ASCII only (above) |
| `manifest.go`, address rule | the repository of an address is lower case | `toLowerCase` compare | No: the repository has passed `rules.RepositoryKey`, which refuses every byte that is not A-Z a-z 0-9 . _ -, so it is ASCII there |
| `rules/repo.go`, `CompareKey` | repositories compared in any case | `toLowerCase` | No: the key is ASCII (same function) |
| `rules/repo.go`, `hasSuffixFold` | a `.git` suffix | `/\.git$/i` | No: the string is ASCII there, and `.git` holds no letter that Go's folding joins to a non-ASCII one (those are `k` and `s`) |
| `check.go`, `unprintable` | the bytes of a message are printable | not a judgement | Not applicable: it sanitises output |
| `rules.IsBlank`, `rules.IsID`, `isLetter` and the other hand-written classes | blank text, ids, names | `trim()`, `/^[a-z0-9]+(-[a-z0-9]+)*$/` and the like | No: byte tests on ASCII, and `IsBlank` is JavaScript's `trim()` set, not Go's |

No site of `rules` needed a change.

## Regenerate

```sh
node internal/publisher/manifest/testdata/reference/generate.mjs          # rewrite the goldens
node internal/publisher/manifest/testdata/reference/generate.mjs --check  # fail if they are stale
```

It needs Node 24 or later, git, npm and network access (`go test` needs none): it
fetches the two references at their pinned commits into a directory that only this run can write, made for the
run and removed when it ends: no checkout is kept between runs (a kept one is state that the run did not make, and what a check can see of it is
less than what could have been done to its object store, so the way not to trust it is not to have it). The locations are one constant,
`internal/publisher/references.mjs`, shared with the generator of package `rules`; `npm ci --omit=dev --ignore-scripts` runs in the
Directory's for the `yaml` package, which the Chinook checkout borrows by a link. A clone that you pass (`--directory <dir>`, `--chinookdb <dir>`) is
checked, not changed: the checkout must be as committed, which is read without trusting its `.git` (replace objects off, the caller's `GIT_*` variables
and the global config ignored; every tracked file hashed raw and compared with the commit's blob; the index compared with the tree; every untracked file
listed with no exclude pattern; local config limited to the keys of a fresh clone, so no `extensions.worktreeConfig`, and no `config.worktree` file), against a mistake and a hidden edit of the working tree, not against
someone who rewrites the object store, and not against an edit inside the `node_modules/yaml` of the clone: a clone you pass is trusted code (it runs, with the checker, in your Node), so pass only one you made. `node --test internal/publisher/references.test.mjs` shows each case on a repository of its own. It
takes about a minute, most of it for the real repositories. When a reference
moves, change `references.mjs`, regenerate, and read the diff of the goldens and of the
tables above. The digests of the rules golden are read from its committed file: run
`internal/publisher/rules/testdata/reference/generate.mjs` first when the rules change.

The job `publisher-goldens` of `.github/workflows/ci.yml` runs the three generators with `--check` and `node --test internal/publisher/references.test.mjs`, with Node
24.19.0 (the version that the goldens record, so that a new Node is a change of the workflow and a regeneration together), on every pull request and push to
main; it fails when a golden is stale. No workflow runs on a schedule (`TestNoScheduledWorkflows`).

## What remains

- Nothing of the table above: slice 3b-1 made the rules about the presence and the readability of files and the `--repository`
  option, and slice 3b-2 the rules about the content of the model file and the meaning file (package `repo`, with `Judge.Meaning`
  here for the meaning file), each held to the checker's lines and proved on whole repositories (see the README of package `repo`).
- The command itself, `ovdb publisher check`, is built (package `internal/publisher/checkcmd`; the README of the module is its manual).
- The Directory's own record and registry checks stay with the Directory.

## Bounded data licence conjunctions

Only `licences.data` may additionally contain 2–4 distinct current Publisher
allowlist atoms separated by exactly ` AND `, at most 64 ASCII bytes. Both profiles
preserve the scalar expression and authored order without normalization. Model
and meaning predicates are unchanged. Directory's existing single-token shape
acceptance remains separate from Publisher's exact 18-ID allowlist, including
legacy plus forms, LicenseRef, unknown shaped IDs and case. Syntax success makes
no legal compatibility or distribution-compliance claim.

`representation_contract` is an optional closed provider-local JSON path and
lowercase SHA256 pair. Manifest validation checks its shape; repository validation
completes structural associations and required format3 raw source-data proofs.

Current canonical checker references: Directory `ec53d7539aafd23d006b4943acdd7a31f4eb9340` and demo-db/chinook `8b904298d0c3bba20c12dfbc29bb75bf5c37f683`. The prior exact datatug/chinookdb `79e7bb0b1d6f0666dce465874990dec64348331f` supplies only frozen corpus documents and mined literal inputs; its code is not imported as a reference validator.

The current Publisher validator is `demo-db/chinook@8b904298d0c3bba20c12dfbc29bb75bf5c37f683`. Upstream native-recordset validation closes all four legacy Directory corpus differences. The Directory comparator permits no looser cases. Publisher cases accepted under the upstream D0 rule must also be accepted by Directory, and every problem reported by the pinned Chinook checker must be classified as D (the dropped entity-name restriction) or K (the added recordset_entities key); every other unrecorded acceptance fails. The generated reference refusals remain intact.

All 67 of the 67 documents that the Publisher profile accepts and the Chinook checker refuses are explained by the upstream D0 rule and also accepted by Directory. The generated chinookClasses records every problem per manifest: D is the entity-name restriction and K is recordset_entities as an unknown key. Any other problem (O) prevents a D0 explanation; a native name alone never excuses another refusal.

## Optional source-data rights profile

The separately versioned `data_rights.format: ovdb-data-rights/1` contract is
validated by `internal/publisher/datarights`; the frozen legacy JS reference and
its digests are unchanged. `DataDeclaration` carries closed scalar/object
`licences.data` validation. `DataRights` carries the raw database declaration,
pinned server author artifact, native recordset overrides and pinned provenance.
`SourceDefinition` carries the separate closed, blocked `ovdb-http-source/1`
contract; `SourceDefinitionEvidence` verifies authored manifest bytes only, with
dynamic input unpinned. These extension fields are outside the frozen legacy
Directory fact table. See [HTTP source definitions](../../../publisher/source/README.md).
`SourceRights` is populated only by immutable repository verification, after
size/hash checks and effective whole-declaration equality. The legacy scalar
`LicenceData` fact stays unchanged for legacy SPDX documents; structured
terms are read through `DataDeclaration`, never through the scalar compatibility
fact. Model and meaning licences retain the existing scalar policy.

New-profile descriptors must carry the identical raw authorship block, required
materialized database terms and each native recordset's effective terms. This
validator is not the provider's JSON-schema checker; provider tooling must add
new schema IDs and select those only for opted-in providers. Directory and JS
reference adoption are follow-up changes. No custom public provider is activated
by accepting this profile in Publisher.
