package codexcli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Codex reports two things the chat should show and the turn's items don't
// carry (#4602, checked against codex-cli 0.172.0's app-server schema):
//
//	hook/completed                  {threadId, turnId|null, run:{id, eventName, status, statusMessage, entries:[{kind, text}]}}
//	mcpServer/startupStatus/updated {threadId|null, name, status:"starting"|"ready"|"failed"|"cancelled", error, failureReason}
//
// A hook that blocked or stopped the turn, or failed, and an MCP server that
// failed to start, each become one finished row in the chat saying why.
// Startup statuses arrive while the thread starts, before any turn reads
// events, so the reader keeps failed ones for the next turn.

const codexMaxKeptStartupNotices = 20

type codexHookRun struct {
	ID            string `json:"id"`
	EventName     string `json:"eventName"`
	Status        string `json:"status"`
	StatusMessage string `json:"statusMessage"`
	Entries       []struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	} `json:"entries"`
}

type codexMcpStartup struct {
	ThreadID      string `json:"threadId"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Error         string `json:"error"`
	FailureReason string `json:"failureReason"`
}

// codexMcpStartupFailed reads a startup status that is a failure of this thread
// (or of the process, when it names no thread).
func codexMcpStartupFailed(params json.RawMessage, thread string) (codexMcpStartup, bool) {
	var status codexMcpStartup
	if json.Unmarshal(params, &status) != nil || status.Status != "failed" || strings.TrimSpace(status.Name) == "" {
		return status, false
	}
	return status, status.ThreadID == "" || thread == "" || status.ThreadID == thread
}

// keepStartupNotice keeps a failed MCP startup that arrived between turns.
func (r *codexRPC) keepStartupNotice(msg codexRPCMessage) bool {
	if msg.Method != "mcpServer/startupStatus/updated" {
		return false
	}
	if _, failed := codexMcpStartupFailed(msg.Params, ""); failed {
		r.mu.Lock()
		if len(r.startupNotices) < codexMaxKeptStartupNotices {
			r.startupNotices = append(r.startupNotices, msg)
		}
		r.mu.Unlock()
	}
	return true
}

func (r *codexRPC) takeStartupNotices() []codexRPCMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.startupNotices
	r.startupNotices = nil
	return kept
}

// codexMcpStartupRow is the finished row for a failed MCP server.
func codexMcpStartupRow(status codexMcpStartup, metadata map[string]any) []llmtypes.StreamChunk {
	why := strings.TrimSpace(status.Error)
	if status.FailureReason == "reauthenticationRequired" {
		why = "It needs you to sign in to it again."
	}
	if why == "" {
		why = "Codex gave no reason."
	}
	label := fmt.Sprintf("MCP server %s didn't start", status.Name)
	return codexNoticeRow("mcp-start-"+status.Name, "codex_mcp_server_start", label, why, metadata)
}

// codexHookRow is the finished row for a hook run that blocked, stopped or
// failed; nil for one that just ran.
func codexHookRow(params json.RawMessage, metadata map[string]any) []llmtypes.StreamChunk {
	var event struct {
		Run codexHookRun `json:"run"`
	}
	if json.Unmarshal(params, &event) != nil {
		return nil
	}
	run := event.Run
	var label string
	switch run.Status {
	case "blocked":
		label = "A Codex hook blocked this step"
	case "stopped":
		label = "A Codex hook stopped the turn"
	case "failed":
		label = "A Codex hook failed"
	default:
		return nil
	}
	if run.EventName != "" {
		label += " (" + run.EventName + ")"
	}
	var lines []string
	if message := strings.TrimSpace(run.StatusMessage); message != "" {
		lines = append(lines, message)
	}
	for _, entry := range run.Entries {
		if text := strings.TrimSpace(entry.Text); text != "" && entry.Kind != "context" {
			lines = append(lines, text)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "The hook gave no reason.")
	}
	return codexNoticeRow("hook-"+run.ID, "codex_hook", label, strings.Join(lines, "\n"), metadata)
}

func codexNoticeRow(id, name, label, result string, metadata map[string]any) []llmtypes.StreamChunk {
	return []llmtypes.StreamChunk{
		{Type: llmtypes.StreamChunkTypeToolCallStart, Content: label, ToolName: name, ToolCallID: id, Metadata: metadata},
		{Type: llmtypes.StreamChunkTypeToolCallEnd, Content: label, ToolName: name, ToolCallID: id, ToolResult: result, Metadata: metadata},
	}
}
