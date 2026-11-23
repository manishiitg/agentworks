# Coding CLI modes: mcp_only and full

## Overview

A coding CLI runs in exactly one of two tool modes. `mcp_only` exposes only
the MCP bridge tools; `full` adds the CLI's own native tools (reads,
edits, subagents) alongside the bridge. Full applies only when the CLI starts
confined (Landlock on Linux, Seatbelt on a Mac) — an unconfined `full` runs as
`mcp_only`, never with native tools. There is no reads-only middle state.

The retired names `hybrid` and `full_unconfined` normalize to `full` (owner
decision 2026-10-03), so old configs keep working under the same confinement
gate.

## Key Files & Locations

| File | Contents |
|---|---|
| `agent/coding_agent_integrations.go` | Mode constants, `fullCLIEnabled()`, `nativeCodingToolsEnabled()`, per-CLI option builders |
| `agent/agent.go` | Per-CLI routing preambles and Full-mode tool wording |
| `agent/coding_agent_modes_contract_test.go` | `TestCodingAgentModesContract`: prompt-vs-launch-options per CLI per mode |

## How the mode is decided

```text
requested mode == "full" AND policy.Confined()  →  full (native tools + bridge)
otherwise                                       →  mcp_only (bridge only)
```

`Confined()` means the Landlock launcher on Linux or Seatbelt on a Mac. Pi has
no confined full mode and stays bridge-only in both modes.

Full mode does not enable the CLI's own shell by default (PLAT-491). The
provider library's `nativeshell.Enabled()` is the shared switch used by both
the adapters and the routing prompt. `AGENTWORKS_CLI_NATIVE_SHELL=on` (also
`1`, `true`, or `yes`) enables the native shell in confined Full mode. Without
that opt-in, commands use the permitted MCP `execute_shell_command` bridge;
non-shell native tools keep their Full-mode behavior. Codex relies on its
shell for native file operations, so those also use the bridge while it is off.

## Per-CLI full behavior

| CLI | Full mode |
|---|---|
| Claude Code | Native reads, search, skills, subagents, todos, web and edits; native Bash requires the shell opt-in; subagents inherit the tool list |
| Codex | Native subagents; native shell requires the shell opt-in, inside workspace-write |
| Cursor | Native file tools via `WithCursorFullNativeTools`; its Delete tool stays denied; commands and deletion use the bridge unless native Shell is opted in; granted folders passed as `--add-dir` |
| Muse | Native file, search and subagent tools under the provider's Full-mode policy; native Bash, stdin and monitor require the shell opt-in |
| Agy | Native non-shell tools; native shell requires the shell opt-in; never copies the person's statusLine into the private home |
| Pi | Bridge-only (no full mode yet) |

The routing preamble names each CLI's real tools, the sandbox boundary, and
the protected files. The contract test fails if prompts and launch options
disagree for any CLI in either mode.

## Provider-specific instructions

`codingAgentProviderRoutingPreambleForContext()` in `agent/agent.go` is the
shared location for per-CLI runtime wording. `effective_system_prompt.go`
renders it inside `<runtime_tools>` at the outbound boundary with the current
profile and turn's tool permissions. `AddInstructions` supplies general caller
extensions; `CodingRuntimeConfig.BridgeRoutingInstructionsOverride` replaces
the entire default routing block, including provider-specific guidance.

Muse receives an extra clarification when its native shell is restricted and
the bridge shell is permitted: native shell/write restrictions do not disable
the MCP shell or authorized writes through it. A native refusal alone is not
evidence that the bridge is unavailable. The clarification is omitted when
the profile or current turn excludes `execute_shell_command`.

The provider library also owns instructions tied to its own integrations:

| CLI | Additional provider-local instructions |
|---|---|
| Claude Code | Shell-routing hook refusals explain platform credentials and the MCP route; native clarification hooks supply submitted answers |
| Codex | Prompt transport and feature settings; no second Full-mode shell-guidance block found |
| Cursor | `cursorBridgeOnlySystemPrompt()` adds session rules, including shell state, denied delegation, and platform-owned goals/questions |
| Muse | Tool-policy refusal reasons, shell-routing hook reasons, and a one-time bridge reminder on native resume |
| Agy | Tool-mode hook reasons distinguish the native shell from `call_mcp_tool` on the bridge server |
| Pi | Bridge-only tool mode and instruction-file projection; no second Full-mode shell-guidance block found |

Keep these provider-local messages consistent with the shared routing preamble
and actual launch policy. They can affect the model after the initial system
instructions, especially after a refusal or session resume.

## Attended clarification questions

Claude Code's `AskUserQuestion` is enabled in either mode when the host enables
user answers for a retained interactive session, advertises
`request_clarification`, and registers that tool's answer handler. Its
`PreToolUse` hook sends the questions to the host's selectable chat card and
returns only the user's submitted answers. It supports grouped questions,
multi-select, custom text, and subsequent prompts. A cancellation or transport
failure denies the native call rather than choosing an answer.

This question tool does not grant filesystem or shell access. Unattended,
unregistered, nonpersistent, and structured transports keep it disabled. Other
coding CLIs can use the same host-registered `request_clarification` bridge
tool. Hosts must expose the bridge's session-scoped HTTP API and have `python3`
available for the native Claude hook. Existing routing hooks are preserved.

## Mac (Seatbelt) notes

Every coding CLI runs under Seatbelt on a Mac with home open. Practical
consequences: Claude's granted folders arrive as `--add-dir` so its own edits
reach the workflow; Codex pre-trusts its folder in the person's own `~/.codex`
and sweeps stale session profiles; personal Codex MCP stays off; Cursor needs
explicit trust/force for new folders. Unconfined launches (sandbox unavailable)
silently run `mcp_only` — native tools never apply without confinement.

## Retired names

`hybrid` and `full_unconfined` in existing configs read as `full`. Behavior is
unchanged for confined launches; previously unconfined `full_unconfined`
launches now run `mcp_only`.
