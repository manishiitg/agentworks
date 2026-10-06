[← platform / learnings-knowledge](index.md)

# PLAT-228 — the "bug-review" guided flow that referenced a missing skill file no longer exists

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | learnings |
| Summary | P2 — Upwork `PUL-B0A88D49` (per the |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `resolved — same "real when filed, already fixed via restructuring, never re-verified" pattern as PLAT-191/195/201/202/210/212/213` |
| Last synchronized | `2026-08-29` |

- **Priority:** P2 — Upwork `PUL-B0A88D49` (per the
  [2026-08-29 triage audit](../../../../audits/pulse-platform-triage-2026-08-29.md)).
- **Related:** [PLAT-190](plat-190.md), which audited every tool's
  `read_skill` reference and left this one explicitly open — "Upwork
  `PUL-B0A88D49` still needs a materialization-level check and a live
  reverify" — because at the time it genuinely still reproduced.

## What the finding reported

`get_workflow_command_guidance(kind='bug-review')` mandated
`read_skill(skills=[{name: builder-reference, path:
references/post-run-monitor.md}])` as one of three required reads. That
call failed: `"file \"references/post-run-monitor.md\" is not bundled with
attached skill \"builder-reference\""`. The other two mandated reads
(`references/pulse-bug-review.md`, `references/assumption-audit.md`)
succeeded. The finding's own workaround named the fix directly: either
bundle the missing file, or point the guidance at
`references/pulse-gate.md`, which actually carries the Engineering Review
trigger content.

## Why it no longer reproduces

`"bug-review"` does not exist anywhere in the current codebase as a
`get_workflow_command_guidance` kind — confirmed by a repo-wide search for
the literal string, which returns zero matches in Go source. The current
`instructions.go` kind listing has no `bug-review` entry at all; the
successor is `engineering-review` ("read-only Technical Review phase;
manual pulse-review aliases attach its receipt-gated Fix phase
automatically").

`engineering-review`'s actual guidance template
(`templates/improve/engineering-review.md:12`) mandates entirely different
reads: `references/pulse-review-fixer.md` and `references/ops-review.md` —
not `post-run-monitor.md` at all. Both of those files are confirmed present
on disk (`templates/system/pulse-review-fixer.md`,
`templates/review/ops-review.md`).

`post-run-monitor` itself is not a live guidance artifact any more —
`docs/design/pulse-post-run-monitor-spec.md` is a design spec, read directly
by this repo's own test suite (`readPulseDesignSpec` in
`render_all_test.go`), not a bundled skill reference. A test comment in that
same file documents exactly the restructuring that made this finding stale:

> *"The deep Bug Review mechanics were extracted out of the Gate-loaded
> post-run-monitor doc into pulse-bug-review, loaded only when bug_review is
> due."*

That is precisely the fix the finding's own workaround asked for, already
done as part of a broader guidance restructuring, independent of this
finding — the dangling `references/post-run-monitor.md` reference and the
`"bug-review"` kind that named it were retired together, not patched.

## Left open, deliberately not folded into this ticket

PLAT-190's own suggestion — a materialization-time validator that checks
every declared `read_skill` reference across every guidance template
actually resolves to a bundled file, catching this whole *class* of defect
before an agent ever hits it live — is still not built. This specific
instance is resolved by restructuring, which happened to remove the dangling
reference but was not a systemic guard against the next one. That validator
remains a separate, real piece of future work; scoping and building it is
not part of this closure.

## Verification

N/A — no files changed. Closure record only, following the same pattern
already used for PLAT-191/195/201/202/210/212/213.
