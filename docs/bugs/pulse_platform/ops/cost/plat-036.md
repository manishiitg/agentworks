[← platform / cost-telemetry](index.md)

# PLAT-036 — context-usage percentage compares incompatible token scopes

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | ops |
| Area | cost |
| Summary | shared cost telemetry writer and its UI/API projection |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-07` |

- **Priority:** P2
- **Owner:** shared cost telemetry writer and its UI/API projection
- **Source workflow:** `Workflow/tectonicusadaytrading`
- **Source finding:** `HARNESS-COST-CONTEXT-PCT-SATURATION`

## Problem

`context_usage_percent` reports 100% for every recorded call. The recorder
divides cumulative `context_window_usage` by a single-call model context window,
so the numerator and denominator describe different scopes. Tectonicus recorded
643,364 cumulative tokens against a 200,000-token single-call window and still
rendered 100% everywhere.

## Impact

Pulse and the UI cannot distinguish normal context pressure from saturation.
This produces false LLM/Ops evidence and can drive unnecessary truncation,
model, or workflow decisions.

## Required fix

Persist and display either a same-call numerator/denominator pair, or mark the
metric unavailable when only cumulative usage exists. Never clamp an
incomparable ratio to 100%. Keep cumulative-session utilization as a separately
named metric if it is useful.

## Implementation — 2026-08-05

Coding-CLI adapters (Codex CLI and Claude Code, interactive and structured)
now mark transcript/CLI usage as **not** being a current-context snapshot.
`mcpagent` keeps that aggregate for pricing, but clears the context numerator
and omits the percentage/denominator from emitted telemetry. Native API
providers retain the existing current-prompt behaviour.

This deliberately reports context utilization as unavailable rather than
inventing a percentage from aggregate accounting. The focused mcpagent test
uses the observed 643,364-token/200,000-token shape and proves it no longer
emits a saturated percentage; Claude Code and Codex CLI transcript tests prove
the marker is carried across the adapter boundary.

The 2026-08-07 Upwork review tried to turn an absent coding-CLI
`context_usage_percent` into a telemetry regression. Reviewer guidance now
states that this absence is the intended representation when only cumulative
transcript usage exists. It must not be filed as a platform defect or used as
proof of context pressure.

## Acceptance

Two calls with different actual context utilization produce different
percentages; a cumulative-only record is labelled unavailable rather than
saturated; and a Pulse cost summary does not use an unavailable value as
optimization evidence. A fresh coding-CLI workflow run is still required to
complete runtime verification.
