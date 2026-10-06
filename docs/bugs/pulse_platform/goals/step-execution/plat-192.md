[← goals / step-execution](index.md)

# PLAT-192 — `diff_patch_workspace_file` can report `applied:true` while silently corrupting content; added an apply-path-agnostic safety net

| Field | Value |
|---|---|
| State | in progress |
| Priority | P2 |
| Product | goals |
| Area | step-execution |
| Summary | P2 — a false-positive success on a file-editing tool is a |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `partially fixed` — a defense-in-depth invariant check shipped and verified; the exact original root cause was not identified and could not be reproduced (see below), so this is a symptom-catching mitigation, not a proven fix for the underlying mechanism |
| Last synchronized | `2026-08-28` |

- **Priority:** P2 — a false-positive success on a file-editing tool is a
  real data-integrity risk, and `diff_patch_workspace_file` is used
  platform-wide (`knowledgebase/`, `learnings/`, any workspace document),
  not just by `confida-login`.
- **Owner:** `workspace/handlers/diff_patch.go` — a separate Go module
  (`github.com/manishiitg/coding-agent-loop/workspace`) at the repo root,
  distinct from `agent_go/`. This is the actual server-side implementation
  behind `PATCH /api/documents/{path}/diff`; `agent_go/pkg/workspace/diff_patch_workspace_file.go`
  is only an HTTP client that proxies to it.
- **Related:** two confida-login harness findings —
  `harness:diff_patch_workspace_file:silent-partial-apply` (high) and
  `harness:diff_patch_workspace_file` (medium, coarser earlier report) —
  describe the live incident this addresses.

## The reported incident

A `diff_patch_workspace_file` call against `knowledgebase/notes/_index.json`
with two hunks (append a `covers[]` entry; replace `last_updated`/
`last_updated_by`/`section_count`/`size_bytes` while preserving a trailing
`title` field) returned `{"data":{"applied":true}}` while only the first
hunk actually applied — the second hunk's fields were left at stale
pre-patch values, and the trailing `title` line was deleted entirely with no
replacement. A tool reporting success while silently destroying content.

## Investigation — what was confirmed, and where it stalled

The real implementation was traced (not assumed — `agent_go`'s own tool
schema/executor is only an HTTP client; the actual patching logic lives in
the separate `workspace/` module). Findings:

1. `DiffPatchDocument` tries a cascade: strict Unix `patch(1)` first (on
   an auto-"corrected" diff, then the original), falling back to
   `applyAgentGeneratedDiffFallback` — a custom hunk matcher for
   imperfect agent-generated diffs.
2. The custom fallback matcher is **fail-closed by design**: when it can't
   confidently match a hunk's context, it returns an error rather than
   silently dropping the hunk (`"could not find matching context lines...
   refusing to apply to prevent corruption"`). This was not the bug.
3. **Leading hypothesis, tested directly, disproven:** `correctAgentGeneratedDiff`
   (a preprocessing step that repairs malformed diff lines) can prematurely
   terminate a hunk mid-way if it hits an unprefixed context line it
   doesn't recognize as "safe" — hypothesized to orphan trailing content.
   Built a direct repro matching the reported shape (two hunks, JSON file,
   malformed trailing context line) and ran it against the real code. **It
   did not reproduce the bug** — the trailing line survived correctly. (It
   did surface a smaller, separate, real issue: context-line indentation
   gets mangled in the fallback path — not filed separately, noted here for
   whoever picks this up next.)
4. **Attempted to recover the exact original failing input** via the
   harness occurrence's `source_run_id` (`iteration-0/confida-staging`).
   Found real `diff_patch_workspace_file` calls against the exact file with
   a matching field pattern in the run's conversation log — but
   `iteration-0/confida-staging` is a reused run folder overwritten by every
   new cycle (the same mechanism PLAT-182 documented), so the calls visible
   now are very likely from a *later*, successful cycle, not the one that
   actually triggered the 2026-08-23 report. One real, smaller anomaly did
   turn up in what's recoverable: one call had a completely empty `result`
   field logged (neither an error nor a success payload) — not confirmed to
   be the same bug, noted for a future pass.

**The exact root-cause mechanism was not identified.** Rather than ship a
fix aimed at an unconfirmed cause, or leave the false-positive-success risk
unaddressed, the decision was to add a defense-in-depth safety net that
catches the *symptom* regardless of which internal path produces it.

## Fix — apply-path-agnostic invariant check

`verifyDiffApplied(originalContent, diffContent, newContent)` (new,
`diff_patch.go`) is called once, in `DiffPatchDocument`, right before the
file is written and `applied:true` is returned — after whichever internal
strategy (strict patch, corrected-diff retry, the fallback matcher)
produced `newContent`. It checks one thing: the actual line-count change
between the original and new content must equal what the diff's own `+`/`-`
body lines claim (`diffClaimedLineDelta`, counted from the body, not the
`@@` header — LLM-supplied header counts are exactly what
`correctAgentGeneratedDiff` already exists to fix, so they aren't trusted
here either). A mismatch refuses the write and returns an error instead of
reporting success.

This is deliberately a **necessary, not sufficient** check — a corruption
that happens to preserve the total line count (e.g. two unrelated lines
swapped) is not caught. It would have caught the exact reported incident:
hunk 1's addition (+1) landing correctly while hunk 2's net-zero replace
silently vanished *and* an unrelated line got deleted (net -1) produces an
actual delta of 0 against a claimed delta of +1 — a mismatch.

**Line-counting bug caught by the test suite during this fix, fixed before
shipping:** the first version used `len(strings.Split(content, "\n"))` for
line counts, which is wrong at both ends — an empty string splits to one
element (implying 1 line for 0 real lines), and a trailing `\n` splits to
one extra empty element (implying one more line than the file has). This
produced a false positive on `TestDiffPatchCreationWithControllingTTY` (a
real existing test, a single-line no-trailing-newline file creation),
rejecting a legitimate patch as corrupted. Replaced with `countContentLines`,
which handles both edge cases correctly and is covered by its own test.

## Explicitly not done

- The actual root-cause mechanism behind the original incident remains
  unidentified. If it recurs, the priority is capturing the exact failing
  `diff` argument before the run folder gets overwritten, not more guessing.
- The smaller indentation-mangling issue found while testing the (disproven)
  leading hypothesis was not fixed or separately filed.
- The one anomalous empty-`result` tool call found while investigating was
  not confirmed to be a real bug or investigated further.

## Verification

- `go build ./...` (in the `workspace` module) clean.
- New tests: `TestVerifyDiffAppliedCatchesSilentPartialApply` (reproduces the
  reported failure shape and proves it's now rejected),
  `TestVerifyDiffAppliedAcceptsACorrectApply` (no false positive on a normal
  multi-hunk patch), `TestDiffClaimedLineDeltaCountsBodyLinesNotHeaders`,
  `TestCountContentLines` (locks down the two edge cases that caused the
  false positive during this fix).
- Full `workspace` module test suite (`go test ./...`) passes, including
  the pre-existing `TestDiffPatchCreationWithControllingTTY` e2e test, which
  genuinely caught the line-counting bug above before this shipped.
- Not yet live-verified: no real workflow has hit this new rejection path
  in production since it shipped.

## Independent confirming data point (2026-08-29)

Twitter/social-media `PUL-D66C6684` (filed 2026-08-26, two days before this
fix shipped): `diff_patch_workspace_file` returned `applied:false`/HTTP 400
for `posts_multi.json`, but the target file still showed the first hunk's
insertion already present — the inverse-shaped symptom (failure reported,
partial write left behind) to the `applied:true`-with-silent-corruption
shape this ticket addresses, but the same underlying tool and the same
general "the real file can be mutated before/around the point a failure is
detected" class of bug.

Read the current `DiffPatchDocument` handler (`workspace/handlers/diff_patch.go`):
`os.WriteFile` (the only place the real target file is mutated) sits
strictly after `verifyDiffApplied`, and every failure path — including a
failed verification — returns before reaching it. Under the current code,
`PUL-D66C6684`'s exact scenario (failure reported, file still partially
mutated) is no longer reachable through this handler. Filed as PLAT-192
follow-up data rather than a new duplicate ticket. Does not change this
ticket's own honest "partially fixed, root cause unidentified" status —
this only confirms the *shipped* safety net's write-ordering closes a
second, independently-reported symptom shape, not that the original
unreproduced root cause is understood.
