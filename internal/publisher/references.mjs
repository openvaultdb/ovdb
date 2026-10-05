// The references that the generators of the parity goldens read, and where they live: the one place to change when a
// reference moves to another organisation or to another commit. Each generator (rules/testdata/reference and
// manifest/testdata/reference) imports this file; nothing else in the repository names a reference's location.
//
// A reference is fetched at exactly its commit, by `remoteUrl`, into a directory that only this run can write (see checkoutReference).
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

export const references = {
  directory: { repository: 'openvaultdb/directory', commit: '087067483686865b13cb76511ff86f7364ea47ff' },
  chinookdb: { repository: 'demo-db/chinook', commit: '8b904298d0c3bba20c12dfbc29bb75bf5c37f683' },
  fixtures: { repository: 'datatug/chinookdb', commit: '79e7bb0b1d6f0666dce465874990dec64348331f' },
};

// The URL a reference is fetched from.
export const remoteUrl = (name) => `https://github.com/${references[name].repository}.git`;

// Git as the checks use it: the environment of the caller is not trusted (a GIT_DIR, a GIT_INDEX_FILE, a GIT_ALTERNATE_OBJECT_DIRECTORIES or a config
// in the home directory could point it anywhere), replace objects are off (`git replace` makes a commit show another tree), and no global or system
// config is read.
const gitEnv = () => {
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('GIT_')));
  return { ...env, GIT_NO_REPLACE_OBJECTS: '1', GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null', GIT_TERMINAL_PROMPT: '0' };
};
const run = (cwd, command, args, input) => execFileSync(command, args, { cwd, env: command === 'git' ? gitEnv() : process.env, input, stdio: [input === undefined ? 'ignore' : 'pipe', 'pipe', 'inherit'], encoding: 'utf8' }).trim();
const runRaw = (cwd, args, input) => execFileSync('git', args, { cwd, env: gitEnv(), input, stdio: ['pipe', 'pipe', 'ignore'], encoding: 'utf8' });

// The keys of the local config of a checkout that `git init` and `git fetch` make: nothing else is allowed. A key such as filter.*.clean, core.fsmonitor,
// core.sparseCheckout or core.worktree changes what git reports about the files, and a checkout that has one is not one that this run made.
const freshConfigKeys = /^(core\.(repositoryformatversion|filemode|bare|logallrefupdates|ignorecase|precomposeunicode|symlinks)|remote\.[^.]+\.(url|fetch)|branch\.[^.]+\.(remote|merge)|extensions\.(objectformat|compatobjectformat|refstorage))$/;

// What a checkout must be before a generator imports code from it: at the pinned commit, every tracked file the bytes that the commit has, no file of
// its own, and no node_modules but the root one, which the generators install or link themselves. The generators run what they import, and the goldens
// are stamped with the pinned commit, so a checkout that differs from it would write goldens that no commit explains.
//
// It does not trust the state of the checkout's .git: `git status` does not report a file marked assume-unchanged or skip-worktree, nor a file that a
// clean filter (config and attributes) makes look unchanged, nor a directory that .gitignore or .git/info/exclude names (a scripts/node_modules/yaml,
// which Node resolves before the root one), and `git replace` makes the pinned commit show another tree. So: replace objects are off; every tracked
// file is hashed raw (no filters) and compared with the blob that the commit's tree has; the index is compared with the tree, entry by entry (a file
// added with `git add -f` is an entry that the tree does not have); every untracked file is listed with no exclude pattern applied; and the local
// config may hold only the keys that a fresh clone has. This guards against a mistake and against a hidden edit of the working tree; it does not
// defend against someone who rewrites the object store, and that is why a run fetches its references fresh and keeps none (checkoutReference).
export function assertAsCommitted(dir, repository, commit) {
  if (run(dir, 'git', ['rev-parse', 'HEAD']) !== commit) throw new Error(`${dir} is not at ${repository}@${commit}`);
  const entries = (text, pattern) => text.split('\0').filter(Boolean).map((entry) => entry.match(pattern)?.groups ?? { malformed: entry });
  const tree = entries(runRaw(dir, ['ls-tree', '-r', '-z', 'HEAD']), /^(?<mode>\d+) (?<type>\w+) (?<blob>[0-9a-f]+)\t(?<path>[^]*)$/);
  const index = entries(runRaw(dir, ['ls-files', '-s', '-z']), /^(?<mode>\d+) (?<blob>[0-9a-f]+) (?<stage>\d)\t(?<path>[^]*)$/);
  if (tree.some((entry) => entry.malformed) || index.some((entry) => entry.malformed)) throw new Error(`${dir}: git's listing of the checkout is not what this check reads`);
  const key = (entry) => `${entry.mode} ${entry.blob} ${entry.path}`;
  const inTree = new Set(tree.map(key));
  const odd = [...index.filter((entry) => entry.stage !== '0' || !inTree.has(key(entry))).map((entry) => entry.path), ...tree.filter((entry) => !index.some((i) => key(i) === key(entry))).map((entry) => entry.path)];
  if (odd.length > 0) throw new Error(`${dir} has an index that is not the commit's tree (${odd.slice(0, 3).join(', ')}); the references are read as committed`);
  const files = tree.filter((entry) => entry.type === 'blob'); // a submodule is not a file
  if (files.some(({ path }) => path.includes('\n'))) throw new Error(`${dir} has a tracked file with a line break in its name`);
  let hashes;
  try {
    hashes = runRaw(dir, ['hash-object', '--no-filters', '--stdin-paths'], `${files.map(({ path }) => path).join('\n')}\n`).split('\n').filter(Boolean);
  } catch {
    throw new Error(`${dir} lacks a file that ${repository}@${commit} has; the references are read as committed`);
  }
  const changed = files.filter((entry, at) => hashes[at] !== entry.blob).map(({ path }) => path);
  if (changed.length > 0) throw new Error(`${dir} has local changes (${changed.slice(0, 3).join(', ')}); the references are read as committed`);
  const strays = runRaw(dir, ['ls-files', '--others', '--directory', '-z']).split('\0').filter((file) => file && file !== 'node_modules/');
  if (strays.length > 0) throw new Error(`${dir} has files that are not in ${repository}@${commit}: ${strays.slice(0, 3).join(', ')}; the references are read as committed`);
  const config = runRaw(dir, ['config', '--local', '--list', '-z']).split('\0').filter(Boolean).map((entry) => entry.split('\n')[0]);
  const foreign = config.filter((name) => !freshConfigKeys.test(name));
  if (foreign.length > 0) throw new Error(`${dir} has local git config that a fresh clone does not have (${foreign.slice(0, 3).join(', ')}); the references are read as committed`);
  // extensions.worktreeConfig is refused above (it is not a key of a fresh clone), and so is the file it makes git read: a config.worktree can
  // set core.worktree to another directory, and `git config --local --list` does not show it.
  const gitDir = runRaw(dir, ['rev-parse', '--absolute-git-dir']).trim();
  if (existsSync(join(gitDir, 'config.worktree'))) throw new Error(`${dir} has a config.worktree, which a fresh clone does not have; the references are read as committed`);
}

let privateRoot; // one directory for the run, made when the first reference is fetched
const privateDirectory = () => {
  if (!privateRoot) {
    // mkdtemp makes a new directory, readable and writable by this user only, so another user of a shared /tmp can neither plant nor edit what is imported.
    privateRoot = mkdtempSync(join(tmpdir(), 'ovdb-reference-'));
    process.on('exit', () => rmSync(privateRoot, { recursive: true, force: true }));
  }
  return privateRoot;
};

// checkoutReference returns a directory that holds the reference `name` as committed at its pinned commit.
//   explicit  a clone the user has (--directory, --chinookdb): checked by assertAsCommitted, not changed.
//   otherwise a fresh private directory, fetched for this run and removed when it ends. No checkout is kept between runs: a kept one is state
//             that the run did not make, and what assertAsCommitted can see of it is less than what could have been done to it (the object store),
//             so the way not to trust it is not to have it; the price is the fetch of two small repositories (seconds).
// The Directory's one dependency (yaml) is installed from its own lock file into a fetched checkout. `pin`, `remote` and `parent` (where the fresh
// directory is made) are for the test of this function, which fetches from a repository of its own.
export function checkoutReference(name, { explicit, parent, pin = references[name], remote = remoteUrl(name) } = {}) {
  const { repository, commit } = pin;
  if (explicit) {
    const dir = resolve(explicit);
    assertAsCommitted(dir, repository, commit);
    if (name === 'directory' && !existsSync(join(dir, 'node_modules', 'yaml'))) {
      throw new Error(`${dir} has no node_modules/yaml, which the Directory's directory.mjs imports: run \`npm ci --omit=dev --ignore-scripts\` there first`);
    }
    return dir;
  }
  const dir = join(parent ?? privateDirectory(), `${name}-${commit}`);
  if (existsSync(dir)) throw new Error(`${dir} exists: a reference is fetched fresh, never reused`);
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  run(dir, 'git', ['init', '--quiet']);
  run(dir, 'git', ['fetch', '--quiet', '--depth', '1', remote, commit]);
  run(dir, 'git', ['checkout', '--quiet', '--detach', 'FETCH_HEAD']);
  assertAsCommitted(dir, repository, commit);
  if (name === 'directory') run(dir, 'npm', ['ci', '--omit=dev', '--ignore-scripts', '--no-audit', '--no-fund']);
  return dir;
}

// A generator composes some verdicts from expressions that the reference keeps inline (not exported), copied into the generator. When the reference
// moves, those expressions may change; a generator that went on would write verdicts that are not the reference's. This names every anchor that the
// pinned file does not hold, one per line, says what to do, and stops with exit status 1, with no stack: the message is the whole report.
export function assertAnchors(source, file, commit, anchors, { err = console.error, exit = process.exit } = {}) {
  const missing = anchors.filter((anchor) => !source.includes(anchor));
  if (missing.length === 0) return;
  err(`${file} at ${commit} no longer holds ${missing.length} of the ${anchors.length} expressions that this generator copies; the verdicts composed from them would not be the reference's:`);
  for (const anchor of missing) err(`  missing anchor: ${anchor}`);
  err('Read the reference at that commit, update the copied expression and the verdict composed from it together, then run the generator again.');
  exit(1);
}
