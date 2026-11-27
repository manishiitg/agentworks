package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/mcpagent/toolcalllog"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxcapture"
)

// truncateString truncates s to maxLen and appends "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

type queryToolCallSummary struct {
	ToolCallID string
	ToolName   string
	Status     string
	Duration   time.Duration
	StartedAt  time.Time
	Args       string
	Result     string
	SessionID  string
}

// makeStepTmuxLookup returns a TmuxLookupFunc that resolves the live tmux session
// for a workshop step running a coding-CLI provider (tmux transport) and captures a
// fresh tail of its pane. It scans the terminal store for a tmux-backed snapshot
// matching the step (the same session shown in the UI terminal), then runs a live
// `tmux capture-pane` so query_step returns CURRENT output — the store's cached
// Content is only refreshed when the UI views the pane, so it can be stale for a
// headless builder. The capture result is also written back to keep the store warm.
func makeStepTmuxLookup(api *StreamingAPI) todo_creation_human.TmuxLookupFunc {
	capturer := tmuxcapture.Capturer{Run: func(ctx context.Context, args ...string) (string, error) {
		return runTerminalTmuxOutputCommand(ctx, args...)
	}}
	return func(ctx context.Context, mainSessionID, stepID string) (string, string, bool) {
		if api == nil || api.terminalStore == nil {
			return "", "", false
		}
		if strings.TrimSpace(mainSessionID) == "" || strings.TrimSpace(stepID) == "" {
			return "", "", false
		}
		for _, snap := range api.terminalStore.ListRaw(mainSessionID) {
			if snap.StepID != stepID {
				continue
			}
			ts := strings.TrimSpace(snap.TmuxSession)
			if ts == "" || !strings.EqualFold(snap.StepTransport, "tmux") {
				continue
			}
			var tail string
			cctx, cancel := context.WithTimeout(ctx, terminalTmuxActionTimeout)
			capture, err := capturer.CaptureAgentTail(cctx, ts, tmuxcapture.Options{})
			cancel()
			if err == nil && strings.TrimSpace(capture.PaneContent) != "" {
				api.terminalStore.RefreshContent(snap.TerminalID, capture.PaneContent)
				tail = capture.Text
			}
			return ts, tail, true
		}
		return "", "", false
	}
}

// formatToolCallSummaries returns a ToolCallQueryFunc that formats event-store
// tool calls plus live HTTP bridge snapshots from per-step MCP sessions.
// When toolCallID is empty, returns a summary with truncated args/results.
// When toolCallID is set, returns full input/output for that specific call.
func formatToolCallSummaries(api *StreamingAPI) todo_creation_human.ToolCallQueryFunc {
	return func(mainSessID, correlationID, stepID, toolCallID string) string {
		summaries := collectQueryToolCallSummaries(api, mainSessID, correlationID, stepID)
		if len(summaries) == 0 {
			return ""
		}

		// Detailed mode: find specific tool call and return full args/result
		if toolCallID != "" {
			for _, tc := range summaries {
				if tc.ToolCallID == toolCallID {
					var sb strings.Builder
					sb.WriteString(fmt.Sprintf("**%s** [%s]", tc.ToolName, strings.ToUpper(tc.Status)))
					if tc.Duration > 0 {
						sb.WriteString(fmt.Sprintf(" (%s)", tc.Duration.Round(time.Millisecond)))
					}
					sb.WriteString(fmt.Sprintf("\ntool_call_id: %s", tc.ToolCallID))
					if tc.SessionID != "" {
						sb.WriteString(fmt.Sprintf("\nsession_id: %s", tc.SessionID))
					}
					if tc.Args != "" {
						sb.WriteString(fmt.Sprintf("\n\n**Input:**\n```json\n%s\n```", tc.Args))
					}
					if tc.Result != "" {
						sb.WriteString(fmt.Sprintf("\n\n**Output:**\n```\n%s\n```", tc.Result))
					}
					return sb.String()
				}
			}
			return fmt.Sprintf("tool_call_id %q not found", toolCallID)
		}

		// Summary mode: truncated args/results
		var sb strings.Builder
		for i, tc := range summaries {
			if i > 0 {
				sb.WriteString("\n")
			}
			switch tc.Status {
			case "running":
				sb.WriteString(fmt.Sprintf("- [RUNNING] %s (id: %s)", tc.ToolName, tc.ToolCallID))
			case "done":
				if tc.Duration > 0 {
					sb.WriteString(fmt.Sprintf("- [DONE] %s (%s) (id: %s)", tc.ToolName, tc.Duration.Round(time.Millisecond), tc.ToolCallID))
				} else {
					sb.WriteString(fmt.Sprintf("- [DONE] %s (id: %s)", tc.ToolName, tc.ToolCallID))
				}
			case "error":
				if tc.Duration > 0 {
					sb.WriteString(fmt.Sprintf("- [ERROR] %s (%s) (id: %s)", tc.ToolName, tc.Duration.Round(time.Millisecond), tc.ToolCallID))
				} else {
					sb.WriteString(fmt.Sprintf("- [ERROR] %s (id: %s)", tc.ToolName, tc.ToolCallID))
				}
			default:
				sb.WriteString(fmt.Sprintf("- [%s] %s (id: %s)", strings.ToUpper(tc.Status), tc.ToolName, tc.ToolCallID))
			}
			if tc.SessionID != "" {
				sb.WriteString(fmt.Sprintf("\n  Session: %s", tc.SessionID))
			}
			if tc.Args != "" {
				sb.WriteString(fmt.Sprintf("\n  Args: %s", truncateString(tc.Args, 200)))
			}
			if tc.Result != "" {
				sb.WriteString(fmt.Sprintf("\n  Result: %s", truncateString(tc.Result, 200)))
			}
		}
		return sb.String()
	}
}

func collectQueryToolCallSummaries(api *StreamingAPI, mainSessID, correlationID, stepID string) []queryToolCallSummary {
	var summaries []queryToolCallSummary
	seen := make(map[string]struct{})

	add := func(tc queryToolCallSummary) {
		if tc.ToolCallID == "" {
			return
		}
		if _, exists := seen[tc.ToolCallID]; exists {
			return
		}
		seen[tc.ToolCallID] = struct{}{}
		summaries = append(summaries, tc)
	}

	if api != nil && api.eventStore != nil && mainSessID != "" && correlationID != "" {
		for _, tc := range api.eventStore.GetToolCallsByCorrelation(mainSessID, correlationID) {
			add(queryToolCallSummary{
				ToolCallID: tc.ToolCallID,
				ToolName:   tc.ToolName,
				Status:     tc.Status,
				Duration:   tc.Duration,
				StartedAt:  tc.StartedAt,
				Args:       tc.Args,
				Result:     tc.Result,
			})
		}

		// For workshop step executions, also include tool calls from API-based delegation
		// sub-agents. CLI sub-agents share the parent MCP session (covered by the prefix
		// scan below), but API sub-agents emit events under their own delegation correlation ID.
		if strings.HasPrefix(correlationID, "workshop-step-") {
			for _, delegID := range getStepDelegations(correlationID) {
				for _, tc := range api.eventStore.GetToolCallsByCorrelation(mainSessID, delegID) {
					add(queryToolCallSummary{
						ToolCallID: tc.ToolCallID,
						ToolName:   tc.ToolName,
						Status:     tc.Status,
						Duration:   tc.Duration,
						StartedAt:  tc.StartedAt,
						Args:       tc.Args,
						Result:     tc.Result,
					})
				}
			}
		}
	}

	addSnapshots := func(calls []toolcalllog.SnapshotCall) {
		for _, call := range calls {
			duration := time.Duration(0)
			if !call.StartedAt.IsZero() && !call.CompletedAt.IsZero() {
				duration = call.CompletedAt.Sub(call.StartedAt)
			}
			status := call.Status
			if status == "" {
				status = "done"
			}
			add(queryToolCallSummary{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Status:     status,
				Duration:   duration,
				StartedAt:  call.StartedAt,
				Args:       call.ArgsJSON,
				Result:     call.Result,
				SessionID:  call.SessionID,
			})
		}
	}

	// Direct lookup covers background task sessions or future callers that pass
	// the actual MCP session id as correlationID.
	if correlationID != "" {
		addSnapshots(toolcalllog.Snapshot(correlationID))
	}

	// Workflow step agents use dedicated MCP session ids such as
	// sub-exec-<stepID>-<timestamp>. Those HTTP bridge calls do not always land
	// in the parent chat event store before query_step runs, so query the live
	// bridge log by the deterministic session prefix too.
	if stepID != "" {
		for _, kind := range []string{"exec", "todo", "learn"} {
			addSnapshots(toolcalllog.SnapshotBySessionPrefix(fmt.Sprintf("sub-%s-%s-", kind, stepID)))
		}
	}

	sort.SliceStable(summaries, func(i, j int) bool {
		left := summaries[i].StartedAt
		right := summaries[j].StartedAt
		if left.IsZero() || right.IsZero() {
			return i < j
		}
		if left.Equal(right) {
			return summaries[i].ToolCallID < summaries[j].ToolCallID
		}
		return left.Before(right)
	})

	return summaries
}
