// The databases registered in the OVDB Directory at the pinned commit of the reference (internal/publisher/references.mjs): the repository, the commit and
// the manifest of each record of `databases/$records`. They are what `realdirectory_test.go` fetches and checks (OVDB_REAL_DIRECTORY=1), and what
// verdicts.json is the verdicts of.
//
//   node internal/publisher/repo/testdata/directory/entries.mjs            # write entries.json
//   node internal/publisher/repo/testdata/directory/entries.mjs --check    # fail if it is stale
//   ... --directory <dir>                                                  # a checkout of the reference at its pinned commit, as for the generators
import { readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { checkoutReference, references } from '../../../references.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const argValue = (name) => { const at = process.argv.indexOf(name); return at === -1 ? undefined : process.argv[at + 1]; };
const root = checkoutReference('directory', { explicit: argValue('--directory') });
const { parse } = createRequire(join(root, 'package.json'))('yaml');
const records = join(root, 'databases', '$records');
const entries = readdirSync(records).filter((name) => name.endsWith('.yaml')).sort().map((name) => {
  const record = parse(readFileSync(join(records, name), 'utf8'));
  return { id: name.replace(/\.yaml$/, ''), repository: record.repository, commit: record.commit, manifest: record.manifest, url: record.url };
});
const text = `${JSON.stringify({ format: 'ovdb-real-entries/1', registry: { repository: references.directory.repository, commit: references.directory.commit }, entries }, null, 1)}\n`;
const path = join(here, 'entries.json');
if (process.argv.includes('--check')) {
  if (readFileSync(path, 'utf8') !== text) { console.error(`${path} is stale: run node ${process.argv[1]}`); process.exit(1); }
  console.log(`entries.json is up to date (${entries.length} entries)`);
} else {
  writeFileSync(path, text);
  console.log(`wrote ${entries.length} entries to ${path}`);
}
