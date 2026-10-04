// A scripted check of checkoutReference (references.mjs), the function that gives the generators the references they import and run:
//
//   node --test internal/publisher/references.test.mjs
//
// It fetches from a repository of its own made in a temporary directory (no network), and shows that a checkout that is not as committed is
// refused: an edited tracked file, an extra file, another commit; that the default is a new private directory each time; and that a cache
// the user keeps is reused only as committed. It is not part of `go test`, which reads the goldens and starts no process.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, before, test } from 'node:test';
import { checkoutReference } from './references.mjs';

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
  git(remote, 'add', '.');
  git(remote, 'commit', '--quiet', '-m', 'the reference');
  pin = { repository: 'example/reference', commit: git(remote, 'rev-parse', 'HEAD') };
});
after(() => rmSync(scratch, { recursive: true, force: true }));

const options = (cacheRoot) => ({ pin, remote, cacheRoot });

test('a cache that is as committed is reused', () => {
  const cache = join(scratch, 'cache-clean');
  const dir = checkoutReference('chinookdb', options(cache));
  assert.equal(dir, join(cache, `chinookdb-${pin.commit}`));
  assert.equal(checkoutReference('chinookdb', options(cache)), dir);
});

test('an edited checker in the cache is refused', () => {
  const cache = join(scratch, 'cache-edited');
  const dir = checkoutReference('chinookdb', options(cache));
  writeFileSync(join(dir, 'scripts', 'check.mjs'), "export const licenceIds = ['MIT', 'EUPL-1.2'];\n"); // HEAD is unchanged, the working tree is not
  assert.equal(git(dir, 'rev-parse', 'HEAD'), pin.commit);
  assert.throws(() => checkoutReference('chinookdb', options(cache)), /has local changes/);
});

test('a file that the commit does not have is refused, a dependency is not', () => {
  const cache = join(scratch, 'cache-stray');
  const dir = checkoutReference('chinookdb', options(cache));
  mkdirSync(join(dir, 'node_modules', 'yaml'), { recursive: true });
  writeFileSync(join(dir, 'node_modules', 'yaml', 'index.js'), '');
  assert.equal(checkoutReference('chinookdb', options(cache)), dir);
  writeFileSync(join(dir, 'scripts', 'extra.mjs'), 'export {};\n');
  assert.throws(() => checkoutReference('chinookdb', options(cache)), /files that are not in/);
});

test('a cache at another commit is refused', () => {
  const cache = join(scratch, 'cache-moved');
  const dir = checkoutReference('chinookdb', options(cache));
  git(dir, 'config', 'user.email', 'test@example.com');
  git(dir, 'config', 'user.name', 'test');
  git(dir, 'commit', '--quiet', '--allow-empty', '-m', 'another commit');
  assert.throws(() => checkoutReference('chinookdb', options(cache)), /is not at example\/reference@/);
});

test('a clone that the user passes is held to the same rule', () => {
  const clone = join(scratch, 'clone');
  git(scratch, 'clone', '--quiet', remote, clone);
  assert.equal(checkoutReference('chinookdb', { pin, remote, explicit: clone }), clone);
  writeFileSync(join(clone, 'scripts', 'check.mjs'), 'export {};\n');
  assert.throws(() => checkoutReference('chinookdb', { pin, remote, explicit: clone }), /has local changes/);
});

test('with no cache the directory is new, private and not at a fixed name', () => {
  const dir = checkoutReference('chinookdb', { pin, remote, cacheRoot: '' });
  const parent = join(dir, '..');
  assert.match(parent, /ovdb-reference-[A-Za-z0-9]+$/);
  assert.notEqual(parent, join(tmpdir(), 'ovdb-publisher-reference'));
  if (process.platform !== 'win32') assert.equal(statSync(parent).mode & 0o077, 0, 'readable or writable by others');
  assert.ok(existsSync(join(dir, 'scripts', 'check.mjs')));
  assert.equal(readFileSync(join(dir, 'scripts', 'check.mjs'), 'utf8'), "export const licenceIds = ['MIT'];\n");
});
