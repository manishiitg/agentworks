[← goals / plans-contracts](index.md)

# PLAT-033 — managed changelog entries contain placeholder evidence

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | plans-contracts |
| Summary | managed mutation and changelog evidence writer |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `implemented` (the two reproduced call sites; shared mechanism, not every caller) |
| Last synchronized | `2026-08-05` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.

- **Priority:** P1
- **Owner:** managed mutation and changelog evidence writer
- **Source finding:** `HARNESS-CHANGELOG-REF-PLACEHOLDER`
- **Source workflow:** `Workflow/social-media`
- **Source fingerprint:** `ae0b8a1b78ba5a0c`
- **Problem:** managed planning tools can record
  `before_ref == after_ref == sha256("[]")` with `changes: []` even when an
  artifact changed. Some entries also name a target the tool did not write,
  and a confirmed mutation was omitted from the changelog.
- **Impact:** the changelog proves only that a tool ran. Artifact Review cannot
  establish what changed, reconstruct an outage-triage window, or verify the
  claimed mutation boundary.
- **Current evidence:**
  - six retained entries use
    `sha256:4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945`,
    the SHA-256 of the literal string `[]`, for both refs;
  - three entries have `changes: []` while
    `planning/step_config.json` has the matching modification time;
  - one `write_workflow_manifest` entry targets `planning/plan.json` while its
    reason describes a direct `workflow.json` write;
  - the latest Fixer reproduced the behavior across 11 `update_step_config`
    calls, including requested fields that were silently dropped.
- **Required fix:** snapshot the actual target artifact before mutation and
  after the persisted mutation; hash those artifact states; record the fields
  actually accepted and changed; use the artifact actually written as the
  target; and fail closed if a requested field is unsupported instead of
  recording a misleading successful edit.
- **Acceptance:** a real mutation produces correct, different content refs and
  a non-empty change description; a no-op is explicitly recorded as a no-op;
  an unsupported field fails without a success entry; the target matches the
  written artifact; and every sanctioned mutation emits exactly one entry.
- **Required tests:** `update_step_config` mutation, no-op, unsupported-field,
  target-integrity, and crash/rollback cases using the real managed writer.

## Relationship to PLAT-012

[PLAT-012](plat-012.md) asks whether every material managed mutation is covered
by the changelog. This ticket asks whether an emitted entry is truthful. The
current Social Media evidence was produced after the PLAT-012 implementation
and is therefore a separate, presently reproduced integrity defect rather than
proof that PLAT-012's coverage work never existed.

## Implementation (2026-08-05, Claude Code, `mcp-agent-builder-go` `cdc3d1a76`)

**Root cause confirmed bit-for-bit against the evidence.**
`completePlanChangelogEntry` (`planning_agent.go`) computed `before_ref`/
`after_ref` by hashing `entry.Changes[].OldValue`/`NewValue` — never the
actual artifact. `sha256("[]")` is literally the SHA-256 of the JSON encoding
of an empty `Changes` list; hashing the string `"[]"` by hand reproduces
`4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945` exactly,
matching the ticket's cited hash. Two call sites reproduce the evidence
directly:
- `update_step_config` (`interactive_workshop_manager.go`) never set
  `Changes` at all — every call collapsed both refs to the same placeholder,
  regardless of what fields actually changed. This matches "11
  `update_step_config` calls, including requested fields that were silently
  dropped" exactly — there was no mechanism that could have recorded them.
- `write_workflow_manifest` (`workflow_manifest.go`, via
  `LogCanonicalArtifactChange`) passed `changes: nil` despite already having
  read the previous file content (`previous`) and having the new content
  (`data`) sitting right there — real before/after bytes were available and
  simply not passed through. Its `Target` also fell through
  `completePlanChangelogEntry`'s tool-name-substring default (only
  `workflow_config`/`step_config`/`learning`/`evaluation` are special-cased;
  `"write_workflow_manifest"` doesn't contain `"workflow_config"`) to
  `planning/plan.json`, reproducing the "targets `planning/plan.json` while
  its reason describes a direct `workflow.json` write" evidence exactly.

**Fix, at the shared choke point plus the two reproduced callers:**
- `PlanChangelogEntry` gains `BeforeSnapshot`/`AfterSnapshot` (`json:"-"`,
  never persisted themselves) and `NoOp` (`json:"no_op,omitempty"`).
  `completePlanChangelogEntry` now hashes the snapshot when present instead
  of deriving from `Changes`, and sets `NoOp` only when **both** snapshots
  are real and hash identically — an entry with no snapshot stays "unknown,"
  it is never misreported as a confirmed no-op. Callers that only ever set
  `Changes` (the other ~13 `logPlanChange`/`PlanChangelogEntry{...}` sites in
  `planning_agent.go`) are unaffected — same ref computation as before,
  verified by `TestCompletePlanChangelogEntryUnchangedForCallersWithoutSnapshots`.
- `LogCanonicalArtifactChange` gained `target string, beforeSnapshot,
  afterSnapshot interface{}` parameters (both existing callers updated:
  `controller_execution.go`'s learnings-update call passes empty/nil — it
  already builds honest `Changes` from real content hashes — and
  `workflow_manifest.go` now passes `"workflow.json"`, `previous`, and
  `string(data)`).
- `update_step_config` now: snapshots `targetConfig` via a JSON round trip
  immediately after loading it and before any field mutation runs (decoupling
  the snapshot from the in-place mutations that follow); after
  `WriteStepConfigsToSubdir` succeeds, **re-reads the persisted file** (not
  the in-memory struct) and snapshots the matching step's entry — so the
  after-snapshot reflects what was actually written, including any silent
  normalization/drop the writer applies, which is exactly the failure mode
  the evidence describes; passes the correct `configSubdir`-derived target
  path instead of relying on the heuristic; and records one real
  `PlanFieldChange` (`Field: "agent_configs"`, old/new = the two snapshots)
  so `changes` is never empty again.

**What this does NOT cover (scoped out, not silently skipped):**
- **Fail-closed on unsupported fields** (required fix item 4) — not
  implemented. `update_step_config` still accepts and silently ignores a
  field name it doesn't model; this fix makes the *recorded* refs honest
  about whatever WAS accepted, it does not reject anything.
- The other ~13 `PlanChangelogEntry{...}` call sites (add/update/delete
  step, routing steps, human-input steps, evaluation-plan updates, etc.) were
  **not audited individually** for the same defect — only `update_step_config`
  and `write_workflow_manifest` were reproduced in the evidence and fixed.
  A comment in `plan_change_backlog.go` (`toUnreviewedPlanChange`) suggests
  `update_step_config` was uniquely bad among these ("update_step_config
  records none today"), implying the others likely already populate
  `Changes` correctly, but this was not independently re-verified per call
  site.
- **No end-to-end test of the `update_step_config` tool handler itself** —
  the shared mechanism (`completePlanChangelogEntry`, `LogCanonicalArtifactChange`)
  is unit-tested directly; the wiring inside `interactive_workshop_manager.go`
  was verified by code reading and a full build, not by driving the tool
  through a constructed `InteractiveWorkshopManager` + fake controller.
- **No-op, unsupported-field, target-integrity, and crash/rollback test
  cases** named in "Required tests" — only the no-op and target-integrity
  cases are covered, and only at the shared-mechanism level, not through the
  real tool handler.

**Tests:** `plat033_changelog_ref_test.go` — snapshot-preferred refs, no-op
only-when-real-evidence, backward-compatible refs for Changes-only callers,
and `LogCanonicalArtifactChange`'s target/snapshot wiring end-to-end through
`writePlanChangelogEntry`. Confirmed to fail to compile against the pre-fix
code (stashed and re-ran).

**Remaining/runtime reverify:** confirm on a real Social Media run that a
fresh `update_step_config` call produces a `before_ref`/`after_ref` pair that
differ when a field actually changed, that `changes` is non-empty, and that
`write_workflow_manifest` entries target `workflow.json`.

## Follow-up: manifest changes still lack a readable field diff (2026-08-05)

**Source finding:** `PUL-988DF96A` in `Workflow/hetznerssh`, reported by
Artifact Review on 2026-08-02.

This is a valid remaining part of PLAT-033, not a workflow-only process
problem. The current `WriteWorkflowManifest` call passes the fixed reason
`"workflow.json was written directly; recorded so artifact drift review can
see the change."` and `changes: nil`. The earlier repair makes its target and
before/after hashes truthful, but a human reading the changelog still cannot
tell *which* manifest fields changed without reconstructing the JSON or using
Git.

The reviewer is right about that outcome, but its suggested remedy (ask each
future caller to write a better sentence) is not sufficient: this shared
writer already has both JSON documents and should compute the evidence
deterministically.

**Required follow-up:** parse the previous and persisted `workflow.json`,
produce a stable, secret-safe list of changed JSON paths, and pass it as
`PlanFieldChange` evidence. Values for secret-bearing fields must be redacted
or omitted; field paths plus `added`/`removed`/`changed` status are enough for
the changelog. Keep the current content hashes as the authoritative exact
boundary. A no-op must still produce no entry.

**Acceptance:** a real manifest write records target `workflow.json`, distinct
content refs, and deterministic changed-path evidence; changing one nested
field does not claim unrelated paths; secret values do not appear in the
changelog; and an unchanged write creates no entry.

## Follow-up implementation (2026-08-05, Codex; runtime reverify pending)

`WriteWorkflowManifest` now derives its `changes[]` evidence from the JSON it
already read and the JSON it just persisted. It emits sorted leaf paths such as
`workflow.json.capabilities.selected_skills`, with only lifecycle markers
(`absent → added`, `present → changed`, or `removed → absent`). It does **not**
copy old or new JSON values into the changelog, including for secret-bearing
fields. Arrays are represented as one changed path so an insertion or reorder
does not manufacture a long, unstable index diff.

The fixed writer records the short reason “Recorded workflow.json field changes
for artifact drift review.” and leaves the existing before/after content hashes
as the authoritative exact mutation boundary. An unchanged manifest still emits
no entry.

**Tests:** `TestWorkflowManifestChangelogChangesIsStableAndValueFree` covers
nested scalar and array changes, deterministic order, and no value leakage;
`TestWorkflowManifestChangelogChangesCreationAndCorruptPriorState` covers a
new manifest and an unparseable historic manifest. Runtime reverify on the next
manifest mutation should confirm a non-empty `changes[]` list alongside the
already-fixed `workflow.json` target and distinct refs.

## Tectonicus coverage evidence — 2026-08-05

Tectonicus Pulse found that **159 of 231** managed changelog entries have none
of `changes[]`, `before_ref`, or `after_ref`. The exact historic callers cannot
be truthfully backfilled; doing so would fabricate mutation provenance. The
finding does prove that fixing two reproduced callers was not enough to satisfy
the ticket's "every sanctioned mutation" acceptance condition.

**Remaining implementation boundary:** audit every managed mutation writer,
make each either emit an actual target plus before/after snapshot and changed
fields or fail closed, and add a table-driven coverage test that registers every
sanctioned mutation tool/caller. New entries must be complete; old incomplete
entries remain explicitly historical/unknown. This is platform-owned work, not
a Tectonicus workflow repair.
