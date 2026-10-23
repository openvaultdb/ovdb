// Like-strength structural metadata and raw-data stages. canonicalMeaning is a
// synthetic fixture association, never a canonical semantic admission verdict.
//
// Two groups of cases. `cases` run each fixture through the data-stage mutations; the fixtures with `-current` in their name are the same fixtures with
// their ModelSpec JSON documents in the current vocabulary (publisher/representation/testdata). `vocabulary` edit the target model or the source schema of
// those, string replacements, and record what the Directory says: the edit is data in the golden, and `bytes` is the SHA-256 of the document it
// makes, so that the Go test, which makes the same edit, shows that it checked the same document.
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { references } from '../../../references.mjs';
const here = dirname(fileURLToPath(import.meta.url));
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const fixtureRoot = join(here, '../../../../../publisher/representation/testdata');
const mutations = [['none',3],['missing-data-reader',3],['wrong-data-head',3],['wrong-data-hash',3],['data-symlink',3],['data-submodule',3],['data-missing',3],['metadata-symlink',3],['raw-bom',3],['raw-invalid-utf8',1],['four-mib-plus-one',(4<<20)+1],['five-mib',5<<20],['five-mib-plus-one',(5<<20)+1]];
const fixtures = ['real-ror','native-geonames','real-ror-current','native-geonames-current'];
// Each edit of a ModelSpec JSON document: the vocabulary it starts from, and the replacements it makes, in order, each of the first occurrence of a text that must be
// in the document. The document it makes is read as the target model or as the source schema of the fixture.
const top = (member) => ['{\n', `{\n  ${member},\n`];
const moved = (group, members) => [['"records": {', `"${group}": {`], ['"fields": {', `"${members}": {`]]; // the contract's record type only under another key
const edits = [
 {name:'unrelated-key-is-kept',from:'current',replace:[top('"description": "unrelated"')]},
 {name:'current-keys-under-the-earlier-identifier',from:'current',replace:[['"modelspec": "1.0-draft-2"','"modelspec": "1.0-draft"']]},
 {name:'earlier-keys-under-the-current-identifier',from:'earlier',replace:[['"modelspec": "1.0-draft"','"modelspec": "1.0-draft-2"']]},
 {name:'current-keys-under-an-unknown-identifier',from:'current',replace:[['"modelspec": "1.0-draft-2"','"modelspec": "1.0-draft-3"']]},
 {name:'earlier-keys-under-an-unknown-identifier',from:'earlier',replace:[['"modelspec": "1.0-draft"','"modelspec": "1.0-draft-3"']]},
 {name:'entities-beside-records',from:'current',replace:[top('"entities": {}')]},
 {name:'records-beside-entities',from:'earlier',replace:[top('"records": {}')]},
 {name:'properties-in-a-record-type',from:'current',replace:[['"fields": {','"properties": {}, "fields": {']]},
 {name:'fields-in-an-entity',from:'earlier',replace:[['"properties": {','"fields": {}, "properties": {']]},
 {name:'entity-on-a-field',from:'current',replace:[['"type": "string"','"entity": "x", "type": "string"']]},
 {name:'record-on-a-property',from:'earlier',replace:[['"type": "string"','"record": "x", "type": "string"']]},
 {name:'collections-under-the-current',from:'current',replace:[top('"collections": {}')]},
 {name:'recordsets-under-the-current',from:'current',replace:[top('"recordsets": {}')]},
 {name:'projections-under-the-earlier',from:'earlier',replace:[top('"projections": {}')]},
 {name:'migrations-under-the-earlier',from:'earlier',replace:[top('"migrations": {}')]},
 {name:'records-in-capitals',from:'current',replace:[['"records"','"RECORDS"']]},
 {name:'identifier-key-in-capitals',from:'current',replace:[['"modelspec"','"MODELSPEC"']]},
 {name:'module-is-another',from:'current',replace:[['"name": "','"name": "other']]},
 // A key that only folds to a word of the other vocabulary is not that word: the Directory matches the exact bytes, and so must Go.
 {name:'records-only-under-Entities',from:'current',replace:moved('Entities','properties')},
 {name:'records-only-under-ENTITIES',from:'current',replace:moved('ENTITIES','properties')},
 {name:'records-only-under-entitie-long-s',from:'current',replace:moved('entitieſ','properties')},
 {name:'fields-only-under-Properties',from:'current',replace:[['"fields": {','"Properties": {']]},
 {name:'fields-only-under-PROPERTIES',from:'current',replace:[['"fields": {','"PROPERTIES": {']]},
 {name:'fields-only-under-propertie-long-s',from:'current',replace:[['"fields": {','"propertieſ": {']]},
 {name:'entitie-long-s-null-beside-records',from:'current',replace:[top('"entitieſ": null')]},
 {name:'Entities-true-beside-records',from:'current',replace:[top('"Entities": true')]},
 {name:'Entities-list-beside-records',from:'current',replace:[top('"Entities": []')]},
 {name:'propertie-long-s-null-in-a-record-type',from:'current',replace:[['"fields": {','"propertieſ": null, "fields": {']]},
 {name:'Properties-list-in-a-record-type',from:'current',replace:[['"fields": {','"Properties": [], "fields": {']]},
 {name:'Entity-on-a-field',from:'current',replace:[['"type": "string"','"Entity": "x", "type": "string"']]},
 {name:'Records-null-beside-entities',from:'earlier',replace:[top('"Records": null')]},
 {name:'Fields-list-in-an-entity',from:'earlier',replace:[['"properties": {','"Fields": [], "properties": {']]},
 {name:'Record-on-a-property',from:'earlier',replace:[['"type": "string"','"Record": "x", "type": "string"']]},
];
const editedBytes = (bytes, edit) => {
 let text = bytes.toString('utf8');
 for (const [old, replacement] of edit.replace) {
  if (!text.includes(old)) throw Error(`${edit.name}: the text to replace is not in the document`);
  text = text.replace(old, () => replacement);
 }
 return Buffer.from(text);
};
export async function representationStages(directoryRoot, checkOnly) {
 const { checkRepresentation, verifySourceData } = await import(pathToFileURL(join(directoryRoot, 'scripts/lib/representation.mjs')).href);
 // One run of the Directory's metadata stage and then its data stage, on a fixture with a data-stage mutation and, perhaps, a model or a schema replaced
 // by `replace` ({position: 'target' | 'source', bytes}), with every hash that the replacement changes put right in the files that hold it.
 const run = (fixture, mutation, size, replace) => {
   const dir=join(fixtureRoot,fixture);
   const doc=JSON.parse(readFileSync(join(dir,'contract.json'),'utf8'));
   doc.format='ovdb-representation-contract/3';
   const provider=new Map(),pools=new Map();
   const key=(ref)=>`${ref.repository}@${ref.revision}`;
   for(const item of JSON.parse(readFileSync(join(dir,'references.json'),'utf8'))) {
    let pool=provider;
    if(item.reference.repository) {if(!pools.has(key(item.reference)))pools.set(key(item.reference),new Map());pool=pools.get(key(item.reference));}
    pool.set(item.reference.path,readFileSync(join(dir,item.file)));
   }
   if(replace?.position==='source') {
    const ref=doc.contracts[0].source.schema;
    pools.get(key(ref)).set(ref.path,replace.bytes);ref.sha256=hash(replace.bytes);
   }
   if(replace?.position==='target') {
    // The model is pinned by the contract, by the receipts that name it and by the snapshot that lists it and them: each file that holds a hash that changed
    // changes too, and the files that hold its hash after that, until none does. Nothing but a hash is edited.
    const ref=doc.contracts[0].target.model;
    const renames=new Map([[ref.sha256,hash(replace.bytes)]]);
    provider.set(ref.path,replace.bytes);
    // A file can change in more than one pass. A hash that a file had at any time is renamed to the hash it has last, so that what holds an earlier one ends right.
    for(let again=true;again;) {
     again=false;
     for(const [path,bytes] of provider) {
      if(path===ref.path||path==='contract.json')continue;
      let text=bytes.toString('latin1');
      for(const [from,to] of renames)text=text.replaceAll(from,to);
      const next=Buffer.from(text,'latin1');
      if(!next.equals(bytes)) {
       const was=hash(bytes),now=hash(next);
       for(const [from,to] of renames)if(to===was)renames.set(from,now);
       renames.set(was,now);provider.set(path,next);again=true;
      }
     }
    }
    let text=JSON.stringify(doc);
    for(const [from,to] of renames)text=text.replaceAll(from,to);
    Object.assign(doc,JSON.parse(text));
   }
   const body=mutation==='raw-bom'?Buffer.from([239,187,191]):mutation==='raw-invalid-utf8'?Buffer.from([255]):Buffer.alloc(size,120);
   const ref={repository:'https://github.com/example/input',revision:'d'.repeat(40),path:'input/$records/rows.json',sha256:hash(body)};
   doc.contracts[0].source.data=ref;
   const input=new Map([[ref.path,body]]);pools.set(key(ref),input);
   if(mutation==='wrong-data-hash')ref.sha256='b'.repeat(64);
   const touched=[];
   const reader=(files,commit,inputReader=false)=>({commit,status(path){if(inputReader&&path===ref.path){if(mutation==='data-symlink')return 'symlink';if(mutation==='data-submodule')return 'submodule';if(mutation==='data-missing')return 'missing';}if(!inputReader&&path===doc.contracts[0].target.binding.document.path&&mutation==='metadata-symlink')return 'symlink';return files.has(path)?'file':'missing';},readBytes(path,limit){touched.push({path,inputReader,limit});const bytes=files.get(path);if(!bytes||bytes.length>limit)throw Error('missing or oversize');return bytes;}});
   const dependencies=new Map([...pools].map(([id,files])=>[id,reader(files,id.split('@')[1],files===input)]));
   if(mutation==='missing-data-reader')dependencies.delete(key(ref));
   if(mutation==='wrong-data-head')dependencies.get(key(ref)).commit='e'.repeat(40);
   const outer=fixture.startsWith('real-ror')?'https://github.com/ingitdb/ror-ingitdb':'https://github.com/ingitdb/geo-ingitdb';
   const data=Buffer.from(JSON.stringify(doc));provider.set('contract.json',data);
   const c=doc.contracts[0];
   const m={model:{modelspec:c.target.model.path},meaning:{file:c.target.binding.document.path},recordsets:[c.target.entity]};
   const checked=checkRepresentation({path:'contract.json',sha256:hash(data)},reader(provider,'a'.repeat(40)),m,outer,dependencies,()=>true);
   if(touched.some((read)=>read.inputReader||read.path===c.native.dataset.path))throw Error('metadata read source or native data');
   let stage='outside_scope';if(checked.document){try{verifySourceData(checked.document,dependencies);stage='checked';}catch{stage='refused';}}
   return {metadata:checked.problems.length===0,stage,problems:checked.problems};
 };
 const cases=[];
 for (const fixture of fixtures) {
  for (const [mutation,size] of mutations) {
   const {metadata,stage,problems}=run(fixture,mutation,size);
   const expectedMetadata=mutation!=='metadata-symlink';
   const expectedData=!expectedMetadata?'outside_scope':['missing-data-reader','wrong-data-head','wrong-data-hash','data-symlink','data-submodule','data-missing','five-mib-plus-one'].includes(mutation)?'refused':'checked';
   if(metadata!==expectedMetadata||stage!==expectedData)throw Error(`${fixture}/${mutation}: unexpected ${metadata}/${stage}: ${problems}`);
   cases.push({fixture,mutation,size,metadata,data:stage});
  }
 }
 // The edits, on the two fixtures in the current vocabulary, made to the target model and to the source schema in turn. An edit that starts from the earlier
 // vocabulary starts from the document of the earlier fixture, which has the same path and the same name in references.json.
 const vocabulary=[];
 for (const fixture of fixtures.filter((name)=>name.endsWith('-current'))) {
  const contract=JSON.parse(readFileSync(join(fixtureRoot,fixture,'contract.json'),'utf8')).contracts[0];
  const list=JSON.parse(readFileSync(join(fixtureRoot,fixture,'references.json'),'utf8'));
  for (const position of ['target','source']) {
   const path=position==='target'?contract.target.model.path:contract.source.schema.path;
   const file=list.find((item)=>item.reference.path===path).file;
   for (const edit of edits) {
    const bytes=editedBytes(readFileSync(join(fixtureRoot,edit.from==='current'?fixture:fixture.replace(/-current$/,''),file)),edit);
    const {metadata}=run(fixture,'none',3,{position,bytes});
    vocabulary.push({fixture,position,edit:edit.name,from:edit.from,replace:edit.replace,bytes:hash(bytes),metadata});
   }
  }
 }
 const output=JSON.stringify({format:'ovdb-representation-stage-reference/1',references:{directory:references.directory},strength:'structural-metadata-and-offline-raw-bytes-only',cases,vocabulary},null,2)+'\n';
 const file=join(here,'representation-stages.json');
 if(checkOnly){if(readFileSync(file,'utf8')!==output)throw Error('representation stage golden stale');}else writeFileSync(file,output);
 return hash(output);
}
