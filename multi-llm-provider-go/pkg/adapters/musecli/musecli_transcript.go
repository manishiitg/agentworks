package musecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// museXDGDataHome mirrors the CLI: $XDG_DATA_HOME, else ~/.local/share.
func museXDGDataHome() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".local", "share")
}

// museSessionLogPath resolves
// $XDG_DATA_HOME/muse/sessions/YYYY/MM/DD/<sessionID>/session.jsonl.
// The date segment is the run date, so it is globbed, not assumed.
func museSessionLogPath(sessionID string, accountDataHome ...string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) {
		return ""
	}
	base := museXDGDataHome()
	if len(accountDataHome) > 0 && accountDataHome[0] != "" {
		base = accountDataHome[0]
	}
	if base == "" {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(base, "muse", "sessions", "*", "*", "*", sessionID, "session.jsonl"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[0]
}

func museLogString(m map[string]any, path ...string) string {
	var cur any = m
	for _, key := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = next[key]
		if !ok {
			return ""
		}
	}
	s, _ := cur.(string)
	return s
}

func museLogInt(m map[string]any, path ...string) int {
	var cur any = m
	for _, key := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur, ok = next[key]
		if !ok {
			return 0
		}
	}
	switch n := cur.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func museLogBool(m map[string]any, path ...string) bool {
	var cur any = m
	for _, key := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = next[key]
		if !ok {
			return false
		}
	}
	b, _ := cur.(bool)
	return b
}

// readMuseTranscriptUsage sums goal_usage_attribution quantities for one run
// (all runs when runID is empty). Best-effort: unparseable rows are skipped,
// and ok=false when no reported row matched. Field paths verified against
// muse 1.1.1 session.jsonl; magnitudes on a paid model are still TBD.
func readMuseTranscriptUsage(logPath, runID string) (llmtypes.Usage, bool) {
	var usage llmtypes.Usage
	matched := false
	raw, err := os.ReadFile(logPath)
	if err != nil {
		return usage, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		payload, _ := rec["payload"].(map[string]any)
		if payload == nil || museLogString(payload, "kind") != "run" {
			continue
		}
		event, _ := payload["event"].(map[string]any)
		if event == nil || museLogString(event, "kind") != "goal_usage_attribution" {
			continue
		}
		if runID != "" {
			ownerRun := museLogString(event, "record", "owner", "run_id")
			payloadRun := museLogString(payload, "run_id")
			if ownerRun != runID && payloadRun != runID {
				continue
			}
		}
		record, _ := event["record"].(map[string]any)
		if record == nil {
			continue
		}
		quantity, _ := record["quantity"].(map[string]any)
		if quantity == nil || !museLogBool(quantity, "reported") {
			continue
		}
		if runID != "" {
			if sequence, _ := rec["sequence"].(float64); !museMarkUsageReported(logPath, int64(sequence)) {
				continue // a retained-turn read already reported this row: the first reporter wins
			}
		}
		matched = true
		usage.InputTokens += museLogInt(quantity, "input_tokens")
		usage.OutputTokens += museLogInt(quantity, "output_tokens")
		cache := museLogInt(quantity, "cached_tokens")
		if usage.CacheTokens != nil {
			cache += *usage.CacheTokens
		}
		if cache > 0 {
			usage.CacheTokens = &cache
		}
		if r := museLogInt(quantity, "reasoning_tokens"); r > 0 {
			n := r
			if usage.ReasoningTokens != nil {
				n += *usage.ReasoningTokens
			}
			usage.ReasoningTokens = &n
		}
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	return usage, matched
}

func museIntPtr(v int) *int { return &v }

// museAttachTurnCost maps sidecar usage onto GenerationInfo and attaches the
// shadow USD estimate through the model rate card. Proven 2026-09-10 against
// live goal_usage_attribution rows: input_tokens is the total prompt-side
// footprint INCLUDING cached_tokens (a warm turn's cached count ~= the prior
// turn's input), so the cache bucket is marked included to avoid
// double-charging it as fresh input. Never fails: zero rates or missing
// usage just leave the estimate off.
func museAttachTurnCost(gi *llmtypes.GenerationInfo, model string, usage *llmtypes.Usage) {
	if gi == nil || usage == nil {
		return
	}
	if gi.Additional == nil {
		gi.Additional = map[string]any{}
	}
	gi.Additional["provider"] = "muse-cli"
	if usage.InputTokens > 0 {
		gi.InputTokens = museIntPtr(usage.InputTokens)
	}
	if usage.OutputTokens > 0 {
		gi.OutputTokens = museIntPtr(usage.OutputTokens)
	}
	if usage.TotalTokens > 0 {
		gi.TotalTokens = museIntPtr(usage.TotalTokens)
	}
	if usage.ReasoningTokens != nil && *usage.ReasoningTokens > 0 {
		r := *usage.ReasoningTokens
		gi.ReasoningTokens = &r
	}
	cache := 0
	if usage.CacheTokens != nil {
		cache = *usage.CacheTokens
	}
	if cache > 0 {
		gi.CachedContentTokens = museIntPtr(cache)
		gi.Additional["cache_read_input_tokens"] = cache
		gi.Additional["prompt_tokens_include_cache"] = true
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultModelID
	}
	if meta, err := GetMuseModelMetadata(model); err == nil && meta != nil {
		if cost := llmtypes.ComputeUSDCostFromMetadata(meta, gi); cost > 0 {
			gi.Additional["cost_usd_estimated"] = cost
			gi.Additional["cost_model_id"] = meta.ModelID
		}
	}
}

// readMuseTranscriptMessages maps one run's committed prompts and assistant
// texts to message content (file order). System rows, non-run envelopes,
// and empty texts are skipped. ok=false only when the log is unreadable.
func readMuseTranscriptMessages(logPath, runID string, includeTools ...bool) ([]llmtypes.MessageContent, bool) {
	var out []llmtypes.MessageContent
	seen, ended := map[string]bool{}, map[string]bool{}
	starts := map[string]time.Time{}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if pt, _ := rec["payload_type"].(string); pt == "runtime.command_intake.received" {
			if runID != "" && museLogString(rec, "payload", "record", "command_id") != runID {
				continue
			}
			if text := museStripResumeNote(museLogString(rec, "payload", "record", "command", "prompt")); strings.TrimSpace(text) != "" {
				out = append(out, llmtypes.MessageContent{
					Role:  llmtypes.ChatMessageTypeHuman,
					Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}},
				})
			}
			continue
		}
		// Muse 1.1.1 persistent-TUI follow-ups are recorded as accepted user
		// intents rather than command_intake rows. Keep the first text refill
		// block as the canonical human message; model_messages contains the same
		// text and would otherwise duplicate it during chat-history resync.
		if pt, _ := rec["payload_type"].(string); pt == "runtime.user_intent.accepted" {
			payload, _ := rec["payload"].(map[string]any)
			intentID := museLogString(payload, "intent_id")
			if runID != "" && intentID != runID {
				continue
			}
			if blocks, _ := payload["refill_blocks"].([]any); len(blocks) > 0 {
				if block, _ := blocks[0].(map[string]any); block != nil {
					if text, _ := block["text"].(string); strings.TrimSpace(museStripResumeNote(text)) != "" {
						text = museStripResumeNote(text)
						out = append(out, llmtypes.MessageContent{
							Role:  llmtypes.ChatMessageTypeHuman,
							Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}},
						})
					}
				}
			}
			continue
		}
		if len(includeTools) > 0 && includeTools[0] {
			for _, chunk := range museTranscriptLineToChunks(line, seen, ended, starts) {
				if chunk.Type == llmtypes.StreamChunkTypeToolCallStart {
					out = append(out, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: chunk.ToolCallID, Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: chunk.ToolName, Arguments: chunk.ToolArgs}}}})
				}
				if chunk.Type == llmtypes.StreamChunkTypeToolCallEnd {
					out = append(out, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: chunk.ToolCallID, Content: chunk.ToolResult}}})
				}
			}
		}
		payload, _ := rec["payload"].(map[string]any)
		if payload == nil || museLogString(payload, "kind") != "run" {
			continue
		}
		if runID != "" && museLogString(payload, "run_id") != runID {
			continue
		}
		event, _ := payload["event"].(map[string]any)
		if event == nil || museLogString(event, "kind") != "assistant_message_committed" {
			continue
		}
		if text := museLogString(event, "text"); strings.TrimSpace(text) != "" {
			out = append(out, llmtypes.MessageContent{
				Role:  llmtypes.ChatMessageTypeAI,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}},
			})
		}
	}
	return out, true
}

// museUsageForTurn resolves the session log and reads one turn's usage.
// Best-effort by design (the cost doc's rule): never fail a turn over
// telemetry. ok=false means "no data", not "zero usage".
func museUsageForTurn(sessionID, runID string) (llmtypes.Usage, bool) {
	path := museSessionLogPath(sessionID)
	if path == "" {
		return llmtypes.Usage{}, false
	}
	return readMuseTranscriptUsage(path, runID)
}
