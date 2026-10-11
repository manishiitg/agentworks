# Event usage catalog

Generated 2026-09-22 from repo-wide reference audit (const names + raw wire strings; tests, generated code, and build artifacts excluded). Companion to PLAT-352. Verdicts: LIVE = produced and consumed; DEAD = neither; DEAD-RENDERER = UI branch with no emitter; RETENTION-ONLY = kept in store, never rendered; NEVER-DISPLAYED = hidden/nulled; BRIDGE-SKIPPED = produced then dropped at the event bridge; TELEMETRY-DEMOTE = emitted, never read, should be logs; FILTERED-BY-DESIGN = excluded from the transcript by NON_TRANSCRIPT_TYPES; OBSERVABILITY-ONLY = emitted to Langfuse/LangSmith tracers, never enters the *events.AgentEvent stream so never reaches polling/SSE; REMOVED = deleted by the 2026-09-22 dead-code batch (tombstone kept so the absence reads as deliberate).

Update 2026-09-22 (deletion batch 1, uncommitted at time of writing): 14 tombstoned REMOVED; `cache_event` corrected (producers were misattributed to `cache_hit`); new `comprehensive_cache_event` entry splits it from the removed `comprehensive_cache` const; `tool_output` corrected to DEAD (constructor never called); `system_prompt` renderer deleted (still emitted, still transcript-filtered). Emission verified per type, not just references: a dispatcher branch alone does not prove live.

Update 2026-09-22 (minimal diagnostics rail): `selectTerminalEvents` gained `TERMINAL_RAIL_HIDDEN_TYPES` — the dev rail now shows user/assistant/tool rows plus content-bearing gates, errors, and results only. Rail-hidden (still emitted, stored, retained; product-chat rendering untouched): the 8 sub-agent lifecycle types, `orchestrator_end`, `todo_task_route_selected/step_completed`, `workflow_start/progress/end`, the 3 summarization types, `step_token_usage`, `routing_evaluated`, `variables_extracted`, `independent_steps_selected`, `retry_attempt`, `broken_pipe`, `max_turns_reached`, `learn_code_script_execution`, `mcp_server_selection`, `synthetic_turn_ready`, `auto_notification_steered`, `conversation_resumed`. Only `orchestrator_agent_start` also lost its dispatcher branch (returns null; component deleted) since it painted nowhere.

Update 2026-09-22 (deletion batch 3, same rule): 10 more REMOVED (37 total) — `context_editing_completed`/`context_editing_error` (whole context-editing feature removed: `mcpagent/agent/context_editing.go`, `/compact` route + handler, `compactContext` client, `handleCompact`, `enable_context_editing` request/preset fields), `learning_completed`/`learning_failed`/`learning_skipped` (legacy eval-subsystem leftovers; consts/structs never constructed), `orchestrator_start`/`orchestrator_error` (never emitted; only `orchestrator_end` ever was), `throttling_detected`/`token_limit_exceeded` (schema-only; zero backend refs outside schema-gen at HEAD), `context_canceled` (single-L misspelling, never on the wire; match arms fixed to `context_cancelled`). Kept deliberately: `learn_code_script_execution` (emitted 4x in `controller_execution.go`, consumed by `useChatStore`/`ChatArea`/`EventDispatcher`, STRUCTURAL), `orchestrator_end`, `variables_extracted`, `workflow_error`. Also fixed live contract bugs found during deletion: `session_execution_tree_test.go` asserted terminal status for deleted `batch_*` events and misspelled `context_canceled` (wire is `context_cancelled`); `session_execution_tree.go` already used the correct spelling.

Update 2026-09-22 (batch 5, feature removal): 3 more REMOVED (60 total) — the `context_summarization_started/completed/error` trio with the entire summarization feature: auto path force-disabled for all coding-agent CLI providers, manual `/summarize` route uncalled from any UI, `fixed_threshold_*` metadata + displays gone with it. Historical rows excluded server-side by the tombstone (60 names).

Update 2026-09-22 (external review acceptance, batch 4, same rule): 20 more REMOVED (57 total) — the reviewer's safe-to-delete list, verified per event at the producer level: the false-positive class (logger-`Debug()`, `logErrorDetails*`, `compactedInLLMMessages` counter, telemetry strings) and schema/Langfuse-only types (`json_validation_*`, `llm_token_usage`, `streaming_progress`, tracer consts with no emitter). Also deleted the whole `workflow_start/progress/end` trio (never emitted; `orchestrator_end`/`workflow_error` are the live signals), the `background_agent_failed` ghost (const never defined), `human_verification_response` (phantom; `HumanVerificationDisplay` renders `request_human_feedback`), and `tool_output`/`tool_response`/`tool_call_progress` (never produced on the chat wire). `mcp_server_connection` REMOVED bare-name only: the carrier struct + ctor stay as the payload for the live `mcp_server_connection_start/end` tracer events. Historical rows for all 57 are excluded server-side by the `LEGACY_REMOVED_EVENT_TYPES` tombstone in `agent_go/internal/events/event_store.go` (`ShouldShowEvent` fails closed; polling never returns them and they don't consume the window), guarded by `TestLegacyRemovedEventTypesExcludedFromPolling`.

Update 2026-09-22 (deletion batch 2, same rule): 13 more REMOVED (27 total) — `step_progress_updated` (emitted, read nowhere), the 7 per-operation cache wire strings (structs never constructed), `todo_steps_extracted` (emit path uncalled), and the 4 batch wrappers (deliberately never emitted; `batch_execution_canceled` survives as the only live batch event). Kept with new guards: `mcp_server_connection_start/end` are OBSERVABILITY-ONLY (Langfuse/LangSmith spans) and were added to bridge SKIP + NEVER_SHOW + NON_TRANSCRIPT_TYPES. Also removed downstream-only machinery with no event source left: `stepStatusMap`/`currentStepId` store + canvas coloring, `BatchProgressHeader`, `extractWorkflowInfo`, batch restore.

## `agent_end` — LIVE

Producers:
- `mcpagent/agent/agent.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/context_aware_bridge.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_exports.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/base_orchestrator_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/background_transcript.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_identity.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/constants/runningWorkflows.ts`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useWorkflowStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/workflowEventProcessor.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/TerminalEventTranscript.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `agent_error` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/event_bridge/base_bridge.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/pkg/agentwrapper/llm_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_events.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_routing.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_identity.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/constants/runningWorkflows.ts`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useWorkflowStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/utils/chatDeliveryTelemetry.ts`
- `mcp-agent-builder-go/frontend/src/components/TerminalCenter.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `agent_processing` — REMOVED

Deleted 2026-09-22: const (`mcpagent/events/types.go`), struct + constructor (`mcpagent/events/data.go`), schema-gen registry/union entries. Zero raw-string references in either repo; no emitter ever existed.

## `agent_start` — LIVE

Producers:
- `mcpagent/agent/agent.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/background_agent_transcript.go`
- `mcp-agent-builder-go/agent_go/cmd/server/background_agents.go`
- `mcp-agent-builder-go/agent_go/cmd/server/delegation.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/cmd/server/virtual-tools/sub_agent_tools.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/llm/base_llm.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/sub_agent_async.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_exports.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_delegation.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/timing_persistence.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/base_orchestrator_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_background_transcript.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/background_transcript.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useRunningWorkflowsStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/workflowEventProcessor.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `auto_notification_steered` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/types.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `background_agent_completed` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/session_execution_tree.go`
- `mcp-agent-builder-go/agent_go/cmd/server/chat_history_routes.go`
- `mcp-agent-builder-go/agent_go/cmd/testing/coding_agent_background_e2e.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/types.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/TerminalEventTranscript.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `background_agent_failed` — REMOVED

Deleted 2026-09-22 (batch 4): const intentionally never defined (`types.go`: "handled via terminal-state normalization"), zero constructors, zero emitters — 4 dead consumer match arms only. Removed: STRUCTURAL entry, session_execution_tree failed arm, session_activity_tree label, chat_history_routes resume-list entry, terminals/store case, useChatStore retention + TerminalCenter error-set entries, LIFECYCLE family entry.

## `background_agent_started` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/chat_history_routes.go`
- `mcp-agent-builder-go/agent_go/cmd/testing/workflow_auto_notification_e2e.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/types.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `background_agent_terminated` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/session_execution_tree.go`
- `mcp-agent-builder-go/agent_go/cmd/server/chat_history_routes.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/types.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `batch_execution_canceled` — LIVE

Kept 2026-09-22 while the other 4 batch wrappers were removed: genuinely emitted on context-cancel (`controller_batch_execution.go:222`), rendered (`BatchExecutionCanceledEventDisplay`), retained, execution-tree classified, STRUCTURAL. The mcpagent-side const was deleted (emitter uses the orchestrator package); schema-gen never carried this type (frontend interface is hand-written in `event-types.ts`).

Producers:
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_batch_execution.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `batch_execution_end` — REMOVED

Deleted 2026-09-22 with the unemitted batch wrappers (see `batch_group_start`).

## `batch_execution_start` — REMOVED

Deleted 2026-09-22 with the unemitted batch wrappers (see `batch_group_start`).

## `batch_group_end` — REMOVED

Deleted 2026-09-22 with the unemitted batch wrappers (see `batch_group_start`).

## `batch_group_start` — REMOVED

Deleted 2026-09-22: the entire batch wrapper family (`batch_execution_start/end`, `batch_group_start/end`) was never produced anywhere — constructors uncalled, no raw-string emitters; the backend comment in `controller_batch_execution.go` confirms routine wrapper lifecycle events are deliberately not emitted. Removed: consts (both repos), structs + ctors (`orchestrator/events/data.go`), schema-gen entries, `planning_exports.go` group-end reader + `notifyWorkflowExecutionPhaseComplete` (+ its test), activity/execution-tree labels, store HIDDEN entries, 4 dispatcher branches, renderer components (`BatchGroupStartEvent.tsx`, `BatchGroupEndEvent.tsx`, start/end displays), `extractWorkflowInfo`, WorkflowLayout batch restore, `useWorkflowStore` batch slice (`batchProgress`, `handleBatchGroupStart/End`, `resetBatchProgress`), `BatchProgressHeader`, retention + SUMMARY entries. `batch_execution_canceled` is the sole survivor (genuinely emitted).

## `blocking_human_feedback` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/server.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_connector.go`
- `mcp-agent-builder-go/agent_go/cmd/server/virtual-tools/human_tools.go`
- `mcp-agent-builder-go/agent_go/cmd/server/virtual-tools/delegation_tools.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_feedback.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_agent_factory.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/constants/runningWorkflows.ts`
- `mcp-agent-builder-go/frontend/src/stores/useRunningWorkflowsStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/humanFeedbackAttention.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `broken_pipe` — LIVE

Producers:
- `mcpagent/agent/parallel_tool_execution.go`
- `mcpagent/agent/conversation.go`
- `mcpagent/agent/error_handler.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `cache_cleanup` — REMOVED

Deleted 2026-09-22 with the unemitted per-operation cache wire strings (see `cache_hit`).

## `cache_error` — REMOVED

Deleted 2026-09-22 with the unemitted per-operation cache wire strings (see `cache_hit`).

## `cache_event` — BRIDGE-SKIPPED, FILTERED-BY-DESIGN

Producers (corrected 2026-09-22; previously misattributed to `cache_hit`):
- `mcpagent/agent/parallel_tool_execution.go` (`NewCacheHitEvent` → `emitTypedEvent`)
- `mcpagent/agent/conversation.go` (`NewCacheHitEvent`, `NewCacheOperationStartEvent` → `emitTypedEvent`)
All `CacheEvent` constructors return wire type `cache_event` via `GetEventType() → GenericCache`; the `cache_hit`/`cache_miss`/`cache_write`/`cache_expired`/`cache_cleanup`/`cache_error`/`cache_operation_start` wire strings are never produced.
Consumers:
- `mcpagent/observability/*` tracers (Langfuse/LangSmith spans)
Dropped at `BaseEventBridge.SKIP_EVENTS` + `EventStore.NEVER_SHOW_EVENTS`, so never reaches polling/SSE; dedicated renderer (`CacheEvent.tsx`) deleted 2026-09-22 and the type added to `NON_TRANSCRIPT_TYPES` as defense-in-depth (mirrors `streaming_*`), covered by `terminalEventTranscript.test.ts`.

## `cache_expired` — REMOVED

Deleted 2026-09-22 with the unemitted per-operation cache wire strings (see `cache_hit`).

## `cache_hit` — REMOVED

Deleted 2026-09-22: all 7 per-operation cache wire strings (`cache_hit/miss/write/expired/cleanup/error/operation_start`) were never produced — the distinct `Cache*Event` structs were never constructed (verified across mcpagent, agent_go, provider repo) and every live cache constructor returns wire type `cache_event`. Removed: 7 consts + component-switch case, 7 structs, 5 uncalled unified ctors (`NewCacheMiss/Write/Expired/Cleanup/ErrorEvent`; Hit/OperationStart kept — they produce `cache_event`), Langfuse `handleCacheHit/Miss/Write/Error` + cases + consts (unreachable), schema-gen entries. The bridge `SKIP_EVENTS` / store `NEVER_SHOW_EVENTS` string guards are kept as belt-and-braces.

## `cache_miss` — REMOVED

Deleted 2026-09-22 with the unemitted per-operation cache wire strings (see `cache_hit`).

## `cache_operation_start` — REMOVED

Deleted 2026-09-22 with the unemitted per-operation cache wire strings (see `cache_hit`).

## `cache_write` — REMOVED

Deleted 2026-09-22 with the unemitted per-operation cache wire strings (see `cache_hit`; the audit's "producing files" were cost-ledger/code references, never event emitters).

## `comprehensive_cache` — REMOVED

Deleted 2026-09-22: const (`mcpagent/events/types.go`) + schema-gen entries. Never emitted under this exact wire string. Do not confuse with `comprehensive_cache_event` (below), which is a live observability-only struct type string.

## `comprehensive_cache_event` — OBSERVABILITY-ONLY

Producers:
- `mcpagent/mcpcache/integration.go` (`EmitComprehensiveCacheEvent`, called from `agent/conversation.go` and cache connect paths)
Consumers:
- `mcpagent/observability/*` tracers only. The streaming tracer forwards to SSE solely for `*events.AgentEvent`; this is a `*mcpcache.ComprehensiveCacheEvent`, so it is type-assertion-filtered and never reaches the wire. Dedicated renderer (`ComprehensiveCacheEvent.tsx`) deleted 2026-09-22; also listed in bridge `SKIP_EVENTS` / store `NEVER_SHOW_EVENTS` as belt-and-braces.

## `context_canceled` — REMOVED

Deleted 2026-09-22 (batch 3): was never a real wire event — a single-L misspelling in two backend match arms (`session_execution_tree.go`, `polling.go`) that could never match the actual `context_cancelled` wire string (const `ContextCancelled`). Both arms fixed to the correct spelling; the listed frontend "consumer" already used `context_cancelled`. Zero code references to the single-L string remain (outside this tombstone).

## `context_cancelled` — LIVE

Producers:
- `mcpagent/agent/llm_generation.go`
- `mcpagent/agent/parallel_tool_execution.go`
- `mcpagent/agent/conversation.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/testing/context-cancellation-test.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/context_aware_bridge.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `context_editing_completed` — REMOVED

Deleted 2026-09-22 (batch 3): the entire context-editing feature was removed, not just the event. Backend: `mcpagent/agent/context_editing.go`, `context_editing_routes.go` (`/compact` route + handler), `agent_tuning.go` flags/thresholds, `ContextEditing*` consts/structs/constructors, `enable_context_editing` request field. Frontend: `ContextEditingCompletedEvent.tsx`, dispatcher branch, `agentApi.compactContext` + `CompactContextRequest/Response`, `handleCompact` (dead: no `/compact` command or button ever called it), `enable_context_editing` submit-override + preset `llmConfig` field. Schema-gen entries + JSON schemas + `event-types.ts` union/map regenerated by hand.

## `context_editing_error` — REMOVED

Deleted 2026-09-22 (batch 3): same feature removal as `context_editing_completed`. Backend: const/struct/constructor, schema-gen entries. Frontend: `ContextEditingErrorEvent.tsx`, dispatcher branch, union/map entries.

## `context_summarization_completed` — REMOVED

Deleted 2026-09-22 (batch 5) with the summarization feature (see `context_summarization_started`). Historical rows covered by the `LEGACY_REMOVED_EVENT_TYPES` tombstone.

## `context_summarization_error` — REMOVED

Deleted 2026-09-22 (batch 5) with the summarization feature (see `context_summarization_started`). Historical rows covered by the `LEGACY_REMOVED_EVENT_TYPES` tombstone.

## `context_summarization_started` — REMOVED

Deleted 2026-09-22 (batch 5): whole context-summarization feature removed. Auto path was force-disabled for every coding-agent CLI provider ("handled natively by CLI") with default-false everywhere; manual `/summarize` route had zero UI callers. Removed: `mcpagent/agent/context_summarization.go`, agent fields/options/defaults, conversation auto-trigger, events, Langfuse handlers, `summarization_routes.go` + route, store Add* methods, config plumbing (server/agent_tuning/llm_agent/interfaces/base_agent/orchestrator/preset), frontend client/UI/renderers, schemas + generated types.

## `conversation_end` — FILTERED-BY-DESIGN

Producers:
- `mcpagent/agent/conversation.go`
- `mcpagent/agent/agent.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_identity.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/constants/runningWorkflows.ts`
- `mcp-agent-builder-go/frontend/src/stores/useRunningWorkflowsStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useWorkflowStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/workflow/workflowChatTabConversion.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `conversation_error` — LIVE

Producers:
- `mcpagent/agent/parallel_tool_execution.go`
- `mcpagent/agent/conversation.go`
- `mcpagent/agent/tool_loop_detector.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/event_bridge/base_bridge.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_identity.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/constants/runningWorkflows.ts`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useWorkflowStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/utils/chatDeliveryTelemetry.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/TerminalCenter.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `conversation_resumed` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/chatSubmitHelpers.ts`
- `mcp-agent-builder-go/frontend/src/utils/sessionRestore.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `conversation_start` — FILTERED-BY-DESIGN

Producers:
- `mcpagent/agent/conversation.go`
- `mcpagent/agent/agent.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `conversation_thinking` — LIVE

Producers:
- `mcpagent/agent/llm_generation.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/utils/thinkingDeltas.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/utils/chatDeliveryTelemetry.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `conversation_turn` — FILTERED-BY-DESIGN

Producers:
- `mcpagent/agent/conversation.go`
- `mcpagent/agent/agent.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/agent_profile_routes.go`
- `mcp-agent-builder-go/agent_go/cmd/server/conversation_turn_queue.go`
- `mcp-agent-builder-go/agent_go/cmd/server/server.go`
- `mcp-agent-builder-go/agent_go/cmd/server/conversation_turn_stall_diagnostics.go`
- `mcp-agent-builder-go/agent_go/cmd/server/pulse_result_guard.go`
- `mcp-agent-builder-go/agent_go/cmd/server/conversation_turn_lifecycle.go`
- `mcp-agent-builder-go/agent_go/cmd/server/bot_session_starter.go`
- `mcp-agent-builder-go/agent_go/cmd/server/workflow_execution_tracker.go`
- `mcp-agent-builder-go/agent_go/cmd/server/background_agents.go`
- `mcp-agent-builder-go/agent_go/cmd/server/delegation.go`
- `mcp-agent-builder-go/agent_go/cmd/server/whatsapp_profile_turn.go`
- `mcp-agent-builder-go/agent_go/cmd/server/scheduler.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `debug` — REMOVED

Deleted 2026-09-22 (batch 4): the 60-file "producer" list was logger-`Debug()` false positives — `DebugEvent` was never constructed and the bare wire type never produced. Removed: const, struct, schema-gen entries, generated/union entries.

## `decision_evaluated` — REMOVED

Deleted 2026-09-22: const (`mcpagent/events/types.go`) + schema-gen entries. Zero references anywhere; no emitter, no consumer.

## `decision_request_missing` — LIVE

Producers: none found
Consumers:
- `mcp-agent-builder-go/frontend/src/components/workflow/pulseFindingPresentation.ts`

## `delegation_end` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/session_execution_tree.go`
- `mcp-agent-builder-go/agent_go/cmd/server/delegation.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `delegation_start` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/delegation.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `error_detail` — REMOVED

Deleted 2026-09-22 (batch 4): provider "producers" were `logErrorDetails*` logging helpers, not the event; `NewErrorDetailEvent` uncalled. Removed: const, struct + ctor, schema-gen entries, generated/union entries, orphaned `ErrorDetailEvent.tsx` + index export (no dispatcher branch ever referenced it).

## `fix_applied` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/pulse_finding_lifecycle.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/workflow/pulseFindingPresentation.ts`

## `human_input` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/agentworks/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/terminal_routes.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/consolidated_plan_tools.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/utils/stepConfigMatching.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/workflow/canvas/WorkflowCanvas.tsx`
- `mcp-agent-builder-go/frontend/src/components/workflow/executionLogs/helpers.tsx`
- `mcp-agent-builder-go/frontend/src/components/workflow/hooks/usePlanToFlow.ts`

## `human_verification_response` — REMOVED

Deleted 2026-09-22 (batch 4): phantom event — `HumanVerificationDisplay` renders `request_human_feedback`, never this type; no backend/frontend constructor. Removed: consts (both repos), event struct, dead payload struct (`workflow_orchestrator.go`, zero refs), schema-gen entries, generated/union entries, polling arm, store attention-check.

## `independent_steps_selected` — REMOVED

Deleted 2026-09-22 (batch 4): full renderer for a never-constructed struct. Removed: consts (both repos), struct, schema-gen entries, generated/union entries, dispatcher branch + `IndependentStepsSelectedEventDisplay.tsx`, rail-denylist entry.

## `json_validation_end` — REMOVED

Deleted 2026-09-22 (batch 4) with its start twin: schema-only, zero emitters. Removed: const, struct, schema-gen entries, generated/union entries, component-switch arms.

## `json_validation_start` — REMOVED

Deleted 2026-09-22 (batch 4) with its end twin: schema-only, zero emitters. Removed: const, struct, schema-gen entries, generated/union entries, component-switch arms.

## `large_tool_output_detected` — LIVE

Producers:
- `mcpagent/agent/parallel_tool_execution.go`
- `mcpagent/agent/conversation.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `large_tool_output_file_write_error` — TELEMETRY-DEMOTE

Producers:
- `mcpagent/agent/parallel_tool_execution.go`
- `mcpagent/agent/conversation.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
Consumers: none found

## `large_tool_output_file_written` — LIVE

Producers:
- `mcpagent/agent/parallel_tool_execution.go`
- `mcpagent/agent/conversation.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `large_tool_output_server_unavailable` — REMOVED

Deleted 2026-09-22: const (`mcpagent/events/types.go`), struct + constructor (`mcpagent/events/data.go`), schema-gen entries. Constructor never called; siblings (`large_tool_output_detected`, `large_tool_output_file_written`, `large_tool_output_file_write_error`) kept.

## `learn_code_script_execution` — LIVE

Kept deliberately in batch 3 (not one of the deleted learning events): the name is legacy, the signal is live scripted-mode execution.
Producers:
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_progress.go` (`emitScriptedExecutionEvent`, 4 call sites in `controller_execution.go`)
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go` (STRUCTURAL retention)
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `learning_completed` — REMOVED

Deleted 2026-09-22 (batch 3): legacy eval-subsystem leftover (eval retired 2026-09-19, commit `7d98e759d`). No constructor ever existed, zero producers. NOTE: `StepContent.tsx` still matches `log.type === 'learning_completed'` — that is a *file-log* entry type from `learning-execution.json` run files (read by `/workflow/logs`), not the wire event; kept for historical run data.

## `learning_failed` — REMOVED

Deleted 2026-09-22 (batch 3): same eval leftover as `learning_completed`; zero producers. `StepContent.tsx` badge match is the file-log namespace (see above), kept for historical runs.

## `learning_skipped` — REMOVED

Deleted 2026-09-22 (batch 3): const + struct + `GetEventType` (`orchestrator/events`), schema-gen entries. Struct never constructed. `StepContent.tsx` badge match is the file-log namespace (see `learning_completed`), kept for historical runs.

## `live_execution_streaming` — REMOVED

Deleted 2026-09-22: dispatcher branch + `LiveExecutionStreamingEventCard` (`EventDispatcher.tsx`) and `formatLiveStreamingPreview` (`frontend/shared/session/streamingStatus.ts`). Frontend-only string; zero backend references, never emitted.

## `live_input_confirmed` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/conversation_turn_queue.go`
- `mcp-agent-builder-go/agent_go/cmd/server/live_input_durable.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/utils/liveInputReceipt.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`

## `llm_generation_end` — LIVE

Producers:
- `mcpagent/agent/conversation.go`
- `mcpagent/agent/agent.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/scheduled_turn_outcome.go`
- `mcp-agent-builder-go/agent_go/cmd/server/bot_session_starter.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/pkg/costobserver/observer.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/context_aware_bridge.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/products/sparkquill/api/platform/events.ts`
- `mcp-agent-builder-go/frontend/src/stores/useRunningWorkflowsStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/utils/chatDeliveryTelemetry.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/TerminalEventTranscript.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `llm_generation_error` — LIVE

Producers:
- `mcpagent/agent/conversation.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/context_aware_bridge.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/TerminalCenter.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `llm_generation_start` — FILTERED-BY-DESIGN

Producers:
- `mcpagent/agent/agent.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/context_aware_bridge.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `llm_generation_with_retry` — FILTERED-BY-DESIGN

Producers:
- `mcpagent/agent/llm_generation.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `llm_messages` — REMOVED

Deleted 2026-09-22 (batch 4): the `conversation.go` "producer" was a `compactedInLLMMessages` counter variable, not the event. Removed: const, struct + `LLMMessage` helper (sole consumer), schema-gen entries, generated/union entries.

## `llm_token_usage` — REMOVED

Deleted 2026-09-22 (batch 4): schema-only, zero emitters. Removed: const, struct, schema-gen entries, generated/union entries.

## `max_turns_reached` — LIVE

Producers:
- `mcpagent/agent/conversation.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `mcp_server_connection` — REMOVED

Deleted 2026-09-22 (batch 4, bare name only): the carrier struct + constructor stay live as the payload for the separately typed `mcp_server_connection_start/end` tracer events (both call sites re-type before emit). Removed: schema-gen bare entries, generated/union bare entries (renderer already gone in batch 1). Const kept as the payload tag for `GetEventType` — see `events/types.go` NOTE.

## `mcp_server_connection_end` — OBSERVABILITY-ONLY

Kept 2026-09-22: emitted per MCP connect (`connection_session.go`, via `ag.tracers` incl. the streaming tracer) and consumed by Langfuse + LangSmith connection-span handlers — a genuine backend purpose. Never had a frontend consumer, so on 2026-09-22 it was added to bridge `SKIP_EVENTS`, store `NEVER_SHOW_EVENTS`, and frontend `NON_TRANSCRIPT_TYPES` (covered by `terminalEventTranscript.test.ts`) to stop it rendering as an "Unknown Event Type" card. Tracers receive it before the bridge, so spans are unaffected.

Producers:
- `mcpagent/agent/connection_session.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
Consumers:
- `mcpagent/observability/langfuse_tracer.go`, `langsmith_tracer.go` (connection spans)

## `mcp_server_connection_error` — REMOVED

Deleted 2026-09-22: const (`mcpagent/events/types.go`), struct (`mcpagent/events/data.go`), Langfuse/LangSmith handler cases + functions, `docs/tracing.md` mention, schema-gen entries, dispatcher branch (shared `MCPServerConnectionEventDisplay`). Zero raw-string references; never emitted.

## `mcp_server_connection_start` — OBSERVABILITY-ONLY

Kept 2026-09-22: emitted per MCP connect (`connection_session.go`, via `ag.tracers` incl. the streaming tracer) and consumed by Langfuse + LangSmith connection-span handlers — a genuine backend purpose. Never had a frontend consumer, so on 2026-09-22 it was added to bridge `SKIP_EVENTS`, store `NEVER_SHOW_EVENTS`, and frontend `NON_TRANSCRIPT_TYPES` (covered by `terminalEventTranscript.test.ts`) to stop it rendering as an "Unknown Event Type" card. Tracers receive it before the bridge, so spans are unaffected.

Producers:
- `mcpagent/agent/connection_session.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
Consumers:
- `mcpagent/observability/langfuse_tracer.go`, `langsmith_tracer.go` (connection spans)

## `mcp_server_discovery` — REMOVED

Deleted 2026-09-22: const (`mcpagent/events/types.go`), struct + constructor (`mcpagent/events/data.go`), Langfuse/LangSmith handler cases + functions, `docs/tracing.md` mention, schema-gen entries, dispatcher branch + `MCPServerDiscoveryEvent.tsx`. Zero raw-string references; never emitted.

## `mcp_server_selection` — NEVER-DISPLAYED

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/work_mcp_selection_tools.go`
- `mcp-agent-builder-go/agent_go/pkg/agentwrapper/llm_agent.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `model_change` — REMOVED

Deleted 2026-09-22: const (`mcpagent/events/types.go`), struct + constructor (`mcpagent/events/data.go`), schema-gen entries, dispatcher branch + `ModelChangeEvent.tsx`. Never emitted; only remaining reference is a dead-tolerant `case` label in the manual e2e tool `agent_go/cmd/testing/coding_agent_chat_e2e.go` (harmless, fires on no producer).

## `orchestrator_agent_end` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_exports.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/base_orchestrator_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/background_transcript.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useRunningWorkflowsStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/TerminalEventTranscript.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `orchestrator_agent_error` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/event_bridge/base_bridge.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_events.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_routing.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/TerminalCenter.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `orchestrator_agent_start` — RETENTION-ONLY

Reclassified 2026-09-22 (batch-3 follow-up): the card was removed from code per "should not be visible anywhere". The `EventDispatcher` branch now returns null and `OrchestratorAgentStartEventDisplay.tsx` is deleted. (It never showed in product chats anyway — `isProductMainConversationEvent` excludes child-execution starts — and the diagnostics rail does not need a banner per spawn; the end card carries the result.)

Still emitted and consumed, only never painted:
Producers:
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/base_orchestrator_agent.go` (`emitAgentStartEvent`, every sub-agent run)
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go` (5 workshop step-execution sites)
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/llm/base_llm.go` (emitted as fallback type)
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
Consumers (all non-rendering):
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts` (important-flush + retention)
- `mcp-agent-builder-go/frontend/src/stores/useRunningWorkflowsStore.ts` (running heartbeat)
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts` (restore fallback)
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go` (bot text narration — still visible as chat text in bot surfaces, not a card)
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_exports.go` (event matching)
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go` (STRUCTURAL retention)

## `orchestrator_end` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/types/workflow_orchestrator.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_events.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/constants/runningWorkflows.ts`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useWorkflowStore.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `orchestrator_error` — REMOVED

Deleted 2026-09-22 (batch 3): never emitted (only `orchestrator_end` ever was; `workflow_error` is the live failure signal). Removed: const + struct, schema-gen entries, `OrchestratorErrorEvent.tsx`, dispatcher branch, `runningWorkflows.ts` ERROR/IMPORTANT entries (ERROR is now `['workflow_error']`), `useChatStore` important/retain matches, union/map entries.

## `orchestrator_start` — REMOVED

Deleted 2026-09-22 (batch 3): never emitted. Removed: const + struct, schema-gen entries, `OrchestratorStartEvent.tsx`, dispatcher branch, union/map entries. (`orchestrator_end` is the only live member of this family.)

## `performance` — REMOVED

Deleted 2026-09-22 (batch 4): "producer" hits were telemetry-field/SSE-flag/HTML-key strings, not the event. Removed: const, struct, schema-gen entries, generated/union entries.

## `phase_completed` — REMOVED

Deleted 2026-09-22: retention-allowlist entry (`useChatStore.shouldRetainEvent`). Frontend-only string; zero backend references, never emitted.

## `phase_started` — REMOVED

Deleted 2026-09-22: retention-allowlist entry (`useChatStore.shouldRetainEvent`). Frontend-only string; zero backend references, never emitted.

## `plan_approval` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/chat_history_routes.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_connector.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/cmd/server/polling.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/types.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useRunningWorkflowsStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `pre_validation_completed` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_progress.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_scripted.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/workflow_events.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/components/TerminalCenter.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `prerequisite_navigation` — REMOVED

Deleted 2026-09-22: const (`mcpagent/events/types.go`), struct + constructor (`mcpagent/events/data.go`), schema-gen entries. Zero references; no emitter, no consumer.

## `presentation_updated` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/types.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/TerminalEventTranscript.tsx`

## `product_interaction` — FILTERED-BY-DESIGN

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/types.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/products/sparkquill/api/platform/events.ts`
- `mcp-agent-builder-go/frontend/src/components/TerminalEventTranscript.tsx`

## `request_human_feedback` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_feedback.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_human_input.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/execution_manager.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/constants/runningWorkflows.ts`
- `mcp-agent-builder-go/frontend/src/stores/useRunningWorkflowsStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useWorkflowStore.ts`
- `mcp-agent-builder-go/frontend/src/components/workflow/hooks/useWorkflowExecution.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `retry_attempt` — LIVE

Producers:
- `mcpagent/agent/llm_generation.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/virtual-tools/sub_agent_tools.go`
- `mcp-agent-builder-go/agent_go/pkg/pulseintake/runtime.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `routing_evaluated` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/session_activity_tree.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/types.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/components/TerminalCenter.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `status_line` — FILTERED-BY-DESIGN

Producers:
- `mcpagent/agent/llm_generation.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `step_execution_end` — REMOVED

Deleted 2026-09-22 with the `step_execution_*` family: consts (`mcpagent/events/types.go`) + schema-gen entries. Zero references; no emitters. (Canvas reads execution logs, not these types; `step_progress_updated` is the live step signal and is kept.)

## `step_execution_failed` — REMOVED

Deleted 2026-09-22 with the `step_execution_*` family (see `step_execution_end`).

## `step_execution_start` — REMOVED

Deleted 2026-09-22 with the `step_execution_*` family (see `step_execution_end`).

## `step_progress_updated` — REMOVED

Deleted 2026-09-22: emitted from 5 backend sites but read nowhere — ChatArea had already removed its processing ("not needed during chat"), WorkflowLayout never read it (it scanned `todo_task_step_completed`), no renderer, no backend readers; the `event_store.go` "required for canvas" comment was stale. Removed: emit calls + `emitStepProgressUpdatedEvent` (wrappers kept as webhook-persistence hooks for their 13 call sites), struct, both consts, schema-gen entries, retention/NEVER_DISPLAY/SUMMARY entries (NEVER_DISPLAY set deleted — it held only this type). Follow-up in the same batch: `stepStatusMap`/`currentStepId` store machinery + canvas node-coloring deleted as well (sole writer was the WorkflowLayout restore of this now-gone flow).

## `step_token_usage` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_types.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_crew.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/workflow_events.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_tokens.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `streaming_chunk` — FILTERED-BY-DESIGN

Producers:
- `mcpagent/agent/turn_session_progress.go`
- `mcpagent/agent/llm_generation.go`
- `mcpagent/agent/agent.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/server.go`
- `mcp-agent-builder-go/agent_go/cmd/server/claude_native_transcript_sync.go`
- `mcp-agent-builder-go/agent_go/cmd/server/chat_history_persistence.go`
- `mcp-agent-builder-go/agent_go/cmd/server/chat_history_routes.go`
- `mcp-agent-builder-go/agent_go/cmd/server/delegation.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/pkg/agentwrapper/llm_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/context_aware_bridge.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_progress.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`

## `streaming_connection_lost` — REMOVED

Deleted 2026-09-22 (batch 4): Langfuse handler + case + const with no emitter. Removed: const, struct, schema-gen entries, generated/union entries, tracer handler/arms/consts.

## `streaming_end` — FILTERED-BY-DESIGN

Producers:
- `mcpagent/agent/llm_generation.go`
- `mcpagent/agent/agent.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_progress.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`

## `streaming_error` — REMOVED

Deleted 2026-09-22 (batch 4): Langfuse handler + case + const with no emitter. Removed: const, struct, schema-gen entries, generated/union entries, tracer handler/arms/consts.

## `streaming_progress` — REMOVED

Deleted 2026-09-22 (batch 4): schema-only, zero emitters. Removed: const, struct, schema-gen entries, generated/union entries.

## `streaming_start` — FILTERED-BY-DESIGN

Producers:
- `mcpagent/agent/llm_generation.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`

## `synthetic_turn_ready` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/types.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `system_prompt` — NEVER-DISPLAYED, FILTERED-BY-DESIGN

Producers:
- `mcpagent/agent/llm_generation.go`
- `mcpagent/agent/layer2_certification.go`
- `mcpagent/agent/conversation.go`
- `mcpagent/agent/effective_system_prompt.go`
- `mcpagent/agent/definition.go`
- `mcpagent/agent/agent.go`
- `mcpagent/agent/prompt/builder.go`
- `mcpagent/agent/prompt/prompt.go`
- `mcpagent/agent/utils.go`
- `mcpagent/agent/turn_session.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/crew_access.go`
- `mcp-agent-builder-go/agent_go/cmd/server/server.go`
- `mcp-agent-builder-go/agent_go/cmd/server/guidance/step_system_prompts.go`
- `mcp-agent-builder-go/agent_go/cmd/server/workflow_phase_prompt.go`
- `mcp-agent-builder-go/agent_go/cmd/server/instructions.go`
- `mcp-agent-builder-go/agent_go/cmd/server/channel_prompt.go`
- `mcp-agent-builder-go/agent_go/pkg/agentprofiles/manifest.go`
- `mcp-agent-builder-go/agent_go/pkg/agentprofiles/types.go`
- `mcp-agent-builder-go/agent_go/pkg/agentprofiles/validate.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/interfaces.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/base_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_exports.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_delegation.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/prompt_sections.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/execution_only_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/kb_update_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/supplementary_prompts.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/skills_integration.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/base_orchestrator_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_agent_factory.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
- `mcp-agent-builder-go/agent_go/internal/videoproduct/profile_definition.go`
- `mcp-agent-builder-go/agent_go/internal/agentsession/agentsession.go`
- `mcp-agent-builder-go/agent_go/internal/agentworksproduct/profile_definition.go`
- `mcp-agent-builder-go/agent_go/internal/dominionproduct/profile_definition.go`
- `mcp-agent-builder-go/agent_go/internal/workproduct/profile_definition.go`
- `multi-llm-provider-go/pkg/adapters/codexcli/codexcli_interactive_adapter.go`
- `multi-llm-provider-go/pkg/adapters/codexcli/codexcli_structured_adapter.go`
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_interactive_adapter.go`
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_structured_adapter.go`
- `multi-llm-provider-go/pkg/adapters/picli/picli_adapter.go`
- `multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`
- `multi-llm-provider-go/pkg/adapters/anthropic/anthropic_adapter.go`
- `multi-llm-provider-go/pkg/adapters/cursorcli/cursorcli_adapter.go`
- `multi-llm-provider-go/pkg/adapters/cursorcli/cursorcli_structured_adapter.go`
- `multi-llm-provider-go/pkg/adapters/cursorcli/cursorcli_interactive_adapter.go`
- `multi-llm-provider-go/pkg/codingagentjob/provider_runner.go`
Consumers (dedicated renderer `SystemPromptEvent.tsx` + dispatcher branch deleted 2026-09-22; still emitted every conversation at `mcpagent/agent/conversation.go`, still hidden by backend `HIDDEN_EVENTS` + frontend `NON_TRANSCRIPT_TYPES`):
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `take_control` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/browser_live.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/workflow/WorkflowLiveBrowser.tsx`

## `throttling_detected` — REMOVED

Deleted 2026-09-22 (batch 3): schema-only — at HEAD the only backend references were schema-gen itself; zero emitters. Removed: schema-gen registry/union entries, `ThrottlingDetectedEvent.tsx`, dispatcher branch + import, `debug/index.ts` export, union/map entries. (Exposed by regen: the stale generated types had masked the dead component until `types:events` re-ran.)

## `todo_steps_extracted` — REMOVED

Deleted 2026-09-22: the sole emit path (`CheckAndEmitPlanUpdateEvent`) had zero callers — dead renderer + dead activity-tree label. Removed: emit funcs + exclusive helpers (`ExtractToolCalls/ChangedStepIDsFromMessages`, `getMetadataKeys`, `removeDuplicates`, `ChangedStepIDs`), struct + custom `MarshalJSON`, both consts, schema-gen entries, dispatcher branch + `TodoStepsExtractedEvent.tsx`, activity-tree case. Kept: `IsPlanModificationTool`/`IsStepConfigModificationTool` (4 tests pin them as the definition of "plan mutation"). (`todo_task_*` events are a separate live family and are kept.)

## `todo_task_route_selected` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/session_activity_tree.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/types.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `todo_task_step_completed` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/session_execution_tree.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/types.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/workflow/WorkflowLayout.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `token_limit_exceeded` — REMOVED

Deleted 2026-09-22 (batch 3): schema-only — at HEAD the only backend references were schema-gen itself; zero emitters. Removed: schema-gen registry/union entries, `TokenLimitExceededEvent.tsx`, dispatcher branch + import, `debug/index.ts` export, union/map entries. (Exposed by regen, same as `throttling_detected`.)

## `token_usage` — FILTERED-BY-DESIGN

Producers:
- `mcpagent/agent/context_summarization.go`
- `mcpagent/agent/convrecord/convrecord.go`
- `mcpagent/agent/llm_generation.go`
- `mcpagent/agent/parallel_tool_execution.go`
- `mcpagent/agent/conversation.go`
- `mcpagent/agent/runtime_services.go`
- `mcpagent/agent/agent.go`
- `mcpagent/agent/turn_session.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/workspace_state.go`
- `mcp-agent-builder-go/agent_go/cmd/server/crew_cost.go`
- `mcp-agent-builder-go/agent_go/cmd/server/cost_storage.go`
- `mcp-agent-builder-go/agent_go/cmd/server/server.go`
- `mcp-agent-builder-go/agent_go/cmd/server/product_schedules.go`
- `mcp-agent-builder-go/agent_go/cmd/server/workflow_review_data.go`
- `mcp-agent-builder-go/agent_go/cmd/server/product_webhooks.go`
- `mcp-agent-builder-go/agent_go/cmd/server/schedule_runs.go`
- `mcp-agent-builder-go/agent_go/cmd/server/virtual-tools/tool_costs.go`
- `mcp-agent-builder-go/agent_go/cmd/server/workflow.go`
- `mcp-agent-builder-go/agent_go/pkg/costobserver/observer.go`
- `mcp-agent-builder-go/agent_go/pkg/agentwrapper/llm_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/workflowtypes/crew_cost.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/cost_storage.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_types.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/token_usage_store.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/context_aware_bridge.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_tokens_helpers.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/crew_step.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/pulse_agent_metrics.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_crew.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/workflow_events.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/base_orchestrator_tokens.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
- `multi-llm-provider-go/pkg/adapters/codexcli/codexcli_transcript_usage.go`
- `multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`
- `multi-llm-provider-go/pkg/adapters/bedrock/bedrock_adapter.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/products/video-studio/projectAgentUsage.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `tool_call` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/server.go`
- `mcp-agent-builder-go/agent_go/cmd/server/session_activity_tree.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/background_transcript.go`
- `multi-llm-provider-go/pkg/adapters/cursorcli/cursorcli_structured_adapter.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/ui/ConversationRenderer.tsx`
- `mcp-agent-builder-go/frontend/src/components/workflow/executionLogs/LogPrimitives.tsx`
- `mcp-agent-builder-go/frontend/src/services/api-types.ts`

## `tool_call_end` — LIVE

Producers:
- `mcpagent/agent/llm_generation.go`
- `mcpagent/agent/tool_registry.go`
- `mcpagent/agent/parallel_tool_execution.go`
- `mcpagent/agent/conversation.go`
- `mcpagent/agent/agent.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/background_agents.go`
- `mcp-agent-builder-go/agent_go/cmd/testing/codex_mcp_tool_call.go`
- `mcp-agent-builder-go/agent_go/cmd/testing/claude_experimental.go`
- `mcp-agent-builder-go/agent_go/pkg/agentwrapper/llm_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/context_aware_bridge.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_observer.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
- `multi-llm-provider-go/pkg/adapters/codexcli/codexcli_structured_adapter.go`
- `multi-llm-provider-go/pkg/adapters/codexcli/codexcli_transcript_stream.go`
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_transcript_stream.go`
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_structured_adapter.go`
- `multi-llm-provider-go/pkg/adapters/internal/toolclock/toolclock.go`
- `multi-llm-provider-go/pkg/adapters/picli/picli_structured_adapter.go`
- `multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`
- `multi-llm-provider-go/pkg/adapters/musecli/musecli_exec_stream.go`
- `multi-llm-provider-go/pkg/adapters/musecli/musecli_transcript_stream.go`
- `multi-llm-provider-go/pkg/adapters/cursorcli/cursorcli_transcript_stream.go`
- `multi-llm-provider-go/pkg/adapters/cursorcli/cursorcli_structured_adapter.go`
- `multi-llm-provider-go/pkg/codingagentjob/provider_runner.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useRunningWorkflowsStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/workflowEventProcessor.ts`
- `mcp-agent-builder-go/frontend/src/utils/decisionRefresh.ts`
- `mcp-agent-builder-go/frontend/src/utils/secretMutationRefresh.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `tool_call_error` — LIVE

Producers:
- `mcpagent/agent/llm_generation.go`
- `mcpagent/agent/tool_registry.go`
- `mcpagent/agent/parallel_tool_execution.go`
- `mcpagent/agent/conversation.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/context_aware_bridge.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_observer.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/TerminalCenter.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `tool_call_progress` — REMOVED

Deleted 2026-09-22 (batch 4): bridge-SKIP entry for a never-produced type. Removed: const, struct, schema-gen entries, generated/union entries, bridge SKIP + NEVER_SHOW entries.

## `tool_call_start` — LIVE

Producers:
- `mcpagent/agent/llm_generation.go`
- `mcpagent/agent/tool_registry.go`
- `mcpagent/agent/layer2_certification.go`
- `mcpagent/agent/parallel_tool_execution.go`
- `mcpagent/agent/conversation.go`
- `mcpagent/agent/agent.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/background_agents.go`
- `mcp-agent-builder-go/agent_go/cmd/testing/codex_mcp_tool_call.go`
- `mcp-agent-builder-go/agent_go/cmd/testing/claude_experimental.go`
- `mcp-agent-builder-go/agent_go/pkg/agentwrapper/llm_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/context_aware_bridge.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_observer.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
- `multi-llm-provider-go/pkg/adapters/codexcli/codexcli_structured_adapter.go`
- `multi-llm-provider-go/pkg/adapters/codexcli/codexcli_transcript_stream.go`
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_adapter.go`
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_transcript_stream.go`
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_structured_adapter.go`
- `multi-llm-provider-go/pkg/adapters/internal/toolclock/toolclock.go`
- `multi-llm-provider-go/pkg/adapters/picli/picli_structured_adapter.go`
- `multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`
- `multi-llm-provider-go/pkg/adapters/musecli/musecli_exec_stream.go`
- `multi-llm-provider-go/pkg/adapters/musecli/musecli_transcript_stream.go`
- `multi-llm-provider-go/pkg/adapters/cursorcli/cursorcli_transcript_stream.go`
- `multi-llm-provider-go/pkg/adapters/cursorcli/cursorcli_structured_adapter.go`
- `multi-llm-provider-go/pkg/codingagentjob/provider_runner.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useRunningWorkflowsStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/sessionRestore.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `tool_execution` — BRIDGE-SKIPPED

Producers:
- `mcpagent/agent/tool_registry.go`
- `mcpagent/agent/parallel_tool_execution.go`
- `mcpagent/agent/conversation.go`
- `mcpagent/agent/definition.go`
- `mcpagent/agent/agent.go`
- `mcpagent/agent/prompt/builder.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/workflow_manifest.go`
- `mcp-agent-builder-go/agent_go/cmd/server/tool_execution_context.go`
- `mcp-agent-builder-go/agent_go/cmd/server/server.go`
- `mcp-agent-builder-go/agent_go/cmd/server/agent_tuning.go`
- `mcp-agent-builder-go/agent_go/cmd/server/delegation.go`
- `mcp-agent-builder-go/agent_go/cmd/server/virtual-tools/workspace_advanced_tools.go`
- `mcp-agent-builder-go/agent_go/cmd/testing/read_image_providers.go`
- `mcp-agent-builder-go/agent_go/pkg/agentwrapper/llm_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/interfaces.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/base_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_delegation.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/step_config_clear.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/external_plan_tools.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/step_config.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/base_orchestrator_agent.go`
- `mcp-agent-builder-go/agent_go/internal/agentsession/agentsession.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/utils/decisionRefresh.ts`
- `mcp-agent-builder-go/frontend/src/utils/secretMutationRefresh.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`

## `tool_output` — REMOVED

Deleted 2026-09-22 (batch 4): constructor uncalled, wire string never produced. Removed: const, struct + ctor, schema-gen entries, generated/union entries, bridge SKIP + NEVER_SHOW entries.

## `tool_response` — REMOVED

Deleted 2026-09-22 (batch 4): listed "producers" were message-part-namespace matches (`case "tool_response"` in message reconstruction is live and untouched); the chat event was never produced. Removed: const, struct + ctor, schema-gen entries, generated/union entries, bridge SKIP + NEVER_SHOW entries.

## `tool_result` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go`
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_tool_results.go`
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_transcript_messages.go`
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_transcript_stream.go`
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_structured_adapter.go`
- `multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`
- `multi-llm-provider-go/pkg/adapters/vertex/vertex_anthropic_adapter.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/ui/ConversationRenderer.tsx`

## `unified_completion` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/server.go`
- `mcp-agent-builder-go/agent_go/cmd/server/structured_completion_persistence.go`
- `mcp-agent-builder-go/agent_go/cmd/server/terminal_owner_reconciliation.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/context_aware_bridge.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_identity.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/products/sparkquill/api/platform/events.ts`
- `mcp-agent-builder-go/frontend/src/constants/runningWorkflows.ts`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/stores/useWorkflowStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/workflowEventProcessor.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/utils/chatDeliveryTelemetry.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/TerminalEventTranscript.tsx`
- `mcp-agent-builder-go/frontend/src/components/workflow/workflowChatTabConversion.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `user_message` — LIVE

Producers:
- `mcpagent/agent/message_delivery.go`
- `mcpagent/agent/parallel_tool_execution.go`
- `mcpagent/agent/conversation.go`
- `mcpagent/agent/runtime_services.go`
- `mcpagent/agent/coding_session.go`
- `mcpagent/agent/turn_session.go`
- `mcpagent/agent/tool_loop_detector.go`
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/cmd/server/conversation_turn_queue.go`
- `mcp-agent-builder-go/agent_go/cmd/server/server.go`
- `mcp-agent-builder-go/agent_go/cmd/server/chat_history_persistence.go`
- `mcp-agent-builder-go/agent_go/cmd/server/bot_session_starter.go`
- `mcp-agent-builder-go/agent_go/cmd/server/codex_native_transcript_sync.go`
- `mcp-agent-builder-go/agent_go/cmd/server/background_agents.go`
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
- `mcp-agent-builder-go/agent_go/cmd/server/polling.go`
- `mcp-agent-builder-go/agent_go/pkg/agentwrapper/llm_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/context_aware_bridge.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_delegation.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/execution_only_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/kb_update_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/base_orchestrator_agent.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/background_transcript.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/events/data.go`
- `mcp-agent-builder-go/agent_go/internal/terminals/store.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_identity.go`
- `multi-llm-provider-go/pkg/adapters/codexcli/codexcli_durable_ack.go`
- `multi-llm-provider-go/pkg/adapters/azure/azure_adapter.go`
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_durable_ack.go`
- `multi-llm-provider-go/pkg/adapters/picli/picli_durable_ack.go`
- `multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`
- `multi-llm-provider-go/pkg/adapters/anthropic/anthropic_adapter.go`
- `multi-llm-provider-go/pkg/adapters/openai/openai_adapter.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/products/sparkquill/api/platform/events.ts`
- `mcp-agent-builder-go/frontend/src/products/work/WorkModelsPanel.tsx`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/utils/internalChatEvents.ts`
- `mcp-agent-builder-go/frontend/src/utils/liveInputSubmission.ts`
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
- `mcp-agent-builder-go/frontend/src/utils/chatSubmitHelpers.ts`
- `mcp-agent-builder-go/frontend/src/utils/sessionRestore.ts`
- `mcp-agent-builder-go/frontend/src/utils/chatDeliveryTelemetry.ts`
- `mcp-agent-builder-go/frontend/src/utils/stepConfigMatching.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatArea.tsx`
- `mcp-agent-builder-go/frontend/src/components/TerminalEventTranscript.tsx`
- `mcp-agent-builder-go/frontend/src/components/workflow/nodes/MessageSequenceNode.tsx`
- `mcp-agent-builder-go/frontend/src/components/workflow/canvas/WorkflowCanvas.tsx`
- `mcp-agent-builder-go/frontend/src/components/workflow/executionLogs/LogPrimitives.tsx`
- `mcp-agent-builder-go/frontend/src/components/workflow/workflowChatTabConversion.ts`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`
- `mcp-agent-builder-go/frontend/src/components/ChatInput.tsx`
- `mcp-agent-builder-go/frontend/src/services/api-types.ts`

## `variables_extracted` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/schema-gen/main.go`
- `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/variable_management.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`

## `viewer_control` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/playwright_live.go`
- `mcp-agent-builder-go/agent_go/cmd/server/browser_live.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/workflow/WorkflowLiveBrowser.tsx`

## `viewer_error` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/playwright_live.go`
- `mcp-agent-builder-go/agent_go/cmd/server/browser_live.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/components/workflow/WorkflowLiveBrowser.tsx`

## `work_identity_updated` — RETENTION-ONLY, FILTERED-BY-DESIGN

Producers: none found
Consumers:
- `mcp-agent-builder-go/frontend/src/products/work/WorkSurface.tsx`

## `work_workflow_references_updated` — FILTERED-BY-DESIGN

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/work_workflow_reference_tools.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/products/work/WorkSurface.tsx`

## `workflow_end` — REMOVED

Deleted 2026-09-22 (batch 4) with the workflow_start/progress/end trio: never emitted (`orchestrator_end` is the live completion signal; `workflow_error` is separately live and kept). Removed: STRUCTURAL entry, tree labels/arms, polling arm, dispatcher branch + `WorkflowEndEvent.tsx`, hand-written data interface, union entries, store completion/retention lists, rail-denylist entry. Historical rows covered by the `LEGACY_REMOVED_EVENT_TYPES` tombstone.

## `workflow_error` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/session_execution_tree.go`
- `mcp-agent-builder-go/agent_go/cmd/server/server.go`
- `mcp-agent-builder-go/agent_go/cmd/server/session_activity_tree.go`
- `mcp-agent-builder-go/agent_go/cmd/server/chat_history_routes.go`
- `mcp-agent-builder-go/agent_go/cmd/server/polling.go`
- `mcp-agent-builder-go/agent_go/internal/events/event_store.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/constants/runningWorkflows.ts`
- `mcp-agent-builder-go/frontend/src/stores/useChatStore.ts`
- `mcp-agent-builder-go/frontend/src/components/TerminalCenter.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/EventDispatcher.tsx`
- `mcp-agent-builder-go/frontend/src/components/events/eventModeUtils.ts`

## `workflow_progress` — REMOVED

Deleted 2026-09-22 (batch 4) with the workflow_start/progress/end trio: never emitted (no struct, no constructor, no emit call). Removed: const, STRUCTURAL + LIFECYCLE-family entries, terminal-status + title/label match arms, progress % extraction, dispatcher branch + `WorkflowProgressEvent.tsx`, RunningWorkflows store arm (no store wired the type), union entries, rail-denylist entry. Historical rows covered by the `LEGACY_REMOVED_EVENT_TYPES` tombstone.

## `workflow_start` — REMOVED

Deleted 2026-09-22 (batch 4) with the workflow_start/progress/end trio: never emitted (no struct, no constructor, no emit call). Removed: const, STRUCTURAL + LIFECYCLE-family entries, terminal-status + title/label match arms, dispatcher branch + `WorkflowStartEvent.tsx`, store start-detection/retention arms, union entries, rail-denylist entry. Historical rows covered by the `LEGACY_REMOVED_EVENT_TYPES` tombstone.

## `workflow_step_completed` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`

## `workflow_step_started` — LIVE

Producers:
- `mcp-agent-builder-go/agent_go/cmd/server/services/bot_event_filter.go`
Consumers:
- `mcp-agent-builder-go/frontend/src/utils/cleanConversation.ts`
