[← goals / step-execution](index.md)

# PLAT-023 — diff patch repeatedly fails context matching on large files

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | `workspace/handlers/diff_patch.go` context matching and recovery |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Follow-up reviewer/fixer | `Codex` |
| Ticket state | `implemented` |
| Execution order | `B — second` |
| Last synchronized | `2026-08-04` |

> Claim this ticket as `in_progress` before implementation. Update this
> fragment during active work; synchronize the shared index at handoff.

- **Priority:** P1
- **Owner:** `workspace/handlers/diff_patch.go` context matching and recovery
- **Evidence:** six `could not find matching context lines` failures. One agent
  needed four additional tool calls to recover while editing a 150 KB file.
- **Sources:**
  [what_the_runtime_tells_an_agent_about_itself.md](../../../what_the_runtime_tells_an_agent_about_itself.md)
  and
  [diff_patch_unbounded_subprocess_hang.md](../../../diff_patch_unbounded_subprocess_hang.md)
- **Problem:** an ordinary stale or imprecise patch context becomes an expensive
  blind retry loop. This ticket concerns matching/recovery ergonomics, not the
  separate unbounded `patch(1)` subprocess-hang defect.
- **Implementation boundary:** determine whether matching should provide a
  bounded nearby-context hint, a structured mismatch location, or a safe retry
  contract. Do not silently apply an ambiguous hunk.
- **First pass (2026-08-04) — investigated, deliberately deferred by
  explicit user decision:** traced the full production fallback chain:
  auto-correct → strict patch → exact-content fallback scan → repeat both
  against the original diff. Only after all four fail does the agent see
  this message, and the code explicitly refuses to guess rather than risk
  corrupting a structured file — confirmed correct and left unchanged. The
  user then explicitly asked for the ticket's actual ask to be built.
- **Root cause found while building the hint (2026-08-04):** the fallback
  scan already computed `bestMatchIndex`/`minMismatches` across every
  candidate position, but its inner loop broke as soon as a candidate
  exceeded `maxAllowedMismatches` (0) — which capped every non-matching
  position's mismatch count at exactly 1, including the position genuinely
  closest to what the hunk intended. `bestMatchIndex` was therefore the
  first position scanned, not the closest one. A "closest match" hint built
  directly on that data would have been actively misleading. Fixed the break
  condition to "worse than the best candidate found so far," which cannot
  change whether any hunk applies (a true match still requires exactly 0
  mismatches) — it only makes the diagnostic data honest.
- **Implementation (2026-08-04):** `boundedContextMismatchHint` surfaces the
  file's real content at the closest position on failure, capped at both the
  hunk's own expected-line count and a 2000-byte budget, with the true
  mismatch count and 1-based file line number. Falls through to the original
  message, unchanged, when the file is shorter than the hunk.
- **Verification:** a 3000-line fixture reproducing the reported shape
  (stale context deep in a large file) asserts the hint contains the actual
  current content, the correct line number, stays bounded, and — the actual
  acceptance boundary — that a diff corrected using only the hint's own
  reported content succeeds on retry. A pathologically long single line
  proves the byte cap holds; a file shorter than the hunk correctly omits
  the hint. Verified the primary test fails against the pre-fix early-break
  scan (reports line 1, arbitrary) and passes against the fix. Full
  `workspace` module green; zero new lint findings in the changed files.
- **Regression tests:**
  `TestApplyAgentGeneratedDiffFallbackReportsBoundedContextOnLargeFileMismatch`,
  `TestApplyAgentGeneratedDiffFallbackHintStaysBoundedOnAPathologicallyLongLine`,
  `TestApplyAgentGeneratedDiffFallbackOmitsHintWhenFileIsShorterThanTheHunk`,
  in `workspace/handlers/diff_patch_test.go`.
- **Independent follow-up (Codex, 2026-08-04):** the first implementation
  retained the first minimum-mismatch position but did not count equally close
  positions. With two repeated near-matches, it could label an arbitrary block
  as the singular closest retry target. The scan now breaks only after a
  candidate becomes strictly worse than the best, counts ties, and refuses to
  emit an actionable content hint unless the nearest candidate is unique.
  `TestApplyAgentGeneratedDiffFallbackDoesNotRecommendAnArbitraryTiedNearMatch`
  covers the repeated-block case.
- **Acceptance:** focused large-file fixtures fail quickly and identify enough
  current context for one corrected retry; valid hunks remain exact, ambiguous
  hunks remain rejected, and the tool never rewrites unintended content. Met
  by direct fixture test; a real large-file agent recovery has not been
  observed post-fix, so this stays `implemented` rather than `done`.
