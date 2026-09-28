# Pipeline exception to seller decision handoff

## Journey

1. Builder inspects existing Pipeline Analyst and Deal Follow-through Coordinator Crews, owners, source grants, stage/currency definitions, stale policy and current contact rules. Propose reuse or reviewed creation.
2. Pipeline Analyst joins prior/current CRM snapshots on the stable opportunity ID. It records what actually changed and computes stale age against the current snapshot date. A stage move changes stage placement, not pipeline creation. It emits one `pipeline-exception-brief/v1` for an owner-reviewable opportunity.
3. Validate scope, snapshots, source coverage, stale arithmetic and exact opportunity identity before the coordinator sees it.
4. Deal Follow-through Coordinator re-reads the current CRM revision, activity, reply, meeting and opt-out state. If an intervening stage or owner change makes the brief stale, return `recheck` and ask Pipeline Analyst to refresh. Otherwise prepare an action key, seller options and an unsent `deal-action-register/v1`.
5. Validate the join, current-state observation, suppression rule and owner/action state. A CRM task, stage change or customer contact needs exact seller approval, fresh checks and a matching provider receipt in a separate route.
6. Repeat only for a comparable snapshot or changed opportunity state. Carry the same opportunity and action keys, close superseded suggestions and do not re-contact based on an old brief.

## Contract

Both artifacts share tenant, CRM account, pipeline, opportunity, reporting window and current snapshot ID. The coordinator cites the exact validated exception artifact. The exception must use two distinct, ordered snapshots, an owner-approved stale threshold, a parseable last activity date and a source coverage note. The action register must state the re-read CRM revision and observed time, activity source ID, stage, owner, current contact status and source coverage; a clear contact state also needs a contact source ID. A changed stage or owner, or a no-longer-stale opportunity, forces `recheck`; opt-out, recent contact and unknown contact coverage prevent a `contact_review` proposal. `external_action_state=none` has no approval or receipt. A performed action needs an exact approval and matching provider receipt. The validator checks mechanical claims; the seller decides treatment using the real records.

## Manual proof

Run one authorized opportunity with two comparable CRM snapshots and a fresh current opportunity and activity read. Save validator results, seller corrections, and action receipts or none. Fictional examples do not complete setup.
