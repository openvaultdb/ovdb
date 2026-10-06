#!/usr/bin/env node
// Regenerates directory-stage.json and directory-stage.digest.json: the verdict of the Directory's own file stage on repositories, at the pinned
// commit of the reference (internal/publisher/references.mjs).
//
//   node internal/publisher/repo/testdata/reference/directory-stage.mjs           # rewrite the goldens
//   node internal/publisher/repo/testdata/reference/directory-stage.mjs --check   # fail if they are stale
//   ... --directory <dir>      a checkout of the Directory at its pinned commit, with `npm ci --omit=dev --ignore-scripts` run
//
// What it is. repository.json (generate.mjs) holds the verdict of the Chinook checker on each repository. Nothing held the Directory's, which is the
// reference of the bar (plan decision D0): analyseDatabase in scripts/lib/directory.mjs, which reads OVDB.md, the manifest, the model file, the
// meaning file and its concepts at the pinned commit. This generator runs that function, as the Directory's own scripts/test.mjs does: each case is
// a local git repository served through `urlFor`, the Directory's own fixtures (scripts/fixtures/chinookdb and core) are the base, and the registries
// are in-memory indexes. It runs the OWN form (the model and the meaning file are in the repository). The shared form needs other repositories
// and the ModelSpec registry; it stays for the slices that handle them (A4, A5).
//
// A case is a list of operations on the base ("file" path text, "remove" path), the Directory's verdict, the first of its problems, and the
// OUTCOME that Go is expected to have against it. The Go test (directory_stage_test.go) checks the outcome, and drift.json says which slice closes
// each looser one. An outcome is:
//   agree                  Go and the Directory give the same verdict
//   looser:<slice>         Go (the Directory profile) accepts what the Directory refuses; the slice (F2 to F6) ports the rule
//   out-of-reach:record    the Directory refuses by what the database's registry record says; no check of a repository alone can. MECHANICAL: the case carries
//                          a `fix` (a record, a key, a registry or a URL map), and the generator runs the same files again under it and requires the Directory to accept
//   out-of-reach:registry  the Directory refuses by what the ModelSpec or MeaningGraph registry says, or by another repository (the Publisher profile
//                          stands in for the graph's address with publisher.repository, and so refuses what the Directory profile cannot)
//   stricter:<kind>        Go refuses what the Directory accepts, as a recorded kind (stricterKinds in directory_stage_test.go)
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { assertAnchors, assertGeneratorNode, checkoutReference, references as pins } from '../../../references.mjs';

assertGeneratorNode();

const here = dirname(fileURLToPath(import.meta.url));
const goldenPath = join(here, 'directory-stage.json');
const digestPath = join(here, 'directory-stage.digest.json');
const argValue = (name) => { const at = process.argv.indexOf(name); return at === -1 ? undefined : process.argv[at + 1]; };
const sha = (text) => createHash('sha256').update(text).digest('hex');

const root = checkoutReference('directory', { explicit: argValue('--directory') });
const lib = (name) => import(pathToFileURL(join(root, 'scripts/lib', name)).href);
const gitLib = await lib('git.mjs');
const { analyseDatabase } = await lib('directory.mjs');
const { indexMeaningRegistry } = await lib('meaning.mjs');
gitLib.setGitProtocols('https:file'); // the Directory's own tests do the same: a repository is served from a local path

// The refusals that the cases walk, as the messages that the pinned file writes: the generator stops when the file at the pin no longer holds one.
const source = readFileSync(join(root, 'scripts/lib/directory.mjs'), 'utf8');
const modelspecSource = readFileSync(join(root, 'scripts/lib/modelspec.mjs'), 'utf8');
const meaningSource = readFileSync(join(root, 'scripts/lib/meaning.mjs'), 'utf8');
assertAnchors(source, 'scripts/lib/directory.mjs', pins.directory.commit, [
  'has no concepts list', 'licences.meaning is ${manifest.licences.meaning}, but ${manifest.meaning.file} declares ${doc.license}',
  'model.name is ${manifest.model.name}, but the ModelSpec at ${manifest.model.modelspec} is module ${model.module}',
  'models must name the ModelSpec module ${model.module} with a relative path that stays inside the repository',
  'must be a .modelspec.hcl file (the model\'s source)',
  'but ${data.manifest} says model.hcl is ${manifest.model.hcl}', 'must not carry ?ref= when the model\'s files are in the same repository',
  'but the model\'s files are in ${repositoryKey(data.repository)}', 'but the ModelSpec at ${manifest.model.modelspec} is module ${model.module}`);',
  'is not a modelspec:///{module}.{Entity} reference', 'names a model outside this database; bindings name this repository\'s own ModelSpec',
  'names module ${ref.module}, but the ModelSpec at ${modelLabel} is module ${model.module}', 'names an entity that is not in the ModelSpec',
  'with role ${binding.role} must name a property', 'which ${ref.name} does not have in the ModelSpec', 'is declared twice', 'validateConcept(concept, position, { bindings: true })',
  'is registered for ${graph.repository}, not for ${data.repository}',
  'is not one of the meaning files the MeaningGraph registry lists for', 'is not registered in the MeaningGraph registry',
  'but the record\'s url is ${data.url}', 'but the ${descriptor ? \'descriptor localId\' : \'record id\'} is ${expectedManifestId}',
  'recordsets lacks ModelSpec entities', 'recordsets names things that do not map to ModelSpec entities', 'recordsets lists a name twice',
  'the recordset page of ${name}, ${url}, ${problem}',
]);
assertAnchors(modelspecSource, 'scripts/lib/modelspec.mjs', pins.directory.commit, [
  "problems.push('has no \"modelspec\" version')", "problems.push('has no module.name that is an identifier')", "problems.push('has no entities')",
  'must be an identifier (letters, digits and _, not starting with a digit)', 'has no properties', 'which is not a type name', 'has neither a type nor an entity',
  'which the ModelSpec does not have',
]);
assertAnchors(meaningSource, 'scripts/lib/meaning.mjs', pins.directory.commit, [
  'has no id`', 'must be lower-case words joined by single hyphens', 'labels must map language codes to labels',
  'must be a plain string of at most 200 characters (no control characters, < or >)', 'must be a concept reference (a string)', 'bindings must be a list',
  'every binding must be a mapping with model, role and property', 'must be one of ${bindingRoles.join', 'extends returns to', 'extends chain is longer than ${maxChain} concepts',
  'const maxChain = 50;', 'which ${target.id} does not have at ${target.ref}', 'is not a concept reference', 'values-of: ${target.error}',
]);

// ---- the world: the Directory's own fixtures, as its tests use them ----

const readTree = (dir) => {
  const files = new Map();
  const walk = (current) => {
    for (const name of readdirSync(current).sort()) {
      const path = join(current, name);
      if (statSync(path).isDirectory()) walk(path);
      else files.set(relative(dir, path), readFileSync(path, 'utf8'));
    }
  };
  walk(dir);
  return files;
};
const writeTree = (dir, files) => {
  for (const [path, text] of files) {
    mkdirSync(dirname(join(dir, path)), { recursive: true });
    writeFileSync(join(dir, path), text);
  }
};
// A commit that is the same on every run: the commit id of the core repository is written into the meaning file.
const gitEnv = { ...Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('GIT_'))), GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_NOSYSTEM: '1', GIT_AUTHOR_NAME: 'ovdb', GIT_AUTHOR_EMAIL: 'ovdb@example.invalid', GIT_COMMITTER_NAME: 'ovdb', GIT_COMMITTER_EMAIL: 'ovdb@example.invalid', GIT_AUTHOR_DATE: '2000-01-01T00:00:00Z', GIT_COMMITTER_DATE: '2000-01-01T00:00:00Z' };
const git = (dir, ...args) => execFileSync('git', ['-C', dir, ...args], { env: gitEnv, encoding: 'utf8' }).trim();
const origin = (files, name) => {
  const dir = mkdtempSync(join(tmpdir(), `ovdb-stage-${name}-`));
  git(dir, 'init', '-q', '-b', 'main');
  writeTree(dir, files);
  git(dir, 'add', '-A');
  git(dir, 'commit', '-q', '-m', 'c');
  return { dir, commit: git(dir, 'rev-parse', 'HEAD') };
};

const chinookUrl = 'https://github.com/demo-db/chinook';
const coreUrl = 'https://github.com/meaninggraph/core';
const fixtureCorePin = 'cb97dbcd9e951b00e7d46cb2e0c4e120c24c8db7';
const core = origin(readTree(join(root, 'scripts/fixtures/core')), 'core');
const base = readTree(join(root, 'scripts/fixtures/chinookdb'));
base.set('model/chinook.meaning.yaml', base.get('model/chinook.meaning.yaml').replaceAll(fixtureCorePin, core.commit));
const manifestPath = 'ovdb.yaml';
const modelPath = 'model/chinook.modelspec.json';
const hclPath = 'model/chinook.modelspec.hcl';
const meaningPath = 'model/chinook.meaning.yaml';
const recordUrl = 'https://chinookdb.com/ovdb/dbs/chinook';

const registryOf = (graphs) => indexMeaningRegistry({ format: 'meaning-registry/draft-1', checksum: `sha256:${sha(JSON.stringify(graphs))}`, graphs }, 'the test registry');

// The Directory's verdict on one repository: the problems of analyseDatabase.
const directoryVerdict = async (files, { editRegistry, record: editRecord, key = 'chinook', urls: extraUrls = [] } = {}) => {
  const publisher = origin(files, 'publisher');
  const graphs = [
    { id: 'chinook', title: 'Chinook', kind: 'dataset', status: 'draft', address: 'meaning://github.com/demo-db/chinook', repository: chinookUrl, commit: publisher.commit, meaning_files: [meaningPath], maintainers: ['x'] },
    { id: 'core', title: 'Core', kind: 'universal', status: 'draft', address: 'meaning://github.com/meaninggraph/core', repository: coreUrl, commit: core.commit, meaning_files: ['*.meaning.yaml'], maintainers: ['x'] },
  ];
  editRegistry?.(graphs);
  const data = { format: 'ovdb-directory/draft-1', title: 'Chinook music store', description: 'The Chinook sample database.', status: 'draft', url: recordUrl, repository: chinookUrl, commit: publisher.commit, manifest: manifestPath, meaning_graph: 'chinook', maintainers: ['x'] };
  editRecord?.(data);
  const cache = mkdtempSync(join(tmpdir(), 'ovdb-stage-cache-'));
  const urls = new Map([[chinookUrl, `file://${publisher.dir}`], [coreUrl, `file://${core.dir}`], ...extraUrls.map((url) => [url, `file://${core.dir}`])]);
  try {
    const result = await analyseDatabase({ key, file: 'chinook.yaml', data }, {
      urlFor: (url) => urls.get(url) ?? url, cacheDir: cache, historyDir: join(cache, 'history'), fetched: new Set(), branches: new Map(),
      meaningRegistry: registryOf(graphs), modelRegistry: async () => ({ source: 'the test registry', byAddress: new Map() }),
    });
    return result.problems.map((problem) => problem.replace(/^chinook\.yaml: /, ''));
  } finally {
    rmSync(cache, { recursive: true, force: true });
    rmSync(publisher.dir, { recursive: true, force: true });
  }
};

// ---- the cases ----

const cases = [];
const json = (mutate) => (text) => `${JSON.stringify((() => { const doc = JSON.parse(text); mutate(doc); return doc; })(), null, 2)}\n`;
const edit = (find, replace) => (text) => { if (!text.includes(find)) throw new Error(`no ${JSON.stringify(find)}`); return text.replace(find, replace); };
const concepts = (snippet) => (text) => `${text.slice(0, text.indexOf('\nconcepts:\n') + 1)}concepts:\n${snippet}`;
const conceptsRaw = (line) => (text) => `${text.slice(0, text.indexOf('\nconcepts:\n') + 1)}${line}\n`;
const add = (id, group, note, changes, expect, outcome, extra = {}) => cases.push({ id, group, note, changes, expect, outcome, ...extra });

// the control: the Directory's own fixture
add('base', 'control', 'the Directory\'s own chinookdb fixture, unchanged', {}, null, 'agree');
add('concepts-empty-list', 'control', 'an empty concepts list: the Directory reads it (Array.isArray) and finds nothing to bind', { [meaningPath]: conceptsRaw('concepts: []') }, null, 'agree');
add('concept-minimal', 'control', 'one concept with an id', { [meaningPath]: concepts('  - id: artist\n') }, null, 'agree');

// the ModelSpec file, parseModelSpec (modelspec.mjs 92-118)
const entityOf = (doc) => doc.entities.Genre;
add('modelspec-not-json', 'modelspec', 'the model file is not JSON', { [modelPath]: () => '{' }, /is not JSON/, 'agree');
add('modelspec-no-version', 'modelspec', 'no "modelspec" version', { [modelPath]: json((doc) => { delete doc.modelspec; }) }, /has no "modelspec" version/, 'agree');
add('modelspec-version-number', 'modelspec', 'the "modelspec" version is a number', { [modelPath]: json((doc) => { doc.modelspec = 1; }) }, /has no "modelspec" version/, 'agree');
add('modelspec-module-name', 'modelspec', 'module.name is not an identifier', { [modelPath]: json((doc) => { doc.module.name = 'bad-name'; }) }, /has no module\.name that is an identifier/, 'agree');
add('modelspec-module-underscore', 'stricter', 'a module name that starts with _: the Directory\'s identifier pattern allows it, the checker\'s module pattern does not', {
  [modelPath]: json((doc) => { doc.module.name = '_chinook'; }), [meaningPath]: (text) => concepts('  - id: artist\n')(text.replace('  chinook: chinook.modelspec.hcl', '  _chinook: chinook.modelspec.hcl')),
}, null, 'stricter:module-name-underscore');
add('modelspec-no-entities', 'modelspec', 'the entities object is empty', { [modelPath]: json((doc) => { doc.entities = {}; }) }, /has no entities/, 'agree');
add('modelspec-entity-name', 'modelspec', 'an entity whose name is not an identifier (the recordsets and the references follow it)', {
  [modelPath]: (text) => text.replaceAll('"Genre"', '"Gen-re"'), [manifestPath]: (text) => text.replace('  - Genre\n', '  - Gen-re\n'),
}, /entity name "Gen-re" must be an identifier/, 'agree');
add('modelspec-entity-no-properties', 'modelspec', 'an entity with an empty properties object', { [modelPath]: json((doc) => { entityOf(doc).properties = {}; }) }, /entity Genre has no properties/, 'agree');
add('modelspec-entity-properties-list', 'modelspec', 'an entity whose properties is a list', { [modelPath]: json((doc) => { entityOf(doc).properties = []; }) }, /entity Genre has no properties/, 'agree');
add('modelspec-entity-null', 'modelspec', 'an entity that is null', { [modelPath]: json((doc) => { doc.entities.Genre = null; }) }, /entity Genre has no properties/, 'agree');
add('modelspec-property-name', 'modelspec', 'a property whose name is not an identifier', { [modelPath]: json((doc) => { entityOf(doc).properties['Na-me'] = entityOf(doc).properties.Name; delete entityOf(doc).properties.Name; }) }, /property name "Genre\.Na-me" must be an identifier/, 'agree');
add('modelspec-property-type', 'modelspec', 'a property type that is not a type name', { [modelPath]: json((doc) => { entityOf(doc).properties.Name.type = 'not a type'; }) }, /Genre\.Name has type "not a type", which is not a type name/, 'agree');
add('modelspec-property-neither', 'modelspec', 'a property with neither a type nor an entity', { [modelPath]: json((doc) => { entityOf(doc).properties.Name = {}; }) }, /Genre\.Name has neither a type nor an entity/, 'agree');
add('modelspec-property-unknown-entity', 'modelspec', 'a property that references an entity the model lacks', { [modelPath]: json((doc) => { entityOf(doc).properties.Name = { entity: 'Nope' }; }) }, /Genre\.Name references entity Nope, which the ModelSpec does not have/, 'agree');
add('modelspec-list-type', 'control', 'a list type (datetime[]) is a type name', { [modelPath]: json((doc) => { entityOf(doc).properties.Name.type = 'datetime[]'; }) }, null, 'agree');

// the concepts of the meaning file: validateConcept (meaning.mjs 58-80) and the duplicate rule
add('concept-no-concepts', 'concept', 'the meaning file has no concepts list (closed by #63)', { [meaningPath]: edit('\nconcepts:\n', '\nconceptz:\n') }, /has no concepts list/, 'agree');
add('concept-not-mapping', 'concept', 'concepts are a number, text and null', { [meaningPath]: concepts('  - 1\n  - x\n  - null\n') }, /concept #1 has no id/, 'agree');
add('concept-flow-scalars', 'concept', 'concepts: [1, "x", null]', { [meaningPath]: conceptsRaw('concepts: [1, "x", null]') }, /concept #1 has no id/, 'agree');
add('concept-no-id', 'concept', 'a concept with no id', { [meaningPath]: concepts('  - labels: {en: A}\n') }, /concept #1 has no id/, 'agree');
add('concept-id-number', 'concept', 'a concept whose id is a number', { [meaningPath]: concepts('  - id: 5\n') }, /concept #1 has no id/, 'agree');
for (const [name, id] of [['upper-case', 'Artist'], ['underscore', 'art_ist'], ['leading-hyphen', '-artist'], ['trailing-hyphen', 'artist-'], ['double-hyphen', 'art--ist'], ['leading-digit', '1artist']]) {
  add(`concept-id-${name}`, 'concept', `a concept id with ${name.replace('-', ' ')}`, { [meaningPath]: concepts(`  - id: ${id}\n`) }, /must be lower-case words joined by single hyphens/, 'agree');
}
add('concept-labels-list', 'concept', 'labels is a list', { [meaningPath]: concepts('  - id: artist\n    labels: [Artist]\n') }, /labels must map language codes to labels/, 'agree');
add('concept-labels-null', 'concept', 'labels is null', { [meaningPath]: concepts('  - id: artist\n    labels:\n') }, /labels must map language codes to labels/, 'agree');
add('concept-label-empty', 'concept', 'a label that is empty', { [meaningPath]: concepts('  - id: artist\n    labels:\n      en: " "\n') }, /the en label must be a plain string/, 'agree');
add('concept-label-number', 'concept', 'a label that is a number', { [meaningPath]: concepts('  - id: artist\n    labels:\n      en: 5\n') }, /the en label must be a plain string/, 'agree');
add('concept-label-long', 'concept', 'a label of 201 characters', { [meaningPath]: concepts(`  - id: artist\n    labels:\n      en: ${'a'.repeat(201)}\n`) }, /the en label must be a plain string/, 'agree');
add('concept-label-200', 'control', 'a label of 200 characters', { [meaningPath]: concepts(`  - id: artist\n    labels:\n      en: ${'a'.repeat(200)}\n`) }, null, 'agree');
add('concept-label-angle', 'concept', 'a label with < in it', { [meaningPath]: concepts('  - id: artist\n    labels:\n      en: "a<b"\n') }, /the en label must be a plain string/, 'agree');
add('concept-label-control', 'concept', 'a label with a tab in it', { [meaningPath]: concepts('  - id: artist\n    labels:\n      en: "a\\tb"\n') }, /the en label must be a plain string/, 'agree');
add('concept-extends-number', 'concept', 'extends is a number', { [meaningPath]: concepts('  - id: artist\n    extends: 5\n') }, /extends must be a concept reference \(a string\)/, 'agree');
add('concept-values-of-list', 'concept', 'values-of is a list', { [meaningPath]: concepts('  - id: artist\n    values-of: [a]\n') }, /values-of must be a concept reference \(a string\)/, 'agree');
add('concept-bindings-text', 'concept', 'bindings is text', { [meaningPath]: concepts('  - id: artist\n    bindings: x\n') }, /bindings must be a list/, 'agree');
add('concept-binding-scalar', 'concept', 'a binding that is text', { [meaningPath]: concepts('  - id: artist\n    bindings:\n      - x\n') }, /every binding must be a mapping/, 'agree');
add('concept-binding-role', 'concept', 'a binding whose role is not one of the roles', { [meaningPath]: concepts('  - id: artist\n    bindings:\n      - model: modelspec:///chinook.Artist\n        role: nope\n') }, /binding role "nope" must be one of/, 'agree');
add('concept-binding-no-role', 'concept', 'a binding with no role', { [meaningPath]: concepts('  - id: artist\n    bindings:\n      - model: modelspec:///chinook.Artist\n') }, /binding role undefined must be one of/, 'agree');
add('concept-duplicate', 'concept', 'a concept declared twice', { [meaningPath]: concepts('  - id: artist\n  - id: artist\n') }, /concept artist is declared twice/, 'agree');

// the bindings (directory.mjs 728-754)
const bound = (binding) => concepts(`  - id: artist\n    bindings:\n${binding}`);
add('binding-valid', 'control', 'an entity binding and a property binding', { [meaningPath]: bound('      - model: modelspec:///chinook.Artist\n        role: entity\n      - model: modelspec:///chinook.Artist\n        property: Name\n        role: display-name\n') }, null, 'agree');
add('binding-no-model', 'binding', 'a binding with no model', { [meaningPath]: bound('      - role: entity\n') }, /binding model undefined is not a modelspec:\/\/\/\{module\}\.\{Entity\} reference/, 'agree');
add('binding-model-form', 'binding', 'a binding model that is not a reference', { [meaningPath]: bound('      - model: chinook.Artist\n        role: entity\n') }, /is not a modelspec:\/\/\/\{module\}\.\{Entity\} reference/, 'agree');
add('binding-other-model', 'binding', 'a binding that names another repository\'s model', { [meaningPath]: bound('      - model: modelspec://github.com/other/repo/chinook.Artist\n        role: entity\n') }, /names a model outside this database/, 'agree');
add('binding-module', 'binding', 'a binding whose module is not the model\'s', { [meaningPath]: bound('      - model: modelspec:///other.Artist\n        role: entity\n') }, /names module other, but the ModelSpec at/, 'agree');
add('binding-entity', 'binding', 'a binding whose entity is not in the model', { [meaningPath]: bound('      - model: modelspec:///chinook.Nope\n        role: entity\n') }, /names an entity that is not in the ModelSpec/, 'agree');
add('binding-role-needs-property', 'binding', 'a role other than entity with no property', { [meaningPath]: bound('      - model: modelspec:///chinook.Artist\n        role: identifier\n') }, /with role identifier must name a property/, 'agree');
add('binding-property', 'binding', 'a property the entity does not have', { [meaningPath]: bound('      - model: modelspec:///chinook.Artist\n        property: Nope\n        role: identifier\n') }, /names property "Nope", which Artist does not have in the ModelSpec/, 'agree');
add('binding-property-number', 'binding', 'a property that is a number', { [meaningPath]: bound('      - model: modelspec:///chinook.Artist\n        property: 5\n        role: identifier\n') }, /names property 5, which Artist does not have in the ModelSpec/, 'agree');

// the chains of a concept (meaning.mjs 195-226), inside the repository's own graph
const chain = (n) => Array.from({ length: n }, (_, i) => `  - id: c${i + 1}\n${i + 1 < n ? `    extends: c${i + 2}\n` : ''}`).join('');
add('chain-own', 'control', 'a concept that extends another of the same graph by its bare id', { [meaningPath]: concepts('  - id: artist\n    extends: album\n  - id: album\n') }, null, 'agree');
add('chain-own-address', 'control', 'a concept that extends another of the same graph by its address', { [meaningPath]: concepts('  - id: artist\n    extends: meaning://github.com/demo-db/chinook/album\n  - id: album\n') }, null, 'agree');
add('chain-cycle', 'chain', 'two concepts that extend each other', { [meaningPath]: concepts('  - id: artist\n    extends: album\n  - id: album\n    extends: artist\n') }, /extends returns to artist/, 'looser:F5');
add('chain-self', 'chain', 'a concept that extends itself', { [meaningPath]: concepts('  - id: artist\n    extends: artist\n') }, /extends returns to artist/, 'looser:F5');
add('chain-unresolved', 'chain', 'extends names a concept the graph does not have', { [meaningPath]: concepts('  - id: artist\n    extends: nothing\n') }, /extends: nothing names concept nothing, which chinook does not have/, 'looser:F5');
add('chain-not-a-reference', 'chain', 'extends is not a concept reference', { [meaningPath]: concepts('  - id: artist\n    extends: Not A Ref\n') }, /extends: "Not A Ref" is not a concept reference/, 'looser:F5');
add('chain-long', 'chain', 'an extends chain of 52 concepts (the limit is 50)', { [meaningPath]: concepts(chain(52)) }, /extends chain is longer than 50 concepts/, 'looser:F5');
add('chain-50', 'control', 'an extends chain of 50 concepts', { [meaningPath]: concepts(chain(50)) }, null, 'agree');
add('chain-values-of-unresolved', 'chain', 'values-of names a concept the graph does not have', { [meaningPath]: concepts('  - id: artist\n    values-of: nothing\n') }, /values-of: nothing names concept nothing, which chinook does not have/, 'looser:F5');
add('chain-values-of-cycle', 'chain', 'values-of names a concept whose chain loops', { [meaningPath]: concepts('  - id: artist\n    values-of: album\n  - id: album\n    extends: genre\n  - id: genre\n    extends: album\n') }, /values-of album: extends returns to album/, 'looser:F5');
add('chain-address-unregistered', 'chain', 'extends names a graph by an address that no registry lists', { [meaningPath]: (text) => concepts(`  - id: artist\n    extends: meaning://github.com/nobody/nothing/date?ref=${core.commit}\n`)(text) }, /is not registered in the MeaningGraph registry/, 'out-of-reach:registry', {
  fix: { urls: ['https://github.com/nobody/nothing'], registry: (graphs) => graphs.push({ id: 'nothing', title: 'Nothing', kind: 'universal', status: 'draft', address: 'meaning://github.com/nobody/nothing', repository: 'https://github.com/nobody/nothing', commit: core.commit, meaning_files: ['*.meaning.yaml'], maintainers: ['x'] }), because: 'the registry registers that address at the commit of the core graph' },
});
add('chain-core-unregistered', 'chain', 'the Directory\'s fixture, with the core graph missing from the registry', {}, /is not registered in the MeaningGraph registry/, 'out-of-reach:registry', { registry: (graphs) => graphs.splice(1, 1), fix: { because: 'the registry registers the core graph, as the fixture\'s own registry does' } });

// the rules that Go has: held as controls, so that "agree" is shown and not assumed
add('has-licence-differs', 'has', 'licences.meaning differs from the meaning file\'s license', { [manifestPath]: edit('  meaning: CC0-1.0', '  meaning: MIT') }, /licences\.meaning is MIT, but model\/chinook\.meaning\.yaml declares CC0-1\.0/, 'agree');
add('has-model-name', 'has', 'model.name is not the module', { [manifestPath]: edit('  modelspec: model/chinook.modelspec.json\n', '  name: other\n  modelspec: model/chinook.modelspec.json\n') }, /model\.name is other, but the ModelSpec at/, 'agree');
add('has-recordsets-lack', 'has', 'a recordset fewer than the entities', { [manifestPath]: edit('  - Genre\n', '') }, /recordsets lacks ModelSpec entities: Genre/, 'agree');
add('has-recordsets-extra', 'has', 'a recordset that is not an entity', { [manifestPath]: edit('  - Genre\n', '  - Genre\n  - Nope\n') }, /recordsets names things that do not map to ModelSpec entities: Nope/, 'agree');
add('has-recordsets-twice', 'has', 'a recordset listed twice', { [manifestPath]: edit('  - Genre\n', '  - Genre\n  - Genre\n') }, /recordsets lists a name twice/, 'agree');
add('has-models-entry-spelling', 'has', 'the models: entry has a leading slash', { [meaningPath]: edit('  chinook: chinook.modelspec.hcl', '  chinook: /chinook.modelspec.hcl') }, /models must name the ModelSpec module chinook with a relative path that stays inside the repository/, 'agree');
add('has-models-entry-hcl', 'has', 'model.hcl is not the models: entry (model.hcl names a file that is there)', { [manifestPath]: edit('  hcl: model/chinook.modelspec.hcl', '  hcl: model/other.modelspec.hcl'), 'model/other.modelspec.hcl': () => 'x\n' }, /says model\.hcl is model\/other\.modelspec\.hcl/, 'agree');
add('has-models-entry-suffix', 'has', 'the models: entry and model.hcl name a file that is not .modelspec.hcl', {
  [manifestPath]: edit('  hcl: model/chinook.modelspec.hcl', '  hcl: model/chinook.model.txt'), [meaningPath]: edit('  chinook: chinook.modelspec.hcl', '  chinook: chinook.model.txt'), 'model/chinook.model.txt': () => 'x\n',
}, /ending in \.modelspec\.hcl/, 'agree');
add('has-models-entry-missing', 'has', 'the models: entry names a file that is not there', { [meaningPath]: edit('  chinook: chinook.modelspec.hcl', '  chinook: nowhere.modelspec.hcl'), [manifestPath]: edit('  hcl: model/chinook.modelspec.hcl', '  hcl: model/nowhere.modelspec.hcl') }, /does not exist at commit/, 'agree');
// model.hcl is optional for the Directory (a manifest stage rule only when it is written); the models: entry is then the only thing that names the file.
const noHcl = edit('  hcl: model/chinook.modelspec.hcl\n', '');
add('nohcl-accepted', 'stricter', 'no model.hcl: the models: entry names the model file, which is there', { [manifestPath]: noHcl }, null, 'stricter:model-hcl-required');
add('nohcl-entry-suffix', 'has', 'no model.hcl, and the models: entry names a file that is not .modelspec.hcl (it exists)', { [manifestPath]: noHcl, [meaningPath]: edit('  chinook: chinook.modelspec.hcl', '  chinook: chinook.model.txt'), 'model/chinook.model.txt': () => 'x\n' }, /the chinook model model\/chinook\.model\.txt must be a \.modelspec\.hcl file/, 'looser:F6');
add('nohcl-entry-missing', 'has', 'no model.hcl, and the models: entry names a file that is not there', { [manifestPath]: noHcl, [meaningPath]: edit('  chinook: chinook.modelspec.hcl', '  chinook: nowhere.modelspec.hcl') }, /model\/nowhere\.modelspec\.hcl does not exist at commit/, 'looser:F6');
add('nohcl-entry-spelling', 'has', 'no model.hcl, and the models: entry has a space in it', { [manifestPath]: noHcl, [meaningPath]: edit('  chinook: chinook.modelspec.hcl', '  chinook: "chinook modelspec.hcl"') }, /models must name the ModelSpec module chinook/, 'looser:F6');
add('stricter-meaning-license-missing', 'stricter', 'the meaning file has no license: the Directory compares it only when it is text', { [meaningPath]: edit('license: CC0-1.0\n', '') }, null, 'stricter:meaning-license-required');
add('stricter-meaning-license-number', 'stricter', 'the meaning file\'s license is a number', { [meaningPath]: edit('license: CC0-1.0\n', 'license: 5\n') }, null, 'stricter:meaning-license-required');
add('stricter-meaning-id', 'stricter', 'the meaning file\'s id is not meaning.graph.id: the Directory does not read it', { [meaningPath]: edit('\nid: chinook\n', '\nid: other\n') }, null, 'stricter:meaning-id-compared');
add('has-model-address-lower', 'has', 'model.address is not in lower case', { [manifestPath]: edit('  modelspec: model/chinook.modelspec.json\n', '  address: modelspec://github.com/Demo-DB/chinook/chinook\n  modelspec: model/chinook.modelspec.json\n') }, /must be written in lower case/, 'agree');
add('has-model-address-ref', 'has', 'model.address carries ?ref=', { [manifestPath]: edit('  modelspec: model/chinook.modelspec.json\n', `  address: modelspec://github.com/demo-db/chinook/chinook?ref=${'0'.repeat(40)}\n  modelspec: model/chinook.modelspec.json\n`) }, /must not carry \?ref=/, 'agree');
add('has-model-address-module', 'has', 'model.address names another module', { [manifestPath]: edit('  modelspec: model/chinook.modelspec.json\n', '  address: modelspec://github.com/demo-db/chinook/other\n  modelspec: model/chinook.modelspec.json\n') }, /model\.address names module other/, 'agree');
add('has-graph-address', 'has', 'meaning.graph.address is another repository\'s', { [manifestPath]: edit('    address: meaning://github.com/demo-db/chinook', '    address: meaning://github.com/demo-db/other') }, /meaning\.graph\.address is meaning:\/\/github\.com\/demo-db\/other, but the MeaningGraph registry registers chinook as/, 'looser:F7');
add('has-recordset-page', 'has', 'the page of a recordset whose name is a percent escape that a router could decode again (lines 775-781)', { [manifestPath]: (text) => `${text.replace('  - Genre\n', '  - a%2Fb\n')}\nrecordset_entities:\n  "a%2Fb": Genre\n` }, /the recordset page of a%2Fb, .*, has a nested percent escape/, 'agree');
add('has-ovdbmd-missing', 'has', 'OVDB.md is missing', { 'OVDB.md': null }, /OVDB\.md: OVDB\.md does not exist at commit/, 'agree');

// the meaning file and the model file as files (lines 485-487, 510-513), and the checks of a repository that the manifest stage cannot see
add('meaning-not-yaml', 'has', 'the meaning file is not valid YAML', { [meaningPath]: () => 'a: [\n' }, /is not valid YAML/, 'agree');
add('meaning-a-list', 'has', 'the meaning file is a list', { [meaningPath]: () => '- a\n' }, /has no concepts list/, 'agree');
add('meaning-concepts-null', 'has', 'concepts is null', { [meaningPath]: edit('\nconcepts:\n', '\nconcepts: ~\nlist:\n') }, /has no concepts list/, 'agree');
add('model-file-missing', 'has', 'the model file is not in the repository', { [modelPath]: null }, /model\.modelspec model\/chinook\.modelspec\.json does not exist at commit/, 'agree');
add('meaning-file-missing', 'has', 'the meaning file is not in the repository', { [meaningPath]: null }, /meaning\.file model\/chinook\.meaning\.yaml does not exist at commit/, 'agree');
add('has-recordsets-mapping-twice', 'has', 'two recordsets are mapped to the same entity (line 470): the manifest stage cannot see it', { [manifestPath]: (text) => `${text.replace('  - Genre\n', '  - Genre\n  - Category\n')}\nrecordset_entities:\n  Category: Genre\n` }, /recordset_entities maps more than one native recordset to the same ModelSpec entity/, 'agree');
add('has-model-address-host', 'has', 'model.address names a host that is not a repository host (line 544)', { [manifestPath]: edit('  modelspec: model/chinook.modelspec.json\n', '  address: modelspec://example.com/demo-db/chinook/chinook\n  modelspec: model/chinook.modelspec.json\n') }, /must name a repository on github\.com/, 'agree');
add('has-model-address-other-repository', 'has', 'model.address names another repository (line 546)', { [manifestPath]: edit('  modelspec: model/chinook.modelspec.json\n', '  address: modelspec://github.com/demo-db/other/chinook\n  modelspec: model/chinook.modelspec.json\n') }, /model\.address names github\.com\/demo-db\/other, but the model's files are in/, 'looser:F7');
add('has-publisher-repository', 'has', 'publisher.repository is not the record\'s repository (line 428; Go compares it with --repository)', { [manifestPath]: edit('  repository: https://github.com/demo-db/chinook', '  repository: https://github.com/demo-db/other') }, /publisher\.repository is https:\/\/github\.com\/demo-db\/other, but the record's repository is/, 'agree');
add('record-graph-id', 'record', 'meaning.graph.id is not the record\'s meaning_graph (line 427; the meaning file follows it)', { [manifestPath]: edit('    id: chinook\n    address: meaning://', '    id: other\n    address: meaning://'), [meaningPath]: edit('\nid: chinook\n', '\nid: other\n') }, /meaning\.graph\.id is other, but the record's meaning_graph is chinook/, 'out-of-reach:record', { fix: { record: (data) => { data.meaning_graph = 'other'; }, registry: (graphs) => { graphs[0].id = 'other'; }, because: 'the record\'s meaning_graph is other, and the registry registers the graph under that id' } });

// siblings of the families, so that the slices that port them have the edges
add('concept-label-gt', 'concept', 'a label with > in it', { [meaningPath]: concepts('  - id: artist\n    labels:\n      en: "a>b"\n') }, /the en label must be a plain string/, 'agree');
add('concept-label-del', 'concept', 'a label with DEL in it', { [meaningPath]: concepts('  - id: artist\n    labels:\n      en: "a\\x7fb"\n') }, /the en label must be a plain string/, 'agree');
add('concept-binding-null', 'concept', 'a binding that is null', { [meaningPath]: concepts('  - id: artist\n    bindings:\n      - null\n') }, /every binding must be a mapping/, 'agree');
add('binding-module-underscore', 'binding', 'a reference whose module starts with _ is not a reference', { [meaningPath]: concepts('  - id: artist\n    bindings:\n      - model: modelspec:///_chinook.Artist\n        role: entity\n') }, /is not a modelspec:\/\/\/\{module\}\.\{Entity\} reference/, 'agree');
add('binding-entity-underscore', 'binding', 'a reference whose entity starts with _ is not a reference', { [meaningPath]: concepts('  - id: artist\n    bindings:\n      - model: modelspec:///chinook._Artist\n        role: entity\n') }, /is not a modelspec:\/\/\/\{module\}\.\{Entity\} reference/, 'agree');
add('chain-51', 'chain', 'an extends chain of 51 concepts', { [meaningPath]: concepts(chain(51)) }, null, 'agree');

// Where the strict YAML reader refuses a meaning file that the Directory's YAML library reads: each is a choice of the reader (package manifest README), one case
// each, so that the list of what is stricter than the Directory in the file stage is the whole list.
add('yaml-key', 'stricter', 'a label whose key YAML reads as a number', { [meaningPath]: concepts('  - id: artist\n    labels:\n      1: One\n') }, null, 'stricter:yaml-key');
add('yaml-anchor', 'stricter', 'an anchor in a label', { [meaningPath]: concepts('  - id: artist\n    labels:\n      en: &l text\n') }, null, 'stricter:yaml-anchor');
add('yaml-tag', 'stricter', 'a tag on a label', { [meaningPath]: concepts('  - id: artist\n    labels:\n      en: !!str 5\n') }, null, 'stricter:yaml-tag');
add('yaml-directive', 'stricter', 'a %YAML directive before the document', { [meaningPath]: (text) => `%YAML 1.2\n---\n${text}` }, null, 'stricter:yaml-directive');
add('yaml-document-end', 'stricter', 'a document end marker after the document', { [meaningPath]: (text) => `${text}...\n` }, null, 'stricter:yaml-documents');
add('yaml-continued-quote', 'stricter', 'a double-quoted label continued with a backslash at the end of the line', { [meaningPath]: concepts('  - id: artist\n    labels:\n      en: "a \\\n        b"\n') }, null, 'stricter:yaml-unsupported');

// what a repository alone cannot say
add('record-id', 'record', 'manifest.id is not the record\'s id', { [manifestPath]: edit('id: chinook\ntitle', 'id: other\ntitle') }, /id is other, but the record id is chinook/, 'out-of-reach:record', { fix: { key: 'other', because: 'the record is called other' } });
add('record-url', 'record', 'manifest.url is not the record\'s url', { [manifestPath]: edit('url: https://chinookdb.com/ovdb/dbs/chinook\n\ndeployment', 'url: https://chinookdb.com/ovdb/dbs/other\n\ndeployment') }, /url is https:\/\/chinookdb\.com\/ovdb\/dbs\/other, but the record's url is/, 'out-of-reach:record', { fix: { record: (data) => { data.url = 'https://chinookdb.com/ovdb/dbs/other'; }, because: 'the record\'s url is that one' } });
add('registry-graph-unregistered', 'registry', 'the graph is not in the MeaningGraph registry', {}, /meaning_graph chinook is not registered in the MeaningGraph registry/, 'out-of-reach:registry', { registry: (graphs) => graphs.splice(0, 1), fix: { because: 'the registry registers the graph' } });
add('registry-graph-repository', 'registry', 'the graph is registered for another repository', {}, /is registered for https:\/\/github\.com\/demo-db\/other, not for/, 'out-of-reach:registry', { registry: (graphs) => { graphs[0].repository = 'https://github.com/demo-db/other'; graphs[0].address = 'meaning://github.com/demo-db/other'; }, fix: { because: 'the registry registers the graph for this repository' } });
add('registry-file-not-listed', 'registry', 'meaning.file is not one of the files the registry lists for the graph', {}, /is not one of the meaning files the MeaningGraph registry lists/, 'out-of-reach:registry', { registry: (graphs) => { graphs[0].meaning_files = ['elsewhere.meaning.yaml']; }, fix: { because: 'the registry lists the meaning file' } });

// ---- run ----

const observed = [];
for (const c of cases) {
  const files = new Map(base);
  for (const [path, change] of Object.entries(c.changes)) {
    if (change === null) files.delete(path);
    else files.set(path, change(files.get(path) ?? ''));
  }
  const problems = await directoryVerdict(files, { editRegistry: c.registry, record: c.record });
  // An out-of-reach case is mechanical: the same files, under the record or registry in `fix`, are accepted by the Directory.
  let acceptedWhen;
  if (c.outcome.startsWith('out-of-reach:')) {
    if (!c.fix) throw new Error(`${c.id}: an out-of-reach case needs a fix`);
    const again = await directoryVerdict(files, { editRegistry: c.fix.registry, record: c.fix.record, key: c.fix.key, urls: c.fix.urls });
    if (again.length > 0) { console.error(`${c.id}: the Directory should accept these files under the fix (${c.fix.because}), and says: ${again.join(' | ').slice(0, 300)}`); process.exit(1); }
    acceptedWhen = c.fix.because;
  } else if (c.fix) throw new Error(`${c.id}: only an out-of-reach case has a fix`);
  const ops = Object.entries(c.changes).map(([path]) => (files.has(path) ? ['file', path, files.get(path)] : ['remove', path]));
  observed.push({ c, problems, ops, acceptedWhen });
}

// Every case is held to the reason that it states: the verdict is the Directory's, and the problem is the one that the note names.
const reasonProblems = [];
for (const { c, problems } of observed) {
  if (c.expect === null) { if (problems.length > 0) reasonProblems.push(`${c.id}: the Directory should accept this case and says: ${problems.join(' | ').slice(0, 200)}`); continue; }
  if (problems.length === 0) reasonProblems.push(`${c.id}: the Directory should refuse this case, as ${c.expect}, and accepts it`);
  else if (!problems.some((problem) => c.expect.test(problem))) reasonProblems.push(`${c.id}: the Directory refuses this case, but not as ${c.expect}: ${problems.join(' | ').slice(0, 300)}`);
}
if (reasonProblems.length > 0) { console.error(`${reasonProblems.length} case(s) whose verdict does not come from the reason they state:\n${reasonProblems.join('\n')}`); process.exit(1); }

// The rule of Go that refuses each case that both refuse: an `agree` that holds because of an unrelated rule would hide a missing one. The Go test holds the
// first finding of the Directory profile to it. (modelspec-no-entities agrees through the recordsets rule: a manifest cannot list no recordsets, so an
// empty entities object always leaves recordsets that name things that are not entities.)
const goRules = {
  'binding-no-model': 'meaning-binding',
  'binding-model-form': 'meaning-binding',
  'binding-other-model': 'meaning-binding',
  'binding-module': 'meaning-binding',
  'binding-entity': 'meaning-binding',
  'binding-role-needs-property': 'meaning-binding',
  'binding-property': 'meaning-binding',
  'binding-property-number': 'meaning-binding',
  'binding-module-underscore': 'meaning-binding',
  'binding-entity-underscore': 'meaning-binding',
  'concept-not-mapping': 'meaning-concept',
  'concept-flow-scalars': 'meaning-concept',
  'concept-no-id': 'meaning-concept',
  'concept-id-number': 'meaning-concept',
  'concept-id-upper-case': 'meaning-concept',
  'concept-id-underscore': 'meaning-concept',
  'concept-id-leading-hyphen': 'meaning-concept',
  'concept-id-trailing-hyphen': 'meaning-concept',
  'concept-id-double-hyphen': 'meaning-concept',
  'concept-id-leading-digit': 'meaning-concept',
  'concept-labels-list': 'meaning-concept',
  'concept-labels-null': 'meaning-concept',
  'concept-label-empty': 'meaning-concept',
  'concept-label-number': 'meaning-concept',
  'concept-label-long': 'meaning-concept',
  'concept-label-angle': 'meaning-concept',
  'concept-label-control': 'meaning-concept',
  'concept-extends-number': 'meaning-concept',
  'concept-values-of-list': 'meaning-concept',
  'concept-bindings-text': 'meaning-concept',
  'concept-binding-scalar': 'meaning-concept',
  'concept-binding-role': 'meaning-concept',
  'concept-binding-no-role': 'meaning-concept',
  'concept-label-gt': 'meaning-concept',
  'concept-label-del': 'meaning-concept',
  'concept-binding-null': 'meaning-concept',
  'concept-duplicate': 'meaning-concept-duplicate',
  'modelspec-no-version': 'repo-model-version',
  'modelspec-version-number': 'repo-model-version',
  'modelspec-entity-name': 'repo-model-entity',
  'modelspec-entity-no-properties': 'repo-model-entity',
  'modelspec-entity-properties-list': 'repo-model-entity',
  'modelspec-entity-null': 'repo-model-entity',
  'modelspec-property-name': 'repo-model-property',
  'modelspec-property-type': 'repo-model-property',
  'modelspec-property-neither': 'repo-model-property',
  'modelspec-property-unknown-entity': 'repo-model-property',
  'modelspec-not-json': 'repo-model-json',
  'modelspec-module-name': 'repo-model-module',
  'modelspec-no-entities': 'repo-recordsets',
  'concept-no-concepts': 'meaning-concepts',
  'has-licence-differs': 'meaning-license',
  'has-model-name': 'repo-model-name',
  'has-recordsets-lack': 'repo-recordsets',
  'has-recordsets-extra': 'repo-recordsets',
  'has-recordsets-twice': 'manifest-recordsets',
  'has-models-entry-spelling': 'meaning-models',
  'has-models-entry-hcl': 'meaning-hcl',
  'has-models-entry-suffix': 'manifest-model',
  'has-models-entry-missing': 'repo-file',
  'has-model-address-lower': 'manifest-model',
  'has-model-address-ref': 'manifest-model',
  'has-model-address-module': 'repo-model-address',
  'has-recordset-page': 'manifest-recordsets',
  'has-ovdbmd-missing': 'repo-ovdbmd',
  'meaning-not-yaml': 'yaml',
  'meaning-a-list': 'meaning-shape',
  'meaning-concepts-null': 'meaning-concepts',
  'model-file-missing': 'repo-file',
  'meaning-file-missing': 'repo-file',
  'has-recordsets-mapping-twice': 'repo-recordsets',
  'has-model-address-host': 'manifest-model',
  'has-publisher-repository': 'repo-repository',
};
for (const { c, problems } of observed) {
  if (problems.length > 0 && c.outcome === 'agree' && !(c.id in goRules)) reasonProblems.push(`${c.id}: a refusing agree case needs the rule of Go that refuses it`);
}
if (reasonProblems.length > 0 && !process.argv.includes('--discover')) { console.error(reasonProblems.join('\n')); process.exit(1); }
const golden = `${JSON.stringify({
  format: 'ovdb-directory-stage/1',
  reference: `${pins.directory.repository}@${pins.directory.commit}`,
  about: 'The verdict of the Directory\'s own file stage (analyseDatabase, own form) on repositories made from the Directory\'s own fixtures; outcome is what Go is expected to do against it. See directory-stage.mjs.',
  repository: chinookUrl,
  base: Object.fromEntries(base),
  cases: observed.map(({ c, problems, ops, acceptedWhen }) => ({ id: c.id, group: c.group, note: c.note, ops, directory: problems.length === 0 ? 'accepts' : 'refuses', problem: problems[0]?.slice(0, 240), outcome: c.outcome, go: goRules[c.id], acceptedWhen })),
}, null, 1)}\n`;
const digest = `${JSON.stringify({ 'repo/testdata/reference/directory-stage.json': sha(golden) }, null, 1)}\n`;
if (process.argv.includes('--check')) {
  if ([[goldenPath, golden], [digestPath, digest]].some(([path, text]) => !existsSync(path) || readFileSync(path, 'utf8') !== text)) { console.error(`the goldens in ${here} are stale: run node ${process.argv[1]}`); process.exit(1); }
  console.log(`directory-stage.json is up to date (${cases.length} cases)`);
} else {
  writeFileSync(goldenPath, golden);
  writeFileSync(digestPath, digest);
  console.log(`wrote ${cases.length} cases`);
}
rmSync(core.dir, { recursive: true, force: true });
