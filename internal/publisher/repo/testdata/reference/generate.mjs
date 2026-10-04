#!/usr/bin/env node
// Regenerates repository.json and digests.json: whole repositories, built as real git repositories at the time of the run, and the
// verdict of the Chinook checker (scripts/lib/ovdb-manifest.mjs, the reference of the Publisher profile) on each, with and without
// its --repository option.
//
//   node internal/publisher/repo/testdata/reference/generate.mjs           # rewrite the goldens
//   node internal/publisher/repo/testdata/reference/generate.mjs --check   # fail if they are stale
//
// The Go tests of internal/publisher/repo read repository.json, rebuild each case in memory and fail if the Go check accepts a
// repository that the checker refuses (see README.md in the package). They never run this script: it needs Node 24 or later, git,
// npm and network access. To use clones you already have, pass --directory <dir> and --chinookdb <dir> (at the pinned commits;
// the Directory's with `npm ci --omit=dev` run).
//
// A case is a list of operations on a base repository, which is the real Chinook repository's own files (OVDB.md, ovdb.yaml, the
// model files and the meaning file, all consistent with each other) plus the example manifest of a hoster; the operations make
// each way the repository can be wrong about the presence of a file. They are applied by this script to build the repository with
// git's plumbing (so that a path git can hold but a working tree cannot, such as two names that differ only in case, can be
// built), and by the Go test to fill a repo.Memory; the operations are:
//   ["file", path, text]       a tracked regular file (what is above or below it in the tree is replaced)
//   ["remove", path]           no such path
//   ["move", from, to]
//   ["exec", path]             mode 100755
//   ["symlink", path, target]  ["submodule", path]
//   ["edit", path, find, replace]                     an edit of the text of a tracked file
//   ["pad", path, prefix, n]   n letters x, after prefix, on a line of their own at the end of a tracked file
//   ["worktree", path, text]   only in the working tree (committed nowhere)
//   ["ignored", path, text]    only in the working tree, and named by .gitignore
//   ["staged", path, text]     in the index and the working tree, not committed
//   ["dirty", path, text]      the working tree's copy of a tracked file differs from the commit's
//   ["many", dir, n]           n empty tracked files in dir
//   ["manifests", n]           OVDB.md lists n manifests, m/00.yaml and on, each a copy of ovdb.yaml
//   ["copy", from, to]         a copy of a tracked file
//   ["padjson", path, n]       a JSON file with one more key, whose value is n letters x (valid, and large)
//   ["break", path, how]       the object of a tracked file is gone ("missing") or damaged ("corrupt") in the repository that is checked
//   ["break-tree", dir, how]   the same for the tree object of a directory ("" is the top)
//   ["state", name]            the repository is bare, shallow, detached, unborn, not a repository, or is checked in a subdirectory;
//                              partial-blob and partial-tree are partial clones (--filter=blob:none, --filter=tree:0) whose source is
//                              still there to fetch from (the checker's git fetches what it lacks; this check does not);
//                              alternates-gone borrows its objects from a repository that is then deleted, and alternates-dangling has
//                              its own copy of the objects and an alternates file that points to the deleted repository
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { checkoutReference, references as pins } from '../../../references.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const goldenPath = join(here, 'repository.json');
const digestsPath = join(here, 'digests.json');
const argValue = (name) => { const at = process.argv.indexOf(name); return at === -1 ? undefined : process.argv[at + 1]; };
const sha = (text) => createHash('sha256').update(text).digest('hex');

// ---- the checker, at the pinned commit ----

const directoryRoot = checkoutReference('directory', { explicit: argValue('--directory') });
const chinookRoot = checkoutReference('chinookdb', { explicit: argValue('--chinookdb') });
// The checker imports `yaml`, which its own checkout does not have installed: a checkout that we fetched gets a link to the copy that the
// Directory's checkout installed from its lock file (see internal/publisher/manifest/testdata/reference/generate.mjs, which does the same).
if (argValue('--chinookdb')) {
  if (!existsSync(join(chinookRoot, 'node_modules', 'yaml'))) throw new Error(`${chinookRoot} has no node_modules/yaml, which ovdb-manifest.mjs imports`);
} else {
  rmSync(join(chinookRoot, 'node_modules'), { recursive: true, force: true });
  mkdirSync(join(chinookRoot, 'node_modules'), { recursive: true });
  symlinkSync(join(directoryRoot, 'node_modules', 'yaml'), join(chinookRoot, 'node_modules', 'yaml'), 'dir');
}
const chinook = await import(pathToFileURL(join(chinookRoot, 'scripts/lib/ovdb-manifest.mjs')).href);
const { isolatedGitEnv } = await import(pathToFileURL(join(chinookRoot, 'scripts/lib/git-env.mjs')).href);
process.emitWarning = () => {}; // the yaml package warns on stderr
const gitEnv = isolatedGitEnv();

// ---- the base repository ----

const own = 'https://github.com/datatug/chinookdb';
const read = (file) => readFileSync(join(chinookRoot, file), 'utf8');
const manifestPath = 'ovdb.yaml';
const modelPath = 'model/chinook.modelspec.json';
const hclPath = 'model/chinook.modelspec.hcl';
const meaningPath = 'model/chinook.meaning.yaml';
const base = {
  'OVDB.md': read('OVDB.md'), [manifestPath]: read('ovdb.yaml'), [modelPath]: read(modelPath), [hclPath]: read(hclPath), [meaningPath]: read(meaningPath),
  'hoster.yaml': read('examples/hoster/ovdb.yaml'),
};

// ---- applying the operations: the repository as git will hold it ----

const putEntry = (tracked, path, entry) => {
  const parts = path.split('/');
  for (let i = 1; i < parts.length; i++) tracked.delete(parts.slice(0, i).join('/')); // a file where a directory is wanted goes
  for (const key of [...tracked.keys()]) if (key.startsWith(`${path}/`)) tracked.delete(key); // so do the files below a path that is now a file
  tracked.set(path, entry);
};
const listing = (n) => Array.from({ length: n }, (_, i) => `./m/${String(i).padStart(2, '0')}.yaml`);
const apply = (ops) => {
  const state = { tracked: new Map(), worktree: new Map(), staged: new Map(), ignored: [], breaks: [], location: 'normal' };
  for (const [path, text] of Object.entries(base)) state.tracked.set(path, { mode: '100644', text });
  const text = (path) => { const entry = state.tracked.get(path); if (entry?.text === undefined) throw new Error(`no text at ${path}`); return entry.text; };
  for (const [op, a, b, c] of ops) {
    switch (op) {
      case 'file': putEntry(state.tracked, a, { mode: '100644', text: b }); break;
      case 'remove': for (const key of [...state.tracked.keys()]) if (key === a || key.startsWith(`${a}/`)) state.tracked.delete(key); break;
      case 'move': { const entry = state.tracked.get(a); state.tracked.delete(a); putEntry(state.tracked, b, entry); break; }
      case 'exec': state.tracked.get(a).mode = '100755'; break;
      case 'symlink': putEntry(state.tracked, a, { mode: '120000', text: b }); break;
      case 'submodule': putEntry(state.tracked, a, { mode: '160000', id: '1'.repeat(40) }); break;
      case 'pad': state.tracked.get(a).text += `\n${b}${'x'.repeat(c)}\n`; break;
      case 'edit': { const before = text(a); if (!before.includes(b)) throw new Error(`${a} has no ${JSON.stringify(b)}`); state.tracked.get(a).text = before.replace(b, c); break; }
      case 'worktree': state.worktree.set(a, b); break;
      case 'ignored': state.ignored.push(a); state.worktree.set(a, b); break;
      case 'staged': state.staged.set(a, b); state.worktree.set(a, b); break;
      case 'dirty': state.worktree.set(a, b); break;
      case 'many': for (let i = 0; i < b; i++) state.tracked.set(`${a ? `${a}/` : ''}f${String(i).padStart(5, '0')}`, { mode: '100644', text: '' }); break;
      case 'manifests': {
        const paths = listing(a);
        state.tracked.get('OVDB.md').text = `---\novdb: 1\npublish: [${paths.join(', ')}]\n---\n`;
        for (const path of paths) state.tracked.set(path.slice(2), { mode: '100644', text: text(manifestPath) });
        break;
      }
      case 'copy': state.tracked.set(b, { ...state.tracked.get(a) }); break;
      case 'padjson': { const json = JSON.parse(text(a)); json._pad = 'x'.repeat(b); state.tracked.get(a).text = JSON.stringify(json); break; }
      case 'break': case 'break-tree': state.breaks.push([op, a, b]); break;
      case 'state': state.location = a; break;
      default: throw new Error(`unknown operation ${op}`);
    }
  }
  if (state.ignored.length > 0) {
    state.tracked.set('.gitignore', { mode: '100644', text: `${state.ignored.join('\n')}\n` });
    state.worktree.set('.gitignore', `${state.ignored.join('\n')}\n`);
  }
  return state;
};

// ---- building the real repository ----

const git = (dir, args, input) => execFileSync('git', ['-C', dir, ...args], { env: gitEnv, input, stdio: [input === undefined ? 'ignore' : 'pipe', 'pipe', 'pipe'] }).toString().trim();
const identity = ['-c', 'user.name=t', '-c', 'user.email=t@example.test', '-c', 'commit.gpgsign=false'];
const writeWorktree = (root, path, text) => { mkdirSync(dirname(join(root, path)), { recursive: true }); writeFileSync(join(root, path), text); };
// The tree objects are written by hand (git hash-object -t tree --literally), not through the index, which would normalise a mode such as
// 100664 to 100644 and cannot hold a path that a working tree cannot. Entries sort as git sorts them: by name, a directory as if its
// name ended in a slash.
const writeTree = (root, files, blob) => {
  const children = new Map();
  for (const [parts, entry] of files) {
    const [head, ...rest] = parts;
    if (rest.length === 0) children.set(head, { entry });
    else (children.get(head) ?? children.set(head, { below: [] }).get(head)).below.push([rest, entry]);
  }
  const records = [...children].map(([name, child]) => {
    const [mode, id] = child.below ? ['40000', writeTree(root, child.below, blob)] : [child.entry.mode.replace(/^0+/, ''), child.entry.id ?? blob(child.entry.text)];
    return { key: Buffer.from(child.below ? `${name}/` : name), bytes: Buffer.concat([Buffer.from(`${mode} ${name}\0`), Buffer.from(id, 'hex')]) };
  });
  records.sort((a, b) => Buffer.compare(a.key, b.key));
  return git(root, ['hash-object', '-t', 'tree', '-w', '--literally', '--stdin'], Buffer.concat(records.map((r) => r.bytes)));
};
// Makes an object of the repository in dir unreadable: loose objects are what can be deleted or damaged one at a time, so packs are
// unpacked first.
const breakObject = (dir, [op, target, how]) => {
  const gitDir = git(dir, ['rev-parse', '--absolute-git-dir']);
  const packs = join(gitDir, 'objects', 'pack');
  for (const name of existsSync(packs) ? readdirSync(packs).filter((n) => n.endsWith('.pack')) : []) {
    const pack = readFileSync(join(packs, name));
    for (const sibling of readdirSync(packs).filter((n) => n.startsWith(name.slice(0, -5)))) rmSync(join(packs, sibling));
    git(dir, ['unpack-objects', '-q'], pack);
  }
  const id = git(dir, ['rev-parse', op === 'break' ? `HEAD:${target}` : target === '' ? 'HEAD^{tree}' : `HEAD:${target}`]);
  const file = join(gitDir, 'objects', id.slice(0, 2), id.slice(2));
  if (how === 'missing') rmSync(file);
  else { chmodSync(file, 0o644); writeFileSync(file, 'garbage\n'); }
};
// Returns the directory to run the checker in, and a function that removes everything.
const build = (state) => {
  const parent = mkdtempSync(join(tmpdir(), 'ovdb-repository-case-'));
  const cleanup = () => rmSync(parent, { recursive: true, force: true });
  try {
    if (state.location === 'not-a-repository') return [parent, cleanup];
    const root = join(parent, 'repo');
    mkdirSync(root);
    git(root, ['init', '--quiet', '-b', 'main']);
    if (state.location === 'unborn') return [root, cleanup];
    const blobs = new Map();
    const blob = (text) => { if (!blobs.has(text)) blobs.set(text, git(root, ['hash-object', '-w', '--stdin'], text)); return blobs.get(text); };
    const prefix = state.location === 'subdirectory' ? 'sub/' : '';
    const tree = writeTree(root, [...state.tracked].map(([path, entry]) => [`${prefix}${path}`.split('/'), entry]), blob);
    // Two commits, so that a shallow clone is shallow.
    const first = git(root, [...identity, 'commit-tree', '-m', 'first', writeTree(root, [], blob)]);
    const commit = git(root, [...identity, 'commit-tree', '-m', 'case', '-p', first, tree]);
    git(root, ['update-ref', 'refs/heads/main', commit]);
    git(root, ['read-tree', commit]);
    let dir = root;
    if (state.location === 'bare') { dir = join(parent, 'bare'); git(parent, ['clone', '--quiet', '--bare', root, dir]); }
    if (state.location === 'shallow') { dir = join(parent, 'shallow'); git(parent, ['clone', '--quiet', '--depth', '1', `file://${root}`, dir]); }
    if (state.location.startsWith('partial-')) {
      git(root, ['config', 'uploadpack.allowFilter', 'true']);
      git(root, ['config', 'uploadpack.allowAnySHA1InWant', 'true']); // the lazy fetch asks for an object by its id
      dir = join(parent, 'partial');
      git(parent, ['clone', '--quiet', `--filter=${state.location === 'partial-blob' ? 'blob:none' : 'tree:0'}`, '--no-checkout', `file://${root}`, dir]);
    }
    if (state.location.startsWith('alternates-')) {
      dir = join(parent, 'borrower');
      git(parent, ['clone', '--quiet', '--shared', '--no-checkout', root, dir]);
      if (state.location === 'alternates-dangling') git(dir, ['repack', '-a', '-d', '--quiet']); // takes the borrowed objects in: -l is not given
      rmSync(root, { recursive: true, force: true });
    }
    if (state.location === 'detached') git(root, ['update-ref', '--no-deref', 'HEAD', commit]);
    if (state.location === 'subdirectory') { dir = join(root, 'sub'); mkdirSync(dir); }
    for (const spec of state.breaks) breakObject(dir, spec);
    for (const [path, text] of state.staged) git(root, ['update-index', '--add', '--cacheinfo', `100644,${blob(text)},${path}`]);
    for (const [path, text] of state.worktree) writeWorktree(root, `${prefix}${path}`, text);
    return [dir, cleanup];
  } catch (error) { cleanup(); throw error; }
};

// ---- the verdicts ----

let thrown = 0;
const explain = argValue('--explain'); // print the problems the checker finds in the cases whose name has this text
let current = '';
const verdict = (dir, repository) => {
  try {
    const { problems } = chinook.reportOvdbManifest(chinook.gitRepoFiles(dir), repository === null ? {} : { repository });
    if (explain !== undefined && current.includes(explain)) console.error(`${current} (${repository === null ? 'plain' : 'with'}): ${problems.join(' | ') || 'nothing'}`);
    return problems.length === 0 ? '1' : '0';
  } catch (error) { thrown += 1; return '0'; }
};
const cases = [];
const addCase = (group, name, ops, repository = own) => {
  current = `${group}: ${name}`;
  const [dir, cleanup] = build(apply(ops));
  try { cases.push({ group, name, ops, repository, plain: verdict(dir, null), with: repository === null ? '-' : verdict(dir, repository) }); } finally { cleanup(); }
};

const swapCase = (path) => { const parts = path.split('/'); const last = parts.pop(); parts.push(last === last.toLowerCase() ? last.toUpperCase() : last.toLowerCase()); return parts.join('/'); };
const places = (path) => ({
  missing: [['remove', path]],
  directory: [['move', path, `${path}/inner`]],
  symlink: [['move', path, 'elsewhere/real'], ['symlink', path, 'elsewhere/real']],
  submodule: [['submodule', path]],
  'worktree-only': [['remove', path], ['worktree', path, base[path]]],
  ignored: [['remove', path], ['ignored', path, base[path]]],
  staged: [['remove', path], ['staged', path, base[path]]],
  executable: [['exec', path]],
  'different-case': [['move', path, swapCase(path)]],
  'case-collision': [['file', swapCase(path), base[path]]],
  ...(path.includes('/') ? { 'parent-is-file': [['file', dirname(path), 'not a directory']] } : {}),
});
addCase('base', 'unchanged', []);
for (const [label, path] of [['OVDB.md', 'OVDB.md'], ['manifest', manifestPath], ['model-file', modelPath], ['hcl-file', hclPath], ['meaning-file', meaningPath]]) {
  for (const [variant, ops] of Object.entries(places(path))) addCase(`place:${label}`, variant, ops);
}

const hosted = [['file', 'OVDB.md', '---\novdb: 1\npublish: [./ovdb.yaml, ./hoster.yaml]\n---\n']];
const edit = (path, find, replace) => ['edit', path, find, replace];
const md = (frontMatter) => ['file', 'OVDB.md', `---\n${frontMatter}\n---\n`];
addCase('document', 'manifest: not a mapping', [['file', manifestPath, '- a\n- b\n']]);
addCase('document', 'manifest: not YAML', [['file', manifestPath, 'a: [\n']]);
addCase('document', 'manifest: empty', [['file', manifestPath, '']]);
addCase('document', 'manifest: unknown key', [['edit', manifestPath, 'format:', 'extra: 1\nformat:']]);
addCase('document', 'manifest: http url', [edit(manifestPath, 'url: https://chinookdb.com/ovdb', 'url: http://chinookdb.com/ovdb')]);
addCase('document', 'manifest: upper-case id', [edit(manifestPath, 'id: chinook\n', 'id: Chinook\n')]);
addCase('document', 'manifest: no publisher.repository', [edit(manifestPath, '  repository: https://github.com/datatug/chinookdb\n', '')]);
addCase('document', 'manifest: over the size bound', [['pad', manifestPath, '# ', 300000]]);
addCase('document', 'OVDB.md: no front matter', [['file', 'OVDB.md', '# nothing\n']]);
addCase('document', 'OVDB.md: ovdb 2', [md('ovdb: 2\npublish: [./ovdb.yaml]')]);
addCase('document', 'OVDB.md: empty publish', [md('ovdb: 1\npublish: []')]);
addCase('document', 'OVDB.md: unknown key', [md('ovdb: 1\npublish: [./ovdb.yaml]\nextra: 1')]);
addCase('document', 'OVDB.md: lists a missing manifest', [md('ovdb: 1\npublish: [./nope.yaml]')]);
addCase('document', 'OVDB.md: lists a directory', [md('ovdb: 1\npublish: [./model]')]);
addCase('document', 'OVDB.md: lists a manifest twice', [md('ovdb: 1\npublish: [./ovdb.yaml, ./ovdb.yaml]')]);
addCase('document', 'OVDB.md: lists a path that goes up', [md('ovdb: 1\npublish: [./ovdb.yaml, ./../x.yaml]')]);
addCase('document', 'OVDB.md: over the size bound', [['pad', 'OVDB.md', '', 300000]]);

addCase('repository', 'the repository of the manifest', [], own);
addCase('repository', 'another repository', [], 'https://github.com/other/chinookdb');
addCase('repository', 'the same in other case', [], 'https://github.com/DataTug/chinookdb');
addCase('repository', 'with a trailing slash', [], `${own}/`);
addCase('repository', 'empty', [], '');
addCase('repository', 'publisher.repository is not a repository', [edit(manifestPath, '  repository: https://github.com/datatug/chinookdb\n', '  repository: https://github.com/datatug/\n')], 'https://github.com/other/other');
addCase('repository', 'not given', [], null);

addCase('listed', 'two manifests', hosted, null);
addCase('listed', 'the second is missing', [...hosted, ['remove', 'hoster.yaml']], null);
addCase('listed', 'the second is a directory', [...hosted, ['move', 'hoster.yaml', 'hoster.yaml/inner']], null);
addCase('listed', 'the second is a symlink', [...hosted, ['move', 'hoster.yaml', 'elsewhere/real'], ['symlink', 'hoster.yaml', 'elsewhere/real']], null);
addCase('listed', 'the second is invalid', [...hosted, ['edit', 'hoster.yaml', 'engine: postgres', 'engine: ']], null);
addCase('listed', 'both are invalid', [...hosted, ['file', 'hoster.yaml', '- a\n'], ['file', manifestPath, '- a\n']], null);
addCase('listed', 'the repository, two manifests', hosted, own);
addCase('listed', '32 manifests', [['manifests', 32]]);
addCase('listed', '33 manifests', [['manifests', 33]]);

addCase('tree', 'a name with a backslash', [['file', 'we\\ird', 'x']]);
addCase('tree', 'a name with a line break', [['file', 'line\nbreak', 'x']]);
addCase('tree', 'a name with a tab and a space', [['file', 'tab\tand space', 'x']]);
addCase('tree', 'two names in the top directory that differ in case', [['file', 'readme', 'x'], ['file', 'README', 'y']]);
addCase('tree', 'two names in a directory of a named file that differ in case', [['file', 'model/Extra.txt', 'x'], ['file', 'model/extra.txt', 'y']]);
addCase('tree', 'two names in another directory that differ in case', [['file', 'docs/A', 'x'], ['file', 'docs/a', 'y']]);
addCase('tree', 'a backslash in another directory', [['file', 'docs/a\\b', 'x']]);
addCase('tree', 'the top directory has 50000 entries', [['many', '', 49996]]); // with OVDB.md, ovdb.yaml, hoster.yaml and model
addCase('tree', 'the top directory has 50001 entries', [['many', '', 49997]]);
addCase('tree', '50001 files in another directory', [['many', 'big', 50001]]);

addCase('worktree', 'only the working tree has a valid manifest', [edit(manifestPath, 'engine: sqlite', 'engine: '), ['dirty', manifestPath, base[manifestPath]]]);
addCase('worktree', 'only the working tree has a broken manifest', [['dirty', manifestPath, 'broken: [']]);
addCase('worktree', 'only the index has OVDB.md', [['remove', 'OVDB.md'], ['staged', 'OVDB.md', base['OVDB.md']]]);

for (const state of ['bare', 'shallow', 'detached', 'unborn', 'not-a-repository', 'subdirectory', 'partial-blob', 'partial-tree', 'alternates-gone', 'alternates-dangling']) addCase('state', state, [['state', state]]);

// A file that a manifest names that cannot be read, or is larger than this check reads (4 MiB; the checker reads 16).
for (const how of ['missing', 'corrupt']) {
  addCase('object', `OVDB.md: ${how}`, [['break', 'OVDB.md', how]]);
  addCase('object', `the manifest: ${how}`, [['break', manifestPath, how]]);
  addCase('object', `the model file: ${how}`, [['break', modelPath, how]]);
  addCase('object', `the meaning file: ${how}`, [['break', meaningPath, how]]);
  addCase('object', `the hcl file, which the checker does not read: ${how}`, [['break', hclPath, how]]);
  addCase('object', `the tree of the directory of the model files: ${how}`, [['break-tree', 'model', how]]);
  addCase('object', `the top tree: ${how}`, [['break-tree', '', how]]);
  addCase('object', `the model file of a shallow clone: ${how}`, [['state', 'shallow'], ['break', modelPath, how]]);
}
addCase('object', 'a model file larger than this check reads', [['padjson', modelPath, 4194304]]);
addCase('object', 'a model file of 3 MiB', [['padjson', modelPath, 3145728]]);
// The named files of a manifest after the first.
const second = [['copy', manifestPath, 'two.yaml'], ['file', 'OVDB.md', '---\novdb: 1\npublish: [./ovdb.yaml, ./two.yaml]\n---\n']];
addCase('listed', 'a second own-form manifest names a file that is not there', [...second, ['edit', 'two.yaml', 'model/chinook.modelspec.json', 'model/other.modelspec.json']]);
addCase('listed', 'a second own-form manifest names a file that is a directory', [...second, ['edit', 'two.yaml', 'model/chinook.meaning.yaml', 'model/elsewhere.meaning.yaml'], ['file', 'model/elsewhere.meaning.yaml/x', 'x']]);
addCase('listed', 'a second own-form manifest, all there', second);

// ---- the golden ----

const counts = { cases: cases.length, accepted: cases.filter((c) => c.plain === '1').length, acceptedWithRepository: cases.filter((c) => c.with === '1').length };
const golden = `${JSON.stringify({
  format: 'ovdb-publisher-repository/1',
  reference: `${pins.chinookdb.repository}@${pins.chinookdb.commit}`,
  repository: own,
  base,
  counts,
  cases: cases.map((c) => ({ group: c.group, name: c.name, ops: c.ops, repository: c.repository, verdict: `${c.plain}${c.with}` })),
}, null, 1)}\n`;
const digests = `${JSON.stringify({ 'repo/testdata/reference/repository.json': sha(golden) }, null, 1)}\n`;
const targets = [[goldenPath, golden], [digestsPath, digests]];
if (thrown > 0) console.error(`note: the checker threw on ${thrown} case(s); they are recorded as refused`);
if (process.argv.includes('--check')) {
  if (targets.some(([path, text]) => !existsSync(path) || readFileSync(path, 'utf8') !== text)) { console.error(`the goldens in ${here} are stale: run node ${process.argv[1]}`); process.exit(1); }
  console.log(`the goldens are up to date (${counts.cases} repositories)`);
} else {
  for (const [path, text] of targets) writeFileSync(path, text);
  console.log(`wrote ${counts.cases} repositories (accepted ${counts.accepted}, accepted with --repository ${counts.acceptedWithRepository}): ${(golden.length / 1024).toFixed(0)} KiB`);
}
