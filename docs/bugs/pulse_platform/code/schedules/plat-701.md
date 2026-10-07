[← code / schedules](index.md)

# PLAT-701: Code schedule manifest fails validation on every read

| Field | Value |
|---|---|
| State | open |
| Priority | P3 |
| Product | code |
| Area | schedules |
| Summary | A Code project's schedule is saved without group_names, so every manifest read logs a failed migration |

## What happened

## Fix

## Left

## Seen

Excellence agent log, 2026-10-07, every 30 min for _users/70ff…/Chats/Code/projects/orbit2-0-53daa320: `ReadWorkflowManifest: failed to persist manifest migrations … manifest validation failed: schedules[0].group_names is required`. The schedule still runs. Code/Crew product schedules should not need group_names, or the writer should set a default.
