const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { prepareDesktopUpdate } = require('./updateInstaller');

test('desktop update passes literal paths and an explicit update to the bundled installer', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'agentworks-update-test-'));
  let staged;
  try {
    fs.writeFileSync(path.join(root, 'install.sh'), 'fixture');
    const dmg = path.join(root, "cache $(touch bad) ' quote.dmg");
    const spec = prepareDesktopUpdate(root, '/Applications/AgentWorks.app/Contents/MacOS/AgentWorks', '1.2.3', dmg);
    staged = path.dirname(spec.args[3]);
    assert.equal(spec.args[spec.args.indexOf('--dmg') + 1], dmg);
    assert.equal(spec.args[spec.args.indexOf('--app-dir') + 1], '/Applications');
    assert.ok(spec.args.includes('--update'));
    assert.equal(fs.readFileSync(spec.args[3], 'utf8'), 'fixture');
    assert.throws(() => prepareDesktopUpdate(root, '', '1; touch bad', dmg));
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
    if (staged) fs.rmSync(staged, { recursive: true, force: true });
  }
});
