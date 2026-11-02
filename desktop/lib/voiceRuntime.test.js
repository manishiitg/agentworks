const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { selectPackagedAgent } = require('./voiceRuntime');

// PLAT-746-303: linking voice into the only server prevented older Macs booting.
test('packaged server avoids voice dylibs below their macOS requirement', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'agentworks-voice-os-'));
  try {
    fs.mkdirSync(path.join(root, 'voice'));
    fs.writeFileSync(path.join(root, 'voice', 'minimum-macos.json'), JSON.stringify({ minimumSystemVersion: '15.5.0' }));
    const old = selectPackagedAgent(root, '13.7.8');
    assert.equal(old.binary, path.join(root, 'agent-server'));
    assert.match(old.voiceUnavailableReason, /15\.5\.0/);
    assert.equal(selectPackagedAgent(root, '15.5').binary, path.join(root, 'voice', 'agent-server'));
    assert.equal(selectPackagedAgent(root, '26.4').binary, path.join(root, 'voice', 'agent-server'));
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});
