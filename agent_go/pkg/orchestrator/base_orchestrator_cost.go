package orchestrator

import (
	"context"
	"fmt"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// agentObserverSink is the slice of the agent surface this file needs:
// somewhere to register an event listener before the agent is finalized.
// *agents.BaseAgent satisfies it.
type agentObserverSink interface {
	AddObserver(observer mcpagent.AgentEventListener) error
}

// attachCostObserver records this agent's LLM spend in the process-wide cost
// ledger.
//
// setupStandardAgent is the single place every orchestrator-created agent
// passes through — workflow steps, todo_task predefined and generic
// sub-agents, and the background stages behind the workshop's
// call_generic_agent (Pulse reviewers, Pulse fixers, Goal Advisor stages).
// None of them were attached to the ledger before, which is why a 56-minute
// Pulse review of four reviewers produced no cost rows at all: the only two
// attachment points in the process were the chat path and delegate().
//
// A nil default ledger is not an error. Tests, CLI tools, and any process that
// never called costledger.SetDefaultLedger simply have nowhere to record; the
// server publishes the ledger during startup, long before a workflow or a
// background agent can launch.
func (bo *BaseOrchestrator) attachCostObserver(
	ctx context.Context,
	agent agentObserverSink,
	config *agents.OrchestratorAgentConfig,
	agentName, phase, stepID string,
) error {
	if bo == nil || agent == nil || config == nil {
		return nil
	}
	ledger := costledger.DefaultLedger()
	if ledger == nil {
		return nil
	}

	sessionID := strings.TrimSpace(config.MCPSessionID)
	if sessionID == "" {
		sessionID = bo.GetMCPSessionID()
	}
	userID, _ := ctx.Value(common.UserIDKey).(string)
	runFolder := strings.TrimSpace(bo.GetIterationFolder())
	launchPath := fmt.Sprintf("orchestrator.setupStandardAgent(phase=%q step=%q agent=%q)", phase, stepID, agentName)

	observer := costobserver.New(
		ledger,
		sessionID,
		strings.TrimSpace(userID),
		bo.GetAgentMode(),
		costobserver.WithModel(config.LLMConfig.Primary.Provider, config.LLMConfig.Primary.ModelID),
		costobserver.WithAttribution(
			// Scope signals are deliberately limited to identifiers the runtime
			// generates — the phase, the agent name, and the run folder. Step
			// ids come from user-authored workflow plans, so a step called
			// "evaluate-leads" must not file itself under the evaluation scope.
			costobserver.InferWorkflowScope(bo.GetAgentMode(), runFolder != "", phase, agentName, runFolder),
			bo.GetWorkspacePath(),
			runFolder,
			agentCostExecutionID(ctx, config, stepID),
		),
		costobserver.WithSourcePlatform(costobserver.SourcePlatformFromContext(ctx)),
		costobserver.WithLaunchPath(launchPath),
	)
	if err := agent.AddObserver(observer); err != nil {
		return fmt.Errorf("attach cost observer to %s: %w", agentName, err)
	}
	return nil
}

// agentCostExecutionID finds the identifier this agent's work is already
// tracked under, so ledger rows group the same way the UI's execution cards
// do. Background and generic agents mint one before they launch and carry it
// on the child context; nothing new is invented here.
func agentCostExecutionID(ctx context.Context, config *agents.OrchestratorAgentConfig, stepID string) string {
	if ctx != nil {
		// Background/generic launches (Pulse reviewers, call_generic_agent
		// children, message-sequence steps) set this to the execution id the
		// launch site registered with the execution notifier.
		if agentID := strings.TrimSpace(virtualtools.SubAgentSpecFromContext(ctx).BackgroundAgentID); agentID != "" {
			return agentID
		}
		if parentID, _ := ctx.Value(orchEvents.ParentExecutionIDKey).(string); strings.TrimSpace(parentID) != "" {
			return strings.TrimSpace(parentID)
		}
	}
	// A plain workflow step outside any registered execution still has a
	// stable per-agent identity: its own MCP session plus the step it runs.
	sessionID := ""
	if config != nil {
		sessionID = strings.TrimSpace(config.MCPSessionID)
	}
	stepID = strings.TrimSpace(stepID)
	switch {
	case sessionID != "" && stepID != "":
		return sessionID + ":" + stepID
	case sessionID != "":
		return sessionID
	default:
		return stepID
	}
}
