#!/usr/bin/env node
// Regenerates corpus.json and directory.verdicts.json: a corpus of OVDB.md and manifest documents, and the verdicts
// of the Directory's JavaScript on them.
//
//   node internal/publisher/manifest/testdata/reference/generate.mjs           # rewrite the goldens
//   node internal/publisher/manifest/testdata/reference/generate.mjs --check   # fail if they are stale
//
// The Go tests of internal/publisher/manifest read the goldens and fail if the Go function accepts a document that
// the reference refuses (see README.md in the package). They never run this script: it needs Node 24 or later, git,
// npm and network access, and it is not part of `go test`.
//
// The reference of the Directory profile is openvaultdb/directory at the commit pinned below: `parseFrontmatter`
// and `manifestProblems` of scripts/lib/directory.mjs, imported as they are, and the OVDB.md checks that
// directory.mjs keeps inline in analyseDatabase, composed here from the same expressions (the script fails if the
// pinned file no longer holds them as copied). The Chinook repository at its pinned commit supplies real documents
// and test code to mine; its checker is the reference of the publisher profile, which comes in a later change.
//
// The documents of the corpus are stored as patches (a range of lines replaced) against a few base documents, so
// that the goldens stay small; the Go test applies the same patches. To use clones you already have, pass
// --directory <dir> and --chinookdb <dir> (at the pinned commits; the Directory's with `npm ci --omit=dev` run).
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const corpusPath = join(here, 'corpus.json');
const verdictsPath = join(here, 'directory.verdicts.json');

const pins = {
  directory: { repository: 'openvaultdb/directory', commit: 'e8db5488db31d3f63865e404acef487c33cf35df' },
  chinookdb: { repository: 'datatug/chinookdb', commit: '79e7bb0b1d6f0666dce465874990dec64348331f' },
};

// ---- the references, at the pinned commits ----

const argValue = (name) => { const at = process.argv.indexOf(name); return at === -1 ? undefined : process.argv[at + 1]; };
const run = (cwd, command, args) => execFileSync(command, args, { cwd, stdio: ['ignore', 'pipe', 'inherit'], encoding: 'utf8' }).trim();

function checkout(name) {
  const { repository, commit } = pins[name];
  let dir = argValue(`--${name}`);
  if (dir) {
    dir = resolve(dir);
    if (run(dir, 'git', ['rev-parse', 'HEAD']) !== commit) throw new Error(`${dir} is not at ${repository}@${commit}`);
    if (run(dir, 'git', ['status', '--porcelain', '--untracked-files=no'])) throw new Error(`${dir} has local changes; the references are read as committed`);
    if (name === 'directory' && !existsSync(join(dir, 'node_modules', 'yaml'))) {
      throw new Error(`${dir} has no node_modules/yaml, which the Directory's directory.mjs imports: run \`npm ci --omit=dev --ignore-scripts\` there first`);
    }
    return dir;
  }
  dir = join(process.env.OVDB_REFERENCE_CACHE ?? join(tmpdir(), 'ovdb-publisher-reference'), `${name}-${commit}`);
  if (!existsSync(join(dir, '.git'))) {
    mkdirSync(dir, { recursive: true });
    run(dir, 'git', ['init', '--quiet']);
    run(dir, 'git', ['fetch', '--quiet', '--depth', '1', `https://github.com/${repository}.git`, commit]);
    run(dir, 'git', ['checkout', '--quiet', '--detach', 'FETCH_HEAD']);
  }
  if (run(dir, 'git', ['rev-parse', 'HEAD']) !== commit) throw new Error(`${dir} is not at ${repository}@${commit}; remove it`);
  if (name === 'directory' && !existsSync(join(dir, 'node_modules', 'yaml'))) {
    run(dir, 'npm', ['ci', '--omit=dev', '--ignore-scripts', '--no-audit', '--no-fund']);
  }
  return dir;
}

const directoryRoot = checkout('directory');
const chinookRoot = checkout('chinookdb');
const directory = await import(pathToFileURL(join(directoryRoot, 'scripts/lib/directory.mjs')).href);
const gitlib = await import(pathToFileURL(join(directoryRoot, 'scripts/lib/git.mjs')).href);
const { parse: parseYaml, stringify: stringifyYaml } = createRequire(join(directoryRoot, 'package.json'))('yaml');
const read = (root, file) => readFileSync(join(root, file), 'utf8');

// The inline OVDB.md rules of analyseDatabase, and isText, are copied below: fail loudly if the pinned file differs.
const directorySource = read(directoryRoot, 'scripts/lib/directory.mjs');
for (const expression of [
  "const isText = (value) => typeof value === 'string' && value.trim() !== '';",
  'if (frontmatter.ovdb !== 1) bad(',
  "if (!Array.isArray(frontmatter.publish) || frontmatter.publish.length === 0) { bad('OVDB.md: publish must list at least one manifest path'); return stop(); }",
  "if (!isText(entry) || !entry.startsWith('./') || !isRepositoryPath(entry.slice(2))) bad(",
  "else published.add(entry.slice(2));",
  'if (!published.has(data.manifest)) {',
  'try { manifest = parseYaml(manifestText); } catch (parseError) {',
  'const missing = manifestProblems(manifest);',
]) {
  if (!directorySource.includes(expression)) throw new Error(`directory.mjs at ${pins.directory.commit} no longer holds \`${expression}\`: the verdicts composed from it in generate.mjs are not the Directory's`);
}

// ---- the verdicts of the Directory profile: 1 accepts, 0 refuses ----

process.emitWarning = () => {}; // the yaml package warns on stderr; the Directory ignores it
let thrown = 0;
const isText = (value) => typeof value === 'string' && value.trim() !== '';
const manifestVerdict = (text) => {
  let manifest;
  try { manifest = parseYaml(text); } catch { return 0; } // as the Directory reads it: default options, an error throws
  try { return directory.manifestProblems(manifest).length === 0 ? 1 : 0; } catch { thrown += 1; return 0; }
};
const mdVerdict = (text, path) => {
  try {
    const { data, error } = directory.parseFrontmatter(text);
    if (error) return 0;
    if (data.ovdb !== 1) return 0;
    if (!Array.isArray(data.publish) || data.publish.length === 0) return 0;
    const published = new Set();
    for (const entry of data.publish) {
      if (!isText(entry) || !entry.startsWith('./') || !gitlib.isRepositoryPath(entry.slice(2))) return 0;
      published.add(entry.slice(2));
    }
    return published.has(path) ? 1 : 0;
  } catch { thrown += 1; return 0; }
};

// ---- the bases: real documents, and what is made from them ----

const bases = {};
const addBase = (name, text) => { bases[name] = text; return name; };
const chinookYaml = addBase('chinook-yaml', read(chinookRoot, 'ovdb.yaml'));
const chinookMd = addBase('chinook-md', read(chinookRoot, 'OVDB.md'));
const hosterYaml = addBase('hoster-yaml', read(chinookRoot, 'examples/hoster/ovdb.yaml'));
const hosterMd = addBase('hoster-md', read(chinookRoot, 'examples/hoster/OVDB.md'));
const fixtureYaml = addBase('fixture-yaml', read(directoryRoot, 'scripts/fixtures/chinookdb/ovdb.yaml'));
const fixtureMd = addBase('fixture-md', read(directoryRoot, 'scripts/fixtures/chinookdb/OVDB.md'));
// The hoster example is the shared form; the Directory accepts it (it judges the shape only: the organisation
// `example_org` is for the publisher profile and for the Directory's listing to refuse).
const sharedObject = parseYaml(bases[hosterYaml]);
const sharedYaml = hosterYaml;
const ownObject = parseYaml(bases[chinookYaml]);
const jsonOf = (object) => `${JSON.stringify(object, null, 2)}\n`;
const ownJson = addBase('own-json', jsonOf(ownObject));
const sharedJson = addBase('shared-json', jsonOf(sharedObject));
for (const name of [chinookYaml, fixtureYaml, hosterYaml, ownJson, sharedJson]) {
  if (manifestVerdict(bases[name]) !== 1) throw new Error(`the base ${name} is not accepted by the Directory`);
}
for (const name of [chinookMd, hosterMd, fixtureMd]) if (mdVerdict(bases[name], 'ovdb.yaml') !== 1) throw new Error(`the base ${name} is not accepted by the Directory`);

// ---- the corpus: a document is a base, a patch of whole lines, and flags ----

const lines = (text) => text.split('\n');
const patchOf = (base, text) => { // the smallest range of lines of `base` whose replacement makes `text`
  const a = lines(base); const b = lines(text);
  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start += 1;
  let endA = a.length; let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) { endA -= 1; endB -= 1; }
  return [start, endA - start, ...b.slice(start, endB)];
};
const applyPatch = (base, patch) => { const a = lines(base); a.splice(patch[0], patch[1], ...patch.slice(2)); return a.join('\n'); };
const flagged = (text, flags) => {
  let out = text;
  if (flags.includes('crlf')) out = out.replaceAll('\n', '\r\n');
  if (flags.includes('bom')) out = `﻿${out}`;
  return out;
};

const families = [];
const familyIndex = (name) => { let at = families.indexOf(name); if (at === -1) { families.push(name); at = families.length - 1; } return at; };
const manifestCases = []; // [base, patch, flags, family]
const mdCases = []; // [base, patch, flags, path, family]
const seenManifest = new Set(); const seenMd = new Set();
function addManifest(family, base, text, flags = '') {
  const patch = patchOf(bases[base], text);
  const key = JSON.stringify([base, patch, flags]);
  if (seenManifest.has(key)) return;
  seenManifest.add(key);
  manifestCases.push([base, patch, flags, familyIndex(family)]);
}
function addMd(family, base, text, path = 'ovdb.yaml', flags = '') {
  const patch = patchOf(bases[base], text);
  const key = JSON.stringify([base, patch, flags, path]);
  if (seenMd.has(key)) return;
  seenMd.add(key);
  mdCases.push([base, patch, flags, path, familyIndex(family)]);
}

// The documents as they are, each in several encodings.
for (const name of [chinookYaml, fixtureYaml, hosterYaml, ownJson, sharedJson]) {
  addManifest('base', name, bases[name]);
  for (const flags of ['crlf', 'bom', 'crlf bom']) addManifest('encoding', name, bases[name], flags);
}
for (const name of [chinookMd, hosterMd, fixtureMd]) {
  addMd('base', name, bases[name]);
  for (const flags of ['crlf', 'bom', 'crlf bom']) addMd('encoding', name, bases[name], 'ovdb.yaml', flags);
  for (const path of ['ovdb.yaml', 'other.yaml', 'sub/ovdb.yaml', '', 'OVDB.yaml']) addMd('listed', name, bases[name], path);
}

// ---- object-level mutants, on the JSON form of an own and a shared manifest ----

const whitespace = ['\t', '\n', '\v', '\f', '\r', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', '　', '﻿', '\u0085', '᠎', '​', '‌', '‍', '⁠', '­', '\u0000'];
const wrongTypes = [123, 1.5, true, false, ['a'], { a: 'b' }, [], {}, null, ''];
const clone = (value) => JSON.parse(JSON.stringify(value));
const paths = (object, prefix = []) => Object.entries(object).flatMap(([key, value]) => [[...prefix, key], ...(value !== null && typeof value === 'object' && !Array.isArray(value) ? paths(value, [...prefix, key]) : [])]);
const parentOf = (object, path) => path.slice(0, -1).reduce((value, key) => value[key], object);
const stringLeaves = (object) => paths(object).filter((path) => typeof path.reduce((value, key) => value[key], object) === 'string');

for (const [baseName, object] of [[ownJson, ownObject], [sharedJson, sharedObject]]) {
  const mutate = (family, change) => { const copy = clone(object); change(copy); addManifest(family, baseName, jsonOf(copy)); };
  for (const path of paths(object)) {
    const key = path.at(-1);
    const label = path.join('.');
    mutate('remove', (copy) => { delete parentOf(copy, path)[key]; });
    mutate('rename', (copy) => { const parent = parentOf(copy, path); const entries = Object.entries(parent).map(([k, v]) => [k === key ? `${key}x` : k, v]); for (const k of Object.keys(parent)) delete parent[k]; Object.assign(parent, Object.fromEntries(entries)); });
    for (const value of baseName === ownJson ? wrongTypes : wrongTypes.slice(0, 5)) mutate(`type ${label}`.replace(/ .*/, ' type'), (copy) => { parentOf(copy, path)[key] = value; });
  }
  for (const path of stringLeaves(object)) for (const space of baseName === ownJson ? whitespace : whitespace.slice(0, 12)) {
    mutate('blank', (copy) => { parentOf(copy, path)[path.at(-1)] = space; });
    if (['\t', ' ', '\u00a0', '\u2028', '\ufeff', '\u0085', '\u200b'].includes(space)) mutate('blank', (copy) => { parentOf(copy, path)[path.at(-1)] = ` ${space} `; });
  }
  mutate('unknown key', (copy) => { copy.extra = 'x'; });
  for (const container of paths(object).filter((path) => { const value = path.reduce((v, key) => v[key], object); return value !== null && typeof value === 'object' && !Array.isArray(value); })) {
    mutate('unknown key', (copy) => { container.reduce((v, key) => v[key], copy).extra = 'x'; });
  }
  mutate('recordsets', (copy) => { copy.recordsets = ['A', 'A']; });
  mutate('recordsets', (copy) => { copy.recordsets = ['A', ' ']; });
  mutate('recordsets', (copy) => { copy.recordsets = ['A', 1]; });
  mutate('recordsets', (copy) => { copy.recordsets = 'A'; });
}

// ---- URL fields replaced by refused and odd URLs ----

const literalsOf = (text) => {
  const found = new Set();
  for (const match of text.matchAll(/'((?:[^'\\\n]|\\.)*)'|"((?:[^"\\\n]|\\.)*)"/g)) {
    const body = match[1] ?? match[2];
    if (!/^(https?|ftp|file|javascript|data|ssh|git):/i.test(body)) continue;
    try { found.add(new Function(`return ${match[0]}`)()); } catch { /* a quote in a comment */ }
  }
  return [...found];
};
const suites = [read(directoryRoot, 'scripts/test.mjs'), read(chinookRoot, 'scripts/test-model.mjs')];
const minedUrls = [...new Set(suites.flatMap(literalsOf))].filter((url) => url.length < 400).sort();
const samples = [...minedUrls.filter((_, at) => at % 12 === 0), 'https://example.com/', 'https://acme.io/ovdb/x/', 'https://acme.io/ovdb/{name}', 'https://xn--bcher-kva.de/ovdb/x', 'https://ovdb.xn--bcher-kva.de/x', 'https://a.1/ovdb', 'https://acme.io:443/ovdb/x', 'https://Acme.io/ovdb/x', 'https://acme.io./ovdb/x', 'https://acme.io/ovdb//x', 'https://acme.io/ovdb/./x',
  'https://acme.io/ovdb/%2e', 'https://acme.io/ovdb/é', 'https://acme.io/ovdb/x?a=1', 'https://acme.io/ovdb/x#a', 'https://user@acme.io/ovdb/x', 'http://acme.io/ovdb/x', 'https://ovdb.com/x', 'https://ovdb.co.uk/x', 'https://x.ovdb.acme.co.uk/x', 'https://localhost/ovdb', 'https://' + 'a'.repeat(64) + '.io/ovdb', `https://acme.io/ovdb/${'a'.repeat(2100)}`];
const urlFields = [['url'], ['homepage'], ['deployment', 'url'], ['deployment', 'discovery'], ['deployment', 'recordset_page'], ['publisher', 'url']];
for (const [baseName, object] of [[ownJson, ownObject], [sharedJson, sharedObject]]) {
  for (const path of baseName === ownJson ? urlFields : urlFields.slice(0, 3)) for (const url of samples) {
    const copy = clone(object);
    parentOf(copy, path)[path.at(-1)] = url;
    addManifest('url', baseName, jsonOf(copy));
  }
}

// ---- the forms, mixed in every combination ----

const pinHex = '8c9e62ed6641c0a00faa3867167d928af4c44b06';
const toggles = [
  (o) => { o.model = { ...o.model, modelspec: 'model/x.modelspec.json' }; },
  (o) => { o.model = { ...o.model, hcl: 'model/x.modelspec.hcl' }; },
  (o) => { o.model = { ...o.model, address: `modelspec://github.com/acme/x/chinook?ref=${pinHex}` }; },
  (o) => { o.model = { ...o.model, address: 'modelspec://github.com/acme/x/chinook' }; },
  (o) => { o.meaning = { ...o.meaning, address: `meaning://github.com/acme/x?ref=${pinHex}` }; },
  (o) => { o.meaning = { ...o.meaning, address: 'meaning://github.com/acme/x' }; },
  (o) => { o.meaning = { ...o.meaning, file: 'model/x.meaning.yaml' }; },
  (o) => { o.meaning = { ...o.meaning, graph: { ...o.meaning?.graph, address: 'meaning://github.com/acme/x' } }; },
  (o) => { o.licences = { ...o.licences, model: 'MIT', meaning: 'MIT' }; },
  (o) => { o.recordsets_partial = true; },
];
const deletes = [
  (o) => { delete o.model?.modelspec; }, (o) => { delete o.model?.hcl; }, (o) => { delete o.model?.address; }, (o) => { delete o.meaning?.address; },
  (o) => { delete o.meaning?.file; }, (o) => { delete o.meaning?.graph?.address; }, (o) => { delete o.licences?.model; }, (o) => { delete o.licences?.meaning; },
];
const subsets = (size, limit) => { // every subset of {0..size-1} of at most `limit` members, as a bit mask
  const out = [];
  for (let mask = 0; mask < 1 << size; mask += 1) if ([...Array(size).keys()].filter((at) => mask & (1 << at)).length <= limit) out.push(mask);
  return out;
};
const formDefining = [0, 1, 2, 4, 6]; // model.modelspec, model.hcl, model.address, meaning.address, meaning.file: all 32 combinations
for (const [baseName, object] of [[ownJson, ownObject], [sharedJson, sharedObject]]) {
  const masks = new Set(subsets(toggles.length, 2));
  for (let bits = 0; bits < 1 << formDefining.length; bits += 1) masks.add(formDefining.reduce((mask, at, i) => mask | (bits & (1 << i) ? 1 << at : 0), 0));
  for (const mask of masks) {
    const copy = clone(object);
    toggles.forEach((toggle, at) => { if (mask & (1 << at)) toggle(copy); });
    addManifest('forms', baseName, jsonOf(copy));
  }
  for (const mask of subsets(deletes.length, 2)) {
    const copy = clone(object);
    deletes.forEach((remove, at) => { if (mask & (1 << at)) remove(copy); });
    addManifest('forms', baseName, jsonOf(copy));
  }
}

// ---- the single-field edits of both JavaScript test suites ----

// Each `(m) => { ... }` or `(manifest) => { ... }` of the suites that edits a manifest, applied to an own and a shared manifest. Those that name
// something only the suite's own scope has (a helper, a constant) throw here and are skipped; the count is recorded.
let minedEdits = 0; let appliedEdits = 0;
const edits = new Set();
for (const text of suites) {
  for (const match of text.matchAll(/\((m|manifest)\) => \{([^}\n]*)\}/g)) edits.add(`${match[1]}\u0000${match[2]}`);
}
for (const entry of [...edits].sort()) {
  const [parameter, body] = entry.split('\u0000');
  minedEdits += 1;
  let edit;
  try { edit = new Function(parameter, body); } catch { continue; }
  let applied = false;
  for (const [baseName, object] of [[ownJson, ownObject], [sharedJson, sharedObject]]) {
    const copy = clone(object);
    try { edit(copy); } catch { continue; }
    addManifest('suite edit', baseName, jsonOf(copy));
    applied = true;
  }
  if (applied) appliedEdits += 1;
}

// ---- text-level mutants of the YAML documents ----

for (const name of [chinookYaml, hosterYaml]) {
  const text = bases[name];
  const rows = lines(text).filter((_, at, all) => at < all.length - 1 || all[at] !== '');
  const total = rows.length;
  const join = (list) => `${list.join('\n')}\n`;
  for (let at = 0; at < total; at += 1) {
    if (/^\s*(#|$)/.test(rows[at])) continue; // a comment or a blank line: nothing to mutate
    const copy = (change) => { const list = [...rows]; change(list, at); return join(list); };
    addManifest('line', name, copy((list, i) => { list.splice(i, 1); }));
    addManifest('line', name, copy((list, i) => { list.splice(i, 0, list[i]); }));
    addManifest('line', name, copy((list, i) => { list[i] = ` ${list[i]}`; }));
    addManifest('line', name, copy((list, i) => { list[i] = list[i].replace(/^ /, ''); }));
    addManifest('line', name, copy((list, i) => { list[i] = `\t${list[i]}`; }));
    addManifest('tab', name, copy((list, i) => { list[i] = list[i].replace(/^( +)/, (spaces) => '\t'.repeat(Math.ceil(spaces.length / 2))); }));
    addManifest('tab', name, copy((list, i) => { list[i] = list[i].replace(/: /, ':\t'); }));
    addManifest('anchor', name, copy((list, i) => { list[i] = list[i].replace(/: (\S)/, ': &a $1'); }));
    addManifest('alias', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, ': *a'); }));
    addManifest('tag', name, copy((list, i) => { list[i] = list[i].replace(/: (\S)/, ': !!str $1'); }));
    addManifest('tag', name, copy((list, i) => { list[i] = list[i].replace(/: (\S)/, ': !x $1'); }));
    addManifest('comment', name, copy((list, i) => { list[i] = `${list[i]} # c`; }));
    addManifest('comment', name, copy((list, i) => { list.splice(i, 0, '# c'); }));
    addManifest('comment', name, copy((list, i) => { list[i] = list[i].replace(/: (\S)/, ': # c\n  $1'); }));
    addManifest('duplicate key', name, copy((list, i) => { list.splice(i + 1, 0, list[i].replace(/: .*$/, ': dup')); }));
    addManifest('quote', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, (_, v) => `: '${v.replaceAll("'", "''")}'`); }));
    addManifest('quote', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, (_, v) => `: ${JSON.stringify(v)}`); }));
    addManifest('block scalar', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, (_, v) => `: |\n    ${v}`); }));
    addManifest('block scalar', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, (_, v) => `: >-\n    ${v}`); }));
    addManifest('multiline', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, (_, v) => `: ${v.slice(0, 3)}\n    ${v.slice(3)}`); }));
    addManifest('key', name, copy((list, i) => { list[i] = list[i].replace(/^(\s*)(\w+):/, '$1"$2":'); }));
    addManifest('key', name, copy((list, i) => { list[i] = list[i].replace(/^(\s*)(\w+):/, '$1? $2\n$1:'); }));
    addManifest('key', name, copy((list, i) => { list[i] = list[i].replace(/^(\s*)(\w+):/, '$1$2 :'); }));
    addManifest('number', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, ': 0x1F'); }));
    addManifest('number', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, ': 1.0'); }));
    addManifest('number', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, ': .inf'); }));
    addManifest('boolean', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, ': yes'); }));
    addManifest('boolean', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, ': True'); }));
    addManifest('null', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, ': ~'); }));
    addManifest('unicode', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, (_, v) => `: ${v}é`); }));
    addManifest('unicode', name, copy((list, i) => { list[i] = list[i].replace(/^ /, ' '); }));
    addManifest('flow', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, (_, v) => `: [${v}]`); }));
    addManifest('flow', name, copy((list, i) => { list[i] = list[i].replace(/: (\S.*)$/, (_, v) => `: {k: ${v}}`); }));
  }
  const wholeDocument = (family, variant) => addManifest(family, name, variant);
  wholeDocument('document', `---\n${text}`);
  wholeDocument('document', `${text}...\n`);
  wholeDocument('document', `${text}---\nformat: x\n`);
  wholeDocument('document', `%YAML 1.2\n---\n${text}`);
  wholeDocument('document', `%TAG ! tag:x,2000:\n---\n${text}`);
  wholeDocument('document', text.trimEnd());
  wholeDocument('document', `${text}\n\n\n`);
  wholeDocument('document', text.replace(/\n/g, '\r'));
  wholeDocument('document', text.replace(/\n/g, '\n\r'));
  wholeDocument('document', `${text}\u0000`);
  wholeDocument('document', `${text}\u0085`);
  wholeDocument('document', text.replace(/\n/g, ' '));
  wholeDocument('document', text.replace(/\n/g, ' \n'));
  wholeDocument('document', `\n\n${text}`);
  wholeDocument('document', `# only a comment\n`);
  wholeDocument('document', '');
  wholeDocument('document', '\n');
  wholeDocument('document', '---\n');
  wholeDocument('document', `${text}\xff`);
  const object = parseYaml(text);
  for (const [family, options] of [['stringify flow', { collectionStyle: 'flow' }], ['stringify double', { defaultStringType: 'QUOTE_DOUBLE', defaultKeyType: 'QUOTE_DOUBLE' }], ['stringify single', { defaultStringType: 'QUOTE_SINGLE' }], ['stringify literal', { defaultStringType: 'BLOCK_LITERAL' }], ['stringify folded', { defaultStringType: 'BLOCK_FOLDED' }], ['stringify wide', { lineWidth: 20 }], ['stringify indent', { indent: 4 }], ['stringify seq', { indentSeq: false }]]) {
    wholeDocument(family, stringifyYaml(object, options));
  }
}
for (const mutate of [(t) => t.replace('\n', '\r\n'), (t) => t.replace('---', '--- # c'), (t) => `${t}${t}`, (t) => t.replace(/\n/g, '\n\n')]) {
  for (const name of [chinookYaml, hosterYaml]) addManifest('document', name, mutate(bases[name]));
}

// ---- OVDB.md mutants ----

const mdName = chinookMd;
const mdText = bases[mdName];
const front = (inner, body = '# Title\n') => `---\n${inner}\n---\n${body}`;
const mdExamples = [
  '', '---', '---\n', '---\n---\n', '---\n\n---\n', '---\r\n\r\n---\r\n', '# title\n', ' ---\novdb: 1\npublish: [./ovdb.yaml]\n---\n', '﻿---\novdb: 1\npublish: [./ovdb.yaml]\n---\n',
  '---\novdb: 1\npublish: [./ovdb.yaml]\n', '---\novdb: 1\npublish: [./ovdb.yaml]\n---x\n', '---\novdb: 1\npublish: [./ovdb.yaml]\n----\n', '---\novdb: 1\npublish: [./ovdb.yaml]\n---', '---\novdb: 1\npublish: [./ovdb.yaml]\n--- \n',
  '---\r\novdb: 1\r\npublish: [./ovdb.yaml]\r\n---\r\n', '---\r\novdb: 1\npublish: [./ovdb.yaml]\n---\r\nbody', '---\novdb: 1\r\npublish: [./ovdb.yaml]\r\n---\n', '---\rovdb: 1\rpublish: [./ovdb.yaml]\r---\r',
  '---\n- a\n---\n', '---\na\n---\n', '---\n1\n---\n', '---\n~\n---\n', '---\n[]\n---\n', '---\n{}\n---\n', '---\novdb: &a 1\npublish: [./ovdb.yaml]\n---\n', '---\novdb: !!int 1\npublish: [./ovdb.yaml]\n---\n',
  '---\novdb: 1.0\npublish: [./ovdb.yaml]\n---\n', '---\novdb: 0x1\npublish: [./ovdb.yaml]\n---\n', '---\novdb: 01\npublish: [./ovdb.yaml]\n---\n', '---\novdb: 1e0\npublish: [./ovdb.yaml]\n---\n', '---\novdb: +1\npublish: [./ovdb.yaml]\n---\n', '---\novdb: "1"\npublish: [./ovdb.yaml]\n---\n',
  '---\novdb: true\npublish: [./ovdb.yaml]\n---\n', '---\novdb: 2\npublish: [./ovdb.yaml]\n---\n', '---\novdb:\npublish: [./ovdb.yaml]\n---\n', '---\npublish: [./ovdb.yaml]\n---\n', '---\novdb: 1\n---\n', '---\novdb: 1\npublish:\n---\n', '---\novdb: 1\npublish: []\n---\n',
  '---\novdb: 1\npublish: ./ovdb.yaml\n---\n', '---\novdb: 1\npublish: {a: ./ovdb.yaml}\n---\n', '---\novdb: 1\npublish: [ovdb.yaml]\n---\n', '---\novdb: 1\npublish: ["./*.yaml"]\n---\n', '---\novdb: 1\npublish: [./ovdb.yaml, ./ovdb.yaml]\n---\n',
  '---\novdb: 1\npublish: [./ovdb.yaml, x]\n---\n', '---\novdb: 1\npublish: [./ovdb.yaml, 1]\n---\n', '---\novdb: 1\npublish: [./ovdb.yaml, ~]\n---\n', '---\novdb: 1\npublish: [./ovdb.yaml, [./a]]\n---\n', '---\novdb: 1\npublish: [./ovdb.yaml, " "]\n---\n',
  '---\novdb: 1\npublish: [./../ovdb.yaml]\n---\n', '---\novdb: 1\npublish: [././ovdb.yaml]\n---\n', '---\novdb: 1\npublish: [./a//b.yaml]\n---\n', '---\novdb: 1\npublish: [./a/]\n---\n', '---\novdb: 1\npublish: [/ovdb.yaml]\n---\n', '---\novdb: 1\npublish: [.\\ovdb.yaml]\n---\n',
  '---\novdb: 1\npublish: ["./ovdb.yaml "]\n---\n', '---\novdb: 1\npublish: [" ./ovdb.yaml"]\n---\n', '---\novdb: 1\npublish: ["./é.yaml"]\n---\n', `---\novdb: 1\npublish: ["./${'a'.repeat(1100)}.yaml"]\n---\n`, '---\novdb: 1\nextra: 2\npublish: [./ovdb.yaml]\n---\n',
  '---\novdb: 1\novdb: 1\npublish: [./ovdb.yaml]\n---\n', '---\novdb: 1\npublish: [./ovdb.yaml]\npublish: [./ovdb.yaml]\n---\n', '---\novdb: 1\npublish:\n- ./ovdb.yaml\n---\n', '---\novdb: 1\npublish:\n  - ./ovdb.yaml\n  - ./sub/b.yaml\n---\n', '---\novdb: 1\npublish: [./ovdb.yaml]\n# comment\n---\n',
  '---\n# comment\novdb: 1\npublish: [./ovdb.yaml] # c\n---\n', '---\novdb: 1\npublish: [\n  ./ovdb.yaml,\n  ./b.yaml\n]\n---\n', '---\novdb: 1\npublish: [./ovdb.yaml]\n---\n---\novdb: 2\n---\n', '---\n---\novdb: 1\npublish: [./ovdb.yaml]\n---\n', '---\n\novdb: 1\npublish: [./ovdb.yaml]\n\n---\n',
  '---\n\tovdb: 1\npublish: [./ovdb.yaml]\n---\n', '---\novdb:\t1\npublish: [./ovdb.yaml]\n---\n', '---\novdb: 1 \npublish: [./ovdb.yaml]\n---\n', '---\novdb: 1\npublish: [./ovdb.yaml]\n---\né\xff\n', '---\novdb: 1\npublish: [./ovdb.yaml]\n---\n\u0000\n',
];
for (const example of mdExamples) {
  addMd('md', mdName, example);
  addMd('md', mdName, example, 'ovdb.yaml', 'crlf');
}
for (const base of [chinookMd, hosterMd, fixtureMd]) {
  const rows = lines(bases[base]);
  const front = rows.findIndex((row, at) => at > 0 && row === '---');
  for (let at = 0; at <= front + 1; at += 1) {
    for (const change of [(list) => list.splice(at, 1), (list) => list.splice(at, 0, list[at]), (list) => { list[at] = ` ${list[at] ?? ''}`; }, (list) => { list[at] = `\t${list[at] ?? ''}`; }, (list) => { list[at] = `${list[at] ?? ''} # c`; }, (list) => { list[at] = (list[at] ?? '').replace(/: (.*)$/, ': &a $1'); }, (list) => { list[at] = (list[at] ?? '').replace(/: .*$/, ': ~'); }, (list) => { list[at] = (list[at] ?? '').replace(/: .*$/, ': "x"'); }, (list) => { list[at] = (list[at] ?? '').replace(/: .*$/, ': [./x.yaml]'); }]) {
      const list = [...rows];
      change(list);
      addMd('md line', base, list.join('\n'));
    }
  }
}
for (const space of whitespace) for (const entry of [`"./ovdb.yaml${space}"`, `"${space}./ovdb.yaml"`, `"${space}"`, `"./${space}ovdb.yaml"`]) addMd('md blank', mdName, front(`ovdb: 1\npublish: [${entry}]`));
for (const value of wrongTypes) for (const field of ['ovdb', 'publish']) addMd('md type', mdName, front(`${field}: ${JSON.stringify(value)}\n${field === 'ovdb' ? 'publish: [./ovdb.yaml]' : 'ovdb: 1'}`));
for (const path of ['ovdb.yaml', 'a/b.yaml', '.ovdb.yaml', 'x']) for (const entry of ['./ovdb.yaml', `./${path}`, '././ovdb.yaml', `./${path}/`, `./${path.toUpperCase()}`]) addMd('md path', mdName, front(`ovdb: 1\npublish: ["${entry}"]`), path);

// ---- the goldens ----

const verdicts = {
  manifest: manifestCases.map(([base, patch, flags]) => manifestVerdict(flagged(applyPatch(bases[base], patch), flags))).join(''),
  md: mdCases.map(([base, patch, flags, path]) => mdVerdict(flagged(applyPatch(bases[base], patch), flags), path)).join(''),
};
const count = (text, digit) => [...text].filter((c) => c === digit).length;
const meta = {
  format: 'ovdb-publisher-manifest-reference/1',
  generatedBy: 'internal/publisher/manifest/testdata/reference/generate.mjs',
  node: process.version,
  references: Object.fromEntries(Object.entries(pins).map(([name, pin]) => [name, pin])),
};
const corpus = {
  ...meta,
  minedEdits: { found: minedEdits, applied: appliedEdits },
  families,
  bases,
  manifestCases,
  mdCases,
};
const verdictFile = {
  ...meta,
  profile: 'directory',
  thrown,
  manifest: { accepted: count(verdicts.manifest, '1'), refused: count(verdicts.manifest, '0'), verdicts: verdicts.manifest },
  md: { accepted: count(verdicts.md, '1'), refused: count(verdicts.md, '0'), verdicts: verdicts.md },
};
// One case per line, so that a diff of the goldens reads as a diff of cases.
const corpusText = [
  '{',
  ...Object.entries(corpus).filter(([key]) => !['manifestCases', 'mdCases', 'bases'].includes(key)).map(([key, value]) => `  ${JSON.stringify(key)}: ${JSON.stringify(value)},`),
  '  "bases": {', Object.entries(bases).map(([name, text]) => `    ${JSON.stringify(name)}: ${JSON.stringify(text)}`).join(',\n'), '  },',
  '  "manifestCases": [', manifestCases.map((entry) => `    ${JSON.stringify(entry)}`).join(',\n'), '  ],',
  '  "mdCases": [', mdCases.map((entry) => `    ${JSON.stringify(entry)}`).join(',\n'), '  ]',
  '}', '',
].join('\n');
const verdictText = `${JSON.stringify(verdictFile, null, 1)}\n`;

if (thrown > 0) console.error(`note: the reference threw on ${thrown} document(s); they are recorded as refused`);
if (process.argv.includes('--check')) {
  if (readFileSync(corpusPath, 'utf8') !== corpusText || readFileSync(verdictsPath, 'utf8') !== verdictText) { console.error(`the goldens in ${here} are stale: run node ${process.argv[1]}`); process.exit(1); }
  console.log(`the goldens are up to date (${manifestCases.length} manifest and ${mdCases.length} OVDB.md documents)`);
} else {
  writeFileSync(corpusPath, corpusText);
  writeFileSync(verdictsPath, verdictText);
  console.log(`wrote ${manifestCases.length} manifest and ${mdCases.length} OVDB.md documents: corpus ${(corpusText.length / 1024).toFixed(0)} KiB, verdicts ${(verdictText.length / 1024).toFixed(0)} KiB; accepted ${verdictFile.manifest.accepted}+${verdictFile.md.accepted}, refused ${verdictFile.manifest.refused}+${verdictFile.md.refused}; mined edits ${appliedEdits} of ${minedEdits} applied`);
}
