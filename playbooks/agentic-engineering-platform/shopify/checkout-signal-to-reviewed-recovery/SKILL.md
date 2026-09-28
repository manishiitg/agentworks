---
name: checkout-signal-to-reviewed-recovery
description: Propose a Shopify checkout signal to consent-aware recovery review with an unsent draft and separate send evidence.
---

# Checkout Signal to Reviewed Recovery

## Outcome

For one checkout and channel, produce a current, source-linked decision: suppress, ask for missing evidence, or prepare an unsent recovery draft for merchant review. Count a later message or order only from separate provider and linked-order evidence.

## When to use

Use for a merchant-owned abandoned checkout recovery queue. Do not use for payment capture, order returns, or a general storefront conversion audit. One artifact pair covers one checkout, campaign, and channel.

## Discovery and user direction

Inspect current Shopify Messaging or other recovery automations first. Ask the merchant which channel and policy apply, whether another provider owns the send, who approves contact, and whether read-only review is the whole job. Propose reuse or creation of the two Crews and show the route before Builder applies it.

## Required inputs

Bind store, market, checkout, campaign, channel, merchant policy version, owner, current checkout and linked-order source, consent/opt-out source, and prior-send history. Missing customer access or contact policy yields an unknown or suppressed decision, not a send.

## Plan and AgentWorks tools

Shopify Growth Analyst emits a minimal `checkout-recovery-signal/v1` with exact identity, observation and hypothesis, excluding contact details. Validate it using `scripts/validate_handoff.py --signal-only <signal.json>`. Checkout Recovery Coordinator re-reads the same checkout, completion/order, consent and message history, then emits `checkout-recovery-review/v1`; validate the pair before any owner review. Keep a separate approved action route for a send. A webhook is a prompt to re-read, with verified delivery and idempotency, never an instruction to contact.

## Knowledge and persistence

Save stable checkout-channel-campaign case and action IDs, source times, policy version, suppression reason, owner decision, prior provider attempts, approval, and send receipt. Store contact details only in the authorized provider, not the shared dashboard. Re-read state before every repeated decision.

## Validation and reporting

The [validator](scripts/validate_handoff.py) rejects mismatched IDs, a draft without clear eligibility, duplicate-send risk, an unapproved sent state, and a recovered-order claim without a trusted checkout/order link. [Valid](examples/checkout-recovery-review.json) and [invalid](examples/invalid-checkout-recovery-review.json) fictional examples exercise the contract. The reporting dashboard separates suppressed, unknown, draft, approved, sent, and linked-order-observed states. Artifact validity does not prove source truth or legal permission.

## Guardrails

Selection does not enable Shopify Messaging, send email or SMS, change consent, create a discount, or activate recurrence. Merchant policy and applicable consent rules control contact. Re-read before any approved send, deduplicate against existing automation, and require provider receipt. A click or matching email is not a verified recovered order.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for exact fields and [setup](SETUP.json) for merchant checks.

## Completion contract

Return Crew IDs, source and policy map, validated artifact paths, a manual case with a suppressed or unknown control, owner decision, unsent draft when eligible, separate send/order evidence if it exists, and activation choice. Preserve missing evidence explicitly.
