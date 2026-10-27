// PLAT-693 one-off pre-start migration for packaged Desktop (no Python needed).
// Remove after installations migrate; this is not a runtime policy fallback.
const fs = require('fs');
const path = require('path');

function audience(value, people) {
  if (value == null) return value;
  if (typeof value === 'string') {
    const word = value.trim().toLowerCase();
    if (['all', 'everyone', 'admins'].includes(word)) return word === 'everyone' ? 'all' : word;
    throw new Error('Unknown account audience');
  }
  if (typeof value !== 'object' || Array.isArray(value) || Object.keys(value).some(key => !['admins', 'users', 'products'].includes(key))) throw new Error('Unknown account audience');
  const users = value.users || [], products = value.products || [];
  if (!Array.isArray(users) || !users.every(id => typeof id === 'string') || !Array.isArray(products) || !products.every(id => typeof id === 'string')) throw new Error('Invalid account audience');
  // Owner chose to include future company members in old product-wide grants.
  if (products.some(product => product.trim())) return 'all';
  const identities = new Map();
  for (const person of people) for (const key of ['id', 'username', 'email']) if (person[key]) identities.set(person[key].trim().toLowerCase(), person.id);
  const chosen = [];
  for (const id of users.map(id => id.trim()).filter(Boolean)) {
    const canonical = identities.get(id.toLowerCase()) || id;
    if (!chosen.some(id => id.toLowerCase() === canonical.toLowerCase())) chosen.push(canonical);
  }
  return chosen.length ? { users: chosen } : 'admins';
}

function migrateAccountAvailability({ workspaceDir, stateDir, env }) {
  const usersFile = path.join(workspaceDir, 'config/users.json');
  const people = fs.existsSync(usersFile) ? JSON.parse(fs.readFileSync(usersFile, 'utf8')).users || [] : [];
  const settingsFile = path.join(workspaceDir, 'config/provider-account-settings.json');
  const changes = [];
  if (fs.existsSync(settingsFile)) {
    const before = fs.readFileSync(settingsFile, 'utf8'), settings = JSON.parse(before);
    const next = { ...settings };
    if (settings.available_to) next.available_to = Object.fromEntries(Object.entries(settings.available_to).map(([key, value]) => [key, audience(value, people)]));
    if (JSON.stringify(settings) !== JSON.stringify(next)) changes.push({ file: settingsFile, name: 'provider-account-settings.json', before, after: JSON.stringify(next, null, 2) + '\n' });
  }
  let nextPolicy;
  if (env.AGENTWORKS_PROVIDER_POLICY?.trim()) {
    const policy = JSON.parse(env.AGENTWORKS_PROVIDER_POLICY);
    const next = Object.fromEntries(Object.entries(policy).map(([key, entry]) => [key, { ...entry, available_to: audience(entry.available_to, people) }]));
    if (JSON.stringify(policy) !== JSON.stringify(next)) {
      nextPolicy = JSON.stringify(next);
      changes.push({ name: 'inherited-policy.json', before: env.AGENTWORKS_PROVIDER_POLICY });
    }
  }
  // Validate everything first, then keep private backups before writing.
  if (changes.length) {
    const backup = path.join(stateDir, 'migration-backups', `account-audiences-${new Date().toISOString().replace(/[:.]/g, '-')}`);
    fs.mkdirSync(backup, { recursive: true, mode: 0o700 });
    for (const change of changes) fs.writeFileSync(path.join(backup, change.name), change.before, { mode: 0o600, flag: 'wx' });
    for (const change of changes) if (change.file) fs.writeFileSync(change.file, change.after);
    if (nextPolicy) env.AGENTWORKS_PROVIDER_POLICY = nextPolicy;
  }
}

module.exports = { migrateAccountAvailability };
