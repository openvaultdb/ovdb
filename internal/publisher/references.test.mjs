// A scripted check of checkoutReference and assertAsCommitted (references.mjs), the functions that give the generators the references they import and run:
//
//   node --test internal/publisher/references.test.mjs
//
// It fetches from a repository of its own made in a temporary directory (no network), and shows that a checkout that is not as committed is
// refused: an edited tracked file (also one marked assume-unchanged, which git status does not show), an extra file, a node_modules that the
// repository ignores and Node would resolve first, another commit; that the default is a new private directory each time; and that a cache
// the user keeps is reused only as committed. It is not part of `go test`, which reads the goldens and starts no process.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, before, test } from 'node:test';
import { assertAnchors, assertAsCommitted, assertGeneratorNode, checkoutReference, generatorNode } from './references.mjs';

const git = (cwd, ...args) => execFileSync('git', args, { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'] }).trim();
let scratch; let remote; let pin;

before(() => {
  scratch = mkdtempSync(join(tmpdir(), 'ovdb-references-test-'));
  remote = join(scratch, 'remote');
  mkdirSync(remote);
  git(remote, 'init', '--quiet');
  git(remote, 'config', 'user.email', 'test@example.com');
  git(remote, 'config', 'user.name', 'test');
  git(remote, 'config', 'uploadpack.allowAnySHA1InWant', 'true');
  mkdirSync(join(remote, 'scripts'));
  writeFileSync(join(remote, 'scripts', 'check.mjs'), "export const licenceIds = ['MIT'];\n");
  writeFileSync(join(remote, '.gitignore'), 'node_modules\n'); // as the repositories of the references do
  git(remote, 'add', '.');
  git(remote, 'commit', '--quiet', '-m', 'the reference');
  pin = { repository: 'example/reference', commit: git(remote, 'rev-parse', 'HEAD') };
});
after(() => rmSync(scratch, { recursive: true, force: true }));

// A fresh checkout in its own parent directory, and the check of it as the generators make it.
let counter = 0;
const fresh = () => checkoutReference('chinookdb', { pin, remote, parent: join(scratch, `parent-${counter += 1}`) });
const check = (dir) => assertAsCommitted(dir, pin.repository, pin.commit);
const refused = (dir, pattern) => assert.throws(() => check(dir), pattern);
const edited = "export const licenceIds = ['MIT', 'EUPL-1.2'];\n";

test('a checkout that a run made is as committed, and is never reused', () => {
  const dir = fresh();
  assert.equal(dir, join(scratch, `parent-${counter}`, `chinookdb-${pin.commit}`));
  check(dir);
  assert.throws(() => checkoutReference('chinookdb', { pin, remote, parent: join(scratch, `parent-${counter}`) }), /fetched fresh, never reused/);
});

test('an edited tracked file is refused', () => {
  const dir = fresh();
  writeFileSync(join(dir, 'scripts', 'check.mjs'), edited); // HEAD is unchanged, the working tree is not
  assert.equal(git(dir, 'rev-parse', 'HEAD'), pin.commit);
  refused(dir, /has local changes/);
});

test('a tracked file edited and marked assume-unchanged or skip-worktree is refused', () => {
  const dir = fresh();
  writeFileSync(join(dir, 'scripts', 'check.mjs'), edited);
  git(dir, 'update-index', '--assume-unchanged', 'scripts/check.mjs');
  assert.equal(git(dir, 'status', '--porcelain'), '', 'git status shows nothing: that is the point');
  refused(dir, /has local changes \(scripts\/check.mjs\)/);
  git(dir, 'update-index', '--no-assume-unchanged', 'scripts/check.mjs');
  git(dir, 'update-index', '--skip-worktree', 'scripts/check.mjs');
  refused(dir, /has local changes/);
});

test('a tracked file that is deleted is refused', () => {
  const dir = fresh();
  rmSync(join(dir, 'scripts', 'check.mjs'));
  refused(dir, /lacks a file|has local changes|index/);
});

test('a file that the commit does not have is refused, the root node_modules is not', () => {
  const dir = fresh();
  mkdirSync(join(dir, 'node_modules', 'yaml'), { recursive: true });
  writeFileSync(join(dir, 'node_modules', 'yaml', 'index.js'), '');
  check(dir);
  writeFileSync(join(dir, 'scripts', 'extra.mjs'), 'export {};\n');
  refused(dir, /files that are not in/);
});

test('a node_modules below the root that .gitignore or .git/info/exclude hides is refused', () => {
  for (const hide of ['.gitignore', '.git/info/exclude']) {
    const dir = fresh();
    mkdirSync(join(dir, 'scripts', 'node_modules', 'yaml'), { recursive: true });
    writeFileSync(join(dir, 'scripts', 'node_modules', 'yaml', 'index.js'), 'export const parse = () => ({});\n');
    if (hide === '.git/info/exclude') writeFileSync(join(dir, '.git', 'info', 'exclude'), 'node_modules\n');
    assert.equal(git(dir, 'status', '--porcelain'), '', `git status shows nothing: ${hide} hides it`);
    assert.equal(git(dir, 'ls-files', '--others', '--exclude-standard'), '', 'and it is not an untracked file');
    refused(dir, /files that are not in .*scripts\/node_modules/);
  }
});

test('a node_modules planted and staged with git add -f is refused', () => {
  const dir = fresh();
  mkdirSync(join(dir, 'scripts', 'node_modules', 'yaml'), { recursive: true });
  writeFileSync(join(dir, 'scripts', 'node_modules', 'yaml', 'index.js'), 'export const parse = () => ({});\n');
  git(dir, 'add', '-f', 'scripts/node_modules/yaml/index.js');
  assert.equal(git(dir, 'ls-files', '--others'), '', 'it is tracked now, in the index');
  refused(dir, /index that is not the commit's tree \(scripts\/node_modules\/yaml\/index.js\)/);
});

test('git replace of the pinned commit by an edited one is refused', () => {
  const dir = fresh();
  writeFileSync(join(dir, 'scripts', 'check.mjs'), edited);
  git(dir, 'add', 'scripts/check.mjs');
  git(dir, '-c', 'user.email=t@example.com', '-c', 'user.name=t', 'commit', '--quiet', '-m', 'edited');
  const other = git(dir, 'rev-parse', 'HEAD');
  git(dir, 'reset', '--quiet', '--hard', pin.commit);
  git(dir, 'replace', pin.commit, other);
  git(dir, 'reset', '--quiet', '--hard', pin.commit); // with the replacement, this writes the edited tree
  assert.equal(readFileSync(join(dir, 'scripts', 'check.mjs'), 'utf8'), edited);
  assert.equal(git(dir, 'rev-parse', 'HEAD'), pin.commit);
  assert.equal(git(dir, 'status', '--porcelain'), '', 'git status shows nothing: the pinned commit now has the edited tree');
  refused(dir, /has local changes|index that is not/);
});

test('a clean filter in .git/config and .git/info/attributes that hides an edit is refused', () => {
  const dir = fresh();
  git(dir, 'config', 'filter.hide.clean', "printf \"export const licenceIds = ['MIT'];\\n\"");
  writeFileSync(join(dir, '.git', 'info', 'attributes'), 'scripts/check.mjs filter=hide\n');
  writeFileSync(join(dir, 'scripts', 'check.mjs'), "export const licenceIds = ['XYZ'];\n"); // the same size: git runs the filter only then
  assert.equal(git(dir, 'status', '--porcelain'), '', 'git status shows nothing: the filter makes the edit look like the commit');
  refused(dir, /has local changes|local git config/);
  // and a config key that a fresh clone does not have is refused by itself
  const clean = fresh();
  git(clean, 'config', 'core.fsmonitor', 'false');
  refused(clean, /local git config that a fresh clone does not have \(core.fsmonitor\)/);
});

test('extensions.worktreeConfig and a config.worktree that sets core.worktree are refused', () => {
  const dir = fresh();
  const clean = join(scratch, 'clean-copy');
  mkdirSync(join(clean, 'scripts'), { recursive: true });
  writeFileSync(join(clean, 'scripts', 'check.mjs'), "export const licenceIds = ['MIT'];\n");
  mkdirSync(join(dir, 'scripts', 'node_modules', 'yaml'), { recursive: true }); // what Node would resolve first, and what git is made not to see
  writeFileSync(join(dir, 'scripts', 'node_modules', 'yaml', 'index.js'), 'export {};\n');
  git(dir, 'config', 'extensions.worktreeConfig', 'true');
  writeFileSync(join(dir, '.git', 'config.worktree'), `[core]\n\tworktree = ${clean}\n`);
  refused(dir, /local git config that a fresh clone does not have \(extensions\.worktreeconfig/);
  git(dir, 'config', '--unset', 'extensions.worktreeConfig');
  rmSync(join(dir, 'scripts', 'node_modules'), { recursive: true }); // without the extension git ignores the file, and the planted directory shows
  refused(dir, /has a config\.worktree/);
});

test('the environment of the caller does not point git elsewhere', () => {
  const dir = fresh();
  writeFileSync(join(dir, 'scripts', 'check.mjs'), edited);
  const other = fresh();
  const saved = process.env.GIT_DIR;
  process.env.GIT_DIR = join(other, '.git'); // the other checkout is clean: if git used it, the edit would not be seen
  try {
    refused(dir, /has local changes/);
  } finally {
    if (saved === undefined) delete process.env.GIT_DIR; else process.env.GIT_DIR = saved;
  }
});

test('a clone that the user passes is held to the same rule', () => {
  const clone = join(scratch, 'clone');
  git(scratch, 'clone', '--quiet', remote, clone);
  assert.equal(checkoutReference('chinookdb', { pin, remote, explicit: clone }), clone);
  writeFileSync(join(clone, 'scripts', 'check.mjs'), 'export {};\n');
  assert.throws(() => checkoutReference('chinookdb', { pin, remote, explicit: clone }), /has local changes/);
});

test('a checkout at another commit is refused', () => {
  const dir = fresh();
  git(dir, 'config', 'user.email', 'test@example.com');
  git(dir, 'config', 'user.name', 'test');
  git(dir, 'commit', '--quiet', '--allow-empty', '-m', 'another commit');
  refused(dir, /is not at example\/reference@/);
});

test('with no parent the directory is new, private and not at a fixed name', () => {
  const dir = checkoutReference('chinookdb', { pin, remote });
  const parent = join(dir, '..');
  assert.match(parent, /ovdb-reference-[A-Za-z0-9]+$/);
  assert.notEqual(parent, join(tmpdir(), 'ovdb-publisher-reference'));
  if (process.platform !== 'win32') assert.equal(statSync(parent).mode & 0o077, 0, 'readable or writable by others');
  assert.equal(readFileSync(join(dir, 'scripts', 'check.mjs'), 'utf8'), "export const licenceIds = ['MIT'];\n");
});

test('assertAnchors is silent when every anchor is held, and otherwise names each missing one and stops with status 1', () => {
  const lines = [];
  const codes = [];
  const options = { err: (line) => lines.push(line), exit: (code) => codes.push(code) };
  assertAnchors('one two three', 'f.mjs', 'c0ffee', ['one', 'three'], options);
  assert.deepEqual([lines, codes], [[], []]);
  assertAnchors('one two three', 'f.mjs', 'c0ffee', ['one', 'four', 'five'], options);
  assert.deepEqual(codes, [1]);
  assert.match(lines[0], /f\.mjs at c0ffee no longer holds 2 of the 3 expressions/);
  assert.deepEqual(lines.filter((line) => line.startsWith('  missing anchor: ')), ['  missing anchor: four', '  missing anchor: five']);
});

test('a generator stops under any Node but the one of CI, and says which to use', () => {
  const lines = []; const codes = [];
  const options = { err: (line) => lines.push(line), exit: (code) => codes.push(code) };
  assertGeneratorNode(generatorNode, options);
  assert.deepEqual([lines, codes], [[], []]);
  assertGeneratorNode('v24.20.0', options);
  assert.deepEqual(codes, [1]);
  assert.ok(lines[0].includes(`Node ${generatorNode}`) && lines[0].includes('Node v24.20.0'));
});

test('the Node of CI is the one that the generators require', () => {
  const ci = readFileSync(new URL('../../.github/workflows/ci.yml', import.meta.url), 'utf8');
  // the jobs of the publisher check pin a full version (24.x.y); the web job's '22' is another matter
  const versions = [...ci.matchAll(/node-version: '(\d+\.\d+\.\d+)'/g)].map((match) => `v${match[1]}`);
  assert.ok(versions.length >= 2);
  for (const version of versions) assert.equal(version, generatorNode);
});
