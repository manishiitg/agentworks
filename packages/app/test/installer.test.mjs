import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, readlinkSync, realpathSync, existsSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';

const repo = fileURLToPath(new URL('../../../', import.meta.url));
function run(command, args, options = {}) {
  return spawnSync(command, args, { encoding: 'utf8', timeout: 90_000, ...options });
}
test('PLAT-856-434: verified Mac install preserves standalone CLI and requires explicit update', { skip: process.platform !== 'darwin' }, () => {
  const root = mkdtempSync(join(tmpdir(), 'agentworks-installer-test-'));
  try {
    const source = join(root, 'source/AgentWorks.app');
    const resources = join(source, 'Contents/Resources');
    mkdirSync(join(resources, 'bin'), { recursive: true });
    writeFileSync(join(resources, 'app.asar'), 'fixture');
    for (const file of ['agent-server', 'workspace-server', 'agentworks-vault', 'bin/agentworks', 'bin/mcpbridge', 'bin/tmux']) {
      writeFileSync(join(resources, file), '#!/bin/sh\necho fixture\n', { mode: 0o755 });
    }
    const dmg = join(root, 'app with spaces.dmg');
    const made = run('hdiutil', ['create', '-quiet', '-srcfolder', join(root, 'source'), '-format', 'UDZO', dmg]);
    assert.equal(made.status, 0, made.stderr);
    const sum = join(root, 'app.sha256');
    writeFileSync(sum, createHash('sha256').update(readFileSync(dmg)).digest('hex') + '  app.dmg\n');
    const bin = join(root, 'bin'); mkdirSync(bin);
    const standalone = join(bin, 'agentworks'); writeFileSync(standalone, 'preserve existing CLI');
    const appDir = join(root, 'Apps');
    const args = [join(repo, 'install.sh'), '--app-dir', appDir, '--bin-dir', bin, '--dmg', dmg, '--sha256-file', sum, '--no-open'];
    const installed = run('/bin/bash', args);
    assert.equal(installed.status, 0, installed.stdout + installed.stderr);
    assert.equal(readFileSync(standalone, 'utf8'), 'preserve existing CLI');
    const installedHelper = join(appDir, 'AgentWorks.app/Contents/Resources/bin/mcpbridge');
    writeFileSync(installedHelper, 'preserve on second run');
    rmSync(standalone);
    const reopened = run('/bin/bash', args, { env: { ...process.env, RUNLOOP_VERSION: 'v1.2.3', RUNLOOP_DMG_PATH: '' } });
    assert.equal(reopened.status, 0, reopened.stderr);
    assert.equal(readFileSync(installedHelper, 'utf8'), 'preserve on second run');
    assert.equal(readlinkSync(standalone), realpathSync(join(appDir, 'AgentWorks.app/Contents/Resources/bin/agentworks')));
    writeFileSync(sum, '0'.repeat(64));
    const refused = run('/bin/bash', [...args, '--update']);
    assert.notEqual(refused.status, 0);
    assert.match(refused.stderr, /Checksum mismatch/);
    assert.equal(readFileSync(installedHelper, 'utf8'), 'preserve on second run');
    writeFileSync(sum, createHash('sha256').update(readFileSync(dmg)).digest('hex'));
    const failingTools = join(root, 'failing-tools'); mkdirSync(failingTools);
    writeFileSync(join(failingTools, 'ditto'), '#!/bin/sh\nexit 1\n', { mode: 0o755 });
    const failedCopy = run('/bin/bash', [...args, '--update'], { env: { ...process.env, PATH: failingTools + ':' + process.env.PATH } });
    assert.notEqual(failedCopy.status, 0);
    assert.match(failedCopy.stderr, /existing app is unchanged/);
    assert.equal(readFileSync(installedHelper, 'utf8'), 'preserve on second run');
    const updated = run('/bin/bash', [...args, '--update']);
    assert.equal(updated.status, 0, updated.stdout + updated.stderr);
    assert.match(readFileSync(installedHelper, 'utf8'), /fixture/);
    // PLAT-856-435: Restart & Install passes the selected release via env.
    // Its app has no bundled CLI, so opening it instead cannot finish an update.
    rmSync(join(appDir, 'AgentWorks.app/Contents/Resources/bin/agentworks'));
    writeFileSync(installedHelper, 'old app');
    const handoffTools = join(root, 'handoff-tools'); mkdirSync(handoffTools);
    // The old process is visible once, then finishes quitting. Never kill it.
    const quitMarker = join(root, 'quit-observed');
    writeFileSync(join(handoffTools, 'ps'), '#!/bin/sh\nif [ ! -f "$QUIT_MARKER" ]; then touch "$QUIT_MARKER"; printf "%s/Contents/MacOS/AgentWorks\\n" "$TEST_APP"; fi\n', { mode: 0o755 });
    const handedOff = run('/bin/bash', [join(repo, 'install.sh'), '--app-dir', appDir, '--bin-dir', bin, '--sha256-file', sum, '--no-open'], {
      env: { ...process.env, RUNLOOP_VERSION: 'v1.2.3', RUNLOOP_DMG_PATH: dmg, PATH: handoffTools + ':' + process.env.PATH, QUIT_MARKER: quitMarker, TEST_APP: join(appDir, 'AgentWorks.app') },
    });
    assert.equal(handedOff.status, 0, handedOff.stdout + handedOff.stderr);
    assert.equal(existsSync(quitMarker), true);
    assert.match(readFileSync(installedHelper, 'utf8'), /fixture/);
    const checkout = join(root, 'checkout'); mkdirSync(join(checkout, '.git'), { recursive: true });
    const devRefused = run('/bin/bash', [join(repo, 'install.sh'), '--app-dir', checkout, '--bin-dir', bin, '--no-open']);
    assert.notEqual(devRefused.status, 0);
    assert.match(devRefused.stderr, /outside your development checkout/);
    assert.equal(existsSync(join(checkout, 'AgentWorks.app')), false);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('development launcher forwards to the existing checkout without pulling or installing', () => {
  const root = mkdtempSync(join(tmpdir(), 'agentworks-dev-test-'));
  try {
    mkdirSync(join(root, 'agent_go'));
    writeFileSync(join(root, 'agent_go/run_server_with_logging.sh'), '');
    writeFileSync(join(root, 'run_agentworks'), '#!/bin/sh\nprintf "%s\\n" "$AGENTWORKS_SKIP_AUTO_PULL" "$@"\n');
    const result = run(process.execPath, [join(repo, 'packages/app/bin/app.mjs'), 'dev', '--repo', root, '--only-frontend', '--help']);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout, '1\n--only-frontend\n--help\n');
  } finally { rmSync(root, { recursive: true, force: true }); }
});

// PLAT-746-303: download selection must follow hardware, including Rosetta.
test('Mac installer selects separate DMG and checksum for Intel and Apple silicon', () => {
  const root = mkdtempSync(join(tmpdir(), 'agentworks-arch-test-'));
  try {
    const tools = join(root, 'tools'); mkdirSync(tools);
    writeFileSync(join(tools, 'uname'), '#!/bin/sh\nif [ "$1" = -s ]; then echo Darwin; else echo "$TEST_CPU"; fi\n', { mode: 0o755 });
    writeFileSync(join(tools, 'sysctl'), '#!/bin/sh\necho "$TEST_TRANSLATED"\n', { mode: 0o755 });
    writeFileSync(join(tools, 'curl'), '#!/bin/sh\nprintf "%s\\n" "$@" >> "$TEST_REQUESTS"\nwhile [ "$#" -gt 0 ]; do if [ "$1" = -o ]; then shift; echo fixture > "$1"; fi; shift; done\n', { mode: 0o755 });
    for (const [cpu, translated, arch] of [['arm64', '0', 'arm64'], ['x86_64', '0', 'x64'], ['x86_64', '1', 'arm64']]) {
      const requests = join(root, `${cpu}-${translated}.txt`);
      const result = run('/bin/bash', [join(repo, 'install.sh'), '--version', 'v1.2.3', '--app-dir', join(root, 'Apps'), '--bin-dir', join(root, 'bin'), '--no-open'], {
        env: { ...process.env, PATH: tools + ':' + process.env.PATH, TEST_CPU: cpu, TEST_TRANSLATED: translated, TEST_REQUESTS: requests },
      });
      assert.notEqual(result.status, 0); // Deliberately invalid checksum: never installs.
      assert.match(result.stderr, /Invalid SHA-256/);
      const urls = readFileSync(requests, 'utf8');
      assert.ok(urls.includes(`/AgentWorks-1.2.3-${arch}.dmg\n`));
      assert.ok(urls.includes(`/AgentWorks-1.2.3-${arch}.dmg.sha256\n`));
    }
  } finally { rmSync(root, { recursive: true, force: true }); }
});
