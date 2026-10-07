[← code / schedules](index.md)

# PLAT-701: Code schedule manifest fails validation on every read

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | code |
| Area | schedules |
| Summary | A Code project's schedule is saved without group_names, so every manifest read logs a failed migration |

## What happened

A Code project's `workflow.json` is written by the product schedule service (`productProjectManifest`, product schedule records with `messages`/`isolated`, no `group_names`), not by the workflow manifest writer. `ReadWorkflowManifest` still parsed it as a `WorkflowManifest` and, because the product fields (identity, schema_version, owner_id, ...) look like retired fields, tried to "prune" them by rewriting the file on every read. `ValidateManifest` then rejected the product schedules (`schedules[0].group_names is required`), so the write failed and the warning repeated every 30 minutes. Had validation passed, the rewrite would have dropped the product fields, as it would for a Crew; Crew projects were already excluded for exactly that reason.

## Fix

`ReadWorkflowManifest` never persists migrations for product-owned runtime manifests: Crew projects as before, and now Code projects too (`isProductRuntimeManifestWorkspace` in `agent_go/cmd/server/workflow_manifest.go`). The product schedule service owns that file's writes. Validation is unchanged: Code schedules never go through `ValidateManifest`, and requiring group_names stays right for workflows. Test: `TestReadWorkflowManifestDoesNotRewriteCrewRuntimeManifest` now covers Code project paths.

## Left

Nothing. The warning stops once this is deployed.

## Seen

Excellence agent log, 2026-10-07, every 30 min for _users/70ff…/Chats/Code/projects/orbit2-0-53daa320: `ReadWorkflowManifest: failed to persist manifest migrations … manifest validation failed: schedules[0].group_names is required`. The schedule still runs. Code/Crew product schedules should not need group_names, or the writer should set a default.
