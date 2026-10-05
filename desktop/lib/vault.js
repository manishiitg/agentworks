'use strict';

// Vault (the mcp-gateway module) for the desktop app, set up like the server
// deploy (deploy/common/install-vault-service.py): platform mode, a private
// service token, state outside the workspace docs. The backend is pointed at it
// with CAPLAYER_SERVICE_URL; without that the backend runs with no Vault.
//
// The gateway needs the agent's port (GATEWAY_PRODUCT_URL) and the agent needs
// the gateway's port, so the caller picks the agent port first (pickPorts),
// starts Vault, then starts the agent on exactly that port.

const { spawn } = require('child_process');
const crypto = require('crypto');
const fs = require('fs');
const path = require('path');
const detect = require('detect-port');
const { fetchHealth } = require('./health');

const VAULT_PREFERRED_PORT = 45680;

// One local token, created once and reused; the gateway requires >= 32 chars.
function ensureTokenFile(stateDir) {
  fs.mkdirSync(stateDir, { recursive: true, mode: 0o700 });
  fs.chmodSync(stateDir, 0o700);
  const tokenFile = path.join(stateDir, 'service-token');
  let ok = false;
  try { ok = fs.readFileSync(tokenFile, 'utf8').trim().length >= 32; } catch (_) { /* missing */ }
  if (!ok) fs.writeFileSync(tokenFile, crypto.randomBytes(32).toString('hex'), { mode: 0o600 });
  fs.chmodSync(tokenFile, 0o600);
  return tokenFile;
}

// Exact environment the gateway runs with. Not inherited from the user's shell:
// a stray GATEWAY_* there (demo grants, local auth) would break platform mode.
function vaultEnv({ stateDir, tokenFile, docsDir, vaultPort, agentPort }) {
  return {
    PATH: process.env.PATH || '/usr/bin:/bin',
    HOME: process.env.HOME || '',
    LOCAL_MODE: 'false',
    GATEWAY_AUTH_MODE: 'platform',
    GATEWAY_BIND: '127.0.0.1',
    GATEWAY_PORT: String(vaultPort),
    GATEWAY_PUBLIC_URL: `http://127.0.0.1:${vaultPort}`,
    GATEWAY_PRODUCT_URL: `http://127.0.0.1:${agentPort}`,
    GATEWAY_STATE_DIR: stateDir,
    GATEWAY_WORKSPACE_DIR: path.join(docsDir, 'Chats', 'CapLayer'),
    GATEWAY_HUMAN_TOKEN_FILE: tokenFile,
    GATEWAY_UPSTREAM_URL: 'none',
    VAULT_AUDIT_PROVIDER: 'sqlite',
    VAULT_AUDIT_WRITE_MODE: 'async',
  };
}

// Choose the agent port and a different Vault port before anything starts.
async function pickPorts(agentPreferred, vaultPreferred = VAULT_PREFERRED_PORT) {
  const agentPort = await detect(agentPreferred);
  let vaultPort = await detect(vaultPreferred);
  if (vaultPort === agentPort) vaultPort = await detect(agentPort + 1);
  return { agentPort, vaultPort };
}

// startVault resolves { child, env } where env is what the agent needs, or
// null (after logging why) when Vault is off or could not start. The caller
// then starts the agent without CAPLAYER_*: a set-but-unreachable Vault URL
// makes the backend fail closed.
async function startVault({ bin, userDataPath, docsDir, agentPort, vaultPort, log, echoToConsole = false, timeoutMs = 20000 }) {
  const say = (m) => { log.write(`[vault] ${m}\n`); if (echoToConsole) console.log(`[vault] ${m}`); };
  if (process.env.AGENTWORKS_LOCAL_VAULT === '0') { say('disabled (AGENTWORKS_LOCAL_VAULT=0); starting without Vault'); return null; }
  if (!fs.existsSync(bin)) { say(`WARNING: vault-server not found at ${bin}; starting without Vault`); return null; }
  let env;
  try {
    const stateDir = path.join(userDataPath, 'vault');
    const tokenFile = ensureTokenFile(stateDir);
    fs.mkdirSync(path.join(docsDir, 'Chats', 'CapLayer'), { recursive: true });
    env = vaultEnv({ stateDir, tokenFile, docsDir, vaultPort, agentPort });
  } catch (err) { say(`WARNING: cannot prepare Vault state (${err.message}); starting without Vault`); return null; }

  const child = spawn(bin, [], { cwd: env.GATEWAY_STATE_DIR, env, stdio: ['ignore', 'pipe', 'pipe'] });
  let exited = null;
  child.on('error', (err) => { exited = `spawn error: ${err}`; say(exited); });
  child.on('exit', (code, signal) => { exited = `exited code=${code} signal=${signal}`; log.write(`\n=== vault-server ${exited} ===\n`); });
  child.stdout.on('data', (d) => log.write(d));
  child.stderr.on('data', (d) => log.write(d));

  const url = `${env.GATEWAY_PUBLIC_URL}/healthz`;
  const deadline = Date.now() + timeoutMs;
  while (!exited && Date.now() < deadline) {
    if (await fetchHealth(url, 2000)) {
      say(`ready on port ${vaultPort}`);
      return {
        child,
        env: { CAPLAYER_SERVICE_URL: env.GATEWAY_PUBLIC_URL, CAPLAYER_SERVICE_TOKEN_FILE: env.GATEWAY_HUMAN_TOKEN_FILE },
      };
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  say(`WARNING: vault-server did not become healthy (${exited || 'timeout'}); starting without Vault`);
  try { child.kill('SIGTERM'); } catch (_) { /* gone */ }
  return null;
}

module.exports = { startVault, pickPorts, vaultEnv, VAULT_PREFERRED_PORT };
