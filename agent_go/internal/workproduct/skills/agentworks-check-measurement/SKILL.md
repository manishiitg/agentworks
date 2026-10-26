---
name: agentworks-check-measurement
description: "Use to check whether a Goal is measured properly: right metric, target, fresh and correct readings, baseline."
---

# Is this Goal measured properly? (measurement health)

A Goal can be on target and still be measured badly. Check this for every Goal at least weekly, and whenever you first start looking after one. It is separate from checking whether the numbers are on target.

Use `workspace` action `references_list` for the exact linked Goal path, then `read_linked_goal` action `metrics` for its configured metrics and their readings (with source run and time). To spot-check a reading against the Goal's own data, use `database` actions `describe_linked` and `query_linked`. The Goal's purpose and success criteria are in its soul file. Skill: `panel-data` (database).

## The checks
1. **Is there a stated outcome?** The soul file says what winning means in the person's terms. If it is missing or vague, say so.
2. **Are metrics configured?** At least one primary metric per outcome. If none: 'measurement setup needed' (never zero, never 'fine'). Propose the metric to the person (see below).
3. **Does the metric measure the outcome, not the activity?** Replies and qualified conversations, not emails sent; subscribers, not articles; applications actually submitted, not jobs found. A metric that can go up while the person gets nothing is the wrong metric. Say so with the example.
4. **Is there a target and a time window?** A number with no target, or a target with no window, cannot be judged.
5. **Are readings recorded by the Goal's own steps, every run?** Look at the latest observations: recent, one per run, with a source run and a time. Gaps (no reading for several runs) and stale readings are a measurement problem even if the Goal is working.
6. **Do the readings match reality?** Spot-check one reading against the Goal's real data (a count from its database or its run files). If they differ, the measurement is wrong; report both numbers.
7. **Is a baseline recorded?** Without a comparable starting point, never claim improvement.
8. **Are supporting measurements and guardrails there when needed?** For example sends per day alongside replies, or errors alongside volume.

## Report
One line per Goal in the roll-up: 'measured well', 'measured but wrong thing' (name it), 'readings stale since <date>' or 'measurement setup needed'. Lead with the worst. Save the finding in that Goal's notes.

## Fixing it
- You may **propose** a metric, target or window to the person in plain words (what, why, how it would be recorded). Do not invent targets; ask once and save their answer.
- Setting up metrics and changing how a step records readings go through the Goal's Builder, only where you may act on that Goal or when the person says yes in chat. There is no Crew metric-configuration tool yet; do not infer write access from the linked database read. Never change a Goal's purpose.
