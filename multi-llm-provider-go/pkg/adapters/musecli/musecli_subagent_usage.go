package musecli

import (
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// museSubagentRun is the usage one native subagent reported (rows first..last of its own session log).
type museSubagentRun struct {
	ID          string
	Model       string // the model the subagent's own log says it ran on ("" when the log has no record)
	First, Last int64
	Usage       llmtypes.Usage
}

// museSubagentUsage sums the usage Muse's native subagents reported since `since` that nobody has reported yet.
// A subagent has its own session log (<session dir>/subagent/<id>/session.jsonl) whose usage rows never reach the
// parent log, so neither the Run's read nor the retained-turn read of the parent saw that spend. Rows are claimed
// through the same first-reporter-wins registry as the parent's, keyed by the subagent's own log path.
func museSubagentUsage(parentLog string, since time.Time) []museSubagentRun {
	if parentLog == "" {
		return nil
	}
	root := filepath.Join(filepath.Dir(parentLog), "subagent")
	var out []museSubagentRun
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != "session.jsonl" {
			return nil
		}
		raw, readErr := os.ReadFile(path) //nolint:gosec // adapter-owned transcript path
		if readErr != nil {
			return nil
		}
		one := museSubagentRun{ID: filepath.Base(filepath.Dir(path))}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			isModel := strings.Contains(line, "run.model.configured")
			if !isModel && !strings.Contains(line, "goal_usage_attribution") {
				continue
			}
			var rec map[string]any
			if json.Unmarshal([]byte(line), &rec) != nil {
				continue
			}
			if isModel {
				if model := museLogString(rec, "payload", "record", "model_id"); model != "" {
					one.Model = model
				}
				continue
			}
			payload, _ := rec["payload"].(map[string]any)
			event, _ := payload["event"].(map[string]any)
			if payload == nil || museLogString(payload, "kind") != "run" || event == nil || museLogString(event, "kind") != "goal_usage_attribution" {
				continue
			}
			record, _ := event["record"].(map[string]any)
			quantity, _ := record["quantity"].(map[string]any)
			if quantity == nil || !museLogBool(quantity, "reported") {
				continue
			}
			if at, ok := rec["recorded_at"].(float64); ok && !since.IsZero() && time.UnixMicro(int64(at)).Before(since) {
				continue
			}
			sequence, _ := rec["sequence"].(float64)
			if !museMarkUsageReported(path, int64(sequence)) {
				continue
			}
			if one.First == 0 {
				one.First = int64(sequence)
			}
			one.Last = int64(sequence)
			one.Usage.InputTokens += museLogInt(quantity, "input_tokens")
			one.Usage.OutputTokens += museLogInt(quantity, "output_tokens")
			if cached := museLogInt(quantity, "cached_tokens"); cached > 0 {
				sum := cached
				if one.Usage.CacheTokens != nil {
					sum += *one.Usage.CacheTokens
				}
				one.Usage.CacheTokens = &sum
			}
			if reasoning := museLogInt(quantity, "reasoning_tokens"); reasoning > 0 {
				sum := reasoning
				if one.Usage.ReasoningTokens != nil {
					sum += *one.Usage.ReasoningTokens
				}
				one.Usage.ReasoningTokens = &sum
			}
		}
		if one.First > 0 {
			one.Usage.TotalTokens = one.Usage.InputTokens + one.Usage.OutputTokens
			out = append(out, one)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	for _, one := range out {
		log.Printf("[RETAINED_USAGE] muse subagent=%s model=%q rows=%d-%d input=%d output=%d cached=%d reported", one.ID, one.Model, one.First, one.Last, one.Usage.InputTokens, one.Usage.OutputTokens, museCacheValue(one.Usage))
	}
	return out
}

// addMuseUsage adds b into a.
func addMuseUsage(a *llmtypes.Usage, b llmtypes.Usage) {
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.TotalTokens = a.InputTokens + a.OutputTokens
	if b.CacheTokens != nil {
		sum := *b.CacheTokens
		if a.CacheTokens != nil {
			sum += *a.CacheTokens
		}
		a.CacheTokens = &sum
	}
	if b.ReasoningTokens != nil {
		sum := *b.ReasoningTokens
		if a.ReasoningTokens != nil {
			sum += *a.ReasoningTokens
		}
		a.ReasoningTokens = &sum
	}
}

// museAttachSubagentCost prices a subagent's usage with the model its own log names; when the log names none, or the
// rate card does not know it, the parent's model is used and the item says so (usage_rate_assumed=parent).
func museAttachSubagentCost(gi *llmtypes.GenerationInfo, subModel, parentModel string, usage *llmtypes.Usage) {
	if subModel != "" {
		if meta, err := GetMuseModelMetadata(subModel); err == nil && meta != nil {
			museAttachTurnCost(gi, subModel, usage)
			gi.Additional["subagent_model"] = subModel
			return
		}
	}
	museAttachTurnCost(gi, parentModel, usage)
	gi.Additional["usage_rate_assumed"] = "parent"
}
