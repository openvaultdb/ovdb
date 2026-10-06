# internal/publisher/rules

The pure rules that a publisher's own OVDB manifest is held to. This package is
the base of `ovdb publisher check`, the command that checks what a publisher
keeps in their repository (see the README of the module); the `ovdb` binary links it.

"Pure" means no file, no network, no clock and no YAML: strings in, a verdict
out. Reading the manifest, the repository and the Directory's records comes in
later changes and calls these functions.

## The rules

| Function | Accepts |
| --- | --- |
| `PublicHTTPSURL` | `https://host/path` and nothing else: no userinfo, port (not even `:443`), query, fragment or percent escape; a lower-case host of letters, digits and hyphens in dot-separated labels (1 to 63 bytes, none starting or ending with a hyphen, at least two labels, at most 253 bytes in all, no trailing dot); no IP address or name that could be read as one (a last label of digits, or `0x` and hex digits), no single-label name, no local, internal or reserved suffix; a path of only `A-Z a-z 0-9 . _ ~ / -`, starting with `/`, with no `//` and no `.` or `..` segment; one canonical spelling. |
| `PublicHTTPSURLTemplate` | The same, with the literal `{name}` exactly once, in the path only. |
| `Homepage` | `PublicHTTPSURL` and at most 200 bytes. |
| `IsID` | `^[a-z0-9]+(-[a-z0-9]+)*$`, at most 80 bytes. |
| `IsCommit` | 40 lower-case hex digits. |
| `RepositoryKey`, `CompareKey` | `https://github.com/{org}/{repo}`: an allow-listed host (today only `github.com`) with exactly its number of path segments (2), each of `A-Z a-z 0-9 . _ -`, none of them `.` or `..`, the last not ending in `.git` in any case, no trailing slash. The key is `host/org/repo` as written; `CompareKey` is its lower case, the key to compare two repositories by. |
| `IsRepositoryPath` | A path to a file in a repository: relative, only `A-Z a-z 0-9 . _ / -` (so no backslash, glob or space), no `//`, no `.` or `..` segment, no trailing `/`. |
| `IsPublishEntry` | An entry of an OVDB.md `publish` list: the explicit form `./` followed by an `IsRepositoryPath`. |
| `IsEngine` | `^[A-Za-z][A-Za-z0-9_.+-]{0,39}$`. |
| `IsLicenceID` | The shape of an SPDX licence id, `^[A-Za-z0-9][A-Za-z0-9.+-]{0,63}$`; it does not know which ids SPDX has assigned. |
| `IsBlank` | Whether a text is empty or only white space **as JavaScript's `trim()` sees it** (it strips U+FEFF and not U+0085; Go's `strings.TrimSpace` does the reverse). Every "is required" check must use it, never `strings.TrimSpace`. |
| `GlobalDatabaseID` | A database's canonical url as the Directory takes it since it published global identities: every rule of `PublicHTTPSURL` (a trailing slash is fine; one path segment or several), and two differences. A path segment may hold percent escapes when it is the canonical encoding of its text (see `EncodePathSegment`) and the text, decoded again and again, never becomes `.`, `..`, a slash, a backslash or a control character; and a host under `.example` is allowed. |
| `RecordsetName` | A recordset name as the Directory takes it since it took native names: text that is not blank by JavaScript's `trim()`, at most 256 UTF-16 code units (an astral character counts for two), not `.` or `..`, with no `/`, `\` or control character (U+0000 to U+001F, U+007F). A space, a dot and any non-ASCII letter are allowed: `dbo.DatabaseLog`, `Order Details`. |
| `EncodePathSegment` | JavaScript's `encodeURIComponent`, with `! ' ( ) *` encoded too: everything except `A-Z a-z 0-9 - _ . ~` as `%XX`, in upper case. |
| `RecordsetPage` | The page of a recordset: the `deployment.recordset_page` template with `{name}` replaced by the encoded name. A name that needs no encoding makes an ordinary public https URL. One that does is accepted only as one whole path segment of the template's path (nothing else shares it), when nothing in the name, decoded again and again, can become `.`, `..`, a slash, a backslash or a control character, and the rest of the URL passes the ordinary rules. The page has no length bound of its own, as the Directory has none: the name is bounded (256 UTF-16 code units, 2304 characters at most once encoded) and the template is a URL of at most 2048, so a page is judged in one pass however long. |
| `Compare`, `ClaimedForm` | How one claimed address stands to another: `Same` when they are equal, `Under` when the first sits under the second at a path-segment boundary (`/dbs/chinook2` is not under `/dbs/chinook`), `Over` when the second sits under the first, `Apart` otherwise, all case-insensitively with trailing slashes set aside. A caller asks `Relation.Conflicts()` (true for everything but `Apart`, and for `Incomparable`, which is also the zero value), and never compares with `Same` or `Under` itself. |

A refused URL comes back as a `*Problem` with a stable `Rule` (match on that,
never on the message) and a detail in words. `ParsePublicHTTPSURL` and
`ParsePublicHTTPSURLTemplate` also return the parts of an accepted URL (`Host`,
`Path`, and for a template the offset of `{name}` in the path), so a rule that
looks at the host or the path never splits the text again. A piece of the input
reaches a message only through one function that cuts it to 40 bytes, quotes it
and escapes it to printable ASCII, so no message holds a control character or a
line break (a forged log line); a test holds every refusal of the matrix to that.

### Limits on input

Every function bounds its input before it reads it, then reads it once, left to
right. An input over the bound is refused (`Compare` says `Incomparable`, which
a caller looking for conflicts must treat as one).

| Bound | Bytes | Where it comes from |
| --- | --- | --- |
| `MaxHostLength`, `MaxLabelLength` | 253, 63 | the references |
| `MaxHomepageLength` | 200 | the references |
| `MaxIDLength` | 80 | the references |
| `MaxEngineLength`, `MaxLicenceLength` | 40, 64 | the references' patterns |
| `MaxURLLength` | 2048 | this package; the references have no bound |
| `MaxPathLength` | 1024 | this package; the references have no bound |
| `MaxRepositoryLength` | 255 | this package; the references have no bound |
| `MaxClaimLength` | 2048 | this package |

## Why the URL rules are written on the text

The URL functions are a hand-written reader of the plain subset above. They do
not give the string to `net/url` and inspect the result, because Go's parser and
the WHATWG parser that JavaScript has disagree on hosts whose last label is
numeric, on punycode, on backslashes and on control characters. The rule here is
that **a Go function never accepts a string that the JavaScript refuses**.
`net/url` is used in the tests, as a judge: the fuzz targets check that what is
accepted, parsed again by it, has scheme `https`, no user, no port, no query and
no fragment.

## The references and the proof that Go never accepts more

The rules are those of the OVDB Directory, and of the Chinook database's port of
them (both in JavaScript, which is where a publisher's check meets them today):

| Reference | Repository | Commit | Files |
| --- | --- | --- | --- |
| directory | `openvaultdb/directory` (CC0-1.0) | `087067483686865b13cb76511ff86f7364ea47ff` | `scripts/lib/urls.mjs`, `git.mjs`, `directory.mjs` |
| chinookdb | `demo-db/chinook` (MIT) | `8b904298d0c3bba20c12dfbc29bb75bf5c37f683` | `scripts/lib/directory-rules.mjs` |

`testdata/reference/generate.mjs` fetches those files at exactly those commits,
imports them as they are, runs their functions over a generated matrix and
writes the verdicts to `testdata/reference/matrix.golden.json` (Node
v24.19.0 made the committed one). `go test` reads the golden and judges every
verdict of the Go functions against it; it starts no process and needs no
network. The matrix is **2584083** verdicts:

- every character U+0000 to U+FFFF, placed in the host (first, middle, last, last
  label), after the host, in the path, before the scheme and after the end of a
  valid URL, for URLs, templates and homepages, and in the middle, first and last
  position of ids, repositories, paths, publish entries, engines and licences
  (the smaller rules sweep U+0000 to U+024F and the surrogate and full-width
  edges);
- every string of a small alphabet up to a length, for the shapes that have
  structure: hosts ending in numeric, hex-looking and odd last labels, `xn--`
  labels, path shapes, ids, repositories, engines, licences;
- every string literal written as a URL in the two JavaScript test suites
  (`openvaultdb/directory` `scripts/test.mjs`, `datatug/chinookdb`
  `scripts/test-model.mjs`), and hand-written cases: ports, userinfo, `%2F`,
  `//`, dot segments, trailing dots, upper-case hosts, numeric last labels,
  punycode labels spelled by Node's own encoder, reserved suffixes, templates,
  and the length of every limit and one over;
- every character U+0000 to U+FFFF alone, led, trailed and between spaces, for the
  blank-text check, and every white-space and format character in a list;
- `xn--` labels whose decoded text begins with `xn--` or has hyphens in its third
  and fourth positions, with every Latin-1 letter;
- every pair of a list of claimed addresses, for `Compare` (apart, same, under
  and over, as the Directory's own claim check reports them in each direction).

`TestReferenceMatrix` fails if any Go function accepts where a reference
refuses, if a Go function is stricter in a way that is not recorded below, or if
a recorded difference never happens in the matrix. `go test -v -run
TestReferenceMatrix ./internal/publisher/rules` prints the counts.

### Recorded differences: where Go is stricter

Go may refuse what the JavaScript accepts. Each kind is recorded here with its
number of cases in the matrix, and a test proves it is real and nothing else:
with the one limit named taken away, Go accepts the same input.

| Kind | Cases | Why |
| --- | --- | --- |
| `url-length` | 11 | A URL over 2048 bytes. The references have no bound; a published URL is text that people and tools read, and an unbounded input is a way to make a check slow. |
| `punycode-decoded-hyphens` | 376 | An `xn--` label that is valid punycode of Latin-1 letters but decodes to text that begins with `xn--` or has hyphens in its third and fourth positions. As UTS #46 reads it (15.1 on), such a label is invalid when hyphens are not checked, so a later Node may refuse it; Node v24.19.0 accepts it. One comparison makes the rule independent of the Node version. |
| `punycode-other-text` | 3442 | An `xn--` label that is valid punycode of text other than Latin-1 lower-case letters (Cyrillic, CJK, control characters, ...). Which code points UTS #46 accepts changes with every Unicode release and Go has no copy of its tables, so a label is accepted only when it spells Latin-1 letters (U+00E0 to U+00FF without U+00F7), which have always been valid. `xn--bcher-kva.de` passes; `xn--80ak6aa92e.com` does not, though Node accepts it. The message says which letters a label may spell, that other scripts and letters are not accepted yet, and to use an ASCII host name; whether to accept more is a product decision. |
| `punycode-malformed` | 50 | An `xn--` label that is not punycode of any text (truncated, ASCII only, a number too large). Node's URL parser takes such labels as written; a hostile publisher could use one to name a host no client can resolve the same way. The message says it is not valid punycode and to write the host name in ASCII. |
| `repository-length` | 4 | A repository URL over 255 bytes. The references have no bound; GitHub names are far shorter. |
| `path-length` | 3 | A path inside a repository over 1024 bytes. The references have no bound. |
| `claim-incomparable` | 640 | `Compare` of an address that is not ASCII or is over 2048 bytes. JavaScript folds case by Unicode rules (U+212A KELVIN SIGN lowers to `k`), which this package does not copy; addresses that reach a comparison have already passed `PublicHTTPSURL`, so this never happens to a valid one, and `Incomparable.Conflicts()` is true. |

The references agree with each other on every verdict of the matrix: where the
Chinook port has drifted from the Directory it is in message text only.

### Regenerating the golden

Only when a reference changes (a new pinned commit) or the matrix does:

```sh
node internal/publisher/rules/testdata/reference/generate.mjs           # rewrite the golden
node internal/publisher/rules/testdata/reference/generate.mjs --check   # fail if it is stale
```

Needs Node 24 or later, git, npm and network access. The two commits are pinned
in `internal/publisher/references.mjs` (which `reference_test.go` reads, and fails if the golden
was made from other ones); to use clones you already have, pass `--directory
<dir>` and `--chinookdb <dir>`, which must be at those commits. The golden is
kept small by storing, for the exhaustive families, only the accepted
characters or strings (or the refused ones, when fewer). The test also fails
when this README states another size, count, commit or Node version than the
golden. The generator also asserts that the two rules the Directory keeps inline
(the `isText` test and the publish-entry expression) are still in `directory.mjs`
at the pinned commit as copied, and says to run `npm ci` in a `--directory`
clone that has no `yaml`.

## Fuzzing

```sh
go test -run '^$' -fuzz FuzzPublicHTTPSURL -fuzztime 60s ./internal/publisher/rules
go test -run '^$' -fuzz FuzzRepositoryKey  -fuzztime 60s ./internal/publisher/rules
go test -run '^$' -fuzz FuzzRepositoryPath -fuzztime 60s ./internal/publisher/rules
```

They assert no panic and the guarantees above (for a URL, re-parsed by `net/url`:
scheme `https`, no user, no port, no query, no fragment, and the same text back).
Under plain `go test` they run only their seed inputs.

## Exact coverage

The packages of the publisher check are held to exactly 100% statement coverage
by a gate scoped to them, `cmd/covergate` (the exact gate of
`modelspec-org/cli` and `meaninggraph/cli`, cut down to a list of packages). The
module as a whole cannot be gated this way: some of its packages have a
`TestMain`, and some have files for one operating system, and the gate refuses
both. The list is in `.github/workflows/ci.yml` (job `publisher-coverage`), and
a test keeps it equal to every package below `internal/publisher`,
`internal/covergate` and `cmd/covergate`.

The gate also refuses whatever lets the build leave a file out of the profile:
a build constraint in any spelling that Go reads (`//go:build`, `// +build`,
`//+build`, extra spaces or a tab, judged by `go/build/constraint` on the header
before the package clause), a GOOS or GOARCH file name, and `import "C"` (left
out when cgo is off). It refuses `//line` and `/*line*/` directives in any file
(found with the scanner's comments, so the same text in a string is not one):
the profile names a block by the file a directive gives and the physical line
and column, so a block that never ran could take the location of a covered one
of another file and be merged into it as covered. A profile with the same
location listed with two statement counts is refused. And it closes the class by a file-set rule: every
non-test file of a gated package that has a statement must have a block in the
profile, so a file left out for any other reason fails the gate by name. To run
it locally:

```sh
go test -covermode=atomic -coverprofile=/tmp/cover.out ./internal/publisher/... ./internal/covergate/... ./cmd/covergate/...
go run ./cmd/covergate /tmp/cover.out ./internal/publisher/exitcode ./internal/publisher/rules ./internal/covergate ./cmd/covergate
```

## Not part of the parity matrix

`ovdb publisher check` is not a CLI, TUI, web and API feature of a running
database, so it stays outside `internal/parity`.

### Decision: internationalised host names (2026-10-04)

Internationalised host names are refused for now: a host is accepted only when it is ASCII, or
spells Latin-1 letters through `xn--` (what the matrix above shows `rules` accepts, and nothing
more). The Unicode tables that UTS #46 needs change with every release and Go has no copy; until
there is a reason to carry them, a publisher writes the ASCII host name. Accepting more is a
product decision, and the kinds `punycode-other-text` and `punycode-malformed` are its record.

Current canonical checker references: Directory `087067483686865b13cb76511ff86f7364ea47ff` and demo-db/chinook `8b904298d0c3bba20c12dfbc29bb75bf5c37f683`. The prior exact datatug/chinookdb `79e7bb0b1d6f0666dce465874990dec64348331f` supplies only frozen corpus documents and mined literal inputs; its code is not imported as a reference validator.
