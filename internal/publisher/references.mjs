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
  directory: { repository: 'openvaultdb/directory', commit: 'e8db5488db31d3f63865e404acef487c33cf35df' },
  chinookdb: { repository: 'datatug/chinookdb', commit: '79e7bb0b1d6f0666dce465874990dec64348331f' },
};

// The URL a reference is fetched from.
export const remoteUrl = (name) => `https://github.com/${references[name].repository}.git`;

const run = (cwd, command, args) => execFileSync(command, args, { cwd, stdio: ['ignore', 'pipe', 'inherit'], encoding: 'utf8' }).trim();

// What a checkout must be before a generator imports code from it: at the pinned commit, every tracked file as that commit has it, no file of its
// own, and no node_modules but the root one, which the generators install or link themselves. The generators run what they import, and the goldens
// are stamped with the pinned commit, so a checkout that differs from it would write goldens that no commit explains.
//
// `git status` is not enough: a file marked assume-unchanged (or skip-worktree) is not reported, and a directory that .gitignore names (a
// scripts/node_modules/yaml, which Node resolves before the root one) is not an untracked file. So each tracked file is hashed and compared with
// the blob that the commit has (git ls-tree), and ignored files and directories are listed too.
export function assertAsCommitted(dir, repository, commit) {
  if (run(dir, 'git', ['rev-parse', 'HEAD']) !== commit) throw new Error(`${dir} is not at ${repository}@${commit}`);
  const tracked = run(dir, 'git', ['ls-tree', '-r', '-z', 'HEAD']).split('\0').filter(Boolean).map((entry) => {
    const [meta, path] = [entry.slice(0, entry.indexOf('\t')), entry.slice(entry.indexOf('\t') + 1)];
    const [mode, type, blob] = meta.split(' ');
    return { mode, type, blob, path };
  }).filter((entry) => entry.type === 'blob'); // a submodule is not a file
  if (tracked.some(({ path }) => path.includes('\n'))) throw new Error(`${dir} has a tracked file with a line break in its name`);
  let hashes;
  try {
    hashes = execFileSync('git', ['hash-object', '--stdin-paths'], { cwd: dir, input: `${tracked.map(({ path }) => path).join('\n')}\n`, stdio: ['pipe', 'pipe', 'ignore'], encoding: 'utf8' }).split('\n').filter(Boolean);
  } catch {
    throw new Error(`${dir} lacks a file that ${repository}@${commit} has; the references are read as committed`);
  }
  const changed = tracked.filter((entry, at) => hashes[at] !== entry.blob).map(({ path }) => path);
  if (changed.length > 0) throw new Error(`${dir} has local changes (${changed.slice(0, 3).join(', ')}); the references are read as committed`);
  const others = (...flags) => run(dir, 'git', ['ls-files', '--others', '--exclude-standard', '--directory', ...flags]).split('\n').filter((file) => file && file !== 'node_modules/');
  const strays = [...others(), ...others('--ignored')];
  if (strays.length > 0) throw new Error(`${dir} has files that are not in ${repository}@${commit}: ${strays.slice(0, 3).join(', ')}; the references are read as committed`);
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
//   explicit  a clone the user has (--directory, --chinookdb): checked, not changed.
//   cacheRoot a cache the user keeps (OVDB_REFERENCE_CACHE): reused only if it is as committed (it is refused otherwise: remove it), and its
//             dependencies are installed again, since they are not part of the commit.
//   neither   a fresh private directory, fetched for this run and removed when it ends.
// The Directory's one dependency (yaml) is installed from its own lock file, in the last two cases. `pin` and `remote` are for the test of this
// function, which fetches from a repository of its own.
export function checkoutReference(name, { explicit, cacheRoot = process.env.OVDB_REFERENCE_CACHE, pin = references[name], remote = remoteUrl(name) } = {}) {
  const { repository, commit } = pin;
  if (explicit) {
    const dir = resolve(explicit);
    assertAsCommitted(dir, repository, commit);
    if (name === 'directory' && !existsSync(join(dir, 'node_modules', 'yaml'))) {
      throw new Error(`${dir} has no node_modules/yaml, which the Directory's directory.mjs imports: run \`npm ci --omit=dev --ignore-scripts\` there first`);
    }
    return dir;
  }
  const dir = join(cacheRoot ? resolve(cacheRoot) : privateDirectory(), `${name}-${commit}`);
  if (!existsSync(join(dir, '.git'))) {
    mkdirSync(dir, { recursive: true, mode: 0o700 });
    run(dir, 'git', ['init', '--quiet']);
    run(dir, 'git', ['fetch', '--quiet', '--depth', '1', remote, commit]);
    run(dir, 'git', ['checkout', '--quiet', '--detach', 'FETCH_HEAD']);
  }
  try {
    assertAsCommitted(dir, repository, commit);
  } catch (error) {
    throw new Error(`${error.message} (remove ${dir} to fetch it again)`);
  }
  // The root node_modules is not part of the commit: whatever a kept cache had is removed, and what the generators need is installed (the Directory's
  // yaml, from its lock file) or linked (by the generator of package manifest, for the Chinook checkout) again.
  rmSync(join(dir, 'node_modules'), { recursive: true, force: true });
  if (name === 'directory') run(dir, 'npm', ['ci', '--omit=dev', '--ignore-scripts', '--no-audit', '--no-fund']);
  return dir;
}
