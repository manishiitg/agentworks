package claudecode

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// claudeSubagentCall is the usage of one model call a native subagent made.
type claudeSubagentCall struct {
	AgentID, ID, Model                    string
	Input, Output, CacheCreate, CacheRead int
}

// claudeSubagentCalls returns the model calls the session's native subagents made at or after turnStart that nobody has
// reported yet. A subagent writes its own transcript (<session>/subagents/agent-*.jsonl); the parent transcript has
// none of those calls, so neither the Run's read nor the retained-turn read saw that spend. Each call (message id) is
// claimed once through the same first-reporter-wins registry as the parent's, keyed by the subagent file.
func claudeSubagentCalls(parentPath string, turnStart time.Time) []claudeSubagentCall {
	if parentPath == "" {
		return nil
	}
	paths, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(parentPath, ".jsonl"), "subagents", "agent-*.jsonl"))
	var out []claudeSubagentCall
	for _, path := range paths {
		f, err := os.Open(path) //nolint:gosec // adapter-owned transcript path
		if err != nil {
			continue
		}
		calls := map[string]claudeSubagentCall{}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			var ev struct {
				Type      string `json:"type"`
				Timestamp string `json:"timestamp"`
				RequestID string `json:"requestId"`
				Message   struct {
					ID    string `json:"id"`
					Model string `json:"model"`
					Usage struct {
						InputTokens              int `json:"input_tokens"`
						OutputTokens             int `json:"output_tokens"`
						CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
						CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(scanner.Bytes(), &ev) != nil || ev.Type != "assistant" {
				continue
			}
			if ts, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil && !turnStart.IsZero() && ts.Before(turnStart) {
				continue
			}
			id := ev.Message.ID
			if id == "" {
				id = ev.RequestID
			}
			u := ev.Message.Usage
			if id == "" || u.InputTokens+u.OutputTokens+u.CacheCreationInputTokens+u.CacheReadInputTokens == 0 {
				continue
			}
			old := calls[id]
			calls[id] = claudeSubagentCall{
				AgentID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), ID: id, Model: ev.Message.Model,
				Input: max(old.Input, u.InputTokens), Output: max(old.Output, u.OutputTokens), CacheCreate: max(old.CacheCreate, u.CacheCreationInputTokens), CacheRead: max(old.CacheRead, u.CacheReadInputTokens),
			}
		}
		for id, call := range calls {
			if claudeMarkUsageReported(path, id) {
				out = append(out, call)
			}
		}
		_ = f.Close()
	}
	if len(out) > 0 {
		log.Printf("[RETAINED_USAGE] claude-code subagents: %d model calls reported from %d transcript(s)", len(out), len(paths))
	}
	return out
}
