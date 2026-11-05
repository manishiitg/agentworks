'use strict';

// Every Mac runs its own Vault (owner, 5 Nov): the packaged agentworks-vault
// beside the agent, in the same private platform mode the source launcher
// (agent_go/run_server_with_logging.sh) uses. Vault prints no port line, so it
// is started on a port chosen here and is ready once /healthz answers.

const crypto = require('crypto');
const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');
const net = require('net');

const AGENT_PORT = 45678;
const VAULT_PORT = 45680;

// localPorts keeps the agent and Vault on the ports they used last time, so the
// sign-in addresses apps saved in Vault keep matching. A saved port that is busy
// now is replaced by a free one and saved; moved says a saved one changed.
async function localPorts(saved = {}) {
  const agent = await freePort(saved.agent || AGENT_PORT);
  const vault = await freePort(saved.vault || VAULT_PORT, [agent]);
  const moved = Boolean(saved.agent && saved.agent !== agent || saved.vault && saved.vault !== vault);
  return { agent, vault, moved };
}

// freePort is the first port from preferred on that can be bound on 127.0.0.1, where both servers listen
// (detect-port also answers "free" for a port another program holds only on 127.0.0.1).
async function freePort(preferred, taken = []) {
  for (let port = preferred; port < preferred + 200; port++) {
    if (taken.includes(port)) continue;
    const ok = await new Promise((resolve) => {
      const probe = net.createServer();
      probe.once('error', () => resolve(false));
      probe.listen(port, '127.0.0.1', () => probe.close(() => resolve(true)));
    });
    if (ok) return port;
  }
  throw new Error(`No free local port near ${preferred}. Quit other programs using ports ${preferred}+ and open AgentWorks again.`);
}

// vaultState makes the private state folder and the service credential the agent
// and Vault share (64 hex characters, owner-only).
function vaultState(stateRoot) {
  const dir = path.join(stateRoot, 'vault');
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  fs.chmodSync(dir, 0o700);
  const tokenFile = path.join(dir, 'service-token');
  if (!fs.existsSync(tokenFile) || fs.statSync(tokenFile).size === 0) {
    fs.writeFileSync(tokenFile, crypto.randomBytes(32).toString('hex'), { mode: 0o600 });
  }
  fs.chmodSync(tokenFile, 0o600);
  return { dir, tokenFile };
}

// vaultEnv drops inherited Vault settings so only this installation's apply.
function vaultEnv({ inherited, port, agentPort, stateDir, tokenFile, docsDir }) {
  const env = {};
  for (const [key, value] of Object.entries(inherited)) {
    if (!key.startsWith('GATEWAY_') && !key.startsWith('VAULT_') && !key.startsWith('CAPLAYER_')) env[key] = value;
  }
  return {
    ...env,
    GATEWAY_PORT: String(port),
    GATEWAY_BIND: '127.0.0.1',
    GATEWAY_PUBLIC_URL: `http://127.0.0.1:${port}`,
    GATEWAY_AUTH_MODE: 'platform',
    GATEWAY_PRODUCT_URL: `http://127.0.0.1:${agentPort}`,
    GATEWAY_STATE_DIR: stateDir,
    GATEWAY_HUMAN_TOKEN_FILE: tokenFile,
    GATEWAY_UPSTREAM_URL: 'none',
    // The Vault chat project, as on servers and the source launcher.
    GATEWAY_WORKSPACE_DIR: path.join(docsDir, '_users', 'default', 'Chats', 'CapLayer'),
    VAULT_AUDIT_PROVIDER: 'sqlite',
  };
}

// startVault prepares Vault's configuration once (GATEWAY_BOOTSTRAP_ONLY), then
// starts it and resolves with the running child.
function startVault({ bin, env, log, onExit }) {
  const pipe = (child) => {
    child.stdout.on('data', (d) => log.write(d));
    child.stderr.on('data', (d) => log.write(d));
  };
  return new Promise((resolve, reject) => {
    const prepare = spawn(bin, [], { env: { ...env, GATEWAY_BOOTSTRAP_ONLY: '1' }, stdio: ['ignore', 'pipe', 'pipe'] });
    pipe(prepare);
    prepare.on('error', reject);
    prepare.on('exit', (code) => {
      if (code !== 0) return reject(new Error(`Vault could not prepare its settings (exit ${code}). Check vault.log for the cause.`));
      const child = spawn(bin, [], { env, stdio: ['ignore', 'pipe', 'pipe'] });
      pipe(child);
      child.on('error', reject);
      child.on('exit', (code, signal) => {
        log.write(`\n=== vault exited code=${code} signal=${signal} ===\n`);
        if (onExit) onExit(code, signal);
      });
      resolve(child);
    });
  });
}

module.exports = { localPorts, vaultState, vaultEnv, startVault, AGENT_PORT, VAULT_PORT };
