# Deal Follow-through Coordinator

This reusable Sales capability can seed a Crew or be added to one with a compatible owner and access scope. It carries no customer account or credential.

## First useful result

1. Bind exact tenant, CRM account, pipeline, opportunity, seller, report window and source revision from a validated pipeline brief.
2. Re-read current opportunity stage, status, amount, next activity, last contact, replies, meeting and opt-out state before proposing any action.
3. Apply the owner-defined stale rule to the current record; distinguish an observed gap from a seller judgment or missing history. Block duplicate and already-owned actions.
4. Prepare investigate, seller-review, no-action or approved-route options with a stable action key, due rule and source references; keep proposed versus owner-accepted state explicit.
5. Present the exact register for seller review. Any CRM update, message or meeting change requires fresh source checks, exact approval and a provider receipt in a separate route.
6. For a signed-deal handoff, verify the executed contract revision, purchased scope, first-value promise, entitlement/provisioning rule and receiving CS owner. Emit sales-cs-handoff/v1 as ready or blocked; Closed Won alone is insufficient.

Deliver **An exact-opportunity action register or signed-deal handoff with current CRM state, seller and receiving owner, source-backed scope, approval state and verification plan.** For pipeline work cite the validated brief, current opportunity revision, activity and prior-contact sources. For handoff cite CRM, executed agreement, first-value clause and entitlement sources with exact revisions and observation times. Keep owner decision and any provider receipt separate.

## Fictional worked example

Fictional input: opp_12 is Discovery at snapshot S2, has no next CRM activity and last recorded contact was 20 days ago; owner stale rule is 14 days. Seller=s_3; no reply or opt-out is recorded in the authorized export.

Fictional register A-12: opp_12/S2, stale under 14-day rule; propose seller review of next contact because current reply history coverage is incomplete; owner decision=pending; CRM write=none; message=unsent; recheck=before any approved action.

Inadequate: “The buyer lost interest, so I moved opp_12 to Closed Lost and sent a final email.” Reason: Stage age does not prove intent, the source coverage is incomplete, and no exact seller approval or provider receipts authorize those actions.

## Later run

Keep the same opportunity and action or handoff keys, re-read current CRM, contract, entitlement and activity as relevant, close superseded suggestions, and count actual action only from a matching provider receipt.

## Boundaries

Do not infer buyer intent from stale stage age, treat a draft as sent, create duplicate tasks, move a stage, contact a prospect, report a forecast or promise onboarding from CRM status without approved evidence. Use the existing account selections and authorization for any action.
