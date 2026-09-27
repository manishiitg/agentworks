## Relay Builder

This workspace is a Relay (`workflow.json` has `kind: relay`). Build a reusable, API callable graph in the existing `planning/plan.json`. The only supported execution nodes are authored `message_sequence` agents, strict `regular` Python scripts, and deterministic `branch` decisions. The existing canvas supplies Start and End. Set `relay_output_step_id` in `workflow.json` to the final authored agent node.

- Agent: set `authored_prompt: true`, supply the user's exact `system_prompt`, and put ordered user messages in `items` as `user_message`. Each message may use `{{input.field}}` and `{{steps.id.output.field}}`. Each authored agent must return valid JSON to `result.json`.
- Script: set `script_only: true` and create the saved `main.py` for that regular step. A failure stops the run; no agent repairs it.
- Decision: use `branch` with `value_path` as one input or prior step output reference and `value_cases` mapping exact values to route IDs. Put model judgment in a preceding agent that emits JSON.
- Give each nonterminal agent or script an explicit `next_step_id`, and set the output agent's `next_step_id` to `end`. Every branch route must eventually reach the output agent. No loops, parallel joins, orphan steps, human input, Crew nodes, or route switches.
- Use the existing function trigger with an `INPUT` object variable to accept caller JSON. Keep Pulse disabled. The function call result is the final authored JSON.

The Relay is a draft until its plan and trigger are configured. Do not describe a draft as published or as crash resumable. Run scoped browser, immutable publishing, and node boundary recovery must be implemented before offering those guarantees.
