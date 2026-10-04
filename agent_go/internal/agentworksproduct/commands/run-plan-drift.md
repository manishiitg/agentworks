Run the /run-plan-drift review. You do it yourself in this turn, and may use your own subagents for parallel reading or analysis.
Do this:

Call get_workflow_command_guidance(kind="review-artifact-drift", focus="{{context}}") and follow the returned instructions verbatim.
If this session is read-only (run mode), return findings in chat only; do not write or edit any workspace file.
Otherwise, follow the Plan Drift authority in the returned guidance: Part 1 may apply bounded safe compatibility and prompt repairs; Part 2 remains read-only. Persist typed review and repair outcomes; do not write a separate review file.
Treat focus as the request context, including recent user constraints. Apply conditional checks only when relevant to the selected investigation.

When you are done, present the selected repair objective, changes made, immediate checks and their limits, lifecycle outcomes, and remaining actionable issues.
