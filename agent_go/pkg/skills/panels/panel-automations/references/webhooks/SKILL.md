---
name: webhooks
description: Configure or inspect authenticated incoming HTTP events.
---

# Webhooks

On a Mac (local mode), functions, external webhooks and dashboard Ask AI run only in a project on the server: "This runs only in a project on the server." Do not create or enable them locally. Viewing dashboards/artifacts and their data, schedules, Watchers and non-function internal project/workflow calls remain available. Server behavior is unchanged.

A webhook always starts a workflow: it runs the workflow directly with the delivery as input, with no agent turn. A project's webhooks belong to its workflow; there are no project message webhooks. When an agent should handle the event, the workflow asks that agent, or the caller uses the project's functions instead.

Use the listed `automations` actions with `scope=webhook`, with project over MCP or the current bound project in Builder. List first to discover routes, steps and existing HTTP hooks. Functions are separate reusable entry points; use functions to define or call those.

Create/update take the advertised trigger fields. Preserve existing canonical URLs, bearer/GitHub authentication, input mapping and encrypted secrets. Keep secret values out of messages, logs and saved source. A save is not proof of delivery. Test only when the person requested execution, then inspect receiver/run evidence.

Read [HTTP setup and evidence](references/operations.md) for input mapping, retries and status.
