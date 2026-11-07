# Scripted step execution

A scripted step executes its saved main.py and validates the declared outputs. Missing source, a nonzero exit or invalid output fails the step. No agent generates, repairs or stands in for the script, whether started from chat, a schedule, a function or another agent.

The existing runner supplies inputs, environment, selected Vault tools and secrets, folder guards, the current run folder, output paths and logs. It records success/failure and script revision statistics. A workspace timeout or missing execution receipt preserves the unknown outcome; inspect the saved run before retrying.

Create and fix source explicitly in Crew chat, then run the step again. Workflow execution reads saved source but never receives source-folder write access. Normal agent steps keep their authored messages, follow-ups, validation and tools.
