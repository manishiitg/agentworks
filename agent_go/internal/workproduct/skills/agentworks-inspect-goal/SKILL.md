---
name: agentworks-inspect-goal
description: "Use to inspect a Goal (workflow) in depth: runs, steps, schedules, database numbers, costs, plan, decisions. Start here for any Goal check."
---

# How to inspect a Goal (workflow) in depth

Start from the tab's notes (`notes/tab-<goal>.md`): purpose, targets, what you learned last time. If you are not sure where something lives (groups, iterations, step files, costs), read `agentworks-goal-anatomy` first. Then open the evidence, newest first. Quote only what is relevant; never copy secret values or other people's private chats.

## Tools and skills
- Runs and steps: `read_linked_goal` action `runs` lists each timer's runs, newest first, with status and times. For one run's detail read its `runs/iteration-*-sched/<group>/run_metadata.json`, then the saved step outputs and logs. Skill: `panel-runs` (run logs). Pick the right run, group and step before diagnosing; do not re-run a failed step just to read it.
- Plan and steps: `run_metadata.json` gives the executed `plan_revision`. Read `planning/plan.json` and `planning/step_config.json` where file access permits it; if they are unavailable, say the plan stays unverified. Read the relevant plan-design guidance when judging a plan.
- Schedules: `read_linked_goal` action `runs` shows each of the Goal's timers, whether it is on, and its recent runs. Check that before claiming anything is paused or missed. `schedules` action `list` covers this Crew's own schedules.
- Decisions waiting on the person: `needs_you` actions `list` and `get`. Skill: `panel-access`.
- Notifications: skill `panel-notify`. Do emails or messages actually arrive; an expired login fails silently.
- **Database (the real numbers):** `workspace` action `references_list` supplies the Goal's exact `workspace_path`. Use `database` action `describe_linked` for tables and columns, then `database` action `query_linked` for a read-only SELECT or WITH; add ORDER BY and use offset for the next page. `db/README.md` documents each table. Count verified sends, actions, replies and applications instead of trusting a report. If access is refused, say exactly which numbers stay unverified.
- Costs: `costs` action `summary` gives the available aggregate. Use the aggregate and saved authorized cost reports where available; say when a step-level cost cannot be verified. Never open `costs/costs.sqlite` directly.
- Knowledge base and learnings: skill `panel-knowledgebase`.
- The Goal's own Builder chat history: skill `panel-chat-history`.

## What to check
1. **Did it run, and did it do the job?** Last runs: finished or not, duration, failed or skipped steps, per group. A run that finished but produced none of the Goal's main output (no sends, actions or drafts) is stopped work, not success.
2. **Numbers vs targets:** count verified actions this run and this week from the database; compare with the notes.
3. **Schedules:** enabled or paused, next run, last status, consecutive failures, runs that waited on a dependency or expired because the Goal was busy.
4. **The plan:** steps that no longer match the purpose, a stale or missing tool, a cap that is too low or high, a route nobody reaches, a step that produced nothing for several runs.
5. **Waiting on the person:** decisions and how long they have waited.
6. **Outside dependencies:** logins, secrets (names only), quotas, accounts that expired.
7. **Cost:** tokens or spend far above normal, per step and per group.
8. **Measurement health:** is the Goal measured properly (right metric, target, fresh and correct readings)? `read_linked_goal` action `metrics`; follow `agentworks-check-measurement` weekly, and the first time you look at a Goal.
9. **Improvement:** on Mondays, `agentworks-improve-goals` (QA, architecture and strategy lenses).

## Finish
Write the cause in one plain line, what you verified and what you could not, and what you did or propose. Save anything lasting to the tab's notes file.
