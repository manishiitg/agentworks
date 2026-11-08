# Repeatable work in a Crew

A Crew is where people talk to their agents and keep instructions, skills, files and shared functions. A workflow is a saved Python program inside that Crew for work that should run the same way again. Ordinary conversation needs no workflow.

Create or change a workflow using the `python-workflows` skill and the current managed source-edit tools. Declare the inputs it needs, optional defaults and a saved preset when useful. A run uses a frozen copy of that source and its selected input values; its progress labels are records inside the program.

Start the requested workflow directly, or call a published function that exposes it. Inspect the returned run and its saved outputs/logs before saying it worked. Schedules and webhooks call the same entry; creating or editing the program does not run it. To ask questions, use the Crew conversation. Sharing exposes the product through its current dashboard, Ask and function entries with the same access checks.

The older plan engine and its Builder conversation are retired. Keep existing files as reference, and create a saved Python workflow for work that must run. Do not offer plan-step execution, an upgrade ladder or a second workflow chat. Use the current tool catalog rather than reconstructing an old command from memory.
