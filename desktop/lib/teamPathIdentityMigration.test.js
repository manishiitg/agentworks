const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { createHash } = require('crypto');
const { migrateTeamPathIdentities } = require('./teamPathIdentityMigration');

// PLAT-746-976: the packaged app must move saved Team metadata without Python,
// retaining every original byte in a private backup before the first write.
test('pre-start Team conversion backs up identities and member baselines once, refusing unsafe state before writes', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'team-identity-'));
  const write = (file, body) => { fs.mkdirSync(path.dirname(file), { recursive: true }); fs.writeFileSync(file, JSON.stringify(body)); };
  try {
    const state = path.join(root, 'state'), file = path.join(state, 'team-folders', 'team', 'team.json');
    const folderID = 'store-1', team = { project_id: 'project-1', owner_id: 'owner', folder: { id: folderID, whole: true, folders: ['docs'] }, agents: { 'agent-1': { in_team: false } } };
    write(file, team);
    const digest = createHash('sha256').update(folderID + '\0chunks-1').digest('hex');
    const canonical = path.join(path.dirname(file), 'folders', digest, 'files');
    fs.mkdirSync(path.join(canonical, 'skills', 'shared-skill'), { recursive: true });
    fs.writeFileSync(path.join(canonical, 'report.md'), 'ordinary shared file');
    const copy = path.join(root, 'copy'); fs.mkdirSync(path.join(copy, 'docs'), { recursive: true });
    const bindingFile = path.join(state, 'team-working', 'member', 'binding.json');
    const bases = { 'docs/a.txt': { version: { hash: 'keep-base' } } };
    write(bindingFile, { project_id: team.project_id, folder_id: folderID, remote_user: 'member', local_folder: copy, files: bases });
    write(path.join(state, 'ownership', 'projects.json'), {projects: {owned: {product:'work', project_id:team.project_id, owner_id:'owner', local_root:copy}}});
    const original = fs.readFileSync(file), originalBinding = fs.readFileSync(bindingFile), logs = [];
    const result = migrateTeamPathIdentities({ stateDir: state, log: line => logs.push(line) });
    assert.equal(result.converted, 1); assert.equal(result.bindings, 1);
    assert.deepEqual(fs.readFileSync(path.join(result.backup, path.relative(state, file))), original);
    assert.deepEqual(fs.readFileSync(path.join(result.backup, path.relative(state, bindingFile))), originalBinding);
    assert.equal(fs.statSync(path.join(result.backup, path.relative(state, file))).mode & 0o777, 0o600);
    const converted = JSON.parse(fs.readFileSync(file)), paths = new Map(converted.folder.paths.map(p => [p.path, p]));
    assert.deepEqual([...paths.keys()].sort(), ['docs', 'report.md', 'skills/shared-skill']);
    assert.equal(new Set([...paths.values()].map(p => p.id)).size, 3);
    assert.equal(paths.get('report.md').file, true);
    assert.deepEqual(converted.agents, team.agents);
    const binding = JSON.parse(fs.readFileSync(bindingFile));
    assert.deepEqual(binding.files, bases); assert.equal(binding.mounted, true);
    assert.equal(binding.mounts[paths.get('docs').id].inode, fs.statSync(path.join(copy, 'docs')).ino);
    const once = fs.readFileSync(file);
    assert.equal(migrateTeamPathIdentities({ stateDir: state, log: line => logs.push(line) }).converted, 0);
    assert.deepEqual(fs.readFileSync(file), once);
    assert.equal(fs.readdirSync(path.join(state, 'backups')).length, 1);
    assert.equal(logs.filter(line => line.startsWith('[TEAM_PATH_ID_MIGRATION]')).length, 2);
    assert.equal(logs.filter(line => line.startsWith('[TEAM_FOLDER_SELECTION_MIGRATION]')).length, 1);
    assert.equal(converted.folder.whole, undefined);
    assert.deepEqual(converted.folder.folders.map(p=>p.path), ['docs']);

    // A bad second record must not partially migrate the first or create a
    // misleading backup receipt. Linked private content is not traversed.
    for (const kind of ['mixed', 'link']) {
      const badState = path.join(root, kind), first = path.join(badState, 'team-folders', 'a', 'team.json');
      write(first, team);
      if (kind === 'mixed') write(path.join(badState, 'team-folders', 'z', 'team.json'), { ...team, folder: { folders: ['docs', { id: 'unknown', path: 'other' }] } });
      else fs.symlinkSync(copy, path.join(badState, 'team-folders', 'z'));
      const before = fs.readFileSync(first);
      assert.throws(() => migrateTeamPathIdentities({ stateDir: badState, log: () => {} }), /Could not prepare Team/);
      assert.deepEqual(fs.readFileSync(first), before);
      assert.equal(fs.existsSync(path.join(badState, 'backups')), false);
    }
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});
