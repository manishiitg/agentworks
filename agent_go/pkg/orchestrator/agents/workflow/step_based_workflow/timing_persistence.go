package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowrun"
)

//nolint:unused // staged for the run-metadata timing persistence rollout.
type persistedToolCallTiming struct {
	ToolCallID             string `json:"tool_call_id,omitempty"`
	ToolName               string `json:"tool_name"`
	Status                 string `json:"status"`
	Args                   string `json:"args,omitempty"`
	Result                 string `json:"result,omitempty"`
	Error                  string `json:"error,omitempty"`
	ArgsBytes              int    `json:"args_bytes,omitempty"`
	ResultBytes            int    `json:"result_bytes,omitempty"`
	ErrorBytes             int    `json:"error_bytes,omitempty"`
	EstimatedArgsTokens    int    `json:"estimated_args_tokens,omitempty"`
	EstimatedResultTokens  int    `json:"estimated_result_tokens,omitempty"`
	EstimatedErrorTokens   int    `json:"estimated_error_tokens,omitempty"`
	DurationNs             int64  `json:"duration_ns,omitempty"`
	DurationMs             int64  `json:"duration_ms,omitempty"`
	Timestamp              string `json:"timestamp,omitempty"`
	StartedAt              string `json:"started_at,omitempty"`
	CompletedAt            string `json:"completed_at,omitempty"`
	OffsetFromAgentStartMs int64  `json:"offset_from_agent_start_ms,omitempty"`
}

//nolint:unused // staged for the run-metadata timing persistence rollout.
type persistedToolTimingSummary struct {
	Count           int                       `json:"count"`
	SuccessfulCount int                       `json:"successful_count"`
	ErroredCount    int                       `json:"errored_count"`
	TotalDurationNs int64                     `json:"total_duration_ns"`
	TotalDurationMs int64                     `json:"total_duration_ms"`
	MaxDurationNs   int64                     `json:"max_duration_ns,omitempty"`
	MaxDurationMs   int64                     `json:"max_duration_ms,omitempty"`
	AvgDurationMs   int64                     `json:"avg_duration_ms,omitempty"`
	Calls           []persistedToolCallTiming `json:"calls,omitempty"`
}

type persistedLLMCallTiming struct {
	Turn                   int     `json:"turn,omitempty"`
	ModelID                string  `json:"model_id,omitempty"`
	Status                 string  `json:"status"`
	Error                  string  `json:"error,omitempty"`
	StartedAt              string  `json:"started_at,omitempty"`
	CompletedAt            string  `json:"completed_at,omitempty"`
	DurationNs             int64   `json:"duration_ns,omitempty"`
	DurationMs             int64   `json:"duration_ms,omitempty"`
	TimeToFirstResponseMs  int64   `json:"time_to_first_response_ms,omitempty"`
	TimeToFirstContentMs   int64   `json:"time_to_first_content_ms,omitempty"`
	TimeToFirstToolCallMs  int64   `json:"time_to_first_tool_call_ms,omitempty"`
	FirstResponseAt        string  `json:"first_response_at,omitempty"`
	FirstContentAt         string  `json:"first_content_at,omitempty"`
	FirstToolCallAt        string  `json:"first_tool_call_at,omitempty"`
	PromptTokens           int     `json:"prompt_tokens,omitempty"`
	CompletionTokens       int     `json:"completion_tokens,omitempty"`
	TotalTokens            int     `json:"total_tokens,omitempty"`
	CacheTokens            int     `json:"cache_tokens,omitempty"`
	ReasoningTokens        int     `json:"reasoning_tokens,omitempty"`
	ToolCalls              int     `json:"tool_calls,omitempty"`
	ContextUsagePercent    float64 `json:"context_usage_percent,omitempty"`
	ModelContextWindow     int     `json:"model_context_window,omitempty"`
	FixedThresholdPercent  float64 `json:"fixed_threshold_percent,omitempty"`
	OffsetFromAgentStartMs int64   `json:"offset_from_agent_start_ms,omitempty"`
}

type persistedLLMTimingSummary struct {
	Count                 int                      `json:"count"`
	SuccessfulCount       int                      `json:"successful_count"`
	ErroredCount          int                      `json:"errored_count"`
	CanceledCount         int                      `json:"canceled_count"`
	TotalDurationNs       int64                    `json:"total_duration_ns"`
	TotalDurationMs       int64                    `json:"total_duration_ms"`
	MaxDurationNs         int64                    `json:"max_duration_ns,omitempty"`
	MaxDurationMs         int64                    `json:"max_duration_ms,omitempty"`
	AvgDurationMs         int64                    `json:"avg_duration_ms,omitempty"`
	FirstStartAt          string                   `json:"first_start_at,omitempty"`
	FirstStartOffsetMs    int64                    `json:"first_start_offset_ms,omitempty"`
	FirstResponseAt       string                   `json:"first_response_at,omitempty"`
	TimeToFirstResponseMs int64                    `json:"time_to_first_response_ms,omitempty"`
	FirstContentAt        string                   `json:"first_content_at,omitempty"`
	TimeToFirstContentMs  int64                    `json:"time_to_first_content_ms,omitempty"`
	FirstToolCallAt       string                   `json:"first_tool_call_at,omitempty"`
	TimeToFirstToolCallMs int64                    `json:"time_to_first_tool_call_ms,omitempty"`
	Calls                 []persistedLLMCallTiming `json:"calls,omitempty"`
}

type persistedTraceSpan struct {
	SpanID                 string                 `json:"span_id"`
	ParentSpanID           string                 `json:"parent_span_id,omitempty"`
	Type                   string                 `json:"type"`
	Name                   string                 `json:"name"`
	Status                 string                 `json:"status,omitempty"`
	StartedAt              string                 `json:"started_at,omitempty"`
	CompletedAt            string                 `json:"completed_at,omitempty"`
	OffsetFromAgentStartMs int64                  `json:"offset_from_agent_start_ms,omitempty"`
	DurationNs             int64                  `json:"duration_ns,omitempty"`
	DurationMs             int64                  `json:"duration_ms,omitempty"`
	InputTokens            int                    `json:"input_tokens,omitempty"`
	OutputTokens           int                    `json:"output_tokens,omitempty"`
	TotalTokens            int                    `json:"total_tokens,omitempty"`
	CacheTokens            int                    `json:"cache_tokens,omitempty"`
	ReasoningTokens        int                    `json:"reasoning_tokens,omitempty"`
	ToolCalls              int                    `json:"tool_calls,omitempty"`
	ArgsBytes              int                    `json:"args_bytes,omitempty"`
	ResultBytes            int                    `json:"result_bytes,omitempty"`
	ErrorBytes             int                    `json:"error_bytes,omitempty"`
	EstimatedArgsTokens    int                    `json:"estimated_args_tokens,omitempty"`
	EstimatedResultTokens  int                    `json:"estimated_result_tokens,omitempty"`
	EstimatedErrorTokens   int                    `json:"estimated_error_tokens,omitempty"`
	Error                  string                 `json:"error,omitempty"`
	Metadata               map[string]interface{} `json:"metadata,omitempty"`
}

type persistedTimingBreakdown struct {
	WallDurationMs         int64 `json:"wall_duration_ms"`
	LLMDurationMs          int64 `json:"llm_duration_ms"`
	ToolDurationMs         int64 `json:"tool_duration_ms"`
	TrackedUnionDurationMs int64 `json:"tracked_union_duration_ms"`
	UntrackedDurationMs    int64 `json:"untracked_duration_ms"`
	LLMCallCount           int   `json:"llm_call_count"`
	ToolCallCount          int   `json:"tool_call_count"`
	TotalInputTokens       int   `json:"total_input_tokens,omitempty"`
	TotalOutputTokens      int   `json:"total_output_tokens,omitempty"`
	TotalTokens            int   `json:"total_tokens,omitempty"`
	TotalCacheTokens       int   `json:"total_cache_tokens,omitempty"`
	TotalReasoningTokens   int   `json:"total_reasoning_tokens,omitempty"`
	ToolArgsBytes          int   `json:"tool_args_bytes,omitempty"`
	ToolResultBytes        int   `json:"tool_result_bytes,omitempty"`
	ToolErrorBytes         int   `json:"tool_error_bytes,omitempty"`
}

//nolint:unused // staged for the run-metadata timing persistence rollout.
func durationToMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return d.Milliseconds()
}

func approxTokensFromBytes(byteCount int) int {
	if byteCount <= 0 {
		return 0
	}
	// A coarse, model-independent estimate used only for diagnosing large tool
	// payloads before provider-side token accounting sees them in a later prompt.
	return (byteCount + 3) / 4
}

//nolint:unused // staged for the run-metadata timing persistence rollout.
func formatRFC3339UTC(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	// Nanosecond precision, not plain RFC3339's whole-second precision: a
	// completed_at value written at this resolution is used to order two
	// dispatches of the same step against each other (loadSingleStepResultFromLogs),
	// and two genuinely concurrent dispatches finishing within the same
	// wall-clock second would otherwise tie and fall back to attempt/iteration
	// counters that are not comparable across dispatches (PLAT-182 review).
	return t.UTC().Format(time.RFC3339Nano)
}

//nolint:unused // staged for the run-metadata timing persistence rollout.
func normalizeToolTimingEntries(toolCalls []orchestrator.ToolCallEntry, agentStartedAt time.Time) persistedToolTimingSummary {
	summary := persistedToolTimingSummary{
		Calls: make([]persistedToolCallTiming, 0, len(toolCalls)),
	}

	for _, call := range toolCalls {
		durationNs := int64(call.Duration)
		durationMs := durationToMillis(call.Duration)
		status := "success"
		if strings.TrimSpace(call.Error) != "" {
			status = "error"
			summary.ErroredCount++
		} else {
			summary.SuccessfulCount++
		}

		summary.Count++
		summary.TotalDurationNs += durationNs
		summary.TotalDurationMs += durationMs
		if durationNs > summary.MaxDurationNs {
			summary.MaxDurationNs = durationNs
			summary.MaxDurationMs = durationMs
		}

		summary.Calls = append(summary.Calls, persistedToolCallTiming{
			ToolCallID:             call.ToolCallID,
			ToolName:               call.ToolName,
			Status:                 status,
			Args:                   call.Args,
			Result:                 call.Result,
			Error:                  call.Error,
			ArgsBytes:              len([]byte(call.Args)),
			ResultBytes:            len([]byte(call.Result)),
			ErrorBytes:             len([]byte(call.Error)),
			EstimatedArgsTokens:    approxTokensFromBytes(len([]byte(call.Args))),
			EstimatedResultTokens:  approxTokensFromBytes(len([]byte(call.Result))),
			EstimatedErrorTokens:   approxTokensFromBytes(len([]byte(call.Error))),
			DurationNs:             durationNs,
			DurationMs:             durationMs,
			Timestamp:              formatRFC3339UTC(call.Timestamp),
			StartedAt:              formatRFC3339UTC(call.StartedAt),
			CompletedAt:            formatRFC3339UTC(call.CompletedAt),
			OffsetFromAgentStartMs: offsetFrom(agentStartedAt, call.StartedAt),
		})
	}

	if summary.Count > 0 {
		summary.AvgDurationMs = summary.TotalDurationMs / int64(summary.Count)
	}

	return summary
}

func buildTimingTrace(
	stepID string,
	agentName string,
	modelID string,
	agentStartedAt time.Time,
	agentCompletedAt time.Time,
	agentDuration time.Duration,
	llmTiming persistedLLMTimingSummary,
	toolTiming persistedToolTimingSummary,
) ([]persistedTraceSpan, persistedTimingBreakdown) {
	wallDurationMs := durationToMillis(agentDuration)
	agentSpanID := fmt.Sprintf("%s:agent", stepID)
	if agentName == "" {
		agentName = stepID
	}

	spans := make([]persistedTraceSpan, 0, 2+len(llmTiming.Calls)+len(toolTiming.Calls))
	spans = append(spans, persistedTraceSpan{
		SpanID:      agentSpanID,
		Type:        "agent",
		Name:        agentName,
		Status:      "completed",
		StartedAt:   formatRFC3339UTC(agentStartedAt),
		CompletedAt: formatRFC3339UTC(agentCompletedAt),
		DurationNs:  int64(agentDuration),
		DurationMs:  wallDurationMs,
		Metadata: map[string]interface{}{
			"model": modelID,
		},
	})

	breakdown := persistedTimingBreakdown{
		WallDurationMs: wallDurationMs,
		LLMDurationMs:  llmTiming.TotalDurationMs,
		ToolDurationMs: toolTiming.TotalDurationMs,
		LLMCallCount:   llmTiming.Count,
		ToolCallCount:  toolTiming.Count,
	}

	for i, call := range llmTiming.Calls {
		totalTokens := call.TotalTokens
		if totalTokens == 0 {
			totalTokens = call.PromptTokens + call.CompletionTokens + call.ReasoningTokens
		}
		breakdown.TotalInputTokens += call.PromptTokens
		breakdown.TotalOutputTokens += call.CompletionTokens
		breakdown.TotalTokens += totalTokens
		breakdown.TotalCacheTokens += call.CacheTokens
		breakdown.TotalReasoningTokens += call.ReasoningTokens

		spans = append(spans, persistedTraceSpan{
			SpanID:                 fmt.Sprintf("%s:llm:%d", stepID, i+1),
			ParentSpanID:           agentSpanID,
			Type:                   "llm",
			Name:                   call.ModelID,
			Status:                 call.Status,
			StartedAt:              call.StartedAt,
			CompletedAt:            call.CompletedAt,
			OffsetFromAgentStartMs: call.OffsetFromAgentStartMs,
			DurationNs:             call.DurationNs,
			DurationMs:             call.DurationMs,
			InputTokens:            call.PromptTokens,
			OutputTokens:           call.CompletionTokens,
			TotalTokens:            totalTokens,
			CacheTokens:            call.CacheTokens,
			ReasoningTokens:        call.ReasoningTokens,
			ToolCalls:              call.ToolCalls,
			Error:                  call.Error,
			Metadata: map[string]interface{}{
				"turn":                       call.Turn,
				"time_to_first_response_ms":  call.TimeToFirstResponseMs,
				"time_to_first_content_ms":   call.TimeToFirstContentMs,
				"time_to_first_tool_call_ms": call.TimeToFirstToolCallMs,
				"context_usage_percent":      call.ContextUsagePercent,
				"model_context_window":       call.ModelContextWindow,
				"fixed_threshold_percent":    call.FixedThresholdPercent,
			},
		})
	}

	for i, call := range toolTiming.Calls {
		breakdown.ToolArgsBytes += call.ArgsBytes
		breakdown.ToolResultBytes += call.ResultBytes
		breakdown.ToolErrorBytes += call.ErrorBytes

		spanID := call.ToolCallID
		if spanID == "" {
			spanID = fmt.Sprintf("%s:tool:%d", stepID, i+1)
		}
		spans = append(spans, persistedTraceSpan{
			SpanID:                 spanID,
			ParentSpanID:           agentSpanID,
			Type:                   "tool",
			Name:                   call.ToolName,
			Status:                 call.Status,
			StartedAt:              call.StartedAt,
			CompletedAt:            call.CompletedAt,
			OffsetFromAgentStartMs: call.OffsetFromAgentStartMs,
			DurationNs:             call.DurationNs,
			DurationMs:             call.DurationMs,
			ArgsBytes:              call.ArgsBytes,
			ResultBytes:            call.ResultBytes,
			ErrorBytes:             call.ErrorBytes,
			EstimatedArgsTokens:    call.EstimatedArgsTokens,
			EstimatedResultTokens:  call.EstimatedResultTokens,
			EstimatedErrorTokens:   call.EstimatedErrorTokens,
			Error:                  call.Error,
			Metadata: map[string]interface{}{
				"tool_call_id": call.ToolCallID,
			},
		})
	}

	breakdown.TrackedUnionDurationMs = computeTrackedUnionDurationMs(spans, agentSpanID, wallDurationMs)
	breakdown.UntrackedDurationMs = wallDurationMs - breakdown.TrackedUnionDurationMs
	if breakdown.UntrackedDurationMs < 0 {
		breakdown.UntrackedDurationMs = 0
	}
	if breakdown.UntrackedDurationMs > 0 {
		spans = append(spans, persistedTraceSpan{
			SpanID:       fmt.Sprintf("%s:untracked", stepID),
			ParentSpanID: agentSpanID,
			Type:         "overhead",
			Name:         "untracked orchestration / IO",
			Status:       "inferred",
			DurationMs:   breakdown.UntrackedDurationMs,
			Metadata: map[string]interface{}{
				"explanation":  "Wall-clock time not covered by captured LLM/tool spans. This usually includes prompt construction, orchestration, file IO, log persistence, validation bookkeeping, or instrumentation gaps.",
				"distribution": "unknown",
			},
		})
	}

	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].Type == "agent" {
			return true
		}
		if spans[j].Type == "agent" {
			return false
		}
		if spans[i].OffsetFromAgentStartMs != spans[j].OffsetFromAgentStartMs {
			return spans[i].OffsetFromAgentStartMs < spans[j].OffsetFromAgentStartMs
		}
		return spans[i].DurationMs > spans[j].DurationMs
	})

	return spans, breakdown
}

func computeTrackedUnionDurationMs(spans []persistedTraceSpan, agentSpanID string, wallDurationMs int64) int64 {
	type interval struct {
		start int64
		end   int64
	}

	intervals := make([]interval, 0, len(spans))
	for _, span := range spans {
		if span.ParentSpanID != agentSpanID || span.DurationMs <= 0 || (span.Type != "llm" && span.Type != "tool") {
			continue
		}
		start := span.OffsetFromAgentStartMs
		if start < 0 {
			start = 0
		}
		end := start + span.DurationMs
		if wallDurationMs > 0 && end > wallDurationMs {
			end = wallDurationMs
		}
		if end > start {
			intervals = append(intervals, interval{start: start, end: end})
		}
	}
	if len(intervals) == 0 {
		return 0
	}

	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start != intervals[j].start {
			return intervals[i].start < intervals[j].start
		}
		return intervals[i].end < intervals[j].end
	})

	total := int64(0)
	current := intervals[0]
	for _, next := range intervals[1:] {
		if next.start <= current.end {
			if next.end > current.end {
				current.end = next.end
			}
			continue
		}
		total += current.end - current.start
		current = next
	}
	total += current.end - current.start
	return total
}

func normalizeLLMTimingEntries(llmCalls []orchestrator.LLMCallEntry, agentStartedAt time.Time) persistedLLMTimingSummary {
	summary := persistedLLMTimingSummary{
		Calls: make([]persistedLLMCallTiming, 0, len(llmCalls)),
	}

	var firstStart time.Time
	var firstResponse time.Time
	var firstContent time.Time
	var firstToolCall time.Time

	for _, call := range llmCalls {
		durationNs := int64(call.Duration)
		durationMs := durationToMillis(call.Duration)

		switch call.Status {
		case "error":
			summary.ErroredCount++
		case "canceled":
			summary.CanceledCount++
		default:
			summary.SuccessfulCount++
		}

		summary.Count++
		summary.TotalDurationNs += durationNs
		summary.TotalDurationMs += durationMs
		if durationNs > summary.MaxDurationNs {
			summary.MaxDurationNs = durationNs
			summary.MaxDurationMs = durationMs
		}

		if !call.StartedAt.IsZero() && (firstStart.IsZero() || call.StartedAt.Before(firstStart)) {
			firstStart = call.StartedAt
		}
		if !call.FirstResponseAt.IsZero() && (firstResponse.IsZero() || call.FirstResponseAt.Before(firstResponse)) {
			firstResponse = call.FirstResponseAt
		}
		if !call.FirstContentAt.IsZero() && (firstContent.IsZero() || call.FirstContentAt.Before(firstContent)) {
			firstContent = call.FirstContentAt
		}
		if !call.FirstToolCallAt.IsZero() && (firstToolCall.IsZero() || call.FirstToolCallAt.Before(firstToolCall)) {
			firstToolCall = call.FirstToolCallAt
		}

		summary.Calls = append(summary.Calls, persistedLLMCallTiming{
			Turn:                   call.Turn,
			ModelID:                call.ModelID,
			Status:                 call.Status,
			Error:                  call.Error,
			StartedAt:              formatRFC3339UTC(call.StartedAt),
			CompletedAt:            formatRFC3339UTC(call.CompletedAt),
			DurationNs:             durationNs,
			DurationMs:             durationMs,
			TimeToFirstResponseMs:  durationToMillis(call.TimeToFirstResponse),
			TimeToFirstContentMs:   durationToMillis(call.TimeToFirstContent),
			TimeToFirstToolCallMs:  durationToMillis(call.TimeToFirstToolCall),
			FirstResponseAt:        formatRFC3339UTC(call.FirstResponseAt),
			FirstContentAt:         formatRFC3339UTC(call.FirstContentAt),
			FirstToolCallAt:        formatRFC3339UTC(call.FirstToolCallAt),
			PromptTokens:           call.PromptTokens,
			CompletionTokens:       call.CompletionTokens,
			TotalTokens:            call.TotalTokens,
			CacheTokens:            call.CacheTokens,
			ReasoningTokens:        call.ReasoningTokens,
			ToolCalls:              call.ToolCalls,
			ContextUsagePercent:    call.ContextUsagePercent,
			ModelContextWindow:     call.ModelContextWindow,
			FixedThresholdPercent:  call.FixedThresholdPercent,
			OffsetFromAgentStartMs: offsetFrom(agentStartedAt, call.StartedAt),
		})
	}

	if summary.Count > 0 {
		summary.AvgDurationMs = summary.TotalDurationMs / int64(summary.Count)
	}
	if !firstStart.IsZero() {
		summary.FirstStartAt = formatRFC3339UTC(firstStart)
		summary.FirstStartOffsetMs = offsetFrom(agentStartedAt, firstStart)
	}
	if !firstResponse.IsZero() {
		summary.FirstResponseAt = formatRFC3339UTC(firstResponse)
		summary.TimeToFirstResponseMs = offsetFrom(agentStartedAt, firstResponse)
	}
	if !firstContent.IsZero() {
		summary.FirstContentAt = formatRFC3339UTC(firstContent)
		summary.TimeToFirstContentMs = offsetFrom(agentStartedAt, firstContent)
	}
	if !firstToolCall.IsZero() {
		summary.FirstToolCallAt = formatRFC3339UTC(firstToolCall)
		summary.TimeToFirstToolCallMs = offsetFrom(agentStartedAt, firstToolCall)
	}

	return summary
}

func offsetFrom(start time.Time, target time.Time) int64 {
	if start.IsZero() || target.IsZero() || target.Before(start) {
		return 0
	}
	return target.Sub(start).Milliseconds()
}

//nolint:unused // staged for the run-metadata timing persistence rollout.
func workflowRunMetadataPath(runFolder string) string {
	cleaned := strings.TrimSpace(filepath.ToSlash(runFolder))
	cleaned = strings.TrimPrefix(cleaned, "../")
	cleaned = strings.TrimPrefix(cleaned, "/")
	switch {
	case cleaned == "":
		return ""
	default:
		return filepath.Join("runs", cleaned, "run_metadata.json")
	}
}

//nolint:unused // staged for the run-metadata timing persistence rollout.
func (hcpo *StepBasedWorkflowOrchestrator) upsertRunMetadata(ctx context.Context, runFolder string, mutate func(map[string]interface{})) error {
	unlock := lockRunMetadata(hcpo.GetWorkspacePath(), runFolder)
	defer unlock()
	metadataPath := workflowRunMetadataPath(runFolder)
	if metadataPath == "" {
		return fmt.Errorf("run folder is required")
	}

	writeCtx := ctx
	if writeCtx == nil || writeCtx.Err() != nil {
		writeCtx = context.Background()
	}

	meta := make(map[string]interface{})
	if existing, err := hcpo.ReadWorkspaceFile(writeCtx, metadataPath); err == nil && strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &meta); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to parse existing run metadata %s: %v", metadataPath, err))
			meta = make(map[string]interface{})
		}
	}

	mutate(meta)

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run metadata %s: %w", metadataPath, err)
	}
	if err := hcpo.WriteWorkspaceFile(writeCtx, metadataPath, string(data)); err != nil {
		return fmt.Errorf("write run metadata %s: %w", metadataPath, err)
	}
	return nil
}

//nolint:unused // staged for the run-metadata timing persistence rollout.
func (hcpo *StepBasedWorkflowOrchestrator) markRunMetadataStarted(ctx context.Context, runFolder string) error {
	return hcpo.markRunMetadataStartedForExecution(ctx, runFolder, "")
}

func (hcpo *StepBasedWorkflowOrchestrator) markRunMetadataStartedForExecution(ctx context.Context, runFolder, executionID string) error {
	planRevision, err := hcpo.ensureExecutablePlanRevision(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if strings.TrimSpace(executionID) == "" {
		executionID = fmt.Sprintf("run-%d", now.UnixNano())
		if opts := hcpo.GetExecutionOptions(); opts != nil && opts.ExecutionID != "" {
			executionID = opts.ExecutionID
		}
	}
	if err := hcpo.upsertRunMetadata(ctx, runFolder, func(meta map[string]interface{}) {
		if _, ok := meta["created_at"]; !ok {
			meta["created_at"] = formatRFC3339UTC(now)
		}
		meta["started_at"] = formatRFC3339UTC(now)
		delete(meta, "completed_at")
		delete(meta, "duration_ms")
		meta["status"] = "running"
		delete(meta, "recovery") // A new execution must not inherit another run's recovery verdict.
		delete(meta, "required_step_ids")
		delete(meta, "completion_receipts")
		// This identity belongs to this producing group execution, not to the
		// long-lived orchestrator instance or mutable iteration-0 slot.
		meta["execution_id"] = executionID
		if hcpo.isRelayExecution() {
			delete(meta, "active_slot_at_start")
		} else {
			meta["active_slot_at_start"] = currentWorkflowRunFolder
		}
		meta["plan_revision"] = planRevision
		meta["run_folder"] = runFolder
		if opts := hcpo.GetExecutionOptions(); opts != nil && (opts.RunKind == "schedule" || opts.RunKind == "slack") {
			meta["run_kind"] = opts.RunKind
			meta["schedule_run_id"] = opts.ScheduleRunID
			meta["schedule_id"] = opts.ScheduleID
			meta["trigger_source"] = opts.TriggerSource
			if !opts.ScheduledFor.IsZero() {
				meta["scheduled_for"] = formatRFC3339UTC(opts.ScheduledFor)
			}
		}
	}); err != nil {
		return fmt.Errorf("persist run identity: %w", err)
	}
	if err := hcpo.writeRunIndex(ctx, "run_started"); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("Could not refresh run provenance index for %s: %v", runFolder, err))
	}
	return nil
}

//nolint:unused // staged for the run-metadata timing persistence rollout.
func (hcpo *StepBasedWorkflowOrchestrator) finalizeRunMetadata(ctx context.Context, runFolder string, status string, startedAt time.Time, completedAt time.Time) {
	if err := hcpo.upsertRunMetadata(ctx, runFolder, func(meta map[string]interface{}) {
		applyRunFinalization(meta, status, startedAt, completedAt)
	}); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to finalize run metadata %s: %v", runFolder, err))
	}
	// Pulse after-run facts (PLAT-697): code only, never fails the run.
	if !hcpo.isRelayExecution() {
		factsCtx := ctx
		if factsCtx == nil || factsCtx.Err() != nil {
			factsCtx = context.Background()
		}
		if canonical := workflowrun.CanonicalStatus(status); canonical != "" {
			status = string(canonical)
		}
		if err := RecordGoalRunFacts(factsCtx, hcpo.GetWorkspacePath(), runFolder, status, startedAt, completedAt); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Could not record goal facts for run %s: %v", runFolder, err))
		}
		// After-run options for a run started from a chat (PLAT-697 phase 0).
		// The server decides; scheduled runs are handled by the scheduler.
		if hook := AfterWorkflowRunHook; hook != nil {
			sessionID := hcpo.httpSessionID
			if sessionID == "" {
				sessionID = hcpo.sessionID
			}
			go hook(hcpo.GetWorkspacePath(), runFolder, status, sessionID)
		}
	}
}

// AfterWorkflowRunHook, when set (by the server at start), runs after a full
// workflow run finished: the workflow's after-run options for manual runs.
// It runs in its own goroutine and never affects the run.
var AfterWorkflowRunHook func(workspacePath, runFolder, status, sessionID string)

func applyRunFinalization(meta map[string]interface{}, status string, startedAt, completedAt time.Time) {
	if canonical := workflowrun.CanonicalStatus(status); canonical != "" {
		status = string(canonical)
	}
	if _, ok := meta["created_at"]; !ok {
		meta["created_at"] = formatRFC3339UTC(startedAt)
	}
	if _, ok := meta["started_at"]; !ok {
		meta["started_at"] = formatRFC3339UTC(startedAt)
	}
	meta["completed_at"] = formatRFC3339UTC(completedAt)
	meta["duration_ms"] = completedAt.Sub(startedAt).Milliseconds()
	if status == "completed" && metadataListHasValues(meta["persistence_errors"]) {
		meta["warnings"] = appendUniqueMetadataString(meta["warnings"], "persistence_error")
	}
	meta["status"] = status
}

func metadataListHasValues(value interface{}) bool {
	switch values := value.(type) {
	case []interface{}:
		return len(values) > 0
	case []map[string]interface{}:
		return len(values) > 0
	default:
		return false
	}
}

func appendUniqueMetadataString(value interface{}, addition string) []interface{} {
	values, _ := value.([]interface{})
	for _, existing := range values {
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(existing)), addition) {
			return values
		}
	}
	return append(values, addition)
}

func (hcpo *StepBasedWorkflowOrchestrator) recordRunPersistenceError(ctx context.Context, stepID string, persistenceErr error) {
	if persistenceErr == nil || strings.TrimSpace(hcpo.selectedRunFolder) == "" {
		return
	}
	if err := hcpo.upsertRunMetadata(ctx, hcpo.selectedRunFolder, func(meta map[string]interface{}) {
		entry := map[string]interface{}{
			"step_id":    stepID,
			"error":      persistenceErr.Error(),
			"created_at": formatRFC3339UTC(time.Now().UTC()),
		}
		existing, _ := meta["persistence_errors"].([]interface{})
		meta["persistence_errors"] = append(existing, entry)
	}); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to record run persistence error metadata: %v", err))
	}
}
