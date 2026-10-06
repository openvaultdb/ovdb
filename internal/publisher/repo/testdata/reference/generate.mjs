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
//   ["break", path, how]       the object of a tracked file is gone ("missing") or damaged ("corrupt", "truncate", "empty") in the repository that
//                              is checked, or holds the bytes of another object ("other")
//   ["break-tree", dir, how]   the same for the tree object of a directory ("" is the top)
//   ["bytes", path, base64]    a tracked regular file of exactly these bytes
//   ["entities", path, n]      a model file whose entities are e0, e1 ... in base 36, n of them
//   ["recordsets", path, n]    a manifest whose recordsets are e0, e1 ... in base 36, n of them
//   ["nest", path, n]          a JSON file with one more key, _deep, whose value is n arrays inside one another
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
import { createRequire } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { assertGeneratorNode, checkoutReference, references as pins } from '../../../references.mjs';

assertGeneratorNode();
import { representationStages } from './representation-stages.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const goldenPath = join(here, 'repository.json');
const digestsPath = join(here, 'digests.json');
const argValue = (name) => { const at = process.argv.indexOf(name); return at === -1 ? undefined : process.argv[at + 1]; };
const sha = (text) => createHash('sha256').update(text).digest('hex');

// ---- the checker, at the pinned commit ----

const directoryRoot = checkoutReference('directory', { explicit: argValue('--directory') });
const stageDigest = await representationStages(directoryRoot, process.argv.includes('--check'));
const chinookRoot = checkoutReference('chinookdb', { explicit: argValue('--chinookdb') });
const fixtureRoot = checkoutReference('fixtures', { explicit: argValue('--fixtures') });
// The checker imports `yaml`, which its own checkout does not have installed: a checkout that we fetched gets a link to the copy that the
// Directory's checkout installed from its lock file (see internal/publisher/manifest/testdata/reference/generate.mjs, which does the same).
if (argValue('--chinookdb')) {
  if (!existsSync(join(chinookRoot, 'node_modules', 'yaml')) || !existsSync(join(chinookRoot, 'node_modules', 'ajv'))) throw new Error(`${chinookRoot} has no node_modules/yaml or node_modules/ajv, which ovdb-manifest.mjs imports`);
} else {
  rmSync(join(chinookRoot, 'node_modules'), { recursive: true, force: true });
  symlinkSync(join(directoryRoot, 'node_modules'), join(chinookRoot, 'node_modules'), 'dir');
}
const chinook = await import(pathToFileURL(join(chinookRoot, 'scripts/lib/ovdb-manifest.mjs')).href);
const { parse: parseYaml } = createRequire(join(directoryRoot, 'package.json'))('yaml');
const { isolatedGitEnv } = await import(pathToFileURL(join(chinookRoot, 'scripts/lib/git-env.mjs')).href);
process.emitWarning = () => {}; // the yaml package warns on stderr
const gitEnv = isolatedGitEnv();

// ---- the base repository ----

const own = 'https://github.com/datatug/chinookdb';
const read = (file) => readFileSync(join(fixtureRoot, file), 'utf8');
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
      case 'bytes': putEntry(state.tracked, a, { mode: '100644', text: Buffer.from(b, 'base64') }); break;
      case 'nest': state.tracked.get(a).text = text(a).replace(/\}\s*$/, `,"_deep":${'['.repeat(b)}${']'.repeat(b)}}\n`); break;
      case 'entities': { const json = JSON.parse(text(a)); json.entities = Object.fromEntries(Array.from({ length: b }, (_, i) => [`e${i.toString(36)}`, { properties: { id: { type: 'int' } } }])); state.tracked.get(a).text = JSON.stringify(json); break; }
      case 'recordsets': state.tracked.get(a).text = text(a).replace(/recordsets:\n(  - .*\n)+/, `recordsets:\n${Array.from({ length: b }, (_, i) => `  - e${i.toString(36)}\n`).join('')}`); break;
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
  if (how === 'missing') { rmSync(file); return; }
  chmodSync(file, 0o644);
  if (how === 'other') {
    const other = git(dir, ['hash-object', '-w', '--stdin'], 'other\n');
    writeFileSync(file, readFileSync(join(gitDir, 'objects', other.slice(0, 2), other.slice(2))));
  } else if (how === 'truncate') writeFileSync(file, readFileSync(file).subarray(0, 12));
  else writeFileSync(file, how === 'empty' ? '' : 'garbage\n');
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
    const blob = (text) => { const key = Buffer.isBuffer(text) ? `bytes:${text.toString('hex')}` : text; if (!blobs.has(key)) blobs.set(key, git(root, ['hash-object', '-w', '--stdin'], text)); return blobs.get(key); };
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
    seen.push(...problems);
    return problems.length === 0 ? '1' : '0';
  } catch (error) { thrown += 1; seen.push(`threw: ${error.message}`); return '0'; }
};
let seen = []; // the problems of the run in progress
const observed = []; // what the checker said about each case, for the check of the reasons below
const cases = [];
const addCase = (group, name, ops, repository = own) => {
  current = `${group}: ${name}`;
  const state = apply(ops);
  const texts = Object.fromEntries([modelPath, meaningPath].map((p) => [p, typeof state.tracked.get(p)?.text === 'string' && state.tracked.get(p).text.length < 100000 ? state.tracked.get(p).text : '']));
  const [dir, cleanup] = build(state);
  try {
    seen = [];
    const plain = verdict(dir, null);
    const plainProblems = seen;
    seen = [];
    const withRepository = repository === null ? '-' : verdict(dir, repository);
    observed.push({ texts, label: current, plain, with: withRepository, plainProblems, withProblems: seen });
    cases.push({ group, name, ops, repository, plain, with: withRepository });
  } finally { cleanup(); }
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
for (const how of ['missing', 'corrupt', 'truncate', 'empty']) {
  addCase('object', `OVDB.md: ${how}`, [['break', 'OVDB.md', how]]);
  addCase('object', `the manifest: ${how}`, [['break', manifestPath, how]]);
  addCase('object', `the model file: ${how}`, [['break', modelPath, how]]);
  addCase('object', `the meaning file: ${how}`, [['break', meaningPath, how]]);
  addCase('object', `the hcl file, which the checker does not read: ${how}`, [['break', hclPath, how]]);
  addCase('object', `the tree of the directory of the model files: ${how}`, [['break-tree', 'model', how]]);
  addCase('object', `the top tree: ${how}`, [['break-tree', '', how]]);
  addCase('object', `the model file of a shallow clone: ${how}`, [['state', 'shallow'], ['break', modelPath, how]]);
}
addCase('object', 'the model file holds the bytes of another object', [['break', modelPath, 'other']]);
addCase('object', 'the meaning file holds the bytes of another object', [['break', meaningPath, 'other']]);
addCase('object', 'a model file larger than this check reads', [['padjson', modelPath, 4194304]]);
addCase('object', 'a model file of 3 MiB', [['padjson', modelPath, 3145728]]);
// The named files of a manifest after the first.
const second = [['copy', manifestPath, 'two.yaml'], ['file', 'OVDB.md', '---\novdb: 1\npublish: [./ovdb.yaml, ./two.yaml]\n---\n']];
addCase('listed', 'a second own-form manifest names a file that is not there', [...second, ['edit', 'two.yaml', 'model/chinook.modelspec.json', 'model/other.modelspec.json']]);
addCase('listed', 'a second own-form manifest names a file that is a directory', [...second, ['edit', 'two.yaml', 'model/chinook.meaning.yaml', 'model/elsewhere.meaning.yaml'], ['file', 'model/elsewhere.meaning.yaml/x', 'x']]);
addCase('listed', 'a second own-form manifest, all there', second);


// ---- the content of the model file and of the meaning file (checker lines 408-468 and 535-539) ----

const modelJson = JSON.parse(base[modelPath]);
const modelText = (change) => { const copy = structuredClone(modelJson); change(copy); return JSON.stringify(copy, null, 2); };
const asModel = (text) => [['file', modelPath, text]];
const asMeaning = (text) => [['file', meaningPath, text]];
const rawModel = (injection) => asModel(base[modelPath].replace('{', `{${injection}`));
const unrelated = (key, value) => asModel(base[modelPath].replace(/\}\s*$/, `,"${key}":${value}}\n`));
const inMeaning = (extra) => [['edit', meaningPath, 'license: CC0-1.0\n', `license: CC0-1.0\n${extra}\n`]];
const inManifest = (find, replace) => [['edit', manifestPath, find, replace]];
// A file is bytes from the text to the disk: the text is UTF-8, and the one byte that is not (0xFF) is written where the text has U+E000. No text is ever
// turned into bytes by a single-byte encoding: that mangled the Cyrillic of the real meaning file (review of #39).
const b64 = (text) => Buffer.concat(text.split('\ue000').flatMap((part, at) => (at === 0 ? [Buffer.from(part, 'utf8')] : [Buffer.from([0xff]), Buffer.from(part, 'utf8')]))).toString('base64');

// The model file as a ModelSpec file: what is JSON and what is a module and entities.
for (const [name, text] of Object.entries({
  'empty': '', 'only white space': ' \n\t\r\n', 'not JSON': 'module', 'a JSON array': '[]', 'null': 'null', 'a number': '0', 'a string': '"x"', 'an empty object': '{}',
  'no module': modelText((m) => { delete m.module; }), 'module null': modelText((m) => { m.module = null; }), 'module a string': modelText((m) => { m.module = 'chinook'; }),
  'module an array': modelText((m) => { m.module = [{ name: 'chinook' }]; }), 'module.name empty': modelText((m) => { m.module.name = ''; }),
  'module.name starts with a digit': modelText((m) => { m.module.name = '1chinook'; }), 'module.name starts with an underscore': modelText((m) => { m.module.name = '_chinook'; }),
  'module.name a number': modelText((m) => { m.module.name = 5; }), 'module.name with a space': modelText((m) => { m.module.name = 'chin ook'; }),
  'module.name with a line break at its end': modelText((m) => { m.module.name = 'chinook\n'; }), 'module.name in full-width letters': modelText((m) => { m.module.name = 'ｃhinook'; }),
  'module.name missing': modelText((m) => { delete m.module.name; }), 'no entities': modelText((m) => { delete m.entities; }), 'entities an array': modelText((m) => { m.entities = []; }),
  'entities null': modelText((m) => { m.entities = null; }), 'entities empty': modelText((m) => { m.entities = {}; }), 'another module': modelText((m) => { m.module.name = 'Hostile'; }),
  'one entity fewer': modelText((m) => { delete m.entities.Track; }), 'one entity more': modelText((m) => { m.entities.Extra = {}; }),
  // The Directory's parseModelSpec (modelspec.mjs 92-118) refuses each of these; the checker reads only the module and the names of the entities.
  'no modelspec version': modelText((m) => { delete m.modelspec; }), 'an entity without properties': modelText((m) => { m.entities.Genre.properties = {}; }),
  'a property with neither type nor entity': modelText((m) => { m.entities.Genre.properties.Name = {}; }),
  'an entity called __proto__ that recordsets lack': modelText((m) => { Object.defineProperty(m.entities, '__proto__', { value: { properties: { id: { type: 'int' } } }, enumerable: true, configurable: true, writable: true }); }),
})) addCase('model', name, asModel(text));
addCase('model', 'an entity called __proto__ that recordsets list', [...asModel(modelText((m) => { Object.defineProperty(m.entities, '__proto__', { value: { properties: { id: { type: 'int' } } }, enumerable: true, configurable: true, writable: true }); })), ...inManifest('  - Track\n', '  - Track\n  - __proto__\n')]);
addCase('model', 'a key called __proto__ at the top', rawModel('"__proto__":{"module":{"name":"Hostile"}},'));
addCase('model', 'model.name is the module', inManifest('  address: modelspec', '  name: chinook\n  address: modelspec'));
addCase('model', 'model.name is not the module', inManifest('  address: modelspec', '  name: Other\n  address: modelspec'));
addCase('model', 'model.name is not a module name, and the file is fine', inManifest('  address: modelspec', '  name: 5x\n  address: modelspec'));
addCase('model', 'the module of model.address is not the file\'s', inManifest('datatug/chinookdb/chinook\n', 'datatug/chinookdb/Other\n'));
addCase('model', 'the module of model.address in another case', inManifest('datatug/chinookdb/chinook\n', 'datatug/chinookdb/Chinook\n'));
addCase('model', 'model.name and model.address in another case than the file', [...inManifest('  address: modelspec', '  name: Chinook\n  address: modelspec'), ...inManifest('datatug/chinookdb/chinook\n', 'datatug/chinookdb/Chinook\n')]);
addCase('model', 'model.name and model.address both differ from the file', [...inManifest('  address: modelspec', '  name: Other\n  address: modelspec'), ...inManifest('datatug/chinookdb/chinook\n', 'datatug/chinookdb/Other\n')]);
addCase('recordsets', 'one fewer than the entities', inManifest('  - Track\n', ''));
addCase('recordsets', 'one more than the entities', inManifest('  - Track\n', '  - Track\n  - Extra\n'));
addCase('recordsets', 'one fewer and one more', inManifest('  - Track\n', '  - Extra\n'));
addCase('recordsets', 'in another case', inManifest('  - Album\n', '  - album\n'));
addCase('recordsets', 'a name twice', inManifest('  - Track\n', '  - Track\n  - Track\n'));
addCase('recordsets', 'the entities of a model without Track, and Track listed', asModel(modelText((m) => { delete m.entities.Track; })));
addCase('recordsets', 'many more than the entities', inManifest('  - Track\n', `  - Track\n${Array.from({ length: 40 }, (_, i) => `  - More${i}\n`).join('')}`));

// What JSON.parse accepts and what Go's decoder would do: the repository's decisions, one case each.
for (const [name, ops] of Object.entries({
  'a repeated module: the last is right': rawModel('"module":{"name":"Hostile"},'),
  'a repeated module: the last is wrong': asModel(base[modelPath].replace(/\}\s*$/, ',"module":{"name":"Hostile"}}\n')),
  'a repeated module.name: the last is right': asModel(base[modelPath].replace('"name": "chinook"', '"name": "Hostile", "name": "chinook"')),
  'a repeated module.name: the last is wrong': asModel(base[modelPath].replace('"name": "chinook"', '"name": "chinook", "name": "Hostile"')),
  'a repeated entities: the last is right': rawModel('"entities":{"Hostile":{}},'),
  'a repeated entities: the last is wrong': asModel(base[modelPath].replace(/\}\s*$/, ',"entities":{"Hostile":{}}}\n')),
  'a repeated entity': asModel(base[modelPath].replace('"entities": {', '"entities": {"Album": 1, ')),
  'a key written with an escape': asModel(base[modelPath].replace('"module"', '"m\\u006fdule"')),
  'a module name written with an escape': asModel(base[modelPath].replace('"chinook"', '"chin\\u006fok"')),
  'a lone surrogate in a value': unrelated('x', '"\\ud800"'),
  'a lone surrogate in the module name': asModel(base[modelPath].replace('"chinook"', '"chinook\\ud800"')),
  'a number too big for a double': unrelated('x', '1e999'),
  'a long integer': unrelated('x', '123456789012345678901234567890'),
  'minus zero': unrelated('x', '-0'),
  'a number with an exponent': unrelated('x', '1E-5'),
  'a number with a leading zero': unrelated('x', '01'),
  'a number that is a plus': unrelated('x', '+1'),
  'a number with a trailing dot': unrelated('x', '1.'),
  'a BOM at the start': asModel(`﻿${base[modelPath]}`),
  'text after the value': asModel(`${base[modelPath]} x`),
  'a second value after the first': asModel(`${base[modelPath]}\n{}`),
  'white space after the value': asModel(`${base[modelPath]}\n\n \t\r\n`),
  'a form feed between tokens': asModel(base[modelPath].replace('"1.0-draft",', '"1.0-draft",\f')),
  'a comment': asModel(base[modelPath].replace('"module": {', '"module": /* c */{')),
  'single quotes': asModel(base[modelPath].replace('"modelspec"', "'modelspec'")),
  'a trailing comma': asModel(base[modelPath].replace(/\}\s*$/, ',}\n')),
  'a raw tab in a string': unrelated('x', '"a\tb"'),
  'a raw line break in a string': unrelated('x', '"a\nb"'),
  'an invalid escape': unrelated('x', '"\\x"'),
  'NaN': unrelated('x', 'NaN'),
  'a value of 49 nested arrays (50 levels in all)': [['nest', modelPath, 49]],
  'a value of 99 nested arrays (100 levels in all)': [['nest', modelPath, 99]],
  'a value of 100 nested arrays (101 levels in all)': [['nest', modelPath, 100]],
  'a value of 5000 nested arrays': [['nest', modelPath, 5000]],
  'a byte that is not UTF-8 in an unrelated string': [['bytes', modelPath, b64(base[modelPath].replace(/\}\s*$/, ',"x":"a\ue000b"}\n'))]],
  'a byte that is not UTF-8 in the module name': [['bytes', modelPath, b64(base[modelPath].replace('"chinook"', '"chin\ue000ook"'))]],
  'the keys Module, Name and Entities in capitals': asModel(base[modelPath].replace('"module"', '"Module"').replace('"entities"', '"Entities"')),
  'the key Name in capitals': asModel(base[modelPath].replace('"name": "chinook"', '"Name": "chinook"')),
  'the key Entities in capitals': asModel(base[modelPath].replace('"entities"', '"Entities"')),
  'the key Module in capitals': asModel(base[modelPath].replace('"module"', '"Module"')),
  'nested 98 arrays below module (100 levels in all)': asModel(modelText((m) => { m.module.x = JSON.parse('['.repeat(98) + ']'.repeat(98)); })),
  'nested 99 arrays below module (101 levels in all)': asModel(modelText((m) => { m.module.x = JSON.parse('['.repeat(99) + ']'.repeat(99)); })),
  'nested 97 arrays below an entity (100 levels in all)': asModel(modelText((m) => { m.entities[Object.keys(m.entities)[0]].x = JSON.parse('['.repeat(97) + ']'.repeat(97)); })),
  'nested 98 arrays below an entity (101 levels in all)': asModel(modelText((m) => { m.entities[Object.keys(m.entities)[0]].x = JSON.parse('['.repeat(98) + ']'.repeat(98)); })),
  'a NUL byte in white space': asModel(base[modelPath].replace('{', '{\0')),
})) addCase('json', name, ops);

// The meaning file as a MeaningGraph file: what is YAML, a mapping, its id, license and models: entry.
const meaningText = base[meaningPath];
const entryPath = 'chinook.modelspec.hcl';
const withEntry = (entry) => [['edit', meaningPath, `  chinook: ${entryPath}\n`, entry === null ? '' : `  chinook: ${entry}\n`]];
for (const [name, ops] of Object.entries({
  'empty': asMeaning(''), 'only comments': asMeaning('# nothing\n'), 'only white space': asMeaning('\n \n'), 'not YAML': asMeaning('a: [\n'), 'a list': asMeaning('- a\n- b\n'),
  'a string': asMeaning('just text\n'), 'a number': asMeaning('5\n'), 'null': asMeaning('null\n'), 'a mapping with nothing in it': asMeaning('{}\n'),
  // The Directory (directory.mjs parseMeaningFile, 487) wants a concepts list; the Chinook checker never reads the concepts.
  'concepts missing': [['edit', meaningPath, '\nconcepts:\n', '\nconceptz:\n']], 'concepts null': [['edit', meaningPath, '\nconcepts:\n', '\nconcepts: ~\nlist:\n']],
  'concepts a mapping': [['edit', meaningPath, '\nconcepts:\n', '\nconcepts: {}\nlist:\n']], 'concepts a string': [['edit', meaningPath, '\nconcepts:\n', '\nconcepts: none\nlist:\n']],
  'concepts an empty list': [['edit', meaningPath, '\nconcepts:\n', '\nconcepts: []\nlist:\n']],
  // The Directory's validateConcept (meaning.mjs 58-80) and its rule that a concept is declared once; the checker never reads the concepts.
  'a concept with no id': [['edit', meaningPath, '\nconcepts:\n', '\nconcepts:\n  - labels: {en: A}\n']], 'a concept id that is not lower case': [['edit', meaningPath, '\nconcepts:\n', '\nconcepts:\n  - id: Artist\n']],
  'a concept declared twice': [['edit', meaningPath, '\nconcepts:\n', '\nconcepts:\n  - id: artist\n  - id: artist\n']], 'a label with an angle bracket': [['edit', meaningPath, '\nconcepts:\n', '\nconcepts:\n  - id: a-b\n    labels: {en: "a<b"}\n']],
  // The Directory's rules for the bindings of a concept against the model (directory.mjs 728-754); the checker never reads the concepts.
  'a binding that names an entity the model lacks': [['edit', meaningPath, '\nconcepts:\n', '\nconcepts:\n  - id: a-b\n    bindings:\n      - model: modelspec:///chinook.Nope\n        role: entity\n']],
  'a binding that names a property the entity lacks': [['edit', meaningPath, '\nconcepts:\n', '\nconcepts:\n  - id: a-b\n    bindings:\n      - model: modelspec:///chinook.Album\n        property: Nope\n        role: identifier\n']],
  'id of another graph': [['edit', meaningPath, '\nid: chinook\n', '\nid: other\n']], 'id missing': [['edit', meaningPath, '\nid: chinook\n', '\n']],
  'id a number': [['edit', meaningPath, '\nid: chinook\n', '\nid: 5\n']], 'id null': [['edit', meaningPath, '\nid: chinook\n', '\nid:\n']],
  'id quoted': [['edit', meaningPath, '\nid: chinook\n', '\nid: "chinook"\n']],
  'a graph id that is digits, and a number in the file': [...inManifest('    id: chinook\n', '    id: "5"\n'), ['edit', meaningPath, '\nid: chinook\n', '\nid: 5\n']],
  'a graph id that is digits, and text in the file': [...inManifest('    id: chinook\n', '    id: "5"\n'), ['edit', meaningPath, '\nid: chinook\n', '\nid: "5"\n']], 'id in another case': [['edit', meaningPath, '\nid: chinook\n', '\nid: Chinook\n']],
  'id written twice': [['edit', meaningPath, '\nid: chinook\n', '\nid: chinook\nid: chinook\n']],
  'license of another licence': [['edit', meaningPath, '\nlicense: CC0-1.0\n', '\nlicense: MIT\n']], 'license missing': [['edit', meaningPath, '\nlicense: CC0-1.0\n', '\n']],
  'licence spelled the other way': [['edit', meaningPath, '\nlicense: CC0-1.0\n', '\nlicence: CC0-1.0\n']],
  'license in another case': [['edit', meaningPath, '\nlicense: CC0-1.0\n', '\nlicense: cc0-1.0\n']],
  'models missing': [['edit', meaningPath, 'models:\n  chinook: chinook.modelspec.hcl\n', '']], 'models a list': [['edit', meaningPath, 'models:\n  chinook: chinook.modelspec.hcl\n', 'models:\n  - chinook.modelspec.hcl\n']],
  'models null': [['edit', meaningPath, 'models:\n  chinook: chinook.modelspec.hcl\n', 'models:\n']], 'models for another module': withEntry(null).concat([['edit', meaningPath, 'models:\n', 'models:\n  other: chinook.modelspec.hcl\n']]),
  'the entry a number': withEntry('5'), 'the entry null': withEntry(''), 'the entry blank': withEntry('" "'), 'the entry a list': withEntry('[a]'),
  'the entry another file': withEntry('other.modelspec.hcl'), 'the entry with ./': withEntry('./chinook.modelspec.hcl'), 'the entry through ..': withEntry('../model/chinook.modelspec.hcl'),
  'the entry through a directory': withEntry('x/../chinook.modelspec.hcl'), 'the entry ending in /.': withEntry('chinook.modelspec.hcl/.'),
  'the entry with a trailing slash': withEntry('chinook.modelspec.hcl/'), 'the entry with a trailing /./': withEntry('chinook.modelspec.hcl/./'),
  'the entry with an empty segment': withEntry('.//chinook.modelspec.hcl'), 'the entry with a leading slash': withEntry('/model/chinook.modelspec.hcl'),
  'the entry that leaves the repository': withEntry('../../chinook.modelspec.hcl'), 'the entry that is ..': withEntry('..'), 'the entry that is .': withEntry('.'),
  'the entry with a space': withEntry('"chinook.modelspec .hcl"'), 'the entry with a glob': withEntry('"*.modelspec.hcl"'), 'the entry with a backslash': withEntry("'model\\chinook.modelspec.hcl'"),
  'the entry an empty string': withEntry('""'), 'the entry with a space that .. takes away': withEntry('"a b/../chinook.modelspec.hcl"'), 'the entry with a star that .. takes away': withEntry('"a*/../chinook.modelspec.hcl"'),
  'the entry with a backslash that .. takes away': withEntry("'a\\b/../chinook.modelspec.hcl'"), 'the entry with every character of the spelling': withEntry('d-d_d.d9/../chinook.modelspec.hcl'),
  'the entry in upper case': withEntry('CHINOOK.modelspec.hcl'), 'the entry written in a flow mapping': [['edit', meaningPath, 'models:\n  chinook: chinook.modelspec.hcl\n', 'models: {chinook: chinook.modelspec.hcl}\n']],
  'the entry written in double quotes': withEntry('"chinook.modelspec.hcl"'), 'the entry written in single quotes': withEntry("'chinook.modelspec.hcl'"),
  'models with the entry twice': [['edit', meaningPath, '  chinook: chinook.modelspec.hcl\n', '  chinook: chinook.modelspec.hcl\n  chinook: chinook.modelspec.hcl\n']],
  'a hcl file that is not the entry': [['copy', hclPath, 'model/other.modelspec.hcl'], ...inManifest('hcl: model/chinook.modelspec.hcl', 'hcl: model/other.modelspec.hcl')],
  'over the size bound (a comment)': [['pad', meaningPath, '# ', 300000]],
})) addCase('meaning', name, ops);

// What the YAML reader refuses and the checker's YAML library reads: each is a kind of the reader, recorded in the README of package manifest.
for (const [name, ops] of Object.entries({
  'an anchor and an alias': inMeaning('x-anchor: &a 1\nx-alias: *a'), 'a tag': inMeaning('x-tag: !!str 1'), 'a merge key': inMeaning('x-merge:\n  <<: {a: 1}'),
  'a key that is a number': inMeaning('2024: x'), 'a tab after a colon': inMeaning('x-tab:\t1'), 'a quoted value over two lines': inMeaning('x-quoted: "a\n  b"'),
  'a directive': asMeaning(`%YAML 1.2\n---\n${meaningText}`), 'a document end marker': asMeaning(`${meaningText}\n...\n`), 'a second document': asMeaning(`${meaningText}\n---\nx: 1\n`),
  'a hexadecimal number': inMeaning('x-hex: 0x10'), 'infinity': inMeaning('x-inf: .inf'), 'an integer beyond 2^53': inMeaning('x-big: 9007199254740993'),
  'a bare carriage return': inMeaning('x-cr: a\rb'), 'a C1 control character': inMeaning('x-c1: "\u0085"'), 'half of a surrogate pair': inMeaning('x-esc: "\\ud83c"'),
  'a collection nested 70 levels': inMeaning(`x-deep: ${'['.repeat(70)}${']'.repeat(70)}`), 'a collection nested 40 levels': inMeaning(`x-deep: ${'['.repeat(40)}${']'.repeat(40)}`),
  'a byte that is not UTF-8': [['bytes', meaningPath, b64(`${meaningText}# a\ue000b\n`)]], 'a NUL character': [['bytes', meaningPath, b64(`${meaningText}# a\0b\n`)]],
  'a byte order mark': asMeaning(`﻿${meaningText}`), 'CRLF line endings': asMeaning(meaningText.replaceAll('\n', '\r\n')),
  'an explicit key': inMeaning('? x-explicit\n: 1'), 'a flow mapping as a key': inMeaning('{a: 1}: x'), 'a long plain value over two lines': inMeaning('x-plain: one\n  two'),
  'a block scalar': inMeaning('x-block: |\n  one\n  two'), 'a null written with a tilde': inMeaning('x-null: ~'), 'a date': inMeaning('x-date: 2024-01-01'),
  'a yes': inMeaning('x-yes: yes'), 'a quoted key': inMeaning('"x-q": 1'), 'a comment after a value': inMeaning('x-c: 1 # c'),
})) addCase('yaml', name, ops);

// A second own-form manifest, judged on the content of the files it names.
addCase('listed', 'a second own-form manifest whose model.address names another module', [...second, ['edit', 'two.yaml', 'datatug/chinookdb/chinook\n', 'datatug/chinookdb/Other\n']]);
addCase('listed', 'a second own-form manifest whose recordsets differ from the entities', [...second, ['edit', 'two.yaml', '  - Track\n', '']]);
addCase('listed', 'a second own-form manifest, all there, the same files', second);

// What one check may cost: the entities of a model and the recordsets of a manifest are bounded (MaxEntities, MaxRecordsets, 10000 each); the checker compares them
// in time that grows with the product of the two.
// The entities are no longer Chinook's, so the concepts of the meaning file, whose bindings name Chinook's entities, go too: the Directory refuses a binding to an entity the model lacks.
const withoutConcepts = base[meaningPath].slice(0, base[meaningPath].indexOf('\nconcepts:\n') + 1) + 'concepts: []\n';
const sameNames = (n) => [['entities', modelPath, n], ['recordsets', manifestPath, n], ['file', meaningPath, withoutConcepts]];
addCase('limits', '10000 entities and the same 10000 recordsets', sameNames(10000));
addCase('limits', '10001 entities and the same 10001 recordsets', sameNames(10001));
addCase('limits', '20000 recordsets against 330000 entities', [['entities', modelPath, 330000], ['recordsets', manifestPath, 20000]]);
addCase('limits', '10001 recordsets against 10000 entities', [['entities', modelPath, 10000], ['recordsets', manifestPath, 10001]]);

// The Directory's own fixture of the same repository, read from its checkout: the whole repository, as another team keeps it.
const fixtureFile = (file) => readFileSync(join(directoryRoot, 'scripts/fixtures/chinookdb', file), 'utf8');
const fixture = ['OVDB.md', manifestPath, modelPath, hclPath, meaningPath].map((file) => ['file', file, fixtureFile(file)]);
addCase('fixtures', 'the Directory\'s fixture of chinookdb', fixture, null);
addCase('fixtures', 'the Directory\'s fixture, with the repository its manifest names', fixture, JSON.parse(JSON.stringify(parseYaml(fixtureFile(manifestPath)))).publisher.repository);

addCase('fixtures', 'the hoster example alone, with no model files', [['copy', 'hoster.yaml', manifestPath], ['remove', modelPath], ['remove', hclPath], ['remove', meaningPath]], null);

// ---- the reasons ----
//
// A golden case is only as good as the reason the checker gives for its verdict: a case named "a byte that is not UTF-8" that the checker refuses
// because the bytes were mangled on the way, and not because of that byte, says nothing about that byte (the review of #39 found two such cases, and the
// goldens had them for a release). So every case has an expectation written from its name, and the checker's own messages must bear it out: for a
// refusal, a problem that matches the text; for a case that both accept, no problem at all. A case that no line names fails, and every mismatch is
// listed before the run fails.
// Where the checker's message puts a fault is part of the reason: a message that two different faults give the same words for (JSON.parse says
// "Expected property name" for a comment, a form feed and single quotes alike) is pinned by where it says the fault is, and by the fault being in the
// text of the case where the name says it is. A case whose fault is swapped for another no longer matches its own line.
const jsonAt = (head, needle, skip = 0, len = needle.length - skip, last = false) => (problem, o) => {
  const text = o.texts[modelPath];
  const start = (last ? text.lastIndexOf(needle) : text.indexOf(needle)) + skip;
  const m = problem.match(/is not a ModelSpec JSON file: (.*?)(?: in JSON)? at position (\d+)/);
  return start >= skip && m !== null && m[1] === head && Number(m[2]) >= start && Number(m[2]) <= start + len;
};
const jsonToken = (token, needle) => (problem, o) => problem.includes(`is not a ModelSpec JSON file: Unexpected token '${token}'`) && o.texts[modelPath].includes(needle);
// The line of the second of two lines that are the same, for "Map keys must be unique at line N, column C".
const duplicate = (path, line, column) => (problem, o) => {
  const lines = o.texts[path].split('\n');
  const first = lines.indexOf(line);
  const second = lines.indexOf(line, first + 1);
  return first >= 0 && second > first && problem.includes(`is not valid YAML: Map keys must be unique at line ${second + 1}, column ${column}`);
};
const tracked = /must be a tracked regular file/;
const expectations = [
  [/^base:/, null],
  // The places of the five files that a manifest and the checker name.
  [/^place:OVDB\.md: (missing|different-case)$/, /OVDB\.md is missing from the repository root/],
  [/^place:[^:]+: (executable|case-collision)$/, null],
  [/^place:/, tracked],
  [/^tree:/, null],
  [/^limits: 20000 recordsets against 330000 entities$/, /recordsets lacks/],
  [/^limits: 10001 recordsets against 10000 entities$/, /recordsets names things/],
  [/^limits:/, null],
  [/^fixtures:/, null],
  [/^worktree: only the working tree has a valid manifest/, /deployment\.engine is required/],
  [/^worktree: only the working tree has a broken manifest/, null],
  [/^worktree: only the index has OVDB\.md/, /OVDB\.md must be a tracked regular file/],
  [/^state: (bare)$/, /cannot be read at HEAD/],
  [/^state: (unborn|not-a-repository|alternates-gone)$/, /git could not read HEAD/],
  [/^state:/, null],
  [/^repository: (the repository of the manifest|not given)$/, null],
  [/^repository: publisher\.repository is not a repository/, /publisher\.repository must be/],
  [/^repository: /, { plain: null, with: /publisher\.repository must be/ }],
  [/^listed: the repository, two manifests/, { plain: null, with: /publisher\.repository must be/ }],
  [/^listed: (two manifests|32 manifests|33 manifests|a second own-form manifest, all there.*)$/, null],
  [/^listed: the second is (missing|a directory|a symlink)$/, /publish entry \.\/hoster\.yaml must be a tracked regular file/],
  [/^listed: the second is invalid/, /deployment\.engine is required/],
  [/^listed: both are invalid/, /is not a mapping/],
  [/^listed: a second own-form manifest names a file that is not there/, /model\.modelspec names model\/other\.modelspec\.json, which must be a tracked/],
  [/^listed: a second own-form manifest names a file that is a directory/, /meaning\.file names model\/elsewhere\.meaning\.yaml, which must be a tracked/],
  [/^listed: a second own-form manifest whose model\.address/, /model\.address must be/],
  [/^listed: a second own-form manifest whose recordsets/, /recordsets lacks/],
  // The documents.
  [/^document: manifest: (not a mapping|empty)$/, /is not a mapping/],
  [/^document: manifest: not YAML$/, /is not valid YAML/],
  [/^document: manifest: unknown key$/, /unknown keys: extra/],
  [/^document: manifest: http url$/, /url must be https/],
  [/^document: manifest: upper-case id$/, /id must be lower-case/],
  [/^document: manifest: no publisher\.repository$/, /publisher\.repository is required/],
  [/^document: OVDB\.md: no front matter$/, /has no YAML frontmatter/],
  [/^document: OVDB\.md: ovdb 2$/, /ovdb must be 1/],
  [/^document: OVDB\.md: empty publish$/, /publish must list at least one/],
  [/^document: OVDB\.md: unknown key$/, /unknown frontmatter keys/],
  [/^document: OVDB\.md: lists a (missing manifest|directory)$/, /publish entry .* must be a tracked regular file/],
  [/^document: OVDB\.md: lists a manifest twice$/, /twice/],
  [/^document: OVDB\.md: lists a path that goes up$/, /must be an explicit file path/],
  [/^document: /, null],
  // The objects of the repository that git cannot give.
  [/^object: OVDB\.md: /, /OVDB\.md cannot be read at HEAD/],
  [/^object: the manifest: /, /ovdb\.yaml cannot be read at HEAD/],
  [/^object: the model file of a shallow clone: /, /chinook\.modelspec\.json cannot be read at HEAD/],
  [/^object: the model file: /, /chinook\.modelspec\.json cannot be read at HEAD/],
  [/^object: the meaning file: /, /chinook\.meaning\.yaml cannot be read at HEAD/],
  [/^object: the tree of the directory of the model files: /, tracked],
  [/^object: the top tree: /, /OVDB\.md is missing from the repository root/],
  [/^object: the model file holds the bytes of another object/, /is not a ModelSpec JSON file/],
  [/^object: the meaning file holds the bytes of another object/, /is not a MeaningGraph file/],
  [/^object: the hcl file, which the checker does not read: /, null],
  [/^object: a model file (larger than this check reads|of 3 MiB)$/, null],
  // The model file.
  [/^model: (empty|only white space|not JSON|a JSON array|null|a number|a string)$/, /is not a ModelSpec JSON file/],
  [/^model: (no modelspec version|an entity without properties|a property with neither type nor entity)$/, null],
  [/^model: (an empty object|no module|module |module\.name )/, /has no module\.name/],
  [/^model: (no entities|entities an array|entities null)$/, /has no entities/],
  [/^model: (entities empty|one entity fewer)$/, /recordsets names things/],
  [/^model: (one entity more|an entity called __proto__ that recordsets lack)$/, /recordsets lacks/],
  [/^model: (another module|the module of model\.address)/, /model\.address must be/],
  [/^model: model\.name is not a module name/, /model\.name, when given, must be/],
  [/^model: model\.name is not the module$/, /model\.name is Other/],
  [/^model: model\.name and model\.address in another case/, /model\.name is Chinook, but .* is module chinook/],
  [/^model: model\.name and model\.address both differ/, /model\.name is Other/],
  [/^model: /, null],
  // JSON the way JSON.parse reads it.
  [/^json: a repeated module(\.name)?: the last is wrong$/, /model\.address must be/],
  [/^json: a repeated entities: the last is wrong$/, /recordsets lacks/],
  [/^json: (a lone surrogate in the module name|a byte that is not UTF-8 in the module name|the keys Module, Name and Entities in capitals|the key Name in capitals|the key Module in capitals)$/, /has no module\.name/],
  [/^json: the key Entities in capitals$/, /has no entities/],
  [/^json: a number with a leading zero$/, jsonAt('Unexpected number', '"x":01', 4, 2)],
  [/^json: a number that is a plus$/, jsonToken('+', ':+1')],
  [/^json: a number with a trailing dot$/, jsonAt('Unterminated fractional number', '"x":1.', 4, 2)],
  [/^json: a BOM at the start$/, jsonToken('\ufeff', '\ufeff{')],
  [/^json: text after the value$/, jsonAt('Unexpected non-whitespace character after JSON', ' x', 1, 1, true)],
  [/^json: a second value after the first$/, jsonAt('Unexpected non-whitespace character after JSON', '\n{}', 1, 1, true)],
  [/^json: a form feed between tokens$/, jsonAt('Expected double-quoted property name', '\f')],
  [/^json: a comment$/, jsonToken('/', '/* c */')],
  [/^json: single quotes$/, jsonAt("Expected property name or '}'", "'modelspec'")],
  [/^json: a trailing comma$/, jsonAt('Expected double-quoted property name', ',}')],
  [/^json: a raw tab in a string$/, jsonAt('Bad control character in string literal', 'a\tb', 1, 1)],
  [/^json: a raw line break in a string$/, jsonAt('Bad control character in string literal', 'a\nb', 1, 1)],
  [/^json: an invalid escape$/, jsonAt('Bad escaped character', '\\x', 1, 1)],
  [/^json: NaN$/, jsonToken('N', 'NaN')],
  [/^json: a NUL byte in white space$/, jsonAt("Expected property name or '}'", '\0')],
  [/^json: /, null],
  // The recordsets.
  [/^recordsets: (one fewer than the entities|one fewer and one more|in another case)$/, /recordsets lacks/],
  [/^recordsets: (one more than the entities|the entities of a model without Track, and Track listed|many more than the entities)$/, /recordsets names things/],
  [/^recordsets: a name twice$/, /recordsets lists a name twice/],
  // The meaning file.
  [/^meaning: (empty|only comments|only white space|a list|a string|a number|null)$/, /is not a MeaningGraph file: it must be a mapping/],
  [/^meaning: not YAML$/, /is not valid YAML: Flow sequence in block collection must be sufficiently indented and end with a \] at line 2/],
  [/^meaning: id written twice$/, duplicate(meaningPath, 'id: chinook', 1)],
  [/^meaning: models with the entry twice$/, duplicate(meaningPath, '  chinook: chinook.modelspec.hcl', 3)],
  [/^meaning: (a mapping with nothing in it|id of another graph|id missing|id a number|id null|id in another case|a graph id that is digits, and a number in the file)$/, /meaning\.graph\.id is/],
  [/^meaning: licen[cs]e/, /licences\.meaning is/],
  [/^meaning: (models missing|models a list|models null|models for another module|the entry a number|the entry null|the entry blank|the entry a list|the entry an empty string)$/, /has no models: entry/],
  [/^meaning: (the entry another file|the entry in upper case|the entry that is \.|a hcl file that is not the entry)$/, /but the meaning file's models: entry/],
  [/^meaning: the entry (with a trailing slash|with a trailing \/\.\/|with an empty segment|with a leading slash|that leaves the repository|that is \.\.|with a space|with a glob|with a backslash|with a space that|with a star that|with a backslash that)/, /models must name/],
  [/^meaning: /, null],
  // The YAML the reader is stricter about: the checker's library reads all of it but a second document.
  [/^yaml: a second document$/, /is not valid YAML: Source contains multiple documents/],
  [/^yaml: /, null],
];
const reasonProblems = [];
for (const o of observed) {
  const line = expectations.find(([pattern]) => pattern.test(o.label));
  if (line === undefined) { reasonProblems.push(`${o.label}: no expectation names this case`); continue; }
  const [, expect] = line;
  const want = expect !== null && typeof expect === 'object' && !(expect instanceof RegExp) ? expect : { plain: expect, with: expect };
  const matches = (expected, p) => (typeof expected === 'function' ? expected(p, o) : expected.test(p));
  for (const [run, expected, verdictOfRun, problems] of [['plain', want.plain, o.plain, o.plainProblems], ['with --repository', want.with, o.with, o.withProblems]]) {
    if (verdictOfRun === '-') continue;
    if (expected === null) { if (verdictOfRun !== '1') reasonProblems.push(`${o.label} (${run}): the checker should accept this case and says: ${problems.join(' | ').slice(0, 160)}`); continue; }
    if (verdictOfRun !== '0') reasonProblems.push(`${o.label} (${run}): the checker should refuse this case, as ${expected}, and accepts it`);
    else if (!problems.some((p) => matches(expected, p))) reasonProblems.push(`${o.label} (${run}): the checker refuses this case, but not as ${expected}: ${problems.join(' | ').slice(0, 160)}`);
  }
}
if (reasonProblems.length > 0) { console.error(`${reasonProblems.length} case(s) whose verdict does not come from the reason their name states:\n${reasonProblems.join('\n')}`); process.exit(1); }
// ---- the golden ----

const counts = { cases: cases.length, accepted: cases.filter((c) => c.plain === '1').length, acceptedWithRepository: cases.filter((c) => c.with === '1').length };
if (argValue('--reasons') !== undefined) for (const o of observed) console.error(`${o.label} | ${o.plain}${o.with} | ${(o.plainProblems[0] ?? '').slice(0, 90)} | ${(o.withProblems[0] ?? '').slice(0, 90)}`);
const golden = `${JSON.stringify({
  format: 'ovdb-publisher-repository/1',
  reference: `${pins.chinookdb.repository}@${pins.chinookdb.commit}`,
  repository: own,
  base,
  counts,
  cases: cases.map((c) => ({ group: c.group, name: c.name, ops: c.ops, repository: c.repository, verdict: `${c.plain}${c.with}` })),
}, null, 1)}\n`;
const digests = `${JSON.stringify({ 'repo/testdata/reference/repository.json': sha(golden), 'repo/testdata/reference/representation-stages.json': stageDigest }, null, 1)}\n`;
const targets = [[goldenPath, golden], [digestsPath, digests]];
if (thrown > 0) console.error(`note: the checker threw on ${thrown} case(s); they are recorded as refused`);
if (process.argv.includes('--check')) {
  if (targets.some(([path, text]) => !existsSync(path) || readFileSync(path, 'utf8') !== text)) { console.error(`the goldens in ${here} are stale: run node ${process.argv[1]}`); process.exit(1); }
  console.log(`the goldens are up to date (${counts.cases} repositories)`);
} else {
  for (const [path, text] of targets) writeFileSync(path, text);
  console.log(`wrote ${counts.cases} repositories (accepted ${counts.accepted}, accepted with --repository ${counts.acceptedWithRepository}): ${(golden.length / 1024).toFixed(0)} KiB`);
}
