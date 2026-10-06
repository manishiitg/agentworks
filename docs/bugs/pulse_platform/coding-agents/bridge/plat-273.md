[← platform / coding-agent-bridge](index.md)

# PLAT-273 — Pi CLI live input was reported failed (409) while Pi had accepted it, and every retry layer then re-sent the message

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | coding-agents |
| Area | bridge |
| Summary | P1 — a chat message sent into a long Pi session is shown to the |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `fixed` — build/test verified with fail-before/pass-after tests in `multi-llm-provider-go`; needs a server rebuild+restart to reach the running dev server |
| Last synchronized | `2026-09-03` |

- **Priority:** P1 — a chat message sent into a long Pi session is shown to the
  operator as `Conversation Error / Request failed with status code 409`, is
  not recorded in the chat, and Pi's reply is never surfaced — while Pi
  actually received the message **four times** and answered. Repeats on
  every send once a session is large enough.
- **Owner:** `multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`
  (`ensurePiInputSubmitted`, now `ensurePiInputSubmittedWith`).
- **Origin:** live, confida-login workflow-builder chat on pi-cli
  (`session 7d849018-2a54-44e7-a392-b8b824eccd09`, 2026-09-03 13:21 and
  13:24 IST). Not a Pulse finding; reported directly by the operator.
- **Numbering note:** first filed as PLAT-272; renumbered on merge because a
  concurrent session filed the workflow-secrets ticket under that id.

## What the operator saw

Two sends, `what does the step validate browser eviance nad api satefy do`
(13:21:27) and `what the validate browser evidance do?` (13:24:03). Both
returned `409` from `POST /api/sessions/{id}/live-input` and again from the
`POST /api/query` fallback (75-byte `Live input unavailable: …` body). The UI
rendered a Conversation Error. The chat showed neither the message nor an
answer.

## What actually happened (from the logs)

`server_debug.log`:

```
13:21:29 [LIVE INPUT] Durable session delivery failed …: failed to submit live input to pi-cli:
         Pi input remained in the prompt after submit retry; trying retained-terminal recovery before rebuilding
13:21:31 <-- POST /api/sessions/…/live-input status=409
13:21:33 [QUERY->LIVE] Durable session delivery failed …: (same)
13:21:34 <-- POST /api/query status=409
```

Pi's own marker stream for the same session (`markers.jsonl`, the bundled
extension's `message_end{role:"user"}` events):

```
13:21:28 agent_start
13:21:28 message_end user  what does the step validate browser eviance nad api satefy do
13:21:40 message_end user  what does the step validate browser eviance nad api satefy do
13:21:52 message_end user  what does the step validate browser eviance nad api satefy do
13:22:01 message_end user  what does the step validate browser eviance nad api satefy do
13:22:09 agent_end
13:24:03 agent_start   (second attempt: same shape, 4 copies, agent_end 13:25:40)
```

Pi's session file holds the reply (`2026-09-03T07:55:40Z`, "**Validate
Browser Evidence** checks the browser test results to make sure…", 735,872
tokens of context, 732K cache-read). So: Pi took the message on the first
keystroke, the adapter said it hadn't, and the four delivery layers above
(durable `Session.Send`, retained-terminal recovery, `/api/query`'s
`QUERY->LIVE` durable send, its retained-terminal recovery) each typed it
again. Four user messages per attempt now sit in that session's context.

## Root cause

`ensurePiInputSubmitted` decided "submitted" purely from the tmux pane, with
a 1.5 s budget:

- submitted if the status line is present and not `idle`, **or** if the
  draft is no longer visible in the 24 lines above the status line;
- otherwise one recovery `Enter` at 250 ms, then `Pi input remained in the
  prompt after submit retry` at the deadline.

On a ~735K-token session both signals are wrong for longer than 1.5 s:

1. Pi's status line keeps saying `💤 idle` until the first model event. At
   that context size the first event takes several seconds (the earlier
   successful sends on the same session, 12:49–13:08, confirmed in ~300 ms
   because the pane flipped fast enough).
2. Pi echoes the submitted message into the transcript directly above the
   editor; `piPromptEditorRegion` treats the 24 lines above the status line
   as "the editor", so the echo matched the draft and `piPaneShowsPromptDraft`
   stayed true.

Idle + draft-visible for 1.5 s → false negative. The error is returned as a
plain failure, so `handleLiveInputMessage` / `tryDeliverQueryAsLiveInput`
fall through to the cold-restart recovery path and re-send via tmux, and the
frontend's `/api/query` fallback repeats the whole chain.

## Fix (`multi-llm-provider-go`, commit `232ac98`)

The bundled marker extension already reports every message Pi accepts
(`message_end{role:"user",text}`), whether it starts a turn or is queued /
steered into a running one. That is authoritative, so it is now checked
first:

- `sendPiInputToTmuxUnserialized` snapshots the marker file offset **before**
  typing, and passes `markerPath`/offset through to the confirmation. Only an
  acknowledgement written after this send counts — an identical earlier
  message in the stream cannot confirm a new one.
- `ensurePiInputSubmittedWith` polls the marker stream every 50 ms alongside
  the existing pane heuristics; a matching `message_end` (whitespace-
  insensitive; prefix match for ≥64-rune messages) returns success
  immediately. With a marker file the settle budget is 6 s
  (`piPromptSubmitMarkerWait`); without one, behaviour is exactly as before
  (1.5 s, pane-only), so panes with no marker file are unaffected.
- The recovery `Enter` is unchanged (harmless on an already-empty editor).
- Public surface unchanged: `SendPiInteractiveInput` now passes the live
  session's marker path; the initial-prompt send does too.

## Verification

`pkg/adapters/picli/picli_submit_confirmation_test.go`:

- `TestIdlePaneWithDraftFixtureReadsAsUnsubmitted` pins the exact pane state
  from the incident (idle status line, draft echoed with a cursor cell) as
  something the pane heuristics alone call "not submitted".
- `TestEnsurePiInputSubmittedTrustsMarkerAcknowledgement` — that pane, plus a
  `message_end user` marker arriving 200 ms after the send → confirmed in
  0.20 s (fail-before: 1.5 s then `remained in the prompt`).
- `TestEnsurePiInputSubmittedIgnoresAcknowledgementsBeforeTheSend` — an
  identical message acknowledged before the offset must not confirm; still
  errors, exactly one recovery Enter.
- `TestEnsurePiInputSubmittedWithoutMarkersKeepsPaneVerdict` — busy status
  line still accepted; idle+draft with no marker file still rejected.
- `TestPiMarkersAcknowledgeUserMessage` — matching rules.
- Existing real-tmux `TestEnsurePiInputSubmittedSendsRecoveryEnter` and the
  full `picli` suite pass; `golangci-lint` 0 issues.

## Rollout note

The fix lives in `multi-llm-provider-go`, consumed by `agent_go` through the
local `replace`. The running dev server (a `go run` build from 08:29 on
2026-09-03) still has the old check; it needs a rebuild and restart. The Pi
tmux pane survives a server restart (cold-restart compatibility in
`deliverRetainedMainTerminalInput`), so restarting does not lose the session.

## Follow-ups (not changed here)

- The 4× duplicate delivery is a property of the retry ladder in
  `cmd/server` (durable send → retained-terminal recovery → `/api/query` →
  same again). With the false negative gone it only fires on genuine
  failures, but a genuine "unconfirmed" send is still re-typed by each layer.
  A distinct unconfirmed error kind that the upper layers treat as
  "check the marker stream before re-sending" would close that.
- `piPromptEditorRegion`'s 24-line window includes transcript echo; a
  tighter editor boundary (Pi's user-message block is rendered with the
  `userMessageBg` style and OSC-133 zone markers) would let the pane
  heuristic stand on its own for panes without a marker file.
- The confida-login session now carries four copies of each of those two
  messages in Pi's context; harmless but wasteful (~735K tokens, most of it
  cache-read).

## 2026-09-12 — Related Cursor false-409 fix

[PLAT-314](plat-314.md) tracks the distinct Cursor empty-composer/footer matching
failure, fixed in provider `570ede6` and deployed to RTS. This cross-reference
does not claim a new live Pi acceptance run for the original incident.
