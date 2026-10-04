# internal/publisher/repo

Judges a repository as far as the presence of files goes, for the future
`ovdb publisher check` (slice 3c; nothing here is wired into the command, and the
shipped binary links none of `internal/publisher`). It reads one commit, at HEAD,
as committed and never the working tree, through a `Reader`, and hands OVDB.md and
the manifests to package `manifest`. The reference is the Chinook checker
(`datatug/chinookdb@79e7bb0b1d6f0666dce465874990dec64348331f`,
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

Git is called with `--literal-pathspecs -c core.fsmonitor=false`, with every `GIT_*`
variable of the caller dropped, no system or user configuration, no prompt, no lazy fetch
of a missing object and no optional lock. A path given to git is first checked with
`rules.IsRepositoryPath` and follows the commit id (`<id>:<path>`), so git never reads it as
an option. Calls: `rev-parse --is-bare-repository --show-prefix`, `rev-parse --verify --quiet
HEAD^{commit}`, `ls-tree -z <id>[:<dir>] --` (output read up to `MaxTreeBytes`, 4 MiB; at
most `MaxEntries`, 50000, entries), `cat-file blob <id>:<path>` (read up to the limit given:
`MaxDocumentBytes`+1), `ls-files -z --cached --others -- <path>`. Output is read through a
bound and git is stopped when it is passed, and each call has 30 seconds. The listing is
parsed strictly (`parseTree`, fuzzed by `FuzzParseTree`). A directory that is listed is
refused when it has a name no path may have (empty, `.`, `..`, `.git`, a slash, a backslash,
a control character) or two names that differ only in case.

The repository states, each proved on real git by the slower test:

| State | Result |
| --- | --- |
| not a repository, git cannot run | `repo-unreadable`, with git's last line of standard error |
| unborn branch (no commit) | `repo-no-commit` |
| bare repository | `repo-bare`: the checker reads `HEAD:./path`, which git refuses outside a working tree, so it refuses; so does this check |
| a directory inside a repository | `repo-subdirectory` |
| shallow clone, detached HEAD | read like any other: the checker reads them too |
| `OVDB.md` only in the working tree or the index | `repo-ovdbmd`, saying it is not committed |

A partial clone is read without fetching (`GIT_NO_LAZY_FETCH`, git 2.44 and later; an
older git may try, and the 30 seconds end it).

## The rules made here

Each is a tracked regular file at HEAD (an executable counts, as in the checker; a
directory, symlink, submodule, a missing path, a path only in the working tree or index,
and a path below a file or symlink do not):

| Rule | What | Checker (`ovdb-manifest.mjs`) |
| --- | --- | --- |
| `repo-ovdbmd` | OVDB.md is at the root | 186-189 |
| `repo-manifest` | every manifest that OVDB.md lists | 221-223 |
| `repo-file` | `model.modelspec`, `model.hcl`, `meaning.file` of an own-form manifest | 374-392 |
| `repo-repository` | `publisher.repository` equals `--repository` exactly (compared only when the manifest rules accept `publisher.repository`; when they do not their finding says so) | 309 |
| `document-size` | OVDB.md and each manifest of at most 262144 bytes | 194, 244-246 |

Every other manifest OVDB.md lists is judged with the Publisher profile through
`manifest.Judge` (the checker's line 226).

## Not yet made (slice 3b-2)

These need the content of the model and the meaning file: the model JSON's module and
entities (checker lines 408-415), `model.name` against the model file (420), the own-form
`model.address` module against the model file (426-429), the meaning file as YAML with its
`id`, licence and `models:` entry (445-468), and `recordsets` against the entities (535-539).
The files are not read here (only their kind).

## The proof

`testdata/reference/generate.mjs` builds each case as a real git repository (trees written
by hand, so a path that a working tree cannot hold can be built), runs the checker on it
without `--repository` and with it, and writes `repository.json` (the cases as operations on
the base repository, which is the Chinook repository's own consistent files, and the verdicts).
`go test` rebuilds each case in memory and fails if `Check` accepts what the checker refuses.
The slower test (`TestRealGit...`, run by the `publisher-goldens` job with `OVDB_REAL_GIT=1`)
builds each case as a real repository and requires that the real git, read through `Git` and
`ExecRunner`, finds exactly what `Memory` finds. `digests.json` holds the digest of the golden.

106 cases: 37 accepted by the checker, 30 accepted with `--repository`; 91 agree with Go, 15
are stricter in Go, 0 accepted by Go that the checker refuses. By group: 51 where a file is wrong
(5 files, each placed 10 or 11 ways), 17 documents, 10 tree names and sizes, 9 listed manifests,
7 `--repository`, 6 repository states, 3 working tree, 1 unchanged.

### Recorded differences: where Go is stricter

| `document-size` | 2 | OVDB.md or a manifest of more than 262144 bytes is refused before it is read; the checker reads files of up to 16 MiB. |
| `repo-case-collision` | 7 | Two names in a directory on the path of a file that is judged differ only in case, so they are one file on a case-insensitive file system; the checker reads the exact name and accepts. |
| `repo-manifests-limit` | 1 | OVDB.md lists more than 32 manifests; the checker judges every one. |
| `repo-subdirectory` | 1 | The directory is inside a repository and not its top; the checker reads it as if it were the top, with a note, and the Directory reads OVDB.md at the top. |
| `repo-tree-limit` | 1 | A directory on the path of a file that is judged has more than 50000 entries; the checker asks git about one path and has no bound. |
| `repo-tree-name` | 3 | A directory on the path of a file that is judged has an entry whose name is empty or . or .. or .git, or has a slash, a backslash or a control character; the checker never lists a directory. |

A path that git cannot report is not tested: git prints only the modes 100644, 100755, 120000,
160000 and 040000 (it canonicalises the others, so `100664` is `100644`).

### Regenerate

`node internal/publisher/repo/testdata/reference/generate.mjs` (add `--check` to only compare;
`--explain <text>` prints the checker's problems for the cases whose name has the text). It needs
Node 24, git and the network, like the other generators, and fetches the references fresh.
