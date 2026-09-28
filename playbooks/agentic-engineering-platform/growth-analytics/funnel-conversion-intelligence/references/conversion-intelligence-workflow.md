# Conversion intelligence workflow

The v0.2 chat-led proposal binds Funnel Analyst and Growth Experiment Planner for a bounded signup-to-paid case. Its [team and handoff](team-and-handoffs.md) requires a validated stage observation before a pending experiment plan. The broader monitoring design below is a later owner choice.

## Define funnels before analysis

Version each funnel with its steps, event/taxonomy revision, population, filters, attribution rule, comparison window, target or threshold, owner, and exclusions. Reuse customer funnel definitions when their steps, population, and windows are compatible; never silently redefine a step to make a trend look better.

Record segment dimensions (plan, channel/campaign, page/screen, device, geography, account size) with their compatibility rules. A segment breakdown is only valid when every segment shares the funnel definition and window.

## Adapt the plan

Use scripted steps for funnel snapshots, conversion and drop-off calculations, segment breakdowns, statistical change detection against the comparison window, and completeness checks (freshness, denominators, missing events). A change is reportable only when it clears the customer's minimum detectable effect and data-quality gate.

Use a message sequence to investigate a supported change: rank observed segment contributions, inspect relevant pages/devices/sources, pull consented session replays when allowed, and test alternative explanations (tracking change, seasonality, mix shift, outage). State a cause as a hypothesis until independent evidence or a valid experiment supports it. Sessions are qualitative context, not a detection method or causal proof on their own.

For recurring monitoring, prove an on-demand investigation first, then propose paused scheduled funnel runs or threshold alerts with explicit scope, cadence, timezone, cost and notification conditions. Never page a human for a change that fails the quality gate.

## Validation and report

Validate funnel reproducibility from durable snapshots, denominator presence on every rate, segment/window compatibility, comparison-window alignment, session linkage for qualitative claims, and evidence for every finding. Verify that taxonomy or tracking changes surface as data-quality events rather than silent conversion shifts.

Build a live conversion dashboard showing funnel steps with denominators, trends only for comparable windows, segments, open hypotheses, consented session references, limitations, and history. A new company may have one baseline window and no trend. Incomplete or untrusted states stay visibly unrated.

## Handoff

Growth Experiment Planner consumes the exact validated `funnel-observation/v1` and returns a pending `funnel-experiment-plan/v1`. A later Growth Experimentation and Follow-Through route may consume approved plans and provider-backed outcomes. Keep observation, proposed cause, launch and verified result separate; downstream Crews should not reconstruct counts from chat history.
