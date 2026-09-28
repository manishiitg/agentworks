# Inbound Lead-to-Meeting Review: team and handoffs

## Team

| Slot | Crew template | Output | Minimum source |
| --- | --- | --- | --- |
| Qualification, required | `lead-intake-qualifier` | `lead-qualification-brief/v1` | Authorized inbound request, fit and routing policy, duplicate scope |
| Research, optional | `account-researcher` | `account-research-brief/v1` | Validated lead brief and approved company sources |
| Follow-up, required | `sales-followup-coordinator` | `sales-followup-draft/v1` | Validated lead brief, contact policy, prior-contact state, approved offer |

Reuse an existing Crew only after inspecting its selected skill, setup evidence, owner, and access. Keep distinct Crew IDs for Qualification and Follow-up. Add Research when account context changes the quality of a first reply; do not force three Crews onto a small team. The Research slot and its validator are implemented, but a customer may omit it.

## First manual route

1. Qualification reads one bounded inbound request and emits a lead brief with stable IDs, evidence, fit, duplicate and contact policy state. It does not create a CRM record or contact the person.
2. A script step runs `python3 skills/agentworks-playbook-inbound-lead-to-meeting-review/scripts/validate_sales_artifact.py qualification path/to/lead-qualification-brief.json --require-ready` from the Workflow workspace. Use the actual installed skill path if renamed. This validates structure and blocks a handoff for not-fit, contact-blocked, or duplicate-to-review cases. The valid internal disposition can still be reported without running a consumer.
3. If Research is selected, give it only the validated lead brief and approved company scope. Validate its output with `research path/to/account-research-brief.json --qualification path/to/lead-qualification-brief.json` before Follow-up consumes it.
4. Follow-up receives the validated brief, optional validated research, and its own authorized contact history and offer sources. It emits a **draft**, with recipient reference, cited claims, approval owner and next check. Validate it with `followup path/to/sales-followup-draft.json --qualification path/to/lead-qualification-brief.json` and add `--research path/to/account-research-brief.json` if Research ran.
5. The owner reviews the draft, booking URL, exact recipient, and next action. Save Crew run IDs, paths, validator results, source truth review and a stable action ledger entry. If delivery is configured and separately approved, follow [Booking and delivery routes](booking-and-delivery.md) for the fresh preflight, provider send, and meeting reconciliation.

The validator checks structure, references, lead/domain binding, state gating, exact draft fingerprint, and handoff scope. The send consumer also reads the actual saved approval and provider state; JSON validation alone cannot prove a source claim, legal permission, a send, or a booking. No account credentials or entire inbox export are passed between Crews. A repeat run re-reads lead, suppression, reply, and meeting state before proposing another touch.

## Artifact notes

The JSON examples in `../examples/` are fictional and contain no real contact address. `contact_ref` points to an authorized source record; it is not an email address. A brief's `fit_status` is `qualified`, `needs_review`, or `not_fit`. A `possible` or `known` duplicate and `blocked` contact policy prevent an outbound draft route. Research findings label observed facts, owner-provided facts, and hypotheses. The follow-up artifact always has `status: draft`, `send_state: not_sent`, and `approval_required: true`. Passing validation is not permission to send.

After an actual send, validate with `python3 skills/agentworks-playbook-inbound-lead-to-meeting-review/scripts/validate_sales_artifact.py delivery path/to/sales-delivery-receipt.json --qualification path/to/lead-qualification-approved.json --followup path/to/sales-followup-booking-draft.json` and add `--research` if the draft used research. After an observed calendar or CRM booking, validate `meeting path/to/sales-meeting-outcome.json` with the same qualification and follow-up arguments plus `--delivery path/to/sales-delivery-receipt.json`. An independently verified instant website booking uses `booking_mode: instant`, the matched qualification brief and booking session ID, and no delivery or draft arguments; it still requires a real provider event. Use actual customer artifact paths. The examples are fictional contract samples only.
