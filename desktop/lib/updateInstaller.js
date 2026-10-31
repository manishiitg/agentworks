'use strict';
const fs = require('fs');
const os = require('os');
const path = require('path');

// Use this release's installer and literal argv. Do not fetch executable code
// from main or interpolate app/cache paths into shell code.
function prepareDesktopUpdate(resources, executable, version, dmg) {
  if (!/^\d+\.\d+\.\d+$/.test(version)) throw new Error('Invalid update version');
  const stage = fs.mkdtempSync(path.join(os.tmpdir(), 'agentworks-update-'));
  const installer = path.join(stage, 'install.sh');
  fs.copyFileSync(path.join(resources, 'install.sh'), installer);
  return {
    command: '/bin/bash',
    args: ['-c', 'sleep 2; exec /bin/bash "$@"', 'agentworks-update', installer, '--update', '--version', `v${version}`, '--dmg', dmg, '--app-dir', path.resolve(path.dirname(executable), '../../..')],
  };
}
module.exports = { prepareDesktopUpdate };
