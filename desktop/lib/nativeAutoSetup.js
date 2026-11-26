'use strict';
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const { execFile } = require('node:child_process');
const { promisify } = require('node:util');
const { isAppFrame, withoutDesktopBootstrap } = require('./connections');

// The installed app is the source of the CLI, including drag-and-drop installs.
// Never replace another program or an arbitrary symlink at this name.
function installBundledCLI(bin, home) {
  fs.accessSync(bin, fs.constants.X_OK);
  const dir = path.join(home, '.local', 'bin');
  fs.mkdirSync(dir, { recursive: true });
  const target = path.join(dir, 'agentworks');
  let previous;
  try { previous = fs.lstatSync(target); } catch (e) { if (e.code !== 'ENOENT') throw e; }
  if (previous?.isSymbolicLink()) {
    const old = fs.readlinkSync(target);
    if (old === bin) return 'current';
    if (!old.endsWith('/AgentWorks.app/Contents/Resources/bin/agentworks')) return 'kept_existing';
  } else if (previous) {
    if (!previous.isFile() || previous.size > 128 * 1024 * 1024 || !fs.readFileSync(target).includes(Buffer.from('github.com/manishiitg/coding-agent-loop/agent_go/cmd/agentworks'))) return 'kept_existing';
  }
  if (previous) fs.renameSync(target, target + '.before-app-update-' + Date.now());
  fs.symlinkSync(bin, target);
  return 'installed';
}

function createNativeAutoSetup({ stateFile, bin, version, home = os.homedir(), run = promisify(execFile), log = console.log }) {
  let state = {};
  // Bad state is not permission to re-enable something the person turned off.
  try {
    if (fs.lstatSync(stateFile).isSymbolicLink()) throw new Error('Linked native setup state');
    state = JSON.parse(fs.readFileSync(stateFile, 'utf8'));
  }
  catch (e) { if (e.code !== 'ENOENT') throw e; }
  if (!state || typeof state !== 'object' || Array.isArray(state)) throw new Error('Invalid native setup state');
  const save = () => {
    fs.mkdirSync(path.dirname(stateFile), { recursive: true });
    const temp = stateFile + '.' + crypto.randomUUID() + '.tmp';
    try {
      fs.writeFileSync(temp, JSON.stringify(state), { mode: 0o600, flag: 'wx' });
      fs.renameSync(temp, stateFile);
    } finally { try { fs.unlinkSync(temp); } catch (_) {} }
  };
  const report = (provider, outcome) => log(`[NATIVE_CONNECT] event=auto_setup outcome=${outcome} provider=${provider}`);
  let pending;
  async function apply(disable) {
    try {
      const { stdout } = await run(bin, ['setup', 'desktop', ...(disable ? ['--disable'] : [])], {
        env: { ...withoutDesktopBootstrap(process.env), PATH: path.join(home, '.local', 'bin') + path.delimiter + (process.env.PATH || '') },
        cwd: home, timeout: 390000, maxBuffer: 65536, windowsHide: true,
      });
      const { apps } = JSON.parse(stdout);
      if (!Array.isArray(apps) || apps.length !== 2 || !['claude', 'codex'].every(p => apps.some(a => a.provider === p && ['installed', 'removed', 'not_installed', 'failed'].includes(a.outcome)))) throw new Error('Invalid setup receipt');
      for (const a of apps) {
        report(a.provider, a.outcome);
        if (a.provider === 'claude' && Array.isArray(a.cleanup)) for (const c of a.cleanup) {
          if (!['none', 'agentworks@agentworks', 'agentworks-tools@agentworks'].includes(c.removed) || !['agentworks@agentworks', 'agentworks-tools@agentworks'].includes(c.kept) || !['user', 'project', 'local'].includes(c.scope) || !['kept', 'removed', 'failed'].includes(c.outcome)) continue;
          log(`[NATIVE_CONNECT] event=plugin_cleanup removed=${c.removed} kept=${c.kept} scope=${c.scope} outcome=${c.outcome}`);
        }
      }
      const complete = apps.every(a => a.outcome !== 'failed');
      if (disable) state.removed = complete;
      else {
        if (complete) state.version = version;
        if (complete && apps.some(a => a.outcome === 'installed')) state.ready = true;
      }
      save();
      return complete;
    } catch (_) { report('claude', 'failed'); report('codex', 'failed'); return false; }
  }
  return {
    start() {
      if (pending) return pending;
      pending = (async () => {
        try {
          const outcome = installBundledCLI(bin, home); report('cli', outcome);
          if (outcome === 'kept_existing') { report('claude', 'cli_conflict'); report('codex', 'cli_conflict'); return; }
        }
        catch (_) { report('cli', 'failed'); return; }
        if (state.disabled) {
          if (!state.removed) await apply(true);
          else { report('claude', 'disabled'); report('codex', 'disabled'); }
        } else if (state.version !== version) await apply(false);
        else { report('claude', 'current'); report('codex', 'current'); }
      })();
      return pending;
    },
    async notice() {
      await this.start();
      if (state.disabled || !state.ready || state.noticeSeen) return false;
      return true;
    },
    dismissNotice() { state.noticeSeen = true; save(); },
    async turnOff() {
      // Persist before invoking providers, so an error never re-enables hooks on update.
      state.disabled = true; state.removed = false; save();
      await this.start();
      return { ok: await apply(true) };
    },
  };
}

function installNativeSetupIPC({ ipcMain, controller, getWindow, getOrigin }) {
  for (const [name, action] of [['native-setup-notice', 'notice'], ['native-setup-off', 'turnOff'], ['native-setup-notice-seen', 'dismissNotice']]) {
    ipcMain.handle(name, async event => {
      const contents = getWindow()?.webContents;
      if (!contents || event.sender !== contents || !isAppFrame(event.senderFrame, contents, getOrigin())) throw new Error('Use the main app window.');
      return controller[action]();
    });
  }
}
module.exports = { createNativeAutoSetup, installBundledCLI, installNativeSetupIPC };
