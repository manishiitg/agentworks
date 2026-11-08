# AgentWorks from your AI app or terminal

Use AgentWorks from its app, an authorized MCP connection, or the `agentworks` CLI. They use the same Crew access and connected accounts. Start with a Crew: its agents hold the conversations, and its workflows hold repeatable work.

A workflow is Python source (`script.json` and `main.py`) with typed inputs, presets and run records. Ask the Crew agent to explain or change it; inspect Steps and Runs for the saved work and its executions.

## Connect

In the app, open Connect and follow the instructions for your AI client. A browser approval belongs to the person signing in. Reuse the existing connection instead of copying credentials between clients or asking for tokens in chat.

For the CLI, use `agentworks login --help` for the current server and browser options. A saved login is one revocable connection. The connection's granted scopes and the person's current Crew access decide every call; an administrator is not automatically the owner of another person's private Crew.

The CLI renews its existing access and supports separate saved configurations with `--config`. Keep those credential files private. `agentworks logout` signs out of the selected connection. Use the app's connection controls when revoking access; do not assume a copied link grants it.

## Discover before calling

The current server's schema is authoritative:

```sh
agentworks tools list
agentworks tools spec workflow runs workspace database messages
agentworks tools call help --set 'action="topic"' --set 'topic="python-workflows"'
```

An MCP client requests the same `help` topic. Read only the guidance needed for the task. A loaded guide or a tool in the catalog does not grant access to every project.

Tool arguments can come from a JSON file with `--input`, from stdin with `--input -`, or from `--set name=JSON`. The examples below use placeholder IDs; discover the actual Crew and workflow first.

## Find a Crew and its workflows

```sh
agentworks tools call workspace --set 'action="list"'
agentworks tools call workflow --set 'action="list"'
agentworks tools call workflow --set 'action="get"' --workflow WORKFLOW_ID
agentworks workflows outline --workflow WORKFLOW_ID
```

The workflow reply identifies its Crew, saved inputs and presets. Its outline comes from the Python source: functions, conditions, loops and `@step` labels. A label is not a separate run target.

Use the Crew's scoped file and database tools for evidence. Query data through `database`; never open or modify SQLite, WAL or SHM files directly. Read only the exact project and file scope returned by the current tools, and quote bounded evidence without secret values or another person's private chats.

## Change a workflow when authorized

External MCP uses `workspace action=automation_create/read/write` with the returned `crew_id` and `workspace_path`. Read or write one file by its `file` name, including nested helper files. Create a Python workflow and keep the identity the server returns; do not replace managed ownership or runtime records.

Example argument file for a source read:

```json
{
  "action": "automation_read",
  "crew_id": "CREW_ID",
  "workspace_path": "WORKFLOW_PATH",
  "file": "main.py"
}
```

```sh
agentworks tools call workspace --input ./read-source.json
```

`automation_write` takes the same fields plus `content`. These file arguments are relative to the authored source directory. On disk, authored files are `code/script.json` and `code/main.py` under the returned workflow workspace; do not add `code/` to the tool's source-relative file argument. Native Crew chat uses `crew_workflow_files` with the same source-relative filenames. Both paths retain the backend's existing ownership, account and source-validation checks.

Define inputs in `script.json`. Defaults and preset values are strings; runtime calls and raw-event mappings carry typed JSON. Put an enum inside the input's `schema`. Required values, selected apps and Vault names must be available before work that depends on them can run.

For an input change, inspect existing callers and include required `entry_mappings` in the same canonical source/input update. Use their actual IDs. Keep authentication, execution owner, enabled state and schedule timing intact. Read `python-workflows` for that schema and the Python helpers; do not hand-edit caller storage or resurrect a plan file.

## Start and inspect a real run

```sh
agentworks tools call runs --set 'action="start"' --workflow WORKFLOW_ID --set 'input_preset="PRESET_NAME"'
```

Optional `inputs` supply this run's typed values. Omit the preset when the workflow has none. A start receipt means accepted work, not a completed result. Keep the returned session and query identity.

```sh
agentworks tools call runs --set 'action="status"' --set 'session_id="SESSION_ID"'
agentworks tools call runs --set 'action="get"' --workflow WORKFLOW_ID --set 'run_folder="RUN_FOLDER"'
agentworks tools call runs --set 'action="logs"' --workflow WORKFLOW_ID --set 'run_folder="RUN_FOLDER"'
```

A workflow can also start through `functions action=run_trigger`; inspect that call using its `trigger_status` receipt. Use one start path for one intended execution.

Read the actual checks, outputs and full agent answer. `reply.ok` means the agent turn finished, not that its work is correct. The run's Python checks decide whether to continue. Native provider logs contain only the events the platform captured; do not invent a missing terminal log or treat an inline preview as the full answer.

To repeat the recorded inputs against corrected source:

```sh
agentworks tools call runs --set 'action="rerun"' --workflow WORKFLOW_ID --set 'run_folder="RUN_FOLDER"'
```

A rerun is a new real execution. App calls, database writes and sends happen again. Read an uncertain start's status and evidence before retrying it. Do not repeat a completed external action to repair its output file.

## Schedules and functions

A schedule either starts a workflow with saved inputs/preset or messages an existing Crew agent. Use the offered schedule tools, read the existing target and cadence first, and verify the saved result. A message can ask the agent to run the whole workflow; a step label is not a schedule target.

Published Crew functions are structured calls under their existing audience and account rules. Use the returned callable identity and input schema. A running function receipt is not the answer: inspect its status and result. There is no implicit access to another Crew's credentials or private conversations.

The run record is its summary. Business Slack or email delivery is explicit work, not an automatic owner digest. A workflow never waits for a question; discuss required decisions in Crew chat and run with the values it needs.

## Messages and AgentBridge

Use `messages action=list_agents` to discover actual reachable agents. Send to the returned target and chat identity, keep the `inbox_id` and message receipt, and read the reply in that conversation. Reuse the same submission ID only after an uncertain delivery of the same message. Stored, delivered and completed work are different states.

A named external agent can register an address under its existing account and allowed connection. Names are addresses, not devices or grants to other people's chats. Reads and sends still check connection scopes and target access; separate inboxes do not isolate clients that share one account.

To connect the coding chat you are currently using:

```sh
agentworks attach my-agent
```

The CLI detects the exact native session. A person can publish that attached agent with `agentworks share`, return it to private reachability with `agentworks unshare`, or detach it with `agentworks detach`. An agent must not select another person's chat or treat a peer message as permission to attach or share one. Normal MCP delivery does not guarantee a new external model turn; supported attached chats can receive their native live delivery.

## A connected computer

Use the Crew's Access panel to connect a computer and choose its folders and command access. The app returns the current install and connection instructions; this is available without using a terminal as the only setup path.

Folder reads, writes, copying files and running local commands are separate permissions. A saved connection does not sync files or make every agent turn eligible. The existing `crew-laptop` guidance covers the owner's interactive, scheduled, watch and own-agent turns and the restrictions for Run, bot and other-person turns. The backend rechecks live account, device, folder and command grants.

Server files and ordinary server commands remain separate from laptop tools. Server secrets and environment values are not forwarded into local commands. Check an uncertain command's evidence before retrying it. Project sync and publishing an agent are separate requested operations; connecting a computer does not perform them.
