[← chat / stop](index.md)

# PLAT-699: No Stop button while a Crew or Code chat shows Working

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | chat |
| Area | stop |
| Summary | Stop now follows the same in-flight signal as the transcript's "Working…" row, so a running turn can always be stopped. |

## What happened

2026-10-07, Excellence: two users (Crew "yami" and a Code chat) saw the agent's
answer, then "Working…", and the composer offered Send but no Stop. Neither
turn came from Slack, so `isBotRun` was not the cause.

"Working…" in the Crew/Code transcript (TerminalEventTranscript's activity
footer) comes from `chatRuntimeActivity`: the tab's `isStreaming` /
`hasRunningBgAgents` flags, OR the turn's own events (a foreground start with
no completion yet), OR the active-session cache saying the session is running.
The composer's Stop (`ChatInput` `showStopButton`) and `SessionStopButton`
itself read only the tab flags. Those flags follow the server's volatile
`can_steer`/tmux-busy heuristic and drop during a run, so the transcript kept
saying Working while Stop vanished and Send came back.

## Fix

- `hooks/useTabTurnActive.ts`: the tab flags OR `chatRuntimeActivity(...)` in
  state `running` (the same classification as the Working row, without its
  refresh side effect).
- `ChatInput` folds it into `isTurnInFlight`; `SessionStopButton` gates on it,
  so the composer Stop and the run footer Stop both show whenever Working does.
- Stop still calls the same session stop endpoint as before; authorization is
  unchanged (it is the same call a person already makes on their own turns).
- Checked and left alone: `isBotRun` hiding the composer Stop. Every tab that
  sets `isBotRun` (workflow and Crew/Code) is a view-only run tab, and ChatArea
  renders its run-footer Stop for those on every surface.
- Test: `components/SessionStopButton.test.tsx`.

## Left

- Not verified live on Excellence; the server log for the 12:43-12:50 CEST
  window was not read, so whether those turns were really still running or
  the Working row was stale is unconfirmed. If stale, Stop now shows with it
  and stopping ends it; a stale Working row on its own would be a separate fix.

## Follow-up: two kinds of Stop (owner, 2026-10-07)

In a chat, the composer Stop only interrupts what the coding CLI is doing in tmux (`/api/session/cancel-turn`, the same as Escape; each adapter sends its own interrupt) and keeps the session. Steps, workflows and background agents the chat started keep running. The run footer on scheduled/triggered runs keeps the full stop (`/api/session/stop`).
