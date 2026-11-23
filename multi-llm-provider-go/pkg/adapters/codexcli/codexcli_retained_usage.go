package codexcli

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ReadRetainedTurnUsage returns what this owner's terminal spent since turnStart that nobody has reported yet: the
// growth of the rollout's cumulative token total (see readCodexTranscriptUsageFile), as one entry whose ID names the
// thread and the cumulative total it reaches, so it is stable. Read-only; it marks what it returns.
func ReadRetainedTurnUsage(ownerSessionID string, turnStart time.Time) []llmtypes.RetainedTurnUsage {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	session, ok := codexPersistentRegistry.Get(ownerSessionID)
	if !ok || session == nil {
		log.Printf("[RETAINED_USAGE] codex-cli owner=%s skipped: no terminal registered for this owner", ownerSessionID)
		return nil
	}
	path := resolveCodexRolloutPath(session, turnStart)
	if path == "" {
		log.Printf("[RETAINED_USAGE] codex-cli owner=%s skipped: no rollout resolved", ownerSessionID)
		return nil
	}
	info, model, threadID, _ := readCodexTranscriptUsageFile(path, turnStart)
	if info == nil {
		log.Printf("[RETAINED_USAGE] codex-cli owner=%s no new usage since %s", ownerSessionID, turnStart.UTC().Format(time.RFC3339))
		return nil
	}
	reported, _ := codexReportedTotal(path)
	item := llmtypes.RetainedTurnUsage{ID: fmt.Sprintf("%s:%d", threadID, reported.TotalTokens), Info: *info}
	if info.PromptTokens != nil {
		item.Usage.InputTokens = *info.PromptTokens
		if info.CachedContentTokens != nil {
			item.Usage.InputTokens += *info.CachedContentTokens
			cached := *info.CachedContentTokens
			item.Usage.CacheTokens = &cached
		}
	}
	if info.CompletionTokens != nil {
		item.Usage.OutputTokens = *info.CompletionTokens
	}
	item.Usage.TotalTokens = item.Usage.InputTokens + item.Usage.OutputTokens
	if model != "" {
		if item.Info.Additional == nil {
			item.Info.Additional = map[string]interface{}{}
		}
		item.Info.Additional["codex_model"] = model
	}
	log.Printf("[RETAINED_USAGE] codex-cli owner=%s id=%s input=%d output=%d reported", ownerSessionID, item.ID, item.Usage.InputTokens, item.Usage.OutputTokens)
	return []llmtypes.RetainedTurnUsage{item}
}
