# Native Coding-Agent Environment Policy

## Problem

Video Studio and a trading product run coding providers in
`runtime.agent_tools.mode: mcp_only` with `runtime.transport: auto`. The
guarded MCP bridge performs project work and product APIs such as
`show_video` through `execute_shell_command`.
`runtime.api_transport.mode: native_shell` remains implemented but disabled;
it is documented here as an experimental alternative, not the active route.

When `native_shell` is experimentally enabled, the route is intentionally not
supplied through AgentWorks' `execute_shell_command` bridge. The server instead
creates a small per-turn environment:

- `MCP_API_URL`, `MCP_API_TOKEN`, and `MCP_SESSION_ID`
- derived routes: `MCP_MCP`, `MCP_CUSTOM`, `MCP_VIRTUAL`
- `MCP_AUTH`
- selected `SECRET_<NAME>` values

The first native Bash test failed with `MCP_CUSTOM_set=no`. AgentWorks had created the route, but `mcpagent` copied only `SECRET_*` keys into the native coding-agent call options. The provider layer then correctly received the already-filtered, incomplete environment.

This is the same drift pattern previously found in duplicated product tool allowlists: policy was independently encoded at more than one layer.

## Why there are two gates

The gates have different responsibilities and should remain separate:

1. AgentWorks constructs values that are legitimate for a particular turn and session. It must not pass arbitrary process environment values to a model.
2. `mcpagent` is the provider boundary. It copies only approved values into a coding-agent request.
3. `multi-llm-provider-go` is the subprocess boundary. It overlays only approved values onto the child CLI environment.

The failure was not that there were multiple boundaries. It was that the definition of an *approved environment key* was duplicated.

## Current repair

Both `mcpagent` and `multi-llm-provider-go` now admit:

- any non-empty `SECRET_*` value;
- exactly these non-empty session API keys: `MCP_API_URL`, `MCP_API_TOKEN`, `MCP_SESSION_ID`, `MCP_MCP`, `MCP_CUSTOM`, `MCP_VIRTUAL`, and `MCP_AUTH`.

All other values, including `PATH` and unrelated server settings, remain
blocked. The active Video Studio profile keeps `execute_shell_command` enabled;
it is mutually exclusive only with an enabled `native_shell` transport.

## Browser-product constraint: provider-native interaction is not web-chat interaction

Native coding tools are not just file and shell operations. A provider can
also open its own terminal UI, including a structured question, chooser, or
approval flow. AgentWorks' browser chat cannot currently render or answer that
provider-owned UI. The Claude Code `AskUserQuestion`-style chooser observed in
Video Studio is the concrete failure: Claude waits in tmux for a selection,
while the browser has no terminal completion event and shows `Working` forever.

This is a functional reason that a browser product must remain `mcp_only` by
default. Native/hybrid mode may be enabled only after its provider-native
interactive surface is either bridged into the product UI or explicitly
disabled with a verified provider deny-list. A prompt instruction saying
"ask normally" is helpful but is not a safety mechanism; the transport must
make the unsupported interaction impossible.

## Proposal: one policy owner

Make `multi-llm-provider-go/llmtypes` the single owner of the key policy, because it owns the final coding-agent subprocess environment.

1. Export a narrow helper, for example:

   ```go
   func IsScopedCodingAgentEnvironmentKey(key string) bool
   ```

   It returns true for `SECRET_*`, `VAR_*`, and the MCP session routes — the
   three prefixes the shell whitelist already admits (see gap 1). Narrowing to
   `SECRET_*` plus the closed MCP set would drop workflow variables that the
   coding-agent prompt actively instructs agents to read.

2. Use the helper in `WithCodingAgentSecretEnvironment`, `CodingAgentSecretEnvironmentFromOptions`, and the subprocess environment merge.

3. Have `mcpagent` import and call the same helper when it copies `CodingRuntimeConfig.SecretEnvironment` into the agent. It must not keep a second local key list.

4. Keep AgentWorks responsible only for creating values for profiles that declare `runtime.api_transport.mode: native_shell`.

5. Add a cross-module contract test, run from AgentWorks, which proves that a native structured provider child sees `MCP_CUSTOM` and `MCP_AUTH`, sees a selected `SECRET_*`, and does not see an unrelated key.

## Gaps to close before implementing

### 1. `VAR_*` is missing from the key set, and it will break workflows

The admitted set above is `SECRET_*` plus the seven MCP session keys. The
surface that already exists is wider. `controller_execution.go` states it
directly: *"VAR_* passes through the shell whitelist (MCP_*, SECRET_*,
VAR_*)"*, and workflow variables are injected as `VAR_<NAME>` alongside
`VAR_WORKSPACE_PATH` and `VAR_GROUP_NAME`.

This is not an internal detail. `mcpagent/agent/prompt/builder.go` documents
`$VAR_<NAME>` to every coding agent as the way to read workflow config, and
instructs it to fail loudly on a missing one (`"${VAR_PAN:?missing}"`). A
policy that drops `VAR_*` therefore produces an agent that does exactly what
it was told: fails loudly, and blames the workflow rather than the transport.

Worse, it would fail *only under `native_shell`*. The same step keeps working
under `bridge_shell`, so the bug presents as transport-dependent and will be
diagnosed as a workflow problem. That is the original drift in a new place.

The policy must admit three prefixes, matching what is already promised:

```text
SECRET_*    selected secret values
VAR_*       workflow variables, workspace path, group name
MCP_*       the session API routes (or the closed set, if kept closed)
```

If the MCP set stays closed rather than a prefix, adding a future route means
editing the helper — acceptable, but only with §5 below.

### 2. The `native_shell` branch fails open

`server.go` logs `[API_TRANSPORT] native_shell requested but MCP bridge
environment is incomplete` and continues. The turn proceeds with no route, and
the agent has no way to distinguish that from a tool that does not exist. It
reports an outage and asks the user to reload — the behaviour this document
exists to prevent, reached by a different path. Specify fail-closed, or make
the condition visible to the agent so it can say something true.

### 3. Nothing requires the prompt to name the route

The acceptance criteria prove `$MCP_CUSTOM` works. They do not require that
anything tells the agent it exists. A route the prompt never names is
functionally absent: this same profile lost `set_workflow_secret` while the
tool was registered and correctly specced, purely because no instruction
mentioned it. Add a criterion that a profile declaring `native_shell`
documents the route in its system prompt.

### 4. The token's exposure surface

The non-goals cover prompts, logs, chat history, tool results, and UI events.
They do not cover the two places the value actually sits.

**Position taken.** The bearer is accepted as visible to the agent's own
process tree, and is contained by scope rather than by secrecy:

- `MCP_API_TOKEN` and `MCP_AUTH` are minted per session and are bound to that
  session's routes. A leaked value grants what the agent could already do.
- The environment is readable by any command the agent runs, including `env`.
  This is inherent to giving native Bash a route at all, and is the trade the
  transport makes in exchange for not exposing `execute_shell_command`.
- `-H "$MCP_AUTH"` places the header on curl's argv, visible in `ps` to other
  processes of the same user. On a single-tenant local install that is the same
  trust boundary; on shared infrastructure it is not, and such a deployment
  should prefer `--config` or a header file.

**Therefore two rules, neither of which is secrecy.** Transcripts and events
must redact anything matching the token value, since the agent will paste
command output back. And the token must remain session-scoped and short-lived,
so its blast radius stays equal to the session that already holds it.

What is NOT acceptable is echoing it deliberately: the agent must never print
`$MCP_AUTH` or `$MCP_API_TOKEN`, and must never write either into a file,
a URL it reports, or a chat message.

### 5. Say how to extend the set

The failure mode for a key outside the policy is silence — the child simply
does not see it, which is the `MCP_CUSTOM_set=no` bug again. Document the
extension path (edit the one helper, add a case to the contract test) so the
next addition is a five-minute change rather than an afternoon of debugging.

## Non-goals

- Do not re-enable `execute_shell_command` in hybrid profiles.
- Do not expose arbitrary server environment variables to a coding provider.
- Do not render MCP tokens or secret values into prompts, logs, saved chat history, tool results, or UI events.
- Do not change bridge-only (`mcp_only`) products; they continue to use their existing bridge policy.

## Acceptance criteria

- A hybrid/native-shell profile rejects `execute_shell_command` in its product allowlist.
- A native Bash command can call a `get_api_spec`-documented custom endpoint through `$MCP_CUSTOM` with `-H "$MCP_AUTH"`.
- The same native Bash command cannot obtain `PATH` or arbitrary parent environment values through the scoped injection mechanism.
- The allowed-key policy is implemented in one shared function and covered by a contract test at every boundary.
