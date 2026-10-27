---
name: "webhook-triggers"
description: "Set up and inspect webhooks and callable functions through the current MCP trigger tools."
external: true
external_tools: ["manage_triggers", "*workflow_function*"]
external_note: "External mapping: use webhooks and functions with their discovered schemas. This topic describes external MCP; the longer Builder reference uses different internal tool names. Existing project permissions and connection consent apply."
---
# Webhooks and functions over MCP

A webhook lets another system start published work when an event arrives. A function is an entry point with defined inputs and results. Use the product's Ask entry point for conversational requests instead.

1. Discover the project and current `webhooks` schema. Pass `workflow_id` for a workflow or Flow, or `crew_id` for a Crew. Call `action=list` first to see existing triggers, saved routes and declared variables; reuse an existing binding when it fits.
2. Create or update with `action=create|update`. Put the trigger configuration inside `trigger`; update/delete/test/runs use the returned trigger `id`. Use the fields supported for that product. Workflow and Flow functions describe their inputs under `function`; Crew published functions are authored through its own Crew tools. Do not copy an internal Builder tool's arguments into this external envelope.
3. For a requested workflow or Flow test, use `action=test`, then `action=status` with `trigger.run_id` and the same trigger `id`. Tests execute real work: a workflow test sends an authenticated internal delivery, and a Flow test executes its draft. There is no simulated test mode. This proves the internal path, not public DNS or gateway connectivity. Crew test/status support is not promised; follow its returned schema and result.
4. Inspect recorded deliveries with `action=runs`, the trigger `id` and an optional `limit`. Check the actual run result before saying work succeeded. Delete a trigger with `action=delete` when requested.
5. For typed calls, discover the product's published functions with `functions action=list`. Use `action=call` with its returned input schema and a stable submission ID; follow the returned call receipt through the listed status/read/reply actions. Calls and webhook deliveries keep their current project access and execution rules.

Use authentication required by the actual trigger. Keep generated credentials out of URLs, logs and shared replies; use the person's requested secret store when needed. Store a person-provided value with the designated secret tools and confirm by name only. Never fetch a stored value to build a webhook command.

The owner configures Crew and Flow triggers; workflows retain their current owners/editors. Share the product's dashboard to give other people use of its published work. Use schedules for time-based calls and Watchers for conditions, through their existing shared entry points. Explain results and the next step in plain words; keep IDs in tool calls unless the person asks for them.
