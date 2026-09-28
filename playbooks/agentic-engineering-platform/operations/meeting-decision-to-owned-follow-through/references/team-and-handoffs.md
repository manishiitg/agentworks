# Meeting action team and handoffs

This package is a Builder proposal. Installation copies guidance and a pending checklist. It does not create tasks, assign owners, message stakeholders, publish notes, or activate recurrence.

## Route

1. **Meeting:** Read the exact authorized notes revision. Extract explicit decisions and actions with source spans. A suggestion or ambiguous owner stays pending confirmation. Deduplicate against the prior action register and tracker.
2. **Validate:** Meeting Actions Coordinator emits `meeting-action-register/v1`. Insert an explicit validator step before another Crew consumes it. A file attachment alone does not validate the handoff.
3. **Status:** Project Status Reporter re-reads the exact project and current tracker state. It emits `project-action-status/v1`: pending confirmation, open, blocked, or done. A pending owner cannot become an owned task; done needs a source-observed completion record.
4. **Optional review:** Chief of Staff reads the validated status and emits `operations-review-brief/v1` with decision requests and source-linked priorities. It does not decide on behalf of the owner.
5. **Act separately:** If the owner approves an exact task or report write, use the permitted destination with a stable idempotency key. Retain provider receipt and re-read destination state. A local success flag is not a created task.

For the fictional fixtures:

    python3 scripts/validate_handoff.py meeting examples/meeting-action-register.json
    python3 scripts/validate_handoff.py status examples/meeting-action-register.json examples/project-action-status.json
    python3 scripts/validate_handoff.py review examples/meeting-action-register.json examples/project-action-status.json examples/operations-review-brief.json

The fixtures demonstrate contract shape, not a real owner acceptance, task write or completed project.

## Stable identity and repeat rules

Every artifact carries tenant and project IDs. Meeting and status carry meeting ID and notes revision; status binds the register ID, and review binds the status ID. Join actions by stable action IDs, not a paraphrased sentence. A changed notes revision requires a recheck of each action and any owner correction.

Use tenant, meeting ID, revision and action ID as the action identity; store task ID and provider receipt separately. A duplicate action references the existing task and never causes a second task creation. Re-read tracker status before a repeat or reminder. If source access is partial, report unknown and the missing system instead of declaring a project green.

## Boundaries

Meeting notes may contain confidential customer or personnel information. Retain only permitted spans and references in cross-Crew artifacts. Any delivery action needs the exact reviewer, destination, fields and policy decision. Report publication, task creation, owner assignment and reminders are independent approvals where the customer policy requires them.
