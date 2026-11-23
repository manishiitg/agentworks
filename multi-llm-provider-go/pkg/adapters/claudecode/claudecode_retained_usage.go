package claudecode

import (
	"bufio"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Claude's bounded Run sums the usage of the model calls it ran (claudeMarkUsageReported records each message id it
// counted). A follow-up typed into the running terminal, or any later turn the Run never saw, has the same per-call
// usage in the transcript but nobody read it. This registry lets the retained-turn reader report each message id once.
var claudeReportedUsage = struct {
	sync.Mutex
	ids map[string]map[string]bool
}{ids: map[string]map[string]bool{}}

func claudeMarkUsageReported(path, messageID string) bool {
	if path == "" || messageID == "" {
		return false
	}
	claudeReportedUsage.Lock()
	defer claudeReportedUsage.Unlock()
	set := claudeReportedUsage.ids[path]
	if set == nil {
		set = map[string]bool{}
		claudeReportedUsage.ids[path] = set
	}
	if set[messageID] {
		return false
	}
	set[messageID] = true
	return true
}

// ReadRetainedTurnUsage returns the usage of each model call (one entry per message id, oldest first) in this owner's
// transcript at or after turnStart that has not been reported yet. Read-only; it marks what it returns.
func ReadRetainedTurnUsage(ownerSessionID string, turnStart time.Time) []llmtypes.RetainedTurnUsage {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	session, ok := claudeInteractivePersistentRegistry.Get(ownerSessionID)
	if !ok || session == nil {
		log.Printf("[RETAINED_USAGE] claude-code owner=%s skipped: no terminal registered for this owner", ownerSessionID)
		return nil
	}
	session.mu.Lock()
	nativeSessionID, workingDir, accountHome := session.nativeSessionID, session.workingDir, session.accountHome
	session.mu.Unlock()
	f, path, err := openClaudeTranscript(nativeSessionID, workingDir, accountHome)
	if err != nil || f == nil {
		log.Printf("[RETAINED_USAGE] claude-code owner=%s skipped: transcript unreadable", ownerSessionID)
		return nil
	}
	defer f.Close()
	type call struct {
		id                                    string
		input, output, cacheCreate, cacheRead int
		model                                 string
	}
	var order []string
	calls := map[string]*call{}
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
		if ts, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil && ts.Before(turnStart) {
			continue
		}
		id := ev.Message.ID
		if id == "" {
			id = ev.RequestID
		}
		if id == "" {
			continue
		}
		if old := calls[id]; old != nil {
			old.input = max(old.input, ev.Message.Usage.InputTokens)
			old.output = max(old.output, ev.Message.Usage.OutputTokens)
			old.cacheCreate = max(old.cacheCreate, ev.Message.Usage.CacheCreationInputTokens)
			old.cacheRead = max(old.cacheRead, ev.Message.Usage.CacheReadInputTokens)
			continue
		}
		calls[id] = &call{id: id, input: ev.Message.Usage.InputTokens, output: ev.Message.Usage.OutputTokens,
			cacheCreate: ev.Message.Usage.CacheCreationInputTokens, cacheRead: ev.Message.Usage.CacheReadInputTokens, model: ev.Message.Model}
		order = append(order, id)
	}
	var out []llmtypes.RetainedTurnUsage
	for _, id := range order {
		c := calls[id]
		if c.input+c.output+c.cacheCreate+c.cacheRead == 0 || !claudeMarkUsageReported(path, id) {
			continue
		}
		info := claudeGenerationInfo(c.input, c.output, c.cacheCreate, c.cacheRead)
		usage := llmtypes.Usage{InputTokens: c.input + c.cacheCreate + c.cacheRead, OutputTokens: c.output}
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		if c.cacheRead > 0 {
			usage.CacheTokens = intRef(c.cacheRead)
		}
		item := llmtypes.RetainedTurnUsage{ID: id, Usage: usage, Info: *info}
		if c.model != "" && c.model != "<synthetic>" {
			if item.Info.Additional == nil {
				item.Info.Additional = map[string]interface{}{}
			}
			item.Info.Additional["claude_code_model"] = c.model
		}
		log.Printf("[RETAINED_USAGE] claude-code owner=%s message=%s input=%d output=%d cache_read=%d reported", ownerSessionID, id, c.input+c.cacheCreate+c.cacheRead, c.output, c.cacheRead)
		out = append(out, item)
	}
	for _, sub := range claudeSubagentCalls(path, turnStart) {
		info := claudeGenerationInfo(sub.Input, sub.Output, sub.CacheCreate, sub.CacheRead)
		usage := llmtypes.Usage{InputTokens: sub.Input + sub.CacheCreate + sub.CacheRead, OutputTokens: sub.Output}
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		if sub.CacheRead > 0 {
			usage.CacheTokens = intRef(sub.CacheRead)
		}
		item := llmtypes.RetainedTurnUsage{ID: "subagent-" + sub.AgentID + "-" + sub.ID, Usage: usage, Info: *info}
		item.Info.Additional["subagent_id"] = sub.AgentID
		if sub.Model != "" && sub.Model != "<synthetic>" {
			item.Info.Additional["claude_code_model"] = sub.Model
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		log.Printf("[RETAINED_USAGE] claude-code owner=%s no new model-call usage since %s (calls_seen=%d)", ownerSessionID, turnStart.UTC().Format(time.RFC3339), len(calls))
	}
	return out
}
