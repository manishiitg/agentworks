'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');

const { BEGIN, END, resolveLoginEnvAsync } = require('./loginEnv');

test('resolveLoginEnvAsync reads login env without blocking the event loop', async () => {
  if (process.platform !== 'darwin' && process.platform !== 'linux') return;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'awenv-'));
  try {
    const fakeShell = path.join(dir, 'fake-shell.sh');
    fs.writeFileSync(fakeShell, `#!/bin/sh
printf 'banner line\\n'
printf '%s' '${BEGIN}'
printf 'PATH=/opt/test/bin:/usr/bin\\0TEST_VAR=hello\\0'
printf '%s' '${END}'
`, { mode: 0o755 });
    const env = await resolveLoginEnvAsync({ shell: fakeShell, timeoutMs: 5000 });
    assert.equal(env.PATH, '/opt/test/bin:/usr/bin');
    assert.equal(env.TEST_VAR, 'hello');
    const missing = await resolveLoginEnvAsync({ shell: path.join(dir, 'missing-shell'), timeoutMs: 1000 });
    assert.deepEqual(missing, {});
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
