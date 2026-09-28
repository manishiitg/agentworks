# Post-incident review and action workflow

## Freeze and reconstruct

Begin only after canonical incident evidence supports stability or an authorized near-miss review. Freeze incident, service, environment, evidence revisions, response window, recovery source, reviewer and sensitive-data scope. Recompute impact from numerator and denominator and cite each timeline event by source and event time. Keep ingestion lag, clock disagreement and missing telemetry visible.

## Analyze without inventing cause

Separate observed facts, reviewed contributing conditions, hypotheses and unknowns. A deploy shortly before errors is a lead to investigate, not automatic cause. A confirmed factor needs multiple independent source references and a human reviewer who can assess whether they support the statement. Avoid individual blame. Note what worked and the detection, diagnosis, mitigation and verification gaps.

## Review and action handoff

Post-Incident Reviewer saves `post-incident-review/v1` as pending or approved. A draft can feed a pending action register for planning but cannot create issues. Improvement Follow-Through Coordinator consumes the exact validated review and saves `incident-improvement-register/v1`. An accepted action keeps the gap, owner, due date, intended outcome and verification criterion from the reviewed proposal; its decision and issue creation have dated receipts. Re-read current issue state by exact ID and suppress duplicate writes. Issue closure alone does not verify the control.

## Verify and report

For verification, use a later independent source: alert replay, deployed change plus health check, runbook exercise or another approved test. Keep failed or missing tests open. The dashboard shows review decision, impact and unknowns, each action's owner/due/state, issue receipt, verification source and aging. Publication, notifications and knowledge promotion are separate approved routes with redaction and delivery evidence. Recurrence checks require later incident data and cannot be claimed from one closed ticket.

The [team guide](team-and-handoffs.md) provides exact sample artifacts, blocking validator commands and rejected claims.
