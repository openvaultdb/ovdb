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
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { references as pinnedReferences, remoteUrl } from '../../../references.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const corpusPath = join(here, 'corpus.json');
const verdictsPath = join(here, 'directory.verdicts.json');
const factsPath = join(here, 'directory.facts.json');
const digestsPath = join(here, 'digests.json');

const pins = pinnedReferences; // internal/publisher/references.mjs: the one place that says where each reference is

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
    run(dir, 'git', ['fetch', '--quiet', '--depth', '1', remoteUrl(name), commit]);
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
const modelspec = await import(pathToFileURL(join(directoryRoot, 'scripts/lib/modelspec.mjs')).href);
const meaningLib = await import(pathToFileURL(join(directoryRoot, 'scripts/lib/meaning.mjs')).href);
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
  // the record-stage refusals that need nothing but the two documents (see recordStageProblems below)
  "if (manifest.publisher.repository !== undefined && lowerKey(repositoryKey(manifest.publisher.repository) ?? '') !== lowerKey(repositoryKey(data.repository))) {",
  'if (new Set(listed).size !== listed.length) bad(',
  'if (manifest.model.name !== undefined && manifest.model.name !== model.module) bad(',
  'if (repositoryKey(`https://${parsed.repository}`) === null) bad(',
  'else if (parsed.repository !== parsed.repository.toLowerCase()) bad(',
  'else if (parsed.ref !== undefined) bad(',
  'const spelled = (label, address, parsed, kind) => {',
  'if (!modelSpelled || !graphSpelled) return stop();',
  "const lowerKey = (value) => value.toLowerCase();",
]) {
  if (!directorySource.includes(expression)) throw new Error(`directory.mjs at ${pins.directory.commit} no longer holds \`${expression}\`: the verdicts composed from it in generate.mjs are not the Directory's`);
}

// ---- the verdicts of the Directory profile: 1 accepts, 0 refuses ----

process.emitWarning = () => {}; // the yaml package warns on stderr; the Directory ignores it
let thrown = 0;
const isText = (value) => typeof value === 'string' && value.trim() !== '';
// What the Directory refuses after manifestProblems, at the record stage, from the manifest alone: no registry,
// no file of the repository and no record field is needed to know it. Each is the Directory's own expression,
// evaluated on the manifest (the line numbers are those of directory.mjs at the pinned commit):
//  - line 315, publisher.repository: written, it must equal, ignoring case, repositoryKey of the record's
//    repository, which recordProblems has made a repository key. A value that repositoryKey refuses ('' and
//    numbers and lists too) equals none, and any other value equals the record that has it.
//  - checkRecordsets: a name listed twice.
//  - model.name (own form and shared): written, it must equal the module, which parseModelSpec makes an identifier.
//  - the spelling of an address: own model.address, and in the shared form model.address and meaning.address:
//    a repository on a host the Directory reads, in lower case; an own model.address carries no ?ref=.
// What needs a record, a registry or a file (the equality of url, id and graph id with the record's, the
// repository the addresses name being this one, the model's module, licences and files) is not judged here:
// the facts carry it.
const lowerKey = (value) => value.toLowerCase();
const recordStageProblems = (manifest) => {
  const problems = [];
  const written = manifest.publisher.repository;
  if (written !== undefined && gitlib.repositoryKey(written) === null) problems.push('publisher.repository');
  if (new Set(manifest.recordsets).size !== manifest.recordsets.length) problems.push('recordsets twice');
  const name = manifest.model?.name;
  if (name !== undefined && !(typeof name === 'string' && modelspec.identifierPattern.test(name))) problems.push('model.name');
  const spelled = (parsed) => gitlib.repositoryKey(`https://${parsed.repository}`) !== null && parsed.repository === lowerKey(parsed.repository);
  if (directory.manifestForm(manifest) === 'own') {
    const parsed = manifest.model.address === undefined ? null : modelspec.parseModelAddress(manifest.model.address);
    if (parsed && (!spelled(parsed) || parsed.ref !== undefined)) problems.push('model.address');
  } else {
    if (!spelled(modelspec.parseModelAddress(manifest.model.address))) problems.push('model.address');
    if (!spelled(meaningLib.parseGraphAddress(manifest.meaning.address))) problems.push('meaning.address');
  }
  return problems;
};
// The Directory reads a file as UTF-8: bytes that are not are replaced by U+FFFD.
const decoded = (buffer) => buffer.toString('utf8');
const manifestVerdict = (buffer) => {
  let manifest;
  try { manifest = parseYaml(decoded(buffer)); } catch { return 0; } // as the Directory reads it: default options, an error throws
  try { return directory.manifestProblems(manifest).length === 0 && recordStageProblems(manifest).length === 0 ? 1 : 0; } catch { thrown += 1; return 0; }
};
// The values the Directory's own code derives from an accepted manifest, for each field of the README table: the
// field as written (null when the key is not written) and what manifestForm, parseModelAddress and
// parseGraphAddress make of it.
const factsOf = (manifest) => {
  const at = (path) => path.split('.').reduce((value, key) => (value === undefined || value === null ? undefined : value[key]), manifest);
  const facts = {};
  for (const path of factFields) facts[path] = at(path) ?? null;
  facts.form = directory.manifestForm(manifest);
  const model = at('model.address') === undefined ? null : modelspec.parseModelAddress(manifest.model.address);
  const meaning = at('meaning.address') === undefined ? null : meaningLib.parseGraphAddress(manifest.meaning.address);
  facts['model.address.repository'] = model?.repository ?? null;
  facts['model.address.module'] = model?.module ?? null;
  facts['model.address.ref'] = model?.ref ?? null;
  facts['meaning.address.repository'] = meaning?.repository ?? null;
  facts['meaning.address.ref'] = meaning?.ref ?? null;
  return facts;
};
const factFields = ['format', 'id', 'title', 'description', 'url', 'homepage', 'deployment.url', 'deployment.engine', 'deployment.discovery', 'deployment.recordset_page', 'model.modelspec', 'model.hcl', 'model.address', 'model.name', 'meaning.address', 'meaning.file', 'meaning.graph.id', 'meaning.graph.address', 'licences.model', 'licences.meaning', 'licences.data', 'publisher.name', 'publisher.url', 'publisher.repository', 'recordsets', 'recordsets_partial'];
// Where directory.mjs at the pinned commit reads each field (the lines cited in the README table), found by
// reading the file: a line reads a field when it names the whole path in one of the Directory's spellings: a chain
// (`manifest.meaning?.graph?.address`), `need(manifest.meaning?.graph, 'address', ...)`, or a loop over field names
// (`for (const field of ['model', 'meaning']) ... manifest.licences?.[field]`); the form and the parts of an address
// are read where `manifestForm`, `parseModelAddress` and `parseGraphAddress` are called. Comment lines do not count.
// The script fails if a field is read nowhere, so a pin that moves cannot leave the table wrong or empty.
const directoryLines = directorySource.split('\n');
const escapeRegExp = (text) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const chain = (segments) => segments.map((segment) => `\\??\\.${escapeRegExp(segment)}`).join('');
const readingLines = (path) => {
  const segments = path.split('.');
  const parent = segments.slice(0, -1); const last = segments.at(-1);
  const patterns = path === 'form' ? [/manifestForm[ (]/]
    : path.startsWith('model.address.') ? [/parseModelAddress\(/]
      : path.startsWith('meaning.address.') ? [/parseGraphAddress\(/]
        : [
          new RegExp(`manifest${chain(segments)}(?![A-Za-z0-9_])`),
          new RegExp(`need\\(manifest${chain(parent)}, '${escapeRegExp(last)}'`),
          new RegExp(`for \\(const field of \\[[^\\]]*'${escapeRegExp(last)}'[^\\]]*\\]\\).*manifest${chain(parent)}(?:\\?)?(?:\\.)?\\[field\\]`),
        ];
  return directoryLines.flatMap((line, at) => (!/^\s*\/\//.test(line) && patterns.some((pattern) => pattern.test(line)) ? [at + 1] : []));
};
const reads = Object.fromEntries([...factFields, 'form', 'model.address.repository', 'model.address.module', 'model.address.ref', 'meaning.address.repository', 'meaning.address.ref'].map((path) => {
  const lines = readingLines(path);
  if (lines.length === 0) throw new Error(`directory.mjs at ${pins.directory.commit} reads ${path} nowhere: the table of fields of the README would be empty`);
  return [path, lines];
}));
const mdDerive = (buffer, path) => {
  try {
    const { data, error } = directory.parseFrontmatter(decoded(buffer));
    if (error) return null;
    if (data.ovdb !== 1) return null;
    if (!Array.isArray(data.publish) || data.publish.length === 0) return null;
    const published = new Set();
    for (const entry of data.publish) {
      if (!isText(entry) || !entry.startsWith('./') || !gitlib.isRepositoryPath(entry.slice(2))) return null;
      published.add(entry.slice(2));
    }
    return published.has(path) ? [...published] : null;
  } catch { thrown += 1; return null; }
};
const mdVerdict = (buffer, path) => (mdDerive(buffer, path) === null ? 0 : 1);

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
// The flags: crlf (every line ends in CRLF), bom, pad=N (a comment line makes the document exactly N bytes), and
// latin1 (the characters of the text, all below U+0100, are written as single bytes: invalid UTF-8). The result is
// the bytes of the document; the Go test makes the same bytes.
const flagged = (text, flags) => {
  const flag = flags.split(' ');
  let out = text;
  if (flag.includes('crlf')) out = out.replaceAll('\n', '\r\n');
  if (flag.includes('bom')) out = `\ufeff${out}`;
  const pad = flag.find((f) => f.startsWith('pad='));
  if (pad) out = `${out}# ${'x'.repeat(Number(pad.slice(4)) - Buffer.byteLength(out) - 3)}\n`;
  return Buffer.from(out, flag.includes('latin1') ? 'latin1' : 'utf8');
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

const whitespace = ['\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2000', '\u2005', '\u200a', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff', '\u0085', '\u180e', '\u200b', '\u200c', '\u200d', '\u2060', '\u00ad', '\u0000'];
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
    addManifest('unicode', name, copy((list, i) => { list[i] = list[i].replace(/^ /, '\u00a0'); }));
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
  wholeDocument('document', text.replace(/\n/g, '\u2028'));
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
  '', '---', '---\n', '---\n---\n', '---\n\n---\n', '---\r\n\r\n---\r\n', '# title\n', ' ---\novdb: 1\npublish: [./ovdb.yaml]\n---\n', '\ufeff---\novdb: 1\npublish: [./ovdb.yaml]\n---\n',
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

// ---- what the record stage refuses, and the kinds where the Go reader and bounds are stricter ----

const longPath = (length, suffix = '') => `${'a/'.repeat(length).slice(0, length - suffix.length - 1)}b${suffix}`; // exactly `length` characters, a plain relative path
const repositoryValues = ['', null, 123, true, ['https://github.com/datatug/chinookdb'], {}, ' ', 'https://github.com/datatug/chinookdb', 'https://github.com/datatug/chinookdb/', 'http://github.com/a/b', 'https://gitlab.com/a/b', 'https://github.com/a', 'https://github.com/a/b/c', 'https://github.com/a/b.git', 'https://github.com/a/b.GIT', ' https://github.com/a/b', 'https://github.com/DataTug/ChinookDB', 'https://github.com/a/b?x=1', 'https://github.com/a/b#x', 'https://github.com:443/a/b', 'https://github.com/a/..', `https://github.com/a/${'b'.repeat(300)}`, `https://github.com/a/${'b'.repeat(234)}`, 'https://github.com/a/b\n'];
const nameValues = ['chinook', 'Chinook', '_x', 'x1', '', ' ', 123, true, null, ['a'], { a: 1 }, '1x', 'a-b', 'a b', 'é', 'x'.repeat(5000), 'Chinook\n'];
const own = ownObject; const shared = sharedObject;
for (const [baseName, object] of [[ownJson, own], [sharedJson, shared]]) {
  const mutate = (family, change) => { const copy = clone(object); change(copy); addManifest(family, baseName, jsonOf(copy)); };
  for (const value of repositoryValues) mutate('record stage: publisher.repository', (m) => { m.publisher.repository = value; });
  for (const value of nameValues) mutate('record stage: model.name', (m) => { m.model.name = value; });
  mutate('record stage: recordsets', (m) => { m.recordsets = [...m.recordsets, m.recordsets[0]]; });
  mutate('record stage: recordsets', (m) => { m.recordsets = [m.recordsets[0], m.recordsets[0], ...m.recordsets.slice(1)]; });
  mutate('record stage: recordsets', (m) => { m.recordsets = [m.recordsets[0], m.recordsets[0].toUpperCase()]; });
  for (const value of [longPath(1024), longPath(1025), longPath(1030, '.yaml')]) {
    mutate('length', (m) => { m.meaning.file = value; });
    if (baseName === ownJson) mutate('length', (m) => { m.model.modelspec = value; });
  }
  if (baseName === ownJson) for (const value of [longPath(1024, '.modelspec.hcl'), longPath(1025, '.modelspec.hcl'), longPath(1033, '.modelspec.hcl')]) mutate('length', (m) => { m.model.hcl = value; });
  for (const value of ['M'.repeat(64), 'M'.repeat(65), `${'M'.repeat(70)}`]) mutate('length', (m) => { m.licences.data = value; });
  for (const value of ['p'.repeat(40), 'p'.repeat(41)]) mutate('length', (m) => { m.deployment.engine = value; });
  for (const homepage of ['https://xn--ovdb-kva.io/ovdb', 'https://xn--bcher-kva.de/ovdb', 'https://xn--80ak6aa92e.com/ovdb', 'https://acme.io/ovdb']) mutate('punycode', (m) => { m.homepage = homepage; });
}
for (const [baseName, object] of [[ownJson, own], [sharedJson, shared]]) {
  const mutate = (family, change) => { const copy = clone(object); change(copy); addManifest(family, baseName, jsonOf(copy)); };
  const addresses = (module) => [
    `modelspec://github.com/datatug/chinookdb/${module}`, `modelspec://github.com/datatug/chinookdb/${module}?ref=${pinHex}`, `modelspec://github.com/DataTug/chinookdb/${module}`, `modelspec://github.com/DataTug/ChinookDB/${module}?ref=${pinHex}`,
    `modelspec://gitlab.com/datatug/chinookdb/${module}`, `modelspec://github.com.evil/datatug/chinookdb/${module}`, `modelspec://github.com/datatug/chinookdb/${module}?ref=${pinHex.toUpperCase()}`, `modelspec://github.com/a/b.git/${module}?ref=${pinHex}`, `modelspec://github.com/a/../${module}?ref=${pinHex}`,
    `modelspec://github.com/${'o'.repeat(300)}/b/${module}?ref=${pinHex}`, `modelspec://github.com/datatug/chinookdb/${'m'.repeat(2048 - 'modelspec://github.com/datatug/chinookdb/'.length)}`, `modelspec://github.com/datatug/chinookdb/${'m'.repeat(2049 - 'modelspec://github.com/datatug/chinookdb/'.length)}`,
    `modelspec://github.com/datatug/chinookdb/${'m'.repeat(2048 - 'modelspec://github.com/datatug/chinookdb/?ref='.length - 40)}?ref=${pinHex}`, `modelspec://github.com/datatug/chinookdb/${'m'.repeat(2049 - 'modelspec://github.com/datatug/chinookdb/?ref='.length - 40)}?ref=${pinHex}`,
  ];
  for (const address of addresses('chinook')) mutate('address spelling', (m) => { m.model.address = address; });
  for (const address of [`meaning://github.com/datatug/chinookdb?ref=${pinHex}`, 'meaning://github.com/datatug/chinookdb', `meaning://github.com/DataTug/chinookdb?ref=${pinHex}`, `meaning://gitlab.com/datatug/chinookdb?ref=${pinHex}`, `meaning://github.com/datatug/chinookdb/x?ref=${pinHex}`, `meaning://github.com/${'o'.repeat(300)}/b?ref=${pinHex}`, `meaning://github.com/datatug/${'r'.repeat(2049 - 'meaning://github.com/datatug/?ref='.length - 40)}?ref=${pinHex}`]) {
    mutate('address spelling', (m) => { m.meaning.address = address; });
  }
}

// Spellings that the Go reader and bounds refuse and the Directory's reader and rules accept: one line added or
// changed in a real manifest, and documents at and over the size bound.
for (const name of [chinookYaml, hosterYaml]) {
  const text = bases[name];
  const add = (family, line, flags = '') => addManifest(family, name, `${text}${line}\n`, flags);
  add('reader', '2024: archived'); add('reader', 'true: 1'); add('reader', '[a]: x'); add('reader', 'null: 1'); add('reader', '? [a, b]\n: x');
  add('reader', 'weight: .inf'); add('reader', 'ratio: .nan'); add('reader', 'rows: 9007199254740993'); add('reader', 'neg: -.inf'); add('reader', 'ok: 9007199254740992'); add('reader', 'exp: 1e3'); add('reader', 'octal: 0o17'); add('reader', 'hex: 0xFF');
  add('reader', 'extra: "\\ud83c"'); add('reader', 'extra: "\\q"'); add('reader', 'extra: "\\x41\\u00e9\\U0001F600"'); add('reader', 'extra: "\\N\\_\\L\\P"');
  add('reader', '# café', 'latin1'); add('reader', '# café');
  add('reader', 'extra: a\rb'); add('reader', 'extra: x', 'crlf');
  addManifest('size', name, text, 'pad=262144'); addManifest('size', name, text, 'pad=262145'); addManifest('size', name, text, 'pad=1048576');
  // Nesting: the reader refuses a collection nested 64 deep (63 is read), which the reference reads to any depth.
  for (const depth of [62, 63, 64, 65, 100]) add('reader', `x: ${'['.repeat(depth)}${']'.repeat(depth)}`);
  // A long string as a YAML tool writes it back: a double-quoted value folded over several lines, from the real manifest.
  for (const width of [40, 60, 80]) {
    const written = stringifyYaml(parseYaml(text), { defaultStringType: 'QUOTE_DOUBLE', lineWidth: width });
    addManifest('multi-line quoted', name, written);
    addManifest('multi-line quoted', name, written.replaceAll('\n', '\r\n'), 'crlf');
  }
  addManifest('multi-line quoted', name, stringifyYaml(parseYaml(text), { defaultStringType: 'QUOTE_SINGLE', lineWidth: 40 }));
  addManifest('reader', name, text.replace(/^title:.*$/m, 'title: Chinook\rmusic'));
  addManifest('reader', name, text.replace(/^title:.*$/m, 'title: "Chinook \\ud83c store"'));
}
for (const flags of ['pad=262144', 'pad=262145', 'latin1']) for (const name of [chinookMd, hosterMd]) addMd('size', name, bases[name], 'ovdb.yaml', flags);
const entryOf = (length) => `./${longPath(length - 2, '.yaml')}`;
for (const length of [1026, 1027, 1100]) addMd('md length', mdName, front(`ovdb: 1\npublish: ["${entryOf(length)}", ./ovdb.yaml]`));
for (let count = 1; count <= 3; count += 1) addMd('md repeated', mdName, front(`ovdb: 1\npublish: [${Array(count).fill('./ovdb.yaml').join(', ')}, ./b.yaml, ./b.yaml]`));
addMd('md repeated', mdName, front(`ovdb: 1\npublish: [${Array.from({ length: 150 }, () => './x').join(', ')}]`), 'x');

// ---- the goldens ----

const manifestBuffers = manifestCases.map(([base, patch, flags]) => flagged(applyPatch(bases[base], patch), flags));
const mdBuffers = mdCases.map(([base, patch, flags]) => flagged(applyPatch(bases[base], patch), flags));
const verdicts = {
  manifest: manifestBuffers.map((buffer) => manifestVerdict(buffer)).join(''),
  md: mdBuffers.map((buffer, at) => mdVerdict(buffer, mdCases[at][3])).join(''),
};
const count = (text, digit) => [...text].filter((c) => c === digit).length;
const meta = {
  format: 'ovdb-publisher-manifest-reference/2',
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
// The facts: for each accepted manifest, the values factsOf derives, as the difference from those of its base
// (a base that is accepted is the reference of its cases); for each accepted OVDB.md, the set it lists.
const factsOfBuffer = (buffer) => factsOf(parseYaml(decoded(buffer)));
const baseFacts = {};
for (const name of Object.keys(bases)) {
  if (!name.endsWith('-md') && manifestVerdict(Buffer.from(bases[name])) === 1) baseFacts[name] = factsOfBuffer(Buffer.from(bases[name]));
}
const factDeltas = [];
manifestCases.forEach(([base], at) => {
  if (verdicts.manifest[at] !== '1') return;
  const facts = factsOfBuffer(manifestBuffers[at]);
  const delta = {};
  for (const [key, value] of Object.entries(facts)) if (JSON.stringify(value) !== JSON.stringify(baseFacts[base][key])) delta[key] = value;
  factDeltas.push(delta);
});
const mdLists = [];
mdCases.forEach(([, , , path], at) => { if (verdicts.md[at] === '1') mdLists.push(mdDerive(mdBuffers[at], path)); });
// Presence: for every manifest of the corpus, accepted or not, which of the fields of the table the Directory's parsed
// manifest has (the key is written, even with null), as a bit mask in the order of the table; "-" when the reference
// cannot read the document and "0" when it reads something that is not a mapping. The Go test compares it with the
// `Present` of every fact of every manifest it reads, so a written value that is refused cannot become an absent fact.
const presenceOf = (buffer) => {
  let manifest;
  try { manifest = parseYaml(decoded(buffer)); } catch { return '-'; }
  if (manifest === null || typeof manifest !== 'object' || Array.isArray(manifest)) return '0';
  const at = (path) => path.split('.').reduce((value, key) => (value === undefined || value === null ? undefined : value[key]), manifest);
  return factFields.reduce((mask, path, bit) => (at(path) !== undefined ? mask | (1 << bit) : mask), 0).toString(16);
};
const basePresence = {};
for (const name of Object.keys(baseFacts)) basePresence[name] = presenceOf(Buffer.from(bases[name]));
const presenceDeltas = manifestCases.map(([base], at) => { const mask = presenceOf(manifestBuffers[at]); return mask === basePresence[base] ? '' : mask; });
const factsFile = {
  ...meta,
  profile: 'directory',
  fields: [...factFields, 'form', 'model.address.repository', 'model.address.module', 'model.address.ref', 'meaning.address.repository', 'meaning.address.ref'],
  reads,
  bases: baseFacts,
  presenceBases: basePresence,
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
const factsText = [
  '{',
  ...Object.entries(factsFile).filter(([key]) => key !== 'bases').map(([key, value]) => `  ${JSON.stringify(key)}: ${JSON.stringify(value)},`),
  '  "bases": {', Object.entries(baseFacts).map(([name, facts]) => `    ${JSON.stringify(name)}: ${JSON.stringify(facts)}`).join(',\n'), '  },',
  '  "presence": [', presenceDeltas.map((mask) => `    ${JSON.stringify(mask)}`).join(',\n'), '  ],',
  '  "manifest": [', factDeltas.map((entry) => `    ${JSON.stringify(entry)}`).join(',\n'), '  ],',
  '  "md": [', mdLists.map((entry) => `    ${JSON.stringify(entry)}`).join(',\n'), '  ]',
  '}', '',
].join('\n');
// A digest of every committed golden of both slices, so that a hand edit of any of them fails `go test` until the
// generator is run again (the digests of the rules golden are read from the committed file, which its own
// generator writes: run that one first when the rules change).
const rulesGolden = join(here, '../../../rules/testdata/reference/matrix.golden.json');
const sha = (data) => createHash('sha256').update(data).digest('hex');
const digestText = `${JSON.stringify({
  'manifest/testdata/reference/corpus.json': sha(corpusText),
  'manifest/testdata/reference/directory.verdicts.json': sha(verdictText),
  'manifest/testdata/reference/directory.facts.json': sha(factsText),
  'rules/testdata/reference/matrix.golden.json': sha(readFileSync(rulesGolden)),
}, null, 1)}\n`;

if (thrown > 0) console.error(`note: the reference threw on ${thrown} document(s); they are recorded as refused`);
const targets = [[corpusPath, corpusText], [verdictsPath, verdictText], [factsPath, factsText], [digestsPath, digestText]];
if (process.argv.includes('--check')) {
  if (targets.some(([path, text]) => readFileSync(path, 'utf8') !== text)) { console.error(`the goldens in ${here} are stale: run node ${process.argv[1]}`); process.exit(1); }
  console.log(`the goldens are up to date (${manifestCases.length} manifest and ${mdCases.length} OVDB.md documents)`);
} else {
  for (const [path, text] of targets) writeFileSync(path, text);
  console.log(`wrote ${manifestCases.length} manifest and ${mdCases.length} OVDB.md documents: corpus ${(corpusText.length / 1024).toFixed(0)} KiB, verdicts ${(verdictText.length / 1024).toFixed(0)} KiB, facts ${(factsText.length / 1024).toFixed(0)} KiB; accepted ${verdictFile.manifest.accepted}+${verdictFile.md.accepted}, refused ${verdictFile.manifest.refused}+${verdictFile.md.refused}; mined edits ${appliedEdits} of ${minedEdits} applied`);
}
