package terminals

import (
	"fmt"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"

	agentevents "github.com/manishiitg/mcpagent/events"
)

func TestStructuredStreamingMaterializesAndAvoidsQuadraticAllocs(t *testing.T) {
	store := NewStore()
	sessionID := "session-stream-alloc"
	now := time.Now()
	meta := map[string]interface{}{
		"provider":            "claude-code",
		"execution_owner_id":  "workflow-step:step-1",
		"workflow_step_id":    "step-1",
		"step_execution_mode": "structured",
	}

	// First streaming chunk creates the structured snapshot, then a tool call, then 399 more chunks.
	chunkPayload := strings.Repeat("streaming output line for throughput check\n", 2)
	store.HandleEvent(sessionID, storeevents.Event{
		Type:      "streaming_chunk",
		SessionID: sessionID,
		Timestamp: now,
		Data: &agentevents.AgentEvent{
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: meta},
				Content:       chunkPayload,
				ChunkIndex:    1,
				IsDelta:       true,
			},
		},
	})
	store.HandleEvent(sessionID, storeevents.Event{
		Type:      string(agentevents.ToolCallStart),
		SessionID: sessionID,
		Timestamp: now.Add(time.Millisecond),
		Data: &agentevents.AgentEvent{
			Data: &agentevents.ToolCallStartEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: meta},
				ToolCallID:    "tc-1",
				ToolName:      "read_file",
				ToolParams:    agentevents.ToolParams{Arguments: `{"path":"README.md"}`},
			},
		},
	})
	for i := 1; i < 400; i++ {
		store.HandleEvent(sessionID, storeevents.Event{
			Type:      "streaming_chunk",
			SessionID: sessionID,
			Timestamp: now.Add(time.Duration(i+1) * time.Millisecond),
			Data: &agentevents.AgentEvent{
				Data: &agentevents.StreamingChunkEvent{
					BaseEventData: agentevents.BaseEventData{Metadata: meta},
					Content:       chunkPayload,
					ChunkIndex:    i + 1,
					IsDelta:       true,
				},
			},
		})
	}

	// Reading via List materializes the full base content + tool line + status.
	snaps := store.List(sessionID)
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	snap := snaps[0]
	if snap.Status.ProviderLabel != "Claude Code" {
		t.Fatalf("expected ProviderLabel Claude Code, got %q", snap.Status.ProviderLabel)
	}
	if !strings.Contains(snap.Content, "→ tool: read_file(") {
		t.Fatalf("expected tool line in materialized content, got tail: %q", snap.Content[max(0, len(snap.Content)-200):])
	}
	if strings.Count(snap.Content, "streaming output line for throughput check") != 800 {
		t.Fatalf("expected 800 streamed lines, got %d", strings.Count(snap.Content, "streaming output line for throughput check"))
	}

	// Completing the structured execution materializes and releases the builder buffer.
	store.HandleEvent(sessionID, storeevents.Event{
		Type:      "streaming_chunk",
		SessionID: sessionID,
		Timestamp: now.Add(time.Second),
		Data: &agentevents.AgentEvent{
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: meta},
				Content:       "final tail line\n",
				ChunkIndex:    401,
				IsDelta:       true,
			},
		},
	})
	store.completeStructuredExecution(sessionID, storeevents.Event{
		SessionID: sessionID,
		Timestamp: now.Add(2 * time.Second),
	}, meta, false)

	done, ok := store.Get(snap.TerminalID)
	if !ok {
		t.Fatalf("expected completed snapshot to exist")
	}
	if !strings.Contains(done.Content, "final tail line") {
		t.Fatalf("expected final tail line after completion")
	}
	if len(store.structuredBase) != 0 || len(store.structuredDirty) != 0 {
		t.Fatalf("expected structured buffers released on completion, got base=%d dirty=%d", len(store.structuredBase), len(store.structuredDirty))
	}

	// Verify linear (non-quadratic) allocation scaling between 200 and 400 chunks.
	runStream := func(n int) float64 {
		return testing.AllocsPerRun(1, func() {
			s := NewStore()
			for i := 0; i < n; i++ {
				s.HandleEvent("bench-sess", storeevents.Event{
					Type:      "streaming_chunk",
					SessionID: "bench-sess",
					Timestamp: now,
					Data: &agentevents.AgentEvent{
						Data: &agentevents.StreamingChunkEvent{
							BaseEventData: agentevents.BaseEventData{Metadata: meta},
							Content:       fmt.Sprintf("chunk %d payload text line\n", i),
							ChunkIndex:    i + 1,
							IsDelta:       true,
						},
					},
				})
			}
			_ = s.List("bench-sess")
		})
	}
	allocs200 := runStream(200)
	allocs400 := runStream(400)
	if allocs400 > allocs200*2.5 {
		t.Fatalf("expected linear allocation scaling, got 200 chunks=%.0f vs 400 chunks=%.0f (ratio %.2fx)", allocs200, allocs400, allocs400/allocs200)
	}
}

func TestDeriveStatusFastPathAndToolParsing(t *testing.T) {
	// Plain text without ANSI escapes still extracts Claude preview and tool summary.
	content := strings.Join([]string{
		"• Called read_file",
		"Calling search_code 3 times",
		"⏺ Here is the final answer.",
	}, "\n")
	status := DeriveStatus(content, map[string]interface{}{"provider": "claude-code"})
	if status.ProviderLabel != "Claude Code" {
		t.Fatalf("ProviderLabel = %q, want Claude Code", status.ProviderLabel)
	}
	if status.AssistantPreview != "Here is the final answer." {
		t.Fatalf("AssistantPreview = %q, want %q", status.AssistantPreview, "Here is the final answer.")
	}
	if status.ToolName != "search_code" || status.ToolCount != 3 {
		t.Fatalf("Tool = (%q, %d), want (search_code, 3)", status.ToolName, status.ToolCount)
	}

	// ANSI-escaped content is still stripped accurately.
	ansiContent := "✓ grep_search(foo)\n\x1b[32m⏺ Colored reply\x1b[0m\n"
	ansiStatus := DeriveStatus(ansiContent, map[string]interface{}{"provider": "claude-code"})
	if ansiStatus.AssistantPreview != "Colored reply" {
		t.Fatalf("ANSI AssistantPreview = %q, want Colored reply", ansiStatus.AssistantPreview)
	}
	if ansiStatus.ToolName != "grep_search" || ansiStatus.ToolCount != 1 {
		t.Fatalf("ANSI Tool = (%q, %d), want (grep_search, 1)", ansiStatus.ToolName, ansiStatus.ToolCount)
	}
}
