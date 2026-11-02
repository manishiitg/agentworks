'use strict';
const fs = require('fs');
const path = require('path');

// Choose before spawning: dyld loads linked speech libraries before main().
// Both binaries implement the same server; only the compatible one runs.
function selectPackagedAgent(resources, systemVersion) {
  const basic = path.join(resources, 'agent-server');
  const voice = path.join(resources, 'voice', 'agent-server');
  const minimum = JSON.parse(fs.readFileSync(path.join(resources, 'voice', 'minimum-macos.json'), 'utf8')).minimumSystemVersion;
  const parse = value => /^\d+\.\d+(\.\d+)?$/.test(value) ? value.split('.').map(Number) : null;
  const current = parse(systemVersion), required = parse(minimum);
  if (!current || !required) throw new Error('Could not read the voice runtime macOS requirement');
  for (let i = 0; i < 3; i++) {
    if ((current[i] || 0) < (required[i] || 0)) return { binary: basic, voiceUnavailableReason: `Voice needs macOS ${minimum} or later on this Mac.` };
    if ((current[i] || 0) > (required[i] || 0)) return { binary: voice, voiceUnavailableReason: '' };
  }
  return { binary: voice, voiceUnavailableReason: '' };
}
module.exports = { selectPackagedAgent };
