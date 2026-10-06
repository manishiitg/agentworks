[← platform / coding-agent-bridge](index.md)

# PLAT-297 — Make Codex retained sessions, resume and structured event extraction reliable

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | records the completed Codex reliability work across AgentWorks, `mcpagent` and `multi-llm-provider-go`. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented on main — unit suites and live retained-session and two-step workflow P0 checks pass; the repository-wide generated-review metadata gate remains unrelated follow-up` |
| Last synchronized | `2026-09-06` |

- **Priority:** P0 reliability. A successful or failed Codex turn must settle
  once, preserve its mode and workspace identity across resume, and render the
  semantic response and tool activity instead of raw terminal UI text.
- **Owners:** `multi-llm-provider-go` Codex adapter, `mcpagent` retained-session
  release routing, and AgentWorks workflow chat/session bridges and P0 harness.
- **Related:** [PLAT-103](plat-103.md) (retained final response),
  [PLAT-160](plat-160.md) (interactive tool completion),
  [PLAT-178](../chat-reliability/plat-178.md)) (resume history),
  [PLAT-180](plat-180.md) (retained tool turn identity),
  [PLAT-268](../frontend-chat/plat-268.md)) (blank formatted chat), and
  [PLAT-296](../security-sandbox/plat-296.md)) (mode-specific private CLI runtimes).

## Problem

Several failures appeared together after isolated workflow CLI runtimes and a
new Codex CLI release were exercised through restart and resume:

- A resumed Run chat could be reconstructed as Workshop or launched from an
  invalid/non-private working directory.
- A new private runtime could stop at Codex's directory-trust prompt.
- Codex 0.153 changed terminal and rollout event shapes. Interrupted or failed
  turns could return an empty response, and semantic MCP calls nested inside
  `event_msg.item_completed` could be flattened into the outer JavaScript
  `exec` wrapper.
- Raw tmux pane text such as `Called └ ...` and `Working (... esc to
  interrupt)` could leak into a persisted assistant response. That pane is a
  terminal UI, not a stable data format.
- Updating a managed CLI while a retained agent was alive could route later
  turns through a different executable release.
- Workflow-step MCP calls could lose their trusted step-session context, so
  folder guards and the private working directory no longer referred to the
  same execution identity.

The combined effect was confusing: a process could still be visibly alive in
tmux while the application had no safe semantic answer, or a restarted server
could know the durable chat but fail to recreate the CLI with the same mode and
private runtime.

## Implemented contract

1. **Codex rollout JSONL is the semantic source of truth.** Turn boundaries,
   final assistant messages, errors, token usage, MCP tool names, arguments,
   results and durations come from structured rollout events. The parser
   understands Codex 0.153 `event_msg.item_completed` MCP records and preserves
   the inner MCP operation instead of presenting the outer transport wrapper.
2. **tmux is the interactive control channel.** It launches and retains the
   TUI, accepts follow-up input, supports steering/cancellation, detects startup
   prompts and supplies bounded diagnostics. Pane text is never authoritative
   for a completed response or tool result.
3. **Resume preserves session identity.** AgentWorks persists and restores the
   chat's original Builder/Run mode. A continuation must match its provider,
   user, workflow, mode and private absolute runtime directory. An incompatible
   handle is rejected; application history can seed a fresh native conversation
   without silently changing authority.
4. **Private Codex runtimes are trusted using their physical path spelling.**
   This prevents a symlink-versus-physical-path mismatch from reopening the
   interactive trust dialog after AgentWorks has selected the managed runtime.
5. **Managed CLI releases are pinned per retained agent session.** A daily CLI
   update affects newly created sessions. Existing sessions keep the executable
   release they started with until they are replaced.
6. **Failed and interrupted turns settle explicitly.** The adapter returns a
   typed failure instead of an empty successful reply, handles Codex 0.153's
   interrupted-turn state, and cleans up orphan session state without treating
   a stale pane frame as a new completion.
7. **Workflow steps retain trusted execution context.** The controller factory
   carries the step session through the Go context used by tool execution, so
   guarded workspace access and CLI isolation agree on the active step.

## Delivery record

| Repository | Main commits | Delivered behavior |
|---|---|---|
| `multi-llm-provider-go` | `2a2d796`, `cb578ff`, `aab045c`, `a7ba270`, `842b584`, `7276072` | typed failed turns, Codex 0.153 handling, pinned release routing, physical-path trust, semantic MCP rollout parsing and filesystem-identity cwd matching |
| `mcpagent` | `17584c6` | one managed CLI release pinned for the retained agent session |
| `mcp-agent-builder-go` | `fc3f51761`, `635109899`, `3f2971e9a` | mode-stable resume, updated provider/runtime dependencies, trusted workflow-step session propagation and stronger real P0 checks |

All listed commits were pushed to each repository's `main` branch on
2026-09-06.

## Verification

- The full `multi-llm-provider-go` unit suite passes, including semantic Codex
  MCP event extraction, failed-turn handling, trust-path normalization and
  managed-release routing.
- The registered Codex P0 suite passes with the managed Codex binary selected
  by the production launcher.
- Live retained-session IC-11 passes with exactly one semantic tool receipt and
  one final response, without reconstructing either from terminal text.
- The live isolated testing workflow completes a two-step message-sequence P0
  run. Both steps retain the explicit private workspace token and trusted step
  session.
- AgentWorks' P0 runner now calls the correct main-terminal endpoint and pins Go
  1.26 for this live harness. Go 1.27.1 crashed in its runtime heap after the
  Codex turn had completed; `CODING_CLI_P0_GO_TOOLCHAIN` remains the explicit
  override for investigating that separate toolchain problem.
- The broad `mcpagent` suite reaches its existing manual generated-review gate.
  Four pre-existing generated JSON review records lack approval metadata; this
  is not a Codex session failure and is not counted as passing that manual gate.

## 2026-09-06 — Remaining pane-text leak fixed

A resumed Social Media builder turn reproduced the raw-text leak after the
earlier semantic MCP parser shipped. The persisted response contained `Called
└ api-bridge.execute_shell_command(...)`, its JSON `stdout`, and the final prose
as one assistant string.

The Codex rollout for that exact execution was healthy and contained a clean
`final_answer` plus a later `task_complete`. The adapter nevertheless recorded
`codex_final_extraction_source=tmux_pane`. Its expected private runtime used the
configured spelling `Application Support/AgentWorks`, while Codex recorded the
same macOS directory as `Application Support/agentworks`. Canonical string
comparison treated those spellings as different, failed to bind the rollout,
and eventually activated the terminal fallback.

`sameCodexWorkingDir` now compares the underlying filesystem objects with
`os.SameFile` before its normalized string fallback. A portable hard-link test
locks the filesystem-identity behavior, and a pane fixture matching the leaked
`execute_step` command verifies the compatibility sanitizer still excludes the
tool replay. The full Codex adapter package passes. No server or workflow was
started for this verification.

## Acceptance boundary

This ticket is complete for the implemented Codex path when:

- a restarted Run chat resumes as Run and cannot inherit Builder tools;
- a private runtime starts without an unattended trust modal;
- one submitted turn produces one completion and does not continue emitting an
  older step's response;
- the formatted chat contains semantic MCP calls and results, with no raw
  `local-command-stdout`, `Called └`, spinner or tmux status text used as the
  assistant answer;
- an installed CLI update does not change the executable beneath an already
  retained agent; and
- folder-guard decisions use the same workflow-step session as the CLI launch.

Cross-provider parity, private-runtime garbage collection and two-user
authorization belong to PLAT-296 follow-up acceptance rather than this Codex
repair.

## 2026-09-06 — Broader audit and completion-source clarification

The follow-up path/resume audit is recorded in PLAT-296. Codex filesystem
matching and trust spelling now share tested filesystem-identity helpers with
the other providers. Codex's existing trust aliases remain compatible, including
macOS `/var` and `/private/var` forms. The full Codex unit package passes.

The earlier register description that tmux was "the interactive control channel
only" was too strong: the adapter still contains a terminal fallback when the
structured transcript cannot bind. The observed case-mismatch trigger is fixed;
this is not proof that every possible fallback or live restart failure is gone.
No server or live workflow was started in this audit.

## Register notes

[PLAT-297](plat-297.md) records the completed Codex reliability
work across AgentWorks, `mcpagent` and `multi-llm-provider-go`. Codex rollout
JSONL is now authoritative for completion, final responses, failures, usage and
semantic MCP tool events when bound; tmux remains the interactive control
channel and retains a compatibility fallback if transcript binding fails.
Resume preserves Builder/Run mode and the private absolute runtime, physical
path trust prevents unattended startup prompts, filesystem-identity matching
binds JSONL even when macOS changes the directory's case spelling, and each
retained agent pins one managed CLI release. Unit suites plus live retained-session and isolated
two-step workflow P0 checks pass. All implementation commits are on `main`;
The cross-provider path/resume audit and remaining live/two-user acceptance
are tracked by PLAT-296.
