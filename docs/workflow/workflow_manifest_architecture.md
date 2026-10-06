# Workflow Manifest Architecture

## Status

This migration is complete.

Workflow mode is now manifest-backed, and `workflow.json` is the source of truth for workflow definition.

Current reality:

- workflow discovery comes from scanning workspaces for `workflow.json`
- workflow create/read/update/delete uses manifest APIs
- workflow execution bootstrap reads manifest capabilities
- workflow schedules and ownership live in the manifest
- frontend workflow "presets" are derived from manifests, not database workflow rows

The old migration-plan framing is obsolete.

## Canonical File

Each workflow workspace has a top-level manifest:

- `Workflow/<name>/workflow.json`

The backend struct lives in [workflow_manifest.go](../../agent_go/cmd/server/workflow_manifest.go).

## Current Manifest Shape

```json
{
  "schema_version": 1,
  "version": "1.0.11",
  "id": "wf_ab12cd34",
  "label": "Customer onboarding",
  "objective": "Optional workflow-level objective",
  "success_criteria": "Optional workflow-level success criteria",
  "capabilities": {
    "selected_servers": ["github"],
    "selected_tools": [],
    "selected_skills": ["account-research"],
    "selected_secrets": ["my-secret-name"],
    "selected_global_secret_names": null,
    "browser_mode": "none",
    "use_code_execution_mode": false,
    "llm_config": {
      "schema_version": 2,
      "mode": "explicit",
      "builder_llm": {
        "provider": "openai",
        "model_id": "gpt-4.1"
      },
      "maintenance_llm": {
        "provider": "anthropic",
        "model_id": "claude-opus-4-8"
      },
      "pulse_llm": {
        "provider": "anthropic",
        "model_id": "claude-sonnet-5-5"
      },
      "tiered_config": {
        "tier_1": {
          "provider": "openai",
          "model_id": "gpt-4.1"
        },
        "tier_2": {
          "provider": "openai",
          "model_id": "gpt-4.1-mini"
        },
        "tier_3": {
          "provider": "openai",
          "model_id": "gpt-4.1-nano"
        }
      }
    }
  },
  "execution_defaults": {
    "always_use_same_run": false,
    "disable_learning": false,
    "global_skill_objective": "What the shared skill should capture",
    "disable_parallel_tool_execution": false,
    "enabled_custom_tools": ["workspace_browser:agent_browser"]
  },
  "ownership": {
    "employee_id": null
  },
  "schedules": [
    {
      "id": "sched_123",
      "name": "Daily run",
      "cron_expression": "0 9 * * 1-5",
      "timezone": "Asia/Kolkata",
      "enabled": true,
      "group_ids": ["default"]
    }
  ],
  "created_at": "2026-04-09T10:00:00Z",
  "updated_at": "2026-04-09T10:00:00Z",
  "oversight_mode": "supervised",
  "decision_log_mutability": "append_only"
}
```

## What Lives In The Manifest

### `capabilities`

Workflow-wide execution defaults:

- `selected_servers`
- `selected_tools`
- `selected_skills`
- `selected_secrets`
- `selected_global_secret_names`
- `browser_mode`
- `use_code_execution_mode`
- `llm_config`

Notes:

- `llm_config.schema_version` is `2`
- `mode="provider_profile"` stores only a coding-agent `provider`; Builder, Maintenance, Pulse, Chief of Staff, and execution tiers resolve from current provider defaults
- `mode="explicit"` stores `builder_llm`, `maintenance_llm`, `pulse_llm`, and all three entries in `tiered_config`
- `builder_llm` runs the workflow-builder chat, scheduled runs, and every turn of the post-run Pulse conversation; `pulse_llm` runs only the background review agents a Pulse turn launches (plan drift / technical / strategic review) and KB maintenance. A scheduled run is one conversation on one retained coding CLI, so its model cannot change between the run and its Pulse turns; a background agent starts its own process, which is where `pulse_llm` takes effect.
- old `phase_llm`, `auto_improve_llm`, and `llm_allocation_mode` fields are migrated once when the manifest is read and are not written again
- tool search fields are not part of current workflow manifest capabilities

### `execution_defaults`

Workflow-level persistent execution defaults:

- `always_use_same_run`
- `disable_learning`
- `global_skill_objective`
- `disable_parallel_tool_execution`
- `enabled_custom_tools`

This is now the active home for global step overrides.

Runtime code reads global overrides from `workflow.json.execution_defaults`, not from `planning/step_override.json`.

### `ownership`

- `ownership.employee_id`

Workflow assignment is manifest-backed now.

### `schedules`

Schedules are manifest-backed now and no longer depend on DB workflow metadata.

Current schedule fields include:

- `id`
- `name`
- `description`
- `cron_expression`
- `timezone`
- `enabled`
- `trigger_payload`
- `group_ids`
- `mode`
- `messages`
- `workshop_mode`

For current runtime behavior, APIs, run history, and workshop-vs-workflow execution paths, see [workflow_scheduling.md](workflow_scheduling.md).

### Auto-improvement framework fields

Two optional top-level fields configure hard behavioral gates the auto-improvement framework reads. All default to backward-compatible values; existing workflows can omit them.

| Field | Values | Default | Purpose |
|---|---|---|---|
| `oversight_mode` | `manual` \| `supervised` \| `autonomous` | `supervised` | Controls when human approval is required for high-risk framework changes. Hard gate. |
| `decision_log_mutability` | `append_only` \| `append_only_strict` | `append_only` | `append_only_strict` forbids rewriting a dated decision entry in `builder/improve.html`, even for correction. Used by compliance workflows. Hard gate. |

`schema_version` controls the JSON shape. `version` controls product-managed workflow behavior. Contract `1.0.21` defines an idempotent artifact-ownership migration: it removes shared AgentWorks bridge/auth, Folder Guard, managed-tool, discovery, and coding-session mechanics from workflow-authored Plan/Learnings/KB prose while preserving semantic behavior and target-specific HOW. The owner starts that migration manually in Builder; schedules keep running the saved contract and do not stamp it. Ambiguous rewrites block the version stamp rather than guessing. The prior `1.0.20` contract remains the schema-5 executive Pulse journal upgrade.

The stable Goal lives only in `soul/soul.md`. `/define-success` records operating-model reasoning as a dated Reflection / Hansei entry in `builder/improve.html`; there is no permanent Workflow Profile card that can silently become an immutable constraint.

For the design rationale and worked examples, see [auto_improvement_framework.md](auto_improvement_framework.md).

## What Does Not Belong In The Manifest

Do not store live runtime/session state in `workflow.json`:

- workflow execution status
- current step progress
- active session ids
- selected run folder
- active execution state

Do not store secret values:

- no plaintext API keys
- no OAuth tokens
- no resolved credential payloads

## Other Workflow Files

The manifest does not replace the rest of the workspace.

These still live alongside it:

- `planning/plan.json`
- `planning/step_config.json`
- `planning/workflow_layout.json`
- `planning/output_plan.json`
- `variables/variables.json`
- `builder/improve.html` — the schema-5 lightweight, newest-first Pulse executive journal: Bug/Goal verdicts, one status sentence, three Latest Pulse cells, and at most six material Activity transitions. Goal / Ikigai remains exclusively in `soul/soul.md` and is rendered directly by Runloop. Reviewer coverage, assumptions, issues, backlog counts, and complete operational detail stay in SQLite/Pulse; older material history can live in linked monthly `builder/improve-archive/YYYY-MM.html` files. See [auto_improvement_framework.md](auto_improvement_framework.md).
- `knowledgebase/rules/rules.md` and `knowledgebase/rules/examples/` — legacy business-rule storage when present. Current user-confirmed runtime context belongs in `knowledgebase/context/`; its audit trail is recorded in dated Reflection entries in `builder/improve.html`.

`workflow.json` is the workflow-level definition file.
The planning files are still the step graph and execution-plan files.

## Runtime Flow

### Discovery

Backend discovery uses [DiscoverWorkflowManifests](../../agent_go/cmd/server/workflow_manifest.go#L344), which scans workspace folders and reads `workflow.json`.

### CRUD APIs

Manifest routes are registered in [server.go](../../agent_go/cmd/server/server.go#L1164):

- `GET /api/workflows/manifests`
- `GET /api/workflows/manifest`
- `POST /api/workflows/manifest`
- `PUT /api/workflows/manifest`
- `DELETE /api/workflows/manifest`
- `POST /api/workflows/manifest/duplicate`

### Execution bootstrap

Workflow execution loads manifest capabilities before running:

- [server.go](../../agent_go/cmd/server/server.go#L2375)
- [workflow_manifest_routes.go](../../agent_go/cmd/server/workflow_manifest_routes.go#L373)

Workshop phase sessions also load manifest config directly:

- [server.go](../../agent_go/cmd/server/server.go#L9762)

### Scheduling

The scheduler is manifest-based:

- [scheduler.go](../../agent_go/cmd/server/scheduler.go#L42)

It scans workflow manifests, loads enabled schedules, and executes them without DB workflow dependency.

### Frontend state

The frontend has a dedicated manifest store:

- [useWorkflowManifestStore.ts](../../frontend/src/stores/useWorkflowManifestStore.ts)

The old "workflow preset" view is now a compatibility layer built from manifests:

- [useGlobalPresetStore.ts](../../frontend/src/stores/useGlobalPresetStore.ts#L14)

## Current Compatibility Leftovers

A few migration-era leftovers still exist in code, but they are no longer the architecture:

- `presetQueryID` is still used in some session and tab compatibility paths, but it resolves to manifest workflow IDs
- some comments and logs still talk about "run migration"
- frontend API types still include `migrateWorkflowsToManifests`, but there is no registered `/api/workflows/migrate` route in current backend routing
- `planning/step_override.json` is still included in version snapshots, but active global overrides come from `execution_defaults`

These are compatibility remnants, not the main design.

## Key Files

- [workflow_manifest.go](../../agent_go/cmd/server/workflow_manifest.go)
- [workflow_manifest_routes.go](../../agent_go/cmd/server/workflow_manifest_routes.go)
- [server.go](../../agent_go/cmd/server/server.go)
- [scheduler.go](../../agent_go/cmd/server/scheduler.go)
- [useWorkflowManifestStore.ts](../../frontend/src/stores/useWorkflowManifestStore.ts)
- [useGlobalPresetStore.ts](../../frontend/src/stores/useGlobalPresetStore.ts)
