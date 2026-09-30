# AGY Full CLI — local integration

AGY's full native toolset is available alongside the AgentWorks MCP bridge.
The SDK accepts all four platform modes; mcpagent forwards the resolved mode
instead of silently reducing Full CLI to hybrid.

| Mode | AGY behavior |
|---|---|
| `mcp_only` | MCP tools; native file operations, shell and delegation denied. |
| `hybrid` | MCP plus native file/web reads and searches; native writes, shell and subagents denied. |
| `full` | Full native toolset, requiring an enforced Linux Landlock launch policy. Linux AGY certification remains open. |
| `full_unconfined` | Full native toolset on an explicitly opted-in single-user local host. |

## Enable locally

Start the single-user backend with `AGY_ALPHA=1` and
`AGENTWORKS_CLI_FULL_UNCONFINED=on`. Keep **Native agent tools** enabled for the
chat. The local `agent_go/run_server_with_logging.sh` runner defaults the Full CLI
flag to `on`; set it to `off` to keep hybrid. Restart the backend when changing
its environment; an existing native session must be relaunched to adopt a new tool mode. The server rejects the
unconfined upgrade in multi-user mode. A chat with native tools off stays
`mcp_only`.

Local Full CLI permits AGY's own reads, searches, file creation and editing,
shell commands, and native subagents. Private MCP configuration and platform
tools remain mounted. The CLI's permission prompts are skipped for this
explicit full mode. The temporary workspace hook is restored when the session
closes. Unconfined mode runs with the local user's host permissions; the working
directory is not a filesystem boundary.

**Initial rollout: local only. Do not use AGY on RTS or excellence for now.** This work
does not deploy to either host, certify Linux confinement, or enable AGY for
shared users.

## Authentication and completion

AGY 1.2.14 uses the same existing Google/Gemini API key used by Pi when exported
as `GEMINI_API_KEY`. The adapter sets `modelProvider: "gemini"` in its private
home. The key does not need to be duplicated into global AGY settings.

Confida now explicitly opts into the alpha server provider with
`AGY_ALPHA=1`, `AGY_ALPHA_MULTI_USER=1` and
`AGENTWORKS_CLI_LANDLOCK=on`. Publication and execution reject the
multi-user opt-in if confinement is disabled. The managed installer verifies
Google's release checksum and configures the service's AGY settings for
Gemini API-key mode, using the existing service `GEMINI_API_KEY`. Other
multi-user deployments remain gated. This opt-in does not extend the local
certification to all Linux native tools or providers.

Native subagent invocation is asynchronous. The retained completion reader
requires both the child's completed native record and its notification in the
parent conversation before accepting the settled parent answer. Terminal
idleness and an interim “waiting” answer do not prove completion. Headless
unstructured full-mode replies use the last completed native assistant answer;
schema replies retain AGY's structured result, including its `finish` output.

## Local validation

All five live checks below passed locally on 2026-09-30 with no failures or
skips. The SDK adapter/formatting suites, mcpagent mode/routing checks and
platform single-user/upgrade checks also passed. The live tests use an isolated
home, disposable workspace and the existing Pi Google key. The new Full CLI
checks use `gemini-3.8-flash-high` and a real MCP canary:

- `TestAgyCLIRealFullNativeToolsExec`: native create/edit, shell file canary,
  inherited-workspace subagent, completed child file and exactly one MCP call.
- `TestAgyCLIRealFullNativeToolsInteractive`: the same native operations plus a
  follow-up in the same native conversation and tmux session.
- Existing hybrid-denial, headless contract and structured resume checks guard
  the restricted modes and schema results.
- Unit checks cover all mode decisions, malformed-hook denial, the full-mode
  launch requirement, child notification/completion and platform single-user
  gating.

Browser/computer actions and subscription-specific AGY features are permitted
by the full-mode hook but are not certified by these checks. The prior
[Pi/AGY P0 certification](../testing/pi_agy_native_mcp_p0_20260930.md) remains the
baseline for the broader provider contract.
