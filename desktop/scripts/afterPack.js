'use strict';

const fs = require('fs');
const path = require('path');

const KEEP_LOCALES = new Set(['en.lproj', 'Base.lproj']);

// Electron ships ~55 Chromium .lproj locale folders (~40 MB uncompressed) inside
// Electron Framework.framework. AgentWorks UI is English-only, so pruning unused
// locales cuts ~12–15 MB from the Mac DMG with no runtime impact.
function pruneElectronLocales(appPath) {
  const resourcesDir = path.join(
    appPath,
    'Contents',
    'Frameworks',
    'Electron Framework.framework',
    'Versions',
    'A',
    'Resources',
  );
  if (!fs.existsSync(resourcesDir)) return { removed: 0 };

  let removed = 0;
  for (const entry of fs.readdirSync(resourcesDir, { withFileTypes: true })) {
    if (!entry.isDirectory() || !entry.name.endsWith('.lproj')) continue;
    if (KEEP_LOCALES.has(entry.name)) continue;
    fs.rmSync(path.join(resourcesDir, entry.name), { recursive: true, force: true });
    removed += 1;
  }
  return { removed };
}

async function afterPack(context) {
  if (!context || context.electronPlatformName !== 'darwin') return;
  const productFilename = context.packager?.appInfo?.productFilename || 'AgentWorks';
  const appPath = path.join(context.appOutDir, `${productFilename}.app`);
  const { removed } = pruneElectronLocales(appPath);
  if (removed > 0) {
    console.log(`[afterPack] Pruned ${removed} unused Electron locale directories from ${appPath}`);
  }
}

module.exports = afterPack;
module.exports.pruneElectronLocales = pruneElectronLocales;
