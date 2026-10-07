[← browser / browser](index.md)

# PLAT-662: Leftover agent-browser drivers after CDP workflow runs

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | browser |
| Area | browser |
| Summary | agent-browser helpers outlived their runs and chats; a reaper in the server now ends orphans, ended chat helpers and day-old runtime files |

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

## Live findings (2026-10-07, owner's Mac, agent-browser 0.38.2)

- agent-browser runs one daemon per `--session` (`agent-browser-darwin-arm64`, no arguments, parent 1, env
  `AGENT_BROWSER_DAEMON=1`, `AGENT_BROWSER_SESSION`, `AGENT_BROWSER_SOCKET_DIR`, `AGENT_BROWSER_CDP`). It records the owning
  daemon's PID in `<socket dir>/<session>.pid`; `/tmp/.agent-browser` held 449 entries, many days old.
- Three daemons ran for `shared-cdp-9222` but its `.pid` named only one: the other two were orphans left when a later start
  took the session and socket over.
- Two per-chat helpers (`session-<hash>--browser`, attached to the extension relay's `/cdp/` endpoint) were still running
  after their Upwork run or chat had ended.
- On a throwaway Chrome (port 9447), `agent-browser --session X --cdp <port> close` makes a CDP-attached daemon exit and
  only disconnects: Chrome and its pages stay open. So `close` is safe for CDP-attached sessions.

## Fix

`agent_go/pkg/browser/helper_reaper.go`, started by the server (`browser.StartHelperReaper`, once at start and every
10 minutes) and also run after every `cleanupBrowserSessions` (chat stop, clear, workflow end). It lists this user's
agent-browser daemons (Linux: `/proc`, same mount namespace only; macOS: `ps` and `ps eww` for the environment) and:

1. Orphans: a daemon older than 2 minutes whose PID is in no session's `.pid` file (its own socket dir must be readable
   here) gets SIGTERM, then SIGKILL after 5 s if it is still the same daemon. It owns no socket any more.
2. Ended chats: a per-chat helper (`[<prefix>--]session-<16 hex>--browser`) whose name is not a live extension relay
   connection or chat client, not in the session tracker, and not the browser of a session with active work is ended:
   CDP-attached with `agent-browser --session <name> --cdp <endpoint> close` (SIGTERM if it is still up), headless with the
   normal full teardown. The shared `shared-cdp-<port>` session is never closed, only its orphans.
3. Stale files: `.pid/.sock/.config/.target` (and the other bookkeeping files) older than a day whose session has no
   running daemon and whose `.pid` names no live process are removed, only when owned by this user.

It does nothing when agent-browser is not installed. A named instance (`AGENTWORKS_SKIP_GLOBAL_BROWSER_CLEANUP=true`)
only ends its own prefixed chat helpers, never orphans or files that may belong to the main app. Every action is logged
with `[BROWSER_HELPER_REAPER]`.

## Verification

`TestPlanHelperReapOrphansEndedChatsAndStaleFiles` pins the orphan, ended-chat and stale-file decisions on the live
layout above (temp dir, injected daemon list). Build, vet, lint and the test ran on GitHub (`scripts/verify-remote.sh`).

## Left (owner, live)

After the next local restart: check that only one `shared-cdp-<port>` helper per port remains
(`ps -axo pid,etime,args | grep agent-browser-`, each PID matching `/tmp/.agent-browser/shared-cdp-<port>.pid`), that
per-chat helpers exit within about 10 minutes after their chat ends, and that the day-old files in `/tmp/.agent-browser`
are gone. The server log shows each action as `[BROWSER_HELPER_REAPER]`.

Follow-up: slot accounts clean up their own leftover helpers, as the slot ([PLAT-685](plat-685.md)).
