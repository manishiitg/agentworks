[← platform / frontend-chat](index.md)

# PLAT-318 — Plan loading serializes requests and can apply stale workflow responses

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | platform |
| Area | frontend-chat |
| Summary | The Plan view waited on plan.json and then step_config.json. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Implemented and regression-tested; release verification pending |
| Last synchronized | 2026-09-12 |

The Plan view waited on plan.json and then step_config.json. Shared cache-promise
consumers used a different loading/error path, and late results from a prior
workflow could update the newly selected workflow's state.

Plan/config reads now run concurrently, with 20-second per-read timeouts. Both
fresh and shared loads clear their loading state; cached results clear a previous
spinner. Results are applied only while their workspace remains current. Optional
step-config absence retains existing behavior. A DOM hook test covers concurrent
fetch, shared requests, workflow switching and late-response isolation.

During investigation RTS load was 0.86 with about 12 GB available memory; a local
workspace API read of rts-latency plan.json returned HTTP 200 in 3 ms. These are
point-in-time diagnostics, not a full public browser performance benchmark.
Type checking, targeted lint and the loader regression passed. A production
browser timing comparison remains unverified.

## 2026-09-12 deployment confirmation

The latest follow-up above is included in RTS release `3a37a1c-20260912143119`
(app `3a37a1c75`). Source revisions and all three services were verified after
activation. The configured-environment Linux sandbox regression passed.
Earlier pending-deployment notes are superseded; feature-specific live acceptance
limits remain as documented. No user accounts/sharing or notification recipients
were changed during verification.
