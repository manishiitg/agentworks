# Progressive prompt and tool discovery

Status: implemented, with partial live CLI qualification, 2026-10-01. The
investigation below records the pre-change baseline: builder `7da5366df`,
mcpagent `42ad02e`, and provider `1fef821`, inspected in owned worktrees.

The problem is instruction ownership and discovery, not just verbosity. We
already load skill bodies on demand, but independently author feature summaries,
product instructions, transport instructions, and a complete tool-name index.
They overlap and sometimes disagree. Shortening each copy independently leaves
the same maintenance problem.

## Implementation and rollout

AgentWorks owns product identity, mode/access constraints, live grants, and
feature procedures. mcpagent owns runtime mechanics, registry filtering,
discovery, schemas, and provider tool-mode facts. Caller base instructions and
`AddInstructions` supplements are preserved. mcpagent generates one reserved
`<runtime_tools>` section at the outbound boundary; arbitrary caller prose is
not rewritten. An explicit bridge-routing override still replaces or suppresses
that section.

- AgentWorks opts Code, Crew Builder/Run, workflow Builder/Run and step wrappers
  into `ToolRuntimeConfig.Discovery`. In code-execution mode the full tool-name
  catalog becomes a short pointer to the live routing contract. Other mcpagent
  consumers retain the legacy inventory by default. API native tool-calling
  still exposes its existing schemas; this change does not hide that cost.
- Intrinsic `search_tools` searches the Agent's current canonical registry by
  lexical name/description/group/server matching. Results are at most 20 short
  descriptions per page, with exact names, source, pagination and an explicit
  `no_matches` result. Authorized group/server hints let callers enumerate when
  synonyms miss. There is no embedding service or additional model call.
  `get_api_spec` supplies the selected schema and existing HTTP route.
- Both discovery and schema lookup check current context permissions and the
  HTTP registry's session allowlist, before returning data or cached schemas.
  HTTP callbacks lack the in-process turn context, so checking only that context
  would reveal blocked tools. Unknown-name suggestions use the same filtering.
  Registry isolation and existing execution authorization remain authoritative.
- Product feature summaries become compact always-on constraints. Full legacy
  `ResolvedFeature.PromptExtension` text remains client-visible catalog metadata,
  not model instructions. Code peer ownership, owner-only personal MCP setup,
  DM-only bots, Google grants, secret and database rules remain explicit.
  Connection, calling, scheduling, bot and configuration procedures live in
  attached skills. No grants or backend access checks are widened.
- The shared Code/Crew feature bundle uses rendered SKILL.md frontmatter as its
  canonical discovery description. Explicit binding overrides remain supported.
  Current feature options render session-local skill variants without modifying
  global builtins: Code peers, outbound-only callers, private Google accounts and
  DM-only bots keep their procedures and restrictions. This happens before the
  profile/skill fingerprint and attachment on each request.
- Native skill discovery remains the primary CLI path. Agy receives an explicit
  names/descriptions fallback and its provider routing block because its adapter
  does not project skills. Skill bodies remain on demand through `read_skill`.
- The generic HTTP tutorial is consolidated in mcpagent. AgentWorks retains
  workflow-specific `VAR_*`, `SECRET_*` and step output/input environment rules.
  The deep bridge reference follows hybrid reads rather than disabling them.
  The personal MCP skill now reflects the existing next-message registration
  boundary; no OAuth/backend connection timing was changed.

No provider repository changes, application restart, or deployment are included.
Existing native conversations use the current instructions/tools on subsequent
turns once the updated application is running.

## Skill-first prompt migration (2026-10-01)

The second migration keeps the always-loaded contract small: role, access/mode
limits, live roots and grants, essential secret safety, discovery and core memory
rules. Skills own operating procedures, examples, templates and troubleshooting.
The skill index retains names and action triggers; prompts do not become bare
links. Argument shapes remain in current tool schemas.

- AgentWorks attaches a managed `project-memory` procedure skill to project chats
  and workflow chats, including delegated project agents. It holds no project
  facts: `MEMORY.md` remains the only store. Writers retain proactive verified
  memory and the existing dated format/correction/forgetting procedure. Run and
  read-only accounts receive retrieval-only content. The mode boundary, one-store
  rule, secret prohibition and explicit skill-authoring authorization stay upfront.
- Crew Builder moves conversation-history lookup, coding/repository layout and
  memory-versus-procedure examples into `crew-builder`. Its frontmatter is the
  canonical trigger description. Identity, granted roots, private-chat boundaries,
  managed database restrictions and workflow-authoring restrictions stay upfront.
- Workflow Builder/Run point to the mode-filtered `workflow-chat` reference in
  `builder-reference`. That reference routes to the existing focused procedures
  for plan edits, branching, human input, runtime grounding, route selection,
  failure inspection and reports. Run does not receive authoring references.
  Sequential defaults, overlap approval, auto-notification behavior, backend-owned
  Slack credentials and execution-versus-authoring limits remain upfront.
- Native API and CLI workflow chats both load procedures from skills. API tools
  retain supplied native schemas; CLI execution retains runtime discovery. The
  linked `cd project` exception is explicitly limited to the private CLI; bridge
  shell examples use absolute paths. Every nested operations reference is checked
  against the mode's actually attached supporting files.
- Dynamic secret names stay in context once, without per-name shell/Python
  examples or values. Provider/model/auth status now comes from the admitted
  `list_llm_capabilities` tool rather than a duplicate upfront snapshot.
- mcpagent attaches `runtime-http-tools` only for progressive code-execution
  sessions. The prompt keeps declared native tools, intrinsic readers, discovery
  and permission rules; the skill owns curl/auth/JSON/error mechanics. Legacy
  consumers keep inline mechanics from the same constant. Removing the managed
  skill restores inline mechanics instead of leaving a dangling pointer. Agy
  retains its names/descriptions fallback; bodies remain on demand.

Same-fixture server composition was measured in owned baseline worktrees at
builder `6b70472f8` / mcpagent `d13b6d3` and the new implementation. These counts
include production server composition, not mcpagent routing, native skill
metadata, tool schemas, history or provider wrappers. They are bytes, not tokens.

| Server composition fixture | Before | After |
| --- | ---: | ---: |
| Code identity + features + workspace + memory | 7,923 | 6,223 |
| Crew Builder identity + features + workspace + memory | 10,466 | 6,324 |
| Crew Run identity + workspace + memory | 2,822 | 3,337 |
| Workflow Builder, CLI references, interactive, optional context | 20,065 | 11,985 |
| Workflow Run, CLI references, interactive, optional context | 20,130 | 12,386 |

Crew Run already had a short memory reminder. Its increase adds common memory
constraints and the retrieval-skill trigger; it is not a reduction claim. The
workflow fixture keeps the same optional capability metadata, grants, browser,
notification and secret input on both sides. Current capability discovery can
remove more static metadata in production, but that saving is not included here.
The workflow server ceiling is now 15KB for this controlled fixture.

Live `TestLocalCLIDiscoverySkillsAndResume` passed on **Claude and Codex** with
both a feature-skill-only validation value and a transport-skill-only marker.
A fresh resumed Agent uses changed tool names and values in both skills. The
receipt requires an actual HTTP execution. Neither value is in initial system
text. The test harness now supplies the production short bridge variables and
session-prefixed routes; its prior omissions made Codex fail despite reading both
skills. Pi stopped before model I/O because local Google credentials were absent;
its previous-migration success is not a qualification of this change. Cursor,
Muse and Agy remain unqualified for this second migration. Full business flows,
OAuth onboarding, total turn cost and latency are still evidence gaps.

### Reproduce a readable local system prompt

Use owned worktrees and a Go workspace pointing to the owned dependencies.
Run the Code-only capture in AgentWorks' `agent_go`:

```sh
PROMPT_SNAPSHOT_DIR=/absolute/output/directory go test ./cmd/server -run '^TestCodePreparedSystemPrompt$' -v -count=1
```

This test sends a request through the real `handleQuery`: it resolves the
project binding, profile/access, provider and native-tool mode, registers the
actual permitted tools, attaches current skills, and calls `FinalizeDefinition`.
A narrow internal test seam captures the finalized agent and stops before a turn
starts. mcpagent's read-only `ReadAgentSystemPrompt` facade invokes the same
outbound composer used by model requests. No prompt logic is copied into the
fixture, and no CLI process or model call is started.

`code.system.md` is the complete outgoing system text for the mocked user/project;
`code.skills.json` holds attached metadata and on-demand bodies; `summary.json`
records byte/character counts and registered tools. Only external state (workspace
API, user, saved project, MCP configuration and credentials) is mocked. Paths and
local time come from the actual handler. This is not a snapshot of a specific
live user's configuration; provider-owned prompts, native tool schemas, history
and native skill indexes can add context outside this text.

A captured Claude Code fixture measured 8,295 UTF-8 bytes / 8,293 characters,
13 attached skills (including a mock project reviewer) and 47 registered tools.
This corrected capture is not comparable to the earlier hand-assembled sample
as a reduction measurement: it includes the real handler's live context.

The earlier two-stage export was removed: it combined base profile data with a
fresh runtime and missed Code's resolved `hybrid` native-tool mode. The handler
capture asserts the enabled native-read policy. Discovery sessions also remove
stale `<available_tools>` catalogs: the runtime block already owns that guidance.

Full server checks reproduced six unrelated failures on the unchanged baseline:
sales-Crew catalog, delegation tier defaults, two playbook catalog cases, native
tmux input timing, and Workshop model defaults. The Crew Run prompt assertion
was updated to check its retained prohibition rather than the old exact sentence.
All other server cases passed in the full run, and changed prompt/skill cases
passed after the final edits. Workflow suites pass with the two existing model
and Agy-gate failures excluded; the absolute-path assertion now handles the
explicit private linked-CLI exception and passes.

## Measured evidence

Byte counts below compare identical controlled fixtures, not the entire server A
provider request or its token count.

| Fixture | Before | After |
| --- | ---: | ---: |
| Code base file | 3,575 | 1,684 |
| Crew base file | 8,257 | 6,285 |
| Same Code feature set | 5,487 | 1,460 |
| Same 70-custom / 44-MCP tools, with the new shared routing on both sides | 5,066 | 1,388 |

The tool fixture uses synthetic names and a common product-policy stub, so the
last row isolates inventory versus discovery rather than including the old
routing reduction. Real CLI tool definitions and native skill descriptions still
consume context. This implementation has not measured total turn tokens or
latency savings; discovery introduces a lookup before schema retrieval.

Opt-in `TestLocalCLIDiscoverySkillsAndResume` passed on **Claude, Codex and Pi**:
read an attached skill, find an unpredictable tool by intent, load its schema,
execute through HTTP, and return its actual unpredictable receipt. A fresh
Agent then resumed the native conversation with a different tool and skill
input; the updated receipt succeeded. No fixture tool name/input/receipt appears
in the initial system prompt. Cursor stopped at its account usage limit before
executing a fixture tool. Muse and Agy have not been live-qualified for this
change; Agy's prompt fallback is covered by unit tests. Do not claim all six
providers are qualified.

Full component suites passed for profile skills, Code, Crew, wrappers and agent
sessions; server prompt/toolset checks passed. Discovery, dynamic permission
changes, HTTP metadata permissions, schema-cache revocation, server selection,
pagination, unmatched queries, caller instructions, dynamic skills, routing and
native API compatibility tests passed. The remaining mcpagent Agent and workflow
suites pass when the following pre-existing baseline failures are excluded;
those failures were reproduced in separate unchanged baseline worktrees:

- mcpagent: two inactive-provider artifact deletion assertions and
  `TestSessionPublicMethodSurface`'s outdated method list.
- Workflows: `TestResolveDelegationTierConfigExpandsProviderProfile`,
  `TestWorkshopPromptShellExamplesUseAbsolutePaths` (rejects the already-shipped
  linked `cd project` guidance), and `TestValidateStepLLMConfigEnforcesAgyAlphaGate`.

Live OAuth connection onboarding, unavailable-provider qualification, and total
turn cost remain open evidence gaps. They do not change the existing next-turn
registration, permissions, or linked-runtime behavior.

## What the agent received before this change

| Layer | Source and delivery | Assessment |
| --- | --- | --- |
| Product identity and behavior | Code/Crew `prompts/system-prompt.md`; workflow phase templates | Keep identity, mode boundaries, and essential behavior. These also contain feature procedures duplicated elsewhere. |
| Runtime context and policy | Server `prompt_sections.go`, assembled in `server.go`; folder/secret/browser sections | Keep live grants and applicable constraints. Replace procedural detail with references where appropriate. |
| Feature summaries | `pkg/agentprofiles/features.go`, `FeaturePromptExtensions`; server section `product-features` | Each nonempty feature extension is always appended to writable profile chats. Some repeat skill procedures and product text. Crew readers deliberately skip this section. |
| Skill discovery | `internal/workproduct/profile_definition.go`, `RegisterFeatureSkills`; mcpagent `agent/skill.go`, `renderSkillListing`; provider projection | API models get names/descriptions in the prompt. Coding CLIs generally get native skill files instead. Full bodies are read on demand. |
| Provider tool mode and bridge routing | mcpagent `agent/agent.go`, `appendBridgeRoutingInstructions`; `coding_agent_bridge_routing_prompt.go` | Provider-specific native permissions plus shared bridge/HTTP instructions. Shared instructions include unrelated feature procedures. |
| Complete authorized tool-name index | mcpagent `effective_system_prompt.go` → `code_execution_tools.go`, `buildToolIndexForContext` → `prompt/builder.go` | Every code-execution outgoing prompt gets the current index. It contains names, grouping, and endpoints, not all argument schemas. |
| Detailed tool schema | mcpagent `get_api_spec` | Loaded on demand, but requires an exact tool name. There is no local `search_tools` discovery tool. |
| Additional HTTP tutorial | mcpagent `GetCodeExecutionInstructions`; builder `workflow_phase_prompt.go`; workflow shared prompt and bundled bridge reference | Workflows have extra copies of discovery, native-tool, endpoint, auth, and curl guidance. |

The feature-to-skill relationship is many-to-many, not one skill per feature:
secrets, models, and attached folders share `work-integrations`; schedules,
triggers, and bots share `work-schedules-and-bots`; database and dashboard share
`work-dashboard`. Code renders these templates under `code-*` names. Some
features, such as memory and terminal, have no feature skill. Do not mechanically
delete every feature extension or introduce eighteen duplicate skill files.

The manifest already uses a canonical tool registry and request-time permission
checks. This is valuable: it reflects late registration and changed allowlists.
There is only one tagged `<available_tools>` section after composition. The
duplication is mainly semantic, not repeated identical manifest blocks.

## Real prompt baseline

An earlier read-only server A inspection of a saved Code session from
2026-09-30T13:18:04Z measured its saved system text:

| Portion | Unicode characters |
| --- | ---: |
| Code base | 3,566 |
| Workspace | 825 |
| Memory instructions | 2,449 |
| 18 feature summaries | 5,487 |
| Attached folders | 173 |
| Browser | 399 |
| Secret references | 1,099 |
| Bridge routing | 3,438 |
| Tool catalog | 4,793 |
| Total | 22,229 |

Features, routing, and catalog account for 13,718 characters, about 62% of that
saved system text. This is opportunity size, not a promised reduction: their
replacements still need content. The catalog had 70 custom and 44 connected MCP
tool names. Twenty-two custom groups repeated the same endpoint.

This snapshot is older than the inspected source and is not a tokenized count
of the entire provider request. Native CLI instructions, skill descriptions,
conversation history, and tool definitions also consume context. An API
tool-calling session can carry schemas separately from its system text. Compare
before/after using identical profile, grants, tools, and provider settings; do
not attribute unrelated profile changes to this work. No private prompt or
credential values are included here.

## Findings from the investigation

1. **Useful skill triggers are discarded.**
   `pkg/agentprofiles/skillfiles.go` strips SKILL.md frontmatter, then uses
   `SkillFileBinding.Description` from Go. For example, `work-mcp` has a detailed
   action-trigger description in its file, but registration substitutes
   “Connect and manage MCP servers for … projects.” Provider projection
   synthesizes frontmatter from that shorter registered description. Improving
   the file's description alone does not improve discovery.
2. **Code repeats function and connection procedures.** Its base prompt explains
   `list_functions`, `call_function`, follow-ups, and peer-Code boundaries;
   `workflow-references` adds another explanation; `code-workflow-files` contains
   the longer procedure. Personal MCP setup similarly appears in the base,
   feature summary, and `code-mcp` skill.
3. **Connection timing disagrees.** Code's base says a newly connected server is
   available on the next message. Its MCP skill says it works immediately, even
   without direct tools, and suggests `get_api_spec` for a server. Local
   mcpagent requires `tool_name`, so server-only discovery is not supported by
   that handler. This needs an end-to-end connection test; do not choose the
   timing contract by editing prose alone.
4. **A workflow reference contradicts hybrid mode.**
   `guidance/templates/system/mcp-bridge.md` says all native reads/search are
   disabled whenever the bridge is active. Current hybrid provider guidance
   permits native reads/search. Loading the deep reference reintroduces the
   contradiction even if the always-loaded prompt is shortened.
5. **Transport guidance includes feature-specific procedures.** Shared bridge
   routing teaches model/provider configuration and blocking human feedback.
   These belong with the appropriate skill/tool contract. The routing block
   gates feedback guidance on shell admission, not on actual feedback admission,
   and includes Cursor timing advice for every provider.
6. **Provider support is not uniform.** Agy is classified as a coding provider,
   so outgoing composition suppresses its skill listing. No Agy `ProjectSkills`
   implementation was found in the inspected provider source. Agy-specific mode
   preambles exist, but its initializer does not call the bridge-routing append
   path used by the other five CLIs. Treat these as static delivery gaps to
   verify, not proof that all Agy actions fail. Removing summaries could make
   the gaps more visible.
7. **A routing test misses the real hybrid branch.**
   `TestCodingAgentProviderRoutingPromptDoesNotNameExcludedBridgeTools` sets
   hybrid mode but no provider. It passes through the bridge-only preamble. A
   real Claude/Codex/Cursor/Muse hybrid preamble still names
   `execute_shell_command` before admission-filtered guidance. Qualification
   must exercise actual provider/mode pairs.
8. **Descriptions do not fully reflect product options.** Code gets its own
   names and personal-MCP file, but other shared skill bodies are principally
   product-name substitutions. Code peer-calling and DM-only bot restrictions
   currently depend partly on base/feature instructions. Audit those option
   contracts before moving their summaries into skills.

## Ownership contract

Keep four things upfront: product identity, applicable mode/authorization rules,
current workspace/grants, and a short transport contract. Keep one skill index:
native provider discovery when supported, otherwise mcpagent's names/descriptions.
Do not add another feature index repeating that same skill catalog.

Feature definitions should separate **always-on constraints** from **procedures**.
Examples of constraints are Code peer ownership, Run cannot author, private
connection ownership, secret handling, and linked-path access. Keep each once
in the appropriate policy section. Procedures such as connection setup,
schedule creation, dashboard authoring, and call follow-up live in skills or
their references. Skills never grant permission; the backend remains the
authority.

Use SKILL.md frontmatter as the canonical capability description, with explicit
registration overrides only where a rendered product variant needs one. The
same parsed description feeds native projection and API listing. Render
option-specific instructions from the resolved profile into the relevant skill
or reference. Shared skills can keep several feature sections; one skill per
feature is unnecessary.

mcpagent should own all transport guidance. It should generate a small contract
from the actual provider, configured native-tool mode, and admitted bridge tools:
which native operations work; which exact direct bridge names are declared; how
to discover other tools; how to execute their returned route. Include one safe
HTTP/auth example when shell routing is available. Move extended examples,
response decoding, quoting, and feedback waiting into on-demand references or
the relevant tool schema. Gate provider quirks and feature instructions on
their actual applicability. Remove product-owned copies of the generic tutorial
after the shared delivery path is verified for that surface.

## Tool discovery before removing the catalog

Add a small intrinsic bridge tool, `search_tools`, while preserving
`get_api_spec(tool_name=...)` for exact schemas and existing callers.

- Search the agent's canonical registry, never the process-global union of
  another session's tools. Apply current tool, server, account, product, and
  turn authorization before returning results. Execution rechecks permission.
- Accept intent text and optional group/server filters. Return a bounded page
  of exact names, short descriptions and source. Skill references stay in the
  canonical skill index, rather than being guessed from tool names.
  Include pagination and an explicit empty/unavailable result. Never return
  credentials or suggest unauthorized alternatives.
- Begin with deterministic ranking over names, descriptions, and capability
  tags derived from existing feature bindings. A group enumeration fallback
  prevents a synonym mismatch from hiding a tool. Embeddings or an additional
  LLM are unnecessary for the initial implementation.
- For example, the agent reads the scheduling skill, searches “create recurring
  project chat message,” obtains the admitted exact tool name, requests its
  schema, then executes its returned route. For an unfamiliar connected app,
  search discovers the app's tool names without injecting every name upfront.
- Tool visibility is discovered on demand, not permanently inferred from the
  first user message. A task can change during a turn. Permissions and current
  connections can change between turns; cached results are not authority.

Only then replace the global `<available_tools>` name dump with the short
discovery contract. Skill instructions may still mention a few important tool
names where they explain the procedure; they do not need to enumerate every
tool or carry argument schemas.

The remote AgentWorks MCP API already exposes `get_api_spec` with no-argument
listing and `call_tool` (`cmd/server/external_mcp.go`). It is a separate surface
with separate permissions. Its existence does not make no-argument discovery
work inside a local coding-agent bridge. Reuse the architectural pattern, not
an assumption that these handlers are interchangeable.

Start with CLI sessions and API code-execution sessions. API native
tool-calling sessions already supply tool schemas separately; hiding those
requires a supported deferred-tool mechanism or a discovery/execution interface
of its own. Removing system text alone does not remove their schema cost.

## Original qualification plan

1. Establish actual skill delivery for all six CLIs, especially Agy, and preserve
   separate Code, Crew Builder/Run, workflow Builder/Run, and step contracts.
2. Make skill descriptions canonical, cover product-option variants, and move
   repeated procedures into those skills. Keep essential constraints explicit.
3. Add session-authorized discovery while retaining the existing catalog. Verify
   unknown-name recovery, empty matches, denied tools, unavailable servers,
   multi-account isolation, late registration, and permission changes after a
   cached schema lookup.
4. Consolidate transport guidance and opt the relevant surfaces into discovery
   instead of the full catalog. Preserve the old path until live discovery is
   qualified; do not change API native tool-calling exposure accidentally.
5. Compare real outbound prompt and tool-definition sizes with the same fixture.
   Exercise first use, platform actions, connected apps, linked `project` and
   `output` searches, denied actions, and native resume. Measure success, extra
   discovery calls, latency, and total turn usage as well as initial prompt size.

Existing focused tests passed on the inspected source for Code feature-skill
rendering, selected feature contracts, exact-name discovery, denial filtering,
schema-cache authorization, and reserved skill reads. These establish useful
invariants; they do not qualify the proposed architecture or resolve the static
delivery/drift findings above.

The first implementation should address discovery and ownership together.
Merely shortening the 18 summaries while retaining independent catalogs and
tutorials would save some text but preserve the cause of the duplication.
