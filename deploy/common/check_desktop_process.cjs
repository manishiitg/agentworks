// Build-host real-binary check for PLAT-746-207. Nothing is deployed.
const assert = require('node:assert/strict');
const { spawn, spawnSync } = require('node:child_process');
const { once } = require('node:events');
const fs = require('node:fs');
const path = require('node:path');
const { spawnWithBootstrap } = require('../../desktop/lib/connections');

let workspaceProcess;
(async () => {
  const [binary, scratch, workspaceBinary] = process.argv.slice(2);
  const net = require('node:net');
  const workspaceProbe = net.createServer(); workspaceProbe.listen(0, '127.0.0.1'); await once(workspaceProbe, 'listening');
  const workspacePort = workspaceProbe.address().port; await new Promise(resolve => workspaceProbe.close(resolve));
  const workspaceURL = `http://127.0.0.1:${workspacePort}`;
  const workspaceToken = 'fixture-workspace-token';
  const docs = path.join(scratch, 'docs'); fs.mkdirSync(docs, { recursive: true });
  workspaceProcess = spawn(workspaceBinary, ['server', '--host', '127.0.0.1', '--port', String(workspacePort), '--docs-dir', docs], {
    cwd: scratch, env: { PATH: process.env.PATH, HOME: scratch, TMPDIR: scratch, DOCS_DIR: docs,
      AGENTWORKS_STATE_ROOT: path.join(scratch, 'workspace-state'), WORKSPACE_API_TOKEN: workspaceToken },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let workspaceOutput = '';
  workspaceProcess.stdout.on('data', data => { workspaceOutput = (workspaceOutput + data).slice(-4000); });
  workspaceProcess.stderr.on('data', data => { workspaceOutput = (workspaceOutput + data).slice(-4000); });
  await require('../../desktop/lib/health').waitForHealth({ checks: [{ name: 'fixture workspace', url: workspaceURL + '/health' }],
    isAlive: () => workspaceProcess.exitCode !== null ? 'Workspace stopped: ' + workspaceOutput : null });
  fs.writeFileSync(path.join(scratch, 'mcp.json'), '{"mcpServers":{}}');
  const bootstrap = { secret: 'process-proof-canary-837', host: '127.0.0.1:18743', auth_secret: 'process-auth-canary-614' };
  const child = spawnWithBootstrap(binary, ['server', '--port', '0', '--mcp-config', path.join(scratch, 'mcp.json')], {
    cwd: scratch,
    env: { PATH: process.env.PATH, HOME: scratch, TMPDIR: scratch,
      AUTH_SECRET: 'inherited-auth-canary', AGENTWORKS_DESKTOP_SECRET: 'inherited-proof-canary',
      MCP_ENV_LOADED: '1', CLI_UPDATE_ENABLED: 'false', CLI_UPDATE_DRY_RUN: 'true',
      AGENTWORKS_STATE_ROOT: path.join(scratch, 'state'), WORKSPACE_DOCS_PATH: path.join(scratch, 'docs'),
      WORKSPACE_API_URL: workspaceURL, WORKSPACE_API_TOKEN: workspaceToken, AGENTWORKS_LOCAL_VAULT: '0' },
  }, bootstrap);
  let output = '';
  const completed = once(child, 'exit');
  child.stdout.on('data', chunk => { output += chunk; });
  child.stderr.on('data', chunk => { output += chunk; });
  let readinessTimer;
  try {
    await new Promise((resolve, reject) => {
      const deadline = Date.now() + 60000;
      readinessTimer = setInterval(() => {
        if (/DynamicPort: \d+/.test(output)) { clearInterval(readinessTimer); resolve(); }
        else if (child.exitCode !== null || child.signalCode !== null || Date.now() > deadline) {
          clearInterval(readinessTimer);
          // Fixture-only output; strip even the synthetic credentials before diagnosis.
          let detail = output.slice(-8000);
          for (const value of Object.values(bootstrap)) detail = detail.replaceAll(value, '[fixture]');
          reject(new Error('server did not become ready: ' + detail));
        }
      }, 100);
    });
    const listing = spawnSync('ps', ['eww', '-p', String(child.pid)], { encoding: 'utf8' });
    assert.equal(listing.status, 0);
    for (const value of ['AGENTWORKS_DESKTOP_', 'AUTH_SECRET=', bootstrap.secret, bootstrap.auth_secret, 'inherited-auth-canary', 'inherited-proof-canary']) {
      assert.equal(listing.stdout.includes(value), false, 'credential exposed through process list');
      assert.equal(output.includes(value), false, 'credential exposed in server output');
    }
    console.log('Real agent-server startup and ps credential check passed.');
  } finally {
    clearInterval(readinessTimer);
    child.kill('SIGKILL'); await completed;
  }
  // Repeat through the development launcher, using a different Vite origin.
  const probe = net.createServer(); probe.listen(0, '127.0.0.1'); await once(probe, 'listening');
  const port = probe.address().port; await new Promise(resolve => probe.close(resolve));
  fs.mkdirSync(path.join(scratch, 'configs'), { recursive: true });
  fs.copyFileSync(path.join(scratch, 'mcp.json'), path.join(scratch, 'configs/mcp_servers_clean.json'));
  const { startDevelopmentAgent } = require('../../desktop/lib/devAgent');
  const apiOrigin = `http://127.0.0.1:${port}`, appOrigin = 'http://127.0.0.1:51733';
  const managed = await startDevelopmentAgent({
    env: { PATH: process.env.PATH, HOME: scratch, TMPDIR: scratch,
      AGENTWORKS_DEV_AGENT_BINARY: binary, AGENTWORKS_DEV_AGENT_CWD: scratch,
      MCP_AGENT_SERVER_URL: apiOrigin, CLI_UPDATE_ENABLED: 'false', CLI_UPDATE_DRY_RUN: 'true',
      AGENTWORKS_STATE_ROOT: path.join(scratch, 'state'), WORKSPACE_DOCS_PATH: path.join(scratch, 'docs'),
      WORKSPACE_API_URL: workspaceURL, WORKSPACE_API_TOKEN: workspaceToken, AGENTWORKS_LOCAL_VAULT: '0' },
    secret: bootstrap.secret, authSecret: bootstrap.auth_secret, appOrigin, log: { write() {} },
  });
  try {
    const login = await fetch(apiOrigin + '/api/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
    assert.equal(login.status, 200, 'local sign-in failed');
    const { token } = await login.json();
    assert.ok(token, 'local sign-in did not return a token');
    const headers = { Authorization: `Bearer ${token}`, Origin: appOrigin, 'Sec-Fetch-Site': 'same-site', 'X-AgentWorks-Desktop': bootstrap.secret };
    const response = await fetch(apiOrigin + '/api/desktop-connections', { headers });
    assert.equal(response.status, 200, 'Vite cannot list desktop connections');
    assert.equal(response.headers.get('access-control-allow-origin'), appOrigin);
    assert.deepEqual((await response.json()).connections, []);
    for (const overrides of [{ Origin: 'http://127.0.0.1:51734' }, { 'X-AgentWorks-Desktop': '' }]) {
      const denied = await fetch(apiOrigin + '/api/desktop-connections', { headers: { ...headers, ...overrides } });
      assert.equal(denied.status, 403, 'untrusted caller reached desktop connections');
    }
    const preflight = await fetch(apiOrigin + '/api/desktop-connections', { method: 'OPTIONS', headers: { Origin: appOrigin, 'Access-Control-Request-Method': 'GET', 'Access-Control-Request-Headers': 'authorization,x-agentworks-desktop' } });
    assert.equal(preflight.status, 200);
    assert.match(preflight.headers.get('access-control-allow-headers'), /x-agentworks-desktop/i);
    const listing = spawnSync('ps', ['eww', '-p', String(managed.child.pid)], { encoding: 'utf8' });
    assert.equal(listing.status, 0);
    assert.equal(listing.stdout.includes(bootstrap.secret) || listing.stdout.includes(bootstrap.auth_secret), false, 'development bootstrap leaked through ps');
    console.log('Development backend sign-in, connection gate, CORS and ps checks passed.');
  } finally { const exit = once(managed.child, 'exit'); managed.child.kill('SIGKILL'); await exit; }
  // PLAT-746-219: backend is a separate process; the same frontend-side helper
  // reconnects after a crash, including recovery of the stale socket file.
  const { readDevelopmentProof } = require('../../desktop/lib/devConnection');
  const socketPath = path.join(scratch, 'private-desktop', 'backend.sock');
  let previousProof;
  for (let attempt = 0; attempt < 2; attempt++) {
    const connectionOrigin = attempt ? `http://localhost:${port}` : apiOrigin;
    const separate = spawn(binary, ['server', '--host', '127.0.0.1', '--port', String(port), '--mcp-config', path.join(scratch, 'mcp.json')], {
      cwd: scratch, env: { PATH: process.env.PATH, HOME: scratch, TMPDIR: scratch,
        AUTH_SECRET: bootstrap.auth_secret, AGENTWORKS_DESKTOP_SOCKET: socketPath, MCP_AGENT_SERVER_URL: connectionOrigin,
        MCP_ENV_LOADED: '1', CLI_UPDATE_ENABLED: 'false', CLI_UPDATE_DRY_RUN: 'true',
        AGENTWORKS_STATE_ROOT: path.join(scratch, 'state'), WORKSPACE_DOCS_PATH: docs,
        WORKSPACE_API_URL: workspaceURL, WORKSPACE_API_TOKEN: workspaceToken, AGENTWORKS_LOCAL_VAULT: '0' },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let separateOutput = '';
    separate.stdout.on('data', value => { separateOutput = (separateOutput + value).slice(-12000); });
    separate.stderr.on('data', value => { separateOutput = (separateOutput + value).slice(-12000); });
    try {
      await require('../../desktop/lib/health').waitForHealth({ checks: [{ name: 'separate backend', url: apiOrigin + '/api/health' }],
        isAlive: () => separate.exitCode !== null || separate.signalCode !== null ? 'Separate backend stopped.' : null });
      assert.equal(fs.statSync(path.dirname(socketPath)).mode & 0o777, 0o700);
      assert.equal(fs.statSync(socketPath).mode & 0o777, 0o600);
      const proof = await readDevelopmentProof(socketPath, connectionOrigin, appOrigin);
      const { token } = await (await fetch(connectionOrigin + '/api/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })).json();
      assert.ok(token);
      const headers = { Authorization: `Bearer ${token}`, Origin: appOrigin, 'Sec-Fetch-Site': 'same-site', 'X-AgentWorks-Desktop': proof };
      const response = await fetch(connectionOrigin + '/api/desktop-connections', { headers });
      assert.equal(response.status, 200, 'separately started frontend cannot list connections');
      assert.deepEqual((await response.json()).connections, []);
      for (const overrides of [{ 'X-AgentWorks-Desktop': '' }, { 'X-AgentWorks-Desktop': 'wrong' }, { Origin: 'http://127.0.0.1:51734' }, { Origin: 'https://example.com' }]) {
        assert.equal((await fetch(connectionOrigin + '/api/desktop-connections', { headers: { ...headers, ...overrides } })).status, 403);
      }
      for (const invalid of ['https://example.com', 'http://127.0.0.1', 'http://127.0.0.1:51733/path', 'http://user@localhost:51733']) {
        await assert.rejects(readDevelopmentProof(socketPath, connectionOrigin, invalid));
      }
      await assert.rejects(readDevelopmentProof(socketPath, 'http://127.0.0.1:1', appOrigin));
      const otherOrigin = 'http://localhost:51734';
      const otherProof = await readDevelopmentProof(socketPath, connectionOrigin, otherOrigin);
      assert.equal((await fetch(connectionOrigin + '/api/desktop-connections', { headers: { ...headers, Origin: otherOrigin, 'X-AgentWorks-Desktop': otherProof } })).status, 200);
      // A second frontend origin never invalidates the first one's proof.
      assert.equal((await fetch(connectionOrigin + '/api/desktop-connections', { headers })).status, 200);
      if (previousProof) {
        assert.notEqual(proof, previousProof);
        assert.equal((await fetch(connectionOrigin + '/api/desktop-connections', { headers: { ...headers, 'X-AgentWorks-Desktop': previousProof } })).status, 403);
      }
      const listing = spawnSync('ps', ['eww', '-p', String(separate.pid)], { encoding: 'utf8' });
      assert.equal(listing.status, 0);
      assert.equal(listing.stdout.includes(proof) || separateOutput.includes(proof), false, 'split proof leaked through ps or logs');
      previousProof = proof;
      console.log(attempt ? 'Separate backend restart with localhost API: fresh proof accepted, retired proof refused.' : 'Separate backend/frontend: connections HTTP 200; missing, wrong and cross-origin proof HTTP 403.');
    } finally {
      if (separate.exitCode === null && separate.signalCode === null) { const closed = once(separate, 'exit'); separate.kill('SIGKILL'); await closed; }
    }
  }
})().catch(error => { console.error(error.message); process.exitCode = 1; }).finally(async () => {
  if (workspaceProcess && workspaceProcess.exitCode === null && workspaceProcess.signalCode === null) {
    const closed = once(workspaceProcess, 'exit'); workspaceProcess.kill('SIGKILL'); await closed;
  }
});
