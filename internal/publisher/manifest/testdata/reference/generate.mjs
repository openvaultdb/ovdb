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
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, join, posix, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { checkoutReference, references as pinnedReferences } from '../../../references.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const corpusPath = join(here, 'corpus.json');
const verdictsPath = join(here, 'directory.verdicts.json');
const factsPath = join(here, 'directory.facts.json');
const publisherVerdictsPath = join(here, 'publisher.verdicts.json');
const publisherFactsPath = join(here, 'publisher.facts.json');
const digestsPath = join(here, 'digests.json');

const pins = pinnedReferences; // internal/publisher/references.mjs: the one place that says where each reference is

// ---- the references, at the pinned commits ----

const argValue = (name) => { const at = process.argv.indexOf(name); return at === -1 ? undefined : process.argv[at + 1]; };
const run = (cwd, command, args) => execFileSync(command, args, { cwd, stdio: ['ignore', 'pipe', 'inherit'], encoding: 'utf8' }).trim();

const checkout = (name) => checkoutReference(name, { explicit: argValue(`--${name}`) });

const directoryRoot = checkout('directory');
const chinookRoot = checkout('chinookdb');
// The Chinook checker imports the `yaml` package, which its own checkout does not have installed (it would need the whole of its
// site's dependencies): a checkout that we fetched gets a fresh link to the copy that the Directory's checkout installed from its lock
// file, the same package (whatever node_modules it had is removed first: it is not part of the commit).
if (argValue('--chinookdb')) {
  if (!existsSync(join(chinookRoot, 'node_modules', 'yaml'))) throw new Error(`${chinookRoot} has no node_modules/yaml, which ovdb-manifest.mjs imports: install it there, or let the script fetch the checkout`);
} else {
  rmSync(join(chinookRoot, 'node_modules'), { recursive: true, force: true });
  mkdirSync(join(chinookRoot, 'node_modules'), { recursive: true });
  symlinkSync(join(directoryRoot, 'node_modules', 'yaml'), join(chinookRoot, 'node_modules', 'yaml'), 'dir');
}
const directory = await import(pathToFileURL(join(directoryRoot, 'scripts/lib/directory.mjs')).href);
const gitlib = await import(pathToFileURL(join(directoryRoot, 'scripts/lib/git.mjs')).href);
const modelspec = await import(pathToFileURL(join(directoryRoot, 'scripts/lib/modelspec.mjs')).href);
const meaningLib = await import(pathToFileURL(join(directoryRoot, 'scripts/lib/meaning.mjs')).href);
const chinook = await import(pathToFileURL(join(chinookRoot, 'scripts/lib/ovdb-manifest.mjs')).href);
const { isolatedGitEnv } = await import(pathToFileURL(join(chinookRoot, 'scripts/lib/git-env.mjs')).href);
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


// ---- the publisher reference: the Chinook checker, run on a repository ----
//
// scripts/lib/ovdb-manifest.mjs reads a git repository at HEAD through a `files` object (problem, read, kind), which its own test
// suite also implements in memory for the cases it has by the hundred. A case is run on a repository held in memory, as that suite
// does, and a sample of the cases is run again on a real throwaway repository, one per case in a directory of its own that is
// removed afterwards, and the script fails if the two ever differ.
//
// What the checker refuses falls in two groups, found by running each case on two repositories:
//  - "consistent": the repository holds every file the documents name, with contents that agree with them (the model of the module that
//    model.address or model.name names and the recordsets as entities, a meaning file with the graph's id, the meaning licence and the
//    models: entry that is model.hcl). What the checker still refuses is refused by the two documents alone: this slice's.
//  - "bare": the repository holds only OVDB.md and the manifests. What it refuses and the consistent one does not needs other files,
//    and is slice 3's. The verdict file records how many cases, by what the refusal is about.
const goodMd = '---\novdb: 1\npublish: [./ovdb.yaml]\n---\n';
const objectOf = (text) => { try { const value = parseYaml(text); return value !== null && typeof value === 'object' && !Array.isArray(value) ? value : null; } catch { return null; } };
const filesFor = (held) => ({ problem: () => '', kind: (path) => (held.has(path) ? 'file' : 'missing'), read: (path) => held.get(path) });
// The files named by a manifest. `consistent` agree with it; `wrong` are well formed and disagree (another module, other entities,
// another graph id and licence, no models: entry); `broken` are not JSON and not a YAML mapping.
const namedFiles = (manifest, kind) => {
  const held = new Map();
  if (manifest === null) return held;
  const string = (value) => (typeof value === 'string' ? value : undefined);
  const address = modelspec.parseModelAddress(manifest.model?.address);
  const consistent = kind === 'consistent';
  const module = consistent ? (address?.module ?? string(manifest.model?.name) ?? 'chinook') : 'Hostile';
  const entities = consistent && Array.isArray(manifest.recordsets) ? manifest.recordsets.filter((name) => typeof name === 'string') : ['Hostile_entity'];
  const modelFile = string(manifest.model?.modelspec); const hcl = string(manifest.model?.hcl); const meaningFile = string(manifest.meaning?.file);
  if (modelFile !== undefined) held.set(modelFile, kind === 'broken' ? 'not json' : JSON.stringify({ module: { name: module }, entities: Object.fromEntries(entities.map((name) => [name, {}])) }));
  if (hcl !== undefined) held.set(hcl, 'module');
  if (meaningFile !== undefined) {
    const relative = hcl !== undefined ? posix.relative(posix.dirname(meaningFile), hcl) : 'x.modelspec.hcl';
    held.set(meaningFile, kind === 'broken' ? '- not a mapping' : JSON.stringify(consistent
      ? { id: string(manifest.meaning?.graph?.id) ?? 'x', license: string(manifest.licences?.meaning) ?? 'MIT', models: { [module]: relative } }
      : { id: 'hostile', license: 'Hostile', models: {} }));
  }
  return held;
};
const repositoryKinds = ['consistent', 'bare', 'wrong', 'broken'];
const manifestRepository = (manifestText, kind) => {
  const held = new Map([['OVDB.md', goodMd]]);
  if (kind !== 'bare') for (const [path, text] of namedFiles(objectOf(manifestText), kind)) held.set(path, text);
  held.set('ovdb.yaml', manifestText);
  return held;
};
// OVDB.md under test: every entry names a tracked copy of the real Chinook manifest, so that only OVDB.md can be wrong.
const mdRepository = (mdText, kind) => {
  const held = new Map([['OVDB.md', mdText]]);
  const chinookManifest = bases[chinookYaml];
  if (kind !== 'bare') for (const [path, text] of namedFiles(objectOf(chinookManifest), kind)) held.set(path, text);
  const { data } = chinook.parseFrontmatter(mdText);
  for (const entry of Array.isArray(data?.publish) ? data.publish : []) {
    if (typeof entry === 'string' && entry.startsWith('./') && entry.length > 2) held.set(entry.slice(2), chinookManifest);
  }
  return held;
};
const publisherProblems = (held) => {
  try { return chinook.reportOvdbManifest(filesFor(held), {}).problems; } catch (error) { thrown += 1; return [`threw: ${error.message}`]; }
};
// The same on a real repository: git init, add, commit, and the checker's own gitRepoFiles.
const realProblems = (held) => {
  const root = mkdtempSync(join(tmpdir(), 'ovdb-publisher-case-'));
  try {
    const env = isolatedGitEnv();
    const git = (...args) => execFileSync('git', ['-C', root, ...args], { env, stdio: 'pipe' });
    git('init', '--quiet');
    for (const [path, text] of held) {
      if (path.length > 200 || !/^[A-Za-z0-9_.\/-]+$/.test(path) || path.startsWith('/') || path.split('/').some((part) => part === '..' || part === '.' || part === '' || part === '.git')) return null; // not a file git can hold: the case is not compared
      mkdirSync(dirname(join(root, path)), { recursive: true });
      writeFileSync(join(root, path), text);
    }
    git('add', '-A', '--force');
    git('-c', 'user.name=t', '-c', 'user.email=t@example.test', '-c', 'commit.gpgsign=false', 'commit', '--quiet', '--allow-empty', '-m', 'case');
    return chinook.reportOvdbManifest(chinook.gitRepoFiles(root), {}).problems;
  } finally { rmSync(root, { recursive: true, force: true }); }
};
// What a refusal needs: the classes of what only the bare repository refuses.
const needsClasses = [
  ['a tracked regular file', /must be a tracked regular file/],
  ['the model file: JSON, module and entities', /is not a ModelSpec JSON file|has no module\.name|has no entities/],
  ['model.name against the model file', /model\.name is .*, but .* is module/],
  ['model.address against the model file', /model\.address must be modelspec:\/\/.*, this repository plus the module name in/],
  ['the meaning file against the manifest', /meaning file's id|licences\.meaning is .* but the meaning file says|has no models: entry|models must name the ModelSpec module|but the meaning file's models: entry|is not valid YAML|is not a MeaningGraph file/],
  ['recordsets against the model', /recordsets lacks ModelSpec entities|recordsets names things that are not ModelSpec entities/],
];
const classOf = (problem) => needsClasses.find(([, pattern]) => pattern.test(problem))?.[0];
// [verdict, needs]: the verdict of the consistent repository, and the classes of what the others refuse on top, or ''.
const publisherVerdict = (held) => {
  if (publisherProblems(held.consistent).length > 0) return ['0', ''];
  const others = repositoryKinds.slice(1).flatMap((kind) => publisherProblems(held[kind]));
  if (others.length === 0) return ['1', ''];
  const classes = [...new Set(others.map((problem) => classOf(problem) ?? `unclassified: ${problem}`))].sort();
  return ['1', classes.join('; ')];
};
const heldOf = (repository, text) => Object.fromEntries(repositoryKinds.map((kind) => [kind, repository(text, kind)]));
const publisherManifestHeld = (buffer) => heldOf(manifestRepository, decoded(buffer));
const publisherMdHeld = (buffer) => heldOf(mdRepository, decoded(buffer));
// The entries of an accepted OVDB.md, as a set in order.
const mdEntries = (buffer) => {
  const { data } = chinook.parseFrontmatter(decoded(buffer));
  return [...new Set(data.publish.map((entry) => entry.slice(2)))];
};

// What the checker's source says, by line: each rule that the publisher profile adds to the Directory's, whether the two documents
// decide it, and where the checker makes it. The script fails if a cited line no longer holds its snippet.
const chinookLines = read(chinookRoot, 'scripts/lib/ovdb-manifest.mjs').split('\n');
// The lists the checker holds: the licence ids (exported) and the allowed keys of each mapping (read from the source of the checker).
const chinookAllowedKeys = (() => {
  const text = chinookLines.join('\n');
  const start = text.indexOf('const allowedKeys = {');
  const end = text.indexOf('\n};', start);
  if (start < 0 || end < 0) throw new Error('ovdb-manifest.mjs no longer holds `const allowedKeys = {...};`');
  return new Function(`return ${text.slice(start + 'const allowedKeys = '.length, end + 2)}`)();
})();
const publisherRules = [
  // [id, who decides, Go rule, snippet, lines]
  ['unknown keys at every level of a manifest', 'documents', 'manifest-keys', 'const unknown = Object.keys(object)', [251, 254, 255]],
  ['id is a lower-case id of at most 80 characters', 'documents', 'manifest-id', 'idPattern.test(manifest.id)', [260]],
  ['deployment.discovery is on the origin of url, at /.well-known/openvaultdb', 'documents', 'manifest-discovery', 'discovery.pathname !== discoveryPath', [289, 290]],
  ['deployment.recordset_page is on the origin of deployment.url', 'documents', 'manifest-url', 'expanded.origin !== deployed.origin', [295]],
  ['publisher.url is https://github.com/<owner>', 'documents', 'manifest-publisher', 'publisher.url must be https://github.com/<owner>', [302]],
  ['publisher.repository is required, a github.com repository, owned by the owner of publisher.url', 'documents', 'manifest-publisher', 'publisher.repository must belong to the owner in publisher.url', [305, 306, 308]],
  ['model.address names a repository of github.com and a module that starts with a letter', 'documents', 'manifest-model', 'model.address must be modelspec://github.com', [67, 319, 320]],
  ['model.name is a module name that starts with a letter', 'documents', 'manifest-model', 'model.name, when given, must be a ModelSpec module name', [322, 323]],
  ['model.name is the module of model.address (shared form; in the own form the checker gets the same through the model file)', 'documents', 'manifest-model', 'but model.address names module', [486, 487]],
  ['shared form: neither address is the publisher\'s own repository', 'documents', 'manifest-model, manifest-meaning', 'notOwn && spelled === ownRepository', [333, 334, 485, 499]],
  ['own form: model.hcl is required, model.modelspec ends in .modelspec.json', 'documents', 'manifest-required, manifest-model', 'model.hcl is required with local model files', [378, 387, 388]],
  ['own form: model.address is this repository (publisher.repository), without a pin', 'documents', 'manifest-model', 'model.address must be ${expected}', [426, 427, 428, 429]],
  ['meaning.graph.id is a registry id (lower-case letters, digits, single hyphens)', 'documents', 'manifest-meaning', 'meaning.graph.id must be a MeaningGraph registry id', [437, 507]],
  ['own form: meaning.graph.address is publisher.repository as an address, in any case', 'documents', 'manifest-meaning', 'derived from publisher.repository', [476, 477, 478]],
  ['shared form: meaning.graph.address, when given, is meaning.address without its pin', 'documents', 'manifest-meaning', 'leave meaning.graph.address out or make it the unpinned address', [510, 511]],
  ['each licence is one of 18 SPDX ids', 'documents', 'manifest-licence', 'must be a known SPDX licence id', [356, 360]],
  ['recordsets are names that look like ModelSpec entities', 'documents', 'manifest-recordsets', 'recordsets names must look like ModelSpec entity names', [525, 526]],
  ['every recordset page the template makes is a public https URL', 'documents', 'manifest-recordsets', 'the recordset page of', [528, 529, 530, 531]],
  ['OVDB.md has no key but ovdb and publish', 'documents', 'ovdbmd-keys', 'unknown frontmatter keys', [198, 199]],
  ['publish lists each manifest once', 'documents', 'ovdbmd-duplicate', 'publish lists ${entry} twice', [216, 217]],
  ['the repository can be read at HEAD (it is a git repository with a commit)', 'files', '', 'files.problem?.()', [186, 187]],
  ['OVDB.md is a tracked regular file', 'files', '', 'OVDB.md must be a tracked regular file', [188, 189]],
  ['OVDB.md can be read (and is not over 16 MB)', 'files', '', "problems: [`OVDB.md: ${error.message}`]", [194]],
  ['every manifest that OVDB.md lists is a tracked regular file', 'files', '', 'must be a tracked regular file, but it is', [221, 222, 223]],
  ['every manifest that OVDB.md lists can be read (and is not over 16 MB)', 'files', '', 'is not valid YAML', [244, 246]],
  ['every manifest that OVDB.md lists is checked', 'files', '', 'analyseManifest(path, files', [226]],
  ['every file a manifest names is a tracked regular file', 'files', '', 'which must be a tracked regular file', [391, 392]],
  ['every file a manifest names can be read (and is not over 16 MB)', 'files', '', 'bad(error.message)', [345, 347]],
  ['the model file is JSON with a module name and entities', 'files', '', 'is not a ModelSpec JSON file', [408, 410, 413, 415]],
  ['own form: model.name is the module of the model file', 'files', '', 'model.name !== moduleName', [420]],
  ['own form: the module of model.address is the model file\'s', 'files', '', 'this repository plus the module name in', [427, 429]],
  ['the meaning file is YAML whose id and license are the manifest\'s', 'files', '', 'but the meaning file\'s id is', [448, 451, 453, 455]],
  ['the meaning file\'s models: entry for the module is model.hcl', 'files', '', 'has no models: entry for module', [459, 461, 467, 468]],
  ['own form: recordsets are exactly the model\'s entities', 'files', '', 'recordsets lacks ModelSpec entities', [535, 538, 539]],
  ['publisher.repository is the repository the check is run in (the --repository option)', 'input', '', 'the repository this manifest is in', [309]],
];
// What the Publisher profile shares with the Directory's: every other refusal of the checker is one the Directory's rules make as well
// (the checker's directory-rules.mjs are copies of the Directory's), so it is a rule of the Directory profile.
const sharedWithDirectory = [
  [[197, 200, 202, 208, 213], 'the shape of OVDB.md: front matter, ovdb: 1, a non-empty publish list of ./ paths'],
  [[227], 'the problems of a listed manifest, passed on'],
  [[248, 258, 259, 265, 269, 273, 282, 286], 'the manifest is a mapping with its format, required texts, URLs, homepage and engine'],
  [[320, 330, 425, 482, 484, 494, 496, 498], 'the addresses: spelled as the Directory does, pinned in the shared form and not in the own'],
  [[359, 370, 372, 377, 379, 384, 503, 504, 509, 514, 521, 524], 'required fields, paths, the forms that never mix, recordsets listed once'],
];
for (const [id, , , snippet, cited] of publisherRules) {
  if (!cited.some((line) => chinookLines[line - 1]?.includes(snippet))) throw new Error(`ovdb-manifest.mjs at ${pins.chinookdb.commit}: none of lines ${cited.join(', ')} holds \`${snippet}\`, which is where the table of generate.mjs says the checker makes "${id}"`);
}
// Every refusal the checker can make is accounted for: a line with `bad(`, `problems.push(` or an early `return { problems` is in a
// rule above (the publisher's, or one that needs files or input) or in what the Directory's rules share.
{
  const accounted = new Set([...publisherRules.flatMap((rule) => rule[4]), ...sharedWithDirectory.flatMap((entry) => entry[0])]);
  const refusals = chinookLines.flatMap((line, at) => (/\bbad\(|problems\.push\(|return \{ problems:/.test(line) && !/const bad =/.test(line) ? [at + 1] : []));
  const missing = refusals.filter((line) => !accounted.has(line));
  if (missing.length > 0) throw new Error(`ovdb-manifest.mjs at ${pins.chinookdb.commit} refuses at lines ${missing.join(', ')}, which no rule of generate.mjs accounts for`);
}

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
  const key = JSON.stringify([base, patch, flags, family.startsWith('reader place: ') ? family : '']); // a place keeps its documents even when another family has them
  if (seenManifest.has(key)) return;
  seenManifest.add(key);
  manifestCases.push([base, patch, flags, familyIndex(family)]);
}
function addMd(family, base, text, path = 'ovdb.yaml', flags = '') {
  const patch = patchOf(bases[base], text);
  const key = JSON.stringify([base, patch, flags, path, family.startsWith('reader place: ') ? family : '']);
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

// Each `(m) => { ... }`, `(manifest) => { ... }` or `(doc) => { ... }` of the suites (a body of one line or of several, found by matching
// the braces), applied to an own and a shared manifest. Those that name something only the suite's own scope has (a helper, a
// constant) throw here and are skipped; the count is recorded.
let minedEdits = 0; let appliedEdits = 0;
const edits = new Set();
const bodyAt = (text, open) => { // the text between the brace at `open` and its match, or null
  let depth = 0;
  for (let at = open; at < text.length; at += 1) {
    if (text[at] === '{') depth += 1;
    else if (text[at] === '}' && (depth -= 1) === 0) return text.slice(open + 1, at);
  }
  return null;
};
for (const text of suites) {
  for (const match of text.matchAll(/\((m|manifest|doc)\) => \{/g)) {
    const body = bodyAt(text, match.index + match[0].length - 1);
    if (body !== null && body.length < 2000) edits.add(`${match[1]}\u0000${body}`);
  }
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

// ---- what only the publisher profile refuses ----

const licenceIds = chinook.licenceIds;
const caseVariants = (id) => [id, id.toLowerCase(), id.toUpperCase(), `${id} `, ` ${id}`, `${id}\n`];
const otherLicences = ['GPL-3.0-or-later', 'GPL-3.0', 'Apache 2.0', 'CC-BY-NC-4.0', 'Proprietary', 'LicenseRef-x', 'MIT OR Apache-2.0', 'cc0-1.0', 'mit', 'Mit', 'BSD-2-Clause-Patent', 'AGPL-3.0', 'LGPL-2.1-only', 'X'.repeat(64), 'X'.repeat(65)];
for (const [baseName, object] of [[ownJson, ownObject], [sharedJson, sharedObject]]) {
  const mutate = (family, change) => { const copy = clone(object); change(copy); addManifest(family, baseName, jsonOf(copy)); };
  // licences: each id in and out of the list, and in other letter case, in each of the three fields
  for (const field of ['data', 'model', 'meaning']) {
    for (const id of licenceIds) for (const value of baseName === ownJson ? caseVariants(id) : [id, id.toLowerCase()]) mutate('publisher: licence', (m) => { m.licences[field] = value; });
    for (const value of otherLicences) mutate('publisher: licence', (m) => { m.licences[field] = value; });
  }
  // discovery and the recordset page
  const origin = new URL(object.url).origin; const cloud = new URL(object.deployment.url).origin;
  for (const path of ['/.well-known/openvaultdb', '/.well-known/openvaultdb/', '/.well-known/openvaultdb2', '/.well-known/OpenVaultDB', '/.well-known/other', '/', '/x', '/.well-known/', '/openvaultdb', '/ovdb/.well-known/openvaultdb', '/.well-known/openvaultdb/x']) mutate('publisher: discovery', (m) => { m.deployment.discovery = `${origin}${path}`; });
  for (const host of ['cloud.example.org', 'ovdb.example.net', 'example.com', 'a.ovdb.example.com', 'github.com']) for (const field of ['discovery', 'recordset_page']) {
    mutate('publisher: origin', (m) => { m.deployment[field] = `https://${host}${field === 'discovery' ? '/.well-known/openvaultdb' : '/c/{name}'}`; });
  }
  for (const path of ['/c/{name}', '/{name}', '/c/{name}/x', '/c/{name}.html', '/a/b/{name}/c/d']) {
    mutate('publisher: origin', (m) => { m.deployment.recordset_page = `${cloud}${path}`; });
    mutate('publisher: origin', (m) => { m.deployment.recordset_page = `${origin}${path}`; });
  }
  // publisher.url and publisher.repository
  for (const url of ['https://github.com/datatug', 'https://github.com/other', 'https://github.com/datatug/', 'https://github.com/datatug/chinookdb', 'https://github.com/', 'https://github.com', 'https://gitlab.com/datatug', 'https://github.com.evil.example/datatug', 'https://www.github.com/datatug', 'https://github.com/DataTug', 'https://github.com/.', 'https://github.com/..', 'https://github.com/a/b/c', 'https://example.com/datatug', 'http://github.com/datatug', 'https://github.com/data~tug', 'https://github.com/acme', 'https://github.com/example_org', 'https://github.com/a.b-c_d', 'https://github.com/datatug.git']) mutate('publisher: owner', (m) => { m.publisher.url = url; });
  for (const repository of ['https://github.com/datatug/chinookdb', 'https://github.com/other/chinookdb', 'https://github.com/DataTug/chinookdb', 'https://github.com/datatug/ChinookDB', 'https://github.com/datatug/chinookdb/', 'https://github.com/datatug/chinookdb.git', 'https://gitlab.com/datatug/chinookdb', 'https://github.com/datatug', 'https://github.com/acme/chinook-hosting', 'https://github.com/example_org/chinook-hosting']) mutate('publisher: owner', (m) => { m.publisher.repository = repository; });
  mutate('publisher: owner', (m) => { delete m.publisher.repository; });
  mutate('publisher: owner', (m) => { delete m.publisher.url; });
  // id
  for (const id of ['Chinook', 'chinook_db', '-chinook', 'chinook-', 'chinook--db', 'chin-ook', '1', 'a'.repeat(80), 'a'.repeat(81), 'a-'.repeat(40) + 'a', 'chinook db', 'chinoók', 'CHINOOK', 'a--', '-', '']) mutate('publisher: id', (m) => { m.id = id; });
  // recordset names and the pages they make
  for (const names of [['1a'], ['a-b'], ['a b'], [''], ['é'], ['a.b'], ['_a'], ['a/b'], ['{name}'], ['A', 'a'], ['A', 'A '], ['a'.repeat(3000)], ['ok_1', 'Ok2', '_'], ['a?b'], ['a%41'], ['Album', 'Album2', '2Album']]) mutate('publisher: recordsets', (m) => { m.recordsets = names; });
  mutate('publisher: recordsets', (m) => { m.deployment.recordset_page = `${cloud}/c/${'x'.repeat(2030)}/{name}`; m.recordsets = ['Album']; });
  mutate('publisher: recordsets', (m) => { m.deployment.recordset_page = `${cloud}/c/${'x'.repeat(1990)}/{name}`; m.recordsets = ['Album', 'a'.repeat(60)]; });
  // keys: unknown, and the allowed ones at the wrong level
  for (const [path, key] of [[[], 'api_key'], [['deployment'], 'token'], [['model'], 'extra'], [['meaning'], 'extra'], [['meaning', 'graph'], 'secret'], [['publisher'], 'email'], [['licences'], 'extra'], [[], 'Format'], [[], 'recordsets_Partial'], [['deployment'], 'recordset_pages'], [['model'], 'modelspec2'], [['publisher'], 'repository2'], [[], '__proto__x'], [[], ''], [[], ' id']]) {
    mutate('publisher: keys', (m) => { let node = m; for (const step of path) node = node[step]; if (node !== undefined && node !== null && typeof node === 'object') node[key] = 'x'; });
  }
  for (const [level, key] of [['model', 'address'], ['meaning', 'address'], ['model', 'name'], ['meaning', 'file'], ['deployment', 'engine']]) mutate('publisher: keys', (m) => { m[key] = 'x'; delete m[level]?.[key]; });
}
// model.hcl, model.modelspec and the own-form names, in both spellings
for (const [baseName, object] of [[ownJson, ownObject], [sharedJson, sharedObject]]) {
  const mutate = (family, change) => { const copy = clone(object); change(copy); addManifest(family, baseName, jsonOf(copy)); };
  for (const value of ['model/chinook.modelspec.hcl', 'model/chinook.modelspec.json', 'model/chinook.hcl', 'model/x.modelspec.hcl', 'chinook.modelspec.hcl', '.modelspec.hcl', 'model/', 'model/*.modelspec.hcl', '/model/chinook.modelspec.hcl', '../chinook.modelspec.hcl', 3, ['x'], '', ' ', null, true, {}, 'model/chinook.modelspec.HCL', 'model/a b.modelspec.hcl']) mutate('publisher: model files', (m) => { m.model = { ...m.model, hcl: value }; });
  for (const value of ['model/chinook.modelspec.json', 'model/chinook.json', 'model/chinook.modelspec.hcl', '.modelspec.json', 'chinook.modelspec.json', '../x.modelspec.json', 3, '', null, ['x'], 'model/chinook.modelspec.JSON']) mutate('publisher: model files', (m) => { m.model = { ...m.model, modelspec: value }; });
  mutate('publisher: model files', (m) => { delete m.model.hcl; });
  mutate('publisher: model files', (m) => { delete m.model.modelspec; });
  mutate('publisher: model files', (m) => { delete m.model.hcl; delete m.model.modelspec; m.model.name = 'chinook'; });
  for (const value of ['model/chinook.meaning.yaml', 'chinook.meaning.yaml', 'model/', 'a//b', '../x.yaml', '/abs.yaml', 'model/*.yaml', 'a b.yaml', 3, '', null]) mutate('publisher: model files', (m) => { m.meaning = { ...m.meaning, file: value }; });
  // names and addresses
  for (const name of ['chinook', 'Chinook', 'other', '_x', 'x_', 'a1', '1a', 'a-b', '', null, 5]) mutate('publisher: model name', (m) => { m.model = { ...m.model, name }; });
  // without an address the name stands alone (an own manifest may leave model.address out)
  for (const name of ['chinook', 'Chinook', '_x', '_', 'x_', 'a1', '1a', 'a-b', 'a b', '\u00e9', '', null, 5, ['a'], true]) mutate('publisher: model name', (m) => { delete m.model.address; m.model = { ...m.model, name }; });
  const repo = (key) => (baseName === ownJson ? 'datatug/chinookdb' : key);
  const pins = baseName === ownJson ? '' : `?ref=${pinHex}`;
  for (const address of [`modelspec://github.com/${repo('datatug/chinookdb')}/chinook${pins}`, `modelspec://github.com/${repo('datatug/chinookdb')}/_chinook${pins}`, `modelspec://github.com/${repo('datatug/chinookdb')}/1chinook${pins}`, `modelspec://github.com/${repo('datatug/chinookdb')}/Chinook${pins}`, `modelspec://github.com/other/chinookdb/chinook${pins}`, 'modelspec://github.com/acme/chinook-hosting/chinook' + pins, 'modelspec://github.com/example_org/chinook-hosting/chinook' + pins, `modelspec://github.com/DataTug/chinookdb/chinook${pins}`, `modelspec://github.com/datatug/chinookdb/chinook?ref=${pinHex}`, 'modelspec://github.com/datatug/chinookdb/chinook', `modelspec://github.com/datatug/chinookdb.git/chinook${pins}`, `modelspec://github.com/a/./chinook${pins}`, `modelspec://example.com/datatug/chinookdb/chinook${pins}`]) {
    mutate('publisher: addresses', (m) => { m.model = { ...m.model, address }; });
    mutate('publisher: addresses', (m) => { m.model = { ...m.model, address, name: 'chinook' }; });
  }
  for (const address of [`meaning://github.com/datatug/chinookdb?ref=${pinHex}`, `meaning://github.com/acme/chinook-hosting?ref=${pinHex}`, `meaning://github.com/example_org/chinook-hosting?ref=${pinHex}`, 'meaning://github.com/datatug/chinookdb', `meaning://github.com/DataTug/chinookdb?ref=${pinHex}`, `meaning://github.com/a/b.git?ref=${pinHex}`, `meaning://github.com/./b?ref=${pinHex}`, `meaning://gitlab.com/a/b?ref=${pinHex}`]) mutate('publisher: addresses', (m) => { m.meaning = { ...m.meaning, address }; });
  for (const address of ['meaning://github.com/datatug/chinookdb', 'meaning://github.com/DataTug/ChinookDB', 'meaning://github.com/datatug/chinookdb/', 'meaning://github.com/other/chinookdb', `meaning://github.com/datatug/chinookdb?ref=${pinHex}`, 'meaning://github.com/acme/chinook-hosting', 'https://github.com/datatug/chinookdb', 'meaning://', 5, '', null]) mutate('publisher: graph', (m) => { m.meaning = { ...m.meaning, graph: { ...m.meaning.graph, address } }; });
  for (const id of ['chinook', 'Chinook', 'chinook-1', 'chinook_1', '-x', 'x-', 'x--y', '1', 'a'.repeat(100), '', null, 5, 'é']) mutate('publisher: graph', (m) => { m.meaning = { ...m.meaning, graph: { ...m.meaning.graph, id } }; });
}
// OVDB.md: unknown keys and publish entries that repeat, or nearly
{
  const entries = ['./ovdb.yaml', './ovdb.yaml ', './Ovdb.yaml', './a/../ovdb.yaml', './ovdb.yaml/', './x/ovdb.yaml', './ovdb.yaml#a', ' ./ovdb.yaml', './/ovdb.yaml', './sub/../ovdb.yaml', 'ovdb.yaml', '././ovdb.yaml'];
  for (const first of ['./ovdb.yaml', './b.yaml']) for (const entry of entries) addMd('publisher: publish', mdName, front(`ovdb: 1\npublish: [${first}, "${entry}"]`));
  addMd('publisher: publish', mdName, front('ovdb: 1\npublish: [./a.yaml, ./b.yaml, ./a.yaml, ./b.yaml, ./c.yaml]'));
  addMd('publisher: publish', mdName, front('ovdb: 1\npublish:\n  - ./ovdb.yaml\n  - ./ovdb.yaml\n  - ./ovdb.yaml'));
  for (const key of ['token', 'Ovdb', 'publish2', 'ovdb ', '', 'null', '_', 'x'.repeat(100), 'ovdb_version', 'format', 'title', 'description', 'license', 'version', 'url']) {
    addMd('publisher: keys', mdName, front(`ovdb: 1\npublish: [./ovdb.yaml]\n${key === '' ? '""' : key.includes(' ') ? `"${key}"` : key}: abc`));
    addMd('publisher: keys', mdName, front(`${key === '' ? '""' : key.includes(' ') ? `"${key}"` : key}: abc\novdb: 1\npublish: [./ovdb.yaml]`));
  }
  for (const value of ['1', '{a: b}', '[1, 2]', '~', '""', 'true']) addMd('publisher: keys', mdName, front(`ovdb: 1\npublish: [./ovdb.yaml]\nextra: ${value}`));
}

// ---- case mapping, look-alikes, and the edges of the publisher rules ----

// Go and JavaScript map case differently outside ASCII (U+0130 lower-cases to i in Go and to i and a combining dot in JavaScript, the
// Kelvin sign to k in both, U+00DF and the sigmas by context): every string that either checker compares or lower-cases gets each
// of these in the place of its first letter of interest, and after its last character.
const lookAlikes = ['İ', 'ı', 'ſ', 'K', 'ß', 'Σ', 'ς', 'Ａ', 'ａ', 'é'];
const compared = [['id'], ['licences', 'data'], ['licences', 'model'], ['licences', 'meaning'], ['model', 'name'], ['model', 'address'], ['meaning', 'address'], ['meaning', 'graph', 'address'], ['meaning', 'graph', 'id'], ['publisher', 'url'], ['publisher', 'repository'], ['deployment', 'discovery'], ['deployment', 'recordset_page'], ['model', 'hcl'], ['model', 'modelspec'], ['meaning', 'file']];
for (const [baseName, object] of [[ownJson, ownObject], [sharedJson, sharedObject]]) {
  const mutate = (family, change) => { const copy = clone(object); change(copy); addManifest(family, baseName, jsonOf(copy)); };
  const leaf = (path) => path.reduce((value, key) => value?.[key], object);
  for (const path of compared) {
    const value = leaf(path);
    if (typeof value !== 'string') continue;
    let at = value.search(/[iksIKS]/);
    if (at < 0) at = value.search(/[A-Za-z]/);
    for (const c of lookAlikes) {
      mutate('unicode: case', (m) => { parentOf(m, path)[path.at(-1)] = `${value.slice(0, at)}${c}${value.slice(at + 1)}`; });
      mutate('unicode: case', (m) => { parentOf(m, path)[path.at(-1)] = `${value}${c}`; });
    }
  }
  const names = object.recordsets[0];
  for (const c of lookAlikes) mutate('unicode: case', (m) => { m.recordsets = [`${names}${c}`, ...m.recordsets.slice(1)]; });
}
// The reviewer's input and its neighbours: an own-form manifest of kitchen/sink, the address spelled with each look-alike in the place of each letter.
{
  const kitchen = (address) => { const m = clone(ownObject); m.publisher.url = 'https://github.com/kitchen'; m.publisher.repository = 'https://github.com/kitchen/sink'; m.model.address = 'modelspec://github.com/kitchen/sink/chinook'; m.meaning.graph.address = address; return m; };
  const address = 'meaning://github.com/kitchen/sink';
  addManifest('unicode: kitchen', ownJson, jsonOf(kitchen(address)));
  addManifest('unicode: kitchen', ownJson, jsonOf(kitchen(address.toUpperCase())));
  addManifest('unicode: kitchen', ownJson, jsonOf(kitchen('meaning://github.com/KITCHEN/Sink')));
  for (let at = 'meaning://'.length; at < address.length; at += 1) {
    if (!/[A-Za-z]/.test(address[at])) continue;
    for (const c of lookAlikes) addManifest('unicode: kitchen', ownJson, jsonOf(kitchen(`${address.slice(0, at)}${c}${address.slice(at + 1)}`)));
  }
  // and a repository that itself holds a letter that look-alikes fold onto
  for (const c of lookAlikes) {
    const m = kitchen(address); m.meaning.graph.address = `meaning://github.com/k${c}tchen/sink`; addManifest('unicode: kitchen', ownJson, jsonOf(m));
  }
}
// The scheme of an own-form meaning.graph.address: the checker compares in lower case, the Directory wants the literal scheme.
for (const address of ['MEANING://github.com/datatug/chinookdb', 'Meaning://github.com/datatug/chinookdb', 'meaning://GitHub.com/DATATUG/ChinookDB', 'meaning:/github.com/datatug/chinookdb', ' meaning://github.com/datatug/chinookdb', 'meaning://github.com/datatug/chinookdb ', 'MEANING://GITHUB.COM/DATATUG/CHINOOKDB']) {
  const m = clone(ownObject); m.meaning.graph.address = address; addManifest('publisher: graph address scheme', ownJson, jsonOf(m));
}
// A publisher.repository at and over its bound (255 bytes), in both forms, with everything else consistent.
for (const length of [226, 227, 228, 229, 230, 262, 1000]) {
  const shared = clone(sharedObject); shared.publisher.url = 'https://github.com/hoster'; shared.publisher.repository = `https://github.com/hoster/${'r'.repeat(length - 'https://github.com/hoster/'.length)}`;
  addManifest('publisher: repository length', sharedJson, jsonOf(shared));
  const own = clone(ownObject); const name = 'r'.repeat(length - 'https://github.com/datatug/'.length);
  own.publisher.repository = `https://github.com/datatug/${name}`; own.model.address = `modelspec://github.com/datatug/${name}/chinook`; own.meaning.graph.address = `meaning://github.com/datatug/${name}`;
  addManifest('publisher: repository length', ownJson, jsonOf(own));
}
// An owner that is the beginning of another, either way round.
for (const [url, repository] of [['https://github.com/data', 'https://github.com/datatug/chinookdb'], ['https://github.com/datatug-x', 'https://github.com/datatug/chinookdb'], ['https://github.com/datatug', 'https://github.com/datatug-x/chinookdb'], ['https://github.com/datatug', 'https://github.com/datatug/chinookdb'], ['https://github.com/dat', 'https://github.com/dat/chinookdb']]) {
  const m = clone(ownObject); m.publisher.url = url; m.publisher.repository = repository; addManifest('publisher: owner boundary', ownJson, jsonOf(m));
  const shared = clone(sharedObject); shared.publisher.url = url; shared.publisher.repository = repository; addManifest('publisher: owner boundary', sharedJson, jsonOf(shared));
}
// A recordset page whose template is within the bound and whose expansion is not (the names are longer than {name}).
{
  const cloud = new URL(ownObject.deployment.url).origin; const stem = `${cloud}/c/`;
  for (const [total, name] of [[2048, 'InvoiceLine'], [2043, 'InvoiceLine'], [2042, 'InvoiceLine'], [2048, 'Album'], [2040, 'A'.repeat(20)]]) {
    const m = clone(ownObject); m.deployment.recordset_page = `${stem}${'x'.repeat(total - stem.length - '{name}'.length)}{name}`; m.recordsets = [name, ...ownObject.recordsets.filter((entry) => entry !== name).slice(0, 2)];
    addManifest('publisher: expansion', ownJson, jsonOf(m));
  }
}

// ---- the reader's raising places ----
//
// Every place in the reader (meaninggraph/cli pkg/meaning, yaml.go and yaml_flow.go) that raises a refusal is a row of the
// table of reader_places_test.go, named by file:line (and, for a function that several places call, by the caller). Here each
// place gets documents of its own, in the family `reader place: <id>`: the real Chinook manifest (the only base the Chinook
// checker accepts) with one line replaced, as the Directory profile and the Publisher profile are both judged on it, and the
// same on an extra unknown key, which only the Directory accepts. The test says which of them the reference accepts.
{
  const place = (id, ...docs) => { for (const [base, text, flags] of docs) addManifest(`reader place: ${id}`, base, text, flags ?? ''); };
  const hosts = (id, snippet, flags = '') => {
    // snippet: lines whose first key is written K; K becomes title (an allowed key) or extra (an unknown key, for the Directory profile).
    for (const name of [chinookYaml, hosterYaml]) {
      const text = bases[name];
      const asTitle = snippet.replace(/\bK\b/, 'title');
      const asExtra = snippet.replace(/\bK\b/, 'extra');
      place(id, [name, text.replace(/^title:.*$/m, asTitle), flags], [name, text.replace(/^title:.*$/m, (line) => `${line}\n${asExtra}`), flags]);
    }
  };
  const publisherBlock = /^publisher:\n  name: .*\n  url: .*\n  repository: .*\n/m;
  const publisherAs = (id, make) => {
    for (const name of [chinookYaml, hosterYaml]) {
      const text = bases[name];
      const m = text.match(/^publisher:\n  name: (.*)\n  url: (.*)\n  repository: (.*)\n/m);
      place(id, [name, text.replace(publisherBlock, make({ name: m[1], url: m[2], repository: m[3] }))]);
    }
  };
  const whole = (id, make, flags = '') => { for (const name of [chinookYaml, hosterYaml]) place(id, [name, make(bases[name]), flags]); };
  const ownOnly = (id, make, flags = '') => place(id, [chinookYaml, make(bases[chinookYaml]), flags]);
  const mdPlace = (id, text, flags = '') => addMd(`reader place: ${id}`, chinookMd, text, 'ovdb.yaml', flags);
  const nest = (depth, open, close, inner = '') => `${open.repeat(depth)}${inner}${close.repeat(depth)}`;

  // --- yaml.go: the characters, the lines and the document ---
  whole('yaml.go:148', (t) => t.replace(/^(#.*\n|\n)+/, '').replace(/^(.*)$/gm, (line) => (line === '' ? line : `  ${line}`)), 'bom');
  hosts('yaml.go:171', 'K: caf\u00e9', 'latin1'); whole('yaml.go:171', (t) => `${t}# \u00ff\n`, 'latin1');
  hosts('yaml.go:173', 'K: a\u0000b'); whole('yaml.go:173', (t) => `${t}\u0000`);
  hosts('yaml.go:178', 'K: a\rb');
  hosts('yaml.go:182', 'K: a\u0085b'); hosts('yaml.go:182', 'K: a\u2028b'); hosts('yaml.go:182', 'K: a\u007fb');
  hosts('yaml.go:295', 'K: Chinook\t'); hosts('yaml.go:295', 'K:\u0020Chinook # c\t');
  hosts('yaml.go:320', `K: ${nest(70, '[', ']')}`); hosts('yaml.go:320', `K:\n${Array.from({ length: 70 }, (_, i) => `${'  '.repeat(i + 1)}a:`).join('\n')} x`);
  whole('yaml.go:332', (t) => `%YAML 1.2\n---\n${t}`); whole('yaml.go:332', (t) => `${t}%TAG ! tag:x,2000:\n`);
  whole('yaml.go:341', (t) => `---\t\n${t}`); whole('yaml.go:341', (t) => `---\t# c\n${t}`);
  whole('yaml.go:345', (t) => `--- &doc\n${t}`); whole('yaml.go:345', (t) => `--- !!map\n${t}`); whole('yaml.go:345', (t) => `--- text\n${t}`);
  whole('yaml.go:352', (t) => `${t}...\n`); whole('yaml.go:352', (t) => `${t}---\nformat: x\n`); whole('yaml.go:352', (t) => `---\n${t}...\n`);
  whole('yaml.go:365', (t) => `${t.replace(/^(.+)$/gm, '  $1')}format: x\n`); whole('yaml.go:365', (t) => `${t.replace(/^(.+)$/gm, '    $1')}  format: x\n`);
  hosts('yaml.go:399', 'K:\n# c\n  Chinook'); hosts('yaml.go:399', 'K: # c\n  # d\n  Chinook');
  // --- yaml.go: block mappings and sequences ---
  whole('yaml.go:462', (t) => t.replace(/^title:.*$/m, 'title: "Chinook"\n    extra: x')); whole('yaml.go:462', (t) => t.replace(/^title:.*$/m, "title: 'Chinook'\n    extra: x")); whole('yaml.go:462', (t) => t.replace(/^( {2}url: .*)$/m, '$1\n     extra: x'));
  whole('yaml.go:466', (t) => t.replace(/^title:.*$/m, 'title: Chinook\n- a')); whole('yaml.go:466', (t) => t.replace(/^( {2}url: .*)$/m, '$1\n  - a'));
  whole('yaml.go:473', (t) => t.replace(/^title:.*$/m, 'title: Chinook\nnotakey')); whole('yaml.go:473', (t) => t.replace(/^( {2}url: .*)$/m, '$1\n  notakey'));
  whole('yaml.go:476', (t) => t.replace(/^title:.*$/m, 'title: a\ntitle: b'));
  whole('yaml.go:509', (t) => t.replace(/^( {2}- Album)$/m, '$1\n      - x')); whole('yaml.go:509', (t) => t.replace(/^( {2}- Album)$/m, '  - "Album"\n      - x'));
  whole('yaml.go:436', (t) => t.replace(/^recordsets:\n( {2}- Album)$/m, 'recordsets:\n  -\tAlbum')); whole('yaml.go:436', (t) => t.replace(/^recordsets:\n( {2}- Album)$/m, 'recordsets:\n-\tAlbum'));
  whole('yaml.go:464', (t) => t.replace(/^title:.*$/m, 'title: Chinook\n-\tx'));
  whole('yaml.go:511', (t) => t.replace(/^( {2}- Artist)$/m, '  -\tArtist')); whole('yaml.go:511', (t) => t.replace(/^( {2}- Album)$/m, '  - Album\n  -\tx'));
  hosts('yaml.go:557', '"K"\t: Chinook'); hosts('yaml.go:563', '"K":\tChinook'); hosts('yaml.go:586', 'K:\tChinook');
  hosts('yaml.go:571', '&k K: Chinook'); hosts('yaml.go:571', '*k K: Chinook');
  hosts('yaml.go:573', '!!str K: Chinook'); hosts('yaml.go:573', '!x K: Chinook');
  hosts('yaml.go:578', '? K\n: Chinook'); hosts('yaml.go:578', '?\n  K\n: Chinook');
  place('yaml.go:594', ...[chinookYaml, hosterYaml].flatMap((n) => ['2024', 'true', 'null', '~', '0x1F', '.inf', '1e3'].map((k) => [n, `${bases[n]}${k}: x\n`])));
  place('yaml.go:594', ...[chinookYaml, hosterYaml].flatMap((n) => ['2024', 'true', 'null'].map((k) => [n, bases[n].replace(/^title:.*$/m, `${k}: x\ntitle: Chinook`)])));
  hosts('yaml.go:628', `K${' '.repeat(1100)}: Chinook`);
  place('yaml.go:628', ...[chinookYaml, hosterYaml].map((n) => [n, `${bases[n]}${'k'.repeat(1100)}: x\n`]), ...[chinookYaml, hosterYaml].map((n) => [n, `${bases[n]}"${'k'.repeat(1100)}": x\n`]));
  place('yaml.go:630', ...[chinookYaml, hosterYaml].map((n) => [n, `${bases[n]}<<: x\n`]), ...[chinookYaml, hosterYaml].map((n) => [n, `${bases[n]}"<<": x\n`]));
  // --- yaml.go: values ---
  hosts('yaml.go:643', 'K:  \tChinook');
  hosts('yaml.go:646', 'K:\n  >\n  Chinook'); hosts('yaml.go:646', 'K:\n  |\n  Chinook');
  hosts('yaml.go:654', 'K: &a Chinook'); hosts('yaml.go:654', 'K: *a');
  hosts('yaml.go:656', 'K: !!str Chinook'); hosts('yaml.go:656', 'K: !x Chinook');
  for (const c of ['@', '`', '%', ',', ']', '}']) hosts('yaml.go:658', `K: ${c}x`);
  hosts('yaml.go:661', 'K: -\tx'); hosts('yaml.go:661', 'K: ?\tx'); hosts('yaml.go:661', 'K: :\tx');
  hosts('yaml.go:664', 'K: - x'); hosts('yaml.go:664', 'K: ? x'); hosts('yaml.go:664', 'K: : x'); hosts('yaml.go:664', 'K: -');
  for (const c of ['[', ']', '{', '}', ',', '&', '*', '!', '|', '>', "'", '"', '%', '@', '`']) hosts('yaml.go:696', `K: A music store,\n  ${c}the${c === '[' ? ']' : c === '{' ? '}' : ''} one`);
  hosts('yaml.go:735', 'K: a: b'); hosts('yaml.go:735', 'K: a:'); hosts('yaml.go:735', 'K: Chinook\n  music: store');
  // the numbers of resolvePlain, from a block value, a block key, a flow value and a flow key; and in OVDB.md
  const numbers = [['yaml.go:784', '9007199254740993'], ['yaml.go:788', '0x1F'], ['yaml.go:788', '0o17'], ['yaml.go:790', '.inf'], ['yaml.go:790', '.nan'], ['yaml.go:794', '1e999']];
  for (const [id, n] of numbers) {
    hosts(`${id}@block value`, `K: ${n}`);
    place(`${id}@block key`, ...[chinookYaml, hosterYaml].map((b) => [b, `${bases[b]}${n}: x\n`]));
    whole(`${id}@flow value`, (t) => t.replace(/^title:.*$/m, `title: {a: ${n}}`)); whole(`${id}@flow value`, (t) => `${t}extra: [${n}]\n`);
    whole(`${id}@flow key`, (t) => `${t}extra: {${n}: x}\n`);
    mdPlace(`${id}@block value`, `---\novdb: ${n}\npublish: [./ovdb.yaml]\n---\n`);
    mdPlace(`${id}@flow value`, `---\novdb: 1\npublish: [./ovdb.yaml, ${n}]\n---\n`);
  }
  mdPlace('yaml.go:788@block value', '---\novdb: 0x1\npublish: [./ovdb.yaml]\n---\n'); mdPlace('yaml.go:788@block value', '---\novdb: 0o1\npublish: [./ovdb.yaml]\n---\n');
  hosts('yaml.go:825@quoted value', 'K: "Chinook" music'); hosts('yaml.go:825@quoted value', 'K: "Chinook"# c'); hosts('yaml.go:825@quoted value', "K: 'Chinook' x");
  hosts('yaml.go:825@block scalar header', 'K: >x\n  Chinook'); hosts('yaml.go:825@block scalar header', 'K: |-x\n  Chinook');
  hosts('yaml.go:825@flow', 'K: {a: b} x'); hosts('yaml.go:825@flow', 'K: [a] x');
  // the quoted scalars of scanQuoted, from a block value, a flow value and a block key (where a failure only says "not a key")
  const quoted = [
    ['yaml.go:864', (q) => `${q}Chinook\n  music${q}`], ['yaml.go:871', () => 'Chinook \\\n  music'], ['yaml.go:878', () => 'a\\qb'], ['yaml.go:878', () => 'a\\zb'],
    ['yaml.go:882', () => 'a\\x4g'], ['yaml.go:882', () => 'a\\u12'], ['yaml.go:892', () => 'a\\ud83cb'], ['yaml.go:892', () => 'a\\ud83c\\u0041'], ['yaml.go:897', () => 'a\\udc00b'], ['yaml.go:897', () => 'a\\U00110000b'], ['yaml.go:897', () => 'a\\UFFFFFFFFb'],
  ];
  // A quoted scalar that is a block key never raises (splitKey drops the error: the line is then not a key line, and the mapping says so),
  // so there is no row for it. In a flow collection the text of publisher.name is a quoted scalar that the checker accepts.
  for (const [id, make] of quoted) {
    if (id === 'yaml.go:864') {
      for (const q of ['"', "'"]) {
        hosts(`${id}@block value`, `K: ${make(q)}`);
        publisherAs(`${id}@flow`, (p) => `publisher: {name: ${make(q)}, url: ${p.url}, repository: ${p.repository}}\n`);
        whole(`${id}@flow`, (t) => `${t}extra: [${make(q)}]\n`);
      }
      continue;
    }
    hosts(`${id}@block value`, `K: "${make()}"`);
    publisherAs(`${id}@flow`, (p) => `publisher: {name: "${make()}", url: ${p.url}, repository: ${p.repository}}\n`);
    whole(`${id}@flow`, (t) => `${t}extra: ["${make()}"]\n`); whole(`${id}@flow`, (t) => `${t}extra: {"${make()}": x}\n`);
  }
  // --- yaml_flow.go: block scalars and flow collections ---
  hosts('yaml_flow.go:14', 'K: >+\n  Chinook'); hosts('yaml_flow.go:14', 'K: |+\n  Chinook');
  hosts('yaml_flow.go:20', 'K: >2\n   Chinook'); hosts('yaml_flow.go:20', 'K: |1\n  Chinook');
  hosts('yaml_flow.go:51', 'K: |\n  Chinook\n      \n  music'); hosts('yaml_flow.go:51', 'K: >-\n  Chinook\n     \n  music');
  whole('yaml_flow.go:169', (t) => `${t}extra: [a, b\n`); whole('yaml_flow.go:169', (t) => `${t}extra: {a: b\n`);
  publisherAs('yaml_flow.go:178', (p) => `publisher: {name: ${p.name},\n  # c\n  url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:186', (p) => `publisher: {name: ${p.name}, # c\n  url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:182', (p) => `publisher: {name: ${p.name},\nurl: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:182', (p) => `publisher: {name: ${p.name}, url: ${p.url},\n repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:202', (p) => `publisher: {name: &a ${p.name}, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:204', (p) => `publisher: {name: !!str ${p.name}, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:206', (p) => `publisher: {name: ${p.name}, , url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:206', (p) => `publisher: {name: |x, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:206', (p) => `publisher: {name: ${p.name}, url: ${p.url}, repository: ${p.repository},}\n`);
  publisherAs('yaml_flow.go:206', (p) => `publisher: {name: @x, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:210', (p) => `publisher: {name: - x, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:210', (p) => `publisher: {name: ? x, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:210', (p) => `publisher: {name: : x, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:240', (p) => `publisher: {name: Data\tTug, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:240', (p) => `publisher: {name:\t${p.name}, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:254', (p) => `publisher: {name: ${p.name.slice(0, 3)}\n  ${p.name.slice(3)}, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:254', (p) => `publisher: {name: ${p.name}\n  x, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:256', (p) => `publisher: {name: "${p.name}" x, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:256', (p) => `publisher: {name: ${p.name} url: ${p.url}, repository: ${p.repository}}\n`);
  whole('yaml_flow.go:256', (t) => t.replace(/^recordsets:\n( {2}- .*\n)+/m, 'recordsets: ["Album" "Artist"]\n'));
  hosts('yaml_flow.go:261', `K: ${nest(70, '{a: ', '}', '1')}`); hosts('yaml_flow.go:261', `K: ${nest(70, '[', ']')}`);
  hosts('yaml_flow.go:286', 'K: [a: b]'); hosts('yaml_flow.go:286', 'K: [a: b, c]');
  whole('yaml_flow.go:286', (t) => t.replace(/^recordsets:\n( {2}- .*\n)+/m, 'recordsets: [Album: x]\n'));
  hosts('yaml_flow.go:306', 'K: {a: 1, a: 2}'); publisherAs('yaml_flow.go:306', (p) => `publisher: {name: ${p.name}, name: x, url: ${p.url}, repository: ${p.repository}}\n`);
  hosts('yaml_flow.go:310', 'K: {a, b: c}'); hosts('yaml_flow.go:310', 'K: {a: 1, b}');
  publisherAs('yaml_flow.go:310', (p) => `publisher: {name: ${p.name}, url: ${p.url}, repository: ${p.repository}, name2}\n`);
  whole('yaml_flow.go:310', (t) => t.replace(/^deployment:\n[\s\S]*?recordset_page: .*\n/m, 'deployment: {url: https://cloud.openvaultdb.com/ovdb/dbs/chinook, engine: sqlite, discovery: https://chinookdb.com/.well-known/openvaultdb, recordset_page}\n'));
  // a key with no value in a flow mapping is null: for each key of the own manifest, the whole document as a flow mapping without that value
  {
    const flowOf = (value, omit, path = []) => `{${Object.entries(value).map(([key, v]) => {
      const here = [...path, key].join('.');
      if (here === omit) return JSON.stringify(key);
      return `${JSON.stringify(key)}: ${v !== null && typeof v === 'object' && !Array.isArray(v) ? flowOf(v, omit, [...path, key]) : JSON.stringify(v)}`;
    }).join(', ')}}`;
    const object = ownObject;
    for (const omit of paths(object).map((path) => path.join('.'))) place('yaml_flow.go:310', [chinookYaml, `${flowOf(object, omit)}\n`]);
  }
  hosts('yaml_flow.go:342', 'K: {&a b: c}'); publisherAs('yaml_flow.go:342', (p) => `publisher: {&a name: ${p.name}, url: ${p.url}, repository: ${p.repository}}\n`);
  hosts('yaml_flow.go:344', 'K: {!!str b: c}'); publisherAs('yaml_flow.go:344', (p) => `publisher: {!!str name: ${p.name}, url: ${p.url}, repository: ${p.repository}}\n`);
  for (const c of ['[a]', '{a: b}', '|', '>', '%x', '@x', '`x', ',', ']']) hosts('yaml_flow.go:346', `K: {${c}: x}`);
  publisherAs('yaml_flow.go:346', (p) => `publisher: {[name]: ${p.name}, url: ${p.url}, repository: ${p.repository}}\n`);
  for (const k of ['2024', 'true', 'null', '~', '? a', '?']) hosts('yaml_flow.go:354', `K: {${k}: x}`);
  publisherAs('yaml_flow.go:354', (p) => `publisher: {? name: ${p.name}, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml_flow.go:354', (p) => `publisher: {? name : ${p.name}, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml.go:628@flow key', (p) => `publisher: {name${' '.repeat(1100)}: ${p.name}, url: ${p.url}, repository: ${p.repository}}\n`);
  publisherAs('yaml.go:628@flow key', (p) => `publisher: {name: ${p.name}, url: ${p.url}, repository: ${p.repository}, ${'k'.repeat(1100)}: x}\n`);
  hosts('yaml.go:630@flow key', 'K: {<<: x}');
  // The three inputs of the review of 3c7ef0b, on its kitchen/sink manifest: a plain value that continues with `*`, `"` or `[`, and the two flow
  // forms of publisher with a key that the checker allows (an explicit key; a key written over 1024 bytes).
  {
    const kitchen = clone(ownObject); kitchen.publisher = { name: 'Kitchen', url: 'https://github.com/kitchen', repository: 'https://github.com/kitchen/sink' };
    kitchen.model.address = 'modelspec://github.com/kitchen/sink/chinook'; kitchen.meaning.graph.address = 'meaning://github.com/kitchen/sink'; kitchen.description = 'A music store, one';
    const text = stringifyYaml(kitchen);
    for (const continuation of ['*the* one', '"store" one', '[beta] one']) place('yaml.go:696', [chinookYaml, text.replace(/^description: .*$/m, `description: A music store,\n  ${continuation}`)]);
    const flow = (key) => `publisher: {${key}: Kitchen, url: https://github.com/kitchen, repository: https://github.com/kitchen/sink}\n`;
    const publisher = /^publisher:\n  name: .*\n  url: .*\n  repository: .*\n/m;
    place('yaml_flow.go:354', [chinookYaml, text.replace(publisher, flow('? name'))]);
    place('yaml.go:628@flow key', [chinookYaml, text.replace(publisher, flow(`name${' '.repeat(1100)}`))]);
  }
  // OVDB.md front matter, the same reader (OVDB.md allows ovdb and publish only)
  mdPlace('yaml.go:696', '---\novdb: 1\npublish: [./ovdb.yaml]\n---\n'.replace('ovdb: 1', 'ovdb: 1\nx: a b,\n  *c* d'));
  mdPlace('yaml.go:864@block value', '---\novdb: 1\nx: "a\n  b"\npublish: [./ovdb.yaml]\n---\n');
  mdPlace('yaml_flow.go:254', '---\novdb: 1\npublish: [./ovdb.yaml,\n  ./a\n  .yaml]\n---\n'); mdPlace('yaml_flow.go:310', '---\novdb: 1\npublish: [./ovdb.yaml]\nx: {a}\n---\n');
}

// ---- the goldens ----

const manifestBuffers = manifestCases.map(([base, patch, flags]) => flagged(applyPatch(bases[base], patch), flags));
const mdBuffers = mdCases.map(([base, patch, flags]) => flagged(applyPatch(bases[base], patch), flags));
const verdicts = {
  manifest: manifestBuffers.map((buffer) => manifestVerdict(buffer)).join(''),
  md: mdBuffers.map((buffer, at) => mdVerdict(buffer, mdCases[at][3])).join(''),
};
const directoryThrown = thrown;
thrown = 0;
// The publisher reference: the verdict of every case, and what a refusal needs.
const publisherManifest = manifestBuffers.map((buffer) => publisherVerdict(publisherManifestHeld(buffer)));
const publisherMd = mdBuffers.map((buffer) => publisherVerdict(publisherMdHeld(buffer)));
const publisherThrown = thrown;
// A sample of the cases again on real repositories: the in-memory repository must not change a verdict.
let realChecked = 0;
const sampled = (buffers, held, step) => buffers.forEach((buffer, at) => {
  if (at % step !== 0) return;
  const repositories = held(buffer);
  for (const kind of repositoryKinds) {
    const real = realProblems(repositories[kind]);
    if (real === null) continue;
    if ((real.length === 0) !== (publisherProblems(repositories[kind]).length === 0)) throw new Error(`the checker refuses differently on a real repository and on one in memory (${kind}) for ${JSON.stringify(decoded(buffer)).slice(0, 300)}: ${real.join('; ')}`);
    realChecked += 1;
  }
});
sampled(manifestBuffers, publisherManifestHeld, 150);
sampled(mdBuffers, publisherMdHeld, 12);
const needsCounts = (pairs) => {
  const counts = {};
  for (const [verdict, needs] of pairs) if (verdict === '1' && needs) for (const name of needs.split('; ')) counts[name] = (counts[name] ?? 0) + 1;
  return Object.fromEntries(Object.entries(counts).sort());
};
const needsFiles = { manifest: needsCounts(publisherManifest), md: needsCounts(publisherMd) };
for (const name of [...Object.keys(needsFiles.manifest), ...Object.keys(needsFiles.md)]) if (name.startsWith('unclassified')) throw new Error(`a refusal that needs other files is not in a class of generate.mjs: ${name}`);
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
  thrown: directoryThrown,
  manifest: { accepted: count(verdicts.manifest, '1'), refused: count(verdicts.manifest, '0'), verdicts: verdicts.manifest },
  md: { accepted: count(verdicts.md, '1'), refused: count(verdicts.md, '0'), verdicts: verdicts.md },
};
// The real repository of the references: the checker accepts the real documents, read from the real git history.
{
  const real = chinook.checkOvdbManifest(chinook.gitRepoFiles(chinookRoot), { repository: 'https://github.com/datatug/chinookdb' });
  if (real.length > 0) throw new Error(`the Chinook checker refuses the Chinook repository at ${pins.chinookdb.commit}: ${real.join('; ')}`);
}
const publisherVerdicts = { manifest: publisherManifest.map(([verdict]) => verdict).join(''), md: publisherMd.map(([verdict]) => verdict).join('') };
const publisherVerdictFile = {
  ...meta,
  profile: 'publisher',
  thrown: publisherThrown,
  realChecked,
  manifest: { accepted: count(publisherVerdicts.manifest, '1'), refused: count(publisherVerdicts.manifest, '0'), verdicts: publisherVerdicts.manifest },
  md: { accepted: count(publisherVerdicts.md, '1'), refused: count(publisherVerdicts.md, '0'), verdicts: publisherVerdicts.md },
  needsFiles,
  allowLists: { licences: [...chinook.licenceIds].sort(), keys: Object.fromEntries(Object.entries(chinookAllowedKeys).map(([where, keys]) => [where, [...keys].sort()])) },
  rules: publisherRules.map(([id, who, go, , lines]) => ({ id, who, go, lines })),
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
// The same under the publisher reference: for each case that it accepts, the values factsOf derives, as the difference from the
// facts of its base when the publisher accepts the base (else all of them), and the set each accepted OVDB.md lists.
const publisherBaseFacts = {};
for (const name of Object.keys(baseFacts)) {
  if (publisherVerdict(heldOf(manifestRepository, bases[name]))[0] === '1') publisherBaseFacts[name] = baseFacts[name];
}
const publisherDeltas = [];
manifestCases.forEach(([base], at) => {
  if (publisherVerdicts.manifest[at] !== '1') return;
  const facts = factsOfBuffer(manifestBuffers[at]);
  const delta = {};
  for (const [key, value] of Object.entries(facts)) if (JSON.stringify(value) !== JSON.stringify(publisherBaseFacts[base]?.[key])) delta[key] = value;
  publisherDeltas.push(delta);
});
const publisherLists = [];
mdCases.forEach((_, at) => { if (publisherVerdicts.md[at] === '1') publisherLists.push(mdEntries(mdBuffers[at])); });
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
const publisherVerdictText = `${JSON.stringify(publisherVerdictFile, null, 1)}\n`;
const publisherFactsText = [
  '{',
  ...Object.entries({ ...meta, profile: 'publisher' }).map(([key, value]) => `  ${JSON.stringify(key)}: ${JSON.stringify(value)},`),
  '  "bases": {', Object.entries(publisherBaseFacts).map(([name, facts]) => `    ${JSON.stringify(name)}: ${JSON.stringify(facts)}`).join(',\n'), '  },',
  '  "manifest": [', publisherDeltas.map((entry) => `    ${JSON.stringify(entry)}`).join(',\n'), '  ],',
  '  "md": [', publisherLists.map((entry) => `    ${JSON.stringify(entry)}`).join(',\n'), '  ]',
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
  'manifest/testdata/reference/publisher.verdicts.json': sha(publisherVerdictText),
  'manifest/testdata/reference/publisher.facts.json': sha(publisherFactsText),
  'rules/testdata/reference/matrix.golden.json': sha(readFileSync(rulesGolden)),
}, null, 1)}\n`;

if (thrown > 0) console.error(`note: the reference threw on ${thrown} document(s); they are recorded as refused`);
const targets = [[corpusPath, corpusText], [verdictsPath, verdictText], [factsPath, factsText], [publisherVerdictsPath, publisherVerdictText], [publisherFactsPath, publisherFactsText], [digestsPath, digestText]];
if (process.argv.includes('--check')) {
  if (targets.some(([path, text]) => readFileSync(path, 'utf8') !== text)) { console.error(`the goldens in ${here} are stale: run node ${process.argv[1]}`); process.exit(1); }
  console.log(`the goldens are up to date (${manifestCases.length} manifest and ${mdCases.length} OVDB.md documents)`);
} else {
  for (const [path, text] of targets) writeFileSync(path, text);
  console.log(`wrote ${manifestCases.length} manifest and ${mdCases.length} OVDB.md documents: corpus ${(corpusText.length / 1024).toFixed(0)} KiB, verdicts ${(verdictText.length / 1024).toFixed(0)} KiB, facts ${(factsText.length / 1024).toFixed(0)} KiB; accepted ${verdictFile.manifest.accepted}+${verdictFile.md.accepted} (publisher ${publisherVerdictFile.manifest.accepted}+${publisherVerdictFile.md.accepted}), refused ${verdictFile.manifest.refused}+${verdictFile.md.refused} (publisher ${publisherVerdictFile.manifest.refused}+${publisherVerdictFile.md.refused}); real repositories ${realChecked}; mined edits ${appliedEdits} of ${minedEdits} applied`);
}
