// Build-host real-binary check for PLAT-746-207. Nothing is deployed.
const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const { once } = require('node:events');
const fs = require('node:fs');
const path = require('node:path');
const { spawnWithBootstrap } = require('../../desktop/lib/connections');

(async () => {
  const [binary, scratch] = process.argv.slice(2);
  fs.writeFileSync(path.join(scratch, 'mcp.json'), '{"mcpServers":{}}');
  const bootstrap = { secret: 'process-proof-canary-837', host: '127.0.0.1:18743', auth_secret: 'process-auth-canary-614' };
  const child = spawnWithBootstrap(binary, ['server', '--port', '0', '--mcp-config', path.join(scratch, 'mcp.json')], {
    cwd: scratch,
    env: { PATH: process.env.PATH, HOME: scratch, TMPDIR: scratch,
      AUTH_SECRET: 'inherited-auth-canary', AGENTWORKS_DESKTOP_SECRET: 'inherited-proof-canary',
      MCP_ENV_LOADED: '1', CLI_UPDATE_ENABLED: 'false', CLI_UPDATE_DRY_RUN: 'true',
      AGENTWORKS_STATE_ROOT: path.join(scratch, 'state'), WORKSPACE_DOCS_PATH: path.join(scratch, 'docs'),
      WORKSPACE_API_URL: 'http://127.0.0.1:1', AGENTWORKS_LOCAL_VAULT: '0' },
  }, bootstrap);
  let output = '';
  const completed = once(child, 'exit');
  child.stdout.on('data', chunk => { output += chunk; });
  child.stderr.on('data', chunk => { output += chunk; });
  try {
    await Promise.race([
      new Promise((resolve, reject) => {
        const timer = setInterval(() => {
          if (/DynamicPort: \d+/.test(output)) { clearInterval(timer); resolve(); }
          else if (child.exitCode !== null) { clearInterval(timer); reject(new Error('server exited before readiness')); }
        }, 100);
        timer.unref();
      }),
      new Promise((_, reject) => { const timer = setTimeout(() => reject(new Error('server readiness timed out')), 60000); timer.unref(); }),
    ]);
    const listing = spawnSync('ps', ['eww', '-p', String(child.pid)], { encoding: 'utf8' });
    assert.equal(listing.status, 0);
    for (const value of ['AGENTWORKS_DESKTOP_', 'AUTH_SECRET=', bootstrap.secret, bootstrap.auth_secret, 'inherited-auth-canary', 'inherited-proof-canary']) {
      assert.equal(listing.stdout.includes(value), false, 'credential exposed through process list');
      assert.equal(output.includes(value), false, 'credential exposed in server output');
    }
    console.log('Real agent-server startup and ps credential check passed.');
  } finally {
    child.kill('SIGKILL'); await completed;
  }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
