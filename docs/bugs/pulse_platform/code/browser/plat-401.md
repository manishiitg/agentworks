[← code / browser](index.md)

# PLAT-401 — Code project browser starts, screenshots and live-views under the Landlock sandbox

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | code |
| Area | browser |
| Summary | fixed on main; installed and proved on Excellence, other servers pending. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; Excellence has the launcher installed (proved with the matrix); deploy to the other servers pending |
| Date | 2026-10-03 |
| Owner | browser |
| Related | PLAT-374 (mount-namespace fallback removed), PLAT-364 |

## Found

After PLAT-374 the Code/Crew project browser ran Chrome as the service account under Landlock. Three separate causes, found with
Chrome's own stderr through the isolator on Excellence:

1. HOME (`/srv/agents/home`) is not writable in the sandbox; Chrome died at start ("exited early ... without writing
   DevToolsActivePort": crashpad `--database is required`, SIGTRAP). `/usr/bin/google-chrome` also writes under HOME.
2. Excellence ran the system Chrome directly (no `AGENT_BROWSER_EXECUTABLE_PATH`, no launcher).
3. Through the `chrome -> /opt/google/chrome/chrome` symlink Chrome looks for `libvulkan` beside the path it was started as;
   SwANGLE failed to initialise and the browser died (SIGTRAP) on the first screenshot or screencast: agent-browser
   "CDP response channel closed".

## Done

- `chrome-agentworks`: writable HOME/XDG under its private temp dir; runs the resolved Chrome binary.
- `install-managed-chrome.sh` (called by `build-and-activate.sh`) installs it beside the host Chrome
  (`<app>/tools/chrome/current`, system Chrome gets `tools/chrome/system`); `runtime_profile.json` sets
  `AGENT_BROWSER_EXECUTABLE_PATH` for every product.
- Regression tests: `workspace/security/chrome_devtools_linux_test.go` (DevTools reached, PNG screenshot; both fail with the
  earlier launchers).
- `deploy/rootless-linux/verify-browser-matrix.py`: full matrix through the real `/api/execute` path with multi-user headers (55
  rows: start/open https+http, interaction, tabs, screenshots viewport/full/annotated/element, PDF, live-view JPEG frames,
  upload/download, console, page errors, network requests, HAR, trace, profiler, capture API, persistence, heavy page, two
  concurrent sessions, kill -9 recovery, crash recovery, idle timeouts, close, 20 open/close cycles).
  `verify-managed-chrome.py` now sends the app's headers too.

## Left

- Deploy to Confida (same script) and, if it uses the rootless deploy, Dominion; re-run the matrix there.
- Matrix findings: an `open` right after `close` can fail once ("Failed to connect": the app already retries three times); the live
  stream's console messages arrive but no console panel uses them. The crashed-tab and tall-screenshot findings moved to
  [PLAT-414](../../platform/browser/plat-414.md).

## Register notes

[PLAT-401](plat-401.md), fixed on main; installed and proved on Excellence, other servers pending.
Managed Chrome launcher with a writable HOME and the resolved binary, installed by the deploy; regression tests and a
52-row browser matrix script.
