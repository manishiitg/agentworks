# PLAT-521 — Global browser capacity eviction stops unrelated sessions

State: open. Date: 2026-10-05. Priority: P2. Source: owner-supplied browser review.

## Verified review

`HandleAgentBrowser` in `executor.go` checks per-agent/workflow limits first,
then calls the global `GetOldestSession` at capacity. That selector has no owner
or workflow predicate. `autoEvict` takes the victim's control lock, skips current
recording sessions, kills its runtime and removes it from tracking.

Thus an idle non-recording browser belonging to another workflow can be killed
by a new open. Active automation/manual control and recordings are protected by
the existing lock/recording guards. This affects tracked managed headless
sessions, not extension bindings or direct-CDP sessions, which bypass this
tracker. Default global capacity is eight and can be configured.

The report's mechanism is confirmed by source review; it was not reproduced by
filling a live server's browser slots. The action is already logged through
BROWSER_TRACKER, so describing it as entirely silent is inaccurate.

## Remaining

Keep reclamation within the requesting authorized owner/project, or reject the
new open with BROWSER_CAPACITY when only unrelated victims exist. Recheck
eligibility under the victim lock and make admission atomic so concurrent opens
cannot exceed capacity. Verify existing owners survive another owner's open at
capacity through the real executor path; add operator visibility for rejections.

No runtime fix or deployment is claimed by this ticket.
