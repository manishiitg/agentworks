[← coding-agents / sessions](index.md)

# PLAT-034 — completed Raw tmux terminals lose their scrollback

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | coding-agents |
| Area | sessions |
| Summary | terminal live-attach retention, chat-history persistence, and Raw terminal UI |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `done` (runtime verified) |
| Last synchronized | `2026-08-05` |

- **Priority:** P1
- **Owner:** terminal live-attach retention, chat-history persistence, and Raw terminal UI
- **Source workflow:** `Workflow/social-media`
- **Problem:** Raw mode scrolled correctly while the tmux terminal was live, but
  completing/closing the process replaced the live xterm with a retained
  snapshot containing only tmux's final visible screen. The earlier rows
  disappeared and the resulting Raw pane could not scroll.
- **Impact:** a user watching the real coding-agent terminal lost the earlier
  terminal transcript exactly when the run completed. Formatted events could
  still exist, but they are a different view and are not a substitute for Raw
  tmux inspection.

## Root cause

The browser's live xterm accumulated the full streamed byte history. The
backend also persisted that stream, but labeled it `tmux_capture`. On the
live-to-settled transition:

1. the frontend correctly stopped using the live WebSocket after the terminal
   process closed;
2. the settled `content=history` route ran `capture-pane` and overwrote the
   stored stream;
3. chat-history persistence preferred another late runtime `capture-pane`
   read over the already stored stream.

Full-screen/alternate-buffer coding CLIs commonly leave tmux with no usable
scrollback at this boundary. The late capture therefore contained one viewport,
even though the full terminal byte stream had already been recorded.

## Fix

Implemented in `mcp-agent-builder-go` commit `b984e6c5c`:

- live-attach transcripts now use the distinct durable source
  `tmux_stream`;
- an inactive `content=history` request preserves a stored `tmux_stream`
  instead of replacing it with `capture-pane`;
- chat-history persistence prefers the matching stored stream over a runtime
  capture;
- restored tmux streams stay on the Raw xterm renderer;
- Raw remains the default view, while Formatted remains an explicit optional
  per-terminal view;
- xterm wheel handling and asynchronous disposal guards preserve scrolling
  without allowing a stale write callback to break the chat UI.

## Verification

Focused regression coverage proves:

- a live-attach seed plus later ANSI output is stored as `tmux_stream`;
- a completed terminal's history request does not call `capture-pane` when the
  stream exists;
- persisted chat history retains the early, middle, and final Raw lines and
  does not invoke the runtime-capture fallback.

Broader verification passed:

```text
go test ./agent_go/cmd/server -count=1
npm test -- --run
npx eslint src/components/TerminalCenter.tsx src/utils/terminalSnapshotIdentity.ts src/utils/sessionRestore.ts
npm run build
```

Result: server package passed; frontend passed 65 files / 411 tests; lint and
production build passed. After rebuilding/restarting the backend, the user
confirmed Raw tmux scrolling works in the Electron app on 2026-08-05.

## Historical limitation

The fix preserves future streams. A terminal completed before this repair may
already have had its stream overwritten by the final one-screen capture. Those
discarded bytes cannot be reconstructed as a faithful Raw terminal transcript;
the event-backed Formatted view remains the fallback for that historical run.
