[← browser / automation](index.md)

# PLAT-231 — the two CDP tab-creation errors are correct input validation, not a harness defect; not PLAT-028

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | automation |
| Summary | P2 — Upwork `PUL-13197E02` (per the |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `investigated — reclassified, harness working as designed` |
| Last synchronized | `2026-08-29` |

- **Priority:** P2 — Upwork `PUL-13197E02` (per the
  [2026-08-29 triage audit](../../../../audits/pulse-platform-triage-2026-08-29.md)).
- **Related:** [PLAT-028](plat-028.md) — the audit itself flagged this as "a
  candidate, but not a proven duplicate," and asked to reproduce before
  linking or closing.

## The finding

`bid-submit` hit two CDP tab-acquisition errors in the same run before
succeeding on retry, with no submission lost: *"browser navigation target
\"new\" must be..."* and *"CDP shared-browser tab creation require..."*. The
finding's own severity was low, and its own limitation note said root cause
(workflow-authored vs. harness) was unconfirmed from single-run evidence.

## Not PLAT-028

Checked directly: PLAT-028 fixed a *recovered bare tab id leaking into a
later page-action's element-reference argument* (`click t2 e64` instead of
selecting tab `t2` and clicking element `e64`) — a routing/argument-plumbing
bug. This finding's two errors are about *tab creation itself* failing, a
different code path entirely (`agent_go/pkg/browser/cdp_tabs.go`, not the
page-action argument routing PLAT-028 touched). The audit's own uncertainty
about linking them was correct to flag and is now resolved: they are not the
same mechanism.

## Root cause, confirmed in code

Both error strings are exact matches to deliberate input validation in
`cdp_tabs.go`, not any kind of transient or race condition:

- `"CDP shared-browser tab creation requires --label <label> so later
  commands can target it"` (`parseNewCDPTabRequest`, `cdp_tabs.go:584`) —
  fires when a `tab new` call omits the required `--label` argument. This is
  the harness correctly rejecting a call that is missing a mandatory
  parameter needed for the created tab to be addressable by any later
  command.
- `"browser navigation target %q must be an absolute URL with an explicit
  scheme such as https://; a CDP tab id such as t9 is not a URL"`
  (`cdp_tabs.go:1044`) — fires when an `open`/`goto`-style call receives
  something that is not a valid absolute URL, explicitly named in the error
  text as commonly a bare tab id passed where a URL was expected.

Both are the harness working exactly as designed: reject a malformed call
with a specific, actionable error rather than accepting it silently or
failing opaquely. Neither is a bug to fix in this repository.

## Conclusion

The finding's own open question — workflow-authored vs. harness — resolves
to workflow-authored. `bid-submit`'s own tab-handling call was malformed on
its first attempt (missing `--label`, or passing a tab id where a URL was
expected), the harness correctly rejected it with a clear error naming the
exact problem, and the step recovered by retrying with a corrected call —
exactly the self-correction behavior specific, actionable error messages are
meant to enable. "Adds retries/latency" describes the cost of the workflow's
own first malformed call, not evidence of a harness defect. Reclassified;
belongs to Upwork's own `bid-submit` step content if worth tightening
(fewer retries), not to this repository's platform-code queue.

## Verification

N/A — no files changed. Investigation and reclassification record.
