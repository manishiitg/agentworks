[← code / costs](index.md)

# PLAT-702: Code and Crew schedule runs show as their own cost scope

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | code |
| Area | costs |
| Summary | Turns started by a product schedule, reminder or trigger are recorded under cost scope `schedule` with the schedule's id and name, and a project's Cost Analysis splits Chat vs Schedules with a per-schedule list |

## What happened

Excellence, Code project orbit2.0: a self-created schedule ("Orbit-dashboard 30-min progress heartbeat", isolated
automation run) ran every 30 minutes on the shared Muse account. The ledger recorded all of it (150 calls, 175M input
tokens in 7 days) as scope `chat`, because product schedule turns reach the normal query path and
`costobserver.InferScope` calls anything unidentified chat. The cost view showed it as the owner's own chatting.

## Fix

- New scope `schedule` (`costobserver.ScopeSchedule`). The product schedule runner
  (`executeAutomationRun`, `applyAutomationCostSource`) stamps every turn it sends, in the project chat, a side tab
  or an isolated conversation, with `cost_source_id` (the scheduler job id), `cost_source_label` (the schedule name;
  `Trigger: <name>` for a webhook/function trigger) and `cost_source_run_id`. `chatTurnCostScope` (query path) and
  `executeDelegatedTask` (sub-agents) record those turns as `schedule`. Pulse and Goals workflow scopes are unchanged.
- Ledger entries carry `source_id`, `source_label`, `source_run_id` (new SQLite columns, added on open). Summaries
  gain `by_source`: per schedule its label, scope, run count and usage.
- Code/Crew project Cost Analysis: "By activity" shows Chat vs Schedules, and a compact Schedules table lists each
  schedule's runs, input/output tokens and cost. Providers → Costs names the scope "Schedules". MCP
  `get_code_costs` shows `schedule` in `by_scope`.
- Old rows are not backfilled: spend before this shipped stays `chat`.

## Verification

GitHub verify run: `TestProductScheduleTurnRecordsScheduleCostScope` (a schedule turn decoded from the runner's
request is recorded as `schedule` with its id, name and run; a person's turn stays `chat`), the
`costActivityBreakdown` vitest, frontend type check. Not run live: let a Code schedule fire and check the project's
Cost Analysis and `get_code_costs`.

## Left

- Deploy, then check orbit2.0 on Excellence after its next heartbeat.
