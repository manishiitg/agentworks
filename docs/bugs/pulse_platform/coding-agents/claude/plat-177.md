[← coding-agents / claude](index.md)

# PLAT-177 — resumed Claude Code session claimed it lost tool access it demonstrably had; a real stale-scope retry gap found and fixed, exact incident mechanism still unconfirmed

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | coding-agents |
| Area | claude |
| Summary | P2 — disruptive when it happens (the agent stops using tools |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — one confirmed, real gap closed; not proven to be this exact incident's mechanism, live reverify pending |
| Last synchronized | `2026-08-22` |

- **Priority:** P2 — disruptive when it happens (the agent stops using tools
  it has and asks the user to re-grant access that was never withdrawn).
- **Owner:** native session resume/relaunch path
  (`cmd/server/server.go`'s `seedCodingAgentRuntimeFromRestoredConversation`
  and the FIX-B materialize guard in `handleQuery`), virtual-tool scope
  registry (`mcpagent/agent/codeexec/registry.go`).
- **Related:** none — this is the first ticket investigating the native
  resume path specifically.

## Symptom

Mid-conversation on workflow "substack" (session
`b5e39872-4e4e-4645-8059-6d6e7a1231db`, Claude Code CLI provider), the agent
told the user it no longer had access to plan-editing tools
(`update_message_sequence_step`, `delete_schedule`, `add_todo_task_route`,
`create_human_input_request`, etc.) — tools it had used successfully earlier
in the same conversation — and described only having "general-purpose tools"
available. The user asked whether this correlates with a session resume;
this ticket confirms it does, but does not confirm *why* the resume causes
the agent to make this claim.

## What's confirmed

- The session underwent a native-runtime resume + relaunch at `13:47:47-50`
  on 2026-08-22 (`[claude-code] Restored native runtime from chat history
  for session b5e39872-...`), followed by the FIX-B "Materialize guard"
  block inside `handleQuery` (`cmd/server/server.go` ~6030-6107) relaunching
  the CLI process via `mcpagent.StartAgentTransportSession` →
  `executeLLMForCodingAgentTransportLaunch`
  (`mcpagent/agent/llm_generation.go:1059-1073`).
- The agent's tool-access complaint follows this relaunch. Before it (e.g.
  `09:19:00`, `09:19:21`), the same tools worked: two successful
  `update_message_sequence_step` calls logged as direct
  `[SESSION_ROUTE_DEBUG] ... url=/s/b5e39872.../tools/custom/update_message_sequence_step`
  hits.

## Three hypotheses investigated and ruled out

**1. Native MCP tool list going stale across `--resume`.** Ruled out at the
design level: this platform's CLI-bridge providers (claude-code, codex,
cursor, pi) never register these ~107 tools as a native MCP tool list in the
first place. `buildBridgeMCPConfig`
(`multi-llm-provider-go/coding_agents_bridge.go:115-193`) only ever emits a
fixed 4-tool native set (`execute_shell_command`,
`diff_patch_workspace_file`, `agent_browser`, `get_api_spec`); every other
tool is discovered via `get_api_spec(tool_name=...)` and invoked as an HTTP
call issued through `execute_shell_command`. There is no native list to go
stale.

**2. The `launchOnly` relaunch path omits the system prompt.** Ruled out by
direct read of the actual live prompt log for the exact relaunch —
`logs/agent_prompts/b5e39872-4e4e-4645-8059-6d6e7a1231db/263_13-47-50_chat-agent_code-exec_claude-code_claude-sonnet-5.md`
(99077 bytes). The system prompt is fully present (complete "Workflow
Builder Agent" instructions, plan steps, phase-detection logic) and its
`<available_tools>` block (lines 186-361) explicitly lists every tool the
agent claimed it lacked — `create_human_input_request` (line 208),
`add_todo_task_route` (line 253), `delete_schedule` (line 263),
`update_message_sequence_step` (line 309) — plus prose (line 168) teaching
the exact `get_api_spec(tool_name=...)` + HTTP invocation convention. This
also directly contradicts an initial subagent investigation report that
claimed the `launchOnly` branch sends no system prompt; re-reading
`executeLLMForCodingAgentTransportLaunch`
(`mcpagent/agent/llm_generation.go:1059-1073`) shows it explicitly appends
`WithCodingProviderLaunchSystemPrompt` before calling
`executeLLMInner(..., launchOnly=true)`, with its own comment explaining
exactly why ("Without this, launch-only sends nil messages... adapter's
splitSystemPrompt returns empty..."). The subagent had examined
`executeLLMInner` in isolation without tracing back to this caller.

**3. Resume silently drops/overrides the workflow's `use_code_execution_mode`
manifest setting, changing the tool-invocation convention mid-conversation.**
This looked promising: the substack workflow's manifest
(`workspace-docs/Workflow/substack/workflow.json`) sets
`"use_code_execution_mode": false`, and the working 09:19 call hit a direct
named route rather than routing through `execute_shell_command`, while the
13:47:50 prompt log is explicitly tagged `_code-exec` and teaches the
shell+`get_api_spec` convention. Ruled out on closer trace: `useCodeExecutionMode`
is force-set `true` for every CLI-bridge provider (claude-code, codex,
cursor, pi) at *every* agent-construction site, unconditionally, regardless
of the manifest — both at original session creation and at resume alike,
with explicit comments stating this is intentional:
  - `mcpagent/agent/agent.go:825-828,1746-1755` (`isCodingCLIBridgeProvider`) —
    "CLI providers always need code execution mode (tools accessed via HTTP
    bridge)."
  - `cmd/server/server.go:4446-4451` — the generic chat-agent construction
    path (the one actually used here, matching "chat-agent" in the log
    filename) — "Multi-agent chat / generic agent always runs in
    code-execution mode regardless of provider... Tool-search and
    simple-agent paths have been retired."
  - `cmd/server/server.go:3685-3687` — same for the workflow orchestrator
    path.
  - `pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go:741-758` —
    checks `common.IsCLIProvider` *before* honoring any per-step override.
  - `controller.go:1351-1369` — "every agent now runs in code-execution mode
    for HTTP-bridge tool routing."

  The resume path (`server.go:5962/5972/6059/7507`) operates on
  `underlyingAgent := llmAgent.GetUnderlyingAgent()`, the *same* agent object
  built earlier in the identical `handleQuery` call by the chat-agent
  construction path above — it does not re-read or drop the manifest
  setting on resume, because that setting was never read for CLI providers
  at any point, including the original 09:19 launch. `useCodeExecutionMode`
  was almost certainly already `true` at 09:19 too (only one prompt-log file
  exists for this session, tagged `_code-exec`, and the filename suffix is
  data-driven off the live flag — `mcpagent/agent/conversation.go:2255-2257`).
  The most likely explanation for the 09:19 direct route is that Claude Code
  CLI's own built-in Bash tool issued the HTTP call directly (`curl`), which
  is the code-execution convention working as intended — it just doesn't
  produce a preceding `execute_shell_command`-dispatch log line the way
  agent_go's own instrumented custom tool does, so it looked superficially
  different from the 13:47:50 traffic without actually being a different
  mode.

## The actual error, and a fourth hypothesis (confirmed real, fix shipped)

The three hypotheses above were investigated before the incident's literal
error text was available. Once obtained, `get_api_spec(tool_name="delete_schedule")`
had actually failed with:

```
tools_unavailable: unknown=["delete_schedule"]: these names are not registered by
any currently connected server. Closest registered name(s): [delete_schedule].
Registered tools for this session: [add_group add_human_input_step ... ~90 workshop
tool names ... validate_plan_change validate_report_html]
```

— a real infrastructure error, not a model misstatement: `get_api_spec`'s own
handler genuinely could not resolve `delete_schedule` at that moment, even
though `server_debug.log` independently confirms
`[Registered session-scoped custom tool] tool=delete_schedule` fired for this
same session at `13:47:47`. This narrowed the search to a fourth, more
specific hypothesis: **two independent tool registries can desync — the
custom-tool HTTP registry cmd/server logs into, versus whatever registry
`get_api_spec`'s `virtual_tool_handler` actually consults.**

**The literal form of that hypothesis is refuted.** `mcpagent/agent/tool_registry.go`'s
`canonicalRegistry()` is a per-`Agent`-object cache, but `cmd/server/server.go:4642`
constructs a **brand-new** `llmAgent` (and underlying `mcpagent.Agent`) on
*every* `handleQuery` call — fresh, resume, or otherwise; there is no
cross-turn Agent reuse to go stale. The ~90-107 custom tools are registered
unconditionally on every such call (`server.go:5016-5085` →
`llmAgent.RegisterCustomTool`), with no "already registered, skip" guard —
`registerDirectTool` (`mcpagent/agent/agent.go:3452-3515`) is explicitly
idempotent by name. This registration block runs, synchronously, *before*
the resume-seed/FIX-B relaunch block later in the same `handleQuery` call,
on the same `underlyingAgent` object — nothing reassigns `llmAgent` in
between. The `[Registered session-scoped custom tool] tool=delete_schedule`
log line itself (`mcpagent/agent/codeexec/registry.go:672`, fired from
`agent/agent.go:3613-3617`) only runs *after* the canonical-registry write
at `agent.go:3505-3515` already succeeded — so that log line firing is
itself proof the canonical registry had `delete_schedule` at that moment,
on whatever Agent object logged it.

**A different, real, currently-broken mechanism was found nearby and
confirmed by direct code read (not the exact hypothesis, but a genuine bug
in the same neighborhood).** `get_api_spec` is a *virtual* tool, routed
through `codeexec.CallVirtualToolWithSession`
(`mcpagent/agent/codeexec/registry.go:522-586`), keyed by a
`sessionID:vt:traceID` scope string baked into the CLI bridge subprocess's
env at launch and regenerated fresh on every relaunch
(`agent/coding_agents_bridge.go:279-281`). Old scope entries in
`sessionVirtualTools` are never pruned — only superseded by
`latestVirtualScopeBySession`, whose own comment names exactly this
incident's shape: *"This lets older restored bridge processes recover when
they still carry a stale session:vt:\<trace\> scope from before workspace
tools were registered."* The recovery path exists
(`CallVirtualToolWithSession` retries against the latest scope when
`shouldRetryVirtualToolWithLatestScope(err)` is true) — but that function
only matched the legacy string `"Available servers/categories: []"`, a
phrasing `get_api_spec`'s current `unavailableToolsError`
(`agent/code_execution_tools.go:148-217`) no longer produces anywhere.
Confirmed by direct grep: that exact string appears nowhere in
`code_execution_tools.go`. **So if a restored bridge process ever does send
a stale scope ID, the one safety net built to recover from it was silently
dead** — the model would see the raw `tools_unavailable:` error with no
retry, which reads exactly like this incident.

**What is and isn't proven:** there is no direct evidence (a captured
`MCP_VIRTUAL_SCOPE_ID` value from the failing bridge process) that this
specific incident hit a stale-scope condition rather than some other path
into `unavailableToolsError`'s healthy-servers/name-genuinely-unregistered
branch. This is the one concrete, currently-broken recovery path found that
matches the error shape and is already documented in the code as an
anticipated resume-adjacent failure mode — not a certainty that it explains
this exact incident.

## Fix implemented

`shouldRetryVirtualToolWithLatestScope`
(`mcpagent/agent/codeexec/registry.go`) now also matches the current
format's unique phrase, `"these names are not registered by any currently
connected server"` (present only in `unavailableToolsError`'s
healthy-servers-name-is-unknown branch — the structural analog of the old
empty-discovery case). Real outage/permission-denial branches of the same
error function deliberately do not contain either phrase and continue to
skip the retry, matching the existing test
`TestCallVirtualToolWithSessionKeepsScopedNonEmptyDiscoveryErrors`'s intent
that a legitimate, specific error must not be papered over by silently
trying a different scope.

## Not implemented

- The three originally-investigated hypotheses remain ruled out as stated
  below — none of them were the cause.
- Reading the raw streamed model turn (`stream_turn-*.txt` in
  `logs/agent_prompts/b5e39872-4e4e-4645-8059-6d6e7a1231db/`) to find direct
  evidence of the actual scope ID sent at the failing call — would be needed
  to move from "plausible, code-confirmed mechanism" to "confirmed root
  cause of this exact incident," not done in this pass.
- Not re-litigating whether CLI providers should honor
  `use_code_execution_mode: false` from the manifest — unrelated to the
  fix above and already settled as intentional in the three hypotheses
  ruled out above.

## Verification

- The fix (`mcpagent/agent/codeexec/registry.go`,
  `shouldRetryVirtualToolWithLatestScope`) is covered by two new tests in
  `registry_test.go`: one reproducing the incident's actual error text
  verbatim and confirming the retry now engages and returns the latest
  scope's result; one confirming a real-outage error in the current format
  (which explicitly states retrying elsewhere won't help) still does not
  retry, mirroring the existing
  `TestCallVirtualToolWithSessionKeepsScopedNonEmptyDiscoveryErrors`'s
  intent. `go build ./...` and `go test ./agent/...` both clean; the one
  pre-existing failure (`TestAgentReviewsApproved`) was confirmed present
  on a clean `origin/main` checkout before this change, unrelated to
  virtual-tool scoping.
- Live reverify pending: no confirmation yet that this fix actually closes
  the user-visible symptom on a real resumed session, since the exact
  incident mechanism (a stale scope ID reaching this retry path) was never
  directly proven for this specific occurrence — see "What is and isn't
  proven" above.
- The three originally-ruled-out hypotheses were independently re-verified
  by direct evidence, not implementation: the live prompt log for the exact
  relaunch, and direct reads of `agent.go`/`server.go`'s agent-construction
  and code-execution-mode call sites, all cited inline above.
