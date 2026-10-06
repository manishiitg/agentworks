# Cross-workflow Pulse platform triage — 2026-08-29

## Scope

This is the single working list for the active externally-owned Pulse findings
in `Workflow/upwork`, `Workflow/linkedin`, and `Workflow/salesoutreach`.
It is an audit ledger, not a replacement for the platform issue register:
individual implementation tickets must retain one narrow repair boundary.

The source of truth for each row is the workflow's `db/db.sqlite` record.
The snapshots contained 32 `external_action_required` rows: 15 Upwork, 11
LinkedIn, and 6 Sales Outreach.

## Already covered by main — reverify, then close the workflow findings

| Platform ticket | Workflow findings | Why it matches | Next action |
|---|---|---|---|
| [PLAT-001](../bugs/pulse_platform/goals/human-decisions/plat-001.md) | Upwork `PUL-7B849CC6` | `run_full_workflow` dropped a keyed human-input override. | Run one bounded override case, then close or reopen against current evidence. |
| [PLAT-033](../bugs/pulse_platform/goals/plans-contracts/plat-033.md) | Upwork `PUL-F4468936` | Both report placeholder/false changelog evidence after a real mutation. | Inspect one new managed mutation and close if its before/after refs are truthful. |
| [PLAT-200](../bugs/pulse_platform/browser/automation/plat-200.md) | Upwork `PUL-DD9EDE3C` | Oversized browser snapshots are now staged into an authorized tool-output path rather than discarded. | Reproduce one oversized guarded snapshot and close if its spill artifact is readable. |
| [PLAT-206](../bugs/pulse_platform/goals/pulse/findings/plat-206.md) | Upwork `PUL-565F9ED1`; Sales Outreach `PUL-4719B06` | Exact same terminal-module-result collision: reviewer `done` blocked the later Fixer's disposition write. | Run one review-plus-fix case with a real disposition. Close both only if it persists the repair. |

`PUL-13197E02` (Upwork tab acquisition) is confirmed **not** a duplicate of
[PLAT-028](../bugs/pulse_platform/browser/automation/plat-028.md) — see
[PLAT-231](../bugs/pulse_platform/browser/automation/plat-231.md), which also found both of its
cited errors are correct input validation in `cdp_tabs.go`, not a harness
defect; reclassified.

## Current Pulse architecture — deployment/reverification required

| Area | Findings | Current conclusion |
|---|---|---|
| Review/Fixer receipt and identity flow | LinkedIn `PUL-517B83AD`; Sales Outreach `PUL-E3F22BCC`; plus the PLAT-206 pair above | Related, not one root cause. `PUL-E3F22BCC` is the old child-session-versus-Pulse-run-ID receipt lookup documented by [PLAT-196](../bugs/pulse_platform/goals/pulse/reviews/plat-196.md). [PLAT-199](../bugs/pulse_platform/goals/pulse/reviews/plat-199.md)'s one retained Review+Fix flow is the correct direction. The local removal of the redundant receipt gate still needs deployment and one live test. |
| ~~Pulse finding identity~~ done | Sales Outreach `PUL-1E38F625` | [PLAT-222](../bugs/pulse_platform/goals/pulse/findings/plat-222.md) scoped `target_key`-based fingerprint identity by reporting module for every issue kind except `harness_issue` (a deliberately shared cross-workflow identity, left unchanged). Runtime reverify remains. |
| Schedule/session lifecycle | Upwork `PUL-1EE39C89`; LinkedIn `PUL-3565D07C`; Sales Outreach `PUL-5D2B9495`; Sales Outreach `PUL-985F2597` | Same subsystem, four different boundaries: iteration retention/identity, stale schedule projection, delayed terminal-session detection, and an upgrade turn occupying the scheduled message slot. Do not merge them without one shared reproduction. [PLAT-194](../bugs/pulse_platform/schedules/history/plat-194.md) is related to terminal reconciliation, not proof that all four are fixed. |
| Evaluation pipeline | Upwork `PUL-E67413EC`; LinkedIn `PUL-E45BE152`; LinkedIn `PUL-90D1E2C9` | Confirmed three different fixes, not one umbrella: `PUL-E67413EC` is `issue_kind: workflow_issue` with no platform blocker named — reclassified to Upwork's own Engineering Review, not this queue. `PUL-E45BE152` is done — see below. `PUL-90D1E2C9` has no structured detail row (only a `run_concerns` text line survived), so it was left uninvestigated rather than guessed at. |
| Artifact/change coverage (done) | LinkedIn `PUL-17E6F19A`; LinkedIn `PUL-7607952E` | [PLAT-230](../bugs/pulse_platform/goals/plans-contracts/plat-230.md): confirmed both were already settled independently — the changelog gap by PLAT-205's explicit user decision, the dashboard cursor by the mechanism it depends on (`builder/improve.html`) having been retired platform-wide before this finding could even be actioned. |

## New platform implementation queue

| Priority | Finding(s) | Required repair boundary |
|---|---|---|
| Reclassify | Sales Outreach `PUL-985F2597` | The cited schedule is `pulse_review_only` and intentionally has no normal workflow message. Its upgrade preflight and later Pulse lifecycle were separate phases; the run became partial because Pulse failed, not because an upgrade displaced a configured job message. |
| P0 | Sales Outreach `PUL-5D2B9495` | [PLAT-219](../bugs/pulse_platform/schedules/general/plat-219.md) records the implemented delayed-closure fix: preserve full-run identity at launch and honor its exact durable failure after bounded inactivity. Runtime reverify remains. |
| P0 | LinkedIn `PUL-3565D07C` | Keep separate: this 2026-08-06 finding concerns stale `list_schedules` projection after a successful run, not a missing full-run completion callback after durable failure. Reverify against the newer runtime-reconciliation changes before implementing more code. |
| P0, partially done | LinkedIn `PUL-B995BF46`; `PUL-3BD9F422` | [PLAT-221](../bugs/pulse_platform/goals/steps/database/plat-221.md) shipped the managed schema-migration route, but a code review caught that the new tool was registered and tested, yet absent from every real agent's allowlist (workflow-step, todo-task, and Workshop/Pulse paths) — fixed as a PLAT-221 follow-up, now genuinely reachable. `PUL-B995BF46` itself turned out already resolved independently (stale finding — real `action_outcome_bindings`/`matched_action_outcome_comparisons` data since 2026-08-21, filing never re-verified); reclassify/close that workflow row. `PUL-3BD9F422` remains genuinely open (`post_approval`/`image_assets` unchanged) — its migration SQL still has not been designed. Do not mark this row fully done until that migration exists and applies. |
| ~~P1~~ done | LinkedIn `PUL-E45BE152` | [PLAT-227](../bugs/pulse_platform/goals/evaluation/plat-227.md) added the missing `pre_validation` editable field to `update_evaluation_plan`, so route gating and pre-run evidence can be realigned atomically. |
| Reclassify | Upwork `PUL-E67413EC` | `issue_kind: workflow_issue`, no platform tool gap named — belongs to Upwork's own Engineering Review, not this repository's platform queue. |
| P1 (uninvestigated) | LinkedIn `PUL-90D1E2C9` | No structured detail row survives for this finding — only a `run_concerns` text line ("evaluation report aggregator dropped a valid current eval-strategy-loop verdict"). Worth checking as a genuine platform-aggregator bug in a future pass; not started here. |
| ~~P1~~ done | Sales Outreach `PUL-1E38F625` | [PLAT-222](../bugs/pulse_platform/goals/pulse/findings/plat-222.md) shipped the module-scoped fix; see above. |
| P1 | Upwork `PUL-D7D173FB` | [PLAT-218](../bugs/pulse_platform/goals/human-decisions/plat-218.md) corrects the diagnosis: HTTP preserved the outer object, but nested `approved_scope` and `post_run_proof` used the wrong types and the handler hid that fact behind a false outer-object error. Exact field errors and schema/guidance clarification are implemented. |
| ~~P1~~ done | Upwork `PUL-EDFF0710` | [PLAT-223](../bugs/pulse_platform/goals/learnings/plat-223.md) reworded the injected instruction to fall back to `query_workflow_db describe` rather than auditing every Folder Guard construction site. |
| Reclassify | Upwork `PUL-9CCE9488` | [PLAT-224](../bugs/pulse_platform/browser/automation/plat-224.md) confirmed this is an external `agent-browser` binary defect (no vendored source, no in-repo output filtering to build on) — same class as PLAT-215. Not fixable as platform code here; needs an upstream fix or documentation correction outside this repository. |
| P1 | Upwork `PUL-A8AB0913`; `PUL-E717A5E1` | [PLAT-226](../bugs/pulse_platform/ops/cost/plat-226.md) confirmed the exact root cause (a shared token-attribution fallback with no signal to disambiguate Pulse/Builder/orchestrator overhead) and ruled out an existing field as a cheap fix, but stopped short of shipping one — it needs new session-kind signaling across three separately-configured call sites plus a live run to verify against, neither safe to guess at statically. |
| ~~P1~~ partially done | Sales Outreach `PUL-AAC278EF` | [PLAT-225](../bugs/pulse_platform/coding-agents/tools/plat-225.md) fixed the concrete, reproduced half: the bridge no longer discards a chained command's real stdout/stderr when it reports failure, matching the finding's own stated `next_check` exactly. Left open, and genuinely fuzzier: whether an ordinary non-2xx HTTP response or expected non-zero subcommand embedded in a script should ever be classified as a tool-level failure at all is a shell-scripting-intent judgment call, not addressed here — a script's own exit code is arguably always the correct signal to trust. |
| ~~P2~~ done | LinkedIn `PUL-61C84987` | [PLAT-229](../bugs/pulse_platform/goals/evaluation/plat-229.md) fixed the false-pass at its root: the shared JSONPath value extractor now only unwraps genuine wildcard multi-matches, never a definite path's own array value. Code review follow-up fixed a related gap in the same function: a genuine wildcard multi-match now checks every result for `value_type`, not just the first. |
| ~~P2~~ done | LinkedIn `PUL-17E6F19A`; `PUL-7607952E` | See above — [PLAT-230](../bugs/pulse_platform/goals/plans-contracts/plat-230.md). |
| ~~P2~~ done | Upwork `PUL-B0A88D49` | [PLAT-228](../bugs/pulse_platform/goals/learnings/plat-228.md) confirmed the specific instance is stale — the whole `"bug-review"` guidance kind it referenced was retired and replaced by `engineering-review`, whose reads are genuinely bundled. The systemic `read_skill` materialization-time validator this row also asked for remains real, unstarted future work. |
| ~~P2~~ done | Upwork `PUL-13197E02` | [PLAT-231](../bugs/pulse_platform/browser/automation/plat-231.md) — reclassified, not a harness defect. |

## Reclassify instead of fixing as platform code

| Finding | Correct destination |
|---|---|
| LinkedIn `PUL-82F50DFF` | External account constraint: the LinkedIn personalized-invitation quota is zero. It needs an account-state display/reopen condition, not a platform-code ticket. |
| LinkedIn `PUL-0394F0B4` | User-owned Soul/content correction. Route to an answerable decision or an authorized Soul edit; it is not a runtime defect. |
| Upwork `PUL-4BBFBC4B` | Accepted data-source/coverage limitation. Keep a bounded evidence note, not a platform repair. |
| Upwork `PUL-D50AD8BC` | Obsolete under the current architecture: normal step-summary concern parsing/step-side filing was intentionally retired. A reviewer files canonical Pulse findings; do not restore the old tool merely to close this row. |

## Main-branch review result

Recent `main` changes are relevant, but they do not close the whole list:

- `22ac69c8a` / PLAT-206 directly fixes the Upwork/Sales terminal-result pair.
- `bb02c1a1a` and `af580f01d` / PLAT-200 directly address the oversized-snapshot path behind Upwork `PUL-DD9EDE3C`.
- `34d5ab283` / PLAT-190 reduces load-bearing missing-skill guidance, but Upwork `PUL-B0A88D49` still needs a materialization-level check and a live reverify.
- `3fb200020` / PLAT-194 is related to the Sales/LinkedIn schedule state findings, but does not prove them fixed.
- `cc98b4598` / PLAT-196 only added receipt-ID diagnostics; it did not fix Sales `PUL-E3F22BCC`.
- `d9223aa61` / PLAT-199 simplified review/fix dispatch. The follow-up removal of the redundant receipt gate is still local until committed and deployed.
- `636fb6e0a` is a Pulse backlog migration: it improves queue accounting and platform handoff, but intentionally does not repair the underlying platform defects.

## Order of work

1. Deploy the current Pulse receipt-gate removal, then reverify and close the
   PLAT-206 pair and Sales `PUL-E3F22BCC` if the live flow succeeds.
2. Run the four already-covered rechecks (PLAT-001, 033, 200, 206) and close
   stale workflow rows with evidence.
3. Split the P0/P1 rows above into narrow implementation tickets; do not give
   one agent all 32 rows as one undifferentiated repair task.
4. Reclassify the four non-platform rows so the platform lane means platform
   work again.
