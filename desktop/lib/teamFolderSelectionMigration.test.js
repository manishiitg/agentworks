const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');
const os = require('os');
const {migrateTeamFolderSelections, ordinaryFolder} = require('./teamFolderSelectionMigration');

// 1020: source folders come from the ownership registry, IDs/baselines survive,
// all original bytes are saved before changes, and offline Mac roots are not guessed.
test('folder-only conversion preserves identities, member baselines and backed-up bytes without following private or linked roots', () => {
 const root=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'team-selection-')));
 const state=path.join(root,'state'), docs=path.join(root,'docs'), crew=path.join(docs,'Crew','owned');
 const write=(f,v)=>{fs.mkdirSync(path.dirname(f),{recursive:true});fs.writeFileSync(f,JSON.stringify(v));};
 try {
  for(const name of ['reports','code','.github','memory','team-copies','node_modules','secrets','db','.ssh']) fs.mkdirSync(path.join(crew,name),{recursive:true});
  fs.symlinkSync(path.join(crew,'reports'),path.join(crew,'linked'));
  fs.writeFileSync(path.join(crew,'root-file.md'),'not a folder');
  write(path.join(state,'ownership','projects.json'),{projects:{one:{product:'work',project_id:'owned',owner_id:'owner',folder:'owned'}}});
  const metadata={project_id:'owned',owner_id:'owner',revision:7,agents:{a:{in_team:false}},people:[{user_id:'member'}],last_folders:['old'],folder:{id:'store',path_ids_version:1,whole:true,folders:[],paths:[{id:'report-id',path:'reports'},{id:'skill-id',path:'skills/shared'}]}};
  const file=path.join(state,'team-folders','owned','team.json');write(file,metadata);
  const remote=path.join(state,'team-folders','offline','team.json');write(remote,{...metadata,project_id:'offline'});
  write(path.join(state,'crew-sync','offline.json'),{mode:'relay',side:'server',peer_project_id:'mac'});
  const binding=path.join(state,'team-working','member','binding.json');write(binding,{project_id:'owned',folder_id:'store',files:{'reports/a.md':{hash:'unchanged'}},mounts:{'report-id':{remote:'reports',local:'reports'}}});
  const before=fs.readFileSync(file), baseline=fs.readFileSync(binding), logs=[];
  assert.equal(migrateTeamFolderSelections({stateDir:state,docsDir:docs,apply:false,log:x=>logs.push(x)}).converted,2);
  assert.deepEqual(fs.readFileSync(file),before);assert.deepEqual(fs.readFileSync(binding),baseline);assert.equal(logs.length,0);
  const result=migrateTeamFolderSelections({stateDir:state,docsDir:docs,log:x=>logs.push(x)});
  assert.equal(result.converted,2);assert.equal(result.bindings,1);
  const converted=JSON.parse(fs.readFileSync(file));
  assert.deepEqual(converted.folder.folders.map(p=>p.path),['.github','code','reports']);
  assert.equal(converted.folder.folders.find(p=>p.path==='reports').id,'report-id');
  assert.deepEqual(converted.agents,metadata.agents);assert.deepEqual(converted.people,metadata.people);assert.equal(converted.revision,8);
  assert.equal(converted.folder.whole,undefined);assert.equal(converted.last_folders,undefined);
  assert.deepEqual(fs.readFileSync(binding),baseline);
  const backups=f=>fs.readdirSync(path.dirname(f)).filter(n=>n.startsWith(path.basename(f)+'.before-folder-selection-'));
  assert.equal(backups(file).length,1);assert.deepEqual(fs.readFileSync(path.join(path.dirname(file),backups(file)[0])),before);
  assert.equal(backups(binding).length,1);assert.deepEqual(fs.readFileSync(path.join(path.dirname(binding),backups(binding)[0])),baseline);
  const offline=JSON.parse(fs.readFileSync(remote));assert.deepEqual(offline.folder.folders.map(p=>p.path),['team']);assert.ok(offline.folder_note);
  assert.equal(logs.length,2);assert.ok(logs.every(line=>line.includes('from=whole')&&!line.includes('reports')));
  const once=fs.readFileSync(file);assert.equal(migrateTeamFolderSelections({stateDir:state,docsDir:docs}).converted,0);assert.deepEqual(fs.readFileSync(file),once);
  // A missing source directory uses the same explicit fallback; an existing
  // selected-folder record retains exactly its IDs and member layout.
  write(path.join(state,'ownership','projects.json'),{projects:{one:{product:'work',project_id:'owned',owner_id:'owner',folder:'absent'}}});
  write(file,metadata);
  const selectedFile=path.join(state,'team-folders','selected','team.json');
  write(selectedFile,{...metadata,project_id:'selected',folder:{id:'selected-store',path_ids_version:1,whole:false,folders:[{id:'keep-id',path:'chosen'}],paths:[{id:'keep-id',path:'chosen'}]}});
  const fallback=migrateTeamFolderSelections({stateDir:state,docsDir:docs,log:()=>{}});
  assert.equal(fallback.converted,2);
  assert.deepEqual(JSON.parse(fs.readFileSync(file)).folder.folders.map(p=>p.path),['team']);
  assert.deepEqual(JSON.parse(fs.readFileSync(selectedFile)).folder.folders,[{id:'keep-id',path:'chosen'}]);
  const backupCount=backups(file).length;
  // Validation of every input precedes backups/writes, including an unsafe second record.
  write(file,metadata);fs.symlinkSync(file,path.join(state,'team-folders','unsafe'));
  assert.throws(()=>migrateTeamFolderSelections({stateDir:state,docsDir:docs}),/Linked Team state/);
  assert.deepEqual(fs.readFileSync(file),before);assert.equal(backups(file).length,backupCount);
  for(const name of ['memory','keys','.env','credentials','secrets','db','settings','team-copies','a/b','a\\b','e\u0301'])assert.equal(ordinaryFolder(name),false,name);
 } finally {fs.rmSync(root,{recursive:true,force:true});}
});
