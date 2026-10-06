[← goals / pulse / general](index.md)

# PLAT-239 — `changed_unverified`'s contradictory `next_check` requirement: already resolved independently

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | goals |
| Area | pulse/general |
| Summary | harness_issue, severity high. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `resolved` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue, severity high.
- **Findings:** Twitter/social-media `PUL-DA42695E` — a reviewer could not
  successfully submit a `changed_unverified` disposition: passing
  `next_check` nested under `disposition.` or `verification.` was rejected
  as `unknown field next_check` by the decoder, and omitting it entirely
  was rejected by lifecycle validation with `"finding ... changed_unverified
  requires next_check naming the run, table, or artifact"` — no placement
  of the field could ever satisfy both the decoder and the validator at
  once.

## Already resolved independently (2026-08-28, before this finding was investigated)

`git log -S "changed_unverified requires next_check"` on
`pulse_finding_lifecycle.go` finds commit `c60f1f830` ("Simplify Pulse
review, repair, and workflow run lifecycle", 2026-08-28 22:09
+0530) — one day before this session — which **removed** the exact
requirement block the finding's evidence quotes near-verbatim:

```go
if disposition.NextCheck == "" {
    add("changed_unverified requires next_check naming the run, table, or artifact whose arrival proves or disproves this fix")
}
```

The current `validateFindingDisposition`'s `FindingDispositionChangedUnverified`
case has no `NextCheck` check at all — only `ChangedFiles`, matching
`before_refs`/`after_refs` lengths, and no failed immediate check are
required. Confirmed the two later commits touching this file
(`d9223aa61`, `636fb6e0a`) did not reintroduce it. `next_check` remains a
correctly-decoded top-level field on each `finding_dispositions[]` element
(`pulseFindingDispositionToolArg.NextCheck`, both in the Go struct and the
tool's JSON schema) for the dispositions that do still require it
(`awaiting_run`, `queued_for_engineering`) — the finding's decoder-rejection
evidence (`disposition.next_check`, `verification.next_check`) reflects
wrong nesting attempts, not a decoder defect; the schema has always
correctly placed `next_check` as a direct sibling of `disposition`/`summary`
within each array element.

Same "real when filed, already fixed via a same-era commit, never
re-verified in the finding" shape as PLAT-228/230/237.

## Verification

Confirmed via `git log`/`git show` against the actual commit history, not
inferred from the finding text alone. No code changed this session — the
fix already shipped in `c60f1f830`.

## Reverify

Not applicable to this session's work. If `changed_unverified` still
appears to require `next_check` in a live run, that would indicate a
regression since `c60f1f830`, not this finding recurring.
