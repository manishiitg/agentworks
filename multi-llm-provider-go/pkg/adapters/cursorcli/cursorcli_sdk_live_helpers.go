package cursorcli

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A Cursor helper's conversation, live (#3132). While a "task" call runs, the
// SDK streams the child's steps on the parent's call ("tool-call-delta" with a
// task update); the bridge forwards them as "helper_progress" lines and Go
// builds the child's conversation as it goes. When the call ends, the child the
// SDK returned replaces it. The host reads it by the parent's task call.

const (
	cursorLiveHelpersPerChat = 32
	cursorLiveHelperMessages = 400
	cursorLiveHelperText     = 64 * 1024
	cursorLiveToolResult     = 4 * 1024
)

type cursorLiveHelper struct {
	child llmtypes.NativeSubagentTranscript
	seen  time.Time
}

var cursorLiveHelpers = struct {
	sync.Mutex
	byOwner map[string]map[string]*cursorLiveHelper
}{byOwner: map[string]map[string]*cursorLiveHelper{}}

// cursorLiveHelperFor returns the chat's helper for the call, creating it
// when create is set (and forgetting the chat's oldest beyond the bound).
func cursorLiveHelperFor(owner, callID string, create bool) *cursorLiveHelper {
	helpers := cursorLiveHelpers.byOwner[owner]
	if helpers == nil {
		if !create {
			return nil
		}
		helpers = map[string]*cursorLiveHelper{}
		cursorLiveHelpers.byOwner[owner] = helpers
	}
	h := helpers[callID]
	if h == nil && create && callID != "" {
		if len(helpers) >= cursorLiveHelpersPerChat {
			oldest := ""
			for id, other := range helpers {
				if oldest == "" || other.seen.Before(helpers[oldest].seen) {
					oldest = id
				}
			}
			delete(helpers, oldest)
		}
		now := time.Now()
		h = &cursorLiveHelper{child: llmtypes.NativeSubagentTranscript{ID: callID, ToolUseID: callID, Name: "Subagent", Provider: "cursor-cli", Status: "running", StartedAt: now, UpdatedAt: now}}
		helpers[callID] = h
	}
	return h
}

// recordCursorHelperProgress adds one streamed step to the child's
// conversation.
func recordCursorHelperProgress(owner, parentID string, event cursorSDKEvent) {
	owner = strings.TrimSpace(owner)
	if owner == "" || event.CallID == "" {
		return
	}
	cursorLiveHelpers.Lock()
	defer cursorLiveHelpers.Unlock()
	h := cursorLiveHelperFor(owner, event.CallID, true)
	if h.child.Status != "running" {
		return // The SDK's own finished child stays.
	}
	now := time.Now()
	h.seen, h.child.UpdatedAt = now, now
	if parentID != "" {
		h.child.ParentID = parentID
	}
	msgs := &h.child.Messages
	if len(*msgs) >= cursorLiveHelperMessages && event.Kind != "started" {
		return
	}
	switch event.Kind {
	case "started":
		var call struct {
			Description string `json:"description"`
			Prompt      string `json:"prompt"`
		}
		_ = json.Unmarshal(event.Args, &call)
		if name := strings.TrimSpace(call.Description); name != "" {
			h.child.Name = name
		}
		if len(*msgs) == 0 && strings.TrimSpace(call.Prompt) != "" {
			*msgs = append(*msgs, llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, call.Prompt))
		}
	case "text":
		if event.Text == "" {
			return
		}
		if n := len(*msgs); n > 0 && (*msgs)[n-1].Role == llmtypes.ChatMessageTypeAI && len((*msgs)[n-1].Parts) == 1 {
			if part, ok := (*msgs)[n-1].Parts[0].(llmtypes.TextContent); ok {
				if len(part.Text) < cursorLiveHelperText {
					(*msgs)[n-1].Parts[0] = llmtypes.TextContent{Text: part.Text + event.Text}
				}
				return
			}
		}
		*msgs = append(*msgs, llmtypes.TextPart(llmtypes.ChatMessageTypeAI, event.Text))
	case "tool_started", "tool_completed":
		if event.ID == "" {
			return
		}
		name, args := cursorSDKMCPToolCall(event.Name, string(event.Args))
		toolID := event.CallID + ":" + event.ID
		if event.Kind == "tool_started" {
			*msgs = append(*msgs, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: toolID, Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: name, Arguments: args}}}})
			return
		}
		out, ok := cursorSDKMCPToolResult(event.Result)
		if !ok {
			out = string(event.Result)
		}
		if len(out) > cursorLiveToolResult {
			out = llmtypes.BackgroundOutputTail(out, cursorLiveToolResult)
		}
		*msgs = append(*msgs, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: toolID, Name: name, Content: out}}})
	}
}

// finishCursorHelper keeps the child the SDK returned when its call ended.
func finishCursorHelper(owner, callID string, child llmtypes.NativeSubagentTranscript) {
	owner = strings.TrimSpace(owner)
	if owner == "" || callID == "" {
		return
	}
	cursorLiveHelpers.Lock()
	defer cursorLiveHelpers.Unlock()
	h := cursorLiveHelperFor(owner, callID, true)
	started := h.child.StartedAt
	child.ToolUseID = callID
	if len(child.Messages) == 0 {
		child.Messages = h.child.Messages
	}
	if !started.IsZero() && started.Before(child.StartedAt) {
		child.StartedAt = started
	}
	h.child, h.seen = child, time.Now()
}

// endCursorHelpers marks the helpers still running in a closed bridge as
// stopped: nothing reports on them any more.
func endCursorHelpers(owner string) {
	cursorLiveHelpers.Lock()
	defer cursorLiveHelpers.Unlock()
	for _, h := range cursorLiveHelpers.byOwner[strings.TrimSpace(owner)] {
		if h.child.Status == "running" {
			h.child.Status, h.child.UpdatedAt = "interrupted", time.Now()
		}
	}
}

// CursorSubagentConversation is, as it is now, the conversation of the helper
// this chat's Cursor started with the task call callID (#3132). Only helpers
// of this chat, seen by this process, are known.
func CursorSubagentConversation(owner, callID string) (llmtypes.NativeSubagentTranscript, bool) {
	cursorLiveHelpers.Lock()
	defer cursorLiveHelpers.Unlock()
	h := cursorLiveHelperFor(strings.TrimSpace(owner), strings.TrimSpace(callID), false)
	if h == nil {
		return llmtypes.NativeSubagentTranscript{}, false
	}
	child := h.child
	child.Messages = append([]llmtypes.MessageContent(nil), h.child.Messages...)
	return child, true
}
