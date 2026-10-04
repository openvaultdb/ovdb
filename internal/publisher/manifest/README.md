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
result.OVDBMd.Publish   // the manifest paths OVDB.md lists, without "./"
result.Manifest.Form    // manifest.FormOwn or manifest.FormShared
```

`Check` judges the pair; `CheckOVDBMd` and `CheckManifest` judge one document
(the second also returns the `Manifest` facts). A `Finding` has a stable `Rule`
(match on that, never on the message), a `Severity`, the `Document`, the `Line`
and a `Message` that says what is wrong and what to write; a piece of a document
reaches a message only through `rules.Quote`, so a message never holds a control
character or a line break. At most `MaxFindings` (100) findings are reported per
document; the last then has the rule `findings-capped` and says how many were
left out. A document with a finding is refused whatever the cap.

## Profiles

A `Profile` says whose rules judge, and is an argument of every function so that
another profile is added without changing a signature. There is one today.

| Profile | Rules |
| --- | --- |
| `Directory` | The OVDB Directory's own: `OVDB.md` has YAML front matter (`---` lines) with `ovdb: 1` and a non-empty `publish` list of `./`-prefixed repository paths, which must list the manifest; the manifest has the right `format`, the required text fields, the URL rules (public https, no trailing slash, an `ovdb` marker in the canonical `url`), and either the own form (a `model.modelspec`, a `meaning.file`) or the shared form (a `model.address` and a `meaning.address` and graph, each a `modelspec://` or `meaning://` address, pinned or not). Unknown keys and a duplicate `publish` entry are accepted. |

The publisher profile of the Chinook checker (allowed keys at every level, the
licence allow-list, discovery and `recordset_page` on the deployment's origin,
`model.hcl` required, one `publish` entry each, no unknown `OVDB.md` keys) is
the next change and is not here.

Not judged, because it needs files other than the two documents: that the named
files are tracked regular files at HEAD and of a size, that the recordsets are
the model's entities, that a licence equals the meaning file's, and anything read
through a registry. A required text is checked with `rules.IsBlank`, never
`strings.TrimSpace` (JavaScript's `trim()` and Go's differ on U+FEFF and U+0085).

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
| `MaxFindings` | 100 per document | this package |
| reader limits | 8 MiB file, nesting 64 | the reader |
| URL, id, path, engine, licence lengths | those of package `rules` | see its README |
| address length | `rules.MaxURLLength` (2048 bytes) | this package |

## The proof that Go never accepts more than the Directory

The rule is the one of package `rules`: **the Go function never accepts a pair of
documents that the JavaScript of the profile refuses**. The reference of the
`Directory` profile is `openvaultdb/directory@e8db5488db31d3f63865e404acef487c33cf35df`: `parseFrontmatter` and `manifestProblems` of
`scripts/lib/directory.mjs`, called as they are, and the `OVDB.md` checks that
`analyseDatabase` keeps inline, composed from the same expressions (the generator
fails if the pinned file no longer holds them as copied). The documents are read as
the Directory reads them, by the `yaml` package with its default options.
`datatug/chinookdb@79e7bb0b1d6f0666dce465874990dec64348331f` supplies real documents and the test suite to mine.

`testdata/reference/generate.mjs` builds a corpus (`corpus.json`) and the
Directory's verdict on each document (`directory.verdicts.json`). `go test` reads
both, applies the Go functions, and starts no process and needs no network. The
corpus is **4198 manifests and 412 OVDB.md documents** (444 KiB), stored as patches of
whole lines against a few base documents (the real Chinook manifest and
`OVDB.md`, the hoster example, the Directory's own fixture, JSON spellings of
the manifests); Node v24.20.0 made the committed ones:

- every field removed, renamed, given each of ten wrong types, given each
  white-space and format character alone (U+0000 to U+3000 and the zero-width and
  byte-order characters, JavaScript's `trim()` and not Go's), and given an
  unknown sibling, in an own and a shared manifest;
- each URL field replaced by URLs mined from the two JavaScript test suites and
  by hand-written odd ones (ports, userinfo, dot segments, punycode, reserved
  suffixes, trailing slashes, a template where none is allowed, over-long);
- the forms mixed: every combination of the form-defining fields
  (`model.modelspec`, `model.hcl`, `model.address`, `meaning.address`,
  `meaning.file`), and up to two of the other toggles (pinned and unpinned
  addresses, a graph address, licences, `recordsets_partial`) and removals;
- the single-field edits of both test suites (`(m) => { ... }`) applied to the
  own and the shared form: **170 of 251** found were applicable alone;
- the YAML text mutated line by line (a line removed, repeated, indented, tabbed,
  anchored, aliased, tagged, quoted, made a block scalar, a flow collection, a
  number such as `0x1F` or `.inf`, a boolean such as `yes`) and spelled
  differently (CRLF, a lone CR, a byte-order mark, `---`, `...`, directives,
  several documents, the stringified document in each of `yaml`'s styles);
- about 400 `OVDB.md` documents: front matter edge cases (missing or unclosed
  fences, CRLF, a byte-order mark, a second document), every `ovdb` and `publish`
  value shape, every white-space character in and around an entry, paths that
  are and are not listed.

`TestReferenceDirectory` fails if any Go function accepts where the reference
refuses, if Go is stricter in a way that is not recorded below, if a recorded
kind never happens in the corpus, or if this file's numbers are stale (it checks
the commits, the counts and every row of the table). `go test -v -run
TestReferenceDirectory ./internal/publisher/manifest` prints them.

On the corpus: **4341 agree, 269 stricter, 0 accepted by Go where the Directory
refuses**.

### Recorded differences: where Go is stricter

Go may refuse what the JavaScript accepts. Each kind is the rule of the first
finding Go makes, with its number of documents in the corpus; a document counts
only if the Directory accepts it, so each kind is real.

| Kind | Documents | Why |
| --- | --- | --- |
| `url-length` | 3 | A URL longer than rules.MaxURLLength (2048 bytes) is refused; the reference has no bound. |
| `yaml-anchor` | 48 | The reader refuses anchors and aliases (& and *): it reads a document once, as written, and expanding references is how a small file becomes a large one. |
| `yaml-character` | 19 | The reader refuses characters that YAML 1.2 does not allow in text, among them the C1 controls such as U+0085; the reference reads them into a string. |
| `yaml-directive` | 4 | The reader refuses a %YAML or %TAG directive; the reference follows it. |
| `yaml-documents` | 2 | The reader refuses a document end marker (`...`) and a second document; the reference reads the first document and ignores what follows. |
| `yaml-number` | 6 | The reader refuses hexadecimal and octal numbers (0x1F, 0o17), which the reference reads as numbers; a value written so is never meant, and its value would depend on the YAML version. |
| `yaml-tab` | 42 | The reader refuses a tab where YAML allows it but whose reading differs between parsers (after a colon, in indentation). |
| `yaml-tag` | 82 | The reader refuses tags (!, !!), which the reference resolves; it reads plain values only. |
| `yaml-unsupported` | 63 | The reader refuses constructs outside its subset, such as explicit keys (`? key`). |

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
and read the diff of the goldens and of the table above.
