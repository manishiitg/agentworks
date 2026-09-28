# Support case team and handoffs

This package is a Builder proposal. Installation copies guidance and a pending checklist. It does not connect a helpdesk, send a reply, page a team, close a case, or enable a trigger.

## Route

1. **Triage:** Read the current authorized case, thread, prior contact, policy revision, and source references. Emit `support-case-triage/v1` for one tenant, case, account, and thread revision. Mark symptoms and unverified incidents separately.
2. **Validate:** Run `python3 scripts/validate_handoff.py triage examples/support-case-triage.json` before another Crew consumes the artifact. In the saved Workflow, add an explicit validator step; attaching a file alone does not invoke it.
3. **Reply:** Re-read current case and approved knowledge. Emit `support-reply-draft/v1` with the exact recipient, channel, draft text, evidence references, and `delivery_state: "unsent"`. Validate the triage and reply together.
4. **Escalate when warranted:** Emit `support-escalation-brief/v1` only with a receiving owner, deadline, acceptance state, and source evidence. A pending handoff must not be described as accepted. Validate the triage and escalation together. If Reply uses the escalation, validate all three.
5. **Act separately:** An owner reviews the exact message and recipient. Re-read the thread and prior deliveries, then send through a permitted provider route with an idempotency key. Validate the provider receipt. An approval or API attempt is not delivery.
6. **Observe:** Re-read the authoritative case source after action. Save case status, latest customer response, status revision, and observed time. Mark resolved only for an observed resolved status with a valid delivery receipt or an explicit approved no-contact resolution decision.

For the fictional fixtures, run the stages in order:

    python3 scripts/validate_handoff.py reply examples/support-case-triage.json examples/support-reply-draft.json
    python3 scripts/validate_handoff.py escalation examples/support-case-triage.json examples/support-escalation-brief.json
    python3 scripts/validate_handoff.py delivery examples/support-case-triage.json examples/approved-support-reply.json examples/provider-delivery.json
    python3 scripts/validate_handoff.py outcome examples/support-case-triage.json examples/approved-support-reply.json examples/provider-delivery.json examples/case-outcome.json

The approved reply fixture represents a later owner decision on the same unsent draft. The approved and delivered fingerprints bind tenant, case, account, thread, recipient, channel, and exact message. Builder must still verify the approval reference and provider receipt against their source systems; a matching local hash cannot prove either event occurred.

## Stable joins and freshness

Every artifact carries `tenant_id`, `case_id`, and `account_id`. Triage and reply also carry `thread_revision`. The consumer rejects a changed thread revision and re-triages. Names, email display text, or free-form summaries are never join keys. A response to a new customer message is a new review, not an automatic resend of the old draft.

Source references use stable IDs and approved revisions. The validator checks structure and joins, not the truth of external systems. Builder must read actual provider records and apply the configured freshness deadline. Fixtures here are fictional; passing them does not complete setup.

## Duplicate and authority rules

Use `tenant_id + case_id + thread_revision + message_fingerprint` as the contact idempotency scope. Save prior provider message IDs and check current thread before retry. Do not claim a response was sent from a local success flag; require a provider receipt for the exact draft and recipient. An escalation needs receiving-team acceptance before the handoff can be reported as owned. Case closure must be observed in the authoritative case source, and reopening supersedes earlier closure.

If the support and receiving teams have the same owner and access, one Crew may carry both packs. Separate Crews are useful for different source access, approval authority, lifecycle, or accountable owner. For a lightweight manual team, use chat with one Crew and the same evidence rules.
