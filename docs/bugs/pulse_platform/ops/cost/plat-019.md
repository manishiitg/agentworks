[← platform / cost-telemetry](index.md)

# PLAT-019 — Pulse agent metrics capture usage but leave every call unpriced

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | ops |
| Area | cost |
| Summary | Pulse agent-metrics accounting and pricing projection |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** Pulse agent-metrics accounting and pricing projection
- **Source run:** RTS Latency
  `schedule-cron--42eca39a_1785810615371091000`
- **Problem:** `pulse_agent_metrics` contains complete usage for Strategy
  Auditor, Workflow Review, and the consolidated Fixer, including roles,
  durations, call counts, model IDs, prompt/completion/cache tokens, and
  `usage_status='captured'`. All three rows nevertheless store
  `total_cost_usd=0`, and each model payload marks every call unpriced. The
  phase and authoritative execution ledgers price the same `claude-opus-5` and
  `claude-sonnet-5` identities with versioned non-zero rate cards.
- **Impact:** the Pulse UI can show time and tokens but reports zero dollars for
  the most expensive agents, while another ledger reports a real cost. Review
  economics cannot be compared reliably and cost-reduction decisions are made
  from contradictory projections.
- **Distinction from PLAT-008:** PLAT-008 fixed phase-ledger arithmetic and model
  rate-card selection; that arithmetic is working here. PLAT-019 is the failure
  to apply the same canonical pricing identity when persisting
  `pulse_agent_metrics`.
- **Implementation (2026-08-04):** cost aggregates retain the provider and the
  token slice belonging specifically to unpriced calls. Pulse metrics apply
  the canonical versioned model rate card only to that slice, preserve existing
  priced cost, and expose component costs/pricing identity. Unknown models are
  recorded as `captured_unpriced` with a non-empty explanation.
- **Verification:** a focused SQLite-ledger test captures an unpriced
  `claude-sonnet-5` Pulse call and proves a non-zero versioned metric result.
  The next real Pulse pass must reconcile the displayed dollars to the phase
  ledger.
- **Regression test:**
  `TestPulseAgentMetricsPricesCapturedClaudeUsageWithCanonicalRateCard`; the
  full costledger and step-based workflow packages pass on 2026-08-04.
- **Acceptance:** for a priced model, each Pulse metric row reconciles to the
  same versioned component costs as the phase/execution ledger. Truly unpriced
  models remain explicit with a non-empty reason; `usage_status='captured'`
  must not silently imply `$0`.
