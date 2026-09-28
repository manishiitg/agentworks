# Relay Builder: graph, test, publish

## User flow and current state

The Relay Builder chat is the authoring surface. A user describes JSON input, agent prompts, Python scripts, decisions, and the desired JSON output. The Builder uses the shared plan and trigger tools to save `planning/plan.json`, step config, scripts, `variables/variables.json`, and `workflow.json`. The right Graph pane displays those saved nodes and edges. Shared `/api/live` plan notices refresh it while the chat is open.

The Builder can test the draft with `run_full_workflow(group_name=..., variables={"INPUT": "<serialized JSON object>"})`, inspect the terminal run and final `result.json`, and explain the observed JSON or failure. The same workflow executor and Execution Logs are used. This is already implemented and was exercised in the isolated preview; see `relays-acceptance-test-plan.md` case R6.

Publishing is implemented through the Relay only `publish_relay` chat tool. It copies the executable, text based workspace files to `Workflow/.relay_releases/<workspace-hash>/vN`, records a content hash, and moves the active version pointer only after validating the copied graph. Builder edits remain in the original workspace. `/api/relays/{id}/runs` defaults to the active release; its optional `version` selects an older release. The workflow HTML `publish` configuration is a separate static page feature.

## Release contract

The intended chat command is “test with this input, then publish.” The Builder must only say “published” after a dedicated release tool returns a durable version and the active release pointer has been checked. A failed test leaves publishing available to the user but the Builder must report the failure and ask whether to proceed; it must never claim the failure was a pass.

For the first release implementation, `Publish` should freeze a tested graph as version `v1`, `v2`, and so on. New Builder edits remain a draft. External API calls execute the active published version until a later version is published. A run pins its release ID at dispatch and returns that ID on both the start and poll responses. Idempotency keys continue to resolve to the original run even after a newer release is published.

Publish is a local product operation. It creates an API callable release in the existing authenticated server; it does not deploy a server, create a public URL, or use the workflow HTML publisher. Existing access token and `runs:execute` checks remain authoritative.

## Implementation and shared code

1. **Release artifact and pointer.** `relay_releases.go` copies executable text files, including `workflow.json`, `planning/plan.json`, variables, step config, and saved `code/<step>/main.py`, into a separate nested workspace. Run history, costs, chat, generated reports, and databases are excluded. It saves `release.json` with the file set/hash and updates `active.json` last. The release reader verifies the file hash before every new API run. The release workspace is outside the draft's folder guard. The shared workflow write route refuses writes targeting release workspaces.
2. **Validation and chat tools.** `publish_relay` and `get_relay_releases` are registered only for an interactive, writable Relay Builder and allowed by `internal/relayproduct/product.yaml`. Publish requires a valid Relay graph, final authored output agent, and at least one enabled function with required object `INPUT`. Script nodes require code layout 1 and saved `main.py`. The Builder receives the exact version and hash.
3. **Pinned execution.** `handleStartRelayRun` resolves the active or requested release, and `dispatchWorkflowFunction` runs the existing scheduler/executor against its snapshot workspace. The saved delivery payload pins `relay_version` and the output step. Polling checks the run's scope and returns `version` with the JSON result. Duplicate keys still resolve to their original run across publish events.
4. **UI.** Triggers shows the active version and available versions, with `Test draft in chat` and `Publish in chat` actions. The Graph continues to show the editable draft. The release badge refetches on the shared live plan notice after publish. Execution Logs has a Draft/vN selector and reuses the existing run-folder and log readers for the selected snapshot.
5. **Acceptance.** Use preview ports 5181/18841/18842 only. Live Builder chat published v1, a new draft changed the greeting from `Hello` to `Hi`, a v1 API call still returned `Hello`, and publishing v2 made default API calls return `Hi` while explicit v1 calls still returned `Hello`. The test run IDs are in `relays-acceptance-test-plan.md`.

## Boundary

Interrupted node resume remains deferred. Cron and calendar schedules currently run the draft through the shared scheduler; version pinning in this release applies to function/API calls. A future schedule option can select a release through the same scheduler. Release snapshots currently accept UTF-8 text files up to 50 MiB total; binary workspace data is outside this release contract. A published script or agent that writes to its own release workspace can change executable files; the next run's hash check then rejects that release until the draft is republished. Release isolation is therefore tested for Builder edits, not arbitrary in-run writes.
