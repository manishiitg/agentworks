[← platform / coding-agent-bridge](index.md)

# PLAT-422 — Every Muse message ran three or more times: a stale "Message not sent" notice made accepted messages look refused

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | fixed on `main`; deploy in progress. |

| Coordination | Value |
|---|---|
| State | fixed on `main` (provider `04645b8`, pinned here); `b75b7b5` (pane-notice count) was live on Excellence `agents-669e775b` and is superseded; deploy of `04645b8` to Excellence and RTS in progress |
| Severity | P1 (duplicate runs and cost on every Muse message; answers arrived late) |
| Date | 2026-10-04 |
| Owner | coding-agent-bridge |
| Related | PLAT-417 (the refused-message retry), PLAT-421, PLAT-352 (durable submit acknowledgement; `docs/refactor/durable_ack_p0.md`) |

## Problem

On Excellence (release `agents-b6dd133e`) a single "hi" in Code with Muse showed up seven times in the Muse terminal, got three different replies, and the chat answered only after 1 min 38 s.
`agent.log` showed `[MUSE_SUBMIT_REJECTED]` with rejection 1 to 6 and growing waits.

## Cause

The retry added in PLAT-417 decides a message was refused by searching the whole visible pane for `message not sent`. Muse also queues a message while a run is still starting, and the notice then stays on screen.
After one real refusal every later submit saw that old notice, was judged refused, cleared with Ctrl+U and typed again. Each typed copy had in fact been accepted, so each one ran.

## Fix

First patch (`b75b7b5`): the notice had to be new or the draft still in the input box. It still let pane text drive a retype, against the durable-ack decision
(`docs/refactor/durable_ack_p0.md`: attempt once, report the truth; "pane string matching alone must never decide a user-visible delivery failure once submission may have occurred"; "Blind Enter retries ... risked duplicate submissions").

Final fix (`04645b8`): `museSendPrompt` submits once. When the notice shows after Enter it is logged (`[MUSE_SUBMIT_NOTICE]`) and nothing is cleared or retyped; `museWaitIntake` observes the native
`session.jsonl` for the `runtime.user_intent.accepted` record (60 s, 5 min when Muse has started a session) and reports "delivery unconfirmed" if it never comes. The retype loop, its counter and the pane-count helpers are gone.
Test `TestMuseSendPromptDoesNotRetypeAfterARefusalNotice`: a stand-in `tmux` that shows the notice after Enter must see the message typed once, submitted once, no clear (the old code loops, waits and retypes).

## Left

- Deploy and check on Excellence: one message shows once in the Muse terminal and answers in seconds, not minutes.
- Why the first submit after a resume is refused for up to about 90 s ("another run is still starting"): cold TUI start or the resumed run re-attaching; not measured yet. The wrapper's update-check stamp is not the cause (PLAT-417 now sets `MUSE_NO_AUTO_UPDATE=1`).
- Muse already has the earlier duplicates in its saved conversation for the owner's test chat.
- If Muse truly refuses a message while a resumed run is still starting (the text then stays in its input), no intake record arrives and the turn ends as unconfirmed; a structured "Muse is ready" record to wait on before submitting has not been found (follow-up).
- Other CLIs: their pane-text use has not been audited for retries yet; their submit paths are covered by the same durable-ack rule in the document.

## Register notes

[PLAT-422](plat-422.md), P1, fixed on `main`; deploy in progress. A refusal now needs a new notice or the draft still in the input box.
