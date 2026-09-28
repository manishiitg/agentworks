# Coding Agent Builder E2E Contract

This file defines the builder-layer contract for coding-agent integrations in
`coding-agent-loop`.

For the current integration file map, P0/P1 enforcement review, and checklist for
adding a CLI, see [Coding CLI onboarding](coding_cli_onboarding_contract_review.md).
That review also records transport-policy documentation drift; the refactor it
proposes is not yet implemented.

The low-level provider/tmux contract lives in:

```text
https://github.com/manishiitg/multi-llm-provider-go/blob/main/docs/coding_sdk_tmux_contract.md
```

That provider contract owns CLI launch details, prompt paste, ready/completion
detection, final text extraction, cancellation, and provider-specific real CLI
tests. This builder contract owns how those provider guarantees are used by the
HTTP API, workflow runtime, event store, MCP bridge, and frontend.

## Scope

The builder must prove these user-facing flows end to end:

- Workflow builder chat.
- Normal chat with a coding provider.
- Workflow step execution.
- Todo-task orchestrators and parallel sub-agents.
- Background agents.
- Live steering while an agent is still running.
- Cancellation from API/client disconnect and explicit stop.
- Terminal center viewing, selection, status, and scroll behavior.
- Chat history resume and event replay.

## Runtime Invariants

1. Chat and workflow shell working directory must match the caller workspace.
   Coding agents and `execute_shell_command` must see the same cwd.
2. Every coding-agent call that uses MCP must receive the correct per-session
   MCP bridge URL/token and must not accidentally use a different session.
3. Workflow steps, workflow-runtime sub-agents (execution, learning, KB,
   conditional, todo orchestrator), and background agents use bounded tmux
   lifecycle for coding-agent providers. Chat defaults to persistent tmux
   lifecycle for coding-agent providers.
4. Bounded tmux terminals must remain viewable for the configured retention
   window, expose `closes_at`, then be cleaned up.
5. Completed terminal snapshots must remain selectable in the UI. Active
   terminal refresh must not steal selection from a manually selected terminal.
6. Terminal lifecycle must be keyed by stable runtime identity, not only by the
   exact event owner string. A start/chunk event may use `workflow-step:<id>`
   while the completion event uses `<id>`; the backend must reconcile the same
   terminal through `tmux_session`, step id, and execution metadata.
7. Terminal refresh must not reset scroll when the user has scrolled away from
   the bottom.
8. Terminal debug IDs must be copyable from the UI without cluttering the normal
   visible terminal view.
9. Unified completion must not duplicate terminal output, tool panels, or stale
   streaming text.
10. Provider completion and terminal UI completion are separate contracts.
    Provider completion is owned by the adapter and must be based on provider
    idle/stable TUI state plus final-response extraction. Terminal UI fallbacks
    such as unchanged pane timers or prompt text like `STATUS: COMPLETED` may
    mark a bounded terminal display inactive, but must not by themselves trigger
    workflow success, validation, learning, next-step execution, or background
    completion notifications.

## Required Builder E2E Matrix

| Area | Required proof |
| --- | --- |
| Chat launch | Start a chat with a coding provider, verify provider/model label, cwd, MCP bridge, and first assistant response. |
| Multi-turn memory | Turn 1 asks the provider to remember a random canary without writing it to disk. Turn 2 asks for it. The second submitted prompt must not contain the canary. |
| Tmux-loss continuation | For tmux providers with native resume, turn 1 captures a canary, the test kills the latest tmux session, and the next chat turn must recover via provider-native continuation and return the canary. Run with `coding-agent-chat-e2e --run-tmux-loss-resume`. |
| Literal prompt text | Send a real chat turn containing a literal social handle such as `@fixyo.urflow`. The provider must treat it as text, not an `@path`, and return the handle without opening a debug console or hanging. |
| Stale prompt draft | Create or simulate an idle coding-agent TUI with an untracked draft already visible, then send a different backend chat turn. The backend/provider adapter must submit only the new turn and must not treat provider suggestion placeholders as user text. |
| Live steer | Send a second user message while the agent is running. It must be routed to the same coding-agent session or provider queue, not create a duplicate run. |
| Cancellation | Stop/cancel must interrupt the coding provider and produce a canceled event, not a false completed response. A canceled persistent coding-agent session must not be reused unless the provider pane is prompt-ready; otherwise the next turn must get a fresh session. |
| Workflow step | Run a code-exec workflow step and verify the coding agent starts in the workflow execution directory and writes the expected output file. |
| Query step | `query_step` by step id must resolve the active/latest execution id and return live progress for running steps plus stored logs for completed steps. |
| Todo orchestrator | Run a todo-task that launches parallel sub-agents. Each child must have distinct execution id, terminal id, MCP session, and output directory. |
| Background agent | Start a background agent and verify terminal/event visibility while it runs and completion notification when done. |
| Terminal center | Active, completed, failed, and closing terminals must show correct state, title/meta, debug copy, and selected terminal content. |
| Terminal lifecycle API | Through backend handlers, create a terminal from a `streaming_chunk`, close it from a `streaming_end`, and verify `/api/terminals` changes active/running to inactive/closing. This must pass even when the end event owner is shorter/different from the chunk owner but has the same `tmux_session` or step metadata. |
| Terminal dismiss API | A completed/closing terminal must be dismissible through the backend API and disappear from subsequent `/api/terminals` responses without requiring frontend state reset. |
| Terminal scroll | While terminal content refreshes, manually scrolling up must preserve scroll position. If already at bottom, output should auto-follow. |
| Bounded retention | Completed step/sub-agent/background terminals must show `state=closing` and `closes_at`, remain viewable during retention, then disappear/close after cleanup. |
| Resume/history | Refresh or reopen the chat and verify events, completion, and terminal records restore consistently without mixing sessions. |
| UI formatting | Markdown/unified completion and terminal text must render in their separate surfaces without table/list collapse or terminal text leakage. |

## P1/P2 Hardening Matrix

P1 cases block release for any coding-provider or terminal-runtime change:

| P1 case | Required proof |
| --- | --- |
| Live steer same session | While the provider is thinking, send a second user message. The message must reach the active provider session/queue and must not remain as frontend-only text. |
| Idle draft submission | If a provider pane shows a pasted draft, the adapter must submit it reliably and prove the pane transitions to processing or prompt-ready after completion. |
| Completion detection | A provider returning to an idle prompt must close the active generation, emit a final assistant response when one exists, and mark the terminal inactive/closing. |
| Final response extraction | With tool panels, old turns, prompt echoes, queued drafts, and retry feedback present in the terminal pane, the returned unified completion must contain only the newest assistant answer. Run `coding-agent-chat-e2e --vertex-final-judge` to have a Gemini/Vertex judge validate app-level `unified_completion.final_result` against terminal snapshots and event JSON. |
| Terminal owner reconciliation | Start/chunk/end event owner IDs may differ. Backend tests must prove terminal state closes by `tmux_session` or step metadata instead of creating or leaving stale active panels. |
| Cancellation | Client cancel, API stop, and server shutdown must interrupt/close bounded provider sessions and must not reuse poisoned panes. |
| Workflow step cwd/MCP | A real code-exec step must run in the workflow execution directory and call the session-scoped MCP bridge. |
| Parallel agents | Parallel todo sub-agents must have unique execution ids, terminals, MCP sessions, and output directories, with no terminal duplication for one tmux pane. |

P2 cases are required before broad rollout or when touching related UI/runtime
code:

| P2 case | Required proof |
| --- | --- |
| Terminal scroll/selection | Refreshing terminal snapshots must preserve manual scroll and selected terminal. |
| Terminal retention UI | Completed/failed terminals must expose closing status, debug info, and dismiss controls until retention cleanup. |
| Unified completion formatting | Markdown completion must preserve lists/tables without mixing terminal box characters into the assistant surface. |
| History replay | Reopening a chat/workflow must restore events and terminal records without duplicating terminal cards. |
| Long running workflow recovery | For workflows with many events, terminal snapshots must remain recoverable through the terminal API rather than depending on the chat event window. |

## Provider Coverage

Builder E2E should run against every enabled coding provider that the selected
environment supports. At minimum:

- Claude Code tmux transport.
- Codex CLI tmux transport.
- Antigravity CLI tmux transport in explicit local/contract runs; the provider
  is visible in the UI as **Antigravity CLI (Alpha)**. Release certification
  still requires the selected live provider and application proofs.
- Cursor CLI tmux transport when installed.
- Pi CLI tmux transport when installed.

Provider-specific certification remains in the provider contract. Builder E2E
only needs to verify that the builder passes the correct runtime context and
handles the provider result/events correctly.

## Test Data Requirements

Real E2E tests should use:

- A temporary workflow under `workspace-docs/Workflow/...`.
- A deterministic random canary per test run.
- A small output file in the step output directory.
- Low/cheap model tiers unless the test specifically certifies a higher tier.
- Real MCP bridge calls, not fake tool output, for at least the workflow-step and
  todo-orchestrator paths.

Fake/unit tests are allowed for renderer, parser, and event-store edge cases,
but they do not certify the coding-agent integration.

## Related Files

- `agent_go/cmd/testing/coding_agent_chat_e2e.go`
- `agent_go/cmd/server/coding_agent_modes_test.go`
- `agent_go/cmd/server/terminal_routes_test.go`
- `agent_go/internal/terminals/store_test.go`
- `frontend/src/components/TerminalCenter.tsx`
- `frontend/src/components/EventDisplay.tsx`
- `docs/core/mcp_bridge_layer.md`
- `docs/workflow/workflow_shell_working_directory.md`
