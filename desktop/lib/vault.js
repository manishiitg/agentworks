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
const { waitForHealth } = require('./health');

const AGENT_PORT = 45678;
const WORKSPACE_PORT = 45679;
const VAULT_PORT = 45680;

// localPorts keeps the agent and Vault on the ports they used last time, so the
// sign-in addresses apps saved in Vault keep matching, and picks a distinct port
// for workspace in the same pass so parallel startup cannot collide when a default
// port is busy. A saved agent/Vault port that is busy now is replaced by a free
// one and saved; moved says a saved one changed.
async function localPorts(saved = {}, defaults = {}) {
  const agentDefault = defaults.agent || AGENT_PORT;
  const workspaceDefault = defaults.workspace || WORKSPACE_PORT;
  const vaultDefault = defaults.vault || VAULT_PORT;
  const agent = await freePort(saved.agent || agentDefault);
  const vault = await freePort(saved.vault || vaultDefault, [agent]);
  const workspace = await freePort(workspaceDefault, [agent, vault]);
  const moved = Boolean(saved.agent && saved.agent !== agent || saved.vault && saved.vault !== vault);
  return { agent, workspace, vault, moved };
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
    // The Vault chat project, as on servers and the source launcher.
    GATEWAY_WORKSPACE_DIR: path.join(docsDir, '_users', 'default', 'Chats', 'CapLayer'),
    VAULT_AUDIT_PROVIDER: 'sqlite',
    // This Mac's own Vault may start the person's command MCP servers (imported from Claude, Codex or Cursor).
    GATEWAY_ALLOW_LOCAL_COMMANDS: '1',
  };
}

// startVault launches agentworks-vault directly and waits until /healthz answers
// 200 (so SQLite, the Platform group, and the listener are ready before the
// agent starts) without a separate GATEWAY_BOOTSTRAP_ONLY pre-run.
function startVault({ bin, env, log, onExit, readyUrl, readyTimeoutMs = 30000, pollMs = 50 }) {
  const healthUrl = readyUrl !== undefined
    ? readyUrl
    : (env && env.GATEWAY_PORT ? `http://127.0.0.1:${env.GATEWAY_PORT}/healthz` : '');
  return new Promise((resolve, reject) => {
    const child = spawn(bin, [], { env, stdio: ['ignore', 'pipe', 'pipe'] });
    child.stdout.on('data', (d) => log.write(d));
    child.stderr.on('data', (d) => log.write(d));
    let settled = false;
    let ready = false;
    let exitReason = '';
    const finish = (err) => {
      if (settled) return;
      settled = true;
      if (err) reject(err);
      else resolve(child);
    };
    child.on('error', (err) => {
      exitReason = err && err.message ? err.message : String(err);
      finish(err);
    });
    child.on('exit', (code, signal) => {
      log.write(`\n=== vault exited code=${code} signal=${signal} ===\n`);
      exitReason = `Vault stopped before startup completed (exit ${code}, signal ${signal || 'none'}). Check vault.log for the cause.`;
      if (!settled) {
        finish(new Error(exitReason));
        return;
      }
      if (ready && onExit) onExit(code, signal);
    });
    child.once('spawn', () => {
      if (!healthUrl) {
        ready = true;
        finish(null);
        return;
      }
      waitForHealth({
        checks: [{ name: 'Vault', url: healthUrl }],
        timeoutMs: readyTimeoutMs,
        pollMs,
        initialDelayMs: 0,
        isAlive: () => exitReason || null,
        hint: 'Check vault.log for the cause.',
      }).then(
        () => {
          ready = true;
          finish(null);
        },
        (err) => {
          if (!exitReason) {
            try { child.kill('SIGTERM'); } catch (_) {}
          }
          finish(err);
        },
      );
    });
  });
}

module.exports = { localPorts, freePort, vaultState, vaultEnv, startVault, AGENT_PORT, WORKSPACE_PORT, VAULT_PORT };
