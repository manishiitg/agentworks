const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { selectPackagedAgent } = require('./voiceRuntime');

// PLAT-746-303: always launch the single novoice agent-server binary and enable
// the standalone voice-helper only when the Mac meets its macOS minimum.
test('packaged server selects standalone voice-helper only at or above its macOS requirement', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'agentworks-voice-os-'));
  try {
    fs.mkdirSync(path.join(root, 'voice'));
    fs.writeFileSync(path.join(root, 'voice', 'minimum-macos.json'), JSON.stringify({ minimumSystemVersion: '15.5.0' }));

    const expectedAgent = path.join(root, 'agent-server');
    const expectedHelper = path.join(root, 'voice', 'voice-helper');

    const old = selectPackagedAgent(root, '13.7.8');
    assert.equal(old.binary, expectedAgent);
    assert.equal(old.voiceHelperBinary, '');
    assert.match(old.voiceUnavailableReason, /15\.5\.0/);

    for (const ver of ['15.5', '26', '26.4']) {
      const ok = selectPackagedAgent(root, ver);
      assert.equal(ok.binary, expectedAgent);
      assert.equal(ok.voiceHelperBinary, expectedHelper);
      assert.equal(ok.voiceUnavailableReason, '');
    }

    const beta = selectPackagedAgent(root, '26 beta');
    assert.equal(beta.binary, expectedAgent);
    assert.equal(beta.voiceHelperBinary, '');

    fs.writeFileSync(path.join(root, 'voice', 'minimum-macos.json'), '{broken');
    assert.equal(selectPackagedAgent(root, '26').voiceHelperBinary, '');

    fs.unlinkSync(path.join(root, 'voice', 'minimum-macos.json'));
    assert.equal(selectPackagedAgent(root, '26').voiceHelperBinary, '');
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
