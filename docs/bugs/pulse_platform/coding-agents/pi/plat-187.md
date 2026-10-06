[← coding-agents / pi](index.md)

# PLAT-187 — pi-cli's model-visible `mcp()` proxy tool has no boundary against custom (non-MCP) tools; a model tried it on `get_human_input_request` and stalled a live schedule run

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | coding-agents |
| Area | pi |
| Summary | P1 — the `mcp()` proxy tool sits in every pi-cli-driven |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — live-verified against the real `mcpbridge` binary; see "Fix implemented" below |
| Last synchronized | `2026-08-24` |

- **Priority:** P1 — the `mcp()` proxy tool sits in every pi-cli-driven
  session's tool list by default, and nothing in the model-facing prompt or
  the tool itself prevented a model from reaching for it against a tool it
  can never resolve. This is the same class of gap PLAT-186 closed for
  `directTools` (a config we already set never actually taking full effect),
  just on the "which tools does this proxy even cover" boundary instead of
  the caching boundary.
- **Owner:** `mcpagent/agent/coding_agent_bridge_routing_prompt.go`
  (`bridgeRoutingExplicitInstructions`), `multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`
  (`normalizePiMCPConfig`), the third-party `pi-mcp-adapter` npm package
  (`disableProxyTool` setting, not ours to change).
- **Related:** [PLAT-186](plat-186.md) — the directTools-caching fix this
  builds on directly; that fix is what makes `disableProxyTool` actually take
  effect reliably (it hides `mcp()` only once directTools resolve from a
  valid cache).

## The live incident

`Workflow/trading`, schedule `833d1500`, run `schedule-manual--833d1500_1787547904876981000`
(2026-08-24, model `openrouter/stealth/ox-alpha`, code-execution mode). The
operator asked the workflow builder to apply 5 pending human-input decisions
before running the workflow. The agent's first two tool calls:

```
Tool Call: get_human_input_request (server: pi-cli, turn: 0)
{"args":"{\"workspace_path\": \"Workflow/trading\", \"input_id\": \"...\"}","tool":"get_human_input_request"}

Tool Result: get_human_input_request (duration: 46.458µs)
{"content":[{"type":"text","text":"Tool \"get_human_input_request\" not found. Use mcp({ search: \"...\" }) to search."}]...}
```

The model correctly formed a real `mcp({tool: "get_human_input_request", args: "..."})`
proxy call (not a malformed/corrupted call — confirmed by reading the raw
tool-call log directly) and got an instant (46µs — a local dictionary
lookup, not a network round-trip) rejection. It retried the identical
losing pattern once more, then the turn ended with two further tool-call
attempts carrying empty arguments. The session was recorded `success` in
`schedule-runs.json` (a separate, un-fixed classification gap — see
"Explicitly out of scope") despite never applying any of the 5 pending
decisions or reaching `run_full_workflow`.

## Root cause, in two parts

**Part 1 — `get_human_input_request` was never reachable through MCP at all,
by design, not by a bug.** It is a curl-only custom tool
(`$MCP_CUSTOM/get_human_input_request`), never declared on the `api-bridge`
MCP server (`MCP_TOOLS` only ever lists `execute_shell_command`,
`diff_patch_workspace_file`, `agent_browser`, `get_api_spec`, `read_skill`).
`mcp()`'s proxy search only knows about tools registered on that one MCP
server, so no phrasing of the call was ever going to succeed. This dual
transport split is deliberate (see "Why two tool-access mechanisms" below),
not a defect on its own.

**Part 2 — nothing told the model, or prevented it, from trying anyway.**
The routing prompt (`bridgeRoutingExplicitInstructions`) taught the `mcp()`
wrapper syntax in one bullet ("use `mcp({ search: ... })`... for the
documented bridge tools when direct `api_bridge_*` names are not visible")
immediately followed by a separate bullet for custom tools via curl — but
never said the wrapper *can't* reach a custom tool at all. A model
reasonably (if incorrectly) generalized "use `mcp()` when a direct name
isn't visible" to any tool it couldn't call directly, including a custom
one. Separately, `pi-mcp-adapter` keeps its generic `mcp` proxy tool
registered and visible in the model's tool list *by default*, even once
`directTools` fully covers the small native set — confirmed by reading its
own `index.ts`:

```typescript
// pi-mcp-adapter@2.27.0, index.ts:64-67
const shouldRegisterProxyTool =
  earlyConfig.settings?.disableProxyTool !== true
  || directSpecs.length === 0
  || missingConfiguredDirectToolServers.length > 0;
```

We never set `settings.disableProxyTool`, so the proxy stayed visible and
self-documented (its own schema declares `tool`/`args`/`search`/`describe`/
`connect`/`action` parameters) permanently — giving the model a plausible,
always-present, always-wrong path to any tool it couldn't otherwise name
directly.

## Why two tool-access mechanisms exist at all (background, not part of the fix)

This platform fronts five coding-agent CLIs (claude-code, codex-cli,
cursor-cli, pi-cli, gemini-cli). MCP tool-calling reliability varies across
them — already documented separately
(`project_video_studio_cursor_cli_mcp_gate.md`: Cursor CLI silently
auto-rejects every MCP tool call in headless/structured mode). Routing the
full, large, workflow-specific custom-tool catalog through real MCP would
inherit each provider's own MCP quirks. `execute_shell_command` + curl is
the one transport every provider reliably has, so the platform deliberately
splits: a small, fixed, high-value set of tools through real MCP
(`api-bridge`, via `pi-mcp-adapter` for pi-cli specifically), everything
else through curl. `execute_shell_command` itself has to be MCP-native
(or an equivalent per-provider native tool) because curl needs shell access
to exist first — that's the minimal bootstrap, not a redundant parallel
path.

Also confirmed, independent of this ticket: pi-cli's own core has **no
built-in MCP support at all**, by explicit upstream design —
`@earendil-works/pi-coding-agent`'s own README states *"**No MCP.** Build
CLI tools with READMEs..., or build an extension that adds MCP support."*
Everything MCP-related for pi-cli (`mcp()`, `directTools`,
`disableProxyTool`) comes entirely from the third-party `pi-mcp-adapter`
extension, not from pi itself.

## Fix implemented

1. **`multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`**
   (`normalizePiMCPConfig`): default `settings.disableProxyTool = true`
   whenever the generated `.pi/mcp.json` doesn't already specify a value.
   Confirmed safe against `pi-mcp-adapter`'s own `shouldRegisterProxyTool`
   logic above — a cold direct-tools cache (first launch, stale hash) still
   registers the proxy as a bootstrap path; this only ever hides it once
   `directTools` are fully resolved and warm, i.e. exactly when there is
   nothing legitimate left to reach through it.
2. **`mcpagent/agent/coding_agent_bridge_routing_prompt.go`**
   (`bridgeRoutingExplicitInstructions`): removed the `mcp()` wrapper-syntax
   teaching bullet entirely, rather than just narrowing its scope. With the
   proxy hidden by default, the model normally won't see the tool at all;
   when it is registered (cold-cache fallback), `pi-mcp-adapter` gives it
   its own complete self-documenting schema, so the prompt no longer needs
   to teach the calling convention either way. The custom-tools-via-curl
   bullet no longer needs to mention `mcp()` as something to avoid, since
   the prompt never introduces it.

### A real, provable P0 test — against the actual bridge binary

`TestPiCLIRealMcpProxyToolIsUnavailableOnWarmDirectToolsCache`
(`multi-llm-provider-go/pkg/adapters/picli/picli_mcp_bridge_real_test.go`)
builds the **real** `mcpbridge` binary fresh from the `mcpagent` sibling
checkout (not a hand-written Node MCP-protocol stub — the stdio<->HTTP
translation layer a production workflow launch actually uses), wires it to
a fake HTTP backend that only needs to satisfy `mcpbridge`'s real
`/tools/custom/{name}` contract (confirmed by reading
`cmd/mcpbridge/main.go` directly: bearer auth, `{"success","result","error"}`
envelope), and runs the same warm-cache two-launch pattern PLAT-186's test
established. On the second (cache-warm) launch, explicitly instructing the
model to call the `mcp` proxy tool got pi's own confirmation that no such
tool exists:

```
"The requested tool \"mcp\" is not available. The available tools are:
`read`, `bash`, `edit`, `write`, and `api_bridge_execute_shell_command`."
```

**Live-verified against real Gemini (`google/gemini-3.7-flash`), PASS in
~133s.** The assertion accepts either shape (call-attempted-and-rejected,
or tool-absent-entirely) since both are legitimate depending on how far
`directTools` resolution got for a given launch, but the tool-absent case
above is what `disableProxyTool` is actually expected to produce once warm.

Building the real binary caught a real bug in the test's own `MCP_TOOLS`
config assembly along the way: embedding it as a raw JSON array instead of
a JSON-string-encoded value crashed `pi-mcp-adapter`'s env-var
interpolation (`value.replace is not a function`, confirmed via a dead
tmux pane) — fixed by properly JSON-string-encoding it, matching
`mcpagent`'s own `bridgeEnv["MCP_TOOLS"] = string(toolsJSON)` convention.
Unrelated to the actual fix, but worth recording: this is exactly the kind
of gap a real-binary P0 test catches that a synthetic stub cannot.

### A live-environment credentials gap found along the way (not a code bug)

While live-verifying, repeated `context deadline exceeded` failures on the
*pre-existing, untouched* `TestPiCLIRealDirectToolsActivateOnRepeatedStableConfig`
baseline test (not just this ticket's new test) turned out to be a missing
credential, not flakiness: `multi-llm-provider-go/.env` has no
`GEMINI_API_KEY`/`GOOGLE_API_KEY`/`PI_API_KEY`, and the test harness's key
resolution (`firstNonEmptyPiTestEnv`) only checks those three names — it
never looks at `.env`'s `OPEN_ROUTER_API_KEY`. Sourcing
`agent_go/.env` instead (which has a working `GEMINI_API_KEY`) resolved it
immediately; both the baseline test and this ticket's new test then passed
cleanly. No code change made for this — recorded here so a future "the live
P0s are all timing out" investigation doesn't re-walk the same path.

## Explicitly out of scope for this ticket

- **The `schedule-runs.json` success/failure classification gap** — the
  live incident's stalled session was recorded `status: "success"` despite
  never applying any decisions or running the workflow. Real, but a
  separate scheduler-side concern from the tool-access boundary this ticket
  fixes.
- **Changing `pi-mcp-adapter`'s own proxy-registration logic** — third-party
  dependency; the fix works entirely within what we control
  (`settings.disableProxyTool` in the config we generate).
- **A live two-concurrent-sessions P0** for the readiness-marker
  concurrency guard — that's PLAT-186's own follow-up scope, unrelated to
  this ticket.

## Verification

- `go build ./...` and `go vet` clean in `mcpagent` and
  `multi-llm-provider-go`.
- `mcpagent`: new unit test
  (`TestAppendBridgeRoutingInstructionsNoLongerTeachesMcpWrapperSyntax`,
  fail-before/pass-after against the old mcp()-teaching bullet) plus the
  full `agent` package suite — only the same pre-existing, unrelated
  `TestAgentReviewsApproved` failure (confirmed identical on a clean
  checkout, per PLAT-186's own note).
- `multi-llm-provider-go`: new unit tests
  (`TestNormalizePiMCPConfigDefaultsDisableProxyTool`,
  `TestNormalizePiMCPConfigPreservesExplicitDisableProxyToolFalse`,
  `TestNormalizePiMCPConfigStillDefaultsDirectToolsAndLifecycleAlongsideProxySetting`)
  plus the full `pkg/adapters/picli` suite, all pass.
- `TestPiCLIRealMcpProxyToolIsUnavailableOnWarmDirectToolsCache` — **live,
  against real Gemini, PASS**, against the actual `mcpbridge` binary — the
  end-to-end proof that `disableProxyTool` genuinely removes the proxy tool
  from the model's available tools once `directTools` are warm, not just
  that the generated config JSON has the field set.
