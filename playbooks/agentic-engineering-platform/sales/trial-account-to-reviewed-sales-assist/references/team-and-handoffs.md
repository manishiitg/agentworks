# Trial account team and handoffs

This Playbook is a Builder proposal. Installation copies guidance and a pending checklist; it creates no Crew, connection, message, CRM write or schedule.

## Route

1. **Observe the trial.** Product Adoption Analyst reads the current exact trial/subscription and authorized product events. It applies the owner-defined use rule and window, excludes internal/test/duplicate events, and records coverage. Emit `trial-usage-observation/v1` for one entity, product, tenant, account, trial and subscription. A complete window with no qualifying event is `not_observed`; partial or unknown coverage is `unknown` unless a qualifying event was actually observed.
2. **Gate the handoff.** Save plain JSON as the producer Crew's `context_output`, add a structural `validation_schema`, and run `scripts/validate_handoff.py` before Sales. The structural gate catches malformed output; the business validator checks identity and state rules.
3. **Decide Sales action.** Sales Follow-up Coordinator re-reads current trial/paid state and the CRM account mapping. It inspects the exact contact, channel permission, suppression, previous messages/replies and booked meetings. A permitted, seller-reviewed account may get an unsent assistance draft. Unknown permission or fit becomes `needs_review`; blocked, converted or previously answered cases become `no_contact`.
4. **Act and observe separately.** An owner reviews the exact recipient and message. Before any authorized send, re-read contact and trial state and use a stable message fingerprint and action ID. A provider receipt proves delivery; a matched calendar/CRM event proves booking; the billing source proves paid conversion. None follows from the draft.

## Stable joins and repeat rule

Join producer and consumer on entity, product, tenant, product account, trial and subscription IDs plus the exact usage artifact ID. The CRM mapping record must explicitly connect the product account to the CRM account. The recipient must be a current CRM contact under that account and the permitted channel. Company names, email display text and one product event do not establish identity or consent. Re-read trial status revision and contact state on each run; retain earlier decisions and provider receipts so a retry does not resend.

## Local contract exercise

Run `python3 scripts/test_validate_handoff.py`. For a saved Workflow, run `python3 scripts/validate_handoff.py <trial-observation.json> <sales-assist.json>` against the actual artifact paths before the Sales step completes. The bundled fixtures are fictional; passing them does not verify customer source access, permission or an owner decision.
