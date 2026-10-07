[← app / ui](index.md)

# PLAT-696: Remove the old Runtime Health panel and its process endpoints

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | app |
| Area | ui |
| Summary | Removed the account-menu Runtime Health panel and the list/cleanup routes only it used |

## What happened

Owner decision 2026-10-07: "we should just remove this … and related backend code". The
Runtime Health panel (account menu → Runtime health: Browsers and Workflow processes, with
kill buttons) was very old, inaccurate on slot servers (it counted only the server account's
`ps` output and never the Chrome extension's tabs, see PLAT-695), its cleanup now happens
automatically, and its list routes showed server processes to every member.

## Fix

- Frontend: deleted `frontend/src/components/topbar/RuntimeHealthControl.tsx`; removed the
  menu item and embedded view from `AccountControl.tsx` (and its test); removed
  `getBrowserProcesses`, `getBrowserSessionTracking`, `cleanupBrowserProcesses`,
  `getWorkflowProcesses`, `cleanupWorkflowProcesses` from `services/api.ts`.
- Workspace service: removed `GET /api/browser/processes`, `POST /api/browser/cleanup`
  (`workspace/handlers/browser_processes.go` and its two tests deleted), `GET /api/processes`
  and `POST /api/processes/cleanup` (`ListWorkflowProcesses`, `CleanupWorkflowProcesses`,
  `listManagedProcesses` in `process_manager.go`).
- Agent server: removed `GET /api/browser/sessions` (`handleGetBrowserSessions`) and
  `browser.ActiveCDPOwnersSnapshot` (and its test), whose only caller it was; removed the
  now-empty `workspaceProxyAdminOnlyRoutes` check and its test.
- Kept: the workspace's periodic stale-process sweeper (`StartWorkflowProcessSweeper`,
  `runWorkflowProcessSweep`, `cleanupStaleWorkflowProcesses`, `findStaleWorkflowProcesses`)
  and the agent server's browser helper reaper (PLAT-662/685).
- `deploy/rootless-linux/verify-browser-matrix.py`: dropped the checks that drove the removed
  routes; "stop" is checked through `close` only.

## Left

Nothing. Committed bundles under `agent_go/static/` refresh on the next frontend build/deploy.
