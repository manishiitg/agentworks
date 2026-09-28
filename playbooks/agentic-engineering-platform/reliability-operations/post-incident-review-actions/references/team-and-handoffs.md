# Post-Incident Review and Actions team and handoff

## Crew jobs and boundary

**Post-Incident Reviewer** owns frozen incident facts, impact arithmetic, timeline, classified contributing conditions and a reviewable action proposal. **Improvement Follow-Through Coordinator** owns accepted action identity, current issue state and evidence that the intended control works. Incident Investigator owns active diagnosis; Engineering Delivery Coordinator may support a release action. Reuse a Crew only if its access and owner fit. The two step outputs remain distinct and validated.

## Honest states

The fictional [draft review](../examples/post-incident-draft.json) cites stable INC-1042 but awaits the review owner. Its [pending register](../examples/incident-improvement-pending.json) contains no issue or verification. The [approved review](../examples/post-incident-review.json) records 160/2,000 = 8% checkout 5xx, a confirmed detection lag, and a deploy *hypothesis* rather than a proven root cause. The [improvement register](../examples/incident-improvement-register.json) has ACT-1 accepted, linked to issue ENG-901 and independently verified by alert replay RUN-77; ACT-2 still awaits acceptance. No publication or notification is claimed.

## Blocking manual route

1. Freeze canonical incident/recovery source revisions, service, environment, impact window, telemetry, privacy scope and reviewer. Reviewer saves `post-incident-review/v1`. Run `python3 scripts/validate_handoff.py review <review.json>` as a blocking Workflow step. It checks stability evidence, dated timeline, impact arithmetic, factor labels and action ownership. The [unsupported cause](../examples/invalid-post-incident-review.json) fails.
2. Pass the exact validated review artifact to the Coordinator by checked alias. Save `incident-improvement-register/v1` and run `python3 scripts/validate_handoff.py register <review.json> <register.json>`. A draft yields only pending actions. Acceptance requires an owner decision; issue creation needs a receipt; verification needs a later independent passing source. The [false completion](../examples/invalid-improvement-register.json) fails even though its issue is closed.
3. Save Crew run IDs, artifact paths, validator output, source revisions, owner corrections, action keys and missing access. The validator checks shape and arithmetic; the review owner judges source truth, cause labels and sensitive content.

## Repeat and external action

Preserve incident/review/action/issue IDs across runs. Re-read issue and verification state, retain earlier decisions, and add a reviewed revision if new evidence changes the retrospective. A ticket is work tracking, not evidence of control effectiveness or reduced recurrence. Issue writes, publication, messages, infrastructure changes and schedules each need explicit authority and provider receipts. Keep action follow-up manual until a repeat policy with timezone, source freshness, duplicate suppression and notification scope is approved.
