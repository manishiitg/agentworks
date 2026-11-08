# Workflows

A workflow is repeatable work inside a Crew. Its conversation stays with a Crew agent. Its page has **Steps** for the saved work and **Runs** for each execution.

## Write and run

- [First workflow](../getting-started/first-workflow.md): describe the job, review inputs and evidence, run it, then add a schedule when wanted.
- [Workflow definition](workflow_manifest_architecture.md): `script.json`, typed inputs, presets and selected secret names.
- [CLI and MCP](../getting-started/agentworks-cli-mcp.md): inspect or change authorized workflow source and start a real run.
- [Schedules](workflow_scheduling.md): start a workflow or message a Crew agent.
- [Monitoring](workflow_monitoring.md): results, checks, logs and available usage.
- [Questions and inputs](human_feedback_system.md): Crew conversations can ask a person; workflow runs use their saved inputs without waiting.
- [Communication](../communication-protocols.md): attributed agent messages and calls to published functions.

## Python model

The source is `script.json` plus `main.py`, with ordinary Python deciding order through functions, conditions and loops. `@step` labels show that work in Steps and Runs; they are not separately runnable. `agent()` performs an AI turn, and Python checks its answer and output files before continuing.

Read the bundled `python-workflows` skill before creating or changing source. In an MCP client, request `help action=topic topic=python-workflows`. Use the canonical workflow tools so the backend validates source, inputs and caller mappings. There is no plan file or separate workflow chat to edit.

Saved data belongs to the Crew. Use managed database tools, keep durable files separate from a run's own outputs, and leave secrets in the existing selected account. Workflow execution, app calls and sends do real work.

## Earlier design records

This folder also contains earlier implementation notes. Terms such as plan graphs, routing files, learn-code phases and separate Builder conversations in those records describe the retired engine. Use the guides above and the current tool catalog for authoring and execution.
