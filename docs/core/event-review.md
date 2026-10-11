# Kept events — manual review checklist

Generated 2026-09-22 from `event-catalog.md` after deletion batches 1+2; updated same day for batches 3+4.
75 kept events (60 REMOVED tombstones excluded; batch-5 removed the 3 summarization events with the whole feature). Batch 3 removed 10 LIVE lines: `context_canceled` (misspelling), `context_editing_completed`, `context_editing_error`, `learning_completed`, `learning_failed`, `learning_skipped`, `orchestrator_error`, `orchestrator_start`, `throttling_detected`, `token_limit_exceeded`.
Full producer/consumer file lists live in the catalog; this is the condensed ballot.
Legend: LIVE = produced and consumed; FILTERED-BY-DESIGN = dropped from transcript on purpose;
BRIDGE-SKIPPED = produced then dropped at the event bridge; OBSERVABILITY-ONLY = tracer spans, never on the wire;
TELEMETRY-DEMOTE = emitted, never read, should be logs; DEAD = neither produced nor consumed (kept consts — delete candidates);
NEVER-DISPLAYED = hidden/nulled; RETENTION-ONLY = kept in store, never rendered.

## LIVE (53)

- [ ] `agent_end` — P: mcp/agent/agent.go, go/cmd/schema-gen/main.go, go/cmd/server/services/bot_event_filter.go, go/pkg/orchestrator/context_aware_bridge.go (+7 more) | C: fe/src/constants/runningWorkflows.ts, fe/src/stores/useChatStore.ts, fe/src/stores/useWorkflowStore.ts, fe/src/utils/workflowEventProcessor.ts (+3 more)
- [ ] `agent_error` — P: go/cmd/schema-gen/main.go, go/cmd/server/event_bridge/base_bridge.go, go/cmd/server/services/bot_event_filter.go, go/pkg/agentwrapper/llm_agent.go (+6 more) | C: fe/src/constants/runningWorkflows.ts, fe/src/stores/useChatStore.ts, fe/src/stores/useWorkflowStore.ts, fe/src/utils/cleanConversation.ts (+3 more)
- [ ] `agent_start` — P: mcp/agent/agent.go, go/cmd/schema-gen/main.go, go/cmd/server/background_agent_transcript.go, go/cmd/server/background_agents.go (+16 more) | C: fe/src/stores/useRunningWorkflowsStore.ts, fe/src/utils/workflowEventProcessor.ts, fe/src/utils/cleanConversation.ts, fe/src/components/events/EventDispatcher.tsx (+1 more)
- [ ] `auto_notification_steered` — P: go/cmd/schema-gen/main.go, go/pkg/orchestrator/events/types.go, go/internal/events/event_store.go | C: fe/src/components/events/EventDispatcher.tsx
- [ ] `background_agent_completed` — P: go/cmd/schema-gen/main.go, go/cmd/server/session_execution_tree.go, go/cmd/server/chat_history_routes.go, go/cmd/testing/coding_agent_background_e2e.go (+3 more) | C: fe/src/stores/useChatStore.ts, fe/src/components/ChatArea.tsx, fe/src/components/TerminalEventTranscript.tsx, fe/src/components/events/EventDispatcher.tsx (+1 more)
- [ ] `background_agent_started` — P: go/cmd/schema-gen/main.go, go/cmd/server/chat_history_routes.go, go/cmd/testing/workflow_auto_notification_e2e.go, go/pkg/orchestrator/events/types.go (+2 more) | C: fe/src/stores/useChatStore.ts, fe/src/utils/cleanConversation.ts, fe/src/components/events/EventDispatcher.tsx, fe/src/components/events/eventModeUtils.ts
- [ ] `background_agent_terminated` — P: go/cmd/schema-gen/main.go, go/cmd/server/session_execution_tree.go, go/cmd/server/chat_history_routes.go, go/pkg/orchestrator/events/types.go (+1 more) | C: fe/src/stores/useChatStore.ts, fe/src/components/events/EventDispatcher.tsx, fe/src/components/events/eventModeUtils.ts
- [ ] `batch_execution_canceled` — P: go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_batch_execution.go, go/pkg/orchestrator/events/data.go | C: fe/src/stores/useChatStore.ts, fe/src/components/events/EventDispatcher.tsx
- [ ] `blocking_human_feedback` — P: go/cmd/schema-gen/main.go, go/cmd/server/server.go, go/cmd/server/services/bot_connector.go, go/cmd/server/virtual-tools/human_tools.go (+4 more) | C: fe/src/constants/runningWorkflows.ts, fe/src/stores/useRunningWorkflowsStore.ts, fe/src/stores/useChatStore.ts, fe/src/utils/humanFeedbackAttention.ts (+2 more)
- [ ] `broken_pipe` — P: mcp/agent/parallel_tool_execution.go, mcp/agent/conversation.go, mcp/agent/error_handler.go | C: fe/src/components/events/EventDispatcher.tsx
- [ ] `context_cancelled` — P: mcp/agent/llm_generation.go, mcp/agent/parallel_tool_execution.go, mcp/agent/conversation.go, go/cmd/schema-gen/main.go (+2 more) | C: fe/src/stores/useChatStore.ts, fe/src/utils/cleanConversation.ts, fe/src/components/ChatArea.tsx, fe/src/components/events/EventDispatcher.tsx (+1 more)
- [ ] `conversation_error` — P: mcp/agent/parallel_tool_execution.go, mcp/agent/conversation.go, mcp/agent/tool_loop_detector.go, go/cmd/schema-gen/main.go (+3 more) | C: fe/src/constants/runningWorkflows.ts, fe/src/stores/useChatStore.ts, fe/src/stores/useWorkflowStore.ts, fe/src/utils/cleanConversation.ts (+5 more)
- [ ] `conversation_resumed` — P: go/internal/events/event_store.go | C: fe/src/stores/useChatStore.ts, fe/src/utils/chatSubmitHelpers.ts, fe/src/utils/sessionRestore.ts, fe/src/components/ChatArea.tsx (+2 more)
- [ ] `conversation_thinking` — P: mcp/agent/llm_generation.go, go/cmd/schema-gen/main.go | C: fe/src/utils/thinkingDeltas.ts, fe/src/utils/cleanConversation.ts, fe/src/utils/chatDeliveryTelemetry.ts, fe/src/components/events/EventDispatcher.tsx
- [ ] `decision_request_missing` — P: none | C: fe/src/components/workflow/pulseFindingPresentation.ts
- [ ] `delegation_end` — P: go/cmd/server/session_execution_tree.go, go/cmd/server/delegation.go, go/cmd/server/services/bot_event_filter.go, go/internal/events/event_store.go | C: fe/src/stores/useChatStore.ts, fe/src/components/ChatArea.tsx, fe/src/components/events/EventDispatcher.tsx, fe/src/components/events/eventModeUtils.ts
- [ ] `delegation_start` — P: go/cmd/server/delegation.go, go/cmd/server/services/bot_event_filter.go, go/internal/terminals/store.go, go/internal/events/event_store.go | C: fe/src/stores/useChatStore.ts, fe/src/components/events/EventDispatcher.tsx, fe/src/components/events/eventModeUtils.ts
- [ ] `fix_applied` — P: go/pkg/orchestrator/agents/workflow/step_based_workflow/pulse_finding_lifecycle.go | C: fe/src/components/workflow/pulseFindingPresentation.ts
- [ ] `human_input` — P: go/cmd/agentworks/main.go, go/cmd/server/terminal_routes.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/consolidated_plan_tools.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_agent.go (+1 more) | C: fe/src/utils/stepConfigMatching.ts, fe/src/components/ChatArea.tsx, fe/src/components/workflow/canvas/WorkflowCanvas.tsx, fe/src/components/workflow/executionLogs/helpers.tsx (+1 more)
- [ ] `large_tool_output_detected` — P: mcp/agent/parallel_tool_execution.go, mcp/agent/conversation.go, go/cmd/schema-gen/main.go | C: fe/src/components/ChatArea.tsx, fe/src/components/events/EventDispatcher.tsx
- [ ] `large_tool_output_file_written` — P: mcp/agent/parallel_tool_execution.go, mcp/agent/conversation.go, go/cmd/schema-gen/main.go | C: fe/src/components/ChatArea.tsx, fe/src/components/events/EventDispatcher.tsx
- [ ] `learn_code_script_execution` — P: go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_progress.go (`emitScriptedExecutionEvent`, 4 call sites), go/cmd/schema-gen/main.go, go/internal/events/event_store.go (STRUCTURAL) | C: fe/src/stores/useChatStore.ts, fe/src/components/ChatArea.tsx, fe/src/components/events/EventDispatcher.tsx, fe/src/components/events/eventModeUtils.ts
- [ ] `live_input_confirmed` — P: go/cmd/server/conversation_turn_queue.go, go/cmd/server/live_input_durable.go | C: fe/src/utils/liveInputReceipt.ts, fe/src/components/ChatArea.tsx
- [ ] `llm_generation_end` — P: mcp/agent/conversation.go, mcp/agent/agent.go, go/cmd/schema-gen/main.go, go/cmd/server/scheduled_turn_outcome.go (+4 more) | C: fe/src/products/sparkquill/api/platform/events.ts, fe/src/stores/useRunningWorkflowsStore.ts, fe/src/stores/useChatStore.ts, fe/src/utils/cleanConversation.ts (+5 more)
- [ ] `llm_generation_error` — P: mcp/agent/conversation.go, go/cmd/schema-gen/main.go, go/pkg/orchestrator/context_aware_bridge.go | C: fe/src/components/TerminalCenter.tsx, fe/src/components/events/EventDispatcher.tsx
- [ ] `max_turns_reached` — P: mcp/agent/conversation.go, go/cmd/schema-gen/main.go | C: fe/src/components/events/EventDispatcher.tsx
- [ ] `orchestrator_agent_end` — P: go/cmd/schema-gen/main.go, go/cmd/server/services/bot_event_filter.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_exports.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go (+3 more) | C: fe/src/stores/useRunningWorkflowsStore.ts, fe/src/stores/useChatStore.ts, fe/src/components/ChatArea.tsx, fe/src/components/TerminalEventTranscript.tsx (+2 more)
- [ ] `orchestrator_agent_error` — P: go/cmd/schema-gen/main.go, go/cmd/server/event_bridge/base_bridge.go, go/cmd/server/services/bot_event_filter.go, go/pkg/orchestrator/base_orchestrator_events.go (+4 more) | C: fe/src/stores/useChatStore.ts, fe/src/components/TerminalCenter.tsx, fe/src/components/events/EventDispatcher.tsx

- [ ] `orchestrator_end` — P: go/cmd/schema-gen/main.go, go/pkg/orchestrator/types/workflow_orchestrator.go, go/pkg/orchestrator/base_orchestrator_events.go, go/pkg/orchestrator/events/data.go | C: fe/src/constants/runningWorkflows.ts, fe/src/stores/useChatStore.ts, fe/src/stores/useWorkflowStore.ts, fe/src/components/events/EventDispatcher.tsx
- [ ] `plan_approval` — P: go/cmd/server/chat_history_routes.go, go/cmd/server/services/bot_connector.go, go/cmd/server/services/bot_event_filter.go, go/cmd/server/polling.go (+2 more) | C: fe/src/stores/useRunningWorkflowsStore.ts, fe/src/stores/useChatStore.ts, fe/src/components/events/EventDispatcher.tsx, fe/src/components/events/eventModeUtils.ts
- [ ] `pre_validation_completed` — P: go/cmd/schema-gen/main.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_progress.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent.go (+2 more) | C: fe/src/stores/useChatStore.ts, fe/src/utils/cleanConversation.ts, fe/src/components/TerminalCenter.tsx, fe/src/components/events/EventDispatcher.tsx (+1 more)
- [ ] `presentation_updated` — P: go/cmd/schema-gen/main.go, go/pkg/orchestrator/events/types.go | C: fe/src/components/TerminalEventTranscript.tsx
- [ ] `request_human_feedback` — P: go/cmd/schema-gen/main.go, go/pkg/orchestrator/base_orchestrator_feedback.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_human_input.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/execution_manager.go (+1 more) | C: fe/src/constants/runningWorkflows.ts, fe/src/stores/useRunningWorkflowsStore.ts, fe/src/stores/useChatStore.ts, fe/src/stores/useWorkflowStore.ts (+3 more)
- [ ] `retry_attempt` — P: mcp/agent/llm_generation.go, go/cmd/schema-gen/main.go, go/cmd/server/virtual-tools/sub_agent_tools.go, go/pkg/pulseintake/runtime.go (+1 more) | C: fe/src/components/events/EventDispatcher.tsx
- [ ] `routing_evaluated` — P: go/cmd/schema-gen/main.go, go/cmd/server/session_activity_tree.go, go/pkg/orchestrator/events/types.go, go/internal/events/event_store.go | C: fe/src/stores/useChatStore.ts, fe/src/utils/cleanConversation.ts, fe/src/components/TerminalCenter.tsx, fe/src/components/events/EventDispatcher.tsx
- [ ] `step_token_usage` — P: go/cmd/schema-gen/main.go, go/pkg/orchestrator/base_orchestrator_types.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_crew.go (+3 more) | C: fe/src/components/events/EventDispatcher.tsx
- [ ] `synthetic_turn_ready` — P: go/cmd/schema-gen/main.go, go/pkg/orchestrator/events/types.go | C: fe/src/components/events/EventDispatcher.tsx
- [ ] `take_control` — P: go/cmd/server/browser_live.go | C: fe/src/components/workflow/WorkflowLiveBrowser.tsx
- [ ] `todo_task_route_selected` — P: go/cmd/server/session_activity_tree.go, go/pkg/orchestrator/events/types.go, go/internal/events/event_store.go | C: fe/src/stores/useChatStore.ts, fe/src/utils/cleanConversation.ts, fe/src/components/events/EventDispatcher.tsx, fe/src/components/events/eventModeUtils.ts
- [ ] `todo_task_step_completed` — P: go/cmd/server/session_execution_tree.go, go/pkg/orchestrator/events/types.go, go/internal/terminals/store.go, go/internal/events/event_store.go | C: fe/src/stores/useChatStore.ts, fe/src/components/ChatArea.tsx, fe/src/components/workflow/WorkflowLayout.tsx, fe/src/components/events/EventDispatcher.tsx (+1 more)
- [ ] `tool_call` — P: go/cmd/server/server.go, go/cmd/server/session_activity_tree.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go, go/pkg/orchestrator/events/background_transcript.go (+1 more) | C: fe/src/stores/useChatStore.ts, fe/src/components/ui/ConversationRenderer.tsx, fe/src/components/workflow/executionLogs/LogPrimitives.tsx, fe/src/services/api-types.ts
- [ ] `tool_call_end` — P: mcp/agent/llm_generation.go, mcp/agent/tool_registry.go, mcp/agent/parallel_tool_execution.go, mcp/agent/conversation.go (+22 more) | C: fe/src/stores/useRunningWorkflowsStore.ts, fe/src/utils/workflowEventProcessor.ts, fe/src/utils/decisionRefresh.ts, fe/src/utils/secretMutationRefresh.ts (+1 more)
- [ ] `tool_call_error` — P: mcp/agent/llm_generation.go, mcp/agent/tool_registry.go, mcp/agent/parallel_tool_execution.go, mcp/agent/conversation.go (+5 more) | C: fe/src/components/TerminalCenter.tsx, fe/src/components/events/EventDispatcher.tsx
- [ ] `tool_call_start` — P: mcp/agent/llm_generation.go, mcp/agent/tool_registry.go, mcp/agent/layer2_certification.go, mcp/agent/parallel_tool_execution.go (+24 more) | C: fe/src/stores/useRunningWorkflowsStore.ts, fe/src/utils/sessionRestore.ts, fe/src/components/events/EventDispatcher.tsx
- [ ] `tool_result` — P: go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go, prov/pkg/adapters/claudecode/claudecode_tool_results.go, prov/pkg/adapters/claudecode/claudecode_transcript_messages.go, prov/pkg/adapters/claudecode/claudecode_transcript_stream.go (+3 more) | C: fe/src/stores/useChatStore.ts, fe/src/components/ui/ConversationRenderer.tsx
- [ ] `unified_completion` — P: go/cmd/schema-gen/main.go, go/cmd/server/server.go, go/cmd/server/structured_completion_persistence.go, go/cmd/server/terminal_owner_reconciliation.go (+2 more) | C: fe/src/products/sparkquill/api/platform/events.ts, fe/src/constants/runningWorkflows.ts, fe/src/stores/useChatStore.ts, fe/src/stores/useWorkflowStore.ts (+8 more)
- [ ] `user_message` — P: mcp/agent/message_delivery.go, mcp/agent/parallel_tool_execution.go, mcp/agent/conversation.go, mcp/agent/runtime_services.go (+32 more) | C: fe/src/products/sparkquill/api/platform/events.ts, fe/src/products/work/WorkModelsPanel.tsx, fe/src/stores/useChatStore.ts, fe/src/utils/internalChatEvents.ts (+16 more)
- [ ] `variables_extracted` — P: go/cmd/schema-gen/main.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/variable_management.go | C: fe/src/components/events/EventDispatcher.tsx
- [ ] `viewer_control` — P: go/cmd/server/playwright_live.go, go/cmd/server/browser_live.go | C: fe/src/components/workflow/WorkflowLiveBrowser.tsx
- [ ] `viewer_error` — P: go/cmd/server/playwright_live.go, go/cmd/server/browser_live.go | C: fe/src/components/workflow/WorkflowLiveBrowser.tsx
- [ ] `workflow_error` — P: go/cmd/server/session_execution_tree.go, go/cmd/server/server.go, go/cmd/server/session_activity_tree.go, go/cmd/server/chat_history_routes.go (+2 more) | C: fe/src/constants/runningWorkflows.ts, fe/src/stores/useChatStore.ts, fe/src/components/TerminalCenter.tsx, fe/src/components/events/EventDispatcher.tsx (+1 more)
- [ ] `workflow_step_completed` — P: go/cmd/server/services/bot_event_filter.go | C: fe/src/utils/cleanConversation.ts
- [ ] `workflow_step_started` — P: go/cmd/server/services/bot_event_filter.go | C: fe/src/utils/cleanConversation.ts

## FILTERED-BY-DESIGN (12)

- [ ] `conversation_end` — P: mcp/agent/conversation.go, mcp/agent/agent.go, go/cmd/schema-gen/main.go, go/internal/events/event_identity.go | C: fe/src/constants/runningWorkflows.ts, fe/src/stores/useRunningWorkflowsStore.ts, fe/src/stores/useChatStore.ts, fe/src/stores/useWorkflowStore.ts (+5 more)
- [ ] `conversation_start` — P: mcp/agent/conversation.go, mcp/agent/agent.go, go/cmd/schema-gen/main.go | C: fe/src/stores/useChatStore.ts, fe/src/components/events/EventDispatcher.tsx, fe/src/components/events/eventModeUtils.ts
- [ ] `conversation_turn` — P: mcp/agent/conversation.go, mcp/agent/agent.go, go/cmd/schema-gen/main.go, go/cmd/server/agent_profile_routes.go (+11 more) | C: fe/src/components/events/EventDispatcher.tsx, fe/src/components/events/eventModeUtils.ts
- [ ] `llm_generation_start` — P: mcp/agent/agent.go, go/cmd/schema-gen/main.go, go/pkg/orchestrator/context_aware_bridge.go | C: fe/src/components/events/EventDispatcher.tsx, fe/src/components/events/eventModeUtils.ts
- [ ] `llm_generation_with_retry` — P: mcp/agent/llm_generation.go, go/cmd/schema-gen/main.go | C: fe/src/components/events/EventDispatcher.tsx, fe/src/components/events/eventModeUtils.ts
- [ ] `product_interaction` — P: go/cmd/schema-gen/main.go, go/pkg/orchestrator/events/types.go | C: fe/src/products/sparkquill/api/platform/events.ts, fe/src/components/TerminalEventTranscript.tsx
- [ ] `status_line` — P: mcp/agent/llm_generation.go, go/cmd/server/services/bot_event_filter.go, go/internal/terminals/store.go | C: fe/src/components/events/EventDispatcher.tsx
- [ ] `streaming_chunk` — P: mcp/agent/turn_session_progress.go, mcp/agent/llm_generation.go, mcp/agent/agent.go, go/cmd/schema-gen/main.go (+11 more) | C: fe/src/components/ChatArea.tsx
- [ ] `streaming_end` — P: mcp/agent/llm_generation.go, mcp/agent/agent.go, go/cmd/schema-gen/main.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_progress.go (+1 more) | C: fe/src/components/ChatArea.tsx
- [ ] `streaming_start` — P: mcp/agent/llm_generation.go, go/cmd/schema-gen/main.go | C: fe/src/components/ChatArea.tsx
- [ ] `token_usage` — P: mcp/agent/context_summarization.go, mcp/agent/convrecord/convrecord.go, mcp/agent/llm_generation.go, mcp/agent/parallel_tool_execution.go (+34 more) | C: fe/src/products/video-studio/projectAgentUsage.ts, fe/src/utils/cleanConversation.ts, fe/src/components/ChatArea.tsx, fe/src/components/events/EventDispatcher.tsx
- [ ] `work_workflow_references_updated` — P: go/cmd/server/work_workflow_reference_tools.go | C: fe/src/products/work/WorkSurface.tsx

## NEVER-DISPLAYED, FILTERED-BY-DESIGN (1)

- [ ] `system_prompt` — P: mcp/agent/llm_generation.go, mcp/agent/layer2_certification.go, mcp/agent/conversation.go, mcp/agent/effective_system_prompt.go (+48 more) | C: none

## RETENTION-ONLY, FILTERED-BY-DESIGN (1)

- [ ] `work_identity_updated` — P: none | C: fe/src/products/work/WorkSurface.tsx

## RETENTION-ONLY (1)

- [ ] `orchestrator_agent_start` — P: go/pkg/orchestrator/agents/base_orchestrator_agent.go, go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go (5 sites), go/pkg/orchestrator/llm/base_llm.go (fallback) | C: fe stores (retention/heartbeat/restore), go bot narration + planning match, STRUCTURAL. Card removed 2026-09-22 (dispatcher returns null); never painted anywhere.

## BRIDGE-SKIPPED (1)

- [ ] `tool_execution` — P: mcp/agent/tool_registry.go, mcp/agent/parallel_tool_execution.go, mcp/agent/conversation.go, mcp/agent/definition.go (+22 more) | C: fe/src/utils/decisionRefresh.ts, fe/src/utils/secretMutationRefresh.ts, fe/src/components/ChatArea.tsx

## BRIDGE-SKIPPED, FILTERED-BY-DESIGN (1)

- [ ] `cache_event` — P: none | C: mcp/observability/*` tracers (Langfuse/LangSmith spans)

## OBSERVABILITY-ONLY (3)

- [ ] `comprehensive_cache_event` — P: mcp/mcpcache/integration.go` (`EmitComprehensiveCacheEvent`, called from `agent/conversation.go` and cache connect paths) | C: mcp/observability/*` tracers only. The streaming tracer forwards to SSE solely for `*events.AgentEvent`; this is a `*mcpcache.ComprehensiveCacheEvent`, so it is type-assertion-filtered and never reaches the wire. Dedicated renderer (`ComprehensiveCacheEvent.tsx`) deleted 2026-09-22; also listed in bridge `SKIP_EVENTS` / store `NEVER_SHOW_EVENTS` as belt-and-braces.
- [ ] `mcp_server_connection_end` — P: mcp/agent/connection_session.go, go/cmd/schema-gen/main.go | C: mcp/observability/langfuse_tracer.go`, `langsmith_tracer.go` (connection spans)
- [ ] `mcp_server_connection_start` — P: mcp/agent/connection_session.go, go/cmd/schema-gen/main.go | C: mcp/observability/langfuse_tracer.go`, `langsmith_tracer.go` (connection spans)

## TELEMETRY-DEMOTE (1)

- [ ] `large_tool_output_file_write_error` — P: mcp/agent/parallel_tool_execution.go, mcp/agent/conversation.go, go/cmd/schema-gen/main.go | C: none

## NEVER-DISPLAYED (1)

- [ ] `mcp_server_selection` — P: go/cmd/schema-gen/main.go, go/cmd/server/work_mcp_selection_tools.go, go/pkg/agentwrapper/llm_agent.go | C: fe/src/components/events/EventDispatcher.tsx

