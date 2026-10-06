# Coding CLI turn signals: where each fact comes from

Status: current as of 2026-09-27 (five original CLIs certified; AGY onboarding
tested at the provider boundary). Detailed history:
[PLAT-354](../bugs/pulse_platform/coding-agents/bridge/plat-354.html). Submit
receipts: [durable_ack_p0.md](../refactor/durable_ack_p0.md).

## Rule

**tmux remains the critical interactive transport.** It launches the CLI, types input
(so a user can steer a running turn), carries live stderr to the terminal
panel, and gives the CLI its full interactive harness.

**Every correctness fact comes from the CLI's own structured record**: was
the message taken in, is the turn finished, what is the final answer. That
record is JSONL or SQLite. This is P0. Reading the pane is P1: a fast early
hint, overridden by the record. The only exceptions are screens that have no
record at all (trust/login prompts, a draft stuck in the input box).

Do not fix a delivery or completion bug by parsing more pane text. Find the
structured signal instead.

## Per CLI

| CLI | Record | Taken in | Finished | Final answer |
|---|---|---|---|---|
| Claude Code | transcript JSONL (`~/.claude/projects/…`) | user row for this prompt | `end_turn`, **and** the `turn_duration` row's `pendingBackgroundAgentCount` is 0 | last assistant text of the turn |
| Codex | rollout JSONL | user message in the rollout | `turn.completed` / task complete | last agent message |
| Muse | `session.jsonl` | `runtime.user_intent.accepted` | run `terminal`; with subagents, follow the chain below | assistant message of the final run |
| Cursor | `store.db` (SQLite) | user row for this query | the turn's rows end in assistant prose (a trailing tool call = still running; a trailing tool result = reply pending) | that prose |
| Pi | `markers.jsonl` + session | intake marker | `agent_settled` (fallback: `agent_end` + 15 s quiet) | last assistant message |
| AGY | conversation SQLite (`~/.gemini/antigravity-cli/conversations/<id>.db`) | matching type-14 user step after the pre-submit index | a status-3 type-15 assistant step with text at the end of the turn's trail, stable for 2 s, with the composer ready | type-15 field 20.1 text after that user step |

AGY 1.2.12 has no distinct run-terminal row in the observed conversation
record. Its SQLite trail is the primary intake and answer proof for normal
and retained turns; tmux composer readiness remains a secondary completion
guard so narration before a tool does not end the turn. A trailing tool step
keeps the turn open. Retained progress also reads new assistant SQLite steps
with a cursor, without replaying old text. This contract is covered by the
persistent MCP bridge live test with a 25-second tool.
For live input, the server's second receipt comes from a new exact-match user
step after the pre-send SQLite index; repeated identical sends need distinct
rows. `TestAgyCLIRealDurableAckContract` passed on two retained turns.

## Messages sent during a running turn (steering)

The user can type while a turn runs. Each CLI records it differently:

- **Claude Code:** queue-operation enqueue/remove rows, plus a `queued_command`
  attachment.
- **Cursor:** a later user row inside the same turn. The reader keeps it in the
  same turn, so the answer is the turn's last prose, not a reply to the steer
  alone.
- **Muse, Codex, Pi:** a new intake record for that message.

## Background subagents

With [Native agent tools](../design/native_agent_tools.md) on, a CLI may start
background subagents and write an interim reply ("waiting for the subagents").
That reply is not the answer:

- **Claude Code:** the turn is not done until `pendingBackgroundAgentCount` is 0.
- **Muse:** `spawn_accepted` → `inbox_item_queued` (matched by
  `source_run_record_id`) → the run that drains it → that run's `terminal`. The
  answer comes from the last run. If the log stops growing for 5 minutes, the
  adapter stops waiting.

## Environment gotcha (Claude Code)

A Claude CLI started from inside another Claude session inherits variables
that turn its transcript saving off. With no transcript, no delivery can be
proven. The adapter clears them for both the tmux launch and the structured
command: `CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CODE_SESSION_ID`,
`CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN`,
`CLAUDE_CODE_SESSION_ATTENDED`.

## Certification

Certified versions are in `scripts/p0-certified-cli-versions.json`; the gate
script is `scripts/run-coding-cli-p0.sh`, which needs `MCP_API_TOKEN` and
`WORKSPACE_API_TOKEN`. Run it for one provider at a time
(`--providers <name>`), not all providers, unless asked. Opt-in stress tests
(`-coding-cli-stress`, count set by `CODING_CLI_STRESS_ITERATIONS`) run
parallel subagents, a slow MCP tool and a mid-turn steer. Results on
2026-09-24: Claude 3/3, Codex 3/3, Muse 3/3, Cursor 5/5.

Known, accepted issues (reopen only if they recur or get worse): an
intermittent Claude `StructuredCompletionP0` failure, an occasional Muse IC-11
retained-shell-start timeout, and one unreproduced Pi empty retry after cancel.
