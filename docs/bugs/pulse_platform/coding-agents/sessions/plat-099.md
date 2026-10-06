[← coding-agents / sessions](index.md)

# PLAT-099 — Updating a workflow's coding agent leaves live-input routing on the old provider

| Field | Value |
|---|---|
| State | deployed |
| Priority | P0 |
| Product | coding-agents |
| Area | sessions |
| Summary | P0 — a user cannot continue an otherwise healthy live workflow-builder chat after changing its coding agent. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented_pending_live_reverify` — Confida recurrence and submission-route correction deployed; live acceptance pending |
| Last synchronized | `2026-09-18` |

- **Priority:** P0 — a user cannot continue an otherwise healthy live workflow-builder chat after changing its coding agent.
- **Owner:** retained coding-agent live-input routing and continuation metadata.

## Actual defect

The workflow setting was successfully changed from Claude Code to Codex and the
next turn created a real `mlp-codex-cli-*` tmux session. The chat's retained
continuation request still named Claude Code, however. Both `/live-input` and
the `/api/query` fallback called the same delivery helper, which preferred that
stale request field over the live terminal identity. It therefore attempted to
send to Claude, reported that no Claude tmux existed, and rejected the user's
message even though the Codex tmux was live.

This was a backend routing defect, not a failed workflow update or a frontend
submission defect.

## Implemented repair

1. A live tmux provider is now authoritative for direct terminal delivery. A
   stored model is reused only when its stored provider matches that tmux.
2. After workflow manifest and tier resolution, the continuation record is
   rewritten with the provider/model that actually launches the coding CLI.
   The request's original LLM config is copied rather than mutated.
3. If an older terminal cannot identify its provider, stored continuation data
   remains the compatibility fallback.

## Regression coverage

- A stale Claude continuation record plus a live Codex tmux delivers to Codex
  and discards the incompatible Claude model.
- Effective runtime synchronization updates both top-level provider/model and
  the stored primary LLM config without mutating the source request.
- Existing retained-terminal and workflow-continuation tests continue to pass.

## Verification

- Focused `cmd/server` provider-switch and continuation tests pass.
- `go build ./...` passes.
- Live UI re-verification requires the backend to be restarted with this code;
  the server was not restarted during the repair.

## Acceptance

- Change a workflow automation from Claude Code to Codex (or the reverse).
- Start/retain the new provider's terminal in the existing chat.
- A subsequent user message is delivered to the live provider without a 409,
  "Could not submit live input", or a provider-mismatch error.

## Confida recurrence — 2026-09-18

After deleting a private Claude account and saving Gemini for `confida-login`,
the existing Builder chat still launched Claude. The CLI displayed onboarding
and login; the generic classifier interpreted its “billing” text as exhausted
quota. A subsequent live-input submission targeted a Claude tmux that was no
longer registered and became durably uncertain.

The first repair (`452b3687b`, deployed to Confida) adds provider/model/account
comparison before retained delivery and propagates the manifest's connection
ID into runtime construction, including clearing a stale private binding when
selecting the server account. Shared provider commit `c732ebb` identifies the
Claude login menu as authentication failure and excludes captured pane-tail
text from generic quota classification. Focused regression tests pass.

Live retry exposed another boundary: workflow-phase `/query` payloads resolve
workspace from `preset_query_id`, but the receipt and retained-policy checks ran
before that resolved folder was assigned to the request. `/live-input` had saved
`Workflow/confida-login`; `/query` compared an empty project and returned
“Idempotency-Key already belongs to another submission.” The earlier provider
repair alone therefore did not fully unblock the conversation.

The follow-up assigns the preset-resolved workflow folder before journal and
retained-policy admission. Legacy empty-project receipts can replay only after
verifying the same owner's durable session belongs to that workflow. Different
messages, owners, sessions and explicit projects remain conflicts. Unknown or
uncertain delivery never triggers automatic resend. The frontend releases a
receipt only for the explicit reconciled `delivery_not_sent` response; a later
user retry can obtain a new receipt.

### Recovery evidence and limits

The workflow conversation retained 350 messages. The former Gemini native
transcript ended during tool work without a completed final answer. The
specific blocked submission `86410322-f505-4bc1-986d-016255c12a96` was backed up
and reconciled as rejected/not delivered: the adapter reported no registered
Claude session and native transcript inspection found no exact user-message
match. No user message was resent during recovery. This incident-specific
reconciliation is not a general exactly-once delivery guarantee.

### Remaining live acceptance

1. Delete a private Claude connection, save Gemini, and continue the existing
   Builder conversation through both `/query` and `/live-input`.
2. Confirm Gemini actually receives the new turn, with prior history available.
3. Switch private account A to B on the same provider and verify exact binding.
4. Repeat across browser refresh/backend restart; retry a proven rejected
   receipt with a new receipt while preserving uncertain-delivery protection.
5. Confirm repeated accepted submissions replay their outcome without a second
   native delivery, including legacy empty-project receipts.

Related persistence and durable-submission tracking: [PLAT-324](../../crew/chat-reliability/plat-324.md).

### Recent-fix provenance

- `1979a25ef` (2026-09-17, PLAT-324) introduced the durable journal's strict
  owner/project/session/message comparison and this exact conflict error.
  The query route still accepted receipts before resolving its workflow folder.
- `4e2d78b89` (2026-09-17, PLAT-324) recovered workflow identity for cold
  live-input dispatch; it did not canonicalize workflow-phase query requests.
- `e6e77d9c8` (2026-09-18) added queued-action deduplication and changed the
  same-message comparison. It preserved the existing project comparison;
  the observed direct retry is not evidence of a queue-deduplication defect.
- `f75a304b5` (2026-09-18, PLAT-262/PLAT-102) introduced fresh retained-policy
  admission. Its workflow gate depends on `SelectedFolder`, so requests that
  identified their workflow only through the preset bypassed that check.

The 409 is a confirmed integration gap in the recent PLAT-324 rollout. The
provider/account defect is the PLAT-099 recurrence; the PLAT-262/102 path shares
the same unresolved-folder prerequisite. The immediately preceding Workshop
UI commit `d8de0e464` does not change these submission or routing paths.

### Follow-up deployment verification

The canonical submission correction `58281d117` and provenance documentation
`d3624e748` are deployed to Confida release
`confida-d3624e74-20260918153600` (2026-09-18). Agent, workspace and gateway are
active; agent health returns HTTP 200. Go submission/retained-policy tests,
four frontend submission-target tests, TypeScript and release checks pass.
A fresh user turn through Gemini has not been submitted by the recovery agent;
live user retry remains the acceptance boundary.
