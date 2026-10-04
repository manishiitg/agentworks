Run /run-technical-review. You do it yourself in this turn, and may use your own subagents for parallel reading or analysis.
Do both parts in this turn, in order: the review instruction first, then, once its receipt is persisted, the fix instruction.

Review instruction:

Call get_workflow_command_guidance(kind="engineering-review", focus="{{context}}") and follow the returned instructions verbatim. Pass run_folder as the workflow's currently selected run folder when one is selected; otherwise omit it.
If this session is read-only (run mode), return findings in chat only; do not write or edit any workspace file.
Otherwise, persist findings, recommendations, and decisions through the typed Pulse tools required by the returned guidance; do not modify implementation files or write a separate review file.
Treat focus as the request context, including recent user constraints. Apply conditional checks only when relevant to the selected investigation.
This is the read-only opening of one retained Review+Fix task. Persist the completed technical_review receipt before ending this turn. The supplied follow-up message owns repair. Small recovered tool failures with correct outputs and negligible overhead do not justify an issue or deeper review.

Fix instruction:

Continue the same bounded Review+Fix task. First confirm this conversation has a completed technical_review receipt; if review failed or is incomplete, report that and do not repair. Then call get_workflow_command_guidance(kind="pulse-fixer") and follow its repair-only instructions, carrying the same focus and run folder. Apply only reviewed, authorized bounded fixes, perform proportional immediate checks, and close applied fixes unless the defect is reproduced. Do not rerun reviewers or create future-run verification tasks. Return the combined review and repair outcome.

When you are done, present the selected repair objective, changes made, immediate checks and their limits, lifecycle outcomes, and remaining actionable issues.
