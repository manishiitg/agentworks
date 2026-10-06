[← platform / security-sandbox](index.md)

# PLAT-307 — A manual workflow run could search, install, or remove MCP servers mid-execution

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | sandbox |
| Area | security |
| Summary | Deployed to RTS as `009b0d6c1`: AgentWorks `product.yaml` chat capability policy, interactive Builder MCP registration, explicit execution/Pulse/read-only boundaries, native catalog refresh and durable MCP config across… |

| Coordination | Value |
|---|---|
| Assigned agent | Claude (original), Codex (Builder follow-up) |
| Ticket state | MCP admission deployed to RTS (`009b0d6c1`); prompt/skill configuration follow-up implemented locally |
| Last synchronized | 2026-09-11 |
| Priority | P1 — Builder integration configuration unavailable on RTS; no evidence of unauthorized MCP installation |

## 2026-09-11 — Explicit Builder/Run prompts and skills

`chat.builder` and `chat.run` now select real Markdown prompt entry files and
ordered core skill lists in AgentWorks `product.yaml`. Shared workflow rules are
included once; the existing dynamic context composer and capability policy stay
in force. Run declares only its two existing runtime bundles. Workflow-selected
skills remain additive, and MCP documentation stays admission-filtered.

Configuration is validated before use; prompt/shared-file/skill-list changes
invalidate retained native instruction identity. API sessions keep their normal
prompt delivery. This follow-up is implemented locally and is not yet deployed.
The extracted prompts matched the previous rendered output for all eight mode,
projection and code-execution combinations in a migration comparison.

## 2026-09-11 — Builder admission and durable MCP configuration

The original fix correctly excluded manual execution but also codified an older
bug: interactive Builder uses `workflow_phase` too, so its MCP management tools
were never registered. The RTS incident (Jam selected but absent from config)
confirmed this on release `2ffcd10-20260911090730`; MCP configuration was unlocked.

Deployed to RTS as `009b0d6c1`: service health and durable MCP storage verified.
An authenticated Jam run remains unverified. This section supersedes the older
registration-gate design below.

- AgentWorks `product.yaml` now declares `chat_policy`: Builder/Run capability
  sets, caller origins, and a read-only ceiling. Unknown capability names and
  missing policy roles fail validation. Tool implementations stay in Go.
- A shared resolver distinguishes interactive chat, scheduled execution, Pulse
  maintenance, children, bots, and notifications, retaining stored provenance on
  resumed turns. Explicitly promoted schedules can become interactive; children
  cannot use that promotion to obtain installer tools.
- Interactive writable Builders receive the actual MCP management registrar.
  Run, schedules, Pulse, children, notifications and read-only users do not.
  Other product manifests still apply their own tool policy.
- Host plan/report/secret/KB/improvement/UI admission uses the resolved policy.
  Legacy workshop procedures receive the corresponding effective mode; typed
  Pulse reviewer/fixer executor restrictions remain intact. Pulse maintenance
  explicitly retains improvement authority and never receives MCP management.
- Integration guidance follows MCP admission. A retained native chat reconnects
  on capability/config changes using the existing durable-conversation replay
  path; config contents and credentials are never put in its identity/logs.
- `AGENTWORKS_MCP_STATE_DIR` keeps the base/overlay pair outside releases. The
  service refreshes the shipped base catalog, migrates a legacy overlay once,
  and preserves existing user entries. RTS rootless/rootful service definitions
  and Dominion/Confida deployments configure it. Local checkouts keep existing paths
  unless this environment variable is set.
- Preflight now accurately reports missing configured names, without claiming
  authentication/connectivity/tool-count validation or requiring a host restart.
- Installer optional strings no longer stringify omitted values as `<nil>`.
  Custom additions trigger per-server discovery rather than a metadata-only refresh.

Validation: the full server, shared manifest, AgentWorks policy and guidance
package suites pass, plus focused workflow mode/read-only/Pulse tests. Deployment
scripts pass `bash -n`; `git diff --check` is clean.

Critical follow-up review found and corrected two additional boundaries:
retained native sessions persist a capability/config fingerprint so compatible
sessions survive server restart, while fresh/API chats keep normal history;
and workspace presentation no longer grants Gmail connection-editing tools to
read-only users. Legacy MCP overlays are published atomically without replacing
an existing durable file. Tests cover these boundaries explicitly.

Regression coverage includes real MCP registration/handler invocation, origin
and read-only admission, Pulse preservation, guidance admission, manifest
validation, config-driven session refresh and release-replacement persistence.

Related design: [single tool surface](../../../../design/agent_tool_surface_single_source.md).

## Problem

`registerMultiAgentMCPServerTools` (`search_mcp_catalog`, `inspect_mcp_server`,
`install_mcp_server`, `add_mcp_server`, `edit_mcp_server`, `remove_mcp_server`,
`get_mcp_server_logs`, `trigger_mcp_discovery`) is meant to be a workflow-builder
/chat-time capability only — installing, removing, or discovering MCP servers
is a configuration decision, not something a running automation should do to
itself.

The actual gate (`server.go:5495`, `isToolBackedChat := !isWorkflowPhase`, where
`isWorkflowPhase := req.AgentMode == "workflow_phase"`) only excludes the
literal string `"workflow_phase"`. There are two separate "running a workflow"
code paths, and only one of them uses that string:

- **Scheduled/cron runs** (`scheduler.go:4204`, `buildWorkshopRequest`) set
  `agent_mode: "workflow_phase"` (distinguished internally from real builder
  chat by `execution_options.workshop_mode: "run"`) — correctly excluded.
- **A manual Run-button click** (`frontend/src/components/workflow/hooks/useWorkflowExecution.ts:185,256`,
  the "Execute workflow for preset..." / "Execute step..." requests) sets
  `agent_mode: 'workflow'` — a different string, not `"workflow_phase"`. This
  fell through `isWorkflowPhase == false` → `isToolBackedChat == true`, so the
  full MCP server management toolset was registered for a manual run.

Net effect: triggering a workflow manually gave that run's agent the ability to
search GitHub's MCP Registry/Smithery, install a new (unvetted) MCP server with
an OAuth flow or API key, or delete an existing one from the account-wide
config — all live, during what's supposed to be execution of an already-built
automation, with no human at the builder UI to review any of it. Scheduled runs
of the identical workflow never had this exposure, purely because of which
internal request-builder path constructs the query.

## Fix

Added `mcpServerToolsEligible(isToolBackedChat, agentMode)` (`workflow_manifest_routes.go`) —
`isToolBackedChat && strings.TrimSpace(agentMode) != "workflow"` — and use it in
place of the bare `isToolBackedChat` check that gated
`registerMultiAgentMCPServerTools`'s call site (`server.go`, inside the
`isToolBackedChat` block that also registers secret-management tools).

Deliberately scoped to just the MCP-server-tools registration, not a redefinition
of `isToolBackedChat`/`isWorkflowPhase` themselves — those two flags also gate
LLM discovery tools, skill tools, and secret-management tools across ~30 other
call sites in the same function, and this fix isn't making a determination about
run-mode eligibility for any of those; only MCP server management was reported
and verified as the concern.

## Verification

- `TestMCPServerToolsEligibleExcludesManualWorkflowRun` (`workflow_manifest_routes_test.go`) —
  table test covering multi-agent chat (eligible), empty/legacy agent-mode
  aliases (eligible), `workflow` with and without surrounding whitespace
  (excluded), and `workflow_phase` (excluded, matching prior behavior).
- `go build ./...` and `go vet ./cmd/server/...` clean.
- Related, separate fix in the same pass: `remove_mcp_server` now scans every
  workflow visible to the user for a dangling reference to the server being
  removed (`workflowsReferencingMCPServer`) and reports affected workflows in
  its response, since removal is account-wide and previously left no trace for
  a workflow that still had the server selected.

## Deployment

Pending. Not yet deployed to any server; implemented and tested locally only.

## Register notes

Deployed to RTS as `009b0d6c1`: AgentWorks `product.yaml` chat capability
policy, interactive Builder MCP registration, explicit execution/Pulse/read-only
boundaries, native catalog refresh and durable MCP config across releases.
The follow-up selecting Builder/Run Markdown prompts and core skills from YAML
is implemented and tested locally; deployment is pending for that follow-up.
See [PLAT-307](plat-307.md) for scope and tests.

[PLAT-307](plat-307.md) fixes a manual Run-button execution
(`agent_mode: "workflow"`) getting the full MCP server management toolset
(search/inspect/install/add/edit/remove/logs/discovery) that only the workflow
builder chat and regular chat are meant to have — scheduled/cron runs were
already correctly excluded via a different `agent_mode` value. Also makes
`remove_mcp_server` scan and warn about workflows left with a dangling
reference after an account-wide removal. Implemented and regression-tested
locally; deployment pending.
