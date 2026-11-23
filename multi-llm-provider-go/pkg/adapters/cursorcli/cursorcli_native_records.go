package cursorcli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func NativeRecordPath(workingDir, sessionID, home string) string {
	if workingDir == "" || sessionID == "" || strings.ContainsAny(sessionID, `/\`) {
		return ""
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return cursorStoreDBForNativeSession(home, workingDir, sessionID)
}

// ReadNativeRecordRows walks only the selected database's current root. Cursor
// supplies stable blob identities, but no trustworthy turn times or usage.
func ReadNativeRecordRows(ctx context.Context, path, sessionID, after string) ([]llmtypes.NativeRecord, string, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, after, err
	}
	defer db.Close()
	refs, err := cursorStoreLatestRootRefs(ctx, db)
	if err != nil {
		return nil, after, err
	}
	start := 0
	if after != "" {
		found := false
		for i, ref := range refs {
			if ref == after {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, after, fmt.Errorf("native root changed without the saved blob boundary")
		}
	}
	var records []llmtypes.NativeRecord
	last := after
	for _, ref := range refs[start:] {
		if err := ctx.Err(); err != nil {
			return nil, after, err
		}
		var msg cursorMessage
		if err := json.Unmarshal(readCursorBlob(ctx, db, ref), &msg); err != nil {
			return nil, after, fmt.Errorf("native message blob could not be decoded; boundary not advanced")
		}
		r := llmtypes.NativeRecord{ID: "blob:" + ref, Provider: "cursor-cli", SessionID: sessionID}
		switch strings.ToLower(msg.Role) {
		case "user":
			if cursorUserQueryFromContent(msg.Content) != "" {
				r.Kind, r.State = "turn", "observed_input"
				records = append(records, r)
			}
		case "assistant":
			if value := cursorAssistantTextFromContent(msg.Content); value != "" {
				r.Kind, r.State, r.Text = "reply", "committed", value
				records = append(records, r)
			}
			var blocks []struct {
				Type       string `json:"type"`
				ToolCallID string `json:"toolCallId"`
				ToolName   string `json:"toolName"`
			}
			if json.Unmarshal(msg.Content, &blocks) == nil {
				for _, block := range blocks {
					if block.Type == "tool-call" && block.ToolCallID != "" {
						out := r
						out.ID += ":" + block.ToolCallID
						out.Text = ""
						out.Kind, out.State, out.ToolID, out.Name = "tool", "started", block.ToolCallID, block.ToolName
						records = append(records, out)
						if cursorSubagentTool(block.ToolName) {
							records = append(records, cursorSubagentRecord(out, "started"))
						}
					}
				}
			}
		case "tool":
			// A finished task call closes its subagent row (the same row Claude's helpers use).
			var blocks []struct {
				Type       string          `json:"type"`
				ToolCallID string          `json:"toolCallId"`
				ToolName   string          `json:"toolName"`
				Result     json.RawMessage `json:"result"`
			}
			if json.Unmarshal(msg.Content, &blocks) == nil {
				for _, block := range blocks {
					if block.Type == "tool-result" && block.ToolCallID != "" && cursorSubagentTool(block.ToolName) {
						out := r
						out.ID += ":" + block.ToolCallID
						out.ToolID, out.Name = block.ToolCallID, block.ToolName
						if state := cursorTaskResultState(block.Result); state != "" {
							records = append(records, cursorSubagentRecord(out, state))
						}
					}
				}
			}
		}
		last = ref
	}
	return records, last, nil
}

// NativeRecordPathFromConfigDir resolves only this selected account and exact ID.
func NativeRecordPathFromConfigDir(configDir, workingDir, sessionID string) string {
	if !filepath.IsAbs(configDir) || sessionID == "" || strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return ""
	}
	if workingDir == "" {
		return ""
	}
	return cursorStoreDBForNativeSession("", workingDir, sessionID, configDir)
}

// cursorSubagentTool is Cursor's native subagent tool; its tool-call id is the subagent's identity.
func cursorSubagentTool(name string) bool { return strings.EqualFold(strings.TrimSpace(name), "task") }

func cursorSubagentRecord(tool llmtypes.NativeRecord, state string) llmtypes.NativeRecord {
	tool.ID += ":helper"
	tool.Kind, tool.State, tool.TaskID, tool.Text = "helper", state, tool.ToolID, ""
	tool.Name, tool.HelperType = "Subagent", "task"
	return tool
}

// A successful task tool may acknowledge only a background launch. Only the
// explicit native success/isBackground=false shape is foreground completion.
func cursorTaskResultState(raw json.RawMessage) string {
	var result struct {
		Status string `json:"status"`
		Value  struct {
			IsBackground *bool `json:"isBackground"`
		} `json:"value"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return ""
	}
	if result.Status == "error" {
		return "failed"
	}
	if result.Status == "success" && result.Value.IsBackground != nil && !*result.Value.IsBackground {
		return "finished"
	}
	return ""
}
