# Designing a product.yaml

A practical guide for the next product built on the `agentprofiles.Profile` /
`product.yaml` pattern (Video Studio, Chief of Staff, Finance, Dominion today).
It exists because every one of those products hit at least one of the gotchas
below independently, each discovered live rather than by reading the previous
product's file. This doc is the thing to read *before* writing the next one.

For the bigger architectural question — whether a new vertical should be a
`product.yaml` profile at all, versus a full standalone application — see
[`../design/reusable_vertical_product_platform.md`](../design/reusable_vertical_product_platform.md).
This doc assumes that question is already answered "yes, it's a profile."

## The two shapes in production today

Every field below reads differently depending on which shape your product is.
There is no universal default — pick deliberately and say why, in a comment,
the way Finance's and Video Studio's own files do.

| | **Video Studio** | **Finance / Dominion** |
|---|---|---|
| Tool count | ~16 (production pipeline, secrets, browser, patch, shell) | 2 (a single read-only query tool + `execute_shell_command`) |
| Shell needed | Yes — for the production pipeline's own HTTP APIs | Yes — but **only** as the call path to the one query tool; see below |
| `transport` | `auto` | `structured` |
| `agent_tools.mode` | `mcp_only` | `mcp_only` |
| Chat | One aside among several tabs | The only interaction surface (or absent) |
| `runtime.capabilities` | Explicitly declared, all 6 keys | Not declared (a gap — see below) |

If your product is closer to Finance's shape (one or a handful of narrow,
read-only or tightly-scoped tools, no need for a persistent terminal), start
from `agent_go/internal/financeproduct/` or `dominionproduct/`, not Video
Studio's — copying the wrong shape is how several of the gotchas below get
reintroduced.

## More than one profile per product: `profiles:`

Since 2026-09-03 a product may declare extra profiles next to its primary
one. `profile:` (with the top-level `prompt:`) stays the primary profile, the
one the product surface opens; `profiles:` lists the others, each with its
own `prompt:` block. All profiles of a product share its dependencies,
branding and workflows and carry the same `Product` tag, so a user granted
the product gets every one of them; what differs per profile is the prompt,
tools, skills, commands and runtime policy.

```yaml
prompt:
  file: prompts/parent.md
profile:
  id: family-parent
  ...
profiles:
  - id: family-child
    name: Child
    version: 1
    prompt:
      file: prompts/child.md
    tool_policy: { mode: allowlist, enabled: [execute_shell_command, ...] }
    runtime:
      conversation: { mode: keyed, key_type: project }   # one chat per activity
      capabilities: { secrets: disabled }
      sandbox: { mode: strict, network: disabled, read_only: [Downloads] }
```

One loader serves every product: `agentprofiles.LoadProductManifest` (unknown
keys rejected, dependency manifest validated, command prompt files resolved,
duplicate ids and missing prompts refused), and `ProductManifest.BuiltinProfiles`
renders each prompt and returns the profiles ready to register. The three
shipped products use it through a one-line `type ProductManifest =
agentprofiles.ProductManifest` alias; do not copy the loader again.

The first product with two profiles is SparkQuill (parent and child). The
child is a PIN on the parent's account, not a user of its own, so the child
profile lives under the same user folder and switching between the two is a
surface concern gated by the PIN, not a permission change.

## Field-by-field, with the load-bearing gotchas

### `profile.scope: project` vs `global`

Almost always `project`, even if your product feels "global" in spirit the
way Chief of Staff does. Two concrete failures if you pick `global` for a
product that isn't actually meant to be:

- `resolveAgentProfileForQuery`'s `isGlobalScope && requestHasExplicitModel`
  branch lets the browser's own chat-level model selection win outright over
  your `provider_options` curation — the restriction you wrote in
  `runtime.provider_options` becomes decorative.
- Global scope takes the dynamic multi-agent delegation prompt **instead of**
  your `prompt.file`. Finance shipped an early global-scoped version that
  silently never sent `prompts/system-prompt.md` to the model at all — this
  was only caught by testing live, not by reading the code.

Chief of Staff genuinely wants both of those behaviors (any published LLM,
the dynamic delegation prompt) — that's why it's `global`. If you're not
building something with that same intent, you want `project`.

### `runtime.transport`: `structured` vs `auto`, and this is a real tradeoff

Leaving `transport` unset (or `auto`) resolves to native/tmux mode for every
CLI provider except `cursor-cli`. Under native/tmux, the coding CLI runs its
own tool loop **entirely outside mcpagent's tool registry** — `tool_policy`
does not apply there at all. Finance confirmed this live: before setting
`transport: structured`, a Finance chat on `codex-cli` made 12 tool calls
outside its declared `[query_finance_source, read_skill, web_fetch,
web_search]` allowlist.

So: **if your product's safety story depends on `tool_policy.mode:
allowlist` actually being enforced, you need `transport: structured`.** This
is not optional for a narrow, security-scoped product like Finance/Dominion.

But `structured` is not a free upgrade — it has a real cost Video Studio
paid for and reverted from:

> Structured transport cannot stream unless the CLI emits partial events, and
> only pi-cli does. `codex exec --json` was probed directly: a
> 1365-character answer arrived as ONE `item.completed` event, so a codex
> user saw nothing at all until the turn finished, and live steering is
> impossible on a transport with no stdin.

Video Studio needs live steering and many tools across four providers, so it
runs `transport: auto` and accepts that `tool_policy` isn't the enforcement
mechanism there — its own comment notes this openly rather than assuming
Finance's finding transfers. See
[`../design/product_api_transport_for_coding_agents.md`](../design/product_api_transport_for_coding_agents.md)
for the full "tmux vs structured" writeup.

**The decision rule:** does this product's safety story require the
allowlist to be real, and is it fine with one-shot (non-streaming, no live
steering) turns? If yes to both, `structured`. If the product needs
streaming/steering and is willing to treat `tool_policy` as advisory rather
than enforced (e.g. because its allowlist is already broad, like Video
Studio's), `auto`.

### `runtime.provider_options`: curate, don't assume "any provider is safe"

Finance curates to exactly `claude-code`/`claude-sonnet-5-5`, and its own test
suite (`TestFinanceManifestDeclaresProjectScopeAndNarrowAllowlist`) pins that
to exactly one entry, with a comment explaining why: a second tool was
reached live on `codex-cli` even under `mcp_only` — a developer's personal
`~/.codex/config.toml` MCP server (`node_repl`) leaked into the session with
a working `fetch` and real filesystem `cwd`, entirely bypassing the
allowlist. This is documented in
[`../bugs/hybrid_profile_told_it_has_no_shell.md`](../bugs/hybrid_profile_told_it_has_no_shell.md)
section 4, "personal MCP servers leak into product sessions." **"Verified
safe" means a specific provider was tested live under this exact transport +
agent_tools combination — it does not transfer from one provider's
production usage to another's, and it does not transfer from one transport
setting to another** (Video Studio's own `claude-code` sessions run under
`auto`→native/tmux, so its production usage does not validate `structured`
for a different product).

### `runtime.capabilities` — declare it, even when it feels obvious

Video Studio declares all six keys explicitly (`live_input: disabled`,
`raw_terminal: disabled`, `warm_session: preferred`, `workflow_execution:
required`, `browser: required`, `secrets: required`, `voice: preferred`).
Finance and Dominion originally didn't declare this block at all — and the
frontend's chat composer's "open tmux terminal" button (`ChatInput.tsx`,
gated only on `mainTerminalAvailable && activeTabId`, with **no** transport
or capability check at all) showed up for both, even though neither profile
has a persistent tmux pane to attach to under `transport: structured` (a
structured session is a one-shot process — `server.go`'s own comment: *"There
is no persistent pane to retain"*). The frontend currently has no way to read
`runtime.transport`/`runtime.capabilities` from the backend to auto-hide
transport-inappropriate controls, so declaring `raw_terminal: disabled`
doesn't yet suppress that button on its own — the actual fix used for
Dominion was passing `inputVariant="product"` to `<ChatArea>` (see the
frontend section below) and gating the button on that flag in `ChatInput.tsx`.
Declare the capabilities block anyway: it's the truthful statement of what
this product's runtime actually is, and it's the thing a future
transport-aware frontend fix will read.

### `tool_policy.mode: allowlist` is the only real enforced boundary

Everything else in `product.yaml` — `ui.*`, `branding.*`, `workflows.*` — is
either display-only or read by exactly one product's own validator; grep
confirms none of it is read generically by the platform. `tool_policy` is
different: it's enforced at one real chokepoint,
`agent_go/cmd/server/product_tool_gate.go`, which filters at tool
registration and logs `[PRODUCT_TOOL_GATE] profile=… registered=… filtered=…`
— that log line is your ground truth for what a session can actually call,
independent of what the prompt claims. `mode: ""` (unset) is fail-open
(observe-only); `mode: allowlist` is fail-closed. A narrow product with real
security stakes (financial data, trading data) must set this explicitly —
Finance's own comment: *"an unrestricted chat over financial data is exactly
the gap this profile exists to close."*

Two tool sets `tool_policy` does **not** govern, so don't rely on it for
either: mcpagent's own intrinsic tools (`get_api_spec`, `get_prompt`,
`get_resource`, `read_skill`, injected by mcpagent itself), and — under
`agent_tools.mode: hybrid` only — the coding CLI's own native tools (read,
search, skills, todos, subagents; never native writes, see
[../design/native_agent_tools.md](../design/native_agent_tools.md)), which
the gate never sees at all (this is why `hybrid` is a materially bigger
trust boundary than `mcp_only`; see the design doc's own reasoning for why
Video Studio picked `mcp_only` over `hybrid` despite `hybrid` being
available).

### Do not exclude `execute_shell_command` if your product has its own custom
### tool — there is no other way to reach it

**This is the sharpest gotcha, and it looks backwards at first.** A narrow,
security-scoped product like Finance or Dominion feels like it should
declare "no shell" — that reads as the more locked-down, more correct
choice. It is wrong, and both products shipped with exactly this mistake
before it was caught live.

Every custom product tool — Finance's `query_finance_source`, Dominion's
`query_dominion_source`, and every custom tool any other product registers —
reaches the model through exactly **one** path, with no alternate route:
`get_api_spec` discovery, then `execute_shell_command` running `curl` against
`$MCP_CUSTOM/<tool>`. This is not one option among several; grep the
platform and there is no second way a `RegisterCustomTool`-registered tool
becomes callable. Video Studio's own `product.yaml` says this outright:
*"Product HTTP APIs still go through the shared shell bridge, because every
provider can call it as an MCP tool."* The 4 fixed core bridge tools
(`execute_shell_command`, `diff_patch_workspace_file`, `agent_browser`,
`get_api_spec`) are the platform's only tool-exposure mechanism — there is
no second, parallel one to reach for, and don't build one. (An earlier
version of this doc claimed `withAdditionalBridgeTools` was such a
mechanism and that Finance/Dominion's tool used it — that was wrong,
reverted, and is recorded below as a mistake worth not repeating, not as
guidance.)

So: **if `tool_policy.enabled` includes a custom tool, `execute_shell_command`
must be in that same list, or the custom tool is unreachable, full stop —
not merely inconvenient to reach.** Confirmed live, reproducibly, on both
Finance and Dominion: with `execute_shell_command` excluded, the model's own
attempt to reach its one registered tool was rejected by the platform itself
— `tools_unavailable: unknown=[execute_shell_command]: ... Registered tools
for this session: [query_dominion_source]` — and every subsequent turn
truthfully reported it had no working tool, because it didn't. Once
`execute_shell_command` was added to the allowlist, the exact same question
resolved end-to-end on the first try: `get_api_spec` → `execute_shell_command`
running `curl ... $MCP_CUSTOM/query_dominion_source` → a real result → a
correct answer.

**This is a real capability grant, not a free exception.** `execute_shell_command`
is not scoped to "curl this one endpoint" — its actual description is *"run
code, call HTTP endpoints with curl, or perform any shell operation."*
Adding it to a "read-only" product's allowlist genuinely does hand the model
a real shell. The tool_policy allowlist is the only server-enforced
boundary here (see above) — it does not narrow what `execute_shell_command`
itself can do once admitted. The way to keep the product's read-only intent
real is in the **system prompt**, not the allowlist: state plainly that the
model has shell access but its only sanctioned use is calling the product's
one query tool, and that it must not use it for anything else. Both
Finance's and Dominion's prompts say this explicitly now — copy that
wording, don't invent your own weaker version of it.

Verifying this is genuinely working (not just registered) needs a live test,
not a static check — see the verification checklist below.

### `runtime.sandbox` — a deny-by-default shell for one profile

By default a project-scoped profile gets the folder guard: its conversation
workspace is writable, `skills/`, `subagents/` and `Downloads/` are read-only,
and the command runs in an allow-by-default kernel sandbox with network. That
is right for a parent or an operator. It is wrong for a child, or any profile
that must not see the rest of the machine:

```yaml
runtime:
  sandbox:
    mode: strict         # deny-by-default: only the workspace, the read_only
                         # folders, system binaries and scratch space exist
    network: disabled    # strict mode only; omit or "allowed" keeps network
    read_only: [Downloads]   # replaces the default read-only list; [] = none
  capabilities:
    secrets: disabled    # the shell environment then carries no secrets
```

What enforces it: the server applies the policy in the same place it applies
the folder guard (`workspace.SetSessionSandbox`), it travels to the workspace
shell handler in the folder-guard config (`strict_allowlist`, `deny_network`),
and `security.Isolator` builds the seatbelt profile from it. Fully enforced on
macOS; on Linux the path rules apply but network is not cut (the Linux
isolator does not implement it yet). Pair it with `tool_policy.mode: allowlist`
so the profile cannot reach tools that bypass the shell.

### `dependencies` — only if you actually have a per-project workspace

Video Studio provisions skills/CLI/MCP servers into each project's workspace
because it *has* per-project workspaces. Finance and Dominion set
`dependencies: {}` — they read a fixed, already-existing workflow database,
not a project folder, so there's nothing to provision. Don't reach for this
block by default; it exists for products that manage their own workspace
lifecycle.

## Backend scaffolding checklist

Mirrors `financeproduct`/`dominionproduct` almost line for line (~95% is
boilerplate copied verbatim, only identifiers change):

1. `product.yaml` — see above.
2. `prompts/system-prompt.md` — identity + an honest capability statement
   (you *do* have `execute_shell_command` if your tool needs it — see
   above — but scoped explicitly to calling your one tool, plus whatever
   else is genuinely absent: "no file write, no delegation") + per-source
   real tables/columns with data-quality landmines called out in bold + a
   short "how to answer" section. The prompt is where a schema's real
   dirtiness lives, so the agent doesn't rediscover it wrong.
3. `product_config.go` — embed loader + `decoder.KnownFields(true)` (a
   typo'd YAML key is a hard failure, not a silent ignore) + a validator
   pinning the load-bearing string fields (`schema_version == 2`,
   `Profile.ID`, `Profile.Scope`, `UI.Surface`, `Prompt.File != ""`).
4. `profile_definition.go` — `BuiltinAgentProfile()` /
   `BuiltinAgentProfiles()` / `RegisterProductSkills()` (keep the last one
   even as a no-op, so `server.go`'s registration call shape matches every
   product and adding a skill later needs no `server.go` change).
5. `<name>_query_tool.go` (or your tool file) — the `ToolFactory`. Read
   `runtime.UserID` inside the factory closure, not at setup time — it runs
   fresh per profile-bound turn. Bad input should return `(message, nil)`,
   not an error; only infrastructure failures return `err`.
6. `product_config_test.go` — pin every load-bearing property with a comment
   explaining *why* it's load-bearing, the way Finance's does. This is what
   makes a future accidental revert (e.g. someone "cleaning up" `transport:
   structured` back to unset) fail a test instead of shipping silently.
7. `server.go` — one import, one ~10-line registration block
   (`RegisterProductSkills` → `RegisterProfile` for each
   `BuiltinAgentProfiles()` entry → `RegisterAgentProfileRuntime`), placed
   next to the other products' identical blocks, before `api :=
   &StreamingAPI{...}` is constructed.

## Frontend scaffolding checklist

1. `<Name>Surface.tsx` — `<NAME>_PROFILE_ID` constant; a
   `use<Name>ChatTab()` hook that finds-or-creates the one singleton chat tab
   for this profile (`agentProfileWorkspace`/`agentProfileProjectTitle` set
   — required for `scope: project` to resolve); `<ProductSurfaceSwitcher/>`
   in the header.
2. **Pass `inputVariant="product"` to `<ChatArea>`.** This is the product-chat
   boundary, not only a styling flag. It installs the shared
   `ProductChatSurface` automatically: durable human/assistant history,
   streaming state, normalized `agent_error` / `conversation_error` / failed
   completion handling, safe technical details, and retry of the last human
   turn. It also drives the product composer decisions — placeholder text,
   padding, hidden live-delivery status, upload styling, and no tmux terminal
   toggle. A domain-specific `contentRenderer` may replace the visual layer,
   but must keep the shared renderer props and failure adapter; do not parse
   provider error strings inside a product surface.
3. `<Name>Mark.tsx` — a gradient badge wrapping a lucide icon; 27 lines,
   copy `FinanceMark.tsx` and swap the icon/gradient.
4. Registration — exactly three files: `useProductSurfaceStore.ts`'s
   `ProductSurface` union, `App.tsx`'s lazy import + one ternary branch, and
   `ProductSurfaceSwitcher.tsx`'s `products` array entry. Nothing else
   needs editing — no router, no icon map beyond the switcher array, no
   product→profile-id mapping file.

## Verification checklist before calling a product "done"

Static checks (typecheck/lint/tests) prove the code compiles and the
manifest parses. They do not prove the chat works. Verify live:

- [ ] `[PRODUCT_TOOL_GATE] profile=<id> mode=allowlist registered=N: ...`
      in the server log matches your intended tool set exactly — your
      custom tool **and** `execute_shell_command` should both be in
      `registered=`, not `filtered=` (see the gotcha above: without
      `execute_shell_command`, your custom tool cannot be reached at all).
- [ ] Send a real message that requires your custom tool. Confirm the full
      chain in the server log: a `get_api_spec` call, then an
      `execute_shell_command` call whose `cmd=curl ...` targets
      `$MCP_CUSTOM/<your_tool>`, then a `[TOOL] ... name=your_tool ...
      duration=...` line with no error — and read the model's own final
      answer to confirm it actually used the result rather than reporting
      failure. A tool executing successfully server-side is not proof the
      model's answer used it; these are two different facts to check.
- [ ] Test on a **genuinely fresh session**, not a resumed one — a resumed
      native CLI session can retain its original system prompt from before
      your fix, making a fix look like it didn't work when it did. (Force a
      fresh session by clearing the one `chat-store` localStorage entry for
      your profile's tab and reloading, if there's no in-app "New Chat" for
      the surface yet.)
- [ ] Open the chat composer and confirm the placeholder text and provider
      chip look product-appropriate, not leftover AgentWorks/Video Studio
      defaults.
- [ ] Force one provider failure (quota/auth/configuration or an unavailable
      test provider). Confirm the spinner stops, the product shows actionable
      copy, raw provider output is collapsed under **Technical details**, and
      **Retry** resubmits the last human message when the failure is retryable.

## AgentWorks chat capabilities

AgentWorks declares `chat_policy.modes` (`builder`, `run`), `origins`
(`interactive`, `scheduled`, `pulse`, `child`, `bot`, `notification`) and
`read_only` in its embedded product manifest. Capabilities are intersected; an
unknown role grants nothing. The current named host groups are MCP management,
plan authoring, report authoring, secret management, KB maintenance, improvement
proposals and workspace UI. Adding a capability requires manifest validation,
a registrar consumer and tests for allowed and denied callers.

Scheduled ordinary execution resolves to Run. Pulse maintenance is a specialized
origin with existing typed reviewer/fixer restrictions, not an interactive
Builder. Client-visible mode alone is never sufficient evidence for installer
admission. Existing non-AgentWorks products keep their own tool allowlists.

Service deployments should set `AGENTWORKS_MCP_STATE_DIR` to a private durable
state directory, outside release/workspace folders. Startup refreshes the shipped
base catalog there and preserves its user overlay. When migrating an existing
release layout, copy the old current overlay before switching releases (as RTS
deployment does). Local startup can keep its existing config path by leaving the
variable unset. Neither secrets nor user server entries belong in product.yaml.

### AgentWorks prompt files and skill lists

`chat.builder` and `chat.run` are the live workflow-chat definitions. They are
separate from the top-level `prompt`, which belongs to the general AgentWorks
profile and does not supply the Builder/Run prompt.

```yaml
chat:
  builder:
    prompt:
      file: prompts/builder.md
      includes: [prompts/workflow-shared.md]
    skills: [system-tools, builder-reference, workflow-commands]
  run:
    prompt:
      file: prompts/run.md
      includes: [prompts/workflow-shared.md]
    skills: [system-tools, builder-reference]
```

Files are relative to the embedded product root. The entry files choose a named
shared template and define their mode instructions; includes contain named Go
template definitions only. Large instructions stay in Markdown, not YAML. The
server supplies workflow paths, current state and tool-transport values at render
time. Other runtime sections (granted capabilities, notifications, secret names,
browser configuration and workspace context) continue through the server's prompt
composer. Runtime/user strings are data, never parsed as template source.

The manifest loader rejects missing/empty/unsafe paths, invalid template syntax
and undefined named templates. AgentWorks also requires both modes and rejects
unknown or duplicate core skill names; `workflow-commands` is Builder-only.
Selected workflow skills remain additive. Skill content is still filtered by
mode and MCP admission, so configuration cannot grant a tool or bypass origin and
read-only restrictions. API and coding-CLI transports share the attached bundles;
CLI projection is supplied by the existing adapter.

A changed prompt file, shared include, or configured skill list changes the native
session definition fingerprint. Existing coding-CLI sessions use the established
conversation replay path to receive updated instructions on their next turn.
Unchanged definitions retain compatible sessions. Files ship in the Go binary:
editing them requires a rebuild/deployment, not a runtime YAML hot reload.

Validation lives in `pkg/agentprofiles/chat_prompt_test.go`,
`internal/agentworksproduct/chat_definition_test.go`, the configured reference
surface tests and the complete server prompt/role regression suites.
