[← browser / browser](index.md)

# PLAT-662: Leftover agent-browser drivers after CDP workflow runs

| Field | Value |
|---|---|
| State | open |
| Priority | P3 |
| Product | browser |
| Area | browser |
| Summary | agent-browser driver processes stay attached to the CDP Chrome after their workflow run ends |

## What happened

Local, 2026-10-07: four `agent-browser-darwin-arm64` processes were running, orphaned (parent 1). Two of them still
held debugging connections to the CDP Chrome (port 9222): one from the linkedin workflow (1 h 13 m old, working folder
`Workflow/linkedin`) and one from a sales outreach run (2 h old). They used no CPU and about 13–17 MB each. The CDP
Chrome's main process was also at 170–200% CPU with only a few pages open (it had been running for two days); quitting
it was the fix.

## What is known

`cleanupBrowserSessions` (`agent_go/cmd/server/session_lifecycle.go`) runs when a chat is stopped, cleared or a workflow
finishes. It releases tab ownership and extension conversations and closes tracked headless sessions, but the
agent-browser daemons attached to the shared CDP Chrome stay up.

## Left

Find out how agent-browser ends a CDP-attached session without closing the shared Chrome, and end the daemon when its
run finishes (or after an idle timeout). This needs a live check against a running CDP Chrome, because a plain `close` may
shut that Chrome down. It is low impact (memory only), so it was ticketed rather than changed blind.
