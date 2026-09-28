---
name: order-exception-to-resolution
description: Propose two Shopify Crews to investigate an order exception, review a related return or refund request, and verify an approved resolution.
---

# Order Exception to Resolution

## Outcome

Produce a sourced order exception, a policy and money review, an unsent customer draft, and proof of any approved resolution. A customer request, Shopify Return, owner approval, refund transaction, and delivered reply are separate states.

## When to use

Use for an order or fulfillment problem tied to a return or refund request. One compatible Crew may fill both slots with separate validated outputs.

## Discovery and user direction

Inspect existing Crews, store access, fulfillment and payment sources, helpdesk case, policy, owners, and prior actions. Propose reuse or creation and show the plan before Builder applies it. Installation only copies this guide and pending `SETUP.json`.

## Required inputs

Confirm exact store, order, customer, case, affected line IDs, currency, policy version, observation window, approval owner, and contact rule. An authorized export supports a manual read-only route.

## Plan and AgentWorks tools

Reuse or propose Store Operations Coordinator and Returns & Refunds Coordinator. Builder may use `create_crew` with stable keys for missing Crews. Route Store Operations' `store-order-exception/v1` to Returns only after `scripts/validate_handoff.py --order-only <order.json>` passes. Returns re-reads current payment, refund, dispute, fulfillment, Shopify Return, and helpdesk state and emits `return-resolution-review/v1`. Validate both artifacts before owner review. Only a fulfilled affected item may take the Shopify Return route; an unfulfilled item needs a reviewed order-edit, cancellation, or refund route.

## Knowledge and persistence

Save store, order, line, customer, case, action, approval, transaction, return, and ticket IDs; source times; policy version; chosen route; receipts; and next check. Re-read current state before every repeat or money action.

## Validation and reporting

Run the [handoff validator](scripts/validate_handoff.py) on real bounded artifacts. The [valid order](examples/store-order-exception.json) and [review](examples/return-resolution-review.json) are fictional; the [invalid review](examples/invalid-return-resolution-review.json) must fail. The reporting dashboard shows prepared, pending approval, executed, verified, and blocked cases with source links and run cost. A validator checks structure, not source truth.

## Guardrails

A proposal or approval never issues a refund, creates a label, or sends a reply. Execute an exact owner-approved action only through the merchant's authorized route. Count a refund or message only from provider receipts and a later source check. For HTTPS Shopify webhooks, verify raw-body HMAC, persist delivery IDs for deduplication, and re-read records; an event is not proof of success.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for goal and route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for schemas and stop conditions; [setup](SETUP.json) for ten customer checks.

## Completion contract

Return Crew IDs, source map, validated artifact paths, manual run, owner decision, unsent or provider-confirmed action state, verification evidence, activation choice, and blockers. Keep recurrence off until a real manual route and duplicate handling are reviewed.
