#!/usr/bin/env node
// Regenerates matrix.golden.json: the verdicts of the JavaScript reference
// implementations of the publisher rules over a generated matrix of inputs.
//
//   node internal/publisher/rules/testdata/reference/generate.mjs           # rewrite the golden
//   node internal/publisher/rules/testdata/reference/generate.mjs --check   # fail if the golden is stale
//
// The Go tests of internal/publisher/rules read the golden and fail if a Go
// function accepts a string that a reference refuses (see README.md in the
// package). They never run this script: it needs Node 24 or later, git and
// network access, and it is not part of `go test`.
//
// The two references, each at the commit pinned below:
//
//   directory  openvaultdb/directory  scripts/lib/urls.mjs, git.mjs, directory.mjs
//   chinookdb  datatug/chinookdb      scripts/lib/directory-rules.mjs
//
// Their files are fetched at exactly those commits into a directory that only
// this run can write (references.mjs: checkoutReference) and imported as they are; nothing
// is copied or re-implemented here, except where a reference keeps a rule inline
// (see `publish` below). The Directory needs its one dependency (yaml), which is
// installed from its own lock file. To use clones you already
// have, pass --directory <dir> and/or --chinookdb <dir>; their HEAD must be the
// pinned commit.
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { domainToASCII, fileURLToPath, pathToFileURL } from 'node:url';
import { assertGeneratorNode, assertAnchors, checkoutReference, references as pinnedReferences } from '../../../references.mjs';

assertGeneratorNode();

const here = dirname(fileURLToPath(import.meta.url));
const goldenPath = join(here, 'matrix.golden.json');

const pins = pinnedReferences; // internal/publisher/references.mjs: the one place that says where each reference is

// ---- the references, at the pinned commits ----

const argValue = (name) => { const at = process.argv.indexOf(name); return at === -1 ? undefined : process.argv[at + 1]; };
const run = (cwd, command, args) => execFileSync(command, args, { cwd, stdio: ['ignore', 'pipe', 'inherit'], encoding: 'utf8' }).trim();

const checkout = (name) => checkoutReference(name, { explicit: argValue(`--${name}`) });

const directoryRoot = checkout('directory');
const chinookRoot = checkout('chinookdb');
const fixtureRoot = checkout('fixtures');
const load = (root, file) => import(pathToFileURL(join(root, file)).href);
const directory = await load(directoryRoot, 'scripts/lib/directory.mjs');
const urls = await load(directoryRoot, 'scripts/lib/urls.mjs');
const gitlib = await load(directoryRoot, 'scripts/lib/git.mjs');
const chinook = await load(chinookRoot, 'scripts/lib/directory-rules.mjs');

// Two rules are kept inline in directory.mjs, not exported, so the verdicts below compose the same
// expression. Fail loudly if the pinned file does not hold exactly the text that is copied.
const directorySource = readFileSync(join(directoryRoot, 'scripts/lib/directory.mjs'), 'utf8');
assertAnchors(directorySource, 'scripts/lib/directory.mjs', pins.directory.commit, [
  "const isText = (value) => typeof value === 'string' && value.trim() !== '';",
  "if (!isText(entry) || !entry.startsWith('./') || !isRepositoryPath(entry.slice(2)))",
  "const localIdPattern = /^[a-z][a-z0-9-]{0,39}$/;",
]);

// ---- the verdicts: true accepts, false refuses, undefined: the reference has no such rule ----

// A manifest that the Directory accepts, to edit one field at a time: the
// Directory checks `deployment.engine` and `licences.data` only inside
// manifestProblems.
const baseManifest = () => ({
  format: 'ovdb-manifest/draft-1', id: 'chinook', title: 'Chinook', description: 'A sample database.',
  url: 'https://cloud.openvaultdb.com/ovdb/dbs/chinook',
  deployment: { url: 'https://cloud.openvaultdb.com/ovdb/dbs/chinook', engine: 'sqlite', discovery: 'https://cloud.openvaultdb.com/.well-known/openvaultdb' },
  model: { modelspec: 'model/chinook.modelspec.json' },
  meaning: { file: 'model/chinook.meaning.yaml', graph: { id: 'chinook', address: 'meaning://github.com/datatug/chinookdb' } },
  licences: { model: 'MIT', meaning: 'CC0-1.0', data: 'MIT' },
  publisher: { name: 'DataTug', url: 'https://github.com/datatug' },
  recordsets: ['Album'],
});
const baseProblems = directory.manifestProblems(baseManifest());
if (baseProblems.length > 0) throw new Error(`the base manifest is not accepted by the Directory: ${baseProblems.join('; ')}`);

const manifestAccepts = (edit, field) => {
  const manifest = baseManifest();
  edit(manifest);
  return !directory.manifestProblems(manifest).some((problem) => problem.startsWith(`${field} `));
};
const recordFile = 'databases/$records/x.yaml';
// Repeated keys of the claim's addresses are compared as manifests claim them.
const claimOf = (key, address) => ({
  key, file: `${key}.yaml`, manifest: 'ovdb.yaml',
  addresses: directory.addressClaims({ url: `https://x.openvaultdb.com/ovdb/${key}`, deployment: { url: address } }).filter((claim) => claim.field === 'deployment.url'),
});
const claimRelation = (address, other) => {
  const problems = directory.claimProblems([claimOf('mine', address), claimOf('other', other)]);
  if (problems.some((problem) => problem.includes(' is claimed by '))) return 'same';
  if (problems.some((problem) => problem.startsWith('mine.yaml: ovdb.yaml: deployment.url ') && problem.includes(' sits under '))) return 'under';
  if (problems.some((problem) => problem.startsWith('other.yaml: ovdb.yaml: deployment.url ') && problem.includes(' sits under '))) return 'over';
  return 'apart';
};

// fns[name][reference] is the verdict function of a reference for a Go function.
const recordsetPageTemplate = 'https://cloud.openvaultdb.com/ovdb/dbs/chinook/collections/{name}';
const fns = {
  url: {
    directory: (v) => urls.publicHttpsProblem(v) === null,
    chinookdb: (v) => chinook.manifestUrlProblem(v) === null,
  },
  'url-template': {
    directory: (v) => urls.publicHttpsProblem(v, { template: true }) === null,
    chinookdb: (v) => chinook.manifestUrlProblem(v, { template: true }) === null,
  },
  homepage: {
    directory: (v) => urls.homepageProblem(v) === null,
    chinookdb: (v) => chinook.homepageFieldProblem(v) === null,
  },
  id: {
    directory: (v) => !directory.recordProblems({ databases: [{ key: v, file: recordFile, data: {} }], maintainers: [] }).some((problem) => problem.startsWith(`${recordFile}: id "`)),
    chinookdb: (v) => chinook.idPattern.test(v) && v.length <= chinook.maxIdLength,
  },
  commit: { directory: (v) => gitlib.commitPattern.test(v) },
  // The local id of a database descriptor: localIdPattern is a constant of directory.mjs that is not exported, copied here (and anchored above).
  'local-id': { directory: (v) => /^[a-z][a-z0-9-]{0,39}$/.test(v) },
  repository: { directory: (v) => gitlib.repositoryKey(v) !== null },
  path: {
    directory: (v) => gitlib.isRepositoryPath(v),
    chinookdb: (v) => chinook.isRepositoryPath(v),
  },
  // The Directory checks a publish entry inline in analyseDatabase (directory.mjs, "OVDB.md: publish entry ...
  // must be an explicit path starting with ./"): the same expression, on the Directory's own isRepositoryPath.
  publish: {
    directory: (v) => v.trim() !== '' && v.startsWith('./') && gitlib.isRepositoryPath(v.slice(2)),
  },
  // directory.mjs's isText, the "is required" test of every text field: not blank by JavaScript's trim().
  text: { directory: (v) => typeof v === 'string' && v.trim() !== '' },
  engine: {
    directory: (v) => manifestAccepts((m) => { m.deployment.engine = v; }, 'deployment.engine'),
    chinookdb: (v) => chinook.enginePattern.test(v),
  },
  licence: { directory: (v) => manifestAccepts((m) => { m.licences.data = v; }, 'licences.data') },
  // A global database identity (globalDatabaseIdProblem, 574a7ad and d089fa8): the Directory's own function.
  'global-id': { directory: (v) => urls.globalDatabaseIdProblem(v) === null },
  // The Directory takes a recordset as the database names it (nativeRecordsetNameProblem, an inline rule of manifestProblems), and writes its page URL from
  // the name as one encoded path segment (encodePathSegment, and publicHttpsProblem's encodedPathSegment, in analyseDatabase). Both are the Directory's own code.
  'recordset-name': {
    directory: (v) => !directory.manifestProblems({ ...baseManifest(), recordsets: [v] }).some((problem) => problem.startsWith('recordsets ')),
  },
  'recordset-page': {
    directory: (v) => {
      const encoded = urls.encodePathSegment(v);
      return urls.publicHttpsProblem(recordsetPageTemplate.replace('{name}', encoded), { encodedPathSegment: encoded.includes('%') ? encoded : undefined }) === null;
    },
  },
};
const references = ['directory', 'chinookdb'];
let thrown = 0;
const verdict = (fn, reference, input) => {
  const check = fns[fn][reference];
  if (!check) return '-';
  try { return check(input) ? '1' : '0'; } catch { thrown += 1; return '0'; }
};
const verdicts = (fnList, input) => fnList.flatMap((fn) => references.map((reference) => verdict(fn, reference, input))).join('');

// ---- encoding of inputs ----

const rep = (prefix, unit, count, suffix = '') => ({ r: [prefix, unit, count, suffix] });
const expand = (input) => (typeof input === 'string' ? input : input.r[0] + input.r[1].repeat(input.r[2]) + input.r[3]);
const ranges = (codes) => {
  const out = [];
  for (const code of codes) {
    const last = out.at(-1);
    if (last && last[1] === code - 1) last[1] = code; else out.push([code, code]);
  }
  return out;
};

// ---- sweeps: one character, U+0000 to U+FFFF, put into a template at {C} ----

const wide = [[0, 0xffff]];
const narrow = [[0, 0x24f], [0xd7ff, 0xe000], [0xff00, 0xffff]];
// Without the surrogates: encodeURIComponent throws on a lone one, and no manifest can hold one (the reader refuses an unpaired \\ud800 escape).
const scalar = [[0, 0xd7ff], [0xe000, 0xffff]];
const sweepRanges = { wide, narrow, scalar };
const codesOf = (spans) => spans.flatMap(([from, to]) => Array.from({ length: to - from + 1 }, (_, at) => from + at));
const sweepSpecs = [];
const urlSweep = (name, fn, template) => sweepSpecs.push({ name, fn, span: 'wide', template });
// The Directory's own sweep (host and path, plain and template) and the places it does not look.
urlSweep('url-host', 'url', 'https://a{C}b.openvaultdb.com/x');
urlSweep('url-host-first', 'url', 'https://{C}ab.openvaultdb.com/x');
urlSweep('url-host-last', 'url', 'https://ab{C}.openvaultdb.com/x');
urlSweep('url-host-tld', 'url', 'https://openvaultdb.c{C}m/x');
urlSweep('url-after-host', 'url', 'https://e.openvaultdb.com{C}/x');
urlSweep('url-path', 'url', 'https://e.openvaultdb.com/a{C}b');
urlSweep('url-before-scheme', 'url', '{C}https://e.openvaultdb.com/x');
urlSweep('url-after-end', 'url', 'https://e.openvaultdb.com/x{C}');
urlSweep('template-host', 'url-template', 'https://a{C}b.openvaultdb.com/{name}');
urlSweep('template-path-after', 'url-template', 'https://e.openvaultdb.com/a{C}b/{name}');
urlSweep('template-path-before', 'url-template', 'https://e.openvaultdb.com/{name}a{C}b');
urlSweep('template-before-scheme', 'url-template', '{C}https://e.openvaultdb.com/{name}');
urlSweep('template-after-end', 'url-template', 'https://e.openvaultdb.com/{name}{C}');
urlSweep('template-between', 'url-template', 'https://e.openvaultdb.com/{na{C}me}');
urlSweep('homepage-host', 'homepage', 'https://a{C}b.openvaultdb.com/x');
urlSweep('homepage-path', 'homepage', 'https://e.openvaultdb.com/a{C}b');
urlSweep('homepage-after-end', 'homepage', 'https://e.openvaultdb.com/x{C}');
urlSweep('gid-host', 'global-id', 'https://a{C}b.openvaultdb.com/x/');
urlSweep('gid-host-example', 'global-id', 'https://a{C}b.example/x/');
urlSweep('gid-path', 'global-id', 'https://e.openvaultdb.com/a{C}b/');
urlSweep('gid-escape-first', 'global-id', 'https://e.openvaultdb.com/a%{C}0/');
urlSweep('gid-escape-second', 'global-id', 'https://e.openvaultdb.com/a%2{C}/');
urlSweep('gid-escape-char', 'global-id', 'https://e.openvaultdb.com/a%C3%A{C}/');
urlSweep('gid-after-end', 'global-id', 'https://e.openvaultdb.com/x/{C}');
urlSweep('gid-before-scheme', 'global-id', '{C}https://e.openvaultdb.com/x/');
// The smaller sweeps of the other rules.
const smallSweep = (name, fn, template) => sweepSpecs.push({ name, fn, span: 'narrow', template });
const smallSweepWide = (name, fn, template) => sweepSpecs.push({ name, fn, span: 'wide', template });
for (const [name, template] of [['id-middle', 'a{C}b'], ['id-first', '{C}a'], ['id-last', 'a{C}'], ['id-alone', '{C}']]) smallSweep(name, 'id', template);
for (const [name, template] of [['text-alone', '{C}'], ['text-lead', '{C}a'], ['text-trail', 'a{C}'], ['text-between', ' {C} ']]) smallSweepWide(name, 'text', template);
for (const [name, template] of [['recordset-name-middle', 'a{C}b'], ['recordset-name-first', '{C}a'], ['recordset-name-last', 'a{C}'], ['recordset-name-alone', '{C}']]) smallSweepWide(name, 'recordset-name', template);
for (const [name, template] of [['recordset-page-middle', 'a{C}b'], ['recordset-page-first', '{C}a'], ['recordset-page-alone', '{C}']]) sweepSpecs.push({ name, fn: 'recordset-page', span: 'scalar', template });
const forty = 'a'.repeat(40);
smallSweep('commit-first', 'commit', `{C}${forty.slice(1)}`);
smallSweep('commit-last', 'commit', `${forty.slice(1)}{C}`);
smallSweep('commit-extra', 'commit', `${forty}{C}`);
smallSweep('repository-host', 'repository', 'https://git{C}ub.com/org/repo');
smallSweep('repository-org', 'repository', 'https://github.com/o{C}g/repo');
smallSweep('repository-repo', 'repository', 'https://github.com/org/r{C}o');
smallSweep('repository-end', 'repository', 'https://github.com/org/repo{C}');
smallSweep('repository-before', 'repository', '{C}https://github.com/org/repo');
for (const [name, template] of [['path-middle', 'a{C}b/c.yaml'], ['path-first', '{C}a'], ['path-last', 'a{C}'], ['path-segment', 'a/{C}']]) smallSweep(name, 'path', template);
for (const [name, template] of [['publish-middle', './a{C}b'], ['publish-dot', '.{C}/a'], ['publish-first', './{C}']]) smallSweep(name, 'publish', template);
for (const [name, template] of [['local-id-middle', 'a{C}b'], ['local-id-first', '{C}a'], ['local-id-last', 'a{C}'], ['local-id-alone', '{C}']]) smallSweep(name, 'local-id', template);
for (const [name, template] of [['engine-middle', 'a{C}b'], ['engine-first', '{C}a'], ['engine-last', 'a{C}']]) smallSweep(name, 'engine', template);
for (const [name, template] of [['licence-middle', 'a{C}b'], ['licence-first', '{C}a'], ['licence-last', 'a{C}']]) smallSweep(name, 'licence', template);

const sweeps = sweepSpecs.map(({ name, fn, span, template }) => {
  const accepted = Object.fromEntries(references.filter((reference) => fns[fn][reference]).map((reference) => [reference, []]));
  for (const code of codesOf(sweepRanges[span])) {
    const input = template.replace('{C}', () => String.fromCharCode(code));
    for (const reference of Object.keys(accepted)) if (verdict(fn, reference, input) === '1') accepted[reference].push(code);
  }
  for (const reference of Object.keys(accepted)) accepted[reference] = ranges(accepted[reference]);
  return { name, fn, span, template, accepted };
});

// ---- products: every string of an alphabet, between two lengths, in a template at {S} ----

const productSpecs = [
  // hosts whose last label is numeric, hex-looking or odd, and punycode-looking labels
  { name: 'host-tail', fn: 'url', template: 'https://a.{S}/x', alphabet: '0x9fa-.', min: 1, max: 4 },
  { name: 'host-tail-template', fn: 'url-template', template: 'https://a.{S}/{name}', alphabet: '0x9fa-.', min: 1, max: 3 },
  { name: 'host-only', fn: 'url', template: 'https://{S}/x', alphabet: '01x.a', min: 1, max: 5 },
  { name: 'xn-short', fn: 'url', template: 'https://xn--{S}.openvaultdb.com/x', alphabet: 'abcdefghijklmnopqrstuvwxyz0123456789-', min: 1, max: 2 },
  { name: 'xn-tail', fn: 'url', template: 'https://a.xn--{S}/x', alphabet: 'a9-k', min: 1, max: 5 },
  { name: 'path-shape', fn: 'url', template: 'https://e.openvaultdb.com/{S}', alphabet: 'a./-~', min: 0, max: 5 },
  { name: 'id-shape', fn: 'id', template: '{S}', alphabet: 'a0-A_', min: 0, max: 5 },
  { name: 'path-segments', fn: 'path', template: '{S}', alphabet: 'a./-~', min: 1, max: 5 },
  { name: 'publish-segments', fn: 'publish', template: './{S}', alphabet: 'a./-', min: 0, max: 5 },
  { name: 'repository-segments', fn: 'repository', template: 'https://github.com/{S}', alphabet: 'a./-g', min: 1, max: 5 },
  { name: 'local-id-shape', fn: 'local-id', template: '{S}', alphabet: 'a0-A_', min: 0, max: 5 },
  { name: 'engine-shape', fn: 'engine', template: '{S}', alphabet: 'a9-.+_/', min: 0, max: 4 },
  { name: 'licence-shape', fn: 'licence', template: '{S}', alphabet: 'a9-.+_/', min: 0, max: 4 },
  { name: 'gid-path-shape', fn: 'global-id', template: 'https://e.example/{S}', alphabet: 'a%2eC3/', min: 0, max: 5 },
  { name: 'gid-escape-shape', fn: 'global-id', template: 'https://e.openvaultdb.com/{S}/', alphabet: '%25aeF.', min: 0, max: 5 },
  { name: 'recordset-name-shape', fn: 'recordset-name', template: '{S}', alphabet: 'a. /\\\t', min: 0, max: 4 },
  { name: 'recordset-page-shape', fn: 'recordset-page', template: '{S}', alphabet: 'a.%2F5c ~', min: 0, max: 5 },
];
function* strings(alphabet, min, max) {
  const letters = [...alphabet];
  for (let length = min; length <= max; length += 1) {
    const digits = new Array(length).fill(0);
    for (;;) {
      yield digits.map((digit) => letters[digit]).join('');
      let at = length - 1;
      while (at >= 0 && digits[at] === letters.length - 1) { digits[at] = 0; at -= 1; }
      if (at < 0) break;
      digits[at] += 1;
    }
  }
}
const products = productSpecs.map((spec) => {
  const verdictFns = references.filter((reference) => fns[spec.fn][reference]);
  const accepted = Object.fromEntries(verdictFns.map((reference) => [reference, []]));
  const refused = Object.fromEntries(verdictFns.map((reference) => [reference, []]));
  let size = 0;
  for (const text of strings(spec.alphabet, spec.min, spec.max)) {
    const input = spec.template.replace('{S}', () => text);
    size += 1;
    for (const reference of verdictFns) (verdict(spec.fn, reference, input) === '1' ? accepted : refused)[reference].push(text);
  }
  // Each reference's verdicts are kept as the smaller of the two lists: the strings it accepts, or the strings it refuses.
  const stored = Object.fromEntries(verdictFns.map((reference) => (accepted[reference].length <= refused[reference].length
    ? [reference, { accepts: accepted[reference] }] : [reference, { refuses: refused[reference] }])));
  return { ...spec, size, stored };
});

// ---- the explicit lists ----

// Every string literal of both test suites that is written as a URL: the hand-written cases of both, as they are.
const literalsOf = (file) => {
  const text = readFileSync(file, 'utf8');
  const found = new Set();
  for (const match of text.matchAll(/'((?:[^'\\\n]|\\.)*)'|"((?:[^"\\\n]|\\.)*)"/g)) {
    const body = match[1] ?? match[2];
    if (!/^(https?|ftp|file|javascript|data|ssh|git|meaning|modelspec):|^git@|^--|^-o/i.test(body)) continue;
    try { found.add(new Function(`return ${match[0]}`)()); } catch { /* a quote inside a comment: not a literal */ }
  }
  return [...found];
};
const suites = {
  directory: join(directoryRoot, 'scripts/test.mjs'),
  fixtures: join(fixtureRoot, 'scripts/test-model.mjs'),
};
const mined = Object.fromEntries(Object.entries(suites).map(([name, file]) => [name, literalsOf(file)]));

const b = 'openvaultdb.com';
const label = (length, char = 'a') => char.repeat(length);
const hostOf = (length) => { // a host of exactly `length` characters: labels of at most 63, then com
  const labels = [];
  let left = length - 3;
  while (left > 0) {
    const take = left - 64 === 1 ? 62 : Math.min(63, left - 1);
    labels.push(label(take));
    left -= take + 1;
  }
  const host = `${labels.join('.')}${labels.length ? '.' : ''}com`;
  if (host.length !== length) throw new Error(`hostOf(${length}) made ${host.length}`);
  return host;
};
const structured = [
  // schemes and shape
  '', ' ', 'https://', 'https:///x', 'https:////e.openvaultdb.com/x', `http://e.${b}/x`, `HTTPS://e.${b}/x`, `Https://e.${b}/x`, `https:/e.${b}/x`, `https:e.${b}/x`, `e.${b}/x`, `//e.${b}/x`,
  `ftp://e.${b}/x`, `file:///etc/passwd`, `javascript:alert(1)`, `wss://e.${b}/x`, `https://e.${b}`, `https://e.${b}/`, `https://e.${b}/ `, ` https://e.${b}/`, `https://e.${b}/\n`, `\nhttps://e.${b}/`, `https://e.${b}/\t`,
  // userinfo, ports, query, fragment
  `https://u@e.${b}/x`, `https://u:p@e.${b}/x`, `https://@e.${b}/x`, `https://:@e.${b}/x`, `https://e.${b}@e.${b}/x`, `https://e.${b}\\@evil.com/x`, `https://e.${b}:443/x`, `https://e.${b}:8443/x`, `https://e.${b}:0/x`, `https://e.${b}:/x`,
  `https://e.${b}:65536/x`, `https://e.${b}:99999999999/x`, `https://e.${b}:abc/x`, `https://e.${b}:443`, `https://e.${b}:/`, `https://e.${b}/x?`, `https://e.${b}/x?a=1`, `https://e.${b}/x#`, `https://e.${b}/x#a`, `https://e.${b}?a`, `https://e.${b}#a`,
  // hosts: case, dots, hyphens, characters
  `https://E.${b}/x`, `https://e.${b.toUpperCase()}/x`, `https://e.${b}./x`, `https://.e.${b}/x`, `https://e..${b}/x`, `https://e.${b}../x`, `https://-e.${b}/x`, `https://e-.${b}/x`, `https://e.-${b}/x`, `https://e.${b}-/x`, `https://e--e.${b}/x`,
  `https://e_e.${b}/x`, `https://e e.${b}/x`, `https://e%65.${b}/x`, `https://%65.${b}/x`, `https://e.${b}%2f/x`, `https://b\u00fccher.example.org/x`, `https://\u00fc.${b}/x`, `https://e.${b}\u3002/x`, `https://e\u3002${b}/x`, `https://e\uff0e${b}/x`,
  `https://e\u00ad.${b}/x`, `https://e\u200b.${b}/x`, `https://e\ufeff.${b}/x`, `https://\uff45.${b}/x`, `https://[::1]/x`, `https://[::ffff:7f00:1]/x`, `https://[e.${b}]/x`, `https://e.${b}[/x`,
  // IP addresses and numeric last labels
  ...['127.0.0.1', '127.1', '1', '0', '00', '08', '09', '0x7f.1', '0x7f.0.0.1', '0X7F.1', '2130706433', '017700000001', '4294967296', '4294967295', '256.256.256.256', '1.2.3.4.5', '1.2.3.4.', '1e3', '1e3.com', '0b1', '0o7', '0x', '0xg', '0xff', '0x1.5', '8.8.8.8', '10.0.0.1', '169.254.169.254', '100.64.0.1', '192.168.1.1', '172.16.0.1']
    .map((host) => `https://${host}/x`),
  ...['0', '1', '00', '01', '08', '09', '10', '255', '256', '4294967296', '99999999999999999999', '0x', '0x0', '0x1', '0xf', '0xff', '0xfF', '0xg', '0x1g', '0xx', 'x0', '0a', 'a0', '1a', 'a1', '1e3', '0b1', '0o7', '12345678901234567890', '1-', '-1', '0x-1']
    .flatMap((last) => [`https://a.${last}/x`, `https://a.b.${last}/x`, `https://1.2.3.${last}/x`, `https://a.${last}./x`, `https://${last}.a/x`, `https://${last}.${last}/x`]),
  // single labels and reserved zones
  `https://localhost/x`, `https://LOCALHOST/x`, `https://localhost./x`, `https://nas/x`, `https://e/x`, `https://com/x`,
  ...['localhost', 'local', 'internal', 'localdomain', 'lan', 'home.arpa', 'arpa', 'intranet', 'corp', 'private', 'svc', 'home', 'test', 'example', 'invalid', 'onion', 'onions', 'locale', 'internals', 'in-addr.arpa', 'arpa.com', 'home.arpa.com', 'example.com', 'example.org', 'a.example.com', 'test.com', 'local.com', 'corp.com', 'svc.com']
    .flatMap((zone) => [`https://x.${zone}/x`, `https://${zone}/x`, `https://x.${zone}.${zone}/x`]),
  // punycode, valid and not
  `https://xn--bcher-kva.de/ovdb`, `https://xn--bcher-kva.de/x`, `https://xn--bcher-kva.-x.com/x`, `https://xn--/x`, `https://xn--.${b}/x`, `https://xn---.${b}/x`, `https://xn--a.${b}/x`, `https://xn--a-.${b}/x`, `https://XN--bcher-kva.de/x`, `https://xn--BCHER-KVA.de/x`,
  `https://xn--bcher-kvb.de/x`, `https://xn--bcher-.de/x`, `https://xn--bcher-kva`, `https://xn--bcher-kva./x`, `https://a.xn--bcher-kva/x`, `https://xn--80ak6aa92e.com/x`, `https://xn--fiq228c.com/x`, `https://xn--ls8h.la/x`, `https://xn--n3h.com/x`,
  `https://xn--ab-ia.${b}/x`, `https://xn--ab-ja.${b}/x`, `https://xn--zca.${b}/x`, `https://xn--dca.${b}/x`, `https://xn--a-jia.${b}/x`,
  // labels and hosts at their limits
  `https://${label(1)}.${b}/x`, `https://${label(62)}.${b}/x`, `https://${label(63)}.${b}/x`, `https://${label(64)}.${b}/x`, `https://${label(63)}.${label(63)}.${label(63)}.${label(61)}/x`, `https://${hostOf(252)}/x`, `https://${hostOf(253)}/x`, `https://${hostOf(254)}/x`, `https://${hostOf(255)}/x`,
  rep('https://', 'a', 253, '/x'), rep('https://', 'a', 63, '.b.c/x'), `https://a.${label(63)}/x`, `https://a.${label(64)}/x`,
  // paths
  `https://e.${b}/a/`, `https://e.${b}/a//b`, `https://e.${b}//a`, `https://e.${b}//`, `https://e.${b}/./a`, `https://e.${b}/a/./b`, `https://e.${b}/a/.`, `https://e.${b}/.`, `https://e.${b}/..`, `https://e.${b}/a/..`, `https://e.${b}/../a`, `https://e.${b}/a/../b`,
  `https://e.${b}/...`, `https://e.${b}/.a`, `https://e.${b}/a.`, `https://e.${b}/a../b`, `https://e.${b}/.well-known/openvaultdb`, `https://e.${b}/~me/x_y-z.html`, `https://e.${b}/A/b_C~d.e/f-g`,
  `https://e.${b}/%2e`, `https://e.${b}/%2E%2e/x`, `https://e.${b}/%2F`, `https://e.${b}/a%2fb`, `https://e.${b}/a%20b`, `https://e.${b}/a b`, `https://e.${b}/a\\b`, `https://e.${b}/a\\.\\b`, `https://e.${b}/a%5cb`, `https://e.${b}/%`, `https://e.${b}/%zz`,
  `https://e.${b}/a"b`, `https://e.${b}/a'b`, `https://e.${b}/a&b`, `https://e.${b}/a<b>`, `https://e.${b}/a(b)`, `https://e.${b}/a,b`, `https://e.${b}/a;b`, `https://e.${b}/a=b`, `https://e.${b}/a@b`, `https://e.${b}/a:b`, `https://e.${b}/a+b`, `https://e.${b}/a$b`, `https://e.${b}/a!b`, `https://e.${b}/a*b`, `https://e.${b}/a[b]`, `https://e.${b}/a|b`, `https://e.${b}/a^b`, `https://e.${b}/a\`b`,
  `https://e.${b}/\u00e9`, `https://e.${b}/\u0430`, `https://e.${b}/a\u202eb`, `https://e.${b}/\u0000`, `https://e.${b}/\u007f`, `https://e.${b}/\u0085`, `https://e.${b}/\u00a0`,
  // templates
  `https://e.${b}/{name}`, `https://e.${b}/a/{name}`, `https://e.${b}/a/{name}/b`, `https://e.${b}/a{name}b`, `https://e.${b}/{name}{name}`, `https://e.${b}/{name}/{name}`, `https://e.${b}/{name`, `https://e.${b}/name}`, `https://e.${b}/{Name}`, `https://e.${b}/{NAME}`, `https://e.${b}/{ name }`,
  `https://e.${b}/{}`, `https://e.${b}/{{name}}`, `https://e.${b}/{name}}`, `https://e.${b}/.{name}`, `https://e.${b}/..{name}`, `https://e.${b}/{name}.`, `https://e.${b}/{name}/`, `https://e.${b}/{name}/..`, `https://e.${b}//{name}`, `https://e.${b}/{name}//`, `https://e.${b}/{name}?`, `https://e.${b}/{name}#`,
  `https://{name}.${b}/x`, `https://e.{name}/x`, `https://e.${b}{name}/x`, `https://e.${b}:{name}/x`, `https://{name}@e.${b}/x`, `https://{name}`, `https://{name}/`, `https://e.${b}{name}`, `{name}`, `{name}https://e.${b}/x`, `https://e.${b}/x{name}`, `https://169.254.169.{name}/latest`, `https://metadata.google.{name}/x`,
  // lengths: homepage at 199, 200, 201 and URLs at their bound
  rep(`https://e.${b}/`, 'a', 200 - `https://e.${b}/`.length), rep(`https://e.${b}/`, 'a', 199 - `https://e.${b}/`.length), rep(`https://e.${b}/`, 'a', 201 - `https://e.${b}/`.length), rep(`https://e.${b}/`, 'a', 100000 - `https://e.${b}/`.length),
  rep(`https://e.${b}/`, 'a', 2047 - `https://e.${b}/`.length), rep(`https://e.${b}/`, 'a', 2048 - `https://e.${b}/`.length), rep(`https://e.${b}/`, 'a', 2049 - `https://e.${b}/`.length), rep(`https://e.${b}/`, 'a', 4096 - `https://e.${b}/`.length), rep(`https://e.${b}/`, 'a/', 1500),
  rep(`https://e.${b}/`, '.', 1500), rep(`https://e.${b}/`, 'a', 300, '/{name}'), rep(`https://e.${b}/{name}/`, 'a', 3000),
];
const urlFns = ['url', 'url-template', 'homepage'];
const urlLike = [...new Set([...structured, ...mined.directory, ...mined.fixtures])];

// RFC 3492 encoder, only to spell the labels whose decoded text has the shapes a rule is about (the verdicts are the
// references', never this function's).
const punycodeEncode = (text) => {
  const input = [...text].map((c) => c.codePointAt(0));
  const digit = (d) => String.fromCharCode(d < 26 ? 97 + d : 22 + d);
  const adapt = (delta, points, first) => {
    let d = first ? Math.floor(delta / 700) : delta >> 1;
    d += Math.floor(d / points);
    let k = 0;
    while (d > 455) { d = Math.floor(d / 35); k += 36; }
    return k + Math.floor((36 * d) / (d + 38));
  };
  let out = input.filter((c) => c < 0x80).map((c) => String.fromCharCode(c)).join('');
  const basic = out.length;
  let handled = basic;
  if (basic > 0) out += '-';
  let n = 128; let delta = 0; let bias = 72;
  while (handled < input.length) {
    const next = Math.min(...input.filter((c) => c >= n));
    delta += (next - n) * (handled + 1);
    n = next;
    for (const c of input) {
      if (c < n) delta += 1;
      if (c !== n) continue;
      let q = delta;
      for (let k = 36; ; k += 36) {
        const t = Math.min(Math.max(k - bias, 1), 26);
        if (q < t) break;
        out += digit(t + ((q - t) % (36 - t)));
        q = Math.floor((q - t) / (36 - t));
      }
      out += digit(q);
      bias = adapt(delta, handled + 1, handled === basic);
      delta = 0;
      handled += 1;
    }
    delta += 1;
    n += 1;
  }
  return out;
};

// Latin-1 and other letters as punycode, spelled by Node's own encoder: the labels that the references really see.
const punycodeCases = [];
const idn = (text) => { const ascii = domainToASCII(text); return ascii && ascii.startsWith('xn--') ? ascii : null; };
const latin1 = Array.from({ length: 0x100 - 0xa0 }, (_, at) => String.fromCharCode(0xa0 + at));
for (const letter of latin1) {
  for (const text of [`a${letter}b`, `${letter}a`, `a${letter}`, letter, `${letter}${letter}`, `${letter}-${letter}`, `a-${letter}`, `${letter}-a`]) { const ascii = idn(text); if (ascii) punycodeCases.push(ascii); }
}
for (const first of latin1) for (const second of latin1) { const ascii = idn(`${first}${second}`); if (ascii && (first.charCodeAt(0) * 31 + second.charCodeAt(0)) % 23 === 0) punycodeCases.push(ascii); }
for (let code = 0x100; code <= 0xffff; code += code < 0x600 ? 7 : 251) { const ascii = idn(`a${String.fromCharCode(code)}b`); if (ascii) punycodeCases.push(ascii); }
for (const text of ['bücher', 'münchen', 'ñandú', 'çà', 'ÿ', 'ß', 'straße', 'ǆ', 'ω', 'я', '日本', 'ａ', 'a\u0308', 'u\u0308', 'ü\u0308', '\u0308a', 'a\u200db', 'a\u200cb', 'a\u00adb', 'à-', '-à', 'à.à', 'Ü', 'ÀB', 'aß', 'ǰ', 'ŉ', 'ſ', 'ĸ']) { const ascii = idn(text); if (ascii) punycodeCases.push(ascii); }
// Labels whose decoded text begins with xn-- or has hyphens in its third and fourth positions, with Latin-1 and other
// letters, and the hyphen shapes around them.
const nestedCases = ['xn--ü', 'xn--a', 'xn--', 'ab--ü', 'ab--c-ü', 'a--ü', 'ü--a', 'ü-a', '-ü', 'ü-', 'xn--ł', 'ab--ł', 'xn--я', 'abc--ü', 'a-b-ü'];
for (const letter of latin1) for (const shape of [`xn--${letter}`, `ab--${letter}`, `a--${letter}`, `${letter}--ab`, `${letter}a--b`, `x${letter}--b`]) nestedCases.push(shape);
for (const text of nestedCases) punycodeCases.push(`xn--${punycodeEncode(text)}`);
// Spelled as the review of this slice found them.
punycodeCases.push('xn--xn---3na', 'xn--xn--a-esa');
const punycodeInputs = [...new Set(punycodeCases)].flatMap((ascii) => [`https://${ascii}.${b}/x`, `https://a.${ascii}/x`]);

const textList = {
  fns: ['text'],
  cases: [
    '', ' ', 'a', ' a ', '\t\n\v\f\r ', '\u0085', '\ufeff', '\u180e', '\u200b', '\u200c', '\u200d', '\u2060', '\u00a0', '\u00a0x', '\u1680', '\u2000', '\u200a', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000',
    ' \ufeff\u3000\n', '\ufeff\u0085', '\u0085 ', '0', '-', '\u0000', '\u00ad', 'é', '\ud800', ' \ud800', 'a\u0085',
  ].map((input) => [input, verdicts(['text'], input)]),
};

const nameFns = ['recordset-name', 'recordset-page'];
const astral = '\u{1f600}';
const recordsetList = {
  fns: nameFns,
  cases: [
    'Album', 'dbo.DatabaseLog', 'Order Details', 'a b', ' a', 'a ', '', ' ', '.', '..', '...', 'a.', '.a', 'a..b', 'a/b', '/a', 'a/', 'a\\b', 'a\tb', 'a\nb', 'a\u0000b', 'a\u001fb', 'a\u007fb', 'a\u0080b',
    '\u00e9', 'caf\u00e9', '\u0430', '\u0085', '\ufeff', 'a\u00a0b', '\u00a0', 'a%b', 'a%2Fb', 'a%2fb', 'a%252Fb', 'a%252F', '%2e', '%2E%2E', '%252e', '%25252e', '%252e%252e', '%2', '%', '%zz', '%41', '%41%', '%C3', '%C3%A9', '%FF', '%c0%80',
    '%ED%A0%80', '%252', '%25%', '%2525', '%2F', '%5C', '%00', '%0a', '%7F', "a'b", 'a(b)', 'a!b', 'a*b', 'a~b', 'a_b', 'a-b', 'a&b', 'a"b', 'a<b>', 'a?b', 'a#b', 'a:b', 'a@b', 'a{b}', '{name}', 'a{name}b',
    '1a', 'a-b', 'A', 'a'.repeat(255), 'a'.repeat(256), 'a'.repeat(257), 'a'.repeat(1000), astral, astral.repeat(127), astral.repeat(128), astral.repeat(129), `${'a'.repeat(254)}${astral}`, `${'a'.repeat(255)}${astral}`,
    'a\u2028b', 'a\u2029b', 'Dbo.Table 1', 'x'.repeat(200),
    // pages over 2048 characters, which the Directory takes (it bounds the name, not the page): the length is no kind of the page family
    '\u20ac'.repeat(256), '\u20ac'.repeat(257), '\u8868'.repeat(230), '\u00e9'.repeat(400), 'a'.repeat(2100), '%'.repeat(1000), 'a%2Fb'.repeat(500),
  ].map((input) => [input, verdicts(nameFns, input)]),
};
const globalIdHosts = ['example', 'x.example', 'x.EXAMPLE', 'a.b.example', 'x.example.', 'x.examples', 'xexample', 'x.test', 'x.localhost', 'x.local', 'x.internal', 'ovdb.example', 'x.example.com', 'x.com.example', 'demodb.dev', 'localhost', '127.0.0.1', 'xn--bcher-kva.de'];
const globalIdPaths = ['', '/', '/sakila', '/sakila/', '/a/b/', '/a/b', '/a//b/', '//', '/./', '/../', '/.../', '/order%20details/', '/order%20details', '/%41/', '/%61/', '/%2f/', '/%2F/', '/%5C/', '/%5c/', '/%00/', '/%1f/', '/%7F/', '/%7f/', '/%2e/', '/%2E/', '/%2e%2e/', '/.%2e/', '/%252e/', '/%252e%252e/', '/%252F/', '/%2525/', '/%25/', '/%2/', '/%/', '/%zz/', '/%C3%A9/', '/%c3%a9/', '/%C3/', '/%FF/', '/%C0%80/', '/%ED%A0%80/', '/%7E/', '/~/', '/%21/', '/!/', '/%27/', '/%28/', '/%2A/', '/*/', '/a%20b/c%20d/', '/a%20b%/', '/a%20b%2/', '/%E2%82%AC/', '/%f0%9f%98%80/', '/%F0%9F%98%80/', '/\u00e9/', '/a b/', '/a\\b/', '/a?b/', '/a#b/', '/a:b/', '/{name}/', '/%7Bname%7D/', '/%25252e/', '/%252e/x/%2e/'];
const globalIdList = {
  fns: ['global-id'],
  cases: [...new Set([...urlLike, ...globalIdHosts.flatMap((host) => globalIdPaths.map((path) => `https://${host}${path}`)), ...globalIdPaths.map((path) => `https://demodb.dev${path}`), 'https://demodb.dev', 'https://demodb.dev/sakila/?x', 'https://demodb.dev:443/sakila/', 'https://u@demodb.dev/sakila/', 'http://demodb.dev/sakila/', rep('https://e.openvaultdb.com/', 'a', 2040, '/'), rep('https://e.openvaultdb.com/', '%20', 700, '/')])].map((input) => [input, verdicts(['global-id'], expand(input))]),
};
const urlList = { fns: urlFns, cases: [...urlLike, ...punycodeInputs].map((input) => [input, verdicts(urlFns, expand(input))]) };
// A string that no reference accepts as a URL still goes through the repository rule below.

const repositoryList = {
  fns: ['repository'],
  cases: [
    ...urlLike.filter((input) => typeof input === 'string' && /^https?:|^git@|^ssh|^file/i.test(input) && input.length < 300),
    'https://github.com/datatug/chinookdb', 'https://github.com/DataTug/ChinookDB', 'https://github.com/a/b', 'https://github.com/a-b/c.d_e', 'https://github.com/a/b.git', 'https://github.com/a/b.GIT', 'https://github.com/a/b.Git', 'https://github.com/a/.git', 'https://github.com/a/git',
    'https://github.com/a/b/', 'https://github.com/a', 'https://github.com/', 'https://github.com', 'https://github.com//b', 'https://github.com/a//b', 'https://github.com/a/b/c', 'https://github.com/a/b/c/d', 'https://github.com/./b', 'https://github.com/a/.', 'https://github.com/../b', 'https://github.com/a/..', 'https://github.com/.../b', 'https://github.com/a/...',
    'https://www.github.com/a/b', 'https://GitHub.com/a/b', 'https://github.com./a/b', 'https://github.com:443/a/b', 'https://user@github.com/a/b', 'https://github.com/a/b?x=1', 'https://github.com/a/b#x', 'https://github.com/a/b c', 'https://github.com/a/b%2e', 'https://github.com/a/b;touch-pwned', 'https://github.com/a/$(touch-pwned)',
    'https://gitlab.com/a/b', 'https://bitbucket.org/a/b', 'https://127.0.0.1/a/b', 'http://github.com/a/b', 'HTTPS://github.com/a/b', 'git@github.com:a/b.git', 'ssh://git@github.com/a/b', 'file:///tmp/x', '--upload-pack=touch pwned', '-ohttps://github.com/a/b', ' https://github.com/a/b', 'https://github.com/a/b ', 'https://github.com/a/b\n', 'https://github.com/a/\u00e9',
    rep('https://github.com/', 'a', 100, '/b'), rep('https://github.com/a/', 'b', 100), rep('https://github.com/a/', 'b', 230), rep('https://github.com/a/', 'b', 231), rep('https://github.com/a/', 'b', 236), rep('https://github.com/a/', 'b', 237), rep('https://github.com/a/', 'b', 5000), rep('https://github.com/', 'a', 300, '/b'),
  ].map((input) => [input, verdicts(['repository'], expand(input))]),
};

const idList = {
  fns: ['id'],
  cases: [
    'chinook', 'chinook-acme', 'a1', '9lives', 'a', '0', '-', 'a-', '-a', 'a--b', 'a-b-c', 'a_b', 'A', 'Chinook', 'a b', ' a', 'a ', 'a\n', 'a.b', 'a/b', 'a:b', 'core"><x', 'a--', '--a', '', 'é', 'a\u0000',
    rep('', 'a', 79), rep('', 'a', 80), rep('', 'a', 81), rep('', 'a', 1000), rep('a', '-a', 40), rep('a', '-a', 39), `${'a'.repeat(78)}-a`, `${'a'.repeat(79)}-`,
  ].map((input) => [input, verdicts(['id'], expand(input))]),
};
const commitList = {
  fns: ['commit'],
  cases: [
    'a'.repeat(40), '0123456789abcdef0123456789abcdef01234567', 'A'.repeat(40), '0123456789ABCDEF0123456789abcdef01234567', 'a'.repeat(39), 'a'.repeat(41), 'a'.repeat(64), '', 'g'.repeat(40), `${'a'.repeat(39)}g`, `${'a'.repeat(39)}\n`, `${'a'.repeat(40)}\n`, ` ${'a'.repeat(39)}`, 'main', 'HEAD', `${'a'.repeat(39)}\u00e9`,
  ].map((input) => [input, verdicts(['commit'], input)]),
};
const localIdFns = ['local-id'];
const localIdList = {
  fns: localIdFns,
  cases: ['chinook', 'a', 'a-', 'a--b', '-a', '1a', 'A', 'a_b', 'a.b', 'a b', '\u00e9', 'a\n', '\na', '', rep('a', 'b', 39), rep('a', 'b', 40), rep('a', '-', 39), rep('', 'a', 41)].map((input) => [input, verdicts(localIdFns, expand(input))]),
};
const pathFns = ['path', 'publish'];
const pathList = {
  fns: pathFns,
  cases: [
    'ovdb.yaml', 'model/chinook.modelspec.hcl', 'a/b/c.yaml', 'a', 'a.b', '.a', 'a.', '...', '.hidden/x', 'a/.hidden', '.github/workflows/x.yml', '', '/', '/a', 'a/', 'a//b', '//a', '.', '..', './', './a', '././a', 'a/.', 'a/./b', 'a/..', 'a/../b', '../a', '../../a', 'a/b/..', '.../a', 'a/...', 'a/..a', 'a/a..',
    'x*.yaml', 'x?.yaml', 'x[a].yaml', 'a b.yaml', 'a\\b', 'a\\', '\\a', 'C:\\a', 'C:/a', 'c:a', 'a:b', ':/a', ':(icase)A', ':!a', '-a', '--help', '~/a', '~a', '$HOME/a', 'a%2fb', 'a%20b', 'a\u00e9', '\u00e9.yaml', 'a\nb', 'a\tb', 'a\u0000b', 'a;b', 'a&b', "a'b", 'a"b', 'a`b',
    './ovdb.yaml', './model/x.hcl', './', './.', './..', './a/', './a//b', './../a', './a/../b', './/a', '.\\a', '/./a', ' ./a', './a ', 'a/./b', './x*.yaml', './é', 'ovdb.yaml/', './ovdb.yaml/',
    rep('', 'a', 1023), rep('', 'a', 1024), rep('', 'a', 1025), rep('', 'a/', 512), rep('', 'a/', 513), rep('./', 'a', 1024), rep('./', 'a', 1025), rep('', '.', 1030),
  ].map((input) => [input, verdicts(pathFns, expand(input))]),
};
const engineFns = ['engine', 'licence'];
const engineList = {
  fns: engineFns,
  cases: [
    'sqlite', 'postgres', 'SQLite3', 'my.engine+x_1-2', 'a', 'A', '9db', '9', 'Cloudflare Workers', '-x', 'x/y', 'x-', 'x+', '+x', '.x', 'x.', 'x_', '_x', '', ' x', 'x ', 'x\n', 'é', 'xé', 'MIT', 'CC0-1.0', 'CC-BY-4.0', 'Apache-2.0', 'BSD-3-Clause', 'GPL-2.0-or-later', 'GPL-3.0-only+', 'LicenseRef-x', 'MIT OR Apache-2.0', 'see the README', 'MTI', 'Foo', '0BSD', '1', '1.0', '-', '+', '.',
    rep('', 'a', 39), rep('', 'a', 40), rep('', 'a', 41), rep('', 'a', 63), rep('', 'a', 64), rep('', 'a', 65), rep('1', 'a', 63), rep('1', 'a', 64), rep('1', 'a', 65), rep('', 'a', 1000),
  ].map((input) => [input, verdicts(engineFns, expand(input))]),
};

// ---- claims: every pair of a list of addresses ----

const claimAddresses = [
  `https://a.${b}/ovdb/dbs/chinook`, `https://A.${b}/OVDB/DBS/CHINOOK`, `https://a.${b}/ovdb/dbs/chinook/`, `https://a.${b}/ovdb/dbs/chinook//`, `https://a.${b}/ovdb/dbs/chinook2`, `https://a.${b}/ovdb/dbs/chinook/x`, `https://a.${b}/ovdb/dbs/chinook/x/y`, `https://a.${b}/ovdb/dbs/chinook/{name}`,
  `https://a.${b}/ovdb/dbs`, `https://a.${b}/ovdb`, `https://a.${b}/`, `https://a.${b}`, `https://b.${b}/ovdb/dbs/chinook`, `https://a.${b}.evil.com/ovdb/dbs/chinook`, `https://xa.${b}/ovdb/dbs/chinook`, `HTTPS://A.${b.toUpperCase()}/OVDB/DBS/CHINOOK/X`,
  `https://a.${b}:8443/ovdb/dbs/chinook`, `https://a.${b}/ovdb/dbs/chinook?x`, `https://a.${b}/ovdb/dbs/chinook#x`, '', '/', '//', 'a', 'a/', 'a/b', 'A/B/', `https://a.${b}/ovdb/dbs/ch\u0131nook`, `https://a.${b}/ovdb/dbs/\u212aelvin`, `https://a.${b}/ovdb/dbs/kelvin`, `https://a.${b}/ovdb/dbs/kelvin/\u00c0`, `https://a.${b}/ovdb/dbs/kelvin/\u00e0`, `https://a.${b}/\u0130`, `https://a.${b}/i\u0307`,
  rep(`https://a.${b}/`, 'a', 2040), rep(`https://a.${b}/`, 'a', 2040, '/x'), rep(`https://a.${b}/`, 'a', 2100), rep(`https://a.${b}/`, 'a', 2100, '/x'),
];
// relations[i][j] is how addresses[i] stands to addresses[j]: a apart, s same, u under, o over.
const claimList = {
  addresses: claimAddresses,
  relations: claimAddresses.map((address) => claimAddresses.map((other) => claimRelation(expand(address), expand(other))[0]).join('')),
};

// ---- the golden ----

const countCases = () => {
  let total = 0;
  for (const sweep of sweeps) total += codesOf(sweepRanges[sweep.span]).length;
  for (const product of products) total += product.size;
  for (const list of [urlList, repositoryList, idList, commitList, pathList, engineList, textList, recordsetList, globalIdList, localIdList]) total += list.cases.length * list.fns.length;
  total += claimAddresses.length * claimAddresses.length;
  return total;
};

const golden = {
  format: 'ovdb-publisher-rules-reference/1',
  generatedBy: 'internal/publisher/rules/testdata/reference/generate.mjs',
  node: process.version,
  references: Object.fromEntries(Object.entries(pins).map(([name, pin]) => [name, { ...pin, files: name === 'directory' ? ['scripts/lib/urls.mjs', 'scripts/lib/git.mjs', 'scripts/lib/directory.mjs'] : name === 'chinookdb' ? ['scripts/lib/directory-rules.mjs'] : ['scripts/test-model.mjs'], testSuite: name === 'directory' ? 'scripts/test.mjs' : name === 'fixtures' ? 'scripts/test-model.mjs' : null, literalsMined: mined[name]?.length ?? 0 }])),
  referenceCalls: { thrown },
  matrixSize: countCases(),
  sweepRanges,
  sweeps,
  products,
  lists: [urlList, repositoryList, idList, commitList, pathList, engineList, textList, recordsetList, globalIdList, localIdList],
  claims: claimList,
};

// One case per line, so that a diff of the golden reads as a diff of cases.
const lineJSON = (value) => JSON.stringify(value);
const text = [
  '{',
  ...Object.entries(golden).filter(([key]) => !['sweeps', 'products', 'lists', 'claims'].includes(key)).map(([key, value]) => `  ${lineJSON(key)}: ${lineJSON(value)},`),
  '  "sweeps": [', golden.sweeps.map((sweep) => `    ${lineJSON(sweep)}`).join(',\n'), '  ],',
  '  "products": [', golden.products.map((product) => `    ${lineJSON(product)}`).join(',\n'), '  ],',
  '  "lists": [',
  golden.lists.map((list) => `    {"fns": ${lineJSON(list.fns)}, "cases": [\n${list.cases.map((entry) => `      ${lineJSON(entry)}`).join(',\n')}\n    ]}`).join(',\n'),
  '  ],',
  `  "claims": {"addresses": [\n${golden.claims.addresses.map((entry) => `    ${lineJSON(entry)}`).join(',\n')}\n  ],\n  "relations": [\n${golden.claims.relations.map((row) => `    ${lineJSON(row)}`).join(',\n')}\n  ]}`,
  '}',
  '',
].join('\n');

if (thrown > 0) console.error(`note: a reference threw on ${thrown} input(s); they are recorded as refused`);
if (process.argv.includes('--check')) {
  if (readFileSync(goldenPath, 'utf8') !== text) { console.error(`${goldenPath} is stale: run node ${process.argv[1]}`); process.exit(1); }
  console.log(`${goldenPath} is up to date (${golden.matrixSize} cases)`);
} else {
  writeFileSync(goldenPath, text);
  console.log(`wrote ${goldenPath}: ${golden.matrixSize} cases, ${(text.length / 1024).toFixed(0)} KiB`);
}
