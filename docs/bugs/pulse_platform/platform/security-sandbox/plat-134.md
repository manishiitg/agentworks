[← platform / security-sandbox](index.md)

# PLAT-134 — direct product chat still entered through the legacy multi-agent orchestrator and constructed capabilities the product explicitly forbade

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | platform |
| Area | security-sandbox |
| Summary | P1 — ordinary and product chat are simple conversational |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `partially_implemented` — direct chat runtime, durable product-chat rotation, and hybrid tool contract are covered; compatibility naming and unreachable legacy implementation remain to be extracted/deleted |
| Last synchronized | `2026-08-22` |

- **Priority:** P1 — ordinary and product chat are simple conversational
  sessions, but their runtime was assembled as a generic multi-agent
  orchestrator. That made delegation, schedule ownership, tier selection and
  background-agent lifecycle appear to be part of every chat even when a
  product profile explicitly disabled them.
- **Owner:** direct chat request/runtime construction (`cmd/server/server.go`,
  `cmd/server/agent_profile_routes.go`), chat UI model/config projection
  (`frontend/src/components/ChatArea.tsx`, `ChatInput.tsx`)

## Intended product contract

Video Studio and Dominion need one reusable product-chat core:

1. a server-owned system prompt;
2. attached skills;
3. an explicit allowlisted tool surface;
4. a server-owned model and credential binding;
5. a server-owned project/workspace binding; and
6. a durable conversation key that resumes the same conversation.

They do not use chat-level delegation. Finance keeps its dashboard UI but does
not expose chat for now. Workflow Builder's `run_in_background` and
`call_sub_agent` are a separate automation-authoring capability and are not in
scope for removal.

## Root cause

The historical name `agent_mode="multi-agent"` became both a UI category and a
runtime policy. `handleQuery` treated every ordinary/product chat as an
orchestrator and constructed session-specific wrappers for:

- `delegate`, `query_agent`, `terminate_agent`, and `list_agents`;
- chat-owned schedule create/update/delete/trigger tools;
- async delegated-task execution and its background registry;
- delegation-tier model selection and per-tab reasoning controls; and
- a large delegation guidance surface.

Product profiles eventually filtered forbidden tools out, but only **after**
the server had constructed and registered the broad machinery. The public
browser request could also carry a large `QueryRequest`, even though prompt,
model, tools, skills, permissions and workspace are product-owned authority.

This is the wrong dependency direction: a product declared a small capability
set, then the generic chat runtime built a larger orchestrator and tried to
subtract capabilities from it.

## Refactor shipped

The live runtime now follows the direct-chat contract:

- Product chat accepts only `message` and optional `conversation_key`; the
  server resolves the profile and authors the internal request.
- Ordinary chat uses `GetAgentWorksChatInstructionsWithUser`, which tells the
  agent to work directly with attached skills/tools and not create sub-agents
  or own schedules.
- `handleQuery` no longer calls `CreateDelegationTools`,
  `CreateDelegationToolExecutors`, delegated-task wrappers, or chat schedule
  tool factories.
- The chat-specific schedule tool file and pre-registration allowlist were
  removed. Scheduler default helpers that are genuinely used by workflow
  scheduling were retained in a small neutral file.
- Direct chat no longer lets delegation tiers silently replace the selected
  primary model. Product profiles retain server-owned model authority.
- Video Studio is currently pinned to its server-owned Claude Code model; its
  provider options and frontend provider dropdown were removed so the UI cannot
  claim a choice the minimal product-chat request no longer sends.
- Video Studio also declares `credential_scope: global`; its per-project coding
  agent token button/dialog were removed, and the runtime no longer layers old
  project token overrides over the shared Claude login.
- The frontend no longer eagerly loads delegation tiers for chat and no longer
  carries the unused delegated-task reasoning popup.
- The delegation reference entry/template was removed from the chat guidance
  surface.
- Product mode now installs one shared `ProductChatSurface` automatically.
  Existing `agent_error`, `conversation_error`, failed completion, and cancel
  carriers normalize into a single product failure state; retryable failures
  can replay the last human turn and raw provider text remains collapsed under
  technical details. New products receive this behavior through
  `inputVariant="product"` without product-specific event parsing.

The focused backend contract asserts that the direct-chat path contains no
delegation or schedule registration. `go build ./...`, the focused backend
tests, TypeScript, and all 632 frontend tests pass.

## Boundary deliberately preserved

Workflow Builder and scheduled workflow execution still need background-agent
tracking for `run_in_background`, `call_sub_agent`, message-sequence children,
Pulse reviewers/Fixer, execution-tree liveness, and auto-notifications. That is
workflow orchestration, not product chat delegation. Deleting the shared
background registry would break those features and is explicitly outside this
ticket's direct-chat removal.

The bot connector also still owns a legacy model-profile store currently named
`delegation_tier_config`. Direct/product chat no longer loads it; it must be
renamed or migrated independently before the store/API can be deleted.

## 2026-08-22 — product chat New Chat and browser tool boundary

Two product-facing details were found while exercising Video Studio. They are
shared ProductChatSurface/runtime behaviour, not a Video Studio-only UI patch.

### New Chat must rotate the server-owned durable conversation

The old New Chat action called `resetTabChat`, which cleared the tab and minted
a browser-local random session id. A keyed product project, however, resolves
its authoritative session from `product.json`, and the product-conversation
registry returns that same canonical session on the next query. The result was
a misleading blank screen followed by the old durable transcript returning as
soon as the user said “hi”.

The client now calls `POST /api/agent-profiles/{id}/conversation/new` before it
clears a product tab. The server mints a fresh conversation and session,
replaces the registry entry, and, for a keyed project, updates that project's
`product.json` `session_id`. The production/artifact workspace is retained and
the prior transcript is not deleted; it simply ceases to be the active chat.
If rotation fails, the UI keeps the old conversation rather than pretending a
new one exists. Singleton products use the same endpoint but only rotate their
registry binding.

### Browser product uses the guarded MCP surface

Video Studio's `runtime.agent_tools.mode: mcp_only` exposes the guarded MCP
bridge for all workspace and product work, including
`execute_shell_command` for product HTTP APIs. This retains the session's
project-scoped Folder Guard and structured tool progress/results for the web
client. It does not claim that native CLI tools have the same guard: they are
not enabled for this browser product.

### Native terminal questions cannot be used by a browser product

Testing native tools exposed a second boundary: Claude Code's native default tool
surface can open an in-terminal multiple-choice question. The web client cannot
render or answer that terminal UI, so the provider waits forever and ProductChat
continues to show `Working`. This is not an LLM latency issue and it is not a
Folder Guard decision.

Browser products must therefore default to `mcp_only` until native interactive
questions are either bridged into ProductChat or denied by a provider-specific,
tested `--disallowed-tools` configuration. Prompt guidance alone is insufficient.

Separately, mcpagent must preserve provider-projected `.claude/skills` and its
managed `CLAUDE.md` across per-turn `Agent.Close()` calls for persistent native
sessions. A web turn creates a short-lived Agent, but its tmux CLI session is
intentionally retained; deleting the files at the former boundary left the
still-live session with no inspectable project setup.

Dominion AI uses the same `transport: auto` + `agent_tools.mode: mcp_only`
profile shape as Video Studio. The transport keeps the Claude session and its
project projection warm; MCP-only keeps terminal-native tools out of the web
chat path.

## Remaining compatibility cleanup

The behavior is simplified, but the source tree is not yet fully reduced:

1. `"multi-agent"` remains the persisted/internal compatibility identifier for
   ordinary chat. It should become a neutral `chat` runtime kind through a
   versioned state migration, not a blind string replacement.
2. Product chat still forwards a server-authored `QueryRequest` into
   `handleQuery`. Extracting a shared direct-turn runner would remove the broad
   request type from the product path without another frontend migration.
3. Unreachable chat-delegation factories/executors remain in
   `virtual-tools/delegation_tools.go`, `delegation.go`, and
   `background_agents.go`. Those files co-locate workflow notifier/lifecycle
   types that are still live. Extract the workflow-owned types first, then
   delete the dead chat-only functions and tests.
4. Rename/migrate the bot connector's tier configuration so chat terminology no
   longer leaks into unrelated connector model selection.

## Acceptance

- A normal chat and a product-profile chat register zero chat-level delegation
  or schedule-management tools.
- Product clients can send only `message` plus an optional durable
  `conversation_key`; model, tools, skills, credentials and workspace cannot be
  overridden by the browser.
- Video Studio and Dominion resume the same product conversation and remain
  constrained to their declared profile capabilities.
- New Chat rotates the durable product conversation server-side; a subsequent
  product query cannot restore the transcript it just replaced.
- Browser-product documentation and prompts require the guarded MCP surface
  and explain why provider-native terminal tools are unavailable there.
- Chat startup performs no delegation-tier API request and has no delegated-task
  reasoning control.
- Every product chat stops its working state and renders a normalized failure
  for both explicit error events and terminal completion text such as
  `all LLMs failed`; retry does not require product-specific code.
- Workflow Builder `run_in_background` / `call_sub_agent`, scheduled workflow
  children and Pulse background reviewers continue working unchanged.
- A later source cleanup can delete the unreachable chat delegation
  implementation without changing runtime behavior.

## Verification status

Code-level and automated verification are complete. A rebuilt server still
needs one live direct-chat turn each for ordinary Chat, Video Studio and
Dominion, plus one Workflow Builder background-agent run proving the preserved
boundary.
