package mcpagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// CompactAgentSessionIdle runs the coding CLI's own compaction on the agent's
// restored native session without sending a message (idle compaction,
// PLAT-872). The agent must be built exactly as for the chat's next turn, so
// the resumed conversation keeps the same prompt prefix and provider cache.
// The compaction's usage is emitted as one LLM generation (cost ledger);
// its compaction status reaches the chat through the normal stream events.
func CompactAgentSessionIdle(ctx context.Context, agent *Agent) (llmtypes.IdleCompactResult, error) {
	if agent == nil {
		return llmtypes.IdleCompactResult{}, fmt.Errorf("agent is nil")
	}
	return agent.compactCodingAgentSessionIdle(ctx)
}

func (a *Agent) compactCodingAgentSessionIdle(ctx context.Context) (llmtypes.IdleCompactResult, error) {
	if _, ok := a.codingContract(); !ok {
		return llmtypes.IdleCompactResult{Reason: llmtypes.IdleCompactReasonUnsupported}, nil
	}
	if _, ok := a.codingProviderContinuationHandleForModel(a.provider, a.modelID); !ok {
		return llmtypes.IdleCompactResult{Reason: llmtypes.IdleCompactReasonNoSession}, nil
	}
	if err := a.prepareCodingAgentTransportLaunch(); err != nil {
		return llmtypes.IdleCompactResult{}, err
	}
	primary := a.getEffectiveLLMConfig().Primary
	if strings.TrimSpace(primary.Provider) == "" {
		primary.Provider = string(a.provider)
	}
	if strings.TrimSpace(primary.ModelID) == "" {
		primary.ModelID = a.modelID
	}
	opts := []llmtypes.CallOption{llmtypes.WithCodingProviderIdleCompact()}
	started := time.Now()
	sm := a.startStreaming(ctx, 0, 0, &opts)
	resp, err := a.executeLLMForCodingAgentTransportLaunch(ctx, primary, opts)
	a.drainStreamingWithoutEnd(sm)
	if err != nil {
		return llmtypes.IdleCompactResult{}, err
	}
	a.updateCodingProviderSessionHandleFromResponse(resp)
	result, ok := llmtypes.IdleCompactResultFromResponse(resp)
	if !ok {
		result = llmtypes.IdleCompactResult{Reason: llmtypes.IdleCompactReasonUnsupported}
	}
	if resp != nil && resp.Usage != nil && resp.Usage.TotalTokens > 0 {
		a.startLLMGeneration(ctx)
		a.endLLMGeneration(ctx, "", 0, 0, time.Since(started), events.UsageMetrics{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}, resp)
	}
	return result, nil
}
