package mcpagent

import (
	"context"
	"log"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// emitRetainedTurnUsage reports the usage the coding CLI recorded in its own transcript for a turn that was typed into
// the running terminal. Such a turn never passes through GenerateContent, so nothing else reports its tokens: before
// this the cost ledger, the account limits that read it and the Costs page all missed every follow-up after the Run.
// One LLM-generation-end event is emitted per run, message or step the CLI reports (each id is handed out once by the
// provider layer), through the same path a normal call takes, so pricing and attribution are identical. Every read
// and every skip is logged (the reason, never a prompt).
func (s *Session) emitRetainedTurnUsage(lifecycle *canonicalTurnLifecycle, provider llm.Provider, startedAt time.Time) {
	owner := s.agent.sessionID
	reader := s.retainedUsage
	if reader == nil {
		reader = llmproviders.ReadCodingAgentRetainedTurnUsage
	}
	items := reader(provider, owner, s.agent.modelID, startedAt)
	if len(items) == 0 {
		log.Printf("[RETAINED_USAGE] session=%s provider=%s no new usage to report for this turn", owner, provider)
		return
	}
	ctx := withCanonicalTurnLifecycle(context.Background(), lifecycle)
	for _, item := range items {
		info := item.Info
		usage := item.Usage
		// Name where this row came from, so a ledger row traces to the CLI's own run/message id (and a later backfill
		// can skip what is already recorded). GenerationInfo.Additional is copied onto the event metadata.
		additional := make(map[string]interface{}, len(info.Additional)+2)
		for k, v := range info.Additional {
			additional[k] = v
		}
		additional["usage_source"], additional["usage_source_id"] = "retained", item.ID
		info.Additional = additional
		resp := &llmtypes.ContentResponse{
			Choices: []*llmtypes.ContentChoice{{GenerationInfo: &info}},
			Usage:   &usage,
		}
		metrics := events.UsageMetrics{PromptTokens: usage.InputTokens, CompletionTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens}
		s.agent.endLLMGeneration(ctx, "", 0, 0, 0, metrics, resp)
		log.Printf("[RETAINED_USAGE] session=%s provider=%s id=%s input=%d output=%d cost event emitted", owner, provider, item.ID, usage.InputTokens, usage.OutputTokens)
	}
}
