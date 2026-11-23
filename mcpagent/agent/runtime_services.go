package mcpagent

import (
	"context"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func SubscribeAgentEvents(ctx context.Context, agent *Agent) (<-chan *events.AgentEvent, func(), bool) {
	if agent == nil {
		return nil, func() {}, false
	}
	return agent.subscribeToEvents(ctx)
}

// AgentDiagnostics is a read-only operational snapshot. Identity is exposed
// separately by Agent.Definition and turn accounting by Result.Usage.
type AgentDiagnostics struct {
	Usage       Usage
	ServerNames []string
	Connections int
}

func ReadAgentDiagnostics(agent *Agent) AgentDiagnostics {
	if agent == nil {
		return AgentDiagnostics{}
	}
	prompt, completion, total, cache, reasoning, calls, cacheCalls,
		inputCost, outputCost, reasoningCost, cacheCost, totalCost, contextUsage := agent.getTokenUsageWithPricing()
	cacheRead, cacheWrite := agent.getCacheTokenBreakdown()
	diagnostics := AgentDiagnostics{
		Usage: Usage{
			PromptTokens:         prompt,
			CompletionTokens:     completion,
			TotalTokens:          total,
			CacheTokens:          cache,
			CacheReadTokens:      cacheRead,
			CacheWriteTokens:     cacheWrite,
			ReasoningTokens:      reasoning,
			LLMCalls:             calls,
			CacheEnabledLLMCalls: cacheCalls,
			InputCostUSD:         inputCost,
			OutputCostUSD:        outputCost,
			ReasoningCostUSD:     reasoningCost,
			CacheCostUSD:         cacheCost,
			TotalCostUSD:         totalCost,
			ContextUsagePercent:  contextUsage,
		},
		ServerNames: agent.getServerNames(),
		Connections: len(agent.clients),
	}
	return diagnostics
}

// The helpers below isolate low-level bridge and large-output test harnesses
// from the runtime Agent surface. They are not agent identity operations.
func InvokeAgentVirtualTool(ctx context.Context, agent *Agent, name string, args map[string]interface{}) (string, error) {
	return agent.handleVirtualTool(ctx, name, args)
}

type AgentRuntimeInfo struct {
	Provider             llm.Provider
	LLMConfig            LLMModel
	ConfiguredServerName string
	SelectedTools        []string
	EffectiveModelID     string
}

func ReadAgentRuntimeInfo(agent *Agent) AgentRuntimeInfo {
	if agent == nil {
		return AgentRuntimeInfo{}
	}
	return AgentRuntimeInfo{
		Provider:             agent.getProvider(),
		LLMConfig:            agent.getLLMModelConfig(),
		ConfiguredServerName: agent.getConfiguredServerName(),
		SelectedTools:        agent.getSelectedTools(),
		EffectiveModelID:     agent.getEffectiveModelID(),
	}
}

func findDefinitionTool(agent *Agent, name string) (ToolDefinitionView, bool) {
	if agent == nil {
		return ToolDefinitionView{}, false
	}
	for _, tool := range agent.Definition().Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return ToolDefinitionView{}, false
}

func DeliverAgentInput(ctx context.Context, agent *Agent, req UserMessageDeliveryRequest) (UserMessageDeliveryResult, error) {
	return agent.deliverUserMessage(ctx, req)
}

// AgentSupportsSteering reports whether agent's transport accepts live input
// into a running turn — see Agent.supportsSteering. A structured/JSON
// coding-agent run (Cursor's one-shot `cursor-agent --print`, etc.) has no
// live pane to steer input into between turns, so it always reports false
// here regardless of what the provider's contract otherwise allows on tmux.
// Callers deciding whether to attempt a live steer (as opposed to queuing for
// the next turn boundary) should check this first, the same way
// Agent.deliverUserMessage itself does.
func AgentSupportsSteering(agent *Agent) bool {
	if agent == nil {
		return false
	}
	return agent.supportsSteering()
}

func DeliverAgentControlKey(ctx context.Context, agent *Agent, req ControlKeyDeliveryRequest) (ControlKeyDeliveryResult, error) {
	return agent.deliverControlKey(ctx, req)
}

func StartAgentTransportSession(ctx context.Context, agent *Agent) (*llmtypes.CodingProviderSessionHandle, error) {
	return agent.startCodingAgentTransportSession(ctx)
}

func SnapshotAgentSession(agent *Agent) *AgentSessionHandle {
	if agent == nil {
		return nil
	}
	return agent.currentAgentSessionHandle()
}

// ApplyAgentResumeHandle restores provider-native continuation state from a
// persisted handle. It reports whether the handle was accepted; on false
// no agent state was mutated and the caller must replay history rather
// than assume native context.
func ApplyAgentResumeHandle(agent *Agent, handle *AgentSessionHandle) bool {
	if agent == nil {
		return false
	}
	return agent.applyAgentSessionHandle(handle)
}

// ReadAgentSystemPrompt renders the exact outbound system text for the current
// context and grants without starting a turn or invoking the model. Attached
// native CLI skill indexes and provider-owned instructions remain separate.
func ReadAgentSystemPrompt(ctx context.Context, agent *Agent) string {
	if agent == nil {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return agent.outgoingSystemPromptForContext(ctx)
}
