[← browser / browser](index.md)

# PLAT-414 — Stop browser finds nothing; a crashed tab stays stuck; tall screenshots kill Chrome

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | browser |
| Area | browser |
| Summary | fixed on main, not deployed. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; proved on a candidate build running beside Excellence's live service; not deployed |
| Date | 2026-10-04 |
| Owner | browser |
| Related | PLAT-401 (launcher), PLAT-374 |

## Found

- **Stop browser.** The browser panel (`WorkflowLiveBrowser`) has no stop button: its controls are Start browser, Take control / Give back,
  Start/Stop recording and Reconnect. The only stop affordance is the runtime-health control, which calls the workspace
  `GET /api/browser/processes` and `POST /api/browser/cleanup`. Both found browsers with `ps aux | grep chromium` and `pkill -f chromium`,
  so the managed Chrome (`/opt/google/chrome/chrome`) was never listed or stopped, while the list also showed other accounts'
  processes. The owner's error text "could not stop session ... start again" is not in the code; the chat Stop button
  (`SessionStopButton`, "Could not stop the session. Please try again.") stops a chat session, not a browser.
  Reproduced on Excellence with the matrix: the session's Chrome was not in the list and cleanup killed 0 of 1.
- **Stuck tab.** After `chrome://crash`, `open` timed out ("Operation timed out") until `close` plus `open`. The tool already recovered
  "CDP response channel closed" but not timeouts or crashed tabs in headless mode.
- **Tall screenshot.** `screenshot --full` of a 600000 px page killed Chrome ("CDP response channel closed").
- **Side findings.** A "Failed to save screenshot ... No such file or directory" error was treated as a dead session and the healthy
  browser was killed and relaunched. An empty `<tmp>/.agent-browser/o/p<owner>` folder and `<session>.config/.target` files were left
  per closed session.

## Done

- `workspace/handlers/browser_processes.go`: list and cleanup match Chromium, Chrome and the headless shell, only for the service
  account; `all` kills the listed pids instead of `pkill -f chromium`.
- `agent_go/pkg/browser/executor.go`: headless timeout/crashed-tab recovery (close once, retry once, `BROWSER_STUCK` otherwise; no retry
  for side-effecting commands), `SCREENSHOT_TOO_TALL` above 16000 px, "Failed to save" is not a dead session, `.config`/`.target` removed
  with the session files.
- `workspace/browserconfig.RemoveEmptySessionSocketDirs`, called after a managed session is closed (executor and workspace `close`).
- Tests: `executor_stuck_test.go`, `browser_close_test.go`, `browser_processes_chrome_test.go`; live check
  `executor_live_recovery_test.go` (skipped without `AW_LIVE_BROWSER_WORKSPACE_URL`).
- Proof on Excellence (candidate workspace build on port 24901 beside the live service, deleted afterwards): the matrix passes 55/55
  including three new stop rows (process list shows the session's Chrome, cleanup kills it and the session relaunches, close leaves
  no socket folder); the live executor test opens a page, opens `chrome://crash`, and the next `open` logs "Stuck session ... closing
  it and retrying once" and succeeds; a 600000 px full-page screenshot returns `SCREENSHOT_TOO_TALL` and the browser stays usable.

## Left

- Deploy workspace and agent to Excellence, then run `verify-browser-matrix.py` against the live service.
- `screenshot` without a path writes under `$HOME/.agent-browser/tmp/screenshots`, which the sandbox does not grant; callers must pass a
  path (the tool brokers it through managed staging). A bare `screenshot` returns the save error.
- The matrix talks to `/api/execute` directly, so its crashed-tab row still needs `close` plus `open`; the recovery lives in the
  agent_browser tool and is covered by the unit and live executor tests.
- `/tmp/.agent-browser` is shared by every account on a host and holds old `.config`/`.target` files of closed sessions; only sessions
  closed after this fix are cleaned.

## Register notes

[PLAT-414](plat-414.md), fixed on main, not deployed. The browser list and cleanup now see Chrome; a stuck
managed browser is closed and the command retried once; full-page screenshots above 16000 px are refused with a clear error.
