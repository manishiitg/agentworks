const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { migrateAccountAvailability } = require('./accountAvailabilityMigration');

test('Desktop pre-start preserves account controls and backups, with no app or login', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'account-audience-'));
  try {
    const docs = path.join(root, 'docs'), state = path.join(root, 'private-state');
    fs.mkdirSync(path.join(docs, 'config'), { recursive: true });
    fs.writeFileSync(path.join(docs, 'config/users.json'), JSON.stringify({ users: [{ id: 'person-a', email: 'person@example.com' }] }));
    const file = path.join(docs, 'config/provider-account-settings.json');
    const before = JSON.stringify({ available_to: { 'codex-cli': { products: ['work'] }, 'muse-cli': { admins: true, users: ['PERSON@example.com'] } }, token_limits: { 'codex-cli': { daily: 1000 } } });
    fs.writeFileSync(file, before);
    const env = { AGENTWORKS_PROVIDER_POLICY: '{"cursor-cli":{"available_to":{"products":["agentworks"]},"pinned":true}}', WORKSPACE_API_URL: 'http://example.com' };
    const originalPolicy = env.AGENTWORKS_PROVIDER_POLICY;
    migrateAccountAvailability({ workspaceDir: docs, stateDir: state, env });
    assert.deepEqual(JSON.parse(fs.readFileSync(file)), { available_to: { 'codex-cli': 'all', 'muse-cli': { users: ['person-a'] } }, token_limits: { 'codex-cli': { daily: 1000 } } });
    assert.deepEqual(JSON.parse(env.AGENTWORKS_PROVIDER_POLICY), { 'cursor-cli': { available_to: 'all', pinned: true } });
    assert.equal(env.WORKSPACE_API_URL, 'http://example.com');
    const backups = path.join(state, 'migration-backups');
    const backup = path.join(backups, fs.readdirSync(backups)[0]);
    assert.equal(fs.readFileSync(path.join(backup, 'provider-account-settings.json'), 'utf8'), before);
    assert.equal(fs.readFileSync(path.join(backup, 'inherited-policy.json'), 'utf8'), originalPolicy);
    assert.equal(fs.statSync(path.join(backup, 'inherited-policy.json')).mode & 0o777, 0o600);
    migrateAccountAvailability({ workspaceDir: docs, stateDir: state, env });
    assert.equal(fs.readdirSync(backups).length, 1);
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});
