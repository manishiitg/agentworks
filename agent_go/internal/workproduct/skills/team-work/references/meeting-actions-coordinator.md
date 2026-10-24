# Meeting Actions Coordinator

This skill gives one Crew the Meeting Actions Coordinator capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s systems as sources of record and reviewed action destinations.

## First useful result

1. Confirm meeting identity, source revision, participants, consent and transcript handling, task convention, and review owner.
2. Separate an explicit decision from a suggestion, question, or inferred assignment; retain a source span for each item.
3. Normalize each action to a stable meeting/action key, proposed owner and due time; mark missing owners or ambiguous dates for confirmation.
4. Check existing task and prior-meeting records for duplicates and already completed actions.
5. Return a reviewed register; create or update tasks only through a separately authorized route with destination receipts.

Deliver **A meeting action register with meeting and source IDs, decision versus proposal, action owner, due date, uncertainty, duplicate link, and review state.** Every decision and action needs a source span, meeting revision, owner state, and a reason when the text is ambiguous.

## Source probe and setup acceptance

Read one authorized meeting notes revision with source spans, the participant and decision-owner map, and the current task register. Match one proposed action to its meeting/action key and search for an existing task before drafting a new one.

Produce a register that distinguishes a decision from a proposal and leaves an ambiguous owner or due date pending. The setup remains pending if it treats a suggestion as approved, duplicates a tracker task, or writes a task before owner acceptance.

## Follow-through

Reuse meeting and action IDs, re-read transcript revisions and tracker state, preserve corrections, and avoid duplicate tasks or reminders.

## Fictional worked example

Fictional input: meeting=launch-sync-42 at 2026-09-26T09:00Z; notes revision=3; participant map=product-lead and design-lead; tracker export=rev-9.

Fictional output: decision D1=release notes require product review, source=notes:42@rev3 lines 18-20, confirmed by product-lead; action A1=design-lead to supply hero image by 2026-09-28, source=lines 24-25, owner acceptance=pending; action A2 duplicates tracker:task-41, no new task; next=confirm A1 before tracker write.

Inadequate: “Everyone agreed to launch tomorrow. I assigned all tasks.” Reason: The notes do not establish that decision, no source spans or accepted owners are shown, and assignments were made without review.

## Automation handoff

Meeting Decision to Owned Follow-through passes meeting-action-register/v1 to Project Status Reporter only after checking meeting revision, action identity, owner acceptance, due date, and source evidence. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Re-read current source state.

## Boundaries

Do not record a suggestion as an approved decision, assign a person without confirmation, publish notes, or create tracker tasks from installation. Installation enables no schedule, trigger, function, Automation, external write, notification, or customer message. Never copy another Crew’s credentials.
