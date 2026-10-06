package step_based_workflow

import (
	"context"
	"fmt"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"time"
)

// executePreparedRun is the shared session lifecycle and step execution path.
// Goals invoke it per batch group; Relays invoke it once per run, with no group.
func (hcpo *StepBasedWorkflowOrchestrator) executePreparedRun(ctx context.Context, breakdownSteps []PlanStepInterface, iteration int, setup *ExecutionSetup) error {
	previousSessionID := hcpo.getSessionID()
	// Apply execution context (sets orchestrator state, including selectedRunFolder)
	// This MUST be done before setting session ID so that Downloads path override uses correct run folder
	hcpo.GetExecutionManager().ApplyExecutionContext(setup)
	hcpo.GetLogger().Info(fmt.Sprintf("🔍 [DEBUG] After ApplyExecutionContext - selectedRunFolder: '%s', runFolder: '%s'", hcpo.selectedRunFolder, setup.RunFolder))

	// Each run owns its stateful MCP connections and Downloads directory.
	runSessionID := fmt.Sprintf("session-run-%s", workflowExecutionIDToken())
	hcpo.sessionID = runSessionID
	hcpo.BaseOrchestrator.SetMCPSessionID(runSessionID)
	virtualtools.InheritSessionNotificationDestination(previousSessionID, runSessionID)
	hcpo.GetLogger().Info(fmt.Sprintf("🔗 Generated unique MCP session ID for group %s: %s (run folder: %s)", setup.GroupName, runSessionID, hcpo.selectedRunFolder))
	if pc := virtualtools.GetParentChat(previousSessionID); pc != nil && pc.SessionID != "" {
		pcCopy := *pc
		if pcCopy.GroupName == "" {
			pcCopy.GroupName = setup.GroupName
		}
		virtualtools.RegisterParentChat(runSessionID, &pcCopy)
		hcpo.GetLogger().Info(fmt.Sprintf("🔗 Registered parent chat for group session %s from previous session %s", runSessionID, previousSessionID))
	}
	// Track group session under HTTP session so stop handler can close it immediately
	if hcpo.httpSessionID != "" {
		mcpagent.RegisterHTTPSession(hcpo.httpSessionID, runSessionID)
		// Inherit folder guard from parent HTTP session so sub-agents running
		// under this group session ID cannot bypass write restrictions (e.g.,
		// planning/ is read-only in workflow-builder mode).
		common.CopySessionFolderGuard(hcpo.httpSessionID, runSessionID)
	}

	// Close MCP session after this group completes to free resources (browser profiles, etc.)
	// Use defer to ensure cleanup even if execution fails.
	// IMPORTANT: Mark as stopped BEFORE closing to prevent in-flight tool calls
	// (from code-exec agents still running in Docker) from resurrecting connections
	// via broken pipe handlers or mcpcache fallback.
	// Also resolve the browser session ID so we can mark it as stopped too.
	// The actual stateful MCP connection lives under this ID, not the group session ID.
	browserSessionID := hcpo.resolveWorkshopBrowserSessionID(setup.GroupName)
	if hcpo.isRelayExecution() {
		browserSessionID = runSessionID
	}
	hcpo.bindWorkshopBrowserSession(runSessionID, browserSessionID)
	cdpPorts := hcpo.cdpPortsForCleanup()
	browser.AcquireCDPTabOwnerLease(browserSessionID, cdpPorts)
	defer func() {
		hcpo.GetLogger().Info(fmt.Sprintf("🔗 Closing MCP session for group %s: %s (browser=%s)", setup.GroupName, runSessionID, browserSessionID))
		virtualtools.UnregisterParentChat(runSessionID)
		virtualtools.DeleteSessionNotificationDestination(runSessionID)
		mcpagent.MarkSessionsStopped([]string{runSessionID, browserSessionID})
		mcpagent.CloseSession(runSessionID)
		mcpagent.CloseSession(browserSessionID)
		browser.ReleaseCDPTabOwnerLease(browserSessionID, cdpPorts, browser.NewClient(getWorkspaceAPIURL()), browser.DefaultCDPTabCleanupDelay)
	}()

	// Load the freshly initialized progress (created by ApplyCleanup)
	progress, err := hcpo.loadStepProgress(ctx)
	if err != nil {
		// If loading fails, create in-memory progress
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to load progress for group %s, using in-memory: %v", setup.GroupName, err))
		progress = &StepProgress{
			CompletedStepIndices:    make([]int, 0),
			TotalSteps:              len(breakdownSteps),
			LastUpdated:             time.Now(),
			RoutingEvaluationCounts: make(RoutingEvaluationCount),
		}
	}

	// Run execution phase for this group
	err = hcpo.runExecutionPhase(ctx, breakdownSteps, iteration, progress, setup.StartFromStep, setup.Context, nil)
	persistenceErr := error(nil)
	if cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge); ok {
		flushCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		persistenceErr = cab.WaitForTokenPersistence(flushCtx)
		cancel()
		if persistenceErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("Workflow execution finished, but cost persistence did not: %v", persistenceErr))
			hcpo.recordRunPersistenceError(context.Background(), "cost_usage", persistenceErr)
		}
	}

	return err
}
