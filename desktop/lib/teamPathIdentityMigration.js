// PLAT-746-976: one-off pre-start conversion, removed after all laptops move.
// Matches the offline server migration; no legacy decoder stays in the app.
const fs = require('fs');
const path = require('path');
const { createHash, randomUUID } = require('crypto');

function ordinary(value) {
  if (typeof value !== 'string' || !value || value.startsWith('/') || value.includes('\\') || value.split('/').some(p => ['', '.', '..'].includes(p))) {
    throw new Error('Invalid shared path');
  }
  return value;
}

function plain(root, file) {
  const rel = path.relative(root, file);
  if (rel.startsWith('..' + path.sep) || rel === '..' || path.isAbsolute(rel)) throw new Error('Team record outside state');
  let current = root;
  for (const part of ['', ...rel.split(path.sep)]) {
    current = path.join(current, part);
    try { if (fs.lstatSync(current).isSymbolicLink()) throw new Error('Linked Team state'); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
  return file;
}

function entries(root, dir) {
  plain(root, dir);
  try { return fs.readdirSync(dir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name)); }
  catch (error) { if (error.code === 'ENOENT') return []; throw error; }
}

function bindingFiles(root, dir) {
  const files = [];
  for (const entry of entries(root, dir)) {
    const file = plain(root, path.join(dir, entry.name));
    if (entry.isDirectory()) files.push(...bindingFiles(root, file));
    else if (entry.name === 'binding.json') files.push(file);
  }
  return files;
}

function migrateTeamPathIdentities({ stateDir, log = console.log }) {
  const root = path.resolve(stateDir), planned = [], teams = new Map();
  let teamCount = 0, backup = '';
  const read = file => {
    const before = fs.readFileSync(plain(root, file));
    return { file, before, document: JSON.parse(before.toString('utf8')) };
  };
  try {
    for (const entry of entries(root, path.join(root, 'team-folders'))) {
      const dir = plain(root, path.join(root, 'team-folders', entry.name));
      if (!entry.isDirectory()) continue;
      const file = path.join(dir, 'team.json');
      if (!fs.existsSync(plain(root, file))) continue;
      const change = read(file), team = change.document, folder = team.folder || {};
      if (folder.path_ids_version === 1) { teams.set(team.project_id, team); continue; }
      const selected = folder.folders || [];
      if (!Array.isArray(selected) || selected.some(p => typeof p !== 'string') || (!selected.length && folder.paths?.length)) {
        throw new Error('Unexpected Team identity schema');
      }
      const paths = selected.map(name => ({ id: randomUUID(), path: ordinary(name) }));
      const known = new Set(paths.map(p => p.path));
      const digest = createHash('sha256').update((folder.id || '') + '\0chunks-1').digest('hex');
      const files = path.join(dir, 'folders', digest, 'files');
      const add = (name, child) => {
        if (!known.has(name)) { paths.push({ id: randomUUID(), path: ordinary(name), ...(child.isFile() ? { file: true } : {}) }); known.add(name); }
      };
      if (folder.whole) {
        const excluded = new Set(['skills', 'MEMORY.md', 'memory', 'PROJECT_INSTRUCTIONS.md', 'team-setup.json', '.agentworks', 'team-copies']);
        for (const child of entries(root, files)) {
          if (excluded.has(child.name)) continue;
          plain(root, path.join(files, child.name));
          add(child.name, child);
        }
      }
      for (const child of entries(root, path.join(files, 'skills'))) {
        plain(root, path.join(files, 'skills', child.name));
        add('skills/' + child.name, child);
      }
      folder.path_ids_version = 1;
      folder.paths = paths;
      folder.folders = selected.map(name => paths.find(p => p.path === name));
      team.folder = folder;
      teams.set(team.project_id, team);
      planned.push(change); teamCount++;
    }
    for (const file of bindingFiles(root, path.join(root, 'team-working'))) {
      const change = read(file), binding = change.document, team = teams.get(binding.project_id);
      if (!team || binding.remote_user === team.owner_id || binding.mounted || binding.folder_id !== team.folder.id) continue;
      if (!path.isAbsolute(binding.local_folder || '')) throw new Error('Invalid Team copy root');
      plain(binding.local_folder, binding.local_folder);
      const folder = team.folder, active = new Set(folder.folders.map(p => p.path)), mounts = {};
      for (const entry of folder.paths) {
        const name = ordinary(entry.path);
        if (!folder.whole && !active.has(name) && !name.startsWith('skills/')) continue;
        const physical = plain(binding.local_folder, path.join(binding.local_folder, name));
        const mount = { remote: name, local: name };
        try {
          const info = fs.statSync(physical);
          if (Number.isSafeInteger(info.dev) && Number.isSafeInteger(info.ino)) Object.assign(mount, { device: info.dev, inode: info.ino });
        } catch (error) { if (error.code !== 'ENOENT') throw error; }
        mounts[entry.id] = mount;
      }
      Object.assign(binding, { mounted: true, mounts });
      planned.push(change);
    }
    // Validate every record before backing up, then back up every input before
    // replacing any of them. Only Team JSON changes; chats/files/SQLite do not.
    if (planned.length) {
      backup = plain(root, path.join(root, 'backups', `team-path-ids-${Date.now()}-${randomUUID()}`));
      fs.mkdirSync(backup, { recursive: true, mode: 0o700 });
      for (const change of planned) {
        const saved = path.join(backup, path.relative(root, change.file));
        fs.mkdirSync(path.dirname(saved), { recursive: true, mode: 0o700 });
        fs.writeFileSync(saved, change.before, { mode: 0o600, flag: 'wx' });
      }
      for (const change of planned) {
        const temporary = change.file + '.migrate-' + randomUUID();
        try {
          fs.writeFileSync(temporary, JSON.stringify(change.document, null, 2) + '\n', { mode: 0o600, flag: 'wx' });
          fs.renameSync(temporary, change.file);
        } finally { fs.rmSync(temporary, { force: true }); }
      }
    }
    log(`[TEAM_PATH_ID_MIGRATION] outcome=done converted=${teamCount} bindings=${planned.length - teamCount} backup=${JSON.stringify(backup || 'none')}`);
    return { converted: teamCount, bindings: planned.length - teamCount, backup };
  } catch (error) {
    log(`[TEAM_PATH_ID_MIGRATION] outcome=failed reason=conversion_failed backup=${JSON.stringify(backup || 'none')}`);
    throw new Error(`Could not prepare Team folders. No local server was started. Backup: ${backup || 'none'}.`);
  }
}

module.exports = { migrateTeamPathIdentities };
