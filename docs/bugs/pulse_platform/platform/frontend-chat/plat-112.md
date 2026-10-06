[← platform / frontend-chat](index.md)

# PLAT-112 — production UI exposes the internal terminal/child-agent debug rail and pays for it even when nobody is debugging

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | frontend-chat |
| Summary | The normal workflow workspace currently renders a large vertical rail of |

| Field | Value |
|---|---|
| Status | `implemented_pending_live_reverify` — default-off product boundary implemented 2026-08-16; restart and live UI verification pending |
| Priority | P1 |
| Owner | runtime projection, tmux/terminal observation APIs, and workflow workspace UI |
| Reported | 2026-08-16 |
| Related | [PLAT-095](../scheduler-runs/plat-095.md)), [PLAT-106](plat-106.md), [PLAT-107](plat-107.md), [PLAT-111](plat-111.md) |

## Problem

The normal workflow workspace currently renders a large vertical rail of
terminal and tmux child nodes. The screenshot shows more than a dozen nearly
identical terminal icons with live indicators. These nodes are useful while an
engineer investigates a runtime defect, but they are not a production user
surface. They obscure the Chat/Schedule product, make the UI look broken, and
encourage accidental selection of a raw terminal instead of the meaningful
workflow outcome.

More importantly, this must not be treated as a CSS-only cleanup. The debug
rail is fed by runtime projection, terminal observation, and streaming/polling
work. Hiding the icons while still enumerating tmux sessions, capturing panes,
projecting every child, subscribing to terminal events, or fetching their
history leaves the production cost and lifecycle complexity intact.

## Required product boundary

Production should show only product-facing concepts:

- the normal interactive Chat;
- a scheduled run when one is active or selected;
- human decisions, workflow status, and concise run progress.

The active main chat additionally has two intentional views: **Formatted** and
**Main terminal**. Selecting Main terminal is an explicit request for that
chat's own raw tmux pane; it is not diagnostic mode and must not reveal or
poll any child terminal.

Individual tmux panes, child terminals, raw structured event streams, pane
screenshots, and execution-tree debug nodes belong to an explicit engineering
diagnostic mode.

## Implemented repair

1. `AGENTWORKS_RUNTIME_DEBUG=1` gates the server’s terminal and
   execution-tree HTTP routes. It defaults off; the capability response reports
   `runtime_debug:false` and terminal routes return 404 before enumerating a
   pane, observing snapshots, or capturing tmux output.
2. `VITE_RUNTIME_DEBUG=1` is the matching frontend opt-in. The terminal rail
   mounts only when **both** client and server flags are enabled. This prevents
   an accidentally enabled browser build from repeatedly calling a disabled
   server API.
3. Normal Chat and Schedule tabs now mount `TerminalEventTranscript` directly
   from their durable session/SSE events. They no longer mount `TerminalCenter`,
   request `/terminals`, request `/execution-tree`, or use terminal state to
   choose a restore/landing surface.
   Resuming an older coding-CLI chat still asks the backend to restore its
   internal transport, but it no longer schedules terminal refresh bursts or
   changes the visible conversation to a terminal/tree surface.
4. Workflow tab badges and “new chat” conflict checks now use ordinary session
   lifecycle fields rather than polling the execution tree or a terminal list.
   A retained-but-idle tmux process is an internal reuse detail, not a reason to
   block product navigation.
5. Restored and newly opened workflow chats explicitly select formatted mode.
   The developer-only terminal rail remains available only with both flags set.
   `./run_server_with_logging.sh --enable-chat-terminal-debugs --with-frontend`
   enables both flags for a local diagnostic run.
6. `/sessions/{session_id}/main-terminal` is the sole normal-mode raw terminal
   endpoint. It resolves one `main_agent` snapshot server-side and reuses the
   canonical tmux capture path; it never lists child terminals. The endpoint
   is mounted only after the user chooses Main terminal, so Formatted view has
   no terminal polling or pane capture.

## Verification and P0 coverage

1. With the flag unset, open a workflow with 20 active background children.
   The UI shows Chat/Schedule status only; no terminal rail or raw-pane tab is
   present.
2. Instrument the terminal discovery, capture, runtime-projection, and
   terminal-event endpoints. In Formatted view they receive **zero** calls
   after initial product-runtime hydration. Switching to Main terminal may
   call only that session's focused main-terminal endpoint.
3. With the flag enabled, the same workflow shows the diagnostic rail and can
   inspect a selected child terminal without changing normal Chat/Schedule
   selection.
4. A live schedule with background agents still reports correct progress and
   completion in flag-off mode. The diagnostic gate must not be another source
   of PLAT-095 lifecycle divergence.
5. Add a frontend deferred-response test proving that a hidden/debug child
   cannot create a tab, focus itself, or surface a blank “Waiting for terminal”
   pane while diagnostics are disabled.

## Acceptance

- Production default is quiet, product-oriented, and makes no debug-terminal
  network calls.
- Diagnostics remain easy to enable deliberately with the paired server and
  frontend development flags.
- Chat, Schedule, status, notifications, and completion work identically with
  diagnostics disabled.
- The gate is centrally enforced in both backend capability/projection and
  frontend request/render paths, rather than merely hiding icons.
