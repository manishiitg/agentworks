[← browser / automation](index.md)

# PLAT-235 — Internal CDP tab-listing helper had zero retry on timeout, unlike every other read command

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | browser |
| Area | automation |
| Summary | harness_issue, severity high across findings. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; canceled-context edge case fixed; runtime reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue, severity high across findings.
- **Findings:** Twitter/social-media `PUL-5629287B`, `PUL-0752CD98` (the
  latter itself an aggregation of 6 already-deduped observations —
  `PUL-19A355A8`, `PUL-4BE982AB`, `PUL-B8569A93`, `PUL-4071B4A8`,
  `PUL-C0FD029A`, `PUL-1234D778` — across intent queue, targeting audit,
  verify, profile health, unfollow cleanup, and remediation steps).

## The pattern

Every finding shares the identical shape: a Stage-0 CDP connection test
passes (`connected=true`, `twitter_visible=true`, `handle_match=true`), then
a later step's authenticated-tab lookup fails with the managed CDP tab
listing timing out after exactly 15 seconds, leaving only a stale
remembered tab ID available. The workflow's own safe-defer behavior — block
public actions, skip verification, report the unavailable state honestly —
is explicitly called out as correct in both findings. The gap is that a
transient 15-second listing timeout, against an endpoint proven reachable
minutes earlier, cost an entire run's worth of actions with no attempt to
recover.

## Root cause

`agent_go/pkg/browser/executor.go` has a general CDP command dispatcher that
already retries certain read-only commands once on timeout
(`shouldRetryCDPTimeout`: `skills`, `snapshot`, `get`, `is`, `screenshot`,
`console`, `errors`) — deliberately excluding commands with side effects,
like `wait`. `"tab"` is excluded from that set too, but for a different
reason: the same command name also covers `tab <id>` (switches focus,
side-effecting) and `tab new` (creates a tab, side-effecting), so it can't
simply be added to the shared allowlist without also making those
side-effecting forms retry-eligible.

The actual internal helper this repo uses everywhere it needs the tab list
for an internal decision — `listCDPTabs` — never goes through that
dispatcher at all. All 6 production call sites (three inside
`HandleAgentBrowser`, plus `listCDPTabsForUser`,
`selectCDPTabForCommand`, and `reuseCDPTabForNew`) call
`e.Client.ExecuteCommand` directly with the bare `tab --cdp <url> --json`
list form and a hardcoded 15-second timeout, with no retry whatsoever. That
bare form has no id/selector argument and is always side-effect-free — the
exact class the dispatcher's retry logic exists for — but it never got that
protection.

## Fix

Added a single internal retry directly inside `listCDPTabs` (all 7 call
sites funnel through this one function): on a command-timeout error, sleep
500ms and retry once, mirroring the dispatcher's own existing
timeout-retry shape for other CDP read commands. This is scoped precisely
to the always-side-effect-free bare-list form and does not touch
`shouldRetryCDPTimeout`'s shared allowlist, so `tab <id>`/`tab new` remain
correctly excluded from blanket retry.

## Verification

Two new regression tests in `cdp_tab_list_retry_test.go`:
`TestListCDPTabsRetriesOnceAfterTimeout` (a listing that times out once
then succeeds on retry returns the recovered result, exactly 2 attempts)
and `TestListCDPTabsDoesNotRetryASecondTime` (a listing that keeps timing
out surfaces the error after exactly 2 attempts, not an unbounded loop).
`go build ./...` and `go test ./pkg/browser/...` pass.

## Reverify

No live step has exercised this retry against a real transient CDP timeout
yet. Reverify by observing whether a future authenticated-tab lookup that
would previously have hit the bare 15-second timeout now recovers on the
internal retry instead of forcing safe-skip/block behavior for the rest of
the run.

## Independent review (2026-08-29)

**Root cause: checks out.** `shouldRetryCDPTimeout` at
`agent_go/pkg/browser/executor.go:1502-1509` really is `skills`, `snapshot`,
`get`, `is`, `screenshot`, `console`, `errors` only, and the general
dispatcher's own timeout-retry (`executor.go:978-990`) really does sleep
500ms and retry exactly once for commands in that set. `listCDPTabs`
(`executor.go:1309-1323`) really did bypass that dispatcher — it calls
`e.Client.ExecuteCommand` directly with the bare `tab --cdp <url> --json`
form — and, before this fix, had no retry of its own.

**Fix: correctly scoped.** The added retry (`executor.go:1316-1321`) is
isolated inside `listCDPTabs` itself, fires only on
`isCommandTimeoutError(err)`, sleeps 500ms, retries exactly once, and does
not touch `shouldRetryCDPTimeout`'s shared allowlist — so `tab <id>`/`tab
new` remain correctly excluded. Both new tests in
`cdp_tab_list_retry_test.go` pass as described:
`TestListCDPTabsRetriesOnceAfterTimeout` (2 attempts, recovers) and
`TestListCDPTabsDoesNotRetryASecondTime` (2 attempts, bounded, surfaces the
error). Ran `go build ./...` and `go test ./pkg/browser/... -run
ListCDPTabs -v` independently — both pass.

**Wording overreach: the call-site count is off by one.** The ticket
(and the matching register row) claims "every one of its 7 call sites."
Grepping `agent_go/pkg/browser/` for `.listCDPTabs(` outside test files
finds exactly 6 production call sites: `executor.go:866`, `:1032`,
`:1121` (three separate calls inside `HandleAgentBrowser`), `:1326`
(`listCDPTabsForUser`), `:1363` (`selectCDPTabForCommand`), and
`cdp_tabs.go:798` (`reuseCDPTabForNew`). The three named examples
(`reuseCDPTabForNew`, `listCDPTabsForUser`, `selectCDPTabForCommand`) are
all real and accurate; the total is 6, not 7. Does not affect the fix's
correctness since the retry lives centrally inside `listCDPTabs` and every
caller benefits regardless of the exact count, but the number should be
corrected.

**Missed/under-disclosed edge cases:**
1. `isCommandTimeoutError` (`executor.go:1490-1500`) treats `"context
   canceled"` as a timeout, not just `"context deadline exceeded"` /
   `"command timed out"`. If the retry fires because the *caller's*
   context was canceled (e.g. the parent request was aborted) rather than
   because the CDP endpoint was genuinely slow, `listCDPTabs` still sleeps
   500ms and reissues the command, which will almost certainly fail
   immediately again for the same reason. This doesn't break the "exactly
   2 attempts" bound the tests pin, but it's a real difference from the
   ticket's framing of the retry as recovering "a transient 15-second
   listing timeout" — a canceled-context "timeout" is not transient and
   the extra 500ms is pure waste in that path.
2. The ticket frames the fix as "mirroring the dispatcher's own existing
   timeout-retry shape," but the dispatcher's retry (`executor.go:978-990`)
   also calls `guardCDPAutomaticRecovery`/`resetCDPSessionRuntime`/
   `clearCDPActiveTabForPort`/`clearCDPExclusiveFeaturesForPort` before
   its retry attempt, while `listCDPTabs`'s retry (`executor.go:1316-1321`)
   does none of that — it only sleeps and reissues the identical command.
   That's a defensible simplification for a side-effect-free bare list
   call, but it means the fix mirrors only the sleep-then-retry-once
   *mechanic*, not the dispatcher's full recovery behavior, and the ticket
   should say so rather than imply a closer parallel.

**Corrections applied (2026-08-29):**
1. Call-site count fixed to 6 (three inside `HandleAgentBrowser`, plus
   `listCDPTabsForUser`, `selectCDPTabForCommand`, `reuseCDPTabForNew`),
   here and in the register row.
2. `listCDPTabs`'s retry now uses a narrower `isGenuineCDPListingTimeout`
   check instead of the shared `isCommandTimeoutError` — excludes
   `"context canceled"` specifically, so a caller-aborted request fails
   immediately instead of paying a useless 500ms sleep before failing
   again for the same reason. New test
   `TestListCDPTabsDoesNotRetryOnCallerCancellation` asserts at most 1
   attempt and completion well under 500ms for an already-canceled
   context.
3. The doc comment on `listCDPTabs` now says explicitly that only the
   sleep-then-retry-once *mechanic* mirrors the dispatcher's retry, not
   its session-recovery steps (`guardCDPAutomaticRecovery` etc.), which
   this retry deliberately skips.

`go build ./...` and `go test ./pkg/browser/...` pass (3 tests for this
fix total).
