# Renewal health to owned contract decision

## Route

1. Builder inspects existing Crews, health sources, agreement and subscription access, notice policy, owner and any prior renewal workflow. Reuse capabilities and preserve account permissions. A manual export can support the first read-only result.
2. Customer Health Coordinator emits `renewal-health-brief/v1` for an exact account and bounded window. Every observed signal cites a source; a relationship concern without confirmation is a hypothesis. Missing usage or support coverage stays visible. Validate before handing it to Renewal.
3. Renewal Coordinator re-reads the executed agreement and current subscription. It calculates the notice deadline in the policy timezone, compares it with the review cutoff, checks current billing and prior notice records, and emits `renewal-decision-register/v1`. The decision cites the exact health artifact and contract revision. A missing executed term or provider read yields `needs_terms` with no owner acceptance.
4. A blocking validator runs before the dashboard or any next route. The renewal owner may accept a bounded next action such as investigate billing, review terms or prepare customer discussion. Customer contact, contract changes, credits and CRM writes each require a separate reviewed exact-object action with a provider receipt.

## Identity and repeat

The health brief and register match tenant and account. The register binds legal entity, contract ID/revision and subscription, and carries the exact source health artifact ID. Keep a stable case key for the account/contract revision. A new case must be absent from the key ledger; an update cites the previous register artifact and retains the same key. Re-read source revisions and prior notice state before another decision; an amended agreement invalidates the old calculation. Preserve earlier owner decisions and action receipts rather than rewriting history. This v1 calculator handles explicit calendar-day notice terms; other contractual notice rules stay `needs_terms` until the owner supplies a reviewed calculation.

## Example acceptance

The fictional example has first value observed and one training question, both with source coverage. The executed agreement has a 60-day UTC notice rule; the current invoice is open. The route prepares a pending owner decision six days before notice. The unknown-health example keeps missing usage as unknown. The pending-terms example cannot claim an owner-reviewed renewal choice. The rejected example calls a passed date and open invoice a verified cancellation; the validator stops it. None proves customer intent or an actual renewal outcome.
