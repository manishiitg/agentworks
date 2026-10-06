# Agent tool surface: one source of truth

**Status:** Partly implemented — stale header corrected 2026-08-17. Step 1 has
landed for the product surfaces: `videoproduct`, `financeproduct` and
`dominionproduct` all carry `tool_policy.mode: allowlist` with an `enabled:`
list. The AgentWorks (Workflow Builder) half — deleting gate 2 — has not been
done, and `GetToolsForWorkshopMode` still exists. Open question 1 is answered
below; 2 and 3 remain open.
**Date:** 2026-08-08
**Supersedes:** [product_tool_registration_and_visibility.md](product_tool_registration_and_visibility.md)
**Related:** [canonical_agent_definition_construction.md](../refactor/canonical_agent_definition_construction.md)

## 2026-09-11 implementation update

AgentWorks now declares host chat capability admission in
`agent_go/internal/agentworksproduct/product.yaml` under `chat_policy`.
`workflow_chat_policy.go` resolves mode, origin and read-only access before
registration. The MCP installer bug and tests are tracked in
[PLAT-307](../bugs/pulse_platform/sandbox/tools/plat-307.md).

Builder and Run are modes. Scheduled execution is an origin, not a third chat
mode. Pulse maintenance and child agents retain explicit improvement-role
boundaries; neither gets host MCP management. Product-specific tool allowlists
and typed reviewer/fixer executor checks remain authoritative. This does not
replace every workflow tool implementation with YAML: YAML owns host capability
admission, and registered tools continue to supply the catalog/API schemas.

Capabilities/config changes reconnect retained native sessions through the
existing durable transcript replay path. There is no second per-turn catalog
filter. The earlier status/header and historical gate descriptions below record
the original design and should not be treated as a current code inventory.

## 2026-09-18 retained-delivery compatibility update

The existing policy fingerprint must be compared before either warm SDK delivery or cold retained terminal delivery can bypass definition construction. Effective workflow access is freshly resolved first. A changed admission rebuilds the native session with the same durable chat identity, current tools/prompt and existing history handoff. The old key remains untouched until normal setup performs its comparison. Native resume handles from the old policy are not reused.

This is lifecycle admission, not a second tool-name filter or replacement authorization. Unchanged admission keeps SDK-owned warm delivery. Explicit new turns and auto-notifications use their existing lane and do not interrupt foreground work just to attempt retained delivery. See PLAT-262, PLAT-324 and PLAT-102 for local evidence and pending deployment/live acceptance.

Product-declared `workflow_suggestions` permits bounded suggestion intake in Run/read-only chat. Existing workflow decision records/UI are reused; owner review cannot grant unattended mutation authority. This does not admit general decision management or Builder mutation tools to readers. See PLAT-330.

## Problem

"What tools does this agent have?" is answered in five places today, in three
different styles. A tool must survive all of them, and any one of them can
silently drop it.

| Tool group | Decision lives in | Style |
|---|---|---|
| Product tools (`video.show-video`) | `product.yaml` `tools:` → `agent_profile_runtime.go:211` | opt-in, YAML |
| Platform pools (workspace, media/LLM, skills, MCP) | `product.yaml` `tool_policy.disabled` → `agent_profile_runtime.go:234`, four call sites (`server.go:4590,5002,5011,5029`) | opt-out, YAML |
| Workflow tools (`query_step`, `execute_step`, `run_full_workflow`, …) | hardcoded Go map `agentProfileWorkflowToolNames`, `agent_profile_workflow.go:21-29` | opt-in, Go |
| Secret tools (`list_secrets`, `set_user_secret`, …) | no policy at all — always registered, `server.go:5041` | always on |
| `createCustomTools` pool | no profile check, `server.go:~4891` | always on |

On top of that sits a second, independent list: `GetToolsForWorkshopMode()`
(`interactive_workshop_manager.go:1473`), applied per turn via `SetToolPolicy`
→ `mcpagent.Turn.ToolPolicy.AllowedTools`.

### The same bug has now happened three times

- **Video Studio secrets.** `set_user_secret` / `set_workflow_secret` were
  registered but absent from the workshop-mode list. The agent could not call
  them or discover them via `get_api_spec`, and fell back to a shell bridge
  Video Studio deliberately disables.
- **`list_llm_capabilities`.** Same shape, documented in the code at
  `interactive_workshop_manager.go:1494-1497`: *"a real, registered tool was
  rejected … Registration and this list live in different files, which is how
  they drifted."*
- **Builder background child (2026-08-08).** Failed at registration instead:
  the child called `prepareCustomTools(nil)` while the parent registered a
  richer workshop surface.

`toolset_invariant_test.go:12-33` exists only to hold two hand-maintained lists
in sync. It is a band-aid over the design, not a fix.

## The key asymmetry

A coding CLI caches its tool catalog **once, at launch**, via `get_api_spec`
(`chat_history_routes.go:143`). From that follows:

- **Removing** a tool the agent already knows about degrades gracefully — it
  calls, gets an error, adapts. `guidance.go:360` already does this well:
  *"error: kind %q is not available in mode %q … Tell the user they need to
  switch workshop mode."*
- **Adding** a tool after launch is invisible. The agent cannot ask for what it
  was never told exists, so it shells out instead.

**You can always take away. You can never add.**

This is why filtering the catalog is dangerous. `GetToolsForWorkshopMode` does
not only reject calls — it filters `buildToolIndex()` / `get_api_spec`, i.e. the
catalog read at launch. An exclusion applied there is indistinguishable from
never registering the tool: no discovery, no error, silent shell fallback. That
is exactly how the secrets bug became unrecoverable.

## Design

Three rules.

1. **One source of truth.** A single declarative list per agent role decides
   what is registered. Registration is complete before the CLI launches.
2. **Never filter the catalog.** `get_api_spec` and the tool index render
   everything registered. Discovery is always complete.
3. **Focus rules live in the prompt; code enforces authority only.**

The agent then always knows what exists. Nothing can silently vanish.

### What code may still enforce

| Rule type | Example | Where |
|---|---|---|
| Focus / workflow discipline | "don't optimize while still building" | Prompt — the agent decides |
| Authority | a reviewer must not mutate the workflow DB | Code — executor check |
| Irreversible / external | spend, delete, send outward | Code — executor check |

The test is whether this is something the agent should not be *trusted* to
decide, or merely something it might get wrong. Only the first belongs in Go.
Reviewer-cannot-mutate protects the reviewer/fixer separation and is exactly
what the canonical doc's Reader/Writer contract exists for. "Which workshop mode
am I in" is not that — and encoding it in Go duplicates a rule the system prompt
already states, which is the same one-decision-two-places failure this document
exists to remove.

### Relationship to the canonical refactor

[canonical_agent_definition_construction.md](../refactor/canonical_agent_definition_construction.md)
fixes drift *across construction paths* (parent vs child, scheduled vs
converted-chat) via one `AgentDefinition`. This document fixes drift *across
consumers within one path* (registered vs agent-visible vs API spec).

The canonical doc's Reader/Writer contract cannot express this axis — "this tool
exists for the HTTP UI but not for the agent" and "this tool is unavailable on
this provider" are not authority facts. The declarative list below is the
missing input to its canonical constructor, not a competing model.

## Fix: Video Studio

### Which Video Studio

One surface: the AgentWorks-embedded profile. `videoproduct.BuiltinAgentProfiles()`
is registered into the profile registry at `server.go:1473` and resolved per
request as `resolvedProfile`. Its agent is an `LLMAgentWrapper`, the platform
pools arrive by default, and `tool_policy` is what filters them — so this is
what the gate covers.

A standalone Video Studio application used to run beside it on its own port,
with an `agentsession` agent whose tool list was hand-built rather than drawn
from the platform pools. It never had this problem and `tool_policy` never
reached it. It was removed once the embedded profile was sufficient for live
projects, so no second surface remains to keep in sync.

Gate 2 is already inert on the embedded path — `workflow_phase_tools.go` took
the `setToolPolicy(nil)` branch for non-Builder phases even before its removal.
So Video Studio becomes a genuine one-gate system with no runtime work.

1. **Extend `ToolPolicy`** (`agent_go/pkg/agentprofiles/types.go:88-90`):

   ```go
   type ToolPolicy struct {
       Mode     string   `yaml:"mode,omitempty"`     // "allowlist" | "" (legacy deny)
       Enabled  []string `yaml:"enabled,omitempty"`
       Disabled []string `yaml:"disabled,omitempty"` // retained for other profiles
   }
   ```

2. **Add one registrar gate.** Follow the existing working pattern —
   `agentProfileWorkflowRegistrar` (`agent_profile_workflow.go:30-77`) already
   wraps `definitionToolRegistrar` and gates every registration by name:

   ```go
   type productToolGate struct {
       target   definitionToolRegistrar
       allowed  map[string]struct{}
       filtered []string
   }
   ```

   This works regardless of which pool a tool came from, because every path
   funnels through `RegisterCustomTool`.

3. **Pass the gate instead of `llmAgent`** into every `register*Tools(...)`
   call. All of them already accept the `definitionToolRegistrar` interface —
   `registerSecretManagementTools` (`secrets_tools.go:31`),
   `registerMultiAgentLLMTools` (`multiagent_llm_tools.go:1163`),
   `registerAgentProfileTools` (`agent_profile_runtime.go:211`) — so this is
   wiring, not refactoring.

4. **Retire the redundant mechanisms**: `tool_policy.disabled` for this profile,
   the `agentProfileWorkflowToolNames` map, and the always-on secrets
   special-case. They collapse into the one `enabled:` list.

5. **Log every filtered tool** at session start. An allowlist fails closed, so a
   missing capability must be diagnosable from logs rather than from confused
   agent behavior.

Result in `product.yaml`:

```yaml
tool_policy:
  mode: allowlist
  enabled:
    - video.show-video
    - list_secrets
    - set_workflow_secret
    - delete_workflow_secret
    - query_step
    - execute_step
    - run_full_workflow
```

### Building the list safely

Do not guess it. Run a real Video Studio session, capture the effective set from
the existing log lines (`[CUSTOM TOOLS] Registered custom tool: %s`,
`[WORKSPACE TOOLS] Registering %d workspace tools`), seed `enabled:` from that,
confirm behavior is unchanged, then trim. Behavior-preserving first, minimal
second.

## Fix: AgentWorks (Workflow Builder)

Higher risk — it touches every workflow — so it comes second.

**Delete gate 2.** Workshop-mode narrowing goes away entirely. Mode discipline
moves to the system prompt, which already describes the modes.

`SetToolPolicy` has two callers, and only one of them is gate 2. They must be
told apart before deleting anything:

| Caller | Supplies | Axis | Action |
|---|---|---|---|
| `workflow_phase_tools.go` mode if/else | `GetToolsForWorkshopMode(mode)` | **focus** — BUILD vs DEBUG | delete |
| `interactive_workshop_manager.go` tool-agent registration | `goalAdvisorReadOnly…` / `goalAdvisorFinalizerApproved…` / `pulseFixerStage…`, paired with `writePaths` | **authority** — Reader vs Writer | keep |

The second is the reviewer/fixer separation the canonical doc's acceptance
criterion 7 protects; deleting it would let a reviewer mutate the workflow DB.
It should eventually move to construction-time admission too, but that is the
canonical refactor's job, not this one.

So the deletion is: the mode if/else and its `setToolPolicy`/`applyAllowList`
parameters, the `server.go` wiring, `GetToolsForWorkshopMode()` itself, the
now-dead `LLMAgentWrapper.SetToolPolicy` and its `toolPolicy` field, and the
exception list in `toolset_invariant_test.go`. `BaseAgent.SetToolPolicy` stays.

### Why not keep it as call-time rejection

Because the allow-list drives two unrelated surfaces. `mcpagent`'s
`agent/turn_session.go:186-192`:

```go
allowed := policy.allowedMap()
codeexec.SetSessionToolAllowList(s.agent.sessionID, allowed)
```

- **Surface A** — the tool list offered to the model this turn
  (`agent/agent.go:3622`). The intended use.
- **Surface B** — the **session-wide** code-execution HTTP registry, which
  execution agents and sub-agents call from generated `main.py`.

Surface B is keyed by session, not by actor. Narrowing the Builder's tools to
enforce its workshop mode therefore also narrows the HTTP bridge for every other
actor in that session. That is why four unrelated names are pinned in the
always-allowed block at `interactive_workshop_manager.go:1499-1502` — not
because modes should permit them, but because omitting them broke execution
agents.

Two consequences make this unfixable in place:

1. **The rule is inexpressible.** "The Builder LLM should not call sub-agents in
   this mode" cannot be stated, because stating it also disables sub-agent calls
   for execution agents.
2. **There is a hidden maintenance obligation.** Any tool any execution agent
   might ever call over HTTP must also appear in the Builder's mode list, or it
   silently breaks — a coupling invisible from either file, and on top of the
   registration drift that caused the three incidents.

### Honest tradeoff

Mode discipline becomes advisory: the LLM sees every registered tool and follows
the prompt. A model may occasionally act out of phase. That is a visible,
recoverable error — versus the current failure, where a tool silently disappears
and the agent quietly shells out instead. Builder prompts should state the mode
boundaries explicitly, since they become the only place the rule lives.

## Sequencing

1. Video Studio gate + `enabled:` list (isolated, gate 2 inert, small blast radius)
2. AgentWorks: delete gate 2; move mode boundaries into the Builder prompts
3. Keep executor-level authority checks (Reader/Writer, irreversible actions)
4. Delete superseded mechanisms and the invariant band-aid

Steps 1 and 2 independently close a shipped bug class and can land separately.

## Verification

- **Video Studio:** run a session; assert the filtered-tool log is empty of
  anything expected; confirm secret tools and workflow tools appear in
  `get_api_spec`; store and read back a workflow secret through the real bridge.
- **AgentWorks:** in each workshop mode, assert every registered tool appears in
  `get_api_spec`; assert a mode-blocked call returns the actionable error rather
  than being absent.
- **Invariant test (replaces the exception list):** for every profile, the set
  in `get_api_spec` equals the set actually registered. No hand-maintained
  membership list.

## Open questions

1. ~~Skills register through `AttachSkill`, a separate method. In scope for the
   gate, or governed separately?~~ **Answered 2026-08-17 by
   [PLAT-125](../bugs/pulse_platform/goals/steps/tools/plat-125.md): governed separately, and by
   the same tool set — selected at materialisation time rather than by a
   registration gate.**

   The reference bundle reaches the agent through `AttachSkill`, so it sat
   outside every gate this document describes, and drifted in the direction
   this document does not cover. Its rules address a **registered tool the
   agent cannot see**; PLAT-125 is the inverse — an **unregistered tool the
   agent is told to call**. A step agent holding eight tools was handed the
   workshop chat's 41-doc bundle, followed `llm-provider-config`, called
   `list_published_llms`, and on failing invented provider names.

   Two consequences for this document's rules, worth keeping in view before
   anyone implements the rest of it:

   - Rule 2, *never filter the catalog*, does not transfer. Making the prose
     honest by registering the tools it names would give a step agent authority
     over provider auth. The rule holds within one agent role, which is what
     Video Studio's 19-tool allowlist actually is; it is not a licence to widen
     a role.
   - Rule 3, *mode discipline moves to the system prompt*, assumes prose is the
     safe place because tools are the dangerous one. Prose drifts the same way
     and fails more quietly — there is no error when guidance names a tool that
     does not exist, only an agent quietly improvising.

   `kindMeta.Tools` in `cmd/server/guidance` now names the tools each reference
   doc explains, and step execution selects docs by the tools its session
   actually registered. Discovery is never removed for a tool the agent holds,
   so the asymmetry this document is built on ("you can always take away, you
   can never add") is respected.
2. Provider-native tools (`agent_tools: mode: hybrid`) never reach this
   registrar — the CLI supplies its own read/search/skill/todo/subagent tools
   (never native writes since 2026-09-24; see [native_agent_tools.md](native_agent_tools.md)). They stay governed by
   `approvals`. Confirm no product expects the list to cover them.
3. MCP bridge tools carry their own `MCPToolPolicy`
   (`productdeps/dependencies.go:89`). Verify whether that is a fourth
   registration path needing the same gate.


## Product surface integration checks

Run `go test ./cmd/server -run ProductSurface` from `agent_go`.
`product_surface_e2e_test.go` exercises the production workflow tool and skill
registration for AgentWorks builder and run modes, and Crew profile tool
registration. It uses a local workspace fixture and does not call an LLM or Slack.

AgentWorks declares its default workflow-phase tools in `chat.<mode>.tools` in
`product.yaml`; Crew declares tools through profile features and bindings.
The checks compare registration against these declarations independently of
runtime gate mutations. They also compare core skills and registered system
prompt templates against the manifest sources, and require both products to
declare the shared prompt-section registry in `instruction_sections`.
Negative cases verify rejection of undeclared tools, skills, prompt sections,
and extra base prompt instructions.

This contract covers default product-owned surfaces. User-selected skills, MCP
servers, provider-native tools, and dynamic request-specific instructions have
separate policies; this test does not claim to scan arbitrary Go instruction text.
A new shared prompt section must be declared in each affected product manifest.


## Session identity at tool execution

The query boundary sets `LLMAgentConfig.ToolExecutionContext` once from the
validated request and its session. `RegisterCustomTool` and
`RegisterCustomToolWithTimeout` wrap every direct tool callback, including
replacement registrations. This is shared by AgentWorks and Crew and applies
to browser, inline, and CLI bridge calls. New tools inherit the binding;
they should read claims from their execution context instead of reconstructing
a user or requiring browser JWT claims. They still call the existing resource
and action authorization handlers.

The shared binding preserves cancellation, rejects conflicting callers and
changed session ownership, checks current account/target access, and revalidates
bot routes without turning the bot's resource owner into account authority.
Detached query contexts use `context.WithoutCancel` to retain authenticated
request values. The Slack-only identity adapter was removed.

CI runs `TestToolExecutionContext.*` and
`TestEveryToolRegistrationBindsExecutionContext` as regression contracts for
this boundary, in addition to the product surface checks above.
