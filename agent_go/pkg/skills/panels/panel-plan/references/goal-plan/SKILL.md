# Edit a workflow plan

This procedure applies in the workflow Builder, including a workflow owned by a Crew. Crew chat uses these same native editing operations through `crew_workflow_files action=edit`.

Read `builder-reference/references/plan-editing-tools.md`, the saved plan and its input definitions. Use the offered `create_plan`, `add_step`, `update_step` and `maintain_plan` schemas for the requested edit. Keep step IDs and existing instructions. One workflow belongs to its resolved parent; do not create another Crew or move existing Goal data just to edit a plan.

Inputs define names, types, defaults and required values once. Presets are optional saved values, not execution groups or run folders. Use the current input/preset arguments. Saved Crew scripts are referenced by ID and input mappings; Crew-agent steps inherit configuration without copying private chat history. Read the Crew procedure for those fields, and preserve explicit step capability overrides.

Check affected prompts, input consumers and downstream outputs, then verify the saved result. A test executes real work. Choose a single step or a full run according to the requested change and inspect its actual result, logs and evidence. Successful execution is not proof that a business goal improved.
