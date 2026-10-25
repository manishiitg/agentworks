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
		costobserver.WithAccount(costobserver.AccountIDFor(config.LLMConfig.Primary.Provider, config.LLMConfig.Primary.ConnectionID)),
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
		costobserver.WithProductAttribution(costobserver.AttributionFromContext(ctx)),
		costobserver.WithSourceFromContext(ctx),
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
