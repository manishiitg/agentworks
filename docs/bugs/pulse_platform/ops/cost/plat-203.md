[← ops / cost](index.md)

# PLAT-203 — pi-cli-routed models had no rate card at all, and a genuinely-zero cost was indistinguishable from "we don't know"

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | ops |
| Area | cost |
| Summary | P2 — cost figures silently absent (not zero) for every |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` |
| Last synchronized | `2026-08-29` |

- **Priority:** P2 — cost figures silently absent (not zero) for every
  pi-cli-routed call, which "could mislead operations review into
  under-counting cycle cost for any step pinned to a non-Claude model" per
  the finding's own impact assessment.
- **Owner:** `agent_go/pkg/orchestrator/base_orchestrator_tokens.go`
  (`calculatePricingFromModelData`), `agent_go/pkg/orchestrator/cost_storage.go`
  (`buildModelTokenUsage`, `EnsureModelTokenUsagePricing`),
  `agent_go/pkg/orchestrator/base_orchestrator_types.go` (`ModelTokenUsage`).
- **Related:** `harness:cost-ledger:reflection-attribution-and-call-count`
  (confida-login, medium; `classification: "no_issue"`,
  `recommended_route: "evidence_wait"` in the harness's own self-assessment
  — this ticket found it was in fact a real, confirmed gap). Same class of
  zero-vs-unset ambiguity `costledger.Aggregate` (the newer, parallel
  cost-tracking system — see PLAT-184) already solved via
  `BillingBasis="unpriced"`/`UnpricedCallCount`; this ticket applies the
  same pattern to the older, still-live `ModelTokenUsage`/`cost_storage.go`
  system the finding's cited file (`costs/execution/<workflow>/<date>.json`)
  actually uses.

## The finding

`costs/execution/confida-staging/2026-08-24.json`'s `by_step_and_model` for
`execute-browser-and-capture-apis` showed `google/gemini-3.7-flash` with
`llm_call_count=4` present but **no `total_cost_usd` key at all** in that
model block.

## Root cause — two compounding gaps, both confirmed in code

1. **pi-cli has no rate card at all, for any model.**
   `picli.PiCLIAdapter.GetModelMetadata` (`multi-llm-provider-go/pkg/adapters/picli/picli_adapter.go:90-104`)
   returns `*llmtypes.ModelMetadata` successfully (no error) but never sets
   any `*CostPer1MTokens` field — not specific to `gemini-3.7-flash`, every
   model this adapter serves gets the same empty pricing. Confirmed
   `gemini-3.7-flash` is in fact the adapter's own current default model
   (`DefaultModelID`, `picli_adapter.go:13`), so this isn't a stale/missing
   entry for an unusual model — it's a structural gap for the whole
   provider.
2. **A genuinely-zero cost and an unknown cost were indistinguishable.**
   `ModelTokenUsage.TotalCost` had `json:"total_cost_usd,omitempty"` — when
   `calculatePricingFromModelData` fell through to all-zero costs (metadata
   missing, or present but empty as in case 1), the JSON key simply
   vanished, identical to what a genuinely free call would produce.

## Fix

`calculatePricingFromModelData` now returns a `pricingFound bool`: true
only when the resolved model metadata actually carries a nonzero rate on at
least one of `Input`/`Output`/`Reasoning`/`CachedInput`/`CachedInputWrite`
cost-per-1M-token fields — not merely "metadata lookup didn't error."

`ModelTokenUsage` gained `Unpriced bool` (`omitempty` — a priced call's JSON
is byte-identical to before this change) and `TotalCost` lost its
`omitempty` (a priced call already always has a real total; an unpriced
call now explicitly shows `"total_cost_usd":0,"unpriced":true"` instead of
a missing key). Both call sites (`buildModelTokenUsage`,
`EnsureModelTokenUsagePricing`) set `Unpriced` from `pricingFound` and log
a `[COST]` line naming the exact provider/model with no rate card, so a
recurrence is immediately debuggable instead of silent.

## Explicitly not done

- Did not add real per-token pricing for `google/gemini-3.7-flash` or any
  other pi-cli model. That requires knowing pi-cli's actual billing model
  (subscription passthrough vs. metered vs. genuinely free tier) with
  confidence — guessing a dollar figure would be worse than an honest
  "unpriced" marker, since a plausible-looking wrong number is harder to
  catch than an explicit unknown. This is now a well-scoped follow-up if
  pi-cli's billing terms are confirmed.
- Did not change the update-condition in `EnsureModelTokenUsagePricing`
  (still gates writing pricing fields on `inputCost>0 || ... || totalCost>0`,
  not on `pricingFound`) — changing that would also affect a real rate-card
  model whose token counts happen to be zero, which no existing test
  covers; scoped this change to exactly what the finding needed.
- Did not touch `costledger`/`costobserver` (the newer, parallel system) —
  it already has its own equivalent `BillingBasis`/`UnpricedCallCount`
  disambiguation and was not what the finding's cited file used.

## Verification

- `go build ./...` clean.
- New tests `TestUnpricedProviderCallsAreExplicitNotAbsent` and
  `TestPricedProviderCallsAreNotMarkedUnpriced`
  (`pkg/orchestrator/unpriced_model_cost_test.go`) prove: a pi-cli/gemini
  call is marked `pricingFound=false`, `Unpriced=true`, and its JSON
  explicitly shows `"total_cost_usd":0,"unpriced":true`; a real
  `claude-sonnet-5` call keeps `pricingFound=true`, `Unpriced=false`, a real
  nonzero total, and — critically — **no `unpriced` key at all** in its
  JSON, so every existing priced-call consumer sees a byte-identical
  payload to before this change.
- Existing `TestPhasePricingUsesImmutableClaudeModelRateCards` (Claude
  rate-card exactness) passes unchanged.
- Full suite: 3 pre-existing failures before and after (confirmed via `git
  stash`), all in `cmd/server/guidance`, none touching this ticket's
  packages — no regression.
