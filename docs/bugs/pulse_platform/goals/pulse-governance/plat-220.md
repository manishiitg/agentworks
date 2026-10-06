[← goals / pulse-governance](index.md)

# PLAT-220 — consolidated two more Pulse tool pairs: focus agenda folded into `get_pulse_state`, the two migration-reconciliation tools merged into one scoped tool

| Field | Value |
|---|---|
| State | closed |
| Priority | P3 |
| Product | goals |
| Area | pulse-governance |
| Summary | P3 — tool-surface simplification, not a defect fix. |

> **2026-09-10 update:** [PLAT-306](plat-306.md) supersedes mandatory Markdown
> checkpoint/report maintenance with optional SQLite review notes on the existing
> result tool. Typed lifecycle records remain authoritative; historical design
> discussion below is retained. No reporting-only turn is required.

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` |
| Last synchronized | `2026-08-29` |

- **Priority:** P3 — tool-surface simplification, not a defect fix. Filed
  for the same reason PLAT-tickets track PLAT-055's typed-tool migration:
  a durable record of why the Pulse tool surface looks the way it does.
- **Owner:** N/A — implemented directly.
- **Related:** the concurrent, independently-landed `a8aaaa012` "Simplify
  Pulse maintenance and harden lifecycle handling" commit removed
  `complete_pulse_review` entirely (findings now persist without a
  completion handshake) in the same file this ticket touches; both changes
  were reconciled via a `git stash`/rebase/pop merge, not a rewrite of
  either side's intent.

## What changed

User-requested, explicitly scoped consolidation of the Pulse agent-facing
tool surface, after a design discussion concluding the tool *count* (not
the underlying SQLite structure) was the actual friction:

1. **`get_pulse_review_focus_agenda` folded into `get_pulse_state(view="focus_agenda")`.**
   `get_pulse_state` already served three views (`module`, `backlog`,
   `review`) behind one tool; the focus-agenda read fit the same
   one-tool-many-views shape. `readPulseFocusAgendaView` wraps the existing
   `getPulseReviewFocusAgenda` and returns the same `{"focuses": [...]}`
   payload; `route_scope` and `limit` were added as `get_pulse_state`
   parameters, gated to this view.

2. **`record_pulse_lifecycle_reconciliation` and
   `record_pulse_actionable_backlog_reconciliation` merged into
   `record_pulse_migration_reconciliation(scope="lifecycle"|"actionable_backlog")`.**
   `ReconcilePulseActionableBacklog` already called
   `ReconcilePulseFindingLifecycle` as its own first step and embedded the
   result — the two were never independent capabilities, only two names for
   "run this migration, optionally with the superset step." An invalid or
   missing `scope` is rejected outright rather than silently defaulting to
   one migration.

Both consolidations are pure surface changes: no SQLite schema, no
migration behavior, and no reviewer-facing semantics changed. All known
callers were updated — `workflow_version_upgrades.go`'s three
contract-upgrade prompt strings (v1.0.32, v1.0.33, v1.0.34), three
guidance templates (`engineering-review.md`, `ops-review.md`,
`pulse-review-fixer.md`), and the test suite
(`pulse_review_tools_test.go`, `pulse_tool_surface_test.go`,
`workflow_schedule_contract_test.go`).

## Explicitly not done

Two other pairs were considered during the same design discussion and
deliberately left alone:

- `resolve_run_concern` vs `record_pulse_result` — different write targets
  (a lifecycle-status flip on an existing concern vs. recording a new,
  possibly evidence-bearing result); merging them would hide that
  `resolve_run_concern` explicitly refuses to close a real finding.
- `record_pulse_review_focus` vs `complete_pulse_review` — at design time
  this pair looked mergeable, but the concurrent `a8aaaa012` commit
  removed `complete_pulse_review` outright before this ticket landed, so
  there is nothing left to merge it with.

Markdown-file-based Pulse management (replacing the SQLite-backed tools
the way PLAT tickets are managed as files) was proposed and rejected in
the same discussion: this session hit a real concurrent git write
collision on a PLAT ticket file (PLAT-207, resolved via `git checkout --`)
that SQLite's `INSERT ... ON CONFLICT` transactions prevent for Pulse
writes, and the "stale finding, already fixed, never re-verified" pattern
recurred five separate times this session (PLAT-191/195/201/202/212) even
with structure in place — a strong signal structure is load-bearing here,
not incidental.

## Verification

- `go build ./...` — clean.
- `go test ./cmd/server/...` and full `go test ./...` from `agent_go/` —
  all pass, including the pre-existing suite; one new test,
  `TestRecordPulseMigrationReconciliationDispatchesByScope`, exercises
  both scopes plus invalid/missing-scope rejection directly against the
  merged executor.
- `pulse_tool_surface_test.go`'s exhaustive `expected` vs `registered` tool
  set check (`TestPulseToolSurfaceIncludesTypedReviewerWrites`) passed
  after the merge with `a8aaaa012`, confirming no stale tool name survived
  from either side of the conflict.
- Grepped the full `agent_go/` tree (excluding `logs/agent_prompts/`,
  which holds historical run transcripts, not live code or guidance) for
  the three removed tool names — no remaining references outside the
  files already updated.
