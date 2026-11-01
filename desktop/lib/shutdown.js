'use strict';

// A SIGTERM/SIGINT to the Electron main process (a `kill`, a supervisor, a
// dev script) does not run Electron's before-quit/will-quit handlers, and
// would otherwise orphan the spawned servers — still bound to their ports,
// still writing into the workspace the next launch expects to own. Shared
// so every desktop shell performs the same cleanup.
function installSignalShutdown({ app, stop, log = console.log, signals = ['SIGTERM', 'SIGINT'] }) {
  for (const signal of signals) {
    process.on(signal, () => {
      log(`[main] received ${signal}`);
      try { stop(); } catch (_) { /* best effort; we are exiting anyway */ }
      app.exit(0);
    });
  }
}

module.exports = { installSignalShutdown };
