# Durable submit acknowledgement: file-ack P0 + pane fast-confirm P1

> Completion, final answer, steering and background subagents (all CLIs, current):
> [../core/coding_cli_turn_signals.md](../core/coding_cli_turn_signals.md).

**Status: all five providers implemented 2026-09-19/20.
Decision updated 2026-09-20: establish explicit P0 and P1
contracts. Durable correctness and pane-only safety blockers are
P0; secondary pane interpretation and terminal experience are P1.
Live P0 green for all five providers; live server↔chat e2e PASS
for codex/pi/muse/Claude (probe: /tmp/durable-e2e/probe.py).
Cursor live P0 passed 2026-09-20 using the RTS deployment key;
its server↔chat e2e remains pending. 2026-09-20 fix: durability receipts are
now consumed on every ingestion path including durable restore
(see "Restore-path receipt consumption" below).**

**Current review disposition: changes required (fixes implemented, pending
re-review).** The follow-up code review below reproduced three remaining
correctness bugs after the FIFO-receipt and append-then-apply fixes; the
"Follow-up P1a/P1b/P2 resolutions" section records the fixes with committed
regression coverage. Historical live passes do not cover those cases, and the
reviewer has not yet re-verified.

### AGY onboarding extension (2026-09-27)

AGY is the sixth coding CLI with a provider-side durable live-input receipt.
The tmux send returns quickly; the existing server watcher then confirms the
exact message against a new type-14 user step in AGY's conversation SQLite.
The watcher snapshots the pre-send step index, and repeated identical sends
require separate user rows. The real CLI test
`TestAgyCLIRealDurableAckContract` passed for two identical retained turns.
The isolated full application P0 runner and retained Chat live check passed
with AGY 1.2.12 in Gemini API-key mode. This extends the live receipt proof
to six providers; it does not revise the earlier historical five-provider run.

### RTS rapid-input follow-up (2026-09-21)

Production session `c7d58080-0058-4f6f-a394-feea651f405b` clarified a UI
latency case that must not be misclassified as slow durable acknowledgement.
Four busy Cursor `/api/query` submissions completed in 15.0–19.6 seconds. The
delay occurred before `sent_to_cli`, while Cursor's adapter waited for a safe
idle or “Add a follow-up” composer; the asynchronous `store.db` durability
watch was not blocking HTTP. The frontend then compounded that legitimate
pending-delivery interval by creating later optimistic rows inside its own
serialized request lane, making rapid messages appear lost.

The frontend fix stages each local user row before that lane, while preserving
the existing meanings of the receipts: no tick before provider acceptance,
single tick after `sent_to_cli`, double tick after exact durable proof. An
accept-now/deliver-later server prototype was rejected and removed because it
would violate this document's attempt-once/report-truth decision. Cursor's
pending server↔chat e2e must now include rapid identical and distinct sends,
assert immediate pending-row visibility, FIFO provider delivery, no premature
single tick, and one distinct durable confirmation per accepted send.

### Muse initial-turn false failure (2026-09-22)

The workflow builder received the same auto-notification repeatedly while its
Muse adapter reported `muse TUI never took in the prompt after submit`. The
native `session.jsonl` actually contained `runtime.user_intent.accepted` for
the exact text at 09:33:28 IST, after the 09:33:16 turn began. The discovery
anchor cut the prompt at 120 **bytes**, splitting the UTF-8 em dash in
`completed — status`; JSON matching could never find that invalid fragment.
Blind Enter retries during the 60-second intake wait then risked duplicate
submissions. This was a false delivery error, not a failed Muse model turn.

The Muse initial-turn correction is in `multi-llm-provider-go`:

* A pane submit failure is provisional. The turn checks native intake before
  reporting a delivery error; durable intake overrides a pane mismatch.
* The intake wait is observe-only. Neither it nor the fast submitter repeats
  Enter after an uncertain submit. A missing durable record at the budget
  boundary remains an *unconfirmed delivery* error, not proof tmux lost bytes.
* Discovery uses a valid UTF-8 prefix and a new timestamped
  `runtime.user_intent.accepted` record containing that text. A file mtime,
  old identical prompt, assistant echo, or nested subagent log cannot confirm
  the new send.

This initial-turn intake check is distinct from the live-input receipt watcher
below. The live path still uses the pane for a fast single tick and the native
log for the durable double tick; its pane-failure arbiter was already
observe-only. The live watcher now requires an exact
`runtime.user_intent.accepted` text match above the send's pre-submit sequence:
an assistant echo or the paired queue event cannot masquerade as another
accepted send. The invariant for both paths is that pane string matching alone
must never decide a user-visible *delivery failure* once submission may have
occurred. Focused SDK regressions are green. Broad local runs had unrelated
CLI/environment-sensitive failures (Pi wedged-process PID fixture, Claude
paste-chip settle, Muse MCP-mount probe, and a Muse resume session dying before
prompt readiness). The Muse resume test passed when rerun alone. This change
has not yet been re-certified against a live Muse workflow run.

### Cross-provider submit-error audit (2026-09-22)

The same P0 rule now covers initial turns in Codex, Pi, Claude Code, Cursor,
and Muse. Each adapter snapshots its provider-specific pre-send boundary and,
when its fast pane submit check errors, waits **observe-only** for a new
provider-owned user-acceptance record before returning that error. A matching
rollout user row (Codex), `message_end` user marker (Pi), transcript user row
(Claude), `user_query` store row (Cursor), or `user_intent.accepted` row
(Muse) overrides the pane error. No match by the provider's budget means
*delivery unconfirmed*, not proof that tmux dropped the bytes. Pi's retained
new-turn path receives the same fallback.

Claude and Cursor live-input sends also now invoke their existing durable
watchers when the fast send returns an error; Codex, Pi, and Muse already had
live-input arbiters. Provider-specific regression tests cover a pane error
with an accepted durable record. Existing automatic Enter-recovery behavior
in the non-Muse adapters remains separate from this error-arbitration change:
its draft-only safety and duplicate risk still need live review before calling
the full transport policy re-certified. The changes are local and have not
yet passed live provider/server↔chat re-certification.

### Production dependency parity (2026-09-22)

The production agent Dockerfile removed the local sibling-repository replaces
and then built the versions pinned in `agent_go/go.mod`. That pin was
`multi-llm-provider-go` revision `36f1e19` from 2026-09-19, while local tests
used the sibling repository's `main`. Production could therefore omit newer
submit/Enter and durable-ack fixes even after those fixes passed locally.

The production Docker build now resolves both `mcpagent` and
`multi-llm-provider-go` from `main`, matching local development. The refs are
explicit Docker build arguments defaulting to `main`; an exact commit may be
supplied only for an intentional rollback or bisect. The deterministic Coding
CLI workflow verifies those defaults so a pinned-production/local-main split
cannot be reintroduced silently.

### Double-submit key transport policy (2026-09-22)

Production showed that tmux can accept a submit keystroke while the provider
TUI fails to act on it. Every tmux coding-agent adapter now sends two submit
keys for the initial submission after writing the draft (`Enter Enter` for
Codex, Pi, and Muse; `C-m C-m` for Cursor; `C-e Enter Enter` for Claude Code).
The pair is emitted by one `tmux send-keys` command and is covered by an exact
key-sequence regression in each provider package (`multi-llm-provider-go`
commit `bcaff33`).

This redundancy does not elevate terminal text scraping into the source of
truth. Provider-owned rollout/transcript/marker/database evidence remains the
authoritative durable acknowledgement. Pane matching remains secondary for
blocking-state detection and bounded stuck-draft recovery.

After the 2026-09-22 main pull, `/api/query` owns steer-vs-next-turn routing.
Human submissions now attempt a compatible retained tmux CLI once *before*
the occupied-turn queue. A known missing target falls through to the durable
next-turn queue; an uncertain send never falls through and risks a duplicate.
Claimed queue workers, bot/scheduled/Pulse turns, personal-access-token API
callers, explicit new turns, and synthetic notifications do not take this
steer-first path. The server's live-delivery
deadline is six minutes, beyond the adapters' maximum five-minute durable-ack
budget, so its former 15-second cap no longer truncates the fallback. Request
cancellation can still end an in-flight HTTP send. Chat's Axios transport has
no request timeout (`timeout: 0` in the installed Axios defaults); the Go HTTP
server has no write deadline and the checked-in Caddy configs have no explicit
response timeout. Other deployed ingress/client disconnect behavior is not
verified. A queued
turn's `queued_for_turn` receipt is not a provider intake acknowledgment.

## Problem

A tmux `send-keys` exit 0 only proves tmux accepted the keystroke. Every
CLI adapter therefore confirms submission by string-matching the pane
(draft cleared, activity started, queued text visible). When the pane
*looks* wrong — redraw races, version drift in spinner/progress strings,
ghost/suggestion placeholders, compaction banners, deep scrollback —
the adapters return `failed to submit live input to <provider>` and
`mcpagent` surfaces that straight to the user instead of queueing
(`mcpagent/agent/message_delivery.go`: failed tmux submission is
returned to the caller). False negatives become user-visible errors;
the natural user reaction (retype and resend) creates duplicates.

Meanwhile the pane is also the only fast signal. The provider's own
rollout/transcript/marker file is authoritative for *what* the model
received but lags (milliseconds idle, unbounded while busy), and it
cannot see unsubmitted drafts, modals, trust prompts, or compaction.
Neither signal alone is sufficient.

## Purpose and reliability outcome

The purpose of this split is to make live-input delivery more reliable
by reducing how much correctness depends on interpreting terminal
screenshots. It does not make tmux itself inherently more reliable.
It gives tmux a smaller, clearer responsibility and uses the provider's
durable data for the facts that data can prove.

Target ownership:

* **Tmux transports interaction:** start and retain the CLI process,
  serialize input, paste exact bytes, submit control keys, interrupt,
  and expose the live terminal.
* **Provider JSON/markers/transcript/DB prove acceptance:** the exact
  send-specific durable record produces the double delivery tick and
  is authoritative for whether the provider received the message.
* **Pane inspection handles only pane-only safety states:** trust,
  login, approval, blocking compaction, and an exact draft visibly
  stuck in the editor.
* **Structured provider data owns conversation state:** final answers,
  restoration, resume identity, and auditable history should not be
  reconstructed from hard-wrapped terminal text.

This improves platform reliability in two concrete ways:

1. Provider TUI wording, layout, animation, wrapping, or repaint timing
   can no longer turn a successfully accepted message into a false
   delivery failure.
2. Shared tmux primitives for session ownership, input ordering, paste,
   keys, and bounded recovery replace duplicated provider-specific
   machinery, so one transport fix applies consistently to every CLI.

The resulting rule is: tmux answers **"did we transport these bytes to
this live process?"**; the provider's durable store answers **"did this
exact send become part of the conversation?"** Pane parsing must not
silently substitute for the second answer except for the explicitly
documented safety blockers that have no durable representation.

## Probe evidence (2026-09-19, codex-cli 0.155.0, gpt-5.6-luna)

Hands-on TUI probe in an isolated tmux server, adapter-exact
load-buffer/paste-buffer/Enter sequence, scratch workdir:

* No rollout file exists before the workspace trust prompt is
  accepted. Trust stays pane-only.
* Rollout rows for a turn: `session_meta` (cwd, cli_version,
  session_id), `task_started`, `turn_context` (turn_id),
  `response_item` user row with the **exact message text**,
  assistant row with `phase=final_answer`, `token_usage_record` +
  `token_count`, terminal `task_complete {turn_id,
  last_agent_message, duration_ms}`.
* Idle submit to user row in file: ~0.2s warm, ~1s cold (first turn).
  Short turn to `task_complete`: ~4-5s.
* Mid-turn steer: native queue confirmed
  (`Messages to be submitted after next tool call` + `↳ <text>`).
  The queued user row landed in the **same turn** (no new
  `task_started`) **17s after Enter**, folded into the extended
  turn; one `task_complete` closed it; the model obeyed.
* Mid-turn pane reproduced the contract regression case: a
  `Planning ... (esc to interrupt)` indicator above a
  ready-looking `Ask Codex to do anything` footer.
* Chromes to keep ignoring: update banner, `model: loading`
  splash, per-turn `done <time>` lines.

Consequence: a file-only submit confirmation with an 8s budget
would have falsely failed a steer that worked perfectly. For
busy input the pane queue state is the fast confirmation and
the file is the eventual durability proof.

## Design

### Three-state fast outcome

Phase 1 (pane, existing budgets, resubmit Enters only here):

1. **Submitted** (draft cleared + activity/new turn) → success
   immediately. No file wait on the caller path.
2. **Queued** (native queue text visible) → held, not submitted.
   Observe-only from here: never send keys into a queued
   composer (Enter/Esc semantics on queued state are
   version-dependent).
3. **No signal** (draft sitting, no transition, no queue) →
   fall through to phase 2.

### Phase 2: observe-only file arbiter (per-provider budget)

Runs only when phase 1 does not report Submitted. Polls the
provider file for the user row matching this send (offset
snapshot taken before paste + exact/prefix text match, so an
identical earlier message cannot confirm a new one):

* Row appears → success. The pane misread (drift, blind
  scraper, slow TUI) is logged as provider-drift signal.
* Budget expires with the pane still positively showing the
  message queued → **accepted-but-unflushed**: held by the
  CLI, durability unconfirmed. Not an error; reconciled by
  the turn's terminal marker / next-turn validation.
* Budget expires with no signal in either channel → real
  error with pane snapshot, as today.

The budget is named and env-tunable per provider (same pattern
as `CODING_SDK_TMUX_PROMPT_WAIT_SECONDS`-style settings);
unit tests use injected probes, never sleeps. Defaults:
codex 180s (raised from 60 after a mid-turn steer landed at
97s — the watch is secondary, so the generous budget only
delays false negatives), cursor 180s (generous: busy path
still unknown, revisit after live P0), pi/muse/Claude 60s;
all clamped 5–300. Worst-case caller block lands only on the
path that fails today in ~8s, and most of those become
successes.

### Background audit (success path)

After a phase-1 Submitted, a bounded background check logs
(never errors) if the user row never lands. Converts silent
drops into observable drift without touching caller latency.

### Chat ticks (WhatsApp-style receipts)

* `✓` single = HTTP fast ack (pane confirm). Data already
  exists in `LiveInputResponse`.
* `✓✓` double = async durable confirm via new SSE event
  `live_input_confirmed {message_id, outcome, proof_source,
  latency_ms}`.
* `✓+clock` = accepted-but-unflushed. `!` = failed.
* Scoped to tmux live-input rows
  (`source: 'coding_agent_live_input'`) only.
* SSE, not blocking HTTP: `liveCodingAgentInputTimeout` is
  15s and P0 requires ack not to wait behind the session
  lane. Frontend upgrades the row matched by
  `metadata.message_id` (same pattern as the HTTP ack);
  reconnects re-query pending ids.

### Certification mapping

Decision updated 2026-09-20: create two real certification
levels. Classify behavior by user impact, not merely by whether
the implementation touches tmux.

#### P0: critical execution contract

A provider cannot be production-ready unless every claimed P0
contract passes:

* Launch the CLI and bind it to the correct owner/session.
* Send the exact message without truncation or interleaving.
* Preserve ordering for rapid messages and concurrent input
  sources through the per-session broker.
* Detect pane-only trust, login, approval, blocking compaction,
  and visibly stuck-draft states before input is lost; perform
  only bounded recovery for the exact demonstrably stuck draft.
* Durably confirm the exact send through the provider JSON,
  marker stream, transcript, or DB.
* Never confirm repeated identical text using an older send's
  receipt.
* Deliver mid-turn steering and bounded interrupt correctly.
* Extract the correct final response from structured provider
  data rather than hard-wrapped pane text.
* Resume the correct native conversation, isolate concurrent
  sessions, and clean up only the owning process/session.

These remain P0 because failure can lose or duplicate work,
cross session boundaries, or return the wrong result. Pane checks
for blockers also remain P0 because no provider file exists yet
at some trust/login gates and persisted output cannot prove that
a draft is still trapped in the editor.

#### P1: secondary tmux and terminal contract

P1 covers behavior whose failure harms responsiveness,
observability, or terminal presentation while the underlying
conversation remains correct:

* Fast pane acknowledgement and the first delivery tick.
* Spinner/progress interpretation and queue-banner presentation.
* Exact terminal colors, formatting, scrollback, resize, and
  reconnect fidelity.
* Inactive terminal previews and friendly pane diagnostics.
* Continuous terminal recording and latency targets.
* Supplemental pane diagnostics when durable confirmation is
  delayed, without making broad spinner/activity guesses part
  of the correctness verdict.

For example, when `store.db` confirms that Cursor accepted the
message, failure to recognize a changed Cursor spinner is P1.
The work was not lost and the provider should remain available.

#### Required gate structure

Create and maintain two explicit runners:

* `run-coding-cli-p0.sh`: correctness, isolation, durable
  acceptance, final response, resume, interrupt, and blocker
  safety. A claimed P0 failure blocks provider/release readiness.
* `run-coding-cli-p1.sh`: terminal UX, fast indicators,
  formatting, reconnect, diagnostics, and latency. A P1 failure
  produces a compatibility report and may disable only the
  affected UI enhancement.

Provider contracts should expose P0 capabilities, P1
capabilities, and known P1 gaps. This replaces the 2026-09-19
decision not to create a P1 gate. The current implementation has
not yet completed this split: normal send paths still use broad
pane-based success polling, so moving secondary pane heuristics
behind P1 remains follow-up work.

## Provider order

1. **Codex** (this doc): best markers, oracle pattern
   half-exists, representative JSONL playbook. Done.
2. **Pi**: `picli_durable_ack.go` (marker-offset receipts,
   `PI_DURABLE_ACK_SECONDS` budget, `Steering:`-keyed queue
   check — the positional matcher has no Pi equivalent, so
   the verdict keys on the native queue banner plus
   draft-gone-and-active); `piMarkersAcknowledgeUserMessage`
   now delegates to the timestamp-returning `piUserAckMarker`
   (same match, single source); `CertDurableAck` registered;
   live P0 green (busy steer acked 8.0s, idle 44ms). Server
   dispatch + watch test added; frontend untouched (generic
   path). Done.
3. **Muse**: `musecli_durable_ack.go` (sequence-scoped
   receipts on the pooled entry under the existing pool lock,
   `MUSE_DURABLE_ACK_SECONDS` budget, `Queued input`-keyed
   queue check + activity fallback — `museTUIAtPrompt` stays
   true while streaming so it cannot serve as the busy
   signal); send path was fully pane-only, so this is the
   highest-value ack of the rollout; `CertDurableAck`
   registered; live P0 green (busy steer acked 1.2s, idle
   1.2s — Muse records intake same-second even when busy).
   Server dispatch + watch test added; frontend untouched.
   Done.
4. **Claude**: intake-row + queue-drain verdict over the
   tmux transcript (see "Claude durable-ack P0" below). Live
   P0 green (busy steer 8s, idle 300ms) + live e2e PASS
   (fast ack 1.2s, durable 27.7s). Done.
5. **Cursor**: full file-ack after all — the probe showed
   idle `store.db` commit in ~9.5s with a stable WAL-safe
   reader, so the "async commit" fear did not hold for the
   measured path. Ref-identity scoping (no blob timestamps).
   Implemented + unit-tested; live P0 passed 2026-09-20
   (busy steer 37.5s, idle follow-up 8.4s). Server↔chat e2e
   remains pending. See "Cursor durable-ack P0" below.

## Codex change list

SDK (`multi-llm-provider-go`):

* [x] `pkg/adapters/codexcli`: pre-paste rollout offset
  snapshot per send (`codexcli_durable_ack.go`); phase-2
  user-row watcher, observe-only, `CODEX_DURABLE_ACK_SECONDS`
  budget (default 180, clamped 5–300); `accepted-but-unflushed`
  outcome; message-text keyed queue check
  (`codexPaneShowsQueuedMessage`) because the positional
  matcher goes blind under the repainted ready footer.
* [x] Deterministic unit tests (`codexcli_durable_ack_test.go`):
  offset/timestamp scoping, malformed-line tolerance, long
  prefix match, poll confirmed/unflushed/failed/cancel,
  receipt stash/peek/cap/TTL.
* [x] Live P0: `TestCodexCLIRealDurableAckContract`
  (busy steer durably acked in 8.1s behind an 8s slow tool,
  steer obeyed; idle follow-up acked in 0.3s); `CertDurableAck`
  registered P0 via `SupportsDurableAck`.

Server (`mcp-agent-builder-go/agent_go` + `mcpagent`):

* [x] `llmtypes.DurableAck` envelope; `llmproviders` +
  `mcpagent/llm` await wrappers (same pattern as `Send*`).
* [x] Bounded watcher after fast ack
  (`live_input_durable.go`), spawned from the single
  `recordLiveCodingAgentUserMessage` choke point so all
  delivery paths (running agent, durable session,
  cold-retained, BG steers) are covered; emits
  `live_input_confirmed` SSE via the event store (replay
  and polling fallback included, no new endpoint needed);
  `QueryResponse.message_id` added for the query path.

Frontend (`mcp-agent-builder-go/frontend`):

* [x] Receipt `confirmation` stage in `liveInputReceipt.ts`
  + pure upgrade/parse/stamp helpers + tests.
* [x] Tick render in `UserMessageEvent.tsx` (✓/✓✓/✓…/!)
  + tests.
* [x] Upgrade handler at the top of `processEventsResponse`
  (covers SSE, polling, reconnect replay) + suppressed-echo
  identity transfer so query-steered rows can match their
  receipt.
* [x] 2026-09-20: receipt consumption on every remaining
  ingestion path — `splitLiveInputConfirmations` /
  `resolveLiveInputConfirmations` helpers wired into durable
  restore (`sessionRestore.ts`, incl. live-tail identity
  transfer onto content-only durable rows), workflow
  hydration (`WorkflowLayout.tsx`), and older-page backfill
  (`ChatArea.tsx`); receipts never enter any timeline. See
  "Restore-path receipt consumption" below.

## Open questions

* Decided: budgets are per-provider, env-tunable, clamped
  5–300: codex 180 (raised from 60 after the live e2e saw
  a mid-turn steer land at 97s — the watch is secondary,
  so the generous budget only delays false negatives),
  cursor 180 (revisit after live P0), pi/muse/Claude 60.
* Decided: `live_input_confirmed` reuses the event-store
  `PollingEvent` envelope — SSE, polling fallback, and
  reconnect replay all carry it with no new endpoint.
* Remaining: accepted-but-unflushed is terminal per watch
  in v1 — if the row lands after the budget, nothing
  promotes the tick to `✓✓`. Options: extend the watch on
  unflushed, or promote on the turn's terminal marker
  (an answered turn proves intake).

## Live server↔chat e2e (2026-09-19)

Harness: isolated workspace API (:18081, temp docs dir) +
agent server (:18943); per provider: real `/api/query`
turn → poll `can_steer` → POST live-input steer →
assert `sent_to_cli` fast ack → poll `?since=0` history
for `live_input_confirmed` → assert the same bytes on
the SSE stream. Probe: `/tmp/durable-e2e/probe.py`.

* [x] codex-cli / gpt-5.6-luna: fast ack 1.3s,
  durable `confirmed` at 54s, proof = session rollout
  jsonl, SSE wire verified, wire shape matches
  `readLiveInputConfirmation` field-for-field.
* [x] pi-cli / google/gemini-3.8-flash: fast ack 6.3s,
  durable `confirmed` at 6.3s, proof = markers.json,
  SSE wire verified.
* [x] muse-cli / muse-spark-1.3-contributor: fast ack
  1.2s, durable `confirmed` at 1.2s, proof = session
  jsonl, SSE wire verified.

Findings (evidence-backed, not yet fixed):

* Codex 60s default budget was too tight for mid-turn
  steers. One run timed out (`failed` after 60s) while
  the rollout recorded the message at 97s — a false
  negative, delivery had succeeded. Fixed: default
  raised to 180 (`codexDurableAckBudgetDefault` +
  `TestCodexDurableAckBudget`).
* `can_steer` went true before the CLI's interactive
  session registered: a muse steer at t+4s 409'd with
  `delivery_uncertain` ("no active Muse interactive
  session registered"). Fixed: `canSteerSession` now
  requires transport registration via per-provider
  `InteractiveSessionRegistered` (codex/pi/muse —
  all three race the same way, muse just lost it).
  Idle foreground-session completion keeps the old
  transport-agnostic liveness (`foregroundTurnAlive`)
  so a registering turn never reads as idle.
* Dropped idea (2026-09-19): collapsing query/steer
  into one async accept-then-deliver sender. Rejected:
  uncertainty is irreducible on short timescales
  (transcript lag ~60–100s), so server retry can't
  check-before-resend quickly; attempt-once-report-truth
  stays the control-plane contract.

## Claude durable-ack P0 (2026-09-19)

Live probe (haiku, persistent tmux): intake row at +3.2s,
mid-turn steer `enqueue` row at +500ms with full text.
Two drain paths found live:

* steer during text generation: `dequeue` then its `user`
  row (~7s).
* steer during a tool call: `remove` with full text, NO
  user row ever — the CLI folds the text into a
  system-reminder with the tool result. An
  instruction-styled steer was then rejected by the model
  as injection-like even though delivery was confirmed;
  benign follow-up phrasing is obeyed (proven by the
  pre-existing queue test, re-run green).

Verdict rule: `confirmed` = user row OR queue drain
(remove with our text, or dequeue after our enqueue —
dequeue rows are contentless so the match is positional).
`accepted_but_unflushed` = enqueue with no drain at
budget expiry. Budget `CLAUDE_DURABLE_ACK_SECONDS`,
default 60 (flush observed at 7–8s; a steer held past a
long turn reports unflushed, truthfully).

* [x] `claudecode_durable_ack.go`: arbiter + stash/peek +
  `AwaitClaudeInputDurable` + `InteractiveSessionRegistered`
  (mirrors the send-path owner registry).
* [x] Send-path stash in `SendClaudeCodeInput`.
* [x] Root `AwaitClaudeInputDurable`, contract flag,
  `CertDurableAck` entry + `TestClaudeCodeRealDurableAckContract`.
* [x] mcpagent wrapper, server watcher switch, steer-gate
  readiness case for `claude-code`.
* [x] Live P0: busy steer confirmed in 8s (model obeyed),
  idle follow-up in 300ms.
* [x] Collateral: two server tests asserted total event
  count 1 where the watch now races a second event;
  they filter to `user_message` rows. `canSteer` precondition
  tests simulate the registered transport via hook.
* [x] Live server↔chat e2e (2026-09-19): fast ack 1.2s
  `sent_to_cli` → durable `confirmed` at 27.7s, proof =
  session transcript, SSE wire verified, mid matches.

## Cursor durable-ack P0 (2026-09-19, live-verified 2026-09-20)

Proof is the session `store.db` (`~/.cursor/chats/<md5(cwd)>/
<agentId>/store.db`): blobs carry no per-message
timestamps, so ref-identity scopes one send — the pre-send
snapshot of latest-root refs plus exact `<user_query>`
text. Confirmed = user_query row under a new ref.
Unflushed = pane still shows our text in the native
follow-ups queue at budget expiry (matcher shaped by
helpers + fixtures, NOT yet by a live pane).

Initial probe evidence (auto model, persistent tmux): idle steer
committed its row in ~9.5s; reader stable. The authenticated
P0 contract was subsequently run with the RTS deployment-managed
`CURSOR_API_KEY`: the busy steer committed to `store.db` in 37.5s,
affected the active turn, and the idle follow-up committed in 8.4s.
Budget
`CURSOR_DURABLE_ACK_SECONDS` default 180 (generous:
secondary, while the live busy path observed 37.5s; revisit
after the server↔chat e2e).

* [x] `cursorcli_durable_ack.go`: arbiter + stash/peek +
  `AwaitCursorInputDurable` + `InteractiveSessionRegistered`.
* [x] Send-path stash in `SendCursorInteractiveInput`.
* [x] Root export, contract flag, `CertDurableAck` entry +
  `TestCursorCLIRealDurableAckContract`, mcpagent wrapper,
  server watcher + steer-gate cases + routing test.
* [x] Unit tests green (real-schema sqlite fixtures).
* [x] Live SDK P0 (2026-09-20):
  `go test ./pkg/adapters/cursorcli -run
  '^TestCursorCLIRealDurableAckContract$' -coding-cli-p0-live
  -count=1 -v` passed in 117.57s. Busy steer durably acked
  in 37.5s and affected the turn; idle follow-up durably
  acked in 8.4s, within the 90s assertion.
* [ ] Live server↔chat e2e: recreate or check in the former
  `/tmp/durable-e2e/probe.py`, add a `("cursor-cli", "auto")`
  entry, and run it against a rebuilt isolated stack. The
  temporary probe is no longer present, so this gate is not
  currently reproducible from the repository. Also confirm
  the queued-followups pane shape and revisit the 180s default.

## Restore-path receipt consumption (2026-09-20)

Live symptom (desktop app, muse-cli): a steered message showed
no tick, and the chat rendered `Unknown Event Type:
live_input_confirmed` with the full receipt JSON. Backend proof
was healthy (durable `confirmed` in 1.2s).

Root cause: receipt consumption existed only in the live path
(`processEventsResponse`). Every restore/hydration path
appended the raw event window, so after any restore the receipt
itself entered the timeline as an unknown row, and the rebuilt
rows — synthesized from role/content-only durable carriers —
lost the tick upgrade (no `message_id` to match).

Fix (frontend, now committed; the implementation notes below describe the
original restore-path change):

* `splitLiveInputConfirmations` /
  `resolveLiveInputConfirmations` in `liveInputReceipt.ts`:
  one choke point that filters receipts out of a window and
  applies them to the rows they name.
* Wired into durable restore (`sessionRestore.ts`: replace,
  live-tail append, existing-tab refresh),
  `transferLiveTailInputIdentity` copies `message_id` +
  delivery facts from volatile live-tail user rows onto
  same-content durable rows (plus any terminal verdict the
  donor already carries), so ticks survive restores.
* Same treatment for workflow hydration
  (`WorkflowLayout.tsx`) and older-page backfill
  (`ChatArea.tsx`), which shared the raw-append gap.

Tests: 3 regression tests in `sessionRestore.test.ts` (real
muse-cli wire shape; failed pre-fix, green post-fix) + a
helper contract test in `liveInputReceipt.test.ts`. Focused
suites 40/40 green, `tsc -b` + eslint clean. Full frontend
suite: 10 failures in 5 unrelated files, all pre-existing
(providers panel, webhooks feed, notifications, history-fetch
args, workflow classification).

## Transcript-view ticks (2026-09-20)

Follow-up gap found live: the terminal transcript renders user
rows itself (`UserTranscriptMessage` in
`TerminalEventTranscript.tsx`) and never received the tick UI —
`DeliveryTick` lived only in `UserMessageEvent.tsx`, which the
transcript does not use for user rows. Log-verified on session
`6de60b72…`: "ok" opened a muse-cli turn 09:55:43, "just
testing" steered live 09:55:47 (HTTP 200 in 253ms), muse
recorded `runtime.user_intent.accepted` — full chain healthy,
nothing rendered.

Fix: `DeliveryTick` + `deliveryTickState`/`deliveryTickTitle`
extracted to shared modules (`DeliveryTick.tsx`,
`deliveryTickState.ts`; `UserMessageEvent.tsx` re-imports,
behavior unchanged), transcript user rows now take `metadata`
and render the tick beside the timestamp in both plain and
collapsible variants. Plain rows render byte-identical to
before. Contract change, surfaced: the old transcript test
asserted timestamp-only rows for accepted delivery statuses;
it now asserts the fast tick (the `sending` case still asserts
timestamp-only). 3 new tests in
`TerminalEventTranscript.deliveryTick.test.tsx` (failed
pre-fix); focused suites 50/50 green, `tsc -b` + eslint clean.

## Review P1/P2 resolutions (2026-09-20)

Fixes for the two "changes required" findings in "Implementation
review" above.

### P1: FIFO-take receipts (SDK, all five adapters)

Each adapter gained `take<Provider>DurableReceipt`: same match
rule as peek, but atomically removes the earliest match under
the session/pool lock. All 7 production peek sites now take
(codex/pi: inline arbiter + Await; Claude/cursor/Muse: Await;
the Muse inline arbiter already takes its baseline as a send-
path parameter, so it never had the bug). `peek` stays for
introspection and existing tests.

Why take is safe: sends are broker-serialized per session, the
inline arbiter runs inside the send (strictly ordered takes),
and watchers take in spawn order under the lock — takes match
sends FIFO. Arbiter and watcher are mutually exclusive per
send (pane-failure path vs fast-ack path), so exactly one of
them takes each receipt; the fallback for a missing receipt is
unchanged.

Regression test per provider (`TestTake*FIFORepeat`): stash
identical text twice with distinct baselines, assert FIFO take
order + third take empty, then prove with the adapter's own
matcher and fixtures that the first row satisfies the first
baseline but cannot satisfy the second. Deterministic, no
sleeps. This implements the P0 contract bullet "Never confirm
repeated identical text using an older send's receipt."

### P2: append-then-apply in the live path (frontend)

`processEventsResponse` applied receipts to the store before
appending its own window, so a same-window row+receipt pair
(reconnect replay) lost the verdict. New shared helper
`appendTimelineAndApplyConfirmations` (sessionRestore.ts):
synchronous append, then apply against the merged store.
The live path calls it for receipt windows; the hot path stays
micro-batched; receipt-only windows and the `!finalTab` early
return still consume receipts against stored rows (control
flow otherwise unchanged). `appendRestoredLiveTail` delegates
to the same helper, so live and restore share one ordering.

Regression tests: 3 helper tests at the seam (same-window
upgrade, receipt-only upgrade, no-op without a match) using
the real wire shape + parser. No ChatArea mount harness
exists in the repo, so the one-line wiring (live path calls
the helper) is covered by inspection, not a mount test.

Out of scope, deliberately: the background-workflow poll
appends raw windows to non-visible tabs, but opening a tab
runs restore (which now consumes receipts), so any receipt
there self-heals and the hot poll path stays untouched.

Verification: new Go tests 5/5 green, incl. a `-race` pass
over receipt stash/peek/take tests in all five adapters;
codex/claude/cursor suites green; pi's wedged-process test
flakes under load (passes serially, shares nothing with this
diff); muse's MCP exec-lane failure reproduces on the
pristine tree (pre-existing). Frontend focused suites green,
`tsc -b` + eslint clean.

## Historical implementation review (2026-09-20, before the resolutions above)

Reviewed against:

* `mcp-agent-builder-go` `251786794`
* `mcpagent` `cb3f8c6`
* `multi-llm-provider-go` `7bd2bc7`

At this review revision, the provider/server/frontend integration and focused
durable-ack tests were present, but two correctness gaps remained. The
"Review P1/P2 resolutions" section records the subsequent fixes; the follow-up
review below describes the remaining issues on the newer revisions.

### P1: repeated identical input can reuse an older receipt

All five adapters retain pending receipts for ten minutes and look one
up using only `(session, trimmed message text)`. The lookup deliberately
does not consume the receipt and always returns the earliest match. For
example, `peekCodexDurableReceipt` returns the first matching entry from
`session.pendingDurable`; Cursor, Pi, Muse, and Claude use the same
pattern.

This does not disambiguate repeated input. If a user sends `yes`, its
durable row lands, and the user sends `yes` again within the receipt TTL,
the second watcher selects the first send's offset/baseline. The first
row can therefore confirm the second send immediately, producing a
false `confirmed` outcome and a false double tick before the second
input is durable.

Required fix:

* Bind the durable wait to a send-specific receipt/token and pass that
  identity through the send/watcher boundary; or atomically consume the
  matching receipt FIFO when `Await*InputDurable` starts.
* Keep the receipt available to the phase-2 arbiter on its mutually
  exclusive failure path.
* Add a regression test for every provider that stashes/sends identical
  text twice and proves the second wait cannot be satisfied by the first
  durable row/ref/sequence.

### P2: same-window live confirmation is discarded

The normal live ingestion path in `ChatArea.processEventsResponse`
extracts `live_input_confirmed` events and applies them to the existing
store before processing and appending the other events in that response.
If a polling/reconnect window contains both a `user_message` and its
confirmation, and the row is not already in the store, the update
matches nothing. The confirmation is then filtered from the timeline
and cannot be replayed, so the row remains at the fast-ack state.

`appendRestoredLiveTail` already implements the correct ordering: split
the window, append timeline events immediately, transfer live-input
identity, and then apply confirmations to the merged rows. The normal
live path should use the same ordering/helper rather than maintaining a
second ingestion algorithm.

Required fix:

* Merge or append the response's timeline rows before applying its
  confirmations.
* Preserve optimistic-row identity transfer before applying the
  confirmation.
* Add a `processEventsResponse` regression test with a matching
  `user_message` and `live_input_confirmed` in one response and assert
  that no receipt enters the timeline and the message reaches the
  terminal confirmation state.

### Review validation

Focused checks passed:

* Durable-ack tests for Codex, Cursor, Pi, Muse, and Claude.
* Server durable watcher routing tests.
* Frontend receipt, restore, message-tick, and transcript-tick suites:
  36 tests passed.

The broader repository suites were not green independently of these
focused checks: tmux sandbox/provider process tests and the mcpbridge
readiness test timed out or failed in the local environment. Those
failures do not exercise the two review findings above.

Historical review disposition: **changes required**. The resolutions above
addressed these findings, but the follow-up review below found additional cases
that still prevent closure.

## Follow-up code review (2026-09-20)

Reviewed after pulling `main`:

* `mcp-agent-builder-go`: `35ad24be3`
* `multi-llm-provider-go`: `6ef98fb` (includes FIFO-take receipt fixes)

Scope: the durable-ack contract in this document, adapter proof matching and
timeout handling, server watcher routing, and frontend receipt ingestion.
Disposition: **changes required**. All three findings below remain open; this
documentation update does not fix production code. Source line references are
for the reviewed revisions.

### P1: identical queued sends can still share one durable proof

Source: `multi-llm-provider-go/pkg/adapters/codexcli/codexcli_durable_ack.go`,
`takeCodexDurableReceipt` (lines 165–188) and `AwaitCodexInputDurable`
(lines 465–475).

FIFO consumption removes the earlier non-consuming lookup bug, but it does not
guarantee distinct evidence for each send. If two identical messages are sent
before the first user row is flushed, both receipts can capture the same file
offset. A later matching row can satisfy both receipts' timestamp/offset checks.
The second message can receive a false double tick even though only one user
row has been persisted.

Reproduction: stash two `yes` receipts with the same offset and successive send
times, then provide one user row timestamped after both sends. Consume both
receipts FIFO and evaluate each with `codexRolloutUserMessageSince`. Both match
the single row. `TestReviewIdenticalQueuedSendsNeedDistinctRows` reproduced the
failure. The existing FIFO regression supplies distinct offsets, so it does not
cover delayed persistence across multiple queued sends.

Required fix:

* Associate each send with distinct provider evidence, such as a claimed row
  identity or ordered occurrence, rather than treating a lower-bound offset as
  a unique receipt identity.
* Preserve send identity across the watcher boundary; spawning goroutines in
  order alone does not guarantee the order in which they acquire receipts.
* Add coverage for identical messages queued before any durable row lands,
  including out-of-order watcher scheduling. Audit analogous matchers in the
  other adapters; this reproduction directly exercised Codex.

### P1: expiry-time pane inspection receives an expired context

Source: `multi-llm-provider-go/pkg/adapters/codexcli/codexcli_durable_ack.go`,
lines 347–356. The same expired-context capture pattern is present in Pi, Muse,
and Cursor's durable-ack pollers.

After `<-deadline.Done()`, the arbiter calls `poll.capture(deadline, ...)`.
That context is already expired. Production capture reaches
`exec.CommandContext` through `tmuxexec`, so the final pane read cannot execute.
A message still held in the native queue is reported as failed instead of
`accepted_but_unflushed`, undermining the documented distinction and potentially
encouraging a duplicate resend.

Reproduction: use a capture callback that respects `ctx.Err()` and otherwise
returns a valid queued-message pane. At budget expiry, the callback rejects the
expired context and the poller returns a durable-ack failure.
`TestReviewQueuedExpiryUsesLiveCaptureContext` reproduced this in Codex. Existing
queue-expiry tests use capture stubs that ignore cancellation and therefore pass.

Required fix:

* Respect cancellation of the parent operation, but on an arbiter timeout use a
  separate short, bounded capture context derived from a still-live parent.
* Test queued expiry with cancellation-aware capture implementations in every
  affected adapter.

### P2: applying a receipt drops buffered timeline events

Source: [sessionRestore.ts](../../frontend/src/utils/sessionRestore.ts),
`appendTimelineAndApplyConfirmations` (lines 329–335), and
[useChatStore.ts](../../frontend/src/stores/useChatStore.ts), `setTabEvents`
(line 1282 onward).

The append-then-apply helper reads committed rows and writes them back through
`setTabEvents`. That setter clears pending micro-batch buffers. A tool event
received just before a durability receipt may still be buffered, so applying
the receipt discards it before the scheduled flush. The same concern applies
to receipt-only store updates in the live ingestion path.

Reproduction: with the real chat store, insert a user row, buffer a
`tool_call_start` through `addTabEvents`, apply a matching receipt with
`appendTimelineAndApplyConfirmations`, then advance timers. The user row remains
but the tool event never appears. A temporary frontend regression reproduced
the loss. Existing helper tests mock the store and do not model batch clearing.

Required fix:

* Flush or preserve pending timeline events before applying confirmations, or
  provide an atomic metadata update that does not replace/clear the event queue.
* Cover receipt-only and combined windows using the real store with fake timers,
  asserting that pending tool/progress events survive alongside the tick upgrade.

### Follow-up validation and remaining gates

* Focused non-live durable/FIFO tests passed for all five adapters:
  `go test ./pkg/adapters/codexcli ./pkg/adapters/picli ./pkg/adapters/musecli ./pkg/adapters/claudecode ./pkg/adapters/cursorcli -run 'Durable|FIFORepeat' -count=1`
  from `multi-llm-provider-go`.
* Server durable tests passed: `go test ./cmd/server -run 'Test.*Durable' -count=1`
  from `mcp-agent-builder-go/agent_go`.
* Existing frontend receipt and restore suites passed: 34 tests across
  `liveInputReceipt.test.ts` and `sessionRestore.test.ts`.
* Three temporary regression tests failed on the intended invariants, confirming
  the findings above. They were removed after reproduction and are not committed
  regression coverage. No production code was changed.
* Live provider certification and live server↔chat delivery were not rerun.
  Cursor's pending server↔chat gate and the unfinished P0/P1 runner split remain
  separate outstanding work, as documented above.

Passing the existing focused suites is not sufficient to close the feature.
Fix the three reproduced cases and retain regression coverage before sign-off.

## Follow-up P1a/P1b/P2 resolutions (2026-09-20)

Fixes for the three open follow-up findings above. SDK changes are in
`multi-llm-provider-go` (all five `pkg/adapters/*/ *_durable_ack.go`
pairs); frontend changes are in this repo. Each fix was verified
failing-first: the new regression test failed on the old code, then
passed after the fix.

### P1a: occurrence-bound receipts (SDK, all five adapters)

Each stashed receipt now carries a 1-based `occurrence`: 1 + identical
unexpired already pending. Stashes run inside the broker-serialized
send, so occurrence is send-ordered. Takes still pop FIFO under the
session/pool lock, and each wait's poll needs the occurrence-th
matching proof row — k takes need k distinct rows however watcher
goroutines schedule. The old "watchers take in spawn order" claim is
removed from all five take doc comments (acquisition order, not send
order); occurrence keeps the binding sound without that assumption.

* Codex/Muse: `codexRolloutUserMessageOccurrenceSince` /
  `museUserAckRowOccurrence` count matches in file order and return
  the occurrence-th; the old first-match functions are occurrence-1
  wrappers, so all existing matcher tests still execute the new code.
* Pi: the poll advances its marker offset across ticks, so the new
  `piUserAckMarkers` (plural) lists every match per chunk and the poll
  accumulates them, confirming on the occurrence-th overall with its
  timestamp. `piUserAckMarker` (first match) now delegates to the
  plural form; the inline 6s submit check is unchanged.
* Claude: occurrence applies to all three matchers — the user-row and
  drain confirm paths plus the enqueue check at budget expiry (one
  enqueue can no longer report two sends as unflushed). Drain events
  (`remove` with our text, `dequeue` after our enqueue) count in file
  order.
* Cursor: `cursorStoreUserQueryCountSince` counts matching new-ref
  rows; the poll confirms at count >= occurrence. Threshold counting
  needs no ref ordering.
* Poll structs gained an `occurrence` field (values < 1 mean 1);
  Await entry points pass the taken receipt's occurrence. No Await,
  stash, take, or matcher signatures changed.

Correction to the "Review P1/P2 resolutions" P1 note: the Muse inline
arbiter did NOT take — it used a send-path baseline parameter, which
left its receipt pending for a later identical send's watcher to take
(a stale-baseline false-confirm hole). `museArbiterAfterSubmitFailure`
now takes its receipt like the Codex/Pi arbiters, falling back to the
caller's snapshot on a take miss.

Regression tests per adapter: `TestTake*OccurrenceRepeat` (stash two
identical receipts on the same baseline, prove occurrence 1 matches
one row while occurrence 2 does not, then satisfy it with a second
row), `TestTake*ConcurrentOccurrence` (two racing goroutines bind
occurrences {1, 2} in either order — the out-of-order scheduling
coverage, `-race` clean), and poll-level occurrence tests (occurrence
2 times out on one row; Pi additionally proves the second marker's
timestamp; Claude proves one enqueue fails rather than double-reports
unflushed). New `TestPollClaudeDurableAck` / `TestPollCursorDurableAck`
suites also lock in basic confirm/cancel behavior those polls lacked.

Known residual: under inverted take order PLUS actual message loss,
two identical waits could misattribute verdicts between the texts
(counts stay correct). Absolute send↔watch binding needs send
identity (e.g. `message_id`) threaded through the Send/Await API —
a larger cross-layer change left as future hardening. Also out of
scope: Pi's inline 6s submit check still first-matches per call
(send1 lost + identical send2 inside its 6s window could falsely
satisfy it); only the durable arbiter/watcher paths use receipts.

### P1b: live capture context on budget expiry (SDK, codex/pi/muse/cursor)

Each affected poll's deadline branch now derives a fresh bounded
context from the poll's parent
(`context.WithTimeout(ctx, <provider>DurableAckCaptureTimeout)`,
5s) for the final pane read instead of passing the expired budget
deadline. Budget-only expiry gets its pane read back (queued verdict
restored); a cancelled parent still fails fast via the existing
`ctx.Err()` return, since the derived context inherits cancellation.
Claude is unaffected — its expiry verdict reads the transcript, with
no capture call.

Regression tests per affected adapter: "expiry capture runs under a
live context" (cancellation-aware stub that rejects an expired
context; failed on old code, unflushed on new) and "parent cancel
fails fast even when queued" (locks in cancellation semantics).
Existing ctx-ignoring queue-expiry tests still pass unchanged.

### P2: atomic receipt patch preserving the micro-batch (frontend)

New `useChatStore.patchTabEvents(sessionId, patch)`: maps the
committed array AND any pending micro-batch buffer through the patch
without `clearPendingEventBatch`, rebuilding the ID/auto-notification
indexes like `setTabEvents` (no cleanup/badge bookkeeping — a 1:1
metadata map never grows the timeline). All three receipt-application
sites use it: `appendTimelineAndApplyConfirmations`, the ChatArea
receipt-only closure, and the live-tail identity transfer. The
receipt loop now lives in one plural
`applyLiveInputConfirmations` helper shared by all three (plus the
pure `resolveLiveInputConfirmations`). Full-timeline replaces
(hydrate, backfill) intentionally keep `setTabEvents` — replacing
the timeline should drop stale buffered events.

Regression tests: new real-store timer suite
`sessionRestore.batchPreservation.test.ts` (seed a committed row,
buffer a `tool_call`, apply a same-window receipt, advance timers —
the tool event survives and the row upgrades; failed on old code).
Mocked-store receipt tests updated to the patch call shape with
identical assertions; no ChatArea mount harness exists, so the
closure rewiring is covered by inspection plus the shared helper.

### Verification

* SDK focused durable suites: 5/5 green, incl. the new occurrence,
  concurrent-take, live-context, and parent-cancel tests.
* SDK full adapter suites with `-race`: codex/pi/cursor/claude
  green; muse has one failure
  (`TestMuseExecLaneMCPMountReachesCLIAndRestores`) that reproduces
  identically on a pristine-HEAD worktree (pre-existing, unrelated
  to durable ack). `go vet` clean on all five adapters.
* Frontend: 41 receipt/restore tests green (incl. the 3 new
  batch-preservation tests), 59 store/ChatArea tests green,
  `tsc -b` clean, eslint 0 errors (2 pre-existing react-hooks
  warnings in ChatArea). `sessionRestore.productFallback.test.ts`
  has 2 failures that reproduce identically on pristine HEAD
  (history-pagination arg drift, unrelated to this diff).
* Not rerun: live provider certification and live server↔chat
  delivery. Cursor's pending server↔chat gate and the unfinished
  P0/P1 runner split remain outstanding, as before.
