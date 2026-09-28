# Call brief to proposal handoff

## Customer journey

1. Builder checks existing Call Briefing and Proposal Crews, their source permissions, seller and commercial owner. Reuse or propose reviewed creation.
2. Call Briefing binds tenant, account, opportunity and exact meeting. It compares the invitation, current CRM notes and approved product material, labels unknown attendee roles and unverified buying signals, and emits `sales-call-brief/v1`.
3. Run the validator on the brief before any proposal step. A meeting brief is only preparation. The seller must conduct the call and review a dated discovery note or explicitly supply an approved post-call record. The route stops if this record is missing or unapproved.
4. Proposal Drafter re-reads the same opportunity, the approved discovery revision, current price list, discount authority and approved product claims. It emits an unsent `sales-proposal-draft/v1`, with line-item arithmetic, assumptions and owner questions.
5. Run the validator on the pair. The commercial owner reviews exact scope, price, terms, recipient and proposal version. Any send or CRM stage change is a separate approved action with a provider receipt.
6. On a later run, retain immutable versions and show source and proposal deltas. Never silently overwrite a reviewed or sent proposal.

## Contract

Both artifacts share tenant, account, opportunity and meeting IDs. The proposal cites the exact brief artifact and a distinct approved discovery note with observation time. Every line item cites a current approved price ID; total is the sum of quantity times unit price in one currency. Discounts must cite an approved rule and approver. The worked example has no discount. Draft state is `unsent`, approval is `pending`, and delivery receipt is null. Later delivery evidence is separate from the proposal artifact.

## Manual proof

Run one real authorized meeting and post-call note. Save source IDs, seller corrections, validation, commercial owner decision, and the absence or presence of provider send evidence. Fictional examples teach the contract only and do not complete setup for a customer.
