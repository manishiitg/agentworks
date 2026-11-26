'use strict';

// GUI-launched Mac apps inherit a minimal PATH (no Homebrew, no nvm, no
// ~/.local/bin), so spawned tools like `claude`, `codex`, `npx` are not found.
// Read the user's interactive-login environment once at startup and merge it
// into process.env for every child we spawn.
//
// Shared by desktop shells and their spawned tools.

const { spawn, spawnSync } = require('child_process');

const BEGIN = '__AW_ENV_BEGIN__';
const END = '__AW_ENV_END__';

// parseEnvBlock extracts the NUL-separated KEY=VALUE pairs between the two
// markers. Markers isolate the env block even when the user's shell rc echoes
// extra text to stdout (ssh-agent banners, "Agent pid …", greeters).
function parseEnvBlock(stdout, begin = BEGIN, end = END) {
  const beginIdx = stdout.indexOf(begin);
  const endIdx = stdout.indexOf(end);
  if (beginIdx === -1 || endIdx === -1 || endIdx < beginIdx) return {};
  const block = stdout.slice(beginIdx + begin.length, endIdx);
  const out = {};
  for (const entry of block.split('\0')) {
    if (!entry) continue;
    const eq = entry.indexOf('=');
    if (eq <= 0) continue;
    out[entry.slice(0, eq)] = entry.slice(eq + 1);
  }
  return out;
}

function resolveLoginEnv({ shell = process.env.SHELL || '/bin/zsh', timeoutMs = 10000 } = {}) {
  if (process.platform !== 'darwin' && process.platform !== 'linux') return {};
  try {
    const result = spawnSync(shell, ['-ilc', `printf '%s' '${BEGIN}'; /usr/bin/env -0; printf '%s' '${END}'`], {
      encoding: 'buffer',
      timeout: timeoutMs,
      maxBuffer: 4 * 1024 * 1024,
    });
    const stdout = result.stdout ? result.stdout.toString('binary') : '';
    return parseEnvBlock(stdout);
  } catch (err) {
    console.warn('[main] Failed to resolve login shell env:', err && err.message ? err.message : err);
    return {};
  }
}

// applyLoginEnv merges the login environment into `target` (process.env by
// default). Existing values win — anything Electron or the launcher set
// intentionally stands — except PATH, which must always come from the login
// shell: that is the whole reason for doing this.
function applyLoginEnv(loginEnv, target = process.env) {
  let applied = 0;
  for (const [k, v] of Object.entries(loginEnv)) {
    if (k === 'PATH') continue;
    if (target[k] === undefined) { target[k] = v; applied++; }
  }
  if (loginEnv.PATH) { target.PATH = loginEnv.PATH; applied++; }
  return applied;
}

function resolveLoginEnvAsync({ shell = process.env.SHELL || '/bin/zsh', timeoutMs = 10000 } = {}) {
  if (process.platform !== 'darwin' && process.platform !== 'linux') return Promise.resolve({});
  return new Promise((resolve) => {
    let settled = false;
    const chunks = [];
    let totalBytes = 0;
    const maxBytes = 4 * 1024 * 1024;
    const finish = (out) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(out);
    };
    let child;
    try {
      child = spawn(shell, ['-ilc', `printf '%s' '${BEGIN}'; /usr/bin/env -0; printf '%s' '${END}'`], {
        stdio: ['ignore', 'pipe', 'ignore'],
      });
    } catch (err) {
      console.warn('[main] Failed to resolve login shell env:', err && err.message ? err.message : err);
      resolve({});
      return;
    }
    const timer = setTimeout(() => {
      try { child.kill('SIGKILL'); } catch (_) {}
      finish(parseEnvBlock(Buffer.concat(chunks).toString('binary')));
    }, timeoutMs);
    child.stdout.on('data', (chunk) => {
      if (totalBytes + chunk.length <= maxBytes) {
        chunks.push(chunk);
        totalBytes += chunk.length;
      }
    });
    child.on('error', (err) => {
      console.warn('[main] Failed to resolve login shell env:', err && err.message ? err.message : err);
      finish({});
    });
    child.on('close', () => {
      finish(parseEnvBlock(Buffer.concat(chunks).toString('binary')));
    });
  });
}

function importLoginShellEnv(options) {
  const loginEnv = resolveLoginEnv(options);
  const applied = applyLoginEnv(loginEnv);
  console.log('[main] Imported', Object.keys(loginEnv).length, 'env vars from login shell');
  return { loginEnv, applied };
}

async function importLoginShellEnvAsync(options) {
  const loginEnv = await resolveLoginEnvAsync(options);
  const applied = applyLoginEnv(loginEnv);
  console.log('[main] Imported', Object.keys(loginEnv).length, 'env vars from login shell');
  return { loginEnv, applied };
}

module.exports = {
  BEGIN,
  END,
  parseEnvBlock,
  resolveLoginEnv,
  resolveLoginEnvAsync,
  applyLoginEnv,
  importLoginShellEnv,
  importLoginShellEnvAsync,
};
