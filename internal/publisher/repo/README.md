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
- **`refs/replace` is honoured**, as the checker does: "the commit as committed" is the commit as
  this clone's replace refs show it. Turning them off would make this check accept a repository the
  checker refuses. The Directory reads the hosted repository, not this clone, and will not see
  replace refs.
- **git is never allowed to fetch.** `GIT_NO_LAZY_FETCH` stops a partial clone from fetching a
  missing object from its promisor remote, whose URL can be a command of the repository's
  choosing. git older than 2.44 does not know the variable, so `Head` reads `git version` first and
  refuses to go on with anything older (`repo-git-version`).

The repository states, each proved on real git by the slower test:

| State | Result |
| --- | --- |
| not a repository, git cannot run, a git that cannot be asked | `repo-unreadable`, with git's last line of standard error |
| git older than 2.44 | `repo-git-version`, saying to update git |
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

Every other manifest OVDB.md lists is judged with the Publisher profile through
`manifest.Judge` (the checker's line 226).

## Not yet made (slice 3b-2)

These need the content of the model and the meaning file: the model JSON's module and
entities (checker lines 408-415), `model.name` against the model file (420), the own-form
`model.address` module against the model file (426-429), the meaning file as YAML with its
`id`, licence and `models:` entry (445-468), and `recordsets` against the entities (535-539).
The two files are read (up to 4 MiB) only to know that they can be; their content is not judged,
so an empty model file (408) or an empty or non-YAML meaning file (448, 451) is accepted here and
refused by the checker.

## The proof

`testdata/reference/generate.mjs` builds each case as a real git repository (trees written
by hand, so a path that a working tree cannot hold can be built), runs the checker on it
without `--repository` and with it, and writes `repository.json` (the cases as operations on
the base repository, which is the Chinook repository's own consistent files, and the verdicts).
`go test` rebuilds each case in memory and fails if `Check` accepts what the checker refuses.
The slower test (`TestRealGit...`, run by the `publisher-goldens` job with `OVDB_REAL_GIT=1`)
builds each case as a real repository and requires that the real git, read through `Git` and
`ExecRunner`, finds exactly what `Memory` finds. `digests.json` holds the digest of the golden.

131 cases: 45 accepted by the checker, 38 accepted with `--repository`; 113 agree with Go, 18
are stricter in Go, 0 accepted by Go that the checker refuses. By group: 53 where a file is wrong
(5 files, each placed 10 or 11 ways), 18 where an object cannot be read, 17 documents, 12 listed
manifests, 10 tree names and sizes, 10 repository states, 7 `--repository`, 3 working tree, 1 unchanged.

### Recorded differences: where Go is stricter

| `document-size` | 2 | OVDB.md or a manifest of more than 262144 bytes is refused before it is read; the checker reads files of up to 16 MiB. |
| `repo-case-collision` | 7 | Two names in a directory on the path of a file that is judged differ only in case, so they are one file on a case-insensitive file system; the checker reads the exact name and accepts. |
| `repo-file-size` | 1 | A file that a manifest names (the model file or the meaning file) of more than 4194304 bytes (MaxFileBytes) is refused; the checker reads files of up to 16 MiB. |
| `repo-manifests-limit` | 1 | OVDB.md lists more than 32 manifests; the checker judges every one. |
| `repo-partial-clone` | 2 | A partial clone (--filter=blob:none or --filter=tree:0) that lacks an object the commit needs: the checker's git fetches the object from the remote, which this check never does (a repository that a remote can make run a command must not be asked to); the message says to check a full clone or to fetch the files first. |
| `repo-subdirectory` | 1 | The directory is inside a repository and not its top; the checker reads it as if it were the top, with a note, and the Directory reads OVDB.md at the top. |
| `repo-tree-limit` | 1 | A directory on the path of a file that is judged has more than 50000 entries; the checker asks git about one path and has no bound. |
| `repo-tree-name` | 3 | A directory on the path of a file that is judged has an entry whose name is empty or . or .. or .git, or has a slash, a backslash or a control character; the checker never lists a directory. |

A path that git cannot report is not tested: git prints only the modes 100644, 100755, 120000,
160000 and 040000 (it canonicalises the others, so `100664` is `100644`).

### Regenerate

`node internal/publisher/repo/testdata/reference/generate.mjs` (add `--check` to only compare;
`--explain <text>` prints the checker's problems for the cases whose name has the text). It needs
Node 24, git and the network, like the other generators, and fetches the references fresh. A clone passed with
`--directory` or `--chinookdb` is checked as committed (no `extensions.worktreeConfig`, no `config.worktree`), but
its `node_modules/yaml` is not: a clone you pass is trusted code.
