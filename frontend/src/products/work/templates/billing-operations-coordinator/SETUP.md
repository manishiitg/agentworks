# Billing Operations Coordinator setup

Template: `billing-operations-coordinator` version 1. Progress is saved in `templates/billing-operations-coordinator/TEMPLATE_SETUP.json`.

## First result

Provide a dated invoice/payment export or authorize and test a billing source, then share invoice terms, refund/contact policies, and an owner for exceptions. Ask: “Review overdue invoices, failed payments, refund requests, and disputes for this period. Give me a source-linked queue and drafts for review.” The first result may cover only the case types supported by the supplied data; state the gaps.

Choose the customer's actual billing source. Stripe and Paddle are bundled MCP discovery options; availability of a particular account or action must be tested in this Crew. Chargebee, QuickBooks, Xero, and customer messaging may require another authorized connector or an export. Do not copy accounts or secrets from another Crew.

## Source probe and example

On one actual case, bind the exact billing account and mode, customer, charge or invoice ID, source revision and cutoff to the owner policy. Recompute any open or refundable balance from payment, credit and refund records. Check previous provider dunning and customer contact before proposing another touch. Have the owner review the status and next action.

Fictional example: a USD 100.00 charge has USD 10.00 previously refunded and a USD 25.00 proposal, leaving USD 65.00 after the proposal. It is **not issued**. Reject a claim that the customer was refunded without a provider receipt. Reproduce this check on a real authorized case before completing setup.

## Optional work after the first queue

| Capability | Setup decision |
| --- | --- |
| Queue review schedule | Choose timezone, cadence, source freshness, and reviewer; test a run before enabling. |
| Billing event trigger | Validate provider signature, account, event ID, object ID, and duplicate delivery; fetch current object state before opening a case. |
| Customer message or refund action | Review the exact customer/payment, content or amount, policy, destination, permission, and approval. Record provider response. |
| Finance handoff | Agree on queue fields, source links, and exception ownership before a separate Automation sends a bounded summary to Finance Analyst or Revenue & Close Analyst. |

Selecting this template enables none of these actions.
