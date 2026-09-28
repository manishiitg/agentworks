# AgentWorks Playbook Specification v1

## Purpose

An AgentWorks playbook is a small, versioned skill package that helps the workflow builder adapt proven operating guidance to a customer's process. It is guidance for building or improving a workflow, not an executable workflow definition. The current catalog targets small business teams across functions: default to one understandable workflow and introduce separate workflows only for incompatible access or lifecycle boundaries.

The user's request, durable preferences, existing workflow, and live platform capabilities take precedence. Installation records how the builder adapted the source package; it does not silently replace the customer's plan.

## Package layout

```text
<playbook-id>/
├── SKILL.md
├── playbook.json
├── references/
│   └── ...
└── examples/
    └── ...
```

`SKILL.md` is the builder entrypoint. Keep it under 500 words and link to details instead of copying platform manuals into it. References may be organized for the topic; they do not repeat the entrypoint section schema. Examples must be fictional, non-secret, and clearly identified as illustrative.

## Fixed entrypoint sections

Every playbook `SKILL.md` uses these H2 headings exactly and in this order:

1. `Outcome`
2. `When to use`
3. `Discovery and user direction`
4. `Required inputs`
5. `Plan and AgentWorks tools`
6. `Knowledge and persistence`
7. `Validation and reporting`
8. `Guardrails`
9. `Read details when needed`
10. `Completion contract`

These headings are the stable UI and builder-consumption contract. Keep each section concise:

- **Outcome:** the durable behavior or capability the installed workflow should provide.
- **When to use:** positive triggers, scope, and important exclusions.
- **Discovery and user direction:** inspect the current workflow before proposing changes, summarize reusable design and gaps, ask focused questions only for material unresolved customer choices, and record the answers. Installation is guidance, not approval to edit or execute the workflow.
- **Required inputs:** information, access, policy, and existing artifacts the builder must resolve. Mark unavailable required inputs as blockers rather than inventing them.
- **Plan and AgentWorks tools:** explain how the builder maps the playbook into a plan: which behavior belongs in scripted steps, message sequences, routes, branches, schedules, or approvals; what existing steps to reuse; and which AgentWorks capabilities to resolve. Link to shared or topic-specific detail.
- **Workflow design and outcomes:** inspect existing goals and metrics, recommend measurable additions when needed, decide whether to extend the current workflow or propose a separate workflow, and recommend manual, event, webhook, release, or scheduled activation using ownership, access, lifecycle, budget, and operational boundaries.
- **Knowledge and persistence:** what belongs in context, knowledgebase notes, learnings, database tables, and durable assets.
- **Validation and reporting:** define machine-checkable readiness and the reporting dashboard: primary status and metrics, useful filters/dimensions, evidence drill-down, history/trends, incomplete states, and any restricted data. Keep the dashboard backed by durable workflow data rather than message text.
- **Guardrails:** constraints that must survive adaptation.
- **Read details when needed:** relative links with a one-line reason to load each reference or example.
- **Completion contract:** the locations, receipts, results, and unresolved items the builder returns.

Use `Not applicable — <reason>` only when the topic truly has no content for a required section. Do not delete or rename the section.

## Frontmatter

The frontmatter contains only:

```yaml
---
name: <playbook-id>
description: <what it does and when the builder should use it>
---
```

The name must match `playbook.json.id` and the package directory. Put triggering language in `description`; do not add customer-specific details.

## Manifest contract

`playbook.json` is catalog and setup metadata. Required fields are:

| Field | Contract |
| --- | --- |
| `metadata_version` | Integer manifest format version; `1` for this specification. |
| `content_schema` | `agentworks-playbook/v1`. |
| `id` | Lowercase kebab-case stable identifier matching the folder and skill name. |
| `version` | Semantic version of the source package. |
| `title`, `description` | Human-readable catalog presentation. |
| `hierarchy` | Ordered catalog path, starting with `AgentWorks`. |
| `order` | Sibling display order. |
| `entrypoint` | `SKILL.md`. |
| `audience` | `workflow_builder`. |
| `team_scope` | Current catalog value is `small_team`; setup should minimize operational overhead and workflow count. |
| `setup_prompt` | User-visible instruction used to start an installation or adaptation. |
| `setup_inputs` | Input descriptors with stable IDs, labels, and required/default conditions. |
| `required_capabilities` | Capability names the builder must resolve or explicitly mark unavailable. |
| `recommended_tools` | Optional recommendations, never implicit installation or authorization. |
| `pulse_focus` | One advisory, goal-specific focus definition for Strategic Review. Plan Drift, Technical, and Architecture are intentionally excluded. |
| `outputs` | Durable completion artifacts and results. |
| `changelog` | Short release summaries keyed by playbook version. Add an entry for each new version so Builder can explain an update without inferring from version numbers. |

Topic-specific relationship fields such as `setup_playbook`, `setup_playbooks`, `accepts_equivalent_setup`, and `next_playbooks` are optional. Unknown extension fields should be ignored by compatible readers.

Each recommended tool contains `id`, `name`, `type`, `purpose`, `capability`, and `optional`. A recommendation improves discovery and UI presentation; the builder still checks current availability and preserves an explicitly selected compatible alternative.

Multi-Crew packages use `agent_slots` with a canonical installed Crew template ID, a required flag, and a versioned output artifact type. A `handoffs` edge names existing producer and consumer slots, declares whether the edge is required, and uses the producer's exact output type. Required edges connect required slots. The graph must be acyclic. An optional route needs its own blocking artifact validation before the next Crew consumes its output.

`pulse_focus` contains exactly one `strategic_review` entry with a human-readable `label`, concise `focus_areas`, and evidence-based `review_when` conditions. This is a workflow-specific lens layered onto canonical Strategic Review; it does not enable the module, schedule a review, replace its general checks, or modify thresholds. Do not add Plan Drift, Technical, or Architecture focus entries. Plan Drift remains a generic structural review, Technical chooses scope from operational evidence, and Architecture chooses scope from structural risk and change.

`recommended_skills` is an optional UI/discovery list. Each item contains `id`, `name`, `publisher`, `source`, `install_hint`, `purpose`, and `optional: true`. The source and install hint are informational and may change independently of the playbook. Before import, resolve the current source, inspect the complete skill package and license, check compatibility, record the source revision/digest, and use the supported AgentWorks skill-import flow. Never execute an install hint or attach a skill automatically.

## Builder consumption

The builder consumes a playbook progressively:

1. Read `SKILL.md` and the manifest.
2. Inspect the existing plan, goals, metrics, workflow configuration, selected capabilities, durable stores, report, and triggers. Summarize what can be reused and what is missing.
3. Resolve required inputs and distinguish known values, reasonable reversible defaults, customer decisions, and blockers. Ask focused questions for material unresolved choices before changing the workflow, then record the answers as customer direction.
4. Map the requested outcome to existing goals and metrics; recommend missing definitions without inventing customer targets.
5. Default to extending one small-team workflow with a route. Recommend a separate workflow only for incompatible access, ownership, trigger, deployment, retention, or failure lifecycles, with concrete reasons and tradeoffs.
6. Recommend how it starts: manual action, workflow handoff, webhook or event, release gate, or schedule. Define scope, cadence and timezone or event filters, concurrency, retries, budget, notification behavior, and proof of completion without enabling an unapproved trigger.
7. Compare the playbook's Strategic Pulse focus with existing review coverage. Recommend it only when useful; keep Plan Drift generic and leave Technical and Architecture scope evidence-driven.
8. Load only the references relevant to the current operation.
9. Map the guidance onto the chosen workflow using supported AgentWorks plan/config tools. Reuse or revise compatible steps before adding new ones.
10. Show concrete human decisions only after the draft, evidence, or proposed change is ready for review.
11. Trial the affected step or route, validate durable output and the report, then record the installed snapshot.

The builder must not copy the reference graph mechanically, add a step per tool call, attach every recommended tool, overwrite user preferences, or edit system-managed plan files directly. Dashboard widgets and drill-downs must read the same durable records used for validation; reporting is part of installation, not a separate optional workflow.

Design human review as a durable asynchronous lifecycle by default. A preparation route saves the exact proposal, evidence, hashes, and a `pending_review` decision record, exposes them in reporting, and ends successfully without applying the change. A reviewer may decide later. A separate action route accepts only that recorded decision, rechecks its proposal identity, authorization, target state, and expiry, then applies or publishes the approved change. Use a blocking human branch only when the customer explicitly chooses an attended run and its bounded wait is acceptable. Never assume a reviewer will be online during execution.

Use the Report dashboard as the primary review surface. Runtime producers create or refresh a structured `create_human_input_request` linked to the proposal and evidence. **Ask in chat** opens discussion without deciding; **Approve**, **Reject**, and **Defer** save the human decision without applying it. After approval, dispatch the action to the executor authorized for the target surface: a writable code/folder route, typed Builder or Fixer tools for plan/configuration changes, or an approved external integration. Record answered, running, applied, verified, rejected, deferred, stale, and failed as distinct states.

## Installed playbook record

An installed playbook is a customer-specific snapshot and adaptation record. The product should persist at least:

```json
{
  "source_playbook_id": "basic-browser-setup",
  "source_version": "0.6.0",
  "content_schema": "agentworks-playbook/v1",
  "installed_at": "ISO-8601 timestamp",
  "source_digest": "content digest",
  "status": "draft|ready|blocked|outdated",
  "customer_overrides": {
    "direction": {},
    "unresolved_questions": []
  },
  "capability_resolution": {},
  "plan_step_ids": [],
  "artifacts": {},
  "validation": {}
}
```

Source updates never silently rewrite an installed workflow. The product compares versions/digests, shows relevant changes, and lets the builder merge them while preserving customer overrides.

Each catalog release adds one concise `changelog` entry with `version` and `summary`. The UI and `search_playbooks` show entries newer than the workflow's installed version. Builder uses these authored summaries to answer what an upgrade contains and states when release notes are unavailable rather than guessing from a version number.

When the catalog version is newer, the installed-playbook UI shows both versions and an update action. Updating refreshes the workflow-local playbook package and resets its setup status to `draft`; it does not edit the plan, goals, metrics, thresholds, triggers, schedules, capabilities, reports, or customer preferences. Builder then reviews the new guidance against the current workflow, proposes only applicable changes, and revalidates affected routes before the setup can return to `ready`.

## Versioning

- **Patch:** wording, examples, or corrections without changing expected behavior.
- **Minor:** new optional guidance, inputs, outputs, or compatible behavior.
- **Major:** renamed/removed contracts, changed guardrails, or incompatible installed behavior.

Record the source version and customer adaptations whenever a playbook is installed or upgraded.

## Validation

Run `python3 playbooks/scripts/validate_playbooks.py` from the repository root. The validator checks package identity, required manifest fields, fixed section order, explicit plan-step and dashboard guidance, the skill word limit, JSON parsing, local Markdown links, entrypoint existence, recommended-tool shape, installed Crew bindings, handoff output types, required-slot rules, cycles, and executable contract suites. These checks prove package consistency; Builder still must wire and test blocking validators with the customer's actual Crew runs.
