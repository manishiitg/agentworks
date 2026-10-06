# PLAT-520 — Shared direct-CDP callers can select other owners’ tabs

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | browser |
| Area | browser |
| Summary | (conditional on mutually untrusted callers sharing a configured CDP browser), open. |

State: open. Date: 2026-10-05. Priority: P1 (conditional on mutually untrusted callers sharing a configured CDP browser). Source: owner-supplied browser review.

## Verified review

The submitted report's URL-reuse and label-disclosure paths are present in
`agent_go/pkg/browser/cdp_tabs.go`: `findReusableCDPTab` accepts label+URL or any
exact URL without filtering by owner, and `reuseCDPTabForNew` writes the caller's
alias/selection. The conflict error exposes the tab ID and full normalized URL.

The proposed owner-only reuse patch would not enforce isolation: `executor.go`
`listCDPTabsForUser` lists all targets, explicit `tab tN` is forwarded, and
`selectCDPTabForCommand` has no ownership authorization. Per-port locks prevent
races; ownership records govern bookkeeping/cleanup, not permission to a target.
The existing real-CDP fixture deliberately requires reuse of a pre-existing user
tab by exact URL without taking cleanup ownership. This is existing direct-CDP
behavior, not a demonstrated extension credential bypass.

The extension follows `extensionBinding` / `handleExtensionBrowser`, looks up
an account/workspace binding, uses a private capability and exposes only that
connection's shared targets. Its controller rejects another root chat. None of
the direct-CDP reuse functions are on that execution path.

No deployed cross-user exposure or live takeover was tested in this review.
Severity is high if mutually untrusted callers are granted the same direct-CDP
browser expecting tab privacy; a configured direct browser currently supplies no
such boundary.

## Remaining

Decide and enforce the direct-CDP target authority boundary consistently across
listing, creation/reuse, explicit selection, every page action and cleanup.
Preserve explicitly authorized user tabs while preventing automatic adoption of
another project's tab. Redact label conflicts. A two-owner real-browser check
must prove list/known-ID/URL/label routes cannot bypass the intended policy.
Until that work lands, do not present direct-CDP tab bookkeeping as a security
boundary; use separate browsers/endpoints or isolated extension connections.

No runtime fix or deployment is claimed by this ticket.

## Register notes

[PLAT-520](plat-520.md), P1 (conditional on mutually untrusted callers sharing a configured CDP browser), open. Source-reviewed; see ticket for guards, evidence limits and required fix.
