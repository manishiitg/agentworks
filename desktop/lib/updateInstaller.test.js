const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { prepareDesktopUpdate, desktopUpdateAsset, pickDesktopUpdate } = require('./updateInstaller');

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

// PLAT-746-303: the updater previously sent every Mac the ARM-only app.
test('Intel and Apple silicon updates select their own DMG', () => {
  assert.equal(desktopUpdateAsset('1.2.3', 'x64'), 'AgentWorks-1.2.3-x64.dmg');
  assert.equal(desktopUpdateAsset('1.2.3', 'arm64'), 'AgentWorks-1.2.3-arm64.dmg');
  assert.throws(() => desktopUpdateAsset('1.2.3', 'ia32'));
});

// Nightly builds reach only apps with "Get Nightly Builds" ticked, and a
// release of the same version replaces a nightly once it ships.
test('nightly builds are offered only when chosen, and releases win over nightlies', () => {
  const releases = [
    { tag_name: 'nightly-1.25.170-nightly.202611281200', prerelease: true },
    { tag_name: 'nightly-1.25.170-nightly.202611271200', prerelease: true },
    { tag_name: 'v1.25.169' },
    { tag_name: 'v1.25.171', draft: true },
    { tag_name: 'other-app-v9.0.0' },
  ];
  assert.equal(pickDesktopUpdate(releases, '1.25.168', false).version, '1.25.169');
  assert.equal(pickDesktopUpdate(releases, '1.25.168', true).version, '1.25.170-nightly.202611281200');
  assert.equal(pickDesktopUpdate(releases, '1.25.170-nightly.202611281200', true), null);
  // Unticking on a nightly waits for the next release; never a downgrade.
  assert.equal(pickDesktopUpdate(releases, '1.25.170-nightly.202611271200', false), null);
  assert.equal(pickDesktopUpdate([...releases, { tag_name: 'v1.25.170' }], '1.25.170-nightly.202611281200', false).version, '1.25.170');
});
