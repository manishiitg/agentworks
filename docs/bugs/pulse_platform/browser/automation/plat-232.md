[← platform / browser-automation](index.md)

# PLAT-232 — `agent_browser click` success proves the event dispatched, not that a toggle control's state changed

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | browser |
| Area | automation |
| Summary | harness_issue, medium/high severity across findings. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `guidance mitigation applied; independent review wording corrections applied; live reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue, medium/high severity across findings.
- **Findings:** Twitter/social-media `PUL-B6B9F1D7`, `PUL-83421488`,
  `PUL-57F0FE8D`, `PUL-A319446F`, `PUL-BEE6AC69` — five independent
  occurrences across like and follow actions, converging on the identical
  mechanism and the identical recovery recipe without coordinating with each
  other.

## The pattern

`agent_browser click` on a toggle-style control (X's like/follow buttons)
returns `success=true` — the click event genuinely dispatched — while the
live DOM's `aria-label`/`data-testid` on that exact element never changes:
the target still reads unliked / not-following after the call returns. Two
findings show a scoped DOM click (`data-testid=like`/`-follow` on the exact
article/control, not a fresh top-level click) recovering it immediately
after; one shows a concurrent HTTP 429 / X `code:88` rate-limit diagnostic
as a plausible real cause; the other two show the same unchanged-state shape
with no rate-limit evidence. All five workflow runs independently
discovered and applied the same defensive recipe: dispatch, re-read the
exact element (not a generic re-snapshot), retry once with a scoped DOM
click if unchanged, and refuse to record success without a real state
transition.

## Why this is not an in-repo click-mechanism fix

Checked first, matching the investigation already done for PLAT-224:
`agent_browser` is invoked as an installed external binary
(`agent_go/pkg/browser/executor.go`), and `"click"` has no dedicated
handling in this repo's Go wrapper at all — it is a pure passthrough, same
as `"network"` was for PLAT-224. There is no Go-side hook this repository
owns that could add post-click DOM verification to the click mechanism
itself; that would require the external CLI's own source.

## Fix

Added a dedicated, prominent section to `browser-usage.md` (the guidance
skill every browser-driving step loads) naming the exact failure mode and
codifying the four-step recipe all five findings independently converged
on, rather than leaving each workflow to rediscover it per incident. This
is a materially more specific lesson than the pre-existing generic
"re-snapshot after every interaction" bullet — that one doesn't warn that
the click *response itself* is unreliable for confirming a toggle action's
semantic effect, only that cached refs go stale.

## Deliberately not folded into this ticket

`PUL-7074FD09`/`PUL-5A977094` (X quote-compose navigating to a route with no
quote embed or enabled submit control) looked adjacent at first glance —
same workflow, same general "X UI didn't do what the click implied"
shape — but the actual symptom is different: a composer failing to render
expected content after navigation, not a toggle control silently not
flipping. Left open as a separate, still-uninvestigated item.

## Verification

`go test ./cmd/server/guidance/...` passes (template renders clean). No
Go code changed — this is a guidance-only fix, consistent with PLAT-224's
conclusion that the click mechanism itself is outside this repository's
reach.

## Reverify

No live step has loaded this guidance section through the deployed server
yet. Reverify by observing whether a future like/follow action that hits
this exact shape now applies the documented verification recipe before
recording success.

## Independent review (2026-08-29)

Consolidating the five like/follow findings and keeping the two quote-compose
findings separate is sound. The guidance mitigation is also directionally
correct: a successful click command is insufficient evidence that the
intended durable state changed.

Three accuracy corrections remain:

1. The platform wrapper proves that the external click command completed
   successfully; it does not itself prove that a page event was genuinely
   delivered. Guidance should say command completion, not event dispatch.
2. The instruction to inspect the "exact same element" must explicitly require
   a fresh scoped snapshot or stable-selector re-query. Reusing the old `@eN`
   reference would contradict this same document's stale-reference rule after
   interactions.
3. The external CLI source is unavailable, but an in-repo improvement is not
   categorically impossible. The owned wrapper could eventually expose an
   opt-in click-and-verify operation with a caller-supplied postcondition.
   Guidance is an appropriate immediate mitigation; it should not be described
   as proof that no platform-side improvement can ever exist.

These points do not invalidate the five SQLite resolutions under the current
fix-applied closure policy, but they keep the remaining guidance/runtime
boundary explicit.

**Corrections applied (2026-08-29):** reworded `browser-usage.md`'s section
heading and opening paragraph to say the `success` field proves *command
completion*, not event dispatch; step 2 of the recipe now explicitly
requires a fresh scoped `snapshot`/`get` or stable-selector re-query, naming
that reusing a stale `@eN` would contradict the document's own
refs-go-stale rule; and the closing paragraph no longer claims no
platform-side fix could ever exist — it now names the concrete possible
future improvement (an opt-in click-and-verify operation with a
caller-supplied postcondition) while keeping this recipe as the current
mitigation. `go build ./...` and `go test ./cmd/server/guidance/...` pass.

**Second correction applied (2026-08-29):** the recipe's step 2 was itself
internally contradictory. It named `data-testid` as both a *stable
selector* to re-locate the element ("a stable-selector re-query (e.g. its
`data-testid`)") and a *value expected to change* as proof of the action
landing — but on X's real like/follow controls, `data-testid` literally
flips between two values (e.g. `"like"` → `"unlike"`) as part of the
state change, so it cannot double as a fixed selector across that
transition: re-querying by the pre-click `data-testid` after a genuinely
successful action would no longer match anything. Reworded step 2 to
locate the element by stable characteristics (position/role/surrounding
text) in the fresh snapshot, separately from reading its now-current
`data-testid` value as one of the two state signals to check. Step 3's
retry no longer references building a selector from the "exact
`data-testid`," which had the same problem. No test changes needed — this
is a guidance-only wording fix; `go build ./...` and `go test
./cmd/server/guidance/...` pass.
