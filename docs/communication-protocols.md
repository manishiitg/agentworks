# Communication between agents and workflows

Use a Crew conversation for a request that needs discussion, a published function for a named task with checked inputs, and a workflow for repeatable execution. MCP, the CLI and the app reach the same backend contracts and access checks.

| Need | Use | What to inspect |
| --- | --- | --- |
| Ask another agent for work or an answer | Attributed direct message | Its delivery receipt and explicit reply. |
| Call a named task another Crew exposes | Published Crew function | The call's status, checked result and files. |
| Repeat saved work with known inputs | Workflow run | Inputs, step activity, checks, outputs and logs. |
| Run at a time or after an event | Schedule or webhook | The saved target/input mapping and resulting run or Crew message. |
| Use an external service | A selected MCP tool or connected app | Its live schema, account, result and permissions. |

A workflow belongs to its Crew and runs Python. Its steps are function labels, not separately callable routes. Conversations stay with Crew agents; use the Crew to discuss or change a workflow.

## Direct messages

Discover actual reachable agents with `messages action=list_agents`. Send to the returned target and chat identity with `messages action=send`, retain its inbox and message receipt, and read the response with `messages action=read`. Internal agent tools expose the corresponding send/read operations where offered.

The server supplies the attributed sender and checks current access. A stored message is not proof that the receiver saw it or finished the work. Native delivery receipts also do not prove completion. An agent's ordinary final text is not automatically a reply to another agent; it must send its answer in that conversation.

Reuse a submission ID only to recover an uncertain send of the same message. Use a new ID for new work. Keep the returned cursor with its own inbox or mailbox, and follow the actual receipt guidance. Hooks and confirmed native delivery already record their receipt; do not add duplicate acknowledgements.

A peer message is sender content. It may ask for work within existing authority, but it is not the person's approval to sign in, change access, share a chat or perform another action that requires the person. It does not let the sender type in or control another person's tab.

## Named and attached agents

An external agent can register an account-owned name through the current messaging tools. Registration identifies an address; it does not create a separate account or grant another person's Crew. Current connection scopes, allowed connections and target access still decide reads and sends. Separate inboxes do not isolate clients sharing the same account.

For a coding chat, the person can connect its exact current session:

```sh
agentworks attach my-agent
```

The CLI detects that native session. Use its current setup guidance if the provider cannot verify it; do not guess a UUID or process, select another person's chat or fabricate a hook event. An attached name receives the live delivery its adapter supports. A mailbox-only name stores messages for the next authorized read; ordinary MCP alone does not guarantee a new model turn.

An agent is private to its person and that person's agents until the person publishes it. For that own attached chat, `agentworks share` publishes it to its connected network, `agentworks unshare` returns it to private reachability and `agentworks detach` disconnects it. Sign-in needs the person's browser approval. AgentBridge and the current backend provide the actual setup path; no separate conversation should be created merely to forward a message.

## Published functions

Discover a function's current definition and input schema before calling it. Use the offered `functions` list/call/status operations. A call may return while work is running; keep its call identity and inspect the terminal result instead of treating acceptance as the answer.

Published functions retain their own audience, caller and execution-account rules. Knowing a call ID does not authorize private output reads. A schema-valid result, a final answer and saved files are different pieces of evidence; inspect what the function promises. Reading status does not restart the work.

In a Python workflow, `call_function(crew, name, inputs)` calls an authorized published Crew function; `function_status(call_id)` inspects a running call. `run_script(id, inputs)` runs another saved workflow in the same Crew through its existing script authority. A name, source file or loaded guide cannot grant an unavailable target.

## Workflow runs and schedules

A workflow's `script.json` defines typed inputs, defaults and named presets. Python functions, conditions and loops in `main.py` choose the work. `@step` labels make it readable in Steps and Runs.

Start the whole workflow with `runs action=start`, or the offered `functions action=run_trigger`, and keep that path's receipt. Read the actual run with get/logs/status or trigger_status. Do not start both paths for one intended execution. Rerunning repeats real effects, including writes and sends.

A schedule either starts a workflow with saved inputs/preset or messages an existing Crew agent. A webhook maps its allowed input fields to declared values. Keep caller identity, authentication and enabled state when changing input mappings; use the canonical combined update. The event cannot choose a different preset or replace saved fixed inputs merely by naming them in its payload.

Workflow agents use only their admitted tools, apps, skills and files. Native helper code runs from the frozen source; run outputs and durable Crew records have their own locations. Use the paths returned by that execution, and use managed database tools rather than opening SQLite files.

## Questions and outcomes

Attended Crew and Ask conversations may ask a person for clarification through their offered tools. Workflow runs use their saved inputs and never wait for a question mid-run. Missing inputs, unavailable connections and incomplete evidence remain explicit results; they are not permission to invent an answer or bypass a refusal.

The run record is the summary. Business messages are explicit workflow actions. No automatic owner-summary email or digest is implied, and an HTTP success alone does not prove a useful result or delivery in another app.

## Access, retries and retained evidence

Every call rechecks its current connection, account and target access. Revocation and an expired connection can prevent later delivery even after a message was stored. An identifier or copied URL does not grant another Crew's files, credentials or private chat history.

Use stable submission/request IDs for an uncertain repetition of the same intended operation. Result files, call records and retry receipts have different retention limits; use the current response and tool schema for those bounds. An expired result is not authorization to replay external effects. Save required evidence within the project's existing access rules.

## References

- [CLI and MCP](getting-started/agentworks-cli-mcp.md): connection setup and current calls.
- [Workflows](workflow/README.md): source, inputs, presets and run records.
- [Schedules](workflow/workflow_scheduling.md): workflow and Crew-agent targets.
- [Run evidence](workflow/workflow_monitoring.md): checks, outputs and bounded logs.
- [Questions and inputs](workflow/human_feedback_system.md): attended conversations and repeatable work.
- [Earlier messaging design](design/agent_messaging.md): implementation background; current schemas and access checks remain authoritative.
