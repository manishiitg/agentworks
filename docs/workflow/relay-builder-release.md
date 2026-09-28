# Relay Builder: graph, test, publish

## User flow and current state

The Relay Builder chat is the authoring surface. A user describes JSON input, agent prompts, Python scripts, decisions, and the desired JSON output. The Builder uses the shared plan and trigger tools to save `planning/plan.json`, step config, scripts, `variables/variables.json`, and `workflow.json`. The right Graph pane displays those saved nodes and edges. Shared `/api/live` plan notices refresh it while the chat is open.

The Builder can test the draft with `run_full_workflow(group_name=..., variables={"INPUT": "<serialized JSON object>"})`, inspect the terminal run and final `result.json`, and explain the observed JSON or failure. The same workflow executor and Execution Logs are used. This is already implemented and was exercised in the isolated preview; see `relays-acceptance-test-plan.md` case R6.

Publishing an API release is **not implemented**. Today `/api/relays/{id}/runs` dispatches the current mutable `workflow.json` function trigger and `planning/plan.json`. Editing the draft can change the next API run. The workflow HTML `publish` configuration is a separate static page feature and must not be presented as a Relay API release.

## Release contract

The intended chat command is “test with this input, then publish.” The Builder must only say “published” after a dedicated release tool returns a durable version and the active release pointer has been checked. A failed test leaves publishing available to the user but the Builder must report the failure and ask whether to proceed; it must never claim the failure was a pass.

For the first release implementation, `Publish` should freeze a tested graph as version `v1`, `v2`, and so on. New Builder edits remain a draft. External API calls execute the active published version until a later version is published. A run pins its release ID at dispatch and returns that ID on both the start and poll responses. Idempotency keys continue to resolve to the original run even after a newer release is published.

Publish is a local product operation. It creates an API callable release in the existing authenticated server; it does not deploy a server, create a public URL, or use the workflow HTML publisher. Existing access token and `runs:execute` checks remain authoritative.

## Implementation plan with shared code

1. **Release artifact and pointer.** Add a Relay specific release store under its existing workflow workspace, for example `relay/releases/v000001/` and `relay/active.json`. Save a canonical, content hashed snapshot of the executable graph: `planning/plan.json`, `planning/step_config.json`, required saved Python scripts, the Relay output step ID, function trigger contracts, variable definitions/default group, and selected per-step runtime settings. Store references to secrets and installed tools/skills, never secret values. Use the existing workspace file API and the content addressed plan revision helpers in `step_based_workflow/run_provenance.go` where their contract fits. Write the release completely and verify it before updating the active pointer. Keep old releases immutable.
2. **Validation and chat tool.** Add `publish_relay` only to the Relay Builder tool list in `internal/relayproduct/product.yaml`. Register it in the existing workflow phase tool registration for a writable Relay Builder, with the current workflow access policy. Reuse `ValidateRelayPlanStructure`, function trigger validation, and step config/script preflight. Return version, content hash, function names, output step, and timestamp. Add a read only `get_relay_release` result so chat and UI can show the active version and whether draft content has changed. Do not reuse the unrelated HTML `publish` tool or status.
3. **Pinned execution.** At `handleStartRelayRun`, resolve the active release once, validate the requested function against its saved trigger contract, and put the release ID in the durable webhook delivery payload. Pass that trusted ID through the scheduler into the shared executor. Its plan, config, scripts and output step must come from that release, not from live draft files. The existing run store, logs, JSON result projection, authentication, schedule machinery and SSE stay shared. Reject an API run when there is no active release. Builder `run_full_workflow` continues to run the draft for tests.
4. **UI.** Keep the left chat and right Graph. Show `Draft` or `Published vN`, a changed draft indicator, `Test in chat`, `Publish in chat`, and the active version/functions in the Graph or Triggers pane. These controls compose chat requests; they do not duplicate Builder logic. Keep Execution Logs as the source for test and API run evidence. The existing live plan notice updates the draft Graph; a release status notice or refetch updates the version badge after publish.
5. **Acceptance.** From the visible in app preview: create a graph by chat, watch it appear live, test it with named JSON and inspect the final result, publish v1, call the API and verify `release_id=v1`, edit the draft and verify API still runs v1, test and publish v2, verify new calls run v2 and the old run remains pollable as v1. Include script changes, invalid graph/publish failure, permissions, idempotency across a publish, and agent restart. Use preview ports 5181/18841/18842 only; do not touch the separate AgentWorks service.

## Boundary

This release work is separate from the deferred interrupted node resume and run scoped browser work. Neither is implied by a publish result. Schedules need an explicit choice: run the draft for author testing, or pin an active release for production. The implementation should record that choice in the existing schedule configuration rather than introduce a second scheduler.
