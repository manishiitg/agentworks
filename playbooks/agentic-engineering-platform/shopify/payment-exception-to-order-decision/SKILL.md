---
name: payment-exception-to-order-decision
description: Propose a Payment Operations and Store Operations handoff from Shopify transaction evidence to an owner-safe order decision.
---

# Payment Exception to Order Decision

## Outcome

Produce a sourced payment exception and a reviewed hold or fulfillment-release decision. A successful authorization, successful capture, pending transaction, Refund object, and money received by the buyer are different claims.

## When to use

Use for one Shopify order whose transaction state affects fulfillment. Abandoned checkouts without an order and manual payment methods need separately defined routes.

## Discovery and user direction

Inspect existing Crews, transaction and gateway sources, current order and fulfillment state, capture policy, prior retries, and owners. Propose reuse or creation and show the plan before Builder applies it. Selection copies pending `SETUP.json` and acts on no payment.

## Required inputs

Bind store, order, case, transaction and parent IDs, presentment amount and currency, observed kind/status, affected FulfillmentOrder, owner, and policy version. A bounded authorized export supports a manual read-only review.

## Plan and AgentWorks tools

Reuse or propose Payment Operations Investigator and Store Operations Coordinator. Builder may use `create_crew` with stable keys. Payments reads the current OrderTransaction and writes `payment-exception/v1`; validate with `scripts/validate_handoff.py --payment-only <payment.json>`. Operations re-reads order and fulfillment state, then writes `payment-order-decision/v1`; validate the pair. A release proposal requires a successful CAPTURE or SALE transaction in this narrow route. Present the exact decision to the owner before any separately authorized fulfillment action. Re-read provider and order state afterward.

## Knowledge and persistence

Save stable store, order, case, transaction, parent, fulfillment, action, approval, receipt, and run IDs; policy version; observed times; and prior retries. Re-read before repeats so a new capture, void, refund, or dispute supersedes old advice.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks identity, money types, release eligibility, approval, and receipts. The fictional [payment](examples/payment-exception.json) and [order decision](examples/payment-order-decision.json) pass; the [authorization-only release](examples/invalid-payment-order-decision.json) fails. The reporting dashboard shows held, pending review, released, verified, and blocked cases with source links and cost. It cannot prove gateway settlement independently.

## Guardrails

Do not capture, retry, void, refund, mark paid, release fulfillment, or contact a buyer through selection. Require current source truth, exact merchant approval, permitted provider action, receipt, and later source check. Multi-capture and currency handling depend on the actual transaction and merchant capabilities. A webhook signals a re-read, not payment success.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for goal and route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for transaction rules and [setup](SETUP.json) for ten merchant checks.

## Completion contract

Return Crew IDs, source map, validated artifacts, manual run, owner decision, provider and later check state, activation choice, and blockers. Keep recurrence off until the manual route is reviewed.
