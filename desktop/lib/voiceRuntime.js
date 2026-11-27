'use strict';
const fs = require('fs');
const path = require('path');

// Always run the single novoice agent-server binary so older macOS versions
// never fail in dyld before main(). When the host macOS meets the speech
// libraries' minimumSystemVersion, pass the standalone voice-helper binary so
// agent-server spawns it lazily on demand.
function selectPackagedAgent(resources, systemVersion) {
  const binary = path.join(resources, 'agent-server');
  const helper = path.join(resources, 'voice', 'voice-helper');
  try {
    const minimum = JSON.parse(fs.readFileSync(path.join(resources, 'voice', 'minimum-macos.json'), 'utf8')).minimumSystemVersion;
    const parse = value => /^\d+(\.\d+){0,2}$/.test(value) ? value.split('.').map(Number) : null;
    const current = parse(systemVersion), required = parse(minimum);
    if (!current || !required) throw new Error('Could not read the voice runtime macOS requirement');
    for (let i = 0; i < 3; i++) {
      if ((current[i] || 0) < (required[i] || 0)) {
        return { binary, voiceHelperBinary: '', voiceUnavailableReason: `Voice needs macOS ${minimum} or later on this Mac.` };
      }
      if ((current[i] || 0) > (required[i] || 0)) {
        return { binary, voiceHelperBinary: helper, voiceUnavailableReason: '' };
      }
    }
    return { binary, voiceHelperBinary: helper, voiceUnavailableReason: '' };
  } catch {
    console.warn('[VOICE] Could not determine voice compatibility; disabling standalone voice helper');
    return { binary, voiceHelperBinary: '', voiceUnavailableReason: 'Voice is unavailable on this Mac' };
  }
}

module.exports = { selectPackagedAgent };
