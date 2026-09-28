# Customer Success: new customer to first value

Status: implemented locally, not deployed. Scope: B2B SaaS onboarding after a signed sales handoff.

## Product boundary

Three reusable Crew capabilities are available: Customer Onboarding Coordinator, Product Adoption Analyst, and Customer Health Coordinator. Each installs a project-local skill and nine-check chat setup list. A Crew may carry more than one capability. The **New Customer to First Value** Automation Playbook proposes two required Crew slots and one optional health slot. Builder chat inspects existing Crews and customer context, proposes concrete steps, then configures them through authorized tools after review. Selection alone creates no Crew, customer action or recurring run.

A Sales meeting or qualified lead is not a customer onboarding trigger. Start only after an authorized signed deal, purchased account, or equivalent owner-confirmed handoff identifies the customer and purchased scope.

## First useful result

1. Onboarding reads one authorized signed handoff or export, confirms account and tenant identity, purchased scope, owner and intended result, then produces `onboarding-milestone-register/v1`. Each milestone has an owner, due date and observable state. A completion needs source evidence.
2. Adoption receives only the validated register and its own authorized product event source. It applies the agreed first-value rule to the same account and tenant within a defined window. `first-value-readout/v1` is `reached` only with a matching event, `not_observed` only with complete source coverage, and `unknown` when instrumentation or access is incomplete.
3. Optional Health reads the validated readout and authorized support or renewal context to produce `customer-health-brief/v1`. It separates observed signals from hypotheses and leaves customer-facing actions for owner review.

The validator checks artifact structure, references, identity and rule binding, milestone links, coverage and observed-state consistency. It blocks the next Crew on failure. Valid JSON does not prove the source event happened; the owner must inspect current records.

## Setup and tools

An export supports the first read-only result. A live route needs the customer's authoritative handoff or CRM, onboarding source and product events, with the exact account ID mapping and exclusions for internal/test activity. Named tools in the template are options, not connected accounts. A schedule or authenticated event may be proposed only after a bounded manual run proves the chosen sources and handoffs. Customer emails, CRM writes, access changes and task creation are separate approved actions with current-state checks.

## Acceptance evidence

- All three Crew templates install through the picker and Builder with uncompleted setup, a selected local skill and no active connections or recurrence.
- The Playbook installs with two required slots, one optional health slot, ten pending checks, validator, references and fictional examples.
- Valid example artifacts pass. Wrong account or tenant, unknown source, unproved milestone completion, an unagreed event, or a `reached` status without an observed event fails.
- Frontend, Go and package tests cover catalog visibility, creation, installation and handoff rules. Real customer setup and a manual run await authorized customer data.
