// Like-strength structural metadata and raw-data stages. canonicalMeaning is a
// synthetic fixture association, never a canonical semantic admission verdict.
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { references } from '../../../references.mjs';
const here = dirname(fileURLToPath(import.meta.url));
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
export async function representationStages(directoryRoot, checkOnly) {
 const { checkRepresentation, verifySourceData } = await import(pathToFileURL(join(directoryRoot, 'scripts/lib/representation.mjs')).href);
 const cases=[];
 for (const fixture of ['real-ror','native-geonames']) {
  for (const [mutation,size] of [['none',3],['missing-data-reader',3],['wrong-data-head',3],['wrong-data-hash',3],['data-symlink',3],['data-submodule',3],['data-missing',3],['metadata-symlink',3],['raw-bom',3],['raw-invalid-utf8',1],['four-mib-plus-one',(4<<20)+1],['five-mib',5<<20],['five-mib-plus-one',(5<<20)+1]]) {
   const dir=join(here,'../../../../../publisher/representation/testdata',fixture);
   const doc=JSON.parse(readFileSync(join(dir,'contract.json'),'utf8'));
   doc.format='ovdb-representation-contract/3';
   const provider=new Map(),pools=new Map();
   const key=(ref)=>`${ref.repository}@${ref.revision}`;
   for(const item of JSON.parse(readFileSync(join(dir,'references.json'),'utf8'))) {
    let pool=provider;
    if(item.reference.repository) {if(!pools.has(key(item.reference)))pools.set(key(item.reference),new Map());pool=pools.get(key(item.reference));}
    pool.set(item.reference.path,readFileSync(join(dir,item.file)));
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
   const outer=fixture==='real-ror'?'https://github.com/ingitdb/ror-ingitdb':'https://github.com/ingitdb/geo-ingitdb';
   const data=Buffer.from(JSON.stringify(doc));provider.set('contract.json',data);
   const c=doc.contracts[0];
   const m={model:{modelspec:c.target.model.path},meaning:{file:c.target.binding.document.path},recordsets:[c.target.entity]};
   const checked=checkRepresentation({path:'contract.json',sha256:hash(data)},reader(provider,'a'.repeat(40)),m,outer,dependencies,()=>true);
   if(touched.some((read)=>read.inputReader||read.path===c.native.dataset.path))throw Error('metadata read source or native data');
   let stage='outside_scope';if(checked.document){try{verifySourceData(checked.document,dependencies);stage='checked';}catch{stage='refused';}}
   const metadata=checked.problems.length===0;
   const expectedMetadata=mutation!=='metadata-symlink';
   const expectedData=!expectedMetadata?'outside_scope':['missing-data-reader','wrong-data-head','wrong-data-hash','data-symlink','data-submodule','data-missing','five-mib-plus-one'].includes(mutation)?'refused':'checked';
   if(metadata!==expectedMetadata||stage!==expectedData)throw Error(`${fixture}/${mutation}: unexpected ${metadata}/${stage}: ${checked.problems}`);
   cases.push({fixture,mutation,size,metadata,data:stage});
  }
 }
 const output=JSON.stringify({format:'ovdb-representation-stage-reference/1',references:{directory:references.directory},strength:'structural-metadata-and-offline-raw-bytes-only',cases},null,2)+'\n';
 const file=join(here,'representation-stages.json');
 if(checkOnly){if(readFileSync(file,'utf8')!==output)throw Error('representation stage golden stale');}else writeFileSync(file,output);
 return hash(output);
}
