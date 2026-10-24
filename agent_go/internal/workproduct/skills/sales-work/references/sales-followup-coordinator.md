# Sales Follow-up Coordinator

This skill provides the Sales Follow-up Coordinator capability. It can seed one Crew or be added to an existing Crew without overwriting that Crew's primary identity.

## First useful result

1. Confirm lead owner, current stage, contact channel and policy, allowed claims, message voice, owner-specific booking URL, and what outcome counts as a useful meeting.
2. Read the selected validated lead brief or trial observation. For a trial, join product account to current CRM account and exact recipient through a source record; never derive a person from a tenant name. Check prior messages, replies, opt-outs, and booked meetings.
3. Draft one short response tied to the prospect’s actual enquiry or an owner-approved trial assistance policy. Cite supported facts and mark uncertain personalization for review. If contact is blocked or unclear, return an internal no-contact decision.
4. Create an action ledger entry with stable lead/action IDs, recipient reference, proposed send time, approval state, owner, and next-check date.
5. Ask the owner to review the exact recipient, message, booking URL and timing. A separately configured action may send only after fresh checks, then record provider delivery and calendar or CRM booking evidence.

Deliver **A reviewable unsent follow-up draft or sourced no-contact decision with exact recipient, owner, evidence and next check.** The draft or no-contact decision links to the selected lead or trial artifact, current CRM identity, prior-contact and permission checks, approved offer material, and any research it uses.

## Contact and repeat-run rules

An inbound request or trial signup is not blanket consent for every channel or cadence. Follow the customer's policy and current suppression state. Stop a draft when fit is unreviewed, contact is blocked, a relevant reply arrived, a meeting is booked, or the trial has converted. For a trial, keep product use separate from buying intent and require an explicit product-account-to-CRM-account mapping plus an exact permitted recipient. If CRM or inbox access is absent, return needs-review rather than a contact-ready draft. For an approved send, re-read the decision and message fingerprint, suppression, reply and meeting state; use a stable action ID and provider receipt. Report booked only from a matched calendar or CRM event.

## Fictional worked example

Fictional input: brief-example-001 qualifies lead-example-001, research-example-001 supplies a three-location observation, and the approved offer document supports a scheduling demo. The recipient is crm-export:contact-001. Contact policy remains review_required; no verified send approval or provider receipt is supplied.

First result: draft-example-001 has subject “Your scheduling demo request” and a short reply about the prospect's stated multi-location question. It cites the enquiry and approved offer, names the Sales owner, sets a next-check date, and records send_state **not_sent** with approval_required **true**. The owner reviews recipient, wording, booking URL, timing and current suppression state. A draft is neither delivered nor booked.

Separate fictional trial input: validated observation U-42 belongs to product account A-42. CRM mapping M-42 links it to CRM account C-42, but contact permission is unknown despite one observed project-created event. First result: trial-sales-assist D-42 says **needs_review**, recipient and draft are null, and Sales asks the owner to resolve permission and fit. Product use is not contact consent or purchase intent.

## Inadequate output to reject

“I sent the follow-up and booked a meeting because the lead asked for a demo.” Reject: no approved send, provider receipt or matched meeting exists. Also reject “email this trial user because they created a project”: use neither identifies an approved recipient nor proves contact permission or buying intent.

The fictional example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Automation handoff

For Inbound Lead-to-Meeting Review, emit `sales-followup-draft/v1` linked to the qualification brief. For Trial Account to Reviewed Sales Assist, consume the validated `trial-usage-observation/v1`, recheck current trial/CRM/contact state, and emit `trial-sales-assist/v1` with an unsent draft or no-contact decision. Keep `send_state` as `not_sent`; a separate approved action needs a provider receipt, and a later booked or paid outcome needs its own source event. Never infer those outcomes from a draft. The Crew step must supply the schema fields and output path; ask Builder to repair a route that omits them. A validator checks structure and references, while a human verifies source truth.

## Boundaries

Do not email, message, enroll a sequence, update CRM, schedule a meeting, or mark a draft as sent without a separately approved action and verified current state. Never copy another Crew's connection or credentials. Installing this skill does not enable a schedule, trigger, function, Automation, outbound message, or account write.
