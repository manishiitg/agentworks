'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');

const afterPack = require('../scripts/afterPack');
const { pruneElectronLocales } = afterPack;

test('afterPack strips non-English Electron .lproj directories on macOS only', async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'aw-afterpack-'));
  try {
    const appPath = path.join(root, 'AgentWorks.app');
    const resourcesDir = path.join(
      appPath,
      'Contents',
      'Frameworks',
      'Electron Framework.framework',
      'Versions',
      'A',
      'Resources',
    );
    for (const locale of ['en.lproj', 'Base.lproj', 'fr.lproj', 'de.lproj', 'ja.lproj']) {
      fs.mkdirSync(path.join(resourcesDir, locale), { recursive: true });
      fs.writeFileSync(path.join(resourcesDir, locale, 'locale.pak'), 'data');
    }
    fs.writeFileSync(path.join(resourcesDir, 'icudtl.dat'), 'icu');

    // Non-darwin platforms are untouched.
    await afterPack({ electronPlatformName: 'win32', appOutDir: root, packager: { appInfo: { productFilename: 'AgentWorks' } } });
    assert.equal(fs.existsSync(path.join(resourcesDir, 'fr.lproj')), true);

    // Darwin prunes unused locales while keeping en.lproj, Base.lproj, and other resources.
    await afterPack({ electronPlatformName: 'darwin', appOutDir: root, packager: { appInfo: { productFilename: 'AgentWorks' } } });
    assert.equal(fs.existsSync(path.join(resourcesDir, 'en.lproj')), true);
    assert.equal(fs.existsSync(path.join(resourcesDir, 'Base.lproj')), true);
    assert.equal(fs.existsSync(path.join(resourcesDir, 'icudtl.dat')), true);
    assert.equal(fs.existsSync(path.join(resourcesDir, 'fr.lproj')), false);
    assert.equal(fs.existsSync(path.join(resourcesDir, 'de.lproj')), false);
    assert.equal(fs.existsSync(path.join(resourcesDir, 'ja.lproj')), false);

    // Missing directory is a safe no-op.
    assert.deepEqual(pruneElectronLocales(path.join(root, 'Missing.app')), { removed: 0 });
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test('desktop package.json enables maximum compression, ULFO DMG, afterPack, and minimal icon resource', () => {
  const pkg = require('../package.json');
  assert.equal(pkg.build.compression, 'maximum');
  assert.equal(pkg.build.afterPack, 'scripts/afterPack.js');
  assert.equal(pkg.build.dmg.format, 'ULFO');
  const iconRes = pkg.build.extraResources.find((r) => r.to === 'icons/icon.png');
  assert.deepEqual(iconRes, { from: 'resources/icons/icon.png', to: 'icons/icon.png' });
});
