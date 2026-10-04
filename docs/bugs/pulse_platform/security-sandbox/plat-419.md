[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-419 — What a workflow step may do: measured permission map (open design question)

| Coordination | Value |
|---|---|
| State | measured; decision needed from the owner |
| Date | 2026-10-04 |
| Owner | security-sandbox |
| Related | PLAT-394 (sandbox contracts), PLAT-395 |

`agent_go test cli-step-contract --provider X` runs a two-step workflow through a
server (a scripted step, then an agent step that depends on a file it wrote) and
reads every verdict from the disk. Measured on macOS with Claude, Codex, Cursor,
Muse and Pi (the same for all):

| Action | Agent step (bridge shell) | Scripted step (saved main.py) |
|---|---|---|
| write its own execution folder | yes | yes |
| write its workflow's `db/` folder | no | yes |
| write its workflow's `code/` folder | no | no |
| read its workflow's `code/` and `planning/` | no | yes |
| read its workflow's `workflow.json` | no | no |
| read a file its upstream step wrote (declared dependency) | yes | n/a |
| read an unattached workflow, a private workspace folder | no | no |
| write `planning/`, another workflow | no | no |

An agent step also cannot run a script stored in its own workflow's `db/` or
`code/` (the bridge shell answers "Operation not permitted"), and its execution
folder is emptied when the step starts.

## Question for the owner

Is the agent-step column what a step needs? Agent steps reach `db/` through the
typed DB tools and have no use for `code/`, so this may be exactly right; the
asymmetry with scripted steps (which can read `planning/` and write `db/`) is the
thing to confirm. Nothing is asserted beyond the refusals that must always hold
(planning, other workflows, private folders); the rest is printed as `info` by the
command so a change shows up.
