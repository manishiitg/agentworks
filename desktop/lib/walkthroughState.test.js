const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const vm = require('node:vm');
const { createWalkthroughState } = require('../walkthroughState');

const key = 'agentworks_code_walkthrough_v1_dismissed';

test('desktop dismissals survive restarts, independent of renderer port', t => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'agentworks-tour-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const profile = path.join(directory, 'profile');
  createWalkthroughState(profile).dismiss(key);
  assert.deepEqual(createWalkthroughState(profile).getDismissed(), [key]);
  assert.deepEqual(createWalkthroughState(path.join(directory, 'other-profile')).getDismissed(), []);
  createWalkthroughState(profile).dismiss('../../config.json');
  assert.deepEqual(createWalkthroughState(profile).getDismissed(), [key]);
});

test('an unreadable dismissal file does not prevent startup', t => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'agentworks-tour-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  fs.writeFileSync(path.join(directory, 'walkthrough-dismissals.json'), '{broken');
  const state = createWalkthroughState(directory);
  assert.deepEqual(state.getDismissed(), []);
  state.dismiss(key);
  assert.deepEqual(createWalkthroughState(directory).getDismissed(), [key]);
});

test('preload restores dismissals before rendering and forwards new choices', () => {
  let bridge;
  const sent = [];
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '..', 'preload.js'), 'utf8'), {
    require: () => ({
      contextBridge: { exposeInMainWorld: (_name, value) => { bridge = value; } },
      ipcRenderer: {
        sendSync: channel => { assert.equal(channel, 'get-walkthrough-dismissals'); return [key]; },
        send: (...args) => sent.push(args),
      },
    }),
  });
  assert.equal(bridge.isWalkthroughDismissed(key), true);
  const crew = 'agentworks_crew_walkthrough_v3_dismissed';
  bridge.dismissWalkthrough(crew);
  assert.equal(bridge.isWalkthroughDismissed(crew), true);
  assert.deepEqual(sent, [['dismiss-walkthrough', crew]]);
  const packaging = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'package.json'), 'utf8'));
  assert.ok(packaging.build.files.includes('walkthroughState.js'));
});
