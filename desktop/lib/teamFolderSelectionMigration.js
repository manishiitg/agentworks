// PLAT-746-1020: one-off pre-start/offline conversion. Delete with the 976
// hook after every server and Mac has moved; never called by a runtime reader.
const fs = require('fs');
const path = require('path');
const { randomUUID } = require('crypto');

const PRIVATE = new Set(['memory', 'team-copies', 'mac-dashboards', '_system', '_files', 'tool_output_folder', 'runs', 'settings', 'db', 'planning', 'knowledgebase', 'conversation', 'costs', 'human_inputs', 'builder', 'chat_history', 'browser-profile', 'browser-data', 'node_modules', 'bower_components', 'jspm_packages', 'site-packages', '__pycache__', 'venv', 'credentials', 'secrets', 'keys', 'auth.json', 'kubeconfig', 'credentials.json', 'token.json', 'tokens.json', 'product.json', 'workflow.json', 'functions.json', 'schedules.json', 'webhooks.json', 'product-schedules.json', 'schedule-runs.json']);
function ordinaryFolder(name) {
  if (typeof name !== 'string' || !name || name.trim() !== name || name.normalize('NFC') !== name || /[\\/\x00-\x1f\x7f]/.test(name) || ['.', '..'].includes(name)) return false;
  const lower = name.toLowerCase();
  return !PRIVATE.has(lower) && (!name.startsWith('.') || ['.agents', '.github'].includes(name)) && !/\.(pem|key|p12|pfx|kdbx|tfstate|env|ppk|jks|keystore|gpg|asc|tfvars)$/i.test(name) && !['id_rsa', 'id_dsa', 'id_ed25519', 'id_ecdsa'].includes(lower) && !(lower.startsWith('service-account') && lower.endsWith('.json'));
}
function plain(root, file) {
  const rel = path.relative(root, file);
  if (rel === '..' || rel.startsWith('..' + path.sep) || path.isAbsolute(rel)) throw new Error('Team state outside root');
  let current = root;
  for (const part of ['', ...rel.split(path.sep)]) {
    current = path.join(current, part);
    try { if (fs.lstatSync(current).isSymbolicLink()) throw new Error('Linked Team state'); }
    catch (e) { if (e.code !== 'ENOENT') throw e; }
  }
  return file;
}
function entries(root, dir) {
  plain(root, dir);
  try { return fs.readdirSync(dir, { withFileTypes: true }).sort((a,b) => a.name.localeCompare(b.name)); }
  catch (e) { if (e.code === 'ENOENT') return []; throw e; }
}
function json(root, file, optional = false) {
  plain(root, file);
  try { const before = fs.readFileSync(file); return { file, before, document: JSON.parse(before) }; }
  catch (e) { if (optional && e.code === 'ENOENT') return null; throw e; }
}
function bindings(root, dir) {
  const out = [];
  for (const e of entries(root, dir)) {
    const file = plain(root, path.join(dir, e.name));
    if (e.isDirectory()) out.push(...bindings(root, file));
    else if (e.name === 'binding.json') out.push(json(root,file));
  }
  return out;
}
function topFolders(root) {
  // The root comes only from the trusted ownership registry, never a Team path
  // or a command-line per-project override. Do not follow linked directories.
  if (!path.isAbsolute(root) || fs.realpathSync(root) !== root || !fs.statSync(root).isDirectory()) throw new Error('Invalid Crew root');
  return fs.readdirSync(root, {withFileTypes:true}).filter(e => e.isDirectory() && !e.isSymbolicLink() && ordinaryFolder(e.name)).map(e=>e.name).sort();
}
function migrateTeamFolderSelections({stateDir, docsDir, local = false, apply = true, serverPlan, log = console.log}) {
  const state = path.resolve(stateDir), planned = [], backupOnly = [], affected = new Set();
  const registry = json(state, path.join(state,'ownership','projects.json'), true)?.document.projects || {};
  for (const entry of entries(state,path.join(state,'team-folders'))) {
    if (entry.isSymbolicLink()) throw new Error('Linked Team state');
    if (!entry.isDirectory()) continue;
    const item = json(state,path.join(state,'team-folders',entry.name,'team.json'),true);
    if (!item) continue;
    const team = item.document, folder = team.folder || {}, wasWhole = folder.whole === true;
    if (!Object.hasOwn(folder,'whole') && !Object.hasOwn(team,'last_folders')) continue;
    if (folder.path_ids_version !== 1 || !Array.isArray(folder.paths || []) || !Array.isArray(folder.folders || [])) throw new Error('Convert Team path identities first');
    if (typeof team.project_id !== 'string' || !team.project_id || team.project_id.length > 128 || ['.', '..'].includes(team.project_id) || /[\\/\x00-\x20\x7f]/.test(team.project_id)) throw new Error('Invalid Team project');
    if (Object.hasOwn(folder,'whole') && typeof folder.whole !== 'boolean') throw new Error('Invalid Team selection');
    const ids = new Set(), names = new Set();
    for (const p of folder.paths || []) {
      if (typeof p.id !== 'string' || !p.id || typeof p.path !== 'string' || !p.path || ids.has(p.id) || names.has(p.path)) throw new Error('Invalid Team path identities');
      ids.add(p.id); names.add(p.path);
    }
    let selected = (folder.folders || []).map(p=>p.path), rootKind = "unchanged", omitted = 0, authoritative = true;
    if (selected.some(p=>typeof p !== 'string')) throw new Error('Invalid Team folder selection');
    if (wasWhole) {
      const records = Object.values(registry).filter(r => r.product === 'work' && r.project_id === team.project_id && r.owner_id === team.owner_id);
      if (records.length > 1) throw new Error('Ambiguous Crew ownership');
      const r = records[0], sync = json(state,path.join(state,'crew-sync',team.project_id+'.json'),true)?.document;
      if (!local) {
        const row = serverPlan?.[team.project_id];
        if (!row || row.owner !== team.owner_id || !['found','missing','remote'].includes(row.root) || !Array.isArray(row.folders) || !row.folders.length || row.folders.length > 20 || row.folders.some(p=>typeof p !== 'string' || !p || p.startsWith('/') || /[\\\x00-\x1f\x7f]/.test(p) || p.split('/').some(part=>!part || part==='.' || part==='..'))) throw new Error('Generate the server folder selection plan with this release first');
        rootKind = row.root; selected = row.folders; omitted = row.omitted || 0;
      } else {
        // A member Mac has bindings and a local member_of marker, not an
        // authoritative Team store. Never invent its server's selection.
        const localTeam = json(state,path.join(state,'crew-sync',team.project_id+'-team.json'),true)?.document;
        authoritative = !!r && !localTeam?.member_of;
        let root = null;
        if (authoritative && !(sync?.side === 'server' && sync?.mode === 'relay')) {
          if (r.local_root) root = path.resolve(r.local_root);
          else if (docsDir && typeof r.folder === 'string' && r.folder && !/[\\/]/.test(r.folder) && !r.folder.startsWith('.')) root = path.join(path.resolve(docsDir),'Crew',r.folder);
        }
        rootKind = authoritative ? 'missing' : 'remote';
        if (root) {
          try { selected = topFolders(root); rootKind = 'found'; }
          catch (e) { if (e.code !== 'ENOENT' && e.code !== 'ENOTDIR') throw e; }
        }
        if (rootKind !== 'found') selected = authoritative ? ['team'] : selected;
        if (selected.length > 20) { omitted = selected.length - 20; selected = selected.slice(0,20); }
      }
      if (authoritative && rootKind !== 'found') team.folder_note = 'Only team/ is shared for now. Open the owner’s Mac and add the folders the Team should share.';
      if (omitted) team.folder_note = 'The Team shares 20 folders. Review the chosen folders in Team settings; other folders are not shared.';
      // A Crew with no ordinary directories still needs an explicit selection.
      if (authoritative && !selected.length) selected = ['team'];
      const known = new Map((folder.paths || []).map(p=>[p.path,p]));
      if (authoritative) folder.folders = selected.map(name => {
        const old = known.get(name);
        if (old?.file) throw new Error('Shared folder identity names a file');
        const p = old || {id:randomUUID(),path:name};
        if (!old) { folder.paths ||= []; folder.paths.push(p); }
        return p;
      });
    }
    delete folder.whole; delete team.last_folders; team.folder = folder;
    if (!Number.isSafeInteger(team.revision || 0) || (team.revision || 0) < 0) throw new Error('Invalid Team revision');
    team.revision = (team.revision || 0) + 1;
    planned.push({...item, project:team.project_id, count:selected.length, wasWhole, rootKind, omitted, authoritative}); affected.add(team.project_id);
  }
  // Current binding.json stores inline file baselines, mounts and receipts, not
  // a Share field. Preserve all bytes and IDs; do not invent a runtime field.
  for (const item of bindings(state,path.join(state,'team-working'))) {
    if (affected.has(item.document.project_id)) backupOnly.push(item);
  }
  if (apply && planned.length) {
    const suffix = '.before-folder-selection-' + Date.now() + '-' + randomUUID();
    // Every source and inline baseline is backed up before the first write.
    for (const item of [...planned,...backupOnly]) fs.writeFileSync(item.file+suffix,item.before,{mode:0o600,flag:'wx'});
    for (const item of planned) {
      const temp = item.file + '.convert-' + randomUUID();
      try { fs.writeFileSync(temp,JSON.stringify(item.document,null,2)+'\n',{mode:0o600,flag:'wx'}); fs.renameSync(temp,item.file); }
      finally { if (fs.existsSync(temp)) fs.unlinkSync(temp); }
      log(`[TEAM_FOLDER_SELECTION_MIGRATION] project=${item.project} from=${item.wasWhole?'whole':'folders'} root=${item.rootKind} folders=${item.count} omitted=${item.omitted} outcome=${item.authoritative?'converted':'keys_removed'}`);
    }
  }
  return {converted:planned.length, bindings:backupOnly.length, projects:planned.map(p=>({project:p.project,folders:p.count,root:p.rootKind,omitted:p.omitted,selection:p.document.folder.folders.map(f=>f.path),from:p.wasWhole?'whole':'folders'}))};
}
module.exports = {migrateTeamFolderSelections, ordinaryFolder};
if (require.main === module) {
  const args=process.argv.slice(2), arg = key => {const i=args.indexOf(key);return i<0?undefined:args[i+1];};
  if (!arg('--state-root') || !arg('--docs-root')) throw new Error('Use --state-root and --docs-root; preview is the default, --apply requires the stopped service');
  console.log(JSON.stringify(migrateTeamFolderSelections({stateDir:arg('--state-root'),docsDir:arg('--docs-root'),local:args.includes('--local'),serverPlan:arg('--plan')?JSON.parse(fs.readFileSync(arg('--plan'))):undefined,apply:args.includes('--apply')})));
}
