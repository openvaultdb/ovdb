# internal/publisher/manifest

The pure judge of a publisher's two documents, `OVDB.md` and the manifest
(`ovdb.yaml`): bytes in, findings and facts out. It is the second piece of
`ovdb publisher check`, a command that checks what a publisher keeps in their
repository; **no command uses it yet**, nothing imports it from `main`, and the
`ovdb` binary does not link it (so nothing in it is visible to a user).

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
(the second also returns the `Manifest` facts). An unknown `Profile` judges
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
| `format` | directory.mjs 180 | `Format` |
| `id` | directory.mjs 181, 313 | `ID` |
| `title` | directory.mjs 181 | `Title` |
| `description` | directory.mjs 181 | `Description` |
| `url` | directory.mjs 109, 188, 193, 194, 312 | `URL` |
| `homepage` | directory.mjs 230, 231, 667 | `Homepage` |
| `deployment.url` | directory.mjs 110, 189, 666 | `DeploymentURL` |
| `deployment.engine` | directory.mjs 190, 666 | `Engine` |
| `deployment.discovery` | directory.mjs 191, 193, 194 | `Discovery` |
| `deployment.recordset_page` | directory.mjs 111, 197, 651, 676 | `RecordsetPage` |
| `model.modelspec` | directory.mjs 173, 216, 390, 391, 395, 402, 424, 441, 450 | `ModelSpec` |
| `model.hcl` | directory.mjs 173, 198, 415 | `ModelHCL` |
| `model.address` | directory.mjs 201, 202, 217, 418, 419, 421, 422, 426, 458, 475 | `ModelAddress` |
| `model.name` | directory.mjs 402, 552 | `ModelName` |
| `meaning.address` | directory.mjs 205, 206, 218, 459, 476 | `MeaningAddress` |
| `meaning.file` | directory.mjs 209, 220, 372, 386, 392, 401, 407, 409, 413, 415, 460, 535, 539, 546 | `MeaningFile` |
| `meaning.graph.id` | directory.mjs 210, 221, 314 | `GraphID` |
| `meaning.graph.address` | directory.mjs 211, 222, 383, 502 | `GraphAddress` |
| `licences.model` | directory.mjs 213, 223, 490 | `LicenceModel` |
| `licences.meaning` | directory.mjs 213, 224, 401, 503 | `LicenceMeaning` |
| `licences.data` | directory.mjs 234, 671 | `LicenceData` |
| `publisher.name` | directory.mjs 226 | `PublisherName` |
| `publisher.url` | directory.mjs 227 | `PublisherURL` |
| `publisher.repository` | directory.mjs 315, 316 | `PublisherRepository` |
| `recordsets` | directory.mjs 235, 337 | `Recordsets` |
| `recordsets_partial` | directory.mjs 214, 219, 342, 351 | `RecordsetsPartial` |
| `form` | directory.mjs 173, 179, 319 | `Form` |
| `model.address.repository` | directory.mjs 201, 217, 419, 458, 564 | `ModelAddress.Repository` |
| `model.address.module` | directory.mjs 201, 217, 419, 458, 564 | `ModelAddress.Module` |
| `model.address.ref` | directory.mjs 201, 217, 419, 458, 564 | `ModelAddress.Ref` |
| `meaning.address.repository` | directory.mjs 205, 459 | `MeaningAddress.Repository` |
| `meaning.address.ref` | directory.mjs 205, 459 | `MeaningAddress.Ref` |

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

A `Profile` says whose rules judge, and is an argument of every function so that
another profile is added without changing a signature. There is one today.

| Profile | Rules |
| --- | --- |
| `Directory` | The OVDB Directory's own: `OVDB.md` has YAML front matter (`---` lines) with `ovdb: 1` and a non-empty `publish` list of `./`-prefixed repository paths, which must list the manifest; the manifest has the right `format`, the required text fields, the URL rules (public https, no trailing slash, an `ovdb` marker in the canonical `url`), and either the own form (a `model.modelspec`, a `meaning.file`) or the shared form (a `model.address` and a `meaning.address` and graph, each a `modelspec://` or `meaning://` address, pinned or not); and what the table above says is judged here. Unknown keys and a repeated `publish` entry are accepted. |

The publisher profile of the Chinook checker (allowed keys at every level, the
licence allow-list, discovery and `recordset_page` on the deployment's origin,
`model.hcl` required, one `publish` entry each, no unknown `OVDB.md` keys) is
the next change and is not here.

Not judged, because it needs files other than the two documents: that the named
files are tracked regular files at HEAD and of a size, and anything read through a
registry. A required text is checked with `rules.IsBlank`, never `strings.TrimSpace`
(JavaScript's `trim()` and Go's differ on U+FEFF and U+0085).

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

## The proof that Go never accepts more than the Directory

The rule is the one of package `rules`: **the Go function never accepts a pair of
documents that the JavaScript of the profile refuses**. The reference of the
`Directory` profile is `openvaultdb/directory@e8db5488db31d3f63865e404acef487c33cf35df`: `parseFrontmatter` and `manifestProblems` of
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
network. The corpus is **4410 manifests and 425 OVDB.md documents** (530
KiB), stored as patches of whole lines against a few base documents (the real
Chinook manifest and `OVDB.md`, the hoster example, the Directory's own fixture,
JSON spellings of the manifests), with flags for CRLF, a byte-order mark, invalid
UTF-8 and padding to an exact size; Node v24.20.0 made the committed ones:

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
  own and the shared form: **170 of 251** found were applicable alone;
- the YAML text mutated line by line and spelled differently (CRLF, a lone CR, a
  byte-order mark, a Latin-1 byte, `---`, `...`, directives, several documents,
  keys that are numbers, escapes, `.inf`, integers beyond 2^53, documents at, and
  over, 262144 bytes);
- about 400 `OVDB.md` documents: front matter edge cases, every `ovdb` and
  `publish` value shape, white space in and around an entry, repeated entries,
  entries at and over the length bound, paths that are and are not listed.

`TestReferenceDirectory` fails if any Go function accepts where the reference
refuses, if Go is stricter in a way that is not recorded below, if a recorded
kind never happens in the corpus, or if this file's numbers are stale (it checks
the commits, the counts and every row of the tables). `TestFactsAgreeWithTheReference`
compares every fact of every document that both accept with what the Directory's
code derives, and holds the table of fields to the generator's. `TestGoldenDigests`
holds every golden of both slices to its SHA-256 in `digests.json`, so a hand edit of
a golden fails until `generate.mjs` is run again. `go test -v -run
'TestReferenceDirectory|TestFacts' ./internal/publisher/manifest` prints the numbers.

On the corpus: **4508 agree, 327 stricter, 0 accepted by Go where the
Directory refuses**.

On the facts: 911 manifests and 85 OVDB.md documents have their facts compared.

For every manifest the Go reader reads, accepted or refused, the presence of every field
is compared with the reference's parsed manifest (`TestPresenceAgreesWithTheReference`): the
facts that are `Present` are the fields the reference has, so a written value that is
refused can never become an absent fact. The presence of every field is compared on 3344 manifests, 2433 of them refused.

### Recorded differences: where Go is stricter

Go may refuse what the JavaScript accepts. Each kind is the rule of the first
finding Go makes (a length refusal is named by what is long), with its number of
documents in the corpus; a document counts only if the Directory accepts it, so each
kind is real. Most are not ordinary manifests (a lone surrogate escape, a byte that is
not UTF-8, nesting 64 deep); the exception is `yaml-unsupported`: a manifest that a YAML
tool has written back, with a long string folded over several lines in a quoted value,
is refused until the reader reads it (meaninggraph/cli#7), and the message tells the
publisher to write the value as a block scalar, `>-` or `|-`.

| Kind | Documents | Why |
| --- | --- | --- |
| `document-size` | 6 | A document over 262144 bytes (MaxDocumentBytes) is refused before it is read; the references read files of any size. |
| `length-address` | 5 | An address over 2048 bytes, or one that names a repository over 247 bytes; the references' address expressions have no bound. |
| `length-entry` | 2 | A publish entry whose path after ./ is over 1024 bytes; the references have no bound. |
| `length-path` | 8 | A file path over 1024 bytes in model.modelspec, model.hcl or meaning.file; the references have no bound. |
| `length-repository` | 2 | A publisher.repository over 255 bytes; the references have no bound. |
| `punycode` | 4 | A homepage host with an xn-- label that does not spell Latin-1 letters (see the README of package rules); Node accepts the label. |
| `url-length` | 3 | A URL longer than rules.MaxURLLength (2048 bytes) is refused; the reference has no bound. |
| `yaml` | 2 | The reader accepts a subset of YAML, and a structure it cannot place (a flow collection as a key, as in [a]: x) is refused; the reference reads it. |
| `yaml-anchor` | 48 | The reader refuses anchors and aliases (& and *): it reads a document once, as written, and expanding references is how a small file becomes a large one. |
| `yaml-character` | 10 | The reader refuses characters that YAML 1.2 does not allow in text, among them the C1 controls such as U+0085; the reference reads them into a string. |
| `yaml-directive` | 4 | The reader refuses a %YAML or %TAG directive; the reference follows it. |
| `yaml-documents` | 2 | The reader refuses a document end marker (`...`) and a second document; the reference reads the first document and ignores what follows. |
| `yaml-encoding` | 2 | The reader refuses a file that is not UTF-8 text (a Latin-1 byte, a NUL character); the reference, which reads a file as UTF-8, replaces the bytes it cannot decode and goes on. |
| `yaml-escape` | 4 | The reader refuses a double-quoted escape that is not a character, such as half of a surrogate pair (\ud83c); the reference accepts it. |
| `yaml-key` | 6 | The reader refuses a key that YAML reads as a number, a boolean or null (2024, true, null) and wants it in quotes; the reference accepts it as a key. |
| `yaml-limit` | 6 | The reader refuses collections nested more than 64 levels deep (63 is read); the reference reads any depth. |
| `yaml-line-ending` | 4 | The reader refuses a carriage return that is not part of CRLF; the reference reads it as a line break. |
| `yaml-number` | 14 | The reader refuses numbers it cannot hold exactly or that are not finite: hexadecimal and octal numbers, .inf, .nan, and integers beyond 2^53; the reference reads them as numbers. |
| `yaml-tab` | 42 | The reader refuses a tab where YAML allows it but whose reading differs between parsers (after a colon, in indentation). |
| `yaml-tag` | 82 | The reader refuses tags (!, !!), which the reference resolves; it reads plain values only. |
| `yaml-unsupported` | 71 | The reader refuses constructs outside its subset: explicit keys (`? key`), and a quoted value written over more than one line, which a YAML tool writes back for any long string (the message asks for a block scalar, `>-` or `|-`; meaninggraph/cli#7); the reference reads both. |

### Regenerate

```sh
node internal/publisher/manifest/testdata/reference/generate.mjs          # rewrite the goldens
node internal/publisher/manifest/testdata/reference/generate.mjs --check  # fail if they are stale
```

It needs Node 24 or later, git, npm and network access (`go test` needs none): it
fetches the two references at their pinned commits into a temporary directory
(`npm ci --omit=dev --ignore-scripts` in the Directory's, for the `yaml` package).
`--directory <dir>` and `--chinookdb <dir>` use clones you already have, at the
pinned commits. When a reference moves, change the pins in the script, regenerate,
and read the diff of the goldens and of the tables above. The digests of the rules
golden are read from its committed file: run `internal/publisher/rules/testdata/reference/generate.mjs`
first when the rules change.
