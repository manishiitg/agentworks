# Support Reply Drafter

This skill gives one Crew the Support Reply Drafter capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s support systems as sources and reviewed action destinations.

## First useful result

1. Bind exact case, customer, recipient channel, current status, latest message, policy version, and owner.
2. Read approved help articles and current product or billing facts; reject stale, conflicting, or account-inapplicable instructions.
3. Draft the answer for the customer’s specific question with cited claims, safe troubleshooting, and clearly marked unknowns.
4. Check escalation and sensitive-data boundaries, previous replies, opt-out or channel rules, and whether the case already resolved.
5. Keep the draft unsent until the owner reviews exact recipient, text, links, and timing. Save a stable action ID for any separately authorized send.

Deliver **An unsent reply draft with exact case and recipient reference, approved source citations, issue-specific steps, unknowns, owner review, and next-check rule.** A plausible answer is insufficient without current article or account references and a check that the draft addresses the latest case state.

## Source probe and acceptance

Read the latest authorized case message, exact recipient and channel, approved article revision, current product/account facts, and prior send receipts. Save each source ID and observed time; a copied article title is not proof the content is current or applicable.

Review one complete unsent reply against the latest question and approved claims. Remove or flag a stale article instruction, an unverified fix date, a wrong-account fact or a duplicate send; record the exact owner and recipient review.

## Follow-through

Re-read the case thread, prior send receipts, and knowledge revision before another draft; stop when the customer replied, the case resolved, or contact policy changed.

## Fictional worked example

Fictional input: case=helpdesk-701; latest question=why does SSO return 403; article=kb:sso-troubleshooting@v5; status=investigating; owner=support-lead.

Fictional output: draft=reply-701-1; case=helpdesk-701; recipient=helpdesk:requester-701; delivery_state=unsent; text=We are investigating the 403 after SSO. Please try the approved session reset steps in article v5; we will update this case after the auth check. No fix time is confirmed; citations=kb:sso-troubleshooting@v5, helpdesk-701@09:10; owner=support-lead; next_check=auth incident status.

Inadequate: “We fixed it. Clear your cookies and try again.” Reason: It invents a fix, omits the current case and article, and gives an unsourced instruction without review.

## Automation handoff

Support Case to Reviewed Resolution consumes support-reply-draft/v1 linked to validated triage and the same current case. It requires source citations, recipient reference, unsent state, and owner approval before a separate delivery route. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Recheck current source state.

## Boundaries

Do not send, promise a refund or fix date, disclose another customer’s data, change the ticket, or run account actions from a draft. Installation enables no schedule, trigger, function, Automation, case write, notification, or customer message. Never copy another Crew’s credentials.
