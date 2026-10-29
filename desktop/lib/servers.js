'use strict';

// Spawning the two local servers every AgentWorks-family desktop shell runs.
// The mechanics are shared here: pick a port (preferred, else next free),
// spawn, stream stdout/stderr into a bounded log, resolve on the server's
// "DynamicPort: N" line, reject on spawn error, and report exit. What differs
// per app — binary paths, cwd, env — is passed in by the caller.

const { spawn } = require('child_process');
const detect = require('detect-port');

const DYNAMIC_PORT_RE = /DynamicPort: (\d+)/;

// spawnServer resolves { child, port } once the server prints its port.
//   name           label used in log lines ("workspace", "agent")
//   bin, args      args may contain the placeholder PORT_PLACEHOLDER
//   preferredPort  detect() returns it if free, else the next available
//   cwd, env       passed to spawn
//   log            a bounded log writer ({ write })
//   onExit(code, signal)   optional
function spawnServer({ name, bin, args, preferredPort, cwd, env, log, onExit, bootstrap, echoToConsole = false }) {
  return detect(preferredPort).then((port) => new Promise((resolve, reject) => {
    const finalArgs = args.map((a) => (a === spawnServer.PORT_PLACEHOLDER ? String(port) : a));
    // Some per-process settings (notably the local OAuth callback origin)
    // depend on the port detect() actually selected. Accept an env factory so
    // callers never have to guess the preferred port or persist a stale one.
    const resolvedEnv = typeof env === 'function' ? env(port) : env;
    const options = { cwd, env: resolvedEnv, stdio: ['ignore', 'pipe', 'pipe'] };
    const child = bootstrap
      ? require('./connections').spawnWithBootstrap(bin, finalArgs, options, bootstrap(port))
      : spawn(bin, finalArgs, options);
    let portFound = false;
    let crewUpdateFailed = false;

    child.on('error', (err) => {
      const msg = `[${name}] spawn error: ${err}\n`;
      log.write(msg);
      if (echoToConsole) console.error(msg.trim());
      if (!portFound) reject(err);
    });
    child.on('exit', (code, signal) => {
      log.write(`\n=== ${name}-server exited code=${code} signal=${signal} ===\n`);
      if (onExit) onExit(code, signal);
      if (!portFound) reject(new Error(crewUpdateFailed
        ? "AgentWorks couldn't finish updating your Crews. Your working folders are unchanged. Quit and reopen to try again."
        : `${name} stopped before startup completed (exit ${code}, signal ${signal || 'none'}). Check its log for the cause.`));
    });
    child.stdout.on('data', (d) => {
      const output = d.toString();
      if (name === 'workspace' && output.includes('[CREW_STATE_MIGRATION] failed:')) crewUpdateFailed = true;
      log.write(output);
      if (echoToConsole) process.stdout.write(`[${name}] ${output}`);
      if (!portFound) {
        const match = output.match(DYNAMIC_PORT_RE);
        if (match) {
          portFound = true;
          resolve({ child, port: parseInt(match[1], 10) });
        }
      }
    });
    child.stderr.on('data', (d) => {
      log.write(d);
      if (echoToConsole) process.stderr.write(`[${name}] ${d}`);
    });
  }));
}
spawnServer.PORT_PLACEHOLDER = '__PORT__';

function killServer(child, signal = 'SIGTERM') {
  if (!child) return;
  try { child.kill(signal); } catch (_) { /* already gone */ }
}

module.exports = { spawnServer, killServer, DYNAMIC_PORT_RE };
