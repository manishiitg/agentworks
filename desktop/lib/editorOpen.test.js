'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { normalizeEditorUrl, jetbrainsArgs, makeEditorOpener } = require('./editorOpen');
const { normalizeExternalUrl } = require('./externalNav');

// PLAT-746-1002: only dedicated main-window IPC may open a file in an editor.
test('editor file links never widen generic external navigation or allow commands', async () => {
  const urls = ['vscode://file/Users/example/project/a%20b.ts:9', 'cursor://file/Users/example/project', 'zed://file/Users/example/project/a%23b.ts:10'];
  for (const url of urls) {
    assert.equal(normalizeEditorUrl(url), url);
    assert.equal(normalizeExternalUrl(url), null);
  }
  const calls = [];
  const open = makeEditorOpener({ openExternal: async uri => calls.push(uri) }, () => { throw Error('unexpected process'); });
  for (const url of ['file:///etc/passwd', 'https://example.com', 'vscode://command/run', 'vscode://file/Users/example/a?windowId=1', 'vscode://user@file/Users/example/a', 'vscode://file/Users/example/a#fragment', 'vscode://file/%00', 'vscode://file/Users/example/%2e%2e/a', 'jetbrains://idea/installPlugin?pluginId=x']) {
    assert.equal(normalizeEditorUrl(url), null, url);
    assert.equal((await open({ url, requestId: 'fixture' })).ok, false);
  }
  assert.equal(calls.length, 0);
  assert.equal((await open({ url: urls[0], requestId: 'fixture' })).ok, true);
  assert.deepEqual(calls, [urls[0]]);
});

test('IDEA has a fixed executable and argument list, never a shell or a command URL', async () => {
  const target = { path: '/Users/example/project/$(command) file.ts', line: 42 };
  assert.deepEqual(jetbrainsArgs(target), ['-na', 'IntelliJ IDEA.app', '--args', '--line', '42', target.path]);
  for (const path of ['--help', '/a/../secret', '/a\nb']) assert.equal(jetbrainsArgs({ path, line: 0 }), null);
  assert.equal(jetbrainsArgs({ path: '/a', line: '3' }), null);
  const calls = [];
  const open = makeEditorOpener({ openExternal: async () => { throw Error('unexpected URL'); } }, (bin, args, options, done) => { calls.push({ bin, args, options }); done(null); });
  assert.equal((await open({ jetbrains: target, requestId: 'fixture' })).ok, true);
  assert.deepEqual(calls, [{ bin: '/usr/bin/open', args: jetbrainsArgs(target), options: { timeout: 10000 } }]);
  const failed = makeEditorOpener({ openExternal: async () => { throw Error('private details'); } });
  assert.deepEqual(await failed({ url: 'zed://file/Users/example/a', requestId: 'fixture' }), { ok: false, error: 'The editor could not open. Install it or choose another editor.' });
});
