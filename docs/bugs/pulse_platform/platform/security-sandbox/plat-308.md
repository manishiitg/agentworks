[← platform / security-sandbox](index.md)

# PLAT-308 — Admin-managed global secrets, including promotion from another workflow

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | platform |
| Area | security-sandbox |
| Summary | Workflow secrets can be promoted to an encrypted, persistent server-wide store through the Secrets UI or admin builder chat. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Implemented and deployed to RTS; verification scope below |
| Last synchronized | 2026-09-12 |
| Implementation commits | 00049777e, 4d980ffa6 |

## Delivered behavior and verification

Workflow secrets can be promoted to an encrypted, persistent server-wide store through the Secrets UI or admin builder chat. Admins can update/delete managed globals; environment globals remain operator-managed. Ordinary owners/readers cannot publish globals. Name collisions do not overwrite existing globals.

`manage_global_secret(action="promote", name="NAME", source_workflow_path="Workflow/<source>")` works from a different workflow chat. `list_secrets(source_workflow_path=...)` discovers source names without exposing values. Omission preserves the active-workflow default. Both operations recheck current admin authority; explicit source access is authorized before reading. Source attachments continue resolving after promotion; destinations select the global name. Changes apply to new turns/runs.

Validation: backend permission, encrypted persistence/reload, collision, resolution, cross-workflow tool, malformed/missing source and admin-demotion tests passed. UI admin controls were inspected on RTS; no production secrets were promoted as part of verification. Existing in-flight environments are not refreshed. Related: [PLAT-272](plat-272.md), [PLAT-276](plat-276.md).

## Deployment receipt

Included in RTS app release `bb7ac6d17ec43750e74a5c92d73ef067f66c69bf`
(`bb7ac6d-20260912135437`), with provider `570ede69fb85beef251ddca9792a2e5ad0dfe95d`.
All three services were active and the public health endpoint was healthy after deployment.
Deployment health is distinct from the feature-specific acceptance scope above.
