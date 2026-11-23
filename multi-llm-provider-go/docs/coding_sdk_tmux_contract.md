# Coding SDK Tmux Contract

This document defines the tmux-backed interactive transport contract for coding
SDK providers in `multi-llm-provider-go`.

Covered providers:

- `claude-code`
- `codex-cli`
- `cursor-cli`

The goal is to expose terminal-native coding tools through the normal provider
interface for both chat and workflow execution: terminal snapshot progress, MCP
bridge tool calls, same-session follow-up input, cancellation, final response
extraction, and native resume.

The mechanical source of truth is `coding_agent_contract.go`. Every provider
that should behave like a coding agent must be declared there with its transport
and capabilities. Host applications should use that contract instead of
copying provider string lists. Adding a future provider means adding a contract
entry and tests for:

- explicit working directory
- native system/developer instruction transport
- MCP bridge launch and bridge-only tool policy
- live input behavior
- interrupt/cancel behavior
- terminal snapshot streaming behavior
- final assistant text extraction
- resume or explicit non-resume semantics
- process-scoped cleanup
- tmux session-loss handling for `no server running`, `can't find pane`,
  `can't find session`, and `no current target`

This contract is the normal product path. Structured/JSON CLI transports are
legacy fallback or provider-regression test paths unless a capability has not
yet been implemented in tmux.

## Product Surfaces

There are two product surfaces that share the same coding-CLI transport:

- Interactive chat: a user is chatting with a coding agent in a workspace. This
  includes workflow builder chat, optimizer chat, run chat, and normal coding
  agent chat.
- Workflow execution: a workflow step, route, sub-agent, background execution,
  scheduled job, or deterministic test run.

Both surfaces should run the provider TUI in tmux when the provider supports it.
The difference is orchestration: workflow execution sends intermediate messages
programmatically and waits for idle; chat accepts live user steer input.

## Highest-Risk Tmux Requirements

Every tmux provider integration lives or dies on two behaviors:

1. **Completion detection: decide when the current provider turn is really
   finished.**
   - The primary signal must be provider-owned TUI state: ready prompt visible,
     no running/thinking/tool indicator, no queued input, no unresolved modal,
     and a stable pane for the provider stability window.
   - Prompt-contract text such as `STATUS: COMPLETED` is not provider-owned
     lifecycle state. It may be used only as a bounded UI recovery hint by host
     applications, never as the adapter's primary completion signal.
   - A false positive here is severe: workflow validation can run before files
     exist, retries can be triggered incorrectly, or a later user draft can be
     treated as the next completed response.

2. **Final response extraction: decide what text is the assistant's answer.**
   - Extraction must run only after completion detection says the turn is idle.
   - The extracted response must exclude terminal chrome, old turns, tool panels,
     MCP JSON, shell output, startup banners, queued user drafts, validation
     feedback, spinner/status lines, and menu/footer text.
   - A false positive here is severe: users see tool dumps as final answers,
     workflow logs persist the wrong response, or validation/learning consumes
     stale text from a previous turn.

Every provider contract test must include fixture and real-E2E coverage for both
behaviors. A provider is not certified just because it can launch in tmux and
stream a pane.

## Normative Coding Agent Contract

A provider is a coding agent only if it can satisfy this shared contract. Exact
CLI flags and TUI strings are provider-specific, but host applications must be
able to rely on the same behavior for every coding provider.

### 1. Launch

The adapter must:

- launch the real provider CLI in tmux
- use one isolated tmux session per logical app/agent session
- start in the exact caller working directory
- keep the provider cwd aligned with the MCP bridge shell cwd
- inherit the user login-shell environment unless explicitly disabled
- expose provider, model, effort, working dir, tmux session id, and native
  session/thread id metadata when available
- detect missing binaries, unsupported versions, lost tmux server/panes, and
  auth/rate-limit failures with clear errors
- avoid putting secrets in process args or shell command text where the CLI
  provides a safer env/config path

### 2. Instructions and Prompting

The adapter must:

- pass system/developer instructions through the provider's native mechanism
  such as system-prompt files, rules files, project config, or env-supported
  instruction paths
- never concatenate system/developer instructions into the pasted user message
- paste user input through tmux buffer paste, preserving newlines, blank lines,
  quotes, markdown, JSON, shell-looking commands, Unicode, and large pasted
  blocks
- submit only the latest user message for persistent native TUI sessions when
  native session memory is proven reliable
- replay bounded prior-turn transcript only for providers that need it, and only
  in a way that cannot be confused with live user input
- reject unsupported content parts such as images instead of silently dropping
  them

### 3. Tool Surface

The adapter must:

- launch with the MCP bridge configuration when tools are needed
- support bridge-only mode where provider-native shell/filesystem/browser tools
  are disabled or denied
- explicitly allow only the intended bridge tools when the provider supports an
  allow-list
- handle permission/trust/tool approval prompts deterministically
- prove through real E2E that forbidden built-in tools are unavailable in
  bridge-only mode; passing flags is not enough

### 4. TUI State Machine

Every tmux adapter must classify the provider pane into a small state machine:

- `starting`: CLI launched, startup banners or MCP init messages may appear
- `needs_trust`: workspace trust prompt or equivalent
- `needs_auth`: login/API key/OAuth/keychain failure
- `rate_limited`: quota or rate-limit prompt/error
- `ready`: input prompt visible and no active work is present
- `working`: thinking, tool calling, editing, waiting, or interrupt footer
  visible
- `queued_input`: user/follow-up text is queued while the provider is still
  working
- `completed`: provider is idle, stable, and final assistant text is parseable
- `failed`: provider surfaced a fatal error
- `lost_session`: tmux server, session, or pane disappeared

Completion detection must require all of:

- ready prompt visible
- no running/thinking/tool-call indicator anywhere in the current captured turn,
  even if the input prompt/footer is also visible at the bottom of the pane
- no queued input
- no unresolved modal, trust prompt, auth prompt, or rate-limit prompt
- pane stable for the provider's configured stability window
- owned tmux session still exists

Completion detection decides **when** to read. It must not decide **what** the
assistant said. Final response extraction is a separate step and must be tested
separately.

Important regression case: a provider may render a prompt-looking input footer
while a long tool call is still active above it, or while queued follow-up text
is waiting for the next tool boundary. The adapter must classify that state as
`working` or `queued_input`, never `ready` or `completed`. Tests must include a
long captured pane where an active line such as `Working (... esc to interrupt)`,
`Calling ...`, `esc to cancel`, or provider equivalent appears above long tool
output and the ready prompt appears near the bottom.

String matching is expected because these providers expose TUIs, not stable
structured APIs. The requirement is to keep the matching state-based, narrow,
provider-specific, and covered by real E2E.

### 5. Live Input

The adapter must:

- send a new message to the existing tmux session when the agent is working
- use a provider-native input queue when the TUI supports it
- otherwise maintain an adapter-level pending queue and submit when the ready
  prompt returns
- never start a duplicate provider run for a live follow-up
- never parse queued user text, validation feedback, or follow-up prompts as
  final assistant output
- return a clear error when a live-input API is used against an idle session and
  the caller should start a normal turn instead

### 6. Cancellation

The adapter must:

- send the provider interrupt key on context cancellation
- wait briefly for idle or process exit
- reuse a persistent tmux session after cancellation only if the provider has
  visibly returned to a prompt-ready state
- accept and complete a new user message under the same owner after Stop;
  readiness alone is not proof that the conversation remains usable
- discard and kill an adapter-owned persistent tmux session when cancellation
  does not return it to prompt-ready state; a later turn/fallback must start a
  fresh session instead of inheriting a still-busy pane
- kill only the adapter-owned tmux session if graceful interrupt fails
- preserve unrelated sessions, parallel agents, and background agents unless the
  owning lifecycle explicitly says they should stop
- unregister stale session ids when tmux reports missing server/session/pane

### 7. Streaming

The adapter must:

- stream terminal snapshots as terminal/screen chunks, not assistant-content
  chunks
- deduplicate snapshots enough to avoid repeated TUI redraw spam
- keep final assistant extraction separate from terminal streaming
- allow the UI to hide/show terminal output without changing provider behavior
- avoid converting progress rows, tool panels, JSON payloads, or menu text into
  normal assistant stream content

### 8. Final Response Extraction

The adapter must extract only the final assistant answer. It must reject:

- tool progress such as `Calling api-bridge...`
- raw MCP panels or JSON tool payloads
- shell output unless the assistant intentionally quoted it as answer text
- user prompt echoes
- queued follow-up input
- pre-validation or retry feedback
- trust/auth/rate-limit menu options
- startup banners, shortcut hints, footer text, spinner frames, and box borders
- stale assistant text from prior turns

Hard invariant: a final response must never be a TUI status line, queued user
message, validation feedback, or terminal menu option.

Extraction decides **what** to return. It must not be used to infer that the
provider has finished. Provider-specific extraction examples:

- Claude Code: latest assistant block marked by `⏺`, or trailing assistant text
  only after the pane is idle.
- Codex CLI: final framed answer between provider separator lines, with a
  segment fallback that rejects tool/status/error segments.

If provider-specific extraction returns no answer after completion detection has
already proven the pane idle, the adapter may use a bounded terminal-tail
fallback. This fallback returns only the latest cleaned text lines from the
current turn, capped at 24 lines, after stripping obvious TUI chrome, prompt
echoes, tool/status rows, startup banners, borders, and queued validation/user
text. It must not return the full pane.

Required regression fixtures:

- busy pane with prompt-looking footer must not complete
- completed pane with tool panels must extract only the final answer
- old turn plus new turn must extract only the newest assistant answer
- queued draft after completion must not be extracted as assistant text
- pre-validation retry feedback must not be extracted as assistant text
- parser-miss fallback must return a bounded cleaned text tail, not a compact
  error or full terminal dump

### 8a. Token & Cache Surfacing

The adapter must:

- populate `gi.PromptTokens` / `gi.CompletionTokens` / `gi.TotalTokens`
  (modern naming) from the sidecar transcript. Mcpagent's
  `accumulateTokenUsage` honors both `PromptTokens/CompletionTokens`
  and legacy `InputTokens/OutputTokens`, so either pair works — but
  one of the pairs MUST be populated or the cost ledger will record
  zero-token entries.
- when the provider reports cache hits, populate both:
  - **typed**: `gi.CachedContentTokens`
  - **raw key**: `gi.Additional["cache_read_input_tokens"]`
- for Anthropic-style providers that bill cache writes separately
  (Claude Code only among the tmux providers), also populate
  `gi.Additional["cache_creation_input_tokens"]`.
- when a sidecar parser fills `usage.Additional`, the adapter MUST
  merge that map into its own local Additional before constructing
  the response, or the raw cache keys won't reach the consumer.

Why: the cost ledger's `extractCacheTokens` keys off the raw
Anthropic-style names in the Additional map, not the typed field.
See [`docs/COSTS_AND_CONVERSATION_HISTORY.md`](COSTS_AND_CONVERSATION_HISTORY.md)
→ "Cache token surfacing contract" for the full audit table and
canonical adapter idiom.

### 9. Sessions and Resume

The adapter must:

- isolate sessions by owner/app session id
- reuse the same tmux session for persistent chat multi-turn
- avoid cross-talk between parallel agents
- close and recreate the session when launch fingerprint changes
- support native resume when the provider exposes stable metadata
- keep resume metadata separate from persistent tmux metadata
- clean up idle sessions and process-scoped registries

### 10. Error Reporting

Errors should be classified into stable categories where possible:

- binary missing
- unsupported version
- auth missing or expired
- trust prompt unresolved
- rate limit
- MCP server failed
- tool policy denied
- prompt paste failed
- timeout waiting for ready
- timeout waiting for completion
- parse failure
- tmux session lost

When useful, include a redacted latest pane snapshot in the error so the caller
can debug the provider TUI state without exposing secrets.

## Provider Transports

Claude Code:

- Default interactive transport: `claude` inside tmux
  (`CLAUDE_CODE_TRANSPORT=tmux`).
- Legacy structured print transport: `claude -p --output-format stream-json`
  (`CLAUDE_CODE_TRANSPORT=print`). This path is disabled unless
  `CLAUDE_CODE_ALLOW_LEGACY_PRINT=1` is also set, and should only be used for
  targeted legacy tests.
- Do not use `claude -p` or `claude --print` inside the tmux transport.
- Workflow and chat both use the tmux transport.
- Tmux transport and tmux persistence are separate concerns. Supplying an
  owner/app session id selects the tmux transport. Supplying the provider's
  persistent-interactive flag keeps that tmux session alive after a completed
  turn.
- Workflow steps, workflow sub-agents, and background tasks default to bounded
  `close_on_completion` lifecycle: create/use the step-owned tmux session,
  execute the turn, extract the final result, then close the owned tmux session.
- Interactive chat and workflow-builder chat default to `keep_alive`
  lifecycle: reuse the same tmux session across turns until idle cleanup,
  explicit close, launch-fingerprint change, lost-session cleanup, or server
  shutdown.
- Step-level runtimes may opt into `keep_alive` only with an explicit lifecycle
  setting such as `coding_agent_tmux_lifecycle="keep_alive"`; this should be
  reserved for steps that intentionally need live steering/debugging after
  completion.

Codex CLI:

- Legacy structured transport: `codex exec --json`.
- Interactive transport: `codex` TUI inside tmux.
- Default coding model: `gpt-5.5`.
- Workflow and chat both use the tmux transport when an owner session id is
  available.

Cursor CLI:

- Interactive transport: `cursor-agent` TUI inside tmux.
- The adapter must not use `cursor-agent -p`, `--print`, or
  `--output-format stream-json` for the tmux path.
- Default model selector: `cursor-cli`, which means "do not pass --model; let
  Cursor use its configured/account default".
- Bounded per-turn calls should still launch `cursor-agent` in tmux, paste one
  turn, parse the final TUI output, and close the tmux session.
- Persistent chat should keep the same tmux session alive when
  `cursor_interactive_session_id` plus `cursor_persistent_interactive=true` are
  provided.

## Image Input Contract

`llmtypes.ImageContent` must never be silently dropped.

- Claude Code `print`: supports base64 and URL image parts through stream-json
  content blocks.
- Claude Code tmux: rejects image input until the TUI transport
  has a real attachment path.
- Codex CLI `exec`: supports base64 image parts by writing temporary image files
  and passing them through native `--image` flags. Image URLs are rejected
  because Codex CLI expects local files.
- Codex CLI persistent tmux: rejects image input until live image attachment is
  implemented for the TUI session.
- Cursor CLI tmux: rejects image input until live image attachment is
  implemented for the TUI session.

## Launch Contract

All tmux-backed providers must:

- Create one adapter-owned tmux session per application session id.
- Use an internal tmux session name that does not leak into the UI.
- Register `app_session_id -> tmux_session_name` before accepting live input.
- Run the provider CLI from the same effective working directory used by the
  MCP bridge shell tools. This is a hard invariant:
  - chat resolves to the logged-in user's chat workspace
  - workflow chat resolves to the workflow workspace
  - workflow step agents resolve to the run/execution workspace selected for
    that step session
  - adding a new coding CLI provider requires adding an explicit working-dir
    option and contract coverage before it can be treated as a coding CLI
- Keep launch settings stable for the lifetime of the persistent tmux session.
- Close and recreate the session if model, MCP config, approval mode, tool
  policy, or workspace root changes. Provider-specific system prompts are
  pinned at session start for persistent chat; normal per-turn app prompt
  variation must not silently restart the TUI and lose chat context.

Provider-specific launch requirements:

- Claude Code:
  - pass system instructions with `--system-prompt-file`
  - pass MCP config with `--mcp-config <file> --strict-mcp-config`
  - disable internal tools with `--tools ""` unless explicitly overridden
  - use `--permission-mode dontAsk`
  - start with `--session-id <uuid> --name <display-name>` when creating a new
    native session
  - resume with `--resume <uuid>`
- Codex CLI:
  - pass model with `--model`, defaulting to `gpt-5.5`
  - pass developer/system instructions through native config overrides
  - pass MCP bridge servers through config overrides
  - disable `shell_tool` when bridge-only tool policy is required
  - use a no-prompt approval policy for MCP-controlled runs
- Cursor CLI:
  - launch `cursor-agent` in tmux from the caller-provided workspace directory
  - pass model with `--model` only when the model selector is not `cursor-cli`
    or `auto`
  - pass system/developer instructions through a temporary/restored
    `.cursor/rules/*.mdc` rule with `alwaysApply: true`
  - pass MCP bridge servers through a temporary/restored `.cursor/mcp.json`
  - pass project permissions through a temporary/restored `.cursor/cli.json`
  - pass `--workspace <dir>` and keep process cwd aligned with the MCP bridge
    shell cwd
  - never concatenate system/developer instructions into the pasted user
    message
## Input Contract

User input must be pasted into the TUI. It must not be typed key-by-key.

Required tmux sequence:

```text
tmux load-buffer <payload>
tmux paste-buffer -p -r
tmux send-keys <provider-submit-key>
```

`<provider-submit-key>` is provider/version specific. Claude Code and Codex
commonly accept `C-m`.

The adapter must wait for the TUI to visibly receive the pasted draft before
sending the submit key. Large prompts may collapse to provider-specific draft
markers such as `[Pasted Text: 61 lines]`; these markers are still unsubmitted
input, not idle/ready state.

Submit retries must be state-based, not blind:

- retry only while the draft is still visible and no active generation/tool
  state is visible
- cap retry attempts and return a clear error with the latest pane if the draft
  remains unsubmitted
- never let an unsubmitted draft run until the full model-call timeout

Before every backend-controlled paste, the adapter must inspect the ready input
prompt for an existing draft. If a non-empty real draft is present, it is
untrusted stale TUI state and must be cleared before the new message is pasted.
The provider must never append a new backend turn onto an untracked draft or
submit that draft instead of the new turn. Provider suggestion placeholders must
be distinguished from real drafts; for example Claude Code may render suggested
text after `❯` using a non-breaking-space separator, and that placeholder must
not be treated as a user-typed message.

The paste path must preserve:

- multiline text
- blank lines
- quotes and markdown
- shell-looking commands
- JSON
- Unicode
- pasted blocks

System/developer instructions must not be concatenated into the pasted user
message. They must use the provider's native instruction mechanism.

## MCP Bridge Contract

The MCP bridge is the intended tool surface for policy-controlled runs.

The provider TUI may show provider-native progress, but actual app/workflow
tools must route through the bridge when bridge-only policy is active.

Required behavior:

- Pass MCP bridge configuration at launch.
- Disable or deny internal filesystem/shell/browser tools unless explicitly
  allowed by the caller.
- Surface bridge tool calls as stream chunks or tool-call events when possible.
- Do not parse pane redraws into live assistant-content chunks during
  generation. Terminal snapshots are the generation stream.

## Streaming Contract

The adapter may stream the provider TUI as a live terminal snapshot while a turn
is generating. This must use a terminal/screen-specific stream chunk, not normal
assistant content. The host UI can replace the previous terminal snapshot with
the latest one and keep it visible after the final response for debugging.

The final response must still come from parsed assistant output, not from the raw
terminal snapshot.

Interactive tmux adapters should not convert live pane snapshots into normal
assistant-content chunks during generation. The final response is parsed only
after the provider turn is complete, and that final parse must not include raw
terminal noise:

- spinner frames
- footer text
- shortcut hints
- repeated "calling tool" status lines
- box borders
- echoed user prompt
- compact/history notices
- tmux focus-events warnings

If no assistant text is available yet, the host application may show either the
terminal snapshot or a compact status such as "Agent is still working", but that
status is not a substitute for final assistant parsing.

The adapter's hard contract is:

- detect when the provider turn is complete
- extract the final assistant text for the unified completion
- if the tmux pane/server disappears, parse any last captured pane before
  failing; then unregister the owned session so later live input/resume attempts
  do not target stale tmux state

Cursor or any future tmux-backed provider must set `HandlesTmuxSessionLoss=true`
in `coding_agent_contract.go` and add an adapter-level test that simulates the
tmux server/pane disappearing during a turn. A contract flag without that
adapter test is not enough for review.

The UI may show the raw terminal snapshot for progress/debugging; adapters must
avoid over-parsing terminal progress into assistant content.

## Terminal Size

Interactive tmux sessions should use an explicit pane size instead of tmux's
detached default. The default is `160x48`, which is closer to the desktop chat
terminal panel and avoids excessive wrapping in streamed snapshots.

Operators may tune this without code changes:

- `CODING_AGENT_TMUX_COLUMNS` controls pane columns. Default `160`.
- `CODING_AGENT_TMUX_ROWS` controls pane rows. Default `48`.

Adapters clamp values to a practical range so broken environment values do not
produce unreadable sessions.

## Login Shell Launch

Interactive tmux sessions start the provider CLI through the user's login shell
by default:

```sh
$SHELL -ilc 'cd "$1" || exit; shift; exec "$@"' coding-agent "$WORKING_DIR" <provider-cli> ...
```

This is required for desktop/DMG launches, where the backend process does not
inherit a Terminal tab's initialized environment. The login-shell launch gives
Claude Code, Codex CLI, Cursor CLI, and Pi CLI the same `PATH`, shims, and exported
values the user normally gets from shell startup files.

Shell resolution order:

1. `CODING_AGENT_LOGIN_SHELL`
2. `SHELL`
3. macOS Directory Services `UserShell`
4. `/etc/passwd`
5. `/bin/zsh`, `/bin/bash`, `/bin/sh`

Supported shell families are POSIX-like shells (`zsh`, `bash`, `sh`, `dash`,
`ksh`) and `fish`. Unsupported or missing shells fall back to direct launch:
`cd "$WORKING_DIR" && exec <provider-cli> ...`.

Set `CODING_AGENT_SHELL_MODE=direct` to disable login-shell launch for
deployments where shell startup files are slow or unsafe for non-human
processes.

## Done Detection

A turn is done when the provider TUI is idle, not when the last text chunk was
seen.

The detector should combine:

- ready prompt visible
- no interrupt footer
- no active thinking/tool/calling progress line in the current captured turn,
  including lines above long tool output or above the visible input footer
- pane content stable for a short window
- provider-specific idle phrase if available

Provider hints:

- Claude Code: idle means a ready prompt is visible and `esc to interrupt` is
  gone.
- Codex CLI: idle means the Codex input prompt/footer is ready and no active
  running status is visible.
- Cursor CLI: idle means the Cursor Agent input prompt/footer is ready and no
  active thinking/running/editing/tool status is visible.

Never inject a final-answer marker into the prompt just to detect completion.

## Final Text Extraction

Final text extraction must use provider-native TUI structure when available:

- Claude Code: prefer the latest assistant block beginning with `⏺`.
- Codex CLI: prefer the assistant block framed by the long horizontal separator
  lines when present; otherwise fall back to the latest clean assistant segment.
- Cursor CLI: prefer the latest clean assistant segment after removing Cursor
  TUI chrome, tool/status lines, echoed user input, and old assistant replay.

The extracted final text must not include tool panels, shell output, footer
chrome, ready prompts, old assistant replay, or echoed user input.

## Live Follow-Up Input

If the user sends a message while a coding agent is still working, the runtime
must first try to send it to the registered tmux session.

Required behavior:

1. Look up `app_session_id -> tmux_session_name`.
2. Deliver the live message to the existing provider session or its adapter-level
   pending queue.
3. Do not start a duplicate provider run for the same app session.
4. Fall back to the generic steer queue only when no live tmux session exists.

Provider-specific behavior:

- Claude Code and Codex CLI may accept live input directly in the TUI.

## Cancellation Contract

Cancellation must interrupt the TUI before cleanup.

Required behavior:

- Foreground cancellation sends the provider's interrupt key to the active tmux
  session.
- The adapter waits briefly for idle or process exit.
- Adapter-owned per-turn sessions are cleaned up after the turn exits.
- Bounded per-turn sessions are retained for an inspection window after
  completion before tmux is killed. The product default is 30 minutes, and the
  completion metadata must expose this as `terminal_retention_seconds` so the UI
  can show a `closing` / `closes in ...` state.
- Persistent chat sessions are cleaned up only when the owner session is closed,
  the launch settings change, the idle timeout fires, or the server shuts down.
- Workflow-step lifecycle is explicit. Default is `close_on_completion`; an
  individual step can request `keep_alive`, but that step then owns the extra
  tmux lifetime and must still be cleaned up by idle timeout, explicit close,
  lost-session handling, or server shutdown.

Cancellation for one app session must not kill a tmux session owned by a
different app session or a background agent.

## Multi-Turn Contract

Persistent interactive chat:

1. Start or reuse the tmux session for the app session id.
2. Launch the provider CLI with native system/MCP configuration.
3. Paste the current user message.
4. Stream assistant text and tool progress.
5. Detect idle.
6. Keep the session alive for follow-up input.
7. Reset idle cleanup after each completed turn.
8. On idle timeout, exit/interrupt gracefully, kill tmux, and unregister the
   session.

Per-turn/native resume flow:

1. Start a bounded provider invocation.
2. Send the latest user message.
3. Wait for completion.
4. Capture native session/thread/project ids.
5. Store provider-specific resume metadata.
6. Close the bounded invocation.
7. Resume the next turn with the latest user message plus native resume id.

Native resume metadata:

- Claude Code: `claude_code_session_id`, resumed with `--resume <uuid>`.
- Codex CLI: `codex_thread_id`, resumed with `codex exec resume`.
- Cursor CLI: `cursor_session_id` when available from Cursor-native session
  state, resumed with `cursor-agent --resume <chatId>` from the same workspace.
On native resume, prefer sending only the latest user message when the provider
session/thread is proven to retain context. If a provider does not reliably
retain context in the current CLI build, the adapter must replay a bounded
prior-turn transcript while still reusing the same tmux/native session.

## Idle Cleanup

Persistent tmux sessions must not live forever.

Required behavior:

- Start an idle timer after each completed turn.
- Reset the timer when new input is accepted.
- On timeout, gracefully ask the TUI to exit when possible.
- Kill the tmux session if graceful exit does not complete.
- Remove the session from the live registry.

Provider-specific timeout env vars may exist for tests, but the product
contract is the same across providers.

Bounded per-turn tmux sessions use a different retention timer:

- Default retention is 30 minutes after a successful turn completes.
- During this retention window the terminal is view-only and should be reported
  as inactive with `state=closing`, `closes_at`, and
  `terminal_retention_seconds`.
- If a follow-up turn reuses the same owner session before the timer fires, the
  adapter must cancel the retention timer and continue in the same tmux session.
- After the retention timer fires, kill tmux and unregister the terminal
  registry entry.

## Testing Contract

Default tests must be deterministic and credit-free for pure parser and UI
normalization logic only. They should not install replacement provider binaries
or replacement `tmux` binaries.

Parser/unit tests are necessary but not sufficient. A coding provider is not
accepted as contract-compliant until it has real provider CLI E2E for the risky
transport behaviors. Fake pane fixtures can prevent regressions after a bug is
understood, but they cannot certify launch, prompt paste, MCP startup, native
queueing, cancellation, auth/trust prompts, or provider-version TUI changes.

### Required Real E2E Certification Matrix

The executable certification registry lives in
`coding_agent_certification.go`. The markdown table below defines the expected
behavior; the registry maps provider capability claims to concrete test
functions. Normal unit tests fail if `claude-code` or `codex-cli` claims a
capability in `coding_agent_contract.go` without a registered certification, or
if the registered certification points at a missing test function.

Every active tmux provider has an explicit certification priority. P0 is a hard,
non-waivable release gate for `claude-code`, `codex-cli`, `cursor-cli`,
`muse-cli`, and `pi-cli`. `knownCertificationGaps` may track P1 work, but it cannot suppress a
missing P0 proof. The fast CI lane verifies the registry on every change; the
authenticated lane runs the actual provider CLIs and the AgentWorks workflow
advance test.

The current P0 set is intentionally limited to product survival requirements:

- fresh launch
- runtime context (system prompt, attached skill, and MCP bridge in one real run)
- exact working directory
- workspace trust/auth startup (including the post-accept transition before the
  first prompt)
- MCP bridge availability
- slow-tool false-idle protection
- done detection
- final response extraction
- confirmed live follow-up input
- follow-up input submitted while the agent is processing is handled by that
  same live session after its in-flight work
- cancellation
- stop-then-new-message on the same persistent owner, including recovery from
  an untracked tmux session name left by a canceled launch or server restart
- parallel isolation

The application-level P0 also proves that live input reaches an active coding
agent exactly once without starting a second query, rapid duplicate UI submits
coalesce into one request, and the turn-completion boundary chooses either live
delivery or a queued next turn but never both. Live-input acknowledgement must
not wait behind the active turn's session lane. Finally, after the real CLI
becomes idle AgentWorks must persist the first step and start a second dependent
step. These catch regressions where chat remains stuck on `Sending`, duplicates
a turn, or the terminal visibly says complete while the workflow remains
`running`.

Run P0 from the `coding-agent-loop` checkout. P0 is always authenticated and
live: every selected provider launches its real CLI, uses the MCP agent bridge,
and completes the workflow/auto-notification contract. Deterministic unit tests
remain part of normal repository CI but are not called P0.

```bash
./scripts/run-coding-cli-p0.sh

./scripts/run-coding-cli-p0.sh --providers codex-cli
```

The `Coding CLI P0` GitHub workflow keeps deterministic contracts in a separate
ordinary CI job. Its P0 job has no fast/mock mode: it can be dispatched manually
and runs nightly on the authenticated self-hosted macOS runner. The live runner
derives its test selector from the certification registry, fails when a required
test skips, and therefore cannot silently omit a newly added P0 proof.

Every tmux coding provider must have opt-in real E2E tests for:

| Area | Required proof |
| --- | --- |
| Fresh launch | Starts the real CLI in tmux, reaches ready, and reports clear errors for missing auth/binary/version. |
| Resume/compaction startup | A resumed persistent chat may enter provider-native compaction before the input prompt returns. Prompt-ready waits must use the global `CODING_SDK_TMUX_PROMPT_WAIT_SECONDS` setting, with provider-specific env vars only as overrides; a 20s fixed cutoff is not acceptable. |
| Startup terminal visibility | While waiting for the initial or reset prompt, the adapter must stream real tmux pane snapshots so auth/trust/MCP-init/compaction/prompt-timeout failures are visible in the terminal UI. Terminal streaming must not begin only after the user prompt is accepted. |
| Working directory | Provider cwd and MCP bridge shell cwd are the exact caller workspace for chat, workflow chat, workflow steps, sub-agents, and background agents. |
| Trust/auth prompts | Fresh untrusted workspace trust prompt is handled or surfaced deterministically; auth and rate-limit states are not parsed as final answers. |
| Native system prompt | System/developer instruction reaches the provider through native mechanism and is not pasted as user text. |
| Prompt paste | Large multiline user input preserves blank lines, JSON, shell-looking text, markdown, quotes, and Unicode. Large collapsed paste markers such as `[Pasted Text: N lines]` must submit successfully. |
| Bridge tool call | Real MCP bridge tool is callable from the provider TUI. |
| Bridge-only policy | Provider-native shell/filesystem/browser tools are disabled or denied when bridge-only mode is active. |
| Slow tool plus live input | While a real MCP tool call is still running, send a follow-up/pre-validation message; the adapter must not complete from queued text. |
| Slow tool false-idle guard | While a real slow MCP tool is still running, captured pane classification remains active even if a prompt-looking input/footer is visible. `GenerateContent` must not return until the tool has finished and the provider is genuinely idle. |
| Done detection | Completion requires idle prompt, no activity anywhere in the current captured turn, no queued input, no modal, and a stable pane. |
| Final extraction | Final text excludes TUI chrome, tool progress, raw tool payloads, queued user text, validation feedback, and stale scrollback. |
| Multi-turn | Turn 2 reuses the same native tmux session and proves memory with a random canary not present in the turn-2 submitted prompt. See also: **Comprehensive multi-turn**. |
| Comprehensive multi-turn | 5+ sequential turns on one persistent session jointly cover sending user messages, completion detection (each turn returns within a bounded time without false idle), final text extraction (Content non-empty + contains expected per-turn token), token tracking (PromptTokens > 0 and CompletionTokens > 0 for each turn), conversation reconstruction (`CodingProviderIntermediateMessages` populated and contains only NEW content beyond the prior turn's tail), and cost ledger accumulation (one entry per turn; total tokens roll up correctly). See "Comprehensive Multi-Turn E2E" below. |
| Stale draft cleanup | Seed an idle provider prompt with an untracked draft such as `go with option B`, then submit a different backend turn. The adapter must clear the stale draft, submit only the new turn, and ignore provider suggestion placeholders. |
| Live steer | A message sent while working goes to the same tmux session or adapter pending queue, not a duplicate provider run, and is submitted when the provider returns to an input boundary. The adapter must not leave pasted live input sitting as an unsubmitted draft when the provider TUI is actively thinking. |
| Cancellation | Context cancellation sends the provider interrupt and does not leave a foreground turn falsely completed. |
| Persistent cancel reuse (P0) | Stop a persistent turn while the provider is working, then send a new user message under the same owner and verify it is answered. Reuse a prompt-ready pane or safely replace an untracked/dead one; never return only a duplicate-session error or mistake readiness for a successful follow-up. |
| Lifecycle policy | Chat sessions keep tmux alive by default; workflow steps/sub-agents/background tasks close on completion unless their explicit lifecycle setting is `keep_alive`. |
| Bounded retention | A completed bounded tmux turn remains viewable with `terminal_retention_seconds`, `closes_at`, and `state=closing`, then is killed after the retention window. |
| Parallel isolation | Parallel sessions do not share tmux session names, pending queues, final text, or terminal snapshots. |
| Parallel startup queue | Launch 6-8 real tmux-backed provider sessions at once. The orchestrator may schedule them in parallel, but the adapter must queue the expensive tmux/CLI startup phase according to `CODING_SDK_TMUX_START_CONCURRENCY` and release the slot only after the provider reaches a ready prompt or fails cleanly. |
| Shared-workdir MCP isolation | Parallel sessions that run from the same working directory must still use distinct provider settings/project dirs and distinct MCP bridge session URLs; each real tool call must route to its own session and write only to its own allowed output directory. |
| Cleanup | Idle timeout, explicit close, failed launch, lost tmux pane/server, and server shutdown unregister and kill owned sessions. |

Minimum release gate for any new tmux provider:

1. Real fresh-launch/ready test.
2. Real native system prompt test.
3. Real working-directory test.
4. Real large-paste submit test.
5. Real MCP bridge call test.
6. Real bridge-only/no-internal-tools test.
7. Real same-session multi-turn canary test.
8. Real slow-MCP plus live follow-up test.
9. Real slow-MCP false-idle guard test that asserts the provider call stays
   open while the pane is active.
10. Real cancellation/interrupt test.
11. Real final-extraction hygiene test.
12. Real shared-working-directory MCP isolation test with parallel sub-agents.
13. Real cleanup/session-loss test.

Cursor or any future provider must not be marked as a complete tmux coding
agent in `coding_agent_contract.go` until the provider has these real E2E tests
or an explicit documented exception for a capability it does not claim.

### Comprehensive Multi-Turn E2E

Single-turn coverage misses regressions that only surface across turns
(cumulative sidecar roots, splice deduplication, false-idle on
between-turn TUI animations, choice.Content corruption from multi-turn
panes). Every tmux coding provider must have one opt-in real e2e that
drives 5 or more sequential turns on a single persistent tmux session
and jointly verifies all of the following on each turn:

1. **Sending user messages**: the test appends a new `Human`
   `MessageContent` to a growing local history slice before each
   `GenerateContent` call. Later turns must include reference tokens
   from earlier turns in their text — proves prior messages reached
   the CLI.
2. **Completion detection**: each call returns inside a bounded wall
   clock (default 90 s). A turn exceeding the bound is a regression
   against the wait-loop / settle-window contract — for example
   stale `Running.../Thinking...` status lines must not keep
   resetting the idle timer once the input prompt is visible.
3. **Final text extraction**: `resp.Choices[0].Content` is non-empty
   and (for content-checking turns) contains the expected token.
   `choice.Content` for a tmux pane that has multiple turns visible
   must not bleed prior turns' input into the latest answer.
4. **Token tracking**: `GenerationInfo.PromptTokens > 0` and
   `CompletionTokens > 0` per turn (`resp.Usage` is an acceptable
   fallback). Char-based estimation is allowed for subscription
   providers (e.g. cursor) but must still produce positive counts.
5. **Conversation reconstruction**: `CodingProviderIntermediateMessages`
   is populated on every turn (best-effort for cursor's async commit;
   document the gap if a fast turn legitimately returns empty), and
   each later turn's `Messages` slice contains only NEW content —
   not the cumulative history of all prior turns. The last spliced
   AI message must reference the current turn's user input.
6. **Cost ledger accumulation**: after N turns the global ledger has
   exactly N entries for this owner session, prompt/completion token
   sums match the per-turn totals, and `TotalCostUSD` is greater than
   zero for metered providers (subscription providers may stay at
   zero — the test must document why).

Reference implementation:
`mcp-agent-builder-go/agent_go/cmd/server/multi_turn_chat_e2e_real_test.go`
— the `runMultiTurnChatE2E` helper plus
`TestMultiTurnChatE2E_{ClaudeCode,Codex,Cursor}` entry points, gated
on the same env vars as the per-provider single-turn cost e2es.

When the canonical doc lists a new tmux provider, add a matching
`TestMultiTurnChatE2E_<Provider>` function to that file before
flipping the contract row to `production-ready`.

### P1/P2 Hardening E2E Matrix

The certification matrix above is the minimum P0 release gate. It is not
exhaustive. Providers that are used in production workflows should also have
hardening coverage for provider upgrades, long-running workflows, app-level
routing, and optional multimodal/tool capabilities.

P1 hardening tests should run before provider upgrades, release candidates, or
large workflow-runtime changes:

| Area | Required proof |
| --- | --- |
| CLI version upgrade | Record provider CLI version in test logs and rerun real tmux contract after any CLI upgrade. |
| Model selector semantics | `auto`, no-model sentinel, low/medium/high aliases, and concrete model ids map to the intended CLI flags. |
| Auth source behavior | API key, OAuth/keychain, and login-shell inherited auth work according to provider capability; missing auth fails clearly. |
| Login shell env | Tmux launch inherits the same PATH/shims/env needed for tools such as `aws`, `gh`, `sentry-cli`, `node`, `python`, and provider CLIs. |
| Terminal stream quality | Terminal snapshots are deduped, readable, not over-repeated, and do not emit assistant-content chunks during generation. |
| Terminal pane aliases | Real app-level terminal API collapses multiple logical owner ids for the same tmux pane using `tmux_session`, including while the turn is still running. |
| Provider status vocabulary drift | Busy/idle detection remains correct when the provider changes spinner glyphs or progress verbs; parser fixtures must include real panes from recent CLI versions after any observed drift. |
| UI terminal persistence | User-controlled terminal hide/show state remains stable across streaming, completion, refresh, and provider changes. |
| Background agents | Background agents get their own tmux session, stream terminal/progress events, and are not killed by unrelated foreground request cancellation. |
| Workflow sub-agents | Parallel todo/sub-agent sessions each expose the correct terminal/progress stream and do not duplicate parent terminal output. |
| Query running step | Query/log APIs return live progress for running steps, including execution id lookup by step id where supported. |
| Query completed step | Query/log APIs return completed step logs and final output without depending on live tmux state. |
| Backend restart/shutdown | Server Ctrl+C/shutdown cleans owned foreground sessions and preserves or cancels background sessions according to lifecycle policy. |
| External tmux loss | Killing tmux server/session/pane externally produces a classified lost-session error and unregisters stale mappings. |
| Rate-limit/modal handling | Provider rate-limit, trust, permission, and model-switch prompts are handled or surfaced without being parsed as assistant text. |
| Long workflow | A long workflow with multiple sequential steps and parallel sub-agents completes without stale terminal replay or session collision. |
| Event de-duplication | Replayed/polled events do not create duplicate terminal blocks, duplicate step-start messages, or duplicate final completions. |
| Large scrollback | Final extraction works when pane scrollback contains old turns, long tables, tool JSON, and compact/history notices. |
| App-level chat API | Real `mcp-agent-builder-go` chat API preserves provider/model, selected folder, owner session id, terminal stream, live steer, and final response. |
| App-level workflow API | Real workflow run/step APIs preserve working directory, step/session identity, terminals, query-step logs, and final step result. |

P2 hardening tests cover optional capabilities and provider-specific extended
behavior. They are required before advertising a capability through the shared
workspace tools or model metadata:

| Area | Required proof |
| --- | --- |
| Native web search | Provider search path produces a real tool-call/search event or a verifiable native search trace, not only a memory answer. |
| Read image | Supported providers read a real local image path and answer content-specific questions; unsupported providers reject image content clearly. |
| Generate image | Supported providers save non-empty image bytes, detect as `image/*`, and return absolute workspace paths. |
| Read PDF/video/audio | Tools that accept files require absolute workspace paths, enforce guard policy, and return content-specific answers. |
| Generate video/audio/music | Supported providers create non-empty media files, detect correct MIME/container, and return absolute workspace paths. |
| Absolute path contract | Workspace tools that read/write files accept absolute paths consistently and reject unsafe paths outside allowed roots. |
| Provider/model discovery | `list_llm_capabilities(..., include_models=true)` exposes provider and model choices for every LLM-backed workspace tool. |
| Cost/usage metadata | If a provider cannot provide reliable token/cost breakdown, usage is explicitly zero/unknown and not guessed. |
| Native resume | Providers claiming native resume survive close/reopen and recall a canary without app-level history replay. |
| Multi-window/browser auth | Browser or desktop-auth dependent tools work when launched from app/DMG environment, not only a developer terminal. |
| K8s/Docker runtime | Tmux-backed providers either run in container/pod environments or fail with a clear unsupported-environment error. |

P1/P2 failures should not automatically block every local development run, but
they should block releases that touch coding-agent transport, provider model
selection, workspace tool routing, event streaming, or workflow orchestration.

Transport behavior must be validated by opt-in real CLI E2E tests, including:

- launch arguments, including model, approval mode, workspace, and provider
  config paths; the provider CLI cwd must match the MCP bridge shell cwd for
  the same app/session id
- system/developer prompt routing through the provider-native mechanism, never
  by concatenating those instructions into the pasted user message
- MCP config routing
- bridge-only tool policy routing, including denial/disablement of built-in
  filesystem/shell tools when required
- paste semantics for multiline text, blank lines, JSON, shell-looking text,
  quotes, markdown, unicode, and pasted blocks
- live input routing while the provider is active
- done detection from provider idle state
- cancel/interrupt behavior
- idle cleanup and server-shutdown cleanup
- no duplicate session creation for live follow-up input
- settings-change behavior: model, MCP config, tool policy, approval mode, or
  workspace root changes must close/recreate the tmux session; normal per-turn
  app system prompt variation must not close the session

Deterministic stream parsing tests must cover:

- provider startup banners are filtered
- shortcut/footer/status text is filtered
- spinner/progress frames are filtered or de-duplicated
- tool progress is de-duplicated
- raw MCP tool panels are not emitted as assistant content
- raw JSON tool results such as `stdout`, `stderr`, `exit_code`, and timing
  fields are not emitted as assistant content
- provider policy/admin warnings are filtered from user-visible assistant text
- echoed user prompts are filtered
- old assistant text still visible in the pane is not replayed on the next turn
- old assistant suffixes still visible in the pane are not replayed on the next
  turn
- final response parsing produces the user-visible completion without
  duplicating text from earlier turns
- terminal snapshot chunks are emitted as terminal/screen chunks, not assistant
  content chunks

Deterministic multi-turn tests must cover:

- turn 1 and turn 2 reuse the same tmux session for the same app session id
- providers that declare native context-only behavior send only the latest user
  message to the TUI on turn 2
- app-level E2E always validates the parsed final completion, not the raw event
  stream, so the token in the original prompt cannot create a false pass
- native-session memory, when claimed by a provider, is proven with a canary
  that cannot be satisfied by app-level history replay:
  - turn 1 says: `Take note of the word <TOKEN>. Do not save it to memory.`
  - turn 2 asks only: `What exact word did I ask you to take note of?`
  - turn 2's submitted prompt must not include turn 1 user text or assistant
    text
  - the returned answer must contain `<TOKEN>` and the provider session must be
    the same native tmux session
- prior assistant text from turn 1 is not pasted back as user input
- prior assistant text from turn 1 is not streamed or returned as part of turn 2
- stream deltas remain correct when the terminal redraw includes both old and
  new text
- a live follow-up sent during an active turn is pasted into the same tmux
  session and does not start a duplicate provider run

Deterministic resume tests must cover:

- per-turn mode captures the provider-native resume/thread/session metadata
- resume mode starts from the recorded provider metadata
- resume sends only the latest user message
- resume uses the same project/workspace directory when the provider requires
  it
- persistent tmux mode and per-turn native resume mode do not mix metadata
  incorrectly
- external tmux loss is recovered through the public continuation API:
  first call starts a persistent tmux session, captures the provider-native
  handle, kills the tmux session, then `ContinueCodingAgentSession` starts a
  fresh tmux process with the same native resume id and recalls a random token
  without app-level history replay

Provider-contract validation must include real provider E2E. These tests stay
environment-gated so normal CI does not spend credits accidentally, but release
and transport-change validation should run them alongside deterministic tests:

- Claude Code: use Haiku unless explicitly testing another model.
- Codex CLI: use the cheaper contract model, currently
  `gpt-5.4-mini`, unless explicitly testing another model.
- Cursor CLI: use the account/default selector (`cursor-cli`) for the default
  smoke unless explicitly testing a model available in that Cursor account.

Application-level chat E2E is required in addition to provider-adapter E2E.
The app-level test must drive the real `mcp-agent-builder-go` HTTP API because
that is where runtime capture, provider selection, session restoration, event
polling, and live `/steer` routing are wired together:

Run the application-level test with the provider and model being certified.

The app-level E2E must fail if:

- `/api/query` silently falls back to a provider other than the requested
  provider/model
- turn 2 cannot recall a random token from turn 1 using the same app session id
- the live `/api/sessions/{session_id}/steer` message is only visible in the
  TUI draft but is never processed by the provider
- terminal/event polling completes without a parseable unified completion

Real tests should cover:

- one normal turn
- multi-turn in the same persistent tmux session
- live follow-up while the agent is active
- cancel/escape while the agent is active
- MCP bridge tool call
- resume after close
- large multiline user input
- large builder-style system prompt
- bridge-only policy with internal filesystem/shell tools disabled or denied
- useful assistant text streams before final completion when the provider emits
  it
- no old assistant text replay in streaming or final content across turns
- no raw TUI chrome, policy warnings, MCP panels, or JSON tool payloads in
  user-visible streaming or final content
- native web search, when the provider exposes `SearchWeb()`, including a
  streamed native tool-call event rather than only a final answer that could
  come from model memory

At least one real E2E per provider must combine the risky path in a single
scenario:

1. Start persistent tmux chat with a large builder-style system prompt.
2. Launch with MCP bridge config and bridge-only tool policy.
3. Send a large multiline first user message that requires one bridge tool call.
4. Assert useful stream chunks are clean and non-duplicated.
5. Wait for idle and assert final content is clean.
6. Send a second user message in the same tmux session.
7. Assert the second turn does not replay any first-turn assistant text.
8. Send live follow-up input while the provider is active.
9. Send cancel/escape while the provider is active.
10. Close or idle-clean the session, then resume when that provider supports
    native resume.

JSON/structured transports that remain available for legacy fallback or direct
provider regression tests must run equivalent stream hygiene tests for their
structured event path. Fixes for tmux parsing must not leave
`stream-json`/`--json` mode leaking policy warnings, tool payloads, or duplicated
assistant text.

### Current Real Tmux E2E Inventory

Claude Code tmux:

- `TestClaudeCodeTmuxIntegrationNoInternalTools`
- `TestClaudeCodeTmuxIntegrationNativeSystemPrompt`
- `TestClaudeCodeTmuxIntegrationFreshPromptCarriesUserText`
- `TestClaudeCodeTmuxIntegrationLargePastedPromptSubmits`
- `TestClaudeCodeTmuxIntegrationNativeResume`
- `TestClaudeCodeTmuxIntegrationHaikuExtendedResumeIsolation`
- `TestClaudeCodeTmuxIntegrationHaikuLiveInputAndEscape`
- `TestClaudeCodeTmuxIntegrationHaikuPersistentInteractiveMultiTurn`

Codex CLI tmux:

- `TestCodexCLIRealInteractiveTmuxFullContract`
- `TestCodexCLIRealInteractiveMCPBridgeContract`
- `TestCodexCLIRealInteractiveWorkspaceTrustPromptContract`
- `TestCodexCLIRealInteractiveQueuedValidationDoesNotCompleteDuringMCPTool`
- `TestCodexCLIRealInteractiveLiveInputAndEscapeContract`

Cursor CLI tmux:

- `TestCursorCLIRealInteractiveTmuxFullContract`
- `TestCursorCLIRealInteractiveLiveInputAndEscapeContract`

Kimi:

- No tmux coding-agent contract. `kimi-code` is intentionally removed; use Pi
  CLI or Antigravity CLI for new coding-agent workflows.

Known certification gaps:

- Codex CLI should get a dedicated `LargePastedPromptSubmits` test, even though
  its full contract already covers multiline paste and same-session context.
Cursor CLI MCP bridge notes:

- Cursor CLI's MCP tool exposure in TUI mode requires per-workspace pre-approval
  via `cursor-agent mcp enable <server>` AND a non-interactive `cursor-agent
  --print --trust` pre-warm before the TUI launch. Without the pre-warm, the
  workspace trust dialog dismissal races the MCP tools/list response and the
  model launches with an empty MCP tool list (see
  `preApproveCursorMCP` / `primeCursorWorkspaceForMCP` in
  `cursorcli_real_contract_test.go`).
- In Cursor TUI `--mode ask`, the model refuses to invoke MCP tools that look
  non-read-only (descriptions or parameter names that suggest delays, writes,
  computation). Contract MCP servers must therefore advertise tools as
  read-only and avoid exposing user-tunable delays as parameters.
- `cursor-agent` resolves the workspace path through symlinks (`/var/folders`
  → `/private/var/folders` on macOS) and treats the two forms as distinct
  projects in `~/.cursor/projects/<hashed-path>/mcp-approvals.json`; pre-
  approval must be issued from both forms when the workspace lives under a
  symlinked temp dir.

The full Cursor CLI tmux test set after these additions:

- `TestCursorCLIRealInteractiveTmuxFullContract`
- `TestCursorCLIRealInteractiveLiveInputAndEscapeContract`
- `TestCursorCLIRealResponseHasNoTUIChrome`
- `TestCursorCLIRealMultiTurnNoHistoryLeakage`
- `TestCursorCLIRealCompletionDetection`
- `TestCursorCLIRealMCPBridgeToolCall` (built-in shell)
- `TestCursorCLIRealBuiltInReadNotBlockedInAskMode`
- `TestCursorCLIRealBuiltInWriteBlockedInAskMode`
- `TestCursorCLIRealBuiltInShellBlockedInAskMode`
- `TestCursorCLIRealInteractiveQueuedValidationDoesNotCompleteDuringMCPTool` (slow MCP + live input + false-idle)
- `TestCursorCLIRealInteractiveMCPBridgeContractTmux` (custom MCP server)
- `TestCursorCLIRealInteractiveSharedWorkingDirMCPIsolation`
- `TestCursorCLIRealInteractiveParallelIsolation`
- `TestCursorCLIRealInteractiveCleanup`

Current Codex CLI real contract command:

```sh
go test ./pkg/adapters/codexcli -run 'TestCodexCLIRealInteractive|TestCodexCLIInteractiveIntegrationSpark' -v -timeout 6m -args -coding-cli-p0-live
```

Current Claude Code tmux real contract commands:

```sh
go test ./pkg/adapters/claudecode -run 'TestClaudeCodeTmuxIntegration(LargePastedPromptSubmits|NoInternalTools|NativeSystemPrompt|FreshPromptCarriesUserText|NativeResume)' -v -timeout 6m -args -coding-cli-p0-live
go test ./pkg/adapters/claudecode -run 'TestClaudeCodeTmuxIntegrationHaikuLiveInputAndEscape' -v -timeout 6m -args -coding-cli-p0-live
go test ./pkg/adapters/claudecode -run 'TestClaudeCodeTmuxIntegrationHaikuPersistentInteractiveMultiTurn' -v -timeout 6m -args -coding-cli-p0-live
```

Current Cursor CLI real contract command:

```sh
go test ./pkg/adapters/cursorcli -run 'TestCursorCLIRealInteractive' -v -timeout 6m -args -coding-cli-p0-live
```

Current legacy structured transport real contract commands:

```sh
RUN_CODEX_CLI_STREAM_JSON_E2E=1 go test ./pkg/adapters/codexcli -run 'TestCodexCLIRealExecJSON' -v -timeout 6m
```

Current native web-search real contract commands:

```sh
RUN_CODEX_CLI_SEARCH_WEB_E2E=1 go test ./pkg/adapters/codexcli -run 'TestCodexCLIRealSearchWeb' -v -timeout 4m
CLAUDE_CODE_ALLOW_LEGACY_PRINT=1 CLAUDE_CODE_TRANSPORT=print RUN_CLAUDE_CODE_SEARCH_WEB_E2E=1 go test ./pkg/adapters/claudecode -run 'TestClaudeCodeRealSearchWeb' -v -timeout 4m
RUN_CLAUDE_CODE_PRINT_INTEGRATION=1 go test ./pkg/adapters/claudecode -run 'TestClaudeCodeStreaming|TestRawClaude' -v -timeout 4m
```

Builder/workspace virtual-tool contract command:

```sh
WORKSPACE_API_URL=http://127.0.0.1:8081 go run . test search-web-llm-providers \
  --providers codex-cli \
  --models codex-cli=gpt-5.4-mini \
  --provider-timeout 3m
```

Provider/model semantics:

- `auto` is a real CLI model selector and must be passed through as `auto`.
  Do not silently rewrite `auto` to `low`, `flash-lite`, or another tier.
- Cursor CLI is the exception: `cursor-cli` and `auto` are adapter sentinels
  that omit `--model`, because Cursor Agent CLI does not expose a documented
  `auto` model flag in the tmux path.
- Low/medium/high tiers are explicit aliases only when the user or test command
  asks for them.
- Search tests must assert a native tool-call path where the adapter exposes
  stream tool-call events. A memory-only final answer is not sufficient.

Current image-input real contract commands:

```sh
CLAUDE_CODE_ALLOW_LEGACY_PRINT=1 CLAUDE_CODE_TRANSPORT=print RUN_CLAUDE_CODE_IMAGE_E2E=1 go test ./pkg/adapters/claudecode -run 'TestClaudeCodePrintRealImageInput' -v -timeout 4m
RUN_CODEX_CLI_IMAGE_E2E=1 go test ./pkg/adapters/codexcli -run 'TestCodexCLIRealImageInput' -v -timeout 4m
```

Cursor CLI currently has negative image-input contract tests so
unsupported image parts fail clearly instead of being removed from the prompt.

Builder/workspace read-image virtual-tool contract command:

```sh
WORKSPACE_API_URL=http://127.0.0.1:8081 go run . test read-image-providers \
  --providers vertex,codex-cli,claude-code \
  --models vertex=gemini-3-pro-preview,codex-cli=gpt-5.4-mini,claude-code=claude-code \
  --image-path _users/default/Chats/misc-topic/google.png \
  --expect-any google \
  --provider-timeout 3m
```

Read-image provider/model semantics:

- Claude Code image input is supported only on the `print`/`-p` path today.
  Tmux chat must reject image parts until a reliable TUI attachment transport is
  implemented.
- Codex CLI image input must use an image-capable model, currently
  `gpt-5.4-mini` for the contract test. (`gpt-5.3-codex-spark` is not offered any more: OpenAI refuses it for Codex with a ChatGPT account.)

Current image-generation real contract commands:

```sh
go run ./cmd/llm-test codex-cli-image-generate \
  --model codex-cli \
  --prompt "A simple red square icon centered on a white background" \
  --aspect-ratio 1:1 \
  --num-images 1 \
  --output-dir /tmp/mlp-codex-image-gen

```

Builder/workspace image-generation virtual-tool contract command:

```sh
WORKSPACE_API_URL=http://127.0.0.1:8081 go run . test image-gen-providers \
  --providers vertex,codex-cli \
  --models vertex=gemini-3.1-flash-image,codex-cli=codex-cli \
  --provider-timeout 4m
```

Image-generation provider/model semantics:

- Only Vertex and Codex CLI are required for the current real contract.
- For Codex CLI image generation, `codex-cli` is the no-model sentinel. The
  adapter must not pass `--model` unless a concrete Codex model was explicitly
  requested.
- Image-generation test commands must fail on missing auth, provider errors,
  zero returned images, empty image bytes, or saved bytes that do not detect as
  `image/*`.
- The builder command must verify that `image_gen` saves the generated image
  back to the workspace and that the saved file downloads as `image/*`.
- Codex CLI image generation should use `gpt-5.4-mini` for the contract test.
  Spark can be tested separately, but it is not the release gate for image
  generation or image understanding.

## Related Docs

- `docs/CODING_AGENT_TRANSPORT_PATTERNS.md`
- `docs/CODEX_CLI_CODING_AGENT_CONTRACT.md`

## Pi native RPC (PLAT-872-20)

Pi uses `pi --mode rpc` for both retained chats and bounded work. Its provider
contract reports `rpc`; terminal frames and tmux controls do not apply.
`prompt` starts a turn, `steer` adds input during work, and interruption clears
the native queue before `abort`. Only `agent_settled` completes a run. Native
message/tool events supply deltas, results and usage; saved sessions supply
resume and durable user-input proof. Explicit provider keys take precedence over
saved logins, and a saved login comes from the selected account environment.

The same Pi harness supplies MCP tools, skills, native tools and compaction.
Existing confinement and account selection still apply. Terminal-only extension
UI is unavailable; extension dialogs are cancelled with a chat notice. User
questions use the product's MCP question tool. There is no tmux or print fallback.
