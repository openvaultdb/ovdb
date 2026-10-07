# internal/publisher/repo

Judges a repository: the presence of files and what they say, for `ovdb publisher check`
(package `internal/publisher/checkcmd`, which the shipped binary links). It reads one commit, at HEAD,
as committed and never the working tree, through a `Reader`, and hands OVDB.md and
the manifests to package `manifest`. The reference is the Chinook checker
(`demo-db/chinook@8b904298d0c3bba20c12dfbc29bb75bf5c37f683`,
`scripts/lib/ovdb-manifest.mjs`): Go never accepts a repository that it refuses, and
every repository that Go refuses and it accepts is a recorded kind below, with cases.

## The entry point

`Check(r Reader, o Options) manifest.Result` returns the shape of `manifest.Check`:
findings (a rule, a message of at most `manifest.MaxMessageBytes`, the document, the
line where it is known), at most `manifest.MaxFindings` in all with the
`findings-capped` notice last, `OVDBMd`, and `Manifest`. `Options` has the profile and
the optional `--repository` value (`*string`: an empty string is a value).

Findings of several manifests share one budget (`manifest.Judge`). They come in this
order: the repository, OVDB.md, then each manifest in the order OVDB.md lists it (its own
findings, then the `--repository` comparison, then its named files). A manifest that cannot
be read is one finding of OVDB.md or of itself. `Result.Manifest` is the first manifest
listed that was read. At most `MaxManifests` (32) are judged; the rest are one finding.

## The reader

`Reader` has four methods: `Head` (pins the commit, or says why the repository cannot be
read), `Entries(dir)` (the names directly in a directory, each with a `Kind`: file,
executable, symlink, submodule, directory), `Blob(path, limit)` and `Uncommitted(path)`
(only to improve a message). The kind of a path is found by walking its directories with
`Entries`; a symlink is never followed and a submodule never entered (a path below one is
missing). `Memory` is the in-memory fake. `Git` is the implementation over git, through a
`Runner`; `ExecRunner` is the Runner that runs the program.

Git is called with `-c core.fsmonitor=false`, with every `GIT_*` variable of the caller
dropped, no system or user configuration, no prompt, no lazy fetch of a missing object and no
optional lock. A path given to git is first checked with `rules.IsRepositoryPath` and follows
the commit id (`<id>:<path>`), so git never reads it as an option. There is no
`--literal-pathspecs`: the only pathspec given is such a path, which has no character that
pathspec magic uses (`:`, `*`, `?`, `[`, `\`), so the flag could change nothing. Calls: `version`,
`rev-parse --is-bare-repository --show-prefix`, `rev-parse --verify --quiet HEAD^{commit}`,
`ls-tree -z <id>[:<dir>] --` (output read up to `MaxTreeBytes`, 4 MiB; at most `MaxEntries`,
50000, entries), `cat-file blob <id>:<path>` (read up to the limit given: `MaxDocumentBytes`+1
for a document, `MaxFileBytes` (4 MiB) for a file a manifest names), `ls-files -z --cached
--others -- <path>`, and, only when an object cannot be read, `config --local --get-regexp` for a
promisor remote. Output is read through a bound and git is stopped when it is passed, and each
call has 30 seconds. The listing is parsed strictly (`parseTree`, fuzzed by `FuzzParseTree`).
A directory that is listed is refused when it has a name no path may have (empty, `.`, `..`,
`.git`, a slash, a backslash, a control character) or two names that differ only in case.

What the reader trusts, and what it does not:

- **Repository-local git configuration is read.** Only the caller's environment (`GIT_*`) and
  the user's and the system's configuration are shut out. Someone who runs the check on a
  repository they did not write runs git on it, with that repository's own `.git/config`:
  `core.fsmonitor` is turned off for every call (the test of the real-git suite shows that
  `ls-files` otherwise runs it), a promisor remote's command is never run because git does not
  fetch (below), and nothing else in the configuration names a program that these calls run
  (tried: aliases, pagers, `core.sshCommand`, `core.hooksPath`, textconv and filter drivers,
  `core.alternateRefsCommand`, `uploadpack.packObjectsHook`, `core.gitProxy`, credential helpers).
  A `.git` file that points elsewhere is followed, as the checker does. Check a clone you made,
  not a directory that someone else handed you, unless you have looked at its `.git/config`.
- **For legacy manifests without attachments, `refs/replace` is honoured**, as the checker does: "the commit as committed" is the commit as
  this clone's replace refs show it. Turning them off would make this check accept a repository the
  checker refuses. The Directory reads the hosted repository, not this clone, and will not see
  replace refs.
- **git is never allowed to fetch.** `GIT_NO_LAZY_FETCH` stops a partial clone from fetching a
  missing object from its promisor remote, whose URL can be a command of the repository's
  choosing. git older than 2.45 does not know the variable (it is in git's `environment.h`, `git.c` and `Documentation/git.txt` and in the 2.45.0 release notes, and in none of them at v2.44.0 or v2.44.2), so `Head` reads `git version` first and
  refuses to go on with anything older (`repo-git-version`).

The repository states, each proved on real git by the slower test:

| State | Result |
| --- | --- |
| not a repository, git cannot run, a git that cannot be asked | `repo-unreadable`, with git's last line of standard error |
| git older than 2.45 | `repo-git-version`, saying to update git |
| unborn branch (no commit) | `repo-no-commit` |
| HEAD names a commit that is not in the repository | the reason below, not "no commit" |
| bare repository | `repo-bare`: the checker reads `HEAD:./path`, which git refuses outside a working tree, so it refuses; so does this check |
| a directory inside a repository | `repo-subdirectory` |
| shallow clone, detached HEAD | read like any other when the objects are there: the checker reads them too |
| `OVDB.md` only in the working tree or the index | `repo-ovdbmd`, saying it is not committed |
| **an object of the commit cannot be read** | one of the four rows below |
| a partial clone (`--filter=blob:none`, `--filter=tree:0`) that lacks an object | `repo-partial-clone`: run the check in a full clone, or fetch the files first (`git checkout` does). The checker's git fetches, so it accepts: a recorded stricter kind. A partial clone whose objects were fetched is read like any other |
| an object that is gone (a loose object deleted, a shallow clone short of an object) | `repo-object-missing`: both refuse |
| an object that is damaged or an empty object file | `repo-object-corrupt`: both refuse |
| objects borrowed from a repository that was deleted (`objects/info/alternates`) | `repo-alternates`: both refuse; a clone that has its own copy of the objects and a dangling alternates file is read like any other, as the checker does |

Which of the four an unreadable object is, is told from the repository's configuration (a
promisor remote) and from what git printed in the C locale; what git said is kept in
`ExitError.Full` and never printed. `repo-unreadable` is what is left: git could not be run, or
failed for a reason none of these names.

## The rules made here

Each is a tracked regular file at HEAD (an executable counts, as in the checker; a
directory, symlink, submodule, a missing path, a path only in the working tree or index,
and a path below a file or symlink do not):

| Rule | What | Checker (`ovdb-manifest.mjs`) |
| --- | --- | --- |
| `repo-ovdbmd` | OVDB.md is at the root | 186-189 |
| `repo-manifest` | every manifest that OVDB.md lists | 221-223 |
| `repo-file` | `model.modelspec`, `model.hcl`, `meaning.file` of an own-form manifest, of every such manifest | 374-392 |
| `repo-object-missing`, `-corrupt`, `repo-partial-clone`, `repo-alternates` | a file a manifest names that the checker reads (the model file and the meaning file; not `model.hcl`, of which it only looks at the kind) can be read: its finding is the one of the reason | 345, 347 |
| `repo-file-size` | such a file is at most 4 MiB (the checker: 16 MiB; recorded below) | 345, 347 |
| `repo-repository` | `publisher.repository` equals `--repository` exactly (compared only when the manifest rules accept `publisher.repository`; when they do not their finding says so) | 309 |
| `document-size` | OVDB.md and each manifest of at most 262144 bytes | 194, 244-246 |
| `repo-model-json` | the model file is JSON that `JSON.parse` reads (no more than one value, no byte order mark, no trailing text), and an object | 405-410 |
| `repo-model-module`, `repo-model-entities` | it has `module.name`, a letter and then letters, digits and `_`, and an `entities` object | 412-416 |
| `repo-model-version`, `repo-model-entity`, `repo-model-property` | the rules of the Directory's `parseModelSpec` (modelspec.mjs 92-118) that the checker never reads: a text `"modelspec"` version; each entity's name is an identifier and it has at least one property; each property's name is an identifier, its type is a type name or it names an entity (a text `entity`) that the model has, and a property that is neither is refused. A repeated key is the last, as in `JSON.parse`. Stricter kinds against the checker, below | the Directory's, not the checker's |
| `repo-model-name` | `model.name`, when written, is that module | 420 |
| `repo-model-address` | the module of `model.address` is that module (the owner and repository of the address are the manifest's, a rule of package manifest) | 426-429 |
| `meaning-shape`, and the reader's own rules | the meaning file is YAML that the strict reader reads, and a mapping (an empty file is not one) | 445-451 |
| `meaning-id`, `meaning-license` | its `id` is `meaning.graph.id` and its `license` is `licences.meaning`, as strings (the Directory compares the licence only when the file has a text one, and does not read the id here: Go is stricter than it, as the checker is) | 453-457 |
| `meaning-binding` | 2 | A binding of a concept names a model that is not this repository's, another module, an entity or a property that the model lacks, or has a role other than entity and no property: the Directory refuses it (directory.mjs 728-754); the checker never reads the concepts. |
| `meaning-chain` | 2 | The extends chain or the values-of of a concept of the meaning file, inside its own graph, returns to a concept already in the chain, is longer than 50 concepts, or names a concept the graph does not have: the Directory refuses it (meaning.mjs 195-226); the checker never reads the concepts. |
| `meaning-concept` | 3 | A concept of the meaning file has a shape the Directory refuses (validateConcept, meaning.mjs): no text id, an id that is not lower-case words joined by single hyphens, labels that are not short plain strings, extends or values-of that is not text, bindings that are not a list of mappings with a role from the list; the checker never reads the concepts. |
| `meaning-concept-duplicate` | 1 | A concept id is declared twice in the meaning file: the Directory refuses it; the checker never reads the concepts. |
| `meaning-concepts` | it has a `concepts:` list (the Directory's rule, `parseMeaningFile`, directory.mjs 487; not the checker's) | |
| `meaning-models`, `meaning-hcl` | its `models:` entry for the module is text spelled as the Directory spells a path, stays inside the repository when joined to the directory of the meaning file, and is `model.hcl` | 459-468 |
| `repo-recordsets` | the recordsets are exactly the entities of the model file, in both directions | 535-539 |

The order of these findings is not the Directory's: it reports the version, the module, the entities, then each entity and property in file order (integer-like names first), and this check reports the version first and then entities and properties by name; at most 1000 of them are kept for a file, because a check shows 101 findings at most and what is kept of a model file is held for every manifest that names it. The verdict is the same either way. `--json` consumers should not read `findings[0]` as the Directory's first problem.

Every other manifest OVDB.md lists is judged with the Publisher profile through
`manifest.Judge` (the checker's line 226).

## The Directory's file stage: what Go is held to, and what it is not

`repository.json` above is the verdict of **the Chinook checker**. The reference of the bar is the Directory (plan decision D0), and its file stage,
`analyseDatabase` in directory.mjs (lines 345 to 838 at the pinned commit), was compared with nothing: the probes of package manifest (`TestDrift`) carry the
Directory's verdicts for the manifest stage, and for the page loop since #58. So "the looser class of drift.json is empty" meant empty for the manifest stage
only, and exit 0 of `publisher check` has meant that the files agree with each other under the rules Go has, not that the Directory's file stage accepts them.
The missing `concepts:` list (#63) was found by reading, not by a test, which is the gap this section closes.

`testdata/reference/directory-stage.mjs` runs `analyseDatabase` itself (the way the Directory's own `scripts/test.mjs` does: a local git repository served through
`urlFor`, the Directory's own `chinookdb` and `core` fixtures as the base, in-memory registries) on 121 repositories of the own form, one rule each, and writes
`directory-stage.json`. The generator stops when the pinned file no longer holds a message it walks (`assertAnchors`) and when a case's verdict is not the reason
its name states. `directory_stage_test.go` replays each case in memory with both profiles and `--repository`, and holds Go to the **outcome** the golden declares:

| Outcome | Cases | What it is |
| --- | --- | --- |
| outcome: agree | 96 | Go and the Directory give the same verdict (the controls, and the rules Go has); a case that both refuse names the rule of Go that refuses it (`go`), and the test holds the first finding to it, so a case cannot agree through an unrelated rule |
| outcome: looser:F6 | 3 | the same, for the meaning file's `models:` entry when the manifest does not write `model.hcl` |
| outcome: looser:F7 | 2 | the same, for the addresses: `meaning.graph.address` (the Publisher profile compares it with `publisher.repository`, the Directory profile does not) and an own-form `model.address` that names another repository (no profile compares it) |
| outcome: out-of-reach:record | 3 | the Directory refuses by what the database's registry record says (its id, its url, its `meaning_graph`); a repository alone cannot |
| outcome: out-of-reach:history | 1 | the Directory refuses by what the repository's earlier commits say: a reference by address to the repository's own graph with a well-formed pin is read at that commit, which must be in the history of the default branch and have the concept (neither the record nor a registry; the fix is a repository that has the commit) |
| outcome: out-of-reach:registry | 5 | the Directory refuses by what a registry says (the graph is registered, for this repository, lists this file; the core graph is registered; an address is registered) |
| outcome: stricter:module-name-underscore | 1 | Go refuses a `module.name` that starts with `_` (the checker's pattern); the Directory's identifier pattern allows it |
| outcome: stricter:model-hcl-required | 1 | Go refuses a manifest that does not write `model.hcl`; the Directory accepts it |
| outcome: stricter:meaning-license-required | 2 | Go refuses a meaning file with no text `license`; the Directory compares it only when it is text |
| outcome: stricter:meaning-id-compared | 1 | Go refuses a meaning file whose `id` is not `meaning.graph.id`; the Directory does not read it in this stage |
| outcome: stricter:yaml-key | 1 | Go refuses a mapping key that YAML reads as a number, a boolean or null (the strict reader); the Directory's YAML library reads it as a key |
| outcome: stricter:yaml-anchor | 1 | Go refuses anchors and aliases; the Directory's library expands them |
| outcome: stricter:yaml-tag | 1 | Go refuses tags (`!`, `!!`); the Directory's library resolves them |
| outcome: stricter:yaml-directive | 1 | Go refuses a `%YAML` or `%TAG` directive; the Directory's library follows it |
| outcome: stricter:yaml-documents | 1 | Go refuses a document end marker and a second document; the Directory's library reads the first document |
| outcome: stricter:yaml-unsupported | 1 | Go refuses a quoted value written over more than one line; the Directory's library reads it |

The strict YAML reader refuses some meaning files that the Directory's library reads (the six `stricter:yaml-*` kinds above, one case each); and a concept that breaks a rule of the concepts' shape may be refused by the reader first, under its own code (a merge key, `%YAML 1.1`, an integer of 30 digits as a label), with the same verdict: do not expect `meaning-concept` for every one of them.

An `out-of-reach` label is mechanical, not a judgement: the case carries the record, key, registry or URL map (`fix`) under which the Directory accepts **the same
files**, the generator runs `analyseDatabase` again under it and stops unless the Directory accepts, and the golden says which (`acceptedWhen`). A refusal that no
record or registry could lift is not out of reach: `meaning.graph.address` must be the address of the record's repository, which must be `publisher.repository`, so the
check is offline once `publisher.repository` is written, and that is F7.

The looser cases are listed in `drift.json` (`goLooser`, `fileProbes`) with their slice, and a test holds the list to the golden both ways, as `TestDrift` does for
the manifest stage: a slice removes its entries in its own pull request. None of them is ported yet (this change ports no rule).

### Every refusal of the Directory's file stage, own form

Lines are of directory.mjs at the pinned commit (modelspec.mjs and meaning.mjs where said). The shared form (lines 578 to 701) needs the ModelSpec registry and other
repositories at their pins; it is not part of a check of one repository and stays for A4 and A5.

| Lines | The Directory refuses | Go | Cases |
| --- | --- | --- | --- |
| 357-368 | a commit that is not on the default branch of the hosted repository; the commit cannot be opened | needs the network and the record | none |
| 372-391 | OVDB.md or the manifest missing, not regular, front matter, `ovdb: 1`, `publish`, the manifest not listed | has | `has-ovdbmd-missing` |
| 396-411 | the descriptor listed and valid; its rules | has (#62); which file is the descriptor needs the record | none |
| 424-426 | `manifest.url`, `manifest.id` against the record | needs the record | `record-url`, `record-id` |
| 427 | `meaning.graph.id` against the record's `meaning_graph` | needs the record (Go compares it with the meaning file's `id`) | `record-graph-id` |
| 428 | `publisher.repository` against the record's repository | has, with `--repository` | `has-publisher-repository` |
| 490-505 | the graph is registered, for this repository, with this address, and lists `meaning.file` | needs the registry; the address is derived from the repository, so offline once `publisher.repository` is written: **F7** | `registry-*`, `has-graph-address` |
| 485-487 | the meaning file is not valid YAML, not a mapping, has no concepts list | has (#63) | `meaning-not-yaml`, `meaning-a-list`, `concept-no-concepts`, `meaning-concepts-null` |
| 510-513 | the model file or the meaning file is not a regular file at the commit | has | `model-file-missing`, `meaning-file-missing` |
| 514-515 | `parseModelSpec` (modelspec.mjs 92-118): not JSON, `module.name`, no entities | has | `modelspec-not-json`, `modelspec-module-name`, `modelspec-no-entities` (through the recordsets rule) |
| 514-515 | `parseModelSpec`: the `"modelspec"` version, entity and property names, properties, types, references | has (F2: `repo-model-version`, `repo-model-entity`, `repo-model-property`) | `modelspec-*` (10) |
| 517, 453-471 | the recordsets are the entities (own form; `recordsets_partial` is for a shared model) | has | `has-recordsets-*` |
| 470 | two recordsets mapped to one entity (the manifest stage cannot see it) | has | `has-recordsets-mapping-twice` |
| 521 | the meaning file has no concepts list | has (#63) | `concept-no-concepts` |
| 522 | `licences.meaning` against the file's `license` | has, and stricter (requires text) | `has-licence-differs`, `stricter-meaning-license-*` |
| 523 | `model.name` is the module | has | `has-model-name` |
| 526-535 | the `models:` entry: a safe relative path, ending in `.modelspec.hcl`, an existing regular file | has through `model.hcl`; **F6** for a manifest without it | `has-models-entry-*`, `nohcl-*` |
| 536 | `model.hcl` is that entry | has | `has-models-entry-hcl` |
| 537-545 | own-form `model.address`: host, lower case, module, no `?ref=` | has | `has-model-address-lower`, `-ref`, `-module`, `-host` |
| 546 | own-form `model.address` names the record's repository | **F7** (no profile compares it with `publisher.repository`) | `has-model-address-other-repository` |
| 549-572 | the model as the ModelSpec registry registers it | needs the registry | none |
| 708-712 | `validateConcept` (meaning.mjs 58-80) and a concept declared twice | has (F3: `meaning-concept`, `meaning-concept-duplicate`) | `concept-*` (27) |
| 728-754 | bindings | has (F4: `meaning-binding`) | `binding-*` (10) |
| 767-770 | chains (meaning.mjs 195-226) inside the own graph | has (F5: `meaning-chain`), including the form of a reference by address (it needs a pin, which is a full lower-case commit id); only a well-formed pinned reference to another graph is the registry's, and one to this graph the history's | `chain-*` (7), `chain-address-unregistered`, `chain-core-unregistered` |
| 775-781 | the page of every recordset, with the real names | has (the page loop of #58, code `manifest-recordsets`) | `has-recordset-page` |
| 784-800 | `representation_contract`: envelope, attachment, source data | has (#54; the attachment content checks are not audited, ovdb#61); the canonical meaning needs the registry | none |
| 890 | `claimProblems`: the claims of one record against the others | needs the other records (A1 and A4) | none |

## Nothing is left

Every refusal of the checker that needs a file now has a rule here. The rows of the table of package manifest's README that are
marked `files` or `input` are all made (the generator holds each to the checker's lines, and a test holds the README to the
generator). The judgments of the content of the two files read the model file with `readModel` (a token reader, bounded) and the
meaning file with `manifest.Judge.Meaning` (the strict reader that reads a manifest). `model.hcl` is never read, as in the checker: it
is a regular file, and its path is compared with the `models:` entry.

### How the model file's JSON is read, against `JSON.parse`

`readModel` uses the standard decoder for its tokens only, so no value is built that the file chooses the size of but the keys of
`entities`. Where the two differ, the choice is the one that cannot make Go looser:

| Difference | Go | Why |
| --- | --- | --- |
| a repeated key | the last one wins, for `module`, `module.name` and `entities` | as in `JSON.parse`: accepted as it is, with the same verdict |
| a string with a byte that is not UTF-8, or half of a surrogate pair | read (the decoder puts U+FFFD there, `JSON.parse` of a UTF-8 file the same, or the lone surrogate) | only the module name and the entity names are compared, and a module name must be ASCII; an entity name with such a character equals no recordset (they are ASCII) |
| a number too big for a double, `-0`, an exponent | read, not converted (`UseNumber`) | no number is compared |
| a byte order mark, a form feed, a comment, a trailing comma, single quotes, `NaN`, `01`, `+1`, `1.`, a raw tab or line break in a string, an invalid escape | refused | both refuse (cases in the golden) |
| text after the value, or a second value | refused | `JSON.parse` refuses; the decoder would read the first and stop, so `readModel` asks for the end |
| `__proto__` as a key | an ordinary key | `JSON.parse` makes it an own property, and `Object.keys` lists it; a recordset can be called `__proto__` |
| nesting deeper than 100 levels | refused, `repo-model-depth`, recorded below | `JSON.parse` has no bound; the reading is recursive and a file of 4 MiB can nest 2 million levels |
| a model file of more than 4 MiB | refused, `repo-file-size` (from slice 3b-1) | the checker reads 16 MiB |

### Reading the meaning file, against what a manifest can reach

The meaning file goes through the same strict reader as a manifest and OVDB.md, with the same bound (`document-size`, 262144 bytes), so
every kind of the reader recorded in the README of package manifest applies to it, and the golden has a case for each of those that
a meaning file can show (below, from `yaml` to `yaml-unsupported`). What a meaning file can reach that a manifest cannot is
structure: a manifest has nesting of three or four levels, a short list of recordsets and no long text; a meaning file has
the `concepts:` tree, long folded and literal scalars, lists of mappings, flow collections and long quoted values. The places of the reader
that these reach (the nesting limit of 64, the plain and quoted values over more than one line, block scalars, flow collections,
`sources:` lists) are exercised by the real Chinook meaning file (15 KiB, accepted by the checker and by Go) and by the cases of the group `yaml`; the corpus of package manifest
has them for manifests and for OVDB.md only. A meaning file that nests deeper than the reader's limit, or writes a long value in
quotes over several lines, is refused by Go and read by the checker: the kinds `yaml-limit` and `yaml-unsupported`.

## The proof

`testdata/reference/generate.mjs` builds each case as a real git repository (trees written
by hand, so a path that a working tree cannot hold can be built), runs the checker on it
without `--repository` and with it, and writes `repository.json` (the cases as operations on
the base repository, which is the Chinook repository's own consistent files, and the verdicts).
`go test` rebuilds each case in memory and fails if `Check` accepts what the checker refuses.
The slower test (`TestRealGit...`, run by the `publisher-goldens` job with `OVDB_REAL_GIT=1`)
builds each case as a real repository and requires that the real git, read through `Git` and
`ExecRunner`, finds exactly what `Memory` finds. `digests.json` holds the digest of the golden.

351 cases: 133 accepted by the checker, 124 accepted with `--repository`; 293 agree with Go, 58
are stricter in Go, in 32 kinds, 0 accepted by Go that the checker refuses. By group: 53 where a file is wrong
(5 files, each placed 10 or 11 ways), 36 where an object cannot be read, 40 model files, 45 JSON
differences, 70 meaning files, 30 YAML reader cases, 17 documents, 15 listed manifests, 10 tree names and sizes, 10 repository
states, 7 recordsets, 7 `--repository`, 4 limits (what one check may cost), 3 working tree, 3 fixtures (the Directory's `chinookdb` fixture, with and without
`--repository`, and the hoster example alone), 1 unchanged (the real Chinook repository's files).

Every case is held to the reason its name states, not only to a verdict: when the generator runs, each case has an
expectation written from its name (a case about a byte that is not UTF-8 is accepted by the checker, which reads the file
as UTF-8 and goes on; a case about a recordset that is not an entity is refused with a message about recordsets), and the
checker's own messages must bear it out, or the generator fails and lists every case that does not. The files of the cases are
bytes from the generator to the disk (a text is UTF-8; the one byte that is not is written as U+E000 in the source of the
case and as 0xFF in the file): the first golden of this check had two cases that the checker refused only because the bytes had been
mangled on the way, so they showed nothing about the bytes.

What one check may cost is bounded. The checker compares the recordsets of a manifest with the entities of the model by
searching a list for each of them, so its time grows with the product: 20,000 recordsets against a model of 330,000 entities
(3.8 MiB) took it 6.3 seconds, and a repository may list 32 manifests. This check compares with sets, so its time is
linear, and it holds two limits on top, each far above anything real (the Chinook model has eleven entities): a model file of
more than `MaxEntities` (10,000) entities is refused with `repo-model-entities-limit`, a stricter kind, and a manifest of more
than `MaxRecordsets` (10,000) recordsets is refused with `repo-recordsets-limit` and its recordsets are not compared. The second
is not a kind of its own in the table below: the checker accepts a manifest only when its recordsets are the entities of its
model, and a model of more than 10,000 entities is refused first, so no repository that the checker accepts has more recordsets
than the limit; it is there so that the work does not grow with the manifest. Both are tested: the worst case above is refused
at once, and 32 manifests of 10,000 recordsets against a model of 10,000 entities are checked in well under the 20 seconds the test allows.

### Recorded differences: where Go is stricter

| `document-size` | 3 | OVDB.md or a manifest of more than 262144 bytes is refused before it is read; the checker reads files of up to 16 MiB. |
| `meaning-concepts` | 4 | The meaning file has no concepts list (the key is missing, or is null, a mapping or text): the Directory refuses it (directory.mjs, parseMeaningFile) and the Chinook checker never reads the concepts. |
| `repo-model-entity` | 1 | An entity of the model file has a name that is not an identifier, or no properties: the Directory refuses it (parseModelSpec); the checker reads only the names of the entities. |
| `repo-model-property` | 1 | A property of the model file has a name that is not an identifier, a type that is not a type name, neither a type nor an entity, or references an entity the model lacks: the Directory refuses it (parseModelSpec); the checker never reads the properties. |
| `repo-model-version` | 1 | The model file has no "modelspec" version that is text: the Directory refuses it (parseModelSpec, modelspec.mjs); the checker reads only the module and the names of the entities. |
| `repo-case-collision` | 7 | Two names in a directory on the path of a file that is judged differ only in case, so they are one file on a case-insensitive file system; the checker reads the exact name and accepts. |
| `repo-file-size` | 1 | A file that a manifest names (the model file or the meaning file) of more than 4194304 bytes (MaxFileBytes) is refused; the checker reads files of up to 16 MiB. |
| `repo-manifests-limit` | 1 | OVDB.md lists more than 32 manifests; the checker judges every one. |
| `repo-model-depth` | 4 | The model file nests arrays and objects more than 100 levels deep (the top object is the first level); JSON.parse has no bound. |
| `repo-model-entities-limit` | 1 | The model file has an entities object of more than 10000 entities (MaxEntities); the checker compares each recordset with each entity, so 20000 recordsets against 330000 entities took it 6 seconds. |
| `repo-partial-clone` | 2 | A partial clone (--filter=blob:none or --filter=tree:0) that lacks an object the commit needs: the checker's git fetches the object from the remote, which this check never does (a repository that a remote can make run a command must not be asked to); the message says to check a full clone or to fetch the files first. |
| `repo-subdirectory` | 1 | The directory is inside a repository and not its top; the checker reads it as if it were the top, with a note, and the Directory reads OVDB.md at the top. |
| `repo-tree-limit` | 1 | A directory on the path of a file that is judged has more than 50000 entries; the checker asks git about one path and has no bound. |
| `repo-tree-name` | 3 | A directory on the path of a file that is judged has an entry whose name is empty or . or .. or .git, or has a slash, a backslash or a control character; the checker never lists a directory. |
| `yaml` | 1 | The reader accepts a subset of YAML and refuses a structure it cannot place (here a flow collection used as a key); the checker's library reads it. |
| `yaml-anchor` | 2 | The reader refuses anchors and aliases (& and *) and merge keys (<<): it reads a document once, as written, and expanding references is how a small file becomes a large one. |
| `yaml-character` | 1 | The reader refuses characters that YAML 1.2 does not allow in text, among them the C1 controls such as U+0085; the checker's library reads them into a string. |
| `yaml-directive` | 1 | The reader refuses a %YAML or %TAG directive; the checker's library follows it. |
| `yaml-documents` | 1 | The reader refuses a document end marker (`...`) and a second document; the checker's library reads the first document and ignores what follows. |
| `yaml-encoding` | 2 | The reader refuses a file that is not UTF-8 text (a byte that is not UTF-8, a NUL character); the checker's library reads a file as UTF-8, replaces the bytes it cannot decode and goes on. |
| `yaml-escape` | 1 | The reader refuses a double-quoted escape that is not a character, such as half of a surrogate pair (\ud83c); the checker's library accepts it. |
| `yaml-key` | 1 | The reader refuses a key that YAML reads as a number, a boolean or null (2024, true, null) and wants it in quotes; the checker's library accepts it as a key. |
| `yaml-limit` | 1 | The reader refuses collections nested more than 64 levels deep (63 is read); the checker's library reads any depth. |
| `yaml-line-ending` | 1 | The reader refuses a carriage return that is not part of CRLF; the checker's library reads it as a line break. |
| `yaml-number` | 3 | The reader refuses numbers it cannot hold exactly or that are not finite: hexadecimal and octal numbers, .inf, .nan, and integers beyond 2^53; the checker's library reads them as numbers. |
| `yaml-tab` | 1 | The reader refuses a tab where YAML allows it but whose reading differs between parsers (after a colon, in indentation). |
| `yaml-tag` | 1 | The reader refuses tags (!, !!), which the checker's library resolves; it reads plain values only. |
| `yaml-unsupported` | 2 | The reader refuses constructs outside its subset: explicit keys (`? key`), and a quoted value written over more than one line, which a YAML tool writes back for any long string; the checker's library reads both. |

A path that git cannot report is not tested: git prints only the modes 100644, 100755, 120000,
160000 and 040000 (it canonicalises the others, so `100664` is `100644`).

### Regenerate

`node internal/publisher/repo/testdata/reference/generate.mjs` (add `--check` to only compare;
`--explain <text>` prints the checker's problems for the cases whose name has the text). It needs
Node 24, git and the network, like the other generators, and fetches the references fresh. A clone passed with
`--directory` or `--chinookdb` is checked as committed (no `extensions.worktreeConfig`, no `config.worktree`), but
its `node_modules/yaml` is not: a clone you pass is trusted code.

## Attached representation checks

Default `Check` uses the same immutable repository/revision reader pool for
metadata closure and the separate format3 exact raw source-data proof. The
attachment must match this manifest's local model, binding and published target/
bridge recordsets. Metadata resolution never reads `source.data`, `native.dataset`,
native chunks or a native key corpus. Source-data proofs hash raw committed bytes
(including malformed UTF8 or a BOM) without parsing rows, at a separate exact
5MiB cap. Distinct full references are verified sequentially and deduplicated only
within this repository check, across at most 32 manifests with 32 contracts each.
Proof caching retains bounded metadata and releases source bytes.

The metadata-only `CheckRepresentation` helper does not prove source-data bytes.
The default check requires them and refuses missing proof. Neither result grants
semantic admission, guarantees row interpretation or runtime success, or substitutes
for Directory's independent canonical admission review. Chinook's offline precheck
retains explicit unresolved external notes and is a partial result.

Reference pins are in `../references.mjs`. Like-strength structural/data parity
with Directory preserves conservative JavaScript safe-integer and null-count
refusals; Go retains exact integer tokens for native count associations. These
expected differences do not constitute full canonical or runtime parity.

Current canonical checker references: Directory `087067483686865b13cb76511ff86f7364ea47ff` and demo-db/chinook `8b904298d0c3bba20c12dfbc29bb75bf5c37f683`. The prior exact datatug/chinookdb `79e7bb0b1d6f0666dce465874990dec64348331f` supplies only frozen corpus documents and mined literal inputs; its code is not imported as a reference validator.

The checked `representation-stages.json` golden compares 26 cases across two
independent native metadata fixtures at the landed Directory validator. Metadata
never reads source or native data; the separate byte stage includes 5MiB boundaries,
raw BOM/invalid UTF8 bytes and reader/mode/hash refusals. Its result strength is
explicitly structural metadata plus offline raw bytes, without canonical admission.

The general canonical identity URL predicate in newer JavaScript companions
accepts root/nested identity paths and trailing slashes. Released Go at
`27f2664782e01feb4fb5938f28b8f8eae0a868a8` requires an `ovdb` marker and refuses a
trailing slash; this pre-existing stricter Go predicate is preserved. W1 Cloud
`/ovdb/dbs/` identities satisfy it. The frozen corpus stays at its exact legacy
fixture bytes, while the reference code runs at the current landed JS pins.

Attached Git checks and all explicit Git dependency readers use original commit,
tree and blob objects, with `git --no-replace-objects` on every command. The policy
is selected by the reader, independent of caller environment variables. An already
selected commit ID is retained and its original commit type is independently
verified; an unselected view pins HEAD before any provider reads. Dependency revision
comparison, tracked regular-file mode, direct commit path and raw-byte checksum
must all match. Grafts can alter ancestry but these checks never traverse ancestry.
Existing environment isolation and promisor lazy-fetch refusal still apply.

A bounded discovery pass examines the original commit's `OVDB.md` and at most 32
listed manifests (256 KiB per document), plus the legacy view when needed. It reads
no models, meaning files, source bytes or native corpus. This prevents replacement
objects from hiding an original attachment. Discovery distinguishes presence,
confirmed absence and uncertainty. Original read, type, size, parsing or count
failures select original validation and retain the refusal even if a retry succeeds.
Only valid original manifests all proven unattached permit the legacy reader;
an attachment declared by the legacy view still selects original validation.
Discovery, mode selection and full validation retain one selected commit ID.
If an attachment appears later in the legacy validation pass, the check restarts
on that commit's original objects with a fresh judge and empty file/tree/proof
caches; no legacy manifest association or metadata contributes to its proofs.
The metadata-only helper pins original provider objects before mode or byte reads
and also selects original dependency objects.
Caller-supplied non-Git readers remain trusted immutable-reader seams.
