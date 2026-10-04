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

// What a checkout must be before a generator imports code from it: at the pinned commit, with no change to a tracked file and no
// file of its own that is not a dependency. The generators run what they import, and the goldens are stamped with the pinned commit,
// so a checkout that differs from it would write goldens that no commit explains.
export function assertAsCommitted(dir, repository, commit) {
  if (run(dir, 'git', ['rev-parse', 'HEAD']) !== commit) throw new Error(`${dir} is not at ${repository}@${commit}`);
  if (run(dir, 'git', ['status', '--porcelain', '--untracked-files=no'])) throw new Error(`${dir} has local changes; the references are read as committed`);
  const strays = run(dir, 'git', ['ls-files', '--others', '--exclude-standard']).split('\n').filter((file) => file && !file.startsWith('node_modules/'));
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
  if (name === 'directory') {
    rmSync(join(dir, 'node_modules'), { recursive: true, force: true });
    run(dir, 'npm', ['ci', '--omit=dev', '--ignore-scripts', '--no-audit', '--no-fund']);
  }
  return dir;
}
