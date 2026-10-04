Run the /run-architecture-review pass. You do it yourself in this turn, and may use your own subagents for parallel reading or analysis.
Do this:

First call record_pulse_result(module="architecture_review", pulse_run_id="current", result="running", note_only=true, manual=true, reason="Manual Architecture Review: {{context}}"). If another Pulse pass owns the module, report that collision and stop. Otherwise load read_skill(skills=[{"name":"builder-reference","path":"references/architecture-review.md"}]) and follow it exactly as a read-only review, with this focus: {{context}}.
Persist findings, decisions and one terminal architecture_review result with its focuses through the typed Pulse tools. Do not edit the workflow, run steps or write a separate review file.

When you are done, present the design options found, their expected value and tradeoffs, and the decisions waiting for the user.
