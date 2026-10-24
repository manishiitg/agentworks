# Launch Coordinator

This skill gives one Crew the Launch Coordinator capability. It can seed a new Crew or be added to a compatible existing Crew. The customer’s offer, claim, contact, and measurement policies remain authoritative.

## First useful result

1. Bind launch ID, offer version, approved audience/message, target dates, channel owner, budget and contact rules from the reviewed brief.
2. Inventory each asset and channel action with stable ID, revision, approval, dependency, scheduled time, and observed provider state.
3. Distinguish draft, approved, published or sent, and verified states. Require a provider receipt or public observation for execution.
4. Read lead/form and CRM source records, join only on explicit campaign or source IDs, deduplicate events, and label attribution unknown when IDs are missing.
5. Return blocked actions, first lead signals, owner decisions, cost, and the next check; hand qualified leads to Sales under the customer’s fit and contact policy.

Deliver **A launch action ledger with approved asset revisions, channel and owner, dates, dependency state, source receipts, first lead signals, and next review.** A launch asset marked done in a project board is not proof it was published or sent; a form event is not a qualified lead or booked meeting.

## Source probe and acceptance

Read the approved launch brief and exact asset revision, current CMS or channel state, provider publication/send receipt if any, and two inbound form or CRM events. Join by campaign/source IDs, record consent and suppression, and show a duplicate event explicitly.

Demonstrate that draft, approved, provider-executed and independently verified are separate states. Reconcile one inbound event to its lead without calling it qualified or contacted; missing receipt or contact eligibility blocks those claims.

## Follow-through

Reconcile the same asset, campaign, event, and lead IDs; update only changed states, preserve approvals and receipts, and never issue duplicate distribution or lead follow-up.

## Fictional worked example

Fictional input: launch=launch-24; approved brief=gtm-brief-24; asset=landing-v3; channel=newsletter; campaign=cmp-17; form source=form-2; owner=launch-owner.

Fictional output: landing-v3 approved but not published; newsletter draft approved, provider send receipt absent; campaign cmp-17 has two form events e-91 and e-92, with e-92 a duplicate of e-91; lead l-55 linked to e-91 and queued for qualification; attribution=source-ID supported; owner=launch-owner; next=verify publish/send separately and pass l-55 to Sales without contacting it.

Inadequate: “Launch complete and two qualified leads generated.” Reason: It confuses approvals with publication and form events with qualification, and ignores the duplicate event.

## Automation handoff

Launch to Qualified Pipeline can consume launch-signal-register/v1 with launch and offer IDs, campaign and source IDs, channel receipts, inbound event IDs, attribution confidence, consent and suppression state, and source refs. Lead Intake & Qualifier must independently read the current lead record before any contact. Builder must bind an exact artifact path and schema and validate the artifact before the next Crew consumes it. Verify current source truth independently.

## Boundaries

Do not publish assets, send outreach, enroll contacts, spend on ads, alter CRM stages, or claim pipeline lift without approved actions and observed provider and sales records. Installing this skill enables no schedule, trigger, function, Automation, publication, message, or account write. Never copy another Crew’s credentials.
