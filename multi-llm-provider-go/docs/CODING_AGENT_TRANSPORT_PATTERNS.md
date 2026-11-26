# Coding Agent Transport Patterns

This is the shared pattern set for terminal-native coding agents such as Claude
Code, Codex CLI, Cursor CLI, and Pi CLI.

The runtime capability registry is `coding_agent_contract.go`. Do not add a new
coding provider by copying string switches only; add the provider contract first,
then wire the provider-specific launch/input/extraction functions that the
contract requires.

## 0. The four ways to drive a coding agent, and which we support

There are exactly four viable shapes here. Two are supported, one is supported
but not recommended for interactive use, and one is deliberately out of scope.
The differences are not stylistic — each forfeits something specific.

| | live mid-turn steer | per-turn process cost | final-reply fidelity | tool calls visible | tokens / cost |
|---|---|---|---|---|---|
| **1. tmux, pane-only** | yes | one warm process | **wrapped** (broken) | pane scraping only | not available |
| **2. tmux + sidecar transcript** ← **default** | yes | one warm process | clean | structured | from sidecar¹ |
| **3. structured / JSON** | **no — queues** | **new process every turn** | clean | structured | structured |
| **4. direct provider API** | n/a | n/a | clean | native | native |

¹ except Cursor — see the caveat below.

**1. tmux, pane-only.** Drive the CLI's TUI in tmux and read everything off the
pane. Live steering works (there is a real stdin), and one warm process serves
every turn. But a terminal hard-wraps at its width, and a soft wrap is
indistinguishable from a newline the model typed — so markdown structure in the
final reply cannot be recovered. Do not heuristically "dewrap"; it corrupts
intentional structure (lists worst of all). This is the historical shape and the
source of `CertReplyFormattingFidelity`.

**2. tmux + sidecar transcript — the default, and what to build on.** Same tmux
session, but split the two jobs the pane was doing badly at once: the pane
answers *"has the turn finished?"* (it is genuinely good at idle detection — see
pattern 6), and the CLI's own on-disk session file answers *"what did it say?"*.
Every supported provider writes such a file, three natively (it is what powers
their own `--resume`) and Pi via a hook this repo injects:

| provider | sidecar | notes |
|---|---|---|
| claude-code | `~/.claude/projects/<cwd-slug>/<sessionID>.jsonl` | prunes at 30 days; no system prompt; reasoning text emptied |
| codex-cli | `~/.codex/sessions/<Y>/<M>/<D>/rollout-*.jsonl` | never prunes; richest metadata (rate limits, plan, credits) |
| cursor-cli | `~/.cursor/chats/<md5(cwd)>/<agentId>/store.db` | sqlite; **no token usage, no per-message timestamps** |
| pi-cli | `~/.pi/agent/sessions/<cwd>/<ts>_<id>.jsonl` | only provider storing plaintext reasoning + dollar cost |

None of them are delta-based — all record complete messages, so there is no
reassembly step to get wrong. Reconciliation must still be defensive: these files
are written asynchronously (Cursor commits `store.db` seconds after the pane
settles), so read the sidecar, verify it against the pane by comparing
whitespace-normalized text, and fall back to the pane when it does not match or
has not appeared yet. Trusting the sidecar blindly risks returning a *different
turn's* reply, which is worse than bad wrapping.

**3. structured / JSON.** The CLI's own non-interactive JSON mode
(`codex exec resume <id> --json` and friends). Everything is clean and
structured, but it spawns a **new process per turn** — full CLI startup plus
context reload on every message — and there is no stdin to write into, so
`Deliver` can only QUEUE (`steering_queue.json` certifies exactly this). Correct
for batch/one-shot work; wrong for an interactive session where a user may
interject mid-turn.

**4. direct provider API.** Out of scope for this package. It gives the best
fidelity and control, but forfeits the reason these adapters exist: the user's
own CLI subscription auth, the CLI's native tools, and its session management.
Choosing it means rebuilding the agent loop rather than driving one.

**Cursor caveat.** Cursor's sidecar carries neither token usage nor per-message
timestamps, unlike the other three. Anything depending on per-turn cost or
timing must therefore come from the caller's own recording
(`convrecord.tmux` certifies real token usage) rather than the sidecar, or be
accepted as a gap on that provider.

**Retention is the caller's problem.** codex-cli never prunes (observed: 2.27 GB
over 129 days, plus a `history.jsonl` holding every prompt verbatim);
claude-code prunes at 30 days. All four store **plaintext**, and every shell
command an agent runs is recorded verbatim — so any command that interpolates a
secret rather than passing it via the environment persists that value to disk.

## 1. One Normal Transport

Interactive chat and workflow execution use the same tmux-backed coding-CLI
transport.

- Chat: persistent tmux TUI, with live follow-up input routed into the same
  owner session.
- Workflow: the same tmux TUI transport, with the orchestrator deciding when to
  send intermediate input, wait for idle, extract the final response, and close
  or retain the owner session.
- Legacy structured transports (`claude -p`, `codex exec --json`) are
  fallback/test-only paths unless a
  provider still lacks a tmux implementation.

Do not add product behavior that depends on structured CLI output unless the
tmux contract cannot support that capability yet.

## 2. Owner Session Registry

Long-lived TUI sessions are keyed by the application session id:

```text
app_session_id -> tmux_session_name
```

The app session id is the public routing key. The tmux session name is an
implementation detail and should not leak into UI or workflow terminology.

## 3. One Live TUI Per Owner

For persistent chat, reuse the existing tmux session for that owner id.

- Hold a per-session mutex while a turn is being submitted and parsed.
- Keep the session registered after the turn returns to idle.
- Close it only on explicit cleanup or idle timeout.
- If launch settings change, close the existing session before starting a new
  one.

## 4. Native Instructions, Pasted User Input

System/developer instructions must use the provider's native mechanism. User
input must be pasted into the TUI.

- Claude Code: system prompt file plus tmux paste for the user message.
- Codex CLI: `developer_instructions` config override plus tmux paste for the
  user message.
- Cursor CLI: temporary/restored `.cursor/rules/*.mdc` project rule plus tmux
  paste for the user message.
- Pi CLI: append-system-prompt plus tmux paste for the user message.

Never concatenate system text into the pasted user prompt. This prevents bugs
where the agent sees empty or malformed user input.

## 5. MCP Bridge Is The Tool Surface

Coding agents should call our MCP bridge, not their internal local tools, when
the runtime wants policy-controlled workflow tools.

- Claude Code: pass `--mcp-config`, `--strict-mcp-config`, and disable internal
  tools unless explicitly allowed.
- Codex CLI: pass MCP config overrides and disable `shell_tool` when required.
- Cursor CLI: pass MCP bridge config through temporary/restored
  `.cursor/mcp.json` and project permissions through `.cursor/cli.json`.
- Pi CLI: pass the MCP configuration and restrict built-in tools when required.

This keeps provider-native terminal UX while preserving our bridge and policy
boundaries.

## 6. Done Means Idle, Not Last Text

Terminal output is noisy. A turn is done only when the TUI is idle.

Signals to combine:

- ready prompt visible
- no interrupt footer
- no active thinking/tool progress line
- pane content stable for a short window

Never rely on a final-answer marker injected into the prompt.

## 7. Stream Terminal Snapshots Separately

For interactive chat, providers may emit the full visible tmux pane as a live
terminal snapshot. This is useful for showing native TUI progress without trying
to parse every redraw.

- Emit terminal snapshots as a terminal/screen stream chunk.
- Do not append terminal snapshots to assistant-content chunks.
- Parse the final unified answer separately from the terminal pane.
- Let the host UI replace prior terminal snapshots and keep the last snapshot
  visible after the final answer when useful for debugging.

## 8. Live Input First, Queue Fallback Second

When the user sends a message during an active coding-agent turn:

1. Try to paste it into the registered tmux session.
2. If no session exists, fall back to the normal agent steer queue.

The fallback is for non-coding providers and early/late races. It should not
start a duplicate coding-agent run while the TUI is already active.

## 8. Cancellation Interrupts The TUI

Context cancellation should first interrupt the provider TUI, then clean up
owned sessions when appropriate.

- Foreground request cancellation sends Escape or Ctrl-C to the TUI.
- Server shutdown drains adapter-owned sessions.
- Background agents own their own lifecycle; unrelated cancellation should not
  kill sessions that belong to a different owner.

## 9. Launch Through The User Login Shell

Coding-agent tmux sessions should launch provider CLIs through the user's login
shell by default. This is especially important for DMG/GUI launches where the
backend does not inherit Terminal's already-initialized environment.

- Resolve shell from `CODING_AGENT_LOGIN_SHELL`, then `SHELL`, then OS login
  shell fallbacks.
- Use login + interactive mode for POSIX-like shells, and fish-specific argv
  handling for fish.
- Keep `CODING_AGENT_SHELL_MODE=direct` as the escape hatch when shell startup
  files interfere with automation.

## 10. Idle Cleanup Is Ownership Cleanup

Persistent sessions are not permanent.

- Keep the session alive for follow-up chat.
- Reset the idle timer after every completed turn.
- On idle timeout, exit/interrupt gracefully and kill the tmux session.
- Remove registry entries during cleanup.

## 10. Test Both Contract And Reality

Default tests should be credit-free and deterministic for parser and UI
normalization logic only. They should not install replacement provider binaries
or replacement `tmux` binaries.

Provider-contract validation should include real provider E2E. These tests are
environment-gated so normal CI does not spend credits accidentally:

- Claude Code with Haiku
- Codex CLI with the cheaper contract model, currently `gpt-5.4-mini`
- Pi CLI with the selected low-cost model
- multi-turn memory in the same persistent tmux session

This gives fast CI coverage for the contract and occasional real validation for
provider TUI changes.
