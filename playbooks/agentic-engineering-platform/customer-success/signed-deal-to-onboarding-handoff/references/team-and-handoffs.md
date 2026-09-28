# Sales to Customer Success handoff

## Route and owners

1. Builder checks whether an approved handoff already exists for the contract revision and whether the current New Customer to First Value workflow can consume it. Reuse matching Crews and one shared workflow where access permits; separate the Sales preparation route if contract access cannot be granted to CS.
2. Sales reads the exact CRM opportunity and executed agreement. It resolves purchased scope and the first-value promise from approved agreement or order-form terms, recording any conflict with sales notes. It emits `sales-cs-handoff/v1` with a stable key, contract revision and source references. Closed Won without execution or a provisioning rule yields `blocked`.
3. A blocking validator runs before CS receives the bounded artifact. CS re-reads the current contract revision, entitlement and account. It emits `onboarding-acceptance/v1` as `needs_resolution` when the scope, revision, entitlement, owner or first-value definition is unresolved.
4. The receiving CS owner reviews exact facts and records acceptance with an owner receipt. Only an accepted handoff is eligible to feed New Customer to First Value. That route still builds milestones and verifies first value independently.
5. Any later provisioning, customer message or CRM change has its own approval and provider receipt. An event trigger only opens a new read; it cannot accept a handoff by itself.

## Identity, access and repeat

The handoff key is stable for tenant, opportunity, contract and revision. The receiving review cites the exact upstream artifact and matches tenant, CRM account, opportunity, customer account, contract ID/revision, purchased scope and agreed goal. Current CRM and contract revisions must match those used by Sales. A repeat checks prior handoff keys and starts a fresh review if the contract or owner changed. Keep contact details and contractual documents in their authorized stores; share only the minimum bounded summary.

## Example acceptance

The fictional deal is signed and provisioning is authorized, but the receiving owner must still confirm current entitlement and the promised outcome. The [accepted case](../examples/onboarding-acceptance.json) can feed an [onboarding register](../examples/onboarding-register-from-accepted.json) only after the first-value Playbook validates their exact account, scope, rule, target and artifact reference. The blocked case has a pending entitlement and no owner acceptance. The rejected case claims acceptance despite a revised contract and must fail validation. None of the examples activates onboarding or proves customer value.
