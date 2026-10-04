[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-422 — Every Muse message ran three or more times: a stale "Message not sent" notice made accepted messages look refused

| Coordination | Value |
|---|---|
| State | fixed on `main` (provider `b75b7b5`, pinned here); deploy to Excellence and RTS in progress |
| Severity | P1 (duplicate runs and cost on every Muse message; answers arrived late) |
| Date | 2026-10-04 |
| Owner | coding-agent-bridge |
| Related | PLAT-417 (the refused-message retry), PLAT-421 |

## Problem

On Excellence (release `agents-b6dd133e`) a single "hi" in Code with Muse showed up seven times in the Muse terminal, got three different replies, and the chat answered only after 1 min 38 s.
`agent.log` showed `[MUSE_SUBMIT_REJECTED]` with rejection 1 to 6 and growing waits.

## Cause

The retry added in PLAT-417 decides a message was refused by searching the whole visible pane for `message not sent`. Muse also queues a message while a run is still starting, and the notice then stays on screen.
After one real refusal every later submit saw that old notice, was judged refused, cleared with Ctrl+U and typed again. Each typed copy had in fact been accepted, so each one ran.

## Fix

`museSubmitRejected` now gets the pane from just before Enter. `museRefusedThisSubmit`: the notice must be on screen and either be new (more notices than before Enter) or the draft must still be in the input box
(the area between the last two rules above the status line); an accepted message empties that box. Test `TestMuseRefusedThisSubmit` uses panes shaped like the live one (stale notice with an empty input, a new notice, a stale
notice scrolled away with the draft left, no rules in the pane).

## Left

- Deploy and check on Excellence: one message shows once in the Muse terminal and answers in seconds, not minutes.
- Why the first submit after a resume is refused for up to about 90 s ("another run is still starting"): cold TUI start or the resumed run re-attaching; not measured yet. The wrapper's update-check stamp is not the cause (PLAT-417 now sets `MUSE_NO_AUTO_UPDATE=1`).
- Muse already has the earlier duplicates in its saved conversation for the owner's test chat.
