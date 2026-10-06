[← crew / security-sandbox](index.md)

# PLAT-339 — Read-only Crew workflow attachments can invoke through scoped internal bindings

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | crew |
| Area | security-sandbox |
| Summary | makes the bidirectional Workflow ↔ Crew integration symmetric. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented locally; focused tests green; deployment and live acceptance pending` |
| Last synchronized | `2026-09-21` |
| Priority | `P1 authorization / integration` |

## Problem

A Crew could attach an AgentWorks workflow as durable read-only context and
discover its triggers, but could not invoke the workflow unless an owner had
already created an internal trigger bound to that Crew. Calling
`run_workflow_trigger` without a binding attempted to create one through the
ordinary workflow trigger-management path, which requires owner access.

That made the two integration directions inconsistent. A workflow can invoke
an attached Crew through a secretless internal trigger bound to the workflow,
while a Crew with an authorized workflow attachment still needed a separate
owner configuration step before it could invoke the workflow.

## Authorized behavior

An attached Crew may now create or reuse one platform-internal workflow
trigger bound to that exact Crew when it invokes the workflow. The read-only
attachment is the authorization for this narrow operation. The binding has no
public URL or secret and retains the workflow trigger's normal deterministic
execution contract.

The exception does not grant general workflow mutation authority:

- the signed-in user must still have current read access to the workflow;
- the workflow must still be attached to the Crew;
- the caller identity is read from the Crew manifest, not supplied by the
  model;
- the Crew project must still exist for that user;
- only a secretless `kind: internal` trigger naming that Crew can be created;
- public webhook triggers and their secrets remain inaccessible;
- unrelated triggers cannot be created, updated, disabled, or deleted;
- dispatch and result polling continue to recheck the attachment, enabled
  state, and exact caller binding.

Concurrent first invocation is serialized with the existing workflow-trigger
configuration lock and rechecks for a binding after acquiring the lock, so two
requests do not create duplicate bindings.

## Implementation

`crewWorkflowTriggerBinding` now performs the read-access and Crew-identity
checks directly, reuses an existing matching binding, or persists the narrowly
scoped internal schedule with the same full-workflow defaults used previously.
`run_workflow_trigger` then uses the existing internal dispatcher and polling
path. Public webhook dispatch behavior is unchanged.

The Crew `workflow-references` feature now admits the four runtime tools
(`list_attached_workflows`, `list_workflow_triggers`,
`run_workflow_trigger`, and `get_workflow_trigger_run`) through the product
allowlist. The attached `work-workflow-files` skill contains the invocation,
idempotent retry, polling, output, and fail-closed procedure. The base Crew
prompt and feature prompt no longer incorrectly describe every workflow
reference as non-executable; they preserve `#` selections as context-only and
limit execution to durable attachments through the scoped internal path.

The canonical feature contract and agent guidance are updated in
[`docs/workflow/crew-step.md`](../../../../workflow/crew-step.md) and
`webhook-triggers.md`.

## Regression and acceptance

Focused `cmd/server` coverage proves:

- attached workflows and their triggers remain discoverable;
- an existing Crew-bound internal trigger is reused;
- a workflow reader can create the exact secretless Crew binding;
- a user without workflow access is rejected;
- unattached, missing, disabled, foreign-Crew, invalid-payload, and expired-run
  cases still fail before unsafe execution;
- invocation and polling retain exact Crew caller checks.
- the Crew product manifest admits all four scoped invocation tools and the
  built-in skill loads with the complete procedure and public-trigger boundary.

Local focused tests pass. After deployment, live acceptance should attach a
workflow read-only to a Crew, invoke it without pre-creating a binding, verify
the resulting `iteration-*-hook` run and returned outputs, then detach or revoke
workflow access and confirm subsequent invocation and polling fail closed.

Related: [PLAT-262](../../sandbox/security/plat-262.md) and the
[Crew workflow step contract](../../../../workflow/crew-step.md).

## Register notes

[PLAT-339](plat-339.md) makes the bidirectional
Workflow ↔ Crew integration symmetric. A Crew with an authorized read-only
workflow attachment can discover triggers and create or reuse one secretless
internal trigger bound to that exact Crew, then invoke and poll it through the
existing internal dispatcher. This is a narrow authorization exception, not
general workflow write access: public webhooks and unrelated trigger management
remain protected, and attachment/access/caller checks fail closed. The change
and focused regressions are complete locally; deployment and live acceptance
remain pending.
