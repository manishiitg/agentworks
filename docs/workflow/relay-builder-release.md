# Relay Builder: graph, test, publish

## User flow and current state

The Relay Builder chat is the authoring surface. A user describes JSON input, agent prompts, Python scripts, decisions, and the desired JSON output. The Builder uses the shared plan and trigger tools to save `planning/plan.json`, step config, scripts, `variables/variables.json`, and `workflow.json`. The right Graph pane displays those saved nodes and edges. Shared `/api/live` plan notices refresh it while the chat is open.

The Builder can test the draft with `run_full_workflow(group_name=..., variables={"INPUT": "<serialized JSON object>"})`, inspect the terminal run and final `result.json`, and explain the observed JSON or failure. The same workflow executor and Execution Logs are used. This is already implemented and was exercised in the isolated preview; see `relays-acceptance-test-plan.md` case R6.

Publishing is implemented through the Relay only `publish_relay` chat tool. It copies the executable, text based workspace files to `Workflow/.relay_releases/<workspace-hash>/vN`, records a content hash, and moves the active version pointer only after validating the copied graph. Builder edits remain in the original workspace. `/api/relays/{id}/runs` defaults to the active release; its optional `version` selects an older release. The workflow HTML `publish` configuration is a separate static page feature.

## Custom Python tools

Relay agents select named Python tools through the existing
`enabled_custom_tools` setting (`python_tools:<name>`). Source and metadata are
stored in `code/tools/<name>/main.py` and `tool.json`. Metadata specifies the
description, object JSON input schema and optional timeout. A synchronous
`run(input)` returns JSON directly to the agent; this differs from a graph
script node's `set_output` handoff. The manifest-owned relay-builder skill has
the complete authoring example.

The shared execution-only agent factory adds immutable `ToolDefinition`s before
construction, so native LLM tools, CLI MCP bridge discovery and existing tool
receipts use the same registration. Each executor captures the guarded shell
after step session/environment injection. No host Python executor, permission
store, tool-management API or separate Relay agent loop is introduced. Schema
validation rejects bad arguments before running Python. Only explicitly named
tools are exposed; source grants are read-only and shared platform state is
not mutated. Existing step credentials and grants apply, including managed
workflow DB restrictions.

Release snapshots include tool source/metadata and step selection. Publishing
checks selected definitions and saved main.py from its exact snapshot;
runtime uses that version's source. It does not import user code during publish
or check package/connectivity availability. Named tool errors go to the existing
agent loop; side effects are not automatically replayed. This does not add crash
recovery or JSON repair.
Ticket: [PLAT-423](../bugs/pulse_platform/coding-agent-bridge/plat-423.md).

## Release contract

The intended chat command is “test with this input, then publish.” The Builder must only say “published” after a dedicated release tool returns a durable version and the active release pointer has been checked. A failed test leaves publishing available to the user but the Builder must report the failure and ask whether to proceed; it must never claim the failure was a pass.

For the first release implementation, `Publish` should freeze a tested graph as version `v1`, `v2`, and so on. New Builder edits remain a draft. External API calls execute the active published version until a later version is published. A run pins its release ID at dispatch and returns that ID on both the start and poll responses. Idempotency keys continue to resolve to the original run even after a newer release is published.

Publish is a local product operation. It creates an API callable release in the existing authenticated server; it does not deploy a server, create a public URL, or use the workflow HTML publisher. Anyone who can see a Relay can execute it and poll their own API runs. Publishing and editing require Owner or Write access. Runs use the owner's configured provider accounts, workflow secrets, and quota; they never attach a reader's private credentials. Existing access-token `runs:execute` and workflow scopes remain authoritative. Listing releases requires `workflows:read` for tokens. Disabled functions and live caller restrictions apply to polling and idempotent retries as well as new dispatches.

## Implementation and shared code

1. **Release artifact and pointer.** `relay_releases.go` copies executable text files, including `workflow.json`, `planning/plan.json`, variables, step config, and saved `code/<step>/main.py`, into a separate nested workspace. Run history, costs, chat, generated reports, and databases are excluded. It saves `release.json` with the file set/hash and updates `active.json` last. The release reader verifies the file hash before every new API run. The release workspace is outside the draft's folder guard. The shared workflow write route refuses writes targeting release workspaces.
2. **Validation and chat tools.** `publish_relay` and `get_relay_releases` are registered only for an interactive, writable Relay Builder and allowed by `internal/relayproduct/product.yaml`. Publish requires a valid Relay graph, final authored output agent, and at least one enabled function with required object `INPUT`. Script nodes require code layout 1 and saved `main.py`. The Builder receives the exact version and hash.
3. **Pinned execution.** `handleStartRelayRun` resolves the active or requested release, and `dispatchWorkflowFunction` runs the existing scheduler/executor against its snapshot workspace. The saved delivery payload pins `relay_version` and the output step. Polling checks the run's scope and returns `version` with the JSON result. Duplicate keys still resolve to their original run across publish events.
4. **UI.** Triggers shows the active version and available versions, with `Test draft in chat` and `Publish in chat` actions. The Graph continues to show the editable draft. The release badge refetches on the shared live plan notice after publish. Execution Logs has a Draft/vN selector and reuses the existing run-folder and log readers for the selected snapshot.
5. **Acceptance.** Use preview ports 5181/18841/18842 only. Live Builder chat published v1, a new draft changed the greeting from `Hello` to `Hi`, a v1 API call still returned `Hello`, and publishing v2 made default API calls return `Hi` while explicit v1 calls still returned `Hello`. The test run IDs are in `relays-acceptance-test-plan.md`.

## Boundary

Interrupted node resume remains deferred. Relays expose API function triggers only; cron/calendar scheduling is retired. Version pinning applies to function/API calls. Existing timed entries are ignored when loading older drafts or releases and removed on the next draft save; published files stay immutable. Release snapshots currently accept UTF-8 text files up to 50 MiB total; binary workspace data is outside this release contract. A published script or agent that writes to its own release workspace can change executable files; the next run's hash check then rejects that release until the draft is republished. Release isolation is therefore tested for Builder edits, not arbitrary in-run writes.

## Review fixes verified on 2026-10-01

- Release readers reject aliased paths and use the live draft's grants. Release version listing and logs are available to readers; own API polling remains bound to the saved caller and scope. Missing explicit versions return 404. Invalid release metadata stays visible with an error instead of disappearing from the list.
- Provider-account admission resolves the release's live Relay identity. Database and cost tools preserve the full release workspace. Draft cost totals include all published versions while per-version views retain their original attribution.
- Capacity waits are durable nonterminal scheduler states. The shared scheduler discovers them after restart, atomically claims the original run once, restores its pinned input, group, routes and checkpoint, and rechecks live caller access and release integrity before resuming. This covers provider quota waits; arbitrary process-crash resume remains deferred.
- Accepted product choices: Write editors may publish. Snapshot variable files may contain stale secret copies; live secret resolution supplies runs, and rotation does not rewrite older snapshots.
- Backend regression tests exercise reader acceptance (202), own polling (200/version), other-run and no-access denial (404), credential ownership, revocation, namespace readers, durable waits, and cost aggregation. The ingress test deliberately fails execution on an absent temporary runtime folder; it does not claim a new live LLM run. Frontend checks cover the visible access policy and broken-release state. Prior live preview evidence remains in the acceptance plan; no deployment or preview restart is part of these fixes.

Verification: backend server build and frontend production build passed. The shared virtual-tools, costledger, costobserver, schedulerstate and Relay product package suites passed; 22 frontend regressions passed. The broader workflow executor suite has one existing failure, `TestValidateStepLLMConfigEnforcesAgyAlphaGate` (`AGY accepted without alpha flag`), reproduced unchanged on a clean `origin/main` worktree at `c48042b56`.

## Integration scope (2026-10-03)

Relays are API products with function triggers and selected MCP tools/skills. They reuse
the platform's authorized Google app connections for Drive, Sheets, Calendar
and Gmail, including per-service grants; the right pane labels the existing
connection panel **Google apps**. Plan creation needs no Google connection.
Slack and WhatsApp connections, tools, bot routes and notifications are excluded.
The product manifest owns the Builder tool allowlist, prompt and skill; manifest
validation and execution-time checks enforce the same scope. Existing saved
Slack bindings are suppressed when constructing a Relay API execution context.
See [PLAT-389](../bugs/pulse_platform/integrations/plat-389.md).
