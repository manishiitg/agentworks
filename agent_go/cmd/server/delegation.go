// Synchronous (blocking) delegation: spawning a sub-agent for a delegated
// task, the workshop sub-agent tracking notifier, and the delegation
// start/end UI events. Relocated verbatim from server.go.
// (Async/background delegation lives in background_agents.go.)
package server

import (
	"context"
	"encoding/json"
	"fmt"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	browserinstructions "github.com/manishiitg/coding-agent-loop/agent_go/pkg/instructions"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/subagents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func safeDelegationRuntimeID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Sprintf("delegation-%d", time.Now().UnixNano())
	}
	var b strings.Builder
	b.Grow(len(id))
	lastDash := false
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	clean := strings.Trim(b.String(), "-")
	if clean == "" {
		return fmt.Sprintf("delegation-%d", time.Now().UnixNano())
	}
	if len(clean) > 96 {
		clean = strings.Trim(clean[:96], "-")
		if clean == "" {
			return fmt.Sprintf("delegation-%d", time.Now().UnixNano())
		}
	}
	return clean
}

func delegatedCodingAgentRuntimeFolder(userID, runtimeID string) string {
	return strings.TrimSuffix(perUserChatsFolderFor(userID), "/") + "/.agents/" + safeDelegationRuntimeID(runtimeID)
}

func scopeDelegatedHumanInputExecutor(workflowPath string, executor func(context.Context, map[string]interface{}) (string, error)) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		requested, _ := args["workspace_path"].(string)
		normalized, normalizeErr := normalizeReportHumanInputWorkspacePath(requested)
		if normalizeErr != nil {
			return "", normalizeErr
		}
		if normalized != workflowPath {
			return "", fmt.Errorf("background workflow agent may create human-input requests only for %s", workflowPath)
		}
		return executor(ctx, args)
	}
}

// --- Background Agent Infrastructure for Async Delegation ---

// workshopExecutionBgNotifier implements WorkshopExecutionNotifier by registering
// workshop step/background executions in bgAgentRegistry so that HasRunningAgents()
// returns true and the frontend keeps polling for events.
type workshopExecutionBgNotifier struct {
	api           *StreamingAPI
	sessionID     string
	workspacePath string
	presetQueryID string
	userID        string
}

func (n *workshopExecutionBgNotifier) OnExecutionStart(start todo_creation_human.WorkshopExecutionStart) {
	if n.api.autoNotificationSessionUnreachable(n.sessionID) {
		log.Printf("[BG AGENT] OnExecutionStart ignored for stopped session %s (exec=%s)", n.sessionID, start.ID)
		if start.Cancel != nil {
			start.Cancel()
		}
		return
	}
	// A declared kind always wins. The name/id prefix sniffing below predates
	// declared kinds and exists only to classify executions that never set one.
	kind := strings.TrimSpace(start.Kind)
	if kind == "" {
		if isWorkflowStepTrackingExecution(start.ID, start.Name, start.Metadata) {
			kind = "workflow_step"
		} else {
			kind = "workshop_background"
		}
	}
	parentExecutionID := strings.TrimSpace(start.ParentExecutionID)
	if parentExecutionID == "" {
		// Workshop sessions deliberately detach long-running work from the
		// initiating HTTP context. If an older/custom launch path did not copy
		// the query root before detaching, recover the exact currently-running
		// conversation turn here rather than registering an orphan execution.
		parentExecutionID = n.api.currentConversationTurnExecutionID(n.sessionID)
	}
	metadata := map[string]string{
		"workflow_path":    n.workspacePath,
		"preset_query_id":  n.presetQueryID,
		"execution_source": trackedExecutionSourceWorkshopBackground,
	}
	for k, v := range start.Metadata {
		if strings.TrimSpace(k) == "" {
			continue
		}
		metadata[k] = v
	}
	bgAgent := &BackgroundAgent{
		ID:                start.ID,
		ParentExecutionID: parentExecutionID,
		Name:              start.Name,
		SessionID:         n.sessionID,
		Kind:              kind,
		Status:            BGAgentRunning,
		CreatedAt:         time.Now(),
		cancel:            start.Cancel,
		Metadata:          metadata,
	}
	n.api.bgAgentRegistry.Register(n.sessionID, bgAgent)
	n.api.trackWorkshopExecutionStart(n.sessionID, n.workspacePath, n.presetQueryID, n.userID, start.ID, start.Name, parentExecutionID, metadata)
	if bgAgent.GetStatus() == BGAgentCanceled {
		n.api.completeTrackedExecution(start.ID, trackedExecutionStatusCanceled, "parent execution canceled", metadata)
		if bgAgent.MarkTerminalNotified() {
			n.api.emitBackgroundAgentTerminated(n.sessionID, start.ID, start.Name, "")
		}
		return
	}

	// Pre-create the channel so NotifyCompletion never drops a completion
	n.api.bgAgentRegistry.GetNotificationChannel(n.sessionID)

	// Ensure background completion loop is running
	n.api.completionLoopStartedMu.Lock()
	if !n.api.completionLoopStarted[n.sessionID] {
		n.api.completionLoopStarted[n.sessionID] = true
		go n.api.backgroundCompletionLoop(n.sessionID)
	}
	n.api.completionLoopStartedMu.Unlock()

	// Emit background_agent_started so the execution remains visible and follows
	// the same parent-notification lifecycle as workflow steps. Long coding-CLI
	// tool calls may detach from the foreground even though their HTTP request is
	// still active, so generic/reviewer children must notify the parent rather than
	// relying only on the synchronous response path.
	// Forward the kind resolved above (creator's declaration, or the
	// workshop_background default, or the workflow_step override) so the
	// terminal store never has to re-infer it from the execution id.
	n.api.emitBackgroundAgentStarted(n.sessionID, start.ID, start.Name, "", parentExecutionID, orchEvents.ParseExecutionKind(kind))
	if metadata["suppress_auto_notification"] != "true" {
		n.api.notifyBackgroundAgentStarted(n.sessionID, start.ID)
	}
}

func isWorkflowStepTrackingExecution(id, name string, meta map[string]string) bool {
	if meta != nil && strings.TrimSpace(meta["execution_type"]) == "workflow-step" {
		return true
	}
	trimmedName := strings.TrimSpace(name)
	if strings.HasPrefix(trimmedName, "Step ->") || strings.HasPrefix(trimmedName, "Workflow step ->") {
		return true
	}
	trimmedID := strings.TrimSpace(id)
	return strings.HasPrefix(trimmedID, "workflow-step-") ||
		(strings.HasPrefix(trimmedID, "workflow-full-") && strings.Contains(trimmedID, "-step-"))
}

// suppressRepeatedChildFailureNotification marks an enclosing execution silent
// when it failed only because a direct message-sequence child already reported
// the same root error. The child remains the authoritative notification; the
// parent is still recorded and emitted as a terminal execution event, but it
// must not create a second synthetic user turn containing the same failure.
// Keeping this at the backend notification boundary makes the behavior
// consistent for every product and every client.
func suppressRepeatedChildFailureNotification(registry *BackgroundAgentRegistry, sessionID string, parent *BackgroundAgent) bool {
	if registry == nil || parent == nil {
		return false
	}
	parentSnapshot := parent.GetSnapshot()
	if parentSnapshot.Status != BGAgentFailed || strings.TrimSpace(parentSnapshot.Error) == "" {
		return false
	}
	for _, candidate := range registry.GetAll(sessionID) {
		if candidate == nil || candidate == parent {
			continue
		}
		child := candidate.GetSnapshot()
		if strings.TrimSpace(child.ParentExecutionID) != parentSnapshot.ID || child.Status != BGAgentFailed {
			continue
		}
		isMessageSequenceItem := child.Kind == "message_sequence_item" ||
			(child.Metadata != nil && child.Metadata["execution_type"] == "message-sequence-item")
		childError := strings.TrimSpace(child.Error)
		if !isMessageSequenceItem || childError == "" || !strings.Contains(parentSnapshot.Error, childError) {
			continue
		}
		parent.SetMetadata(map[string]string{
			"suppress_auto_notification": "true",
			"notification_suppression":   "repeated-message-sequence-child-failure",
		})
		return true
	}
	return false
}

// suppressParentOwnedMessageSequenceSuccess keeps successful completion at the
// workflow-step boundary. A message-sequence item is an implementation detail
// of that step; publishing both the child success and the parent success creates
// two identical "Step complete" turns. Failures deliberately take the opposite
// path above so the specific child error remains visible. A standalone item is
// never suppressed because there is no registered parent that can report its
// completion.
func suppressParentOwnedMessageSequenceSuccess(registry *BackgroundAgentRegistry, sessionID string, child *BackgroundAgent) bool {
	if registry == nil || child == nil {
		return false
	}
	snapshot := child.GetSnapshot()
	isMessageSequenceItem := snapshot.Kind == "message_sequence_item" ||
		(snapshot.Metadata != nil && snapshot.Metadata["execution_type"] == "message-sequence-item")
	parentID := strings.TrimSpace(snapshot.ParentExecutionID)
	if snapshot.Status != BGAgentCompleted || !isMessageSequenceItem || parentID == "" || registry.Get(sessionID, parentID) == nil {
		return false
	}
	child.SetMetadata(map[string]string{
		"suppress_auto_notification": "true",
		"notification_suppression":   "parent-owned-message-sequence-success",
	})
	return true
}

func (n *workshopExecutionBgNotifier) OnExecutionComplete(execID, name, result string, meta map[string]string, err error) {
	if n.api.autoNotificationSessionUnreachable(n.sessionID) {
		n.api.completeTrackedExecution(execID, trackedExecutionStatusCanceled, "session stopped", meta)
		log.Printf("[BG AGENT] OnExecutionComplete ignored for stopped session %s (exec=%s)", n.sessionID, execID)
		return
	}
	agent := n.api.bgAgentRegistry.Get(n.sessionID, execID)
	if agent == nil {
		return
	}

	// An agent already marked canceled was explicitly stopped by stop_step,
	// stop_all, or session shutdown. That path owns the terminal event and must not
	// be converted into a failure when the worker later unwinds with context.Canceled.
	if agent.GetStatus() == BGAgentCanceled {
		log.Printf("[BG AGENT] OnExecutionComplete skipped for already-canceled agent %s", execID)
		return
	}

	// Context-canceled / deadline-exceeded while the registry still says running
	// is a runtime timeout or unexpected parent-context loss, not an explicit user
	// cancellation. Treat it as failed and notify the waiting main agent. Marking
	// it canceled would make processBackgroundAgentCompletion deliberately suppress
	// the synthetic turn, which strands Pulse after a timed-out maintenance agent.
	if err != nil && (strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "context deadline exceeded")) {
		if !agent.SetError(err.Error()) {
			return
		}
		n.api.completeTrackedExecution(execID, trackedExecutionStatusFailed, err.Error(), meta)
		duration := time.Since(agent.CreatedAt)
		n.api.emitBackgroundAgentCompleted(n.sessionID, execID, name, "failed", "", err.Error(), duration.Truncate(time.Second).String())
		suppressNotification := agent.GetSnapshot().Metadata["suppress_auto_notification"] == "true"
		log.Printf("[BG AGENT] Background execution %s ended from context loss while still running (suppress_auto_notification=%t): %v", execID, suppressNotification, err)
		if !suppressNotification {
			n.api.bgAgentRegistry.NotifyCompletion(n.sessionID, execID)
		}
		return
	}

	duration := time.Since(agent.CreatedAt)
	if len(meta) > 0 {
		agent.SetMetadata(meta)
	}
	if err != nil {
		if !agent.SetError(err.Error()) {
			return
		}
		n.api.completeTrackedExecution(execID, trackedExecutionStatusFailed, err.Error(), meta)
		n.api.emitBackgroundAgentCompleted(n.sessionID, execID, name, "failed", "", err.Error(), duration.Truncate(time.Second).String())
	} else {
		if !agent.SetResult(result) {
			return
		} // A concurrent stop owns the terminal outcome.
		n.api.completeTrackedExecution(execID, trackedExecutionStatusCompleted, "", meta)
		displayResult := workshopCompletionDisplayResult(n.workspacePath, result, meta)
		n.api.emitBackgroundAgentCompleted(n.sessionID, execID, name, "completed", displayResult, "", duration.Truncate(time.Second).String())
	}

	if err == nil && suppressParentOwnedMessageSequenceSuccess(n.api.bgAgentRegistry, n.sessionID, agent) {
		log.Printf("[BG AGENT] Suppressed child success notification for %s; enclosing execution %s owns the completion", execID, agent.GetSnapshot().ParentExecutionID)
	}
	if err != nil && suppressRepeatedChildFailureNotification(n.api.bgAgentRegistry, n.sessionID, agent) {
		log.Printf("[BG AGENT] Suppressed repeated parent failure notification for %s; direct message-sequence child already owns the same error", execID)
	}

	// A finished parent cannot still have live progress children. Settle any it
	// left running, so an end event that never arrived cannot pin the session
	// busy forever and stall the scheduler's drain-wait (PLAT-091).
	if orphans := n.api.bgAgentRegistry.ReconcileOrphanedProgressChildren(
		n.sessionID, execID,
		fmt.Sprintf("parent execution %s finished without an end event for this step", execID),
	); len(orphans) > 0 {
		log.Printf("[BG AGENT] Settled %d orphaned progress child(ren) of finished execution %s in session %s: %v",
			len(orphans), execID, n.sessionID, orphans)
		// The registry cannot reach the durable log, so emit each settlement
		// here (PLAT-117). Without this, a settled orphan stays stored
		// status='running', completed_at=NULL in PLAT-114's background_agent_log
		// forever — social-media 2026-08-16 left two such rows, which read as
		// live work to anyone auditing that table later.
		reason := fmt.Sprintf("parent execution %s finished without an end event for this step", execID)
		for _, orphanID := range orphans {
			// ReconcileOrphanedProgressChildren owns the background-agent
			// registry, while Global Monitor reads the unified execution
			// tracker. Settle both copies of the same progress mirror. Leaving
			// the tracker running makes the authoritative runtime snapshot busy
			// even though the foreground turn, terminal, parent execution, and
			// durable schedule run have all finished (PLAT-117).
			n.api.completeTrackedExecution(orphanID, trackedExecutionStatusFailed, reason, nil)
			name := orphanID
			if orphan := n.api.bgAgentRegistry.Get(n.sessionID, orphanID); orphan != nil {
				name = orphan.GetSnapshot().Name
			}
			n.api.emitBackgroundAgentCompleted(n.sessionID, orphanID, name, "failed", "", reason, "")
		}
	}

	// Signal completion to the notification loop unless the parent is already
	// synchronously awaiting this execution's direct tool result.
	if agent.GetSnapshot().Metadata["suppress_auto_notification"] != "true" {
		n.api.bgAgentRegistry.NotifyCompletion(n.sessionID, execID)
	}
}

func workshopCompletionDisplayResult(workspacePath, result string, meta map[string]string) string {
	const defaultLimit = 500
	if strings.TrimSpace(meta["execution_type"]) != "pulse-reviewer" {
		return truncateForToolResponse(result, defaultLimit)
	}

	resultPath := strings.TrimSpace(meta["review_result_path"])
	workspacePath = strings.TrimSpace(workspacePath)
	if resultPath == "" || workspacePath == "" || filepath.IsAbs(resultPath) {
		return truncateForToolResponse(result, defaultLimit)
	}
	root, err := filepath.Abs(workspacePath)
	if err != nil {
		return truncateForToolResponse(result, defaultLimit)
	}
	candidate, err := filepath.Abs(filepath.Join(root, filepath.Clean(resultPath)))
	if err != nil {
		return truncateForToolResponse(result, defaultLimit)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return truncateForToolResponse(result, defaultLimit)
	}
	content, err := os.ReadFile(candidate)
	if err != nil {
		return truncateForToolResponse(result, defaultLimit)
	}

	findings := strings.TrimSpace(string(content))
	if marker := "\n## Findings\n"; strings.Contains(findings, marker) {
		findings = strings.TrimSpace(strings.SplitN(findings, marker, 2)[1])
	}
	if findings == "" {
		return truncateForToolResponse(result, defaultLimit)
	}
	// Keep the complete review in SQLite. Only this immediate UI preview is
	// bounded (and explicitly marked as truncated); this is not a finding or
	// storage cap.
	return truncateForToolResponse(findings, 6000)
}

func (n *workshopExecutionBgNotifier) OnExecutionTerminated(execID, name string) {
	if n.api.autoNotificationSessionUnreachable(n.sessionID) {
		n.api.completeTrackedExecution(execID, trackedExecutionStatusCanceled, "session stopped", nil)
		return
	}
	for _, agent := range n.api.bgAgentRegistry.CancelExecutionTree(n.sessionID, execID) {
		snapshot := agent.GetSnapshot()
		n.api.completeTrackedExecution(snapshot.ID, trackedExecutionStatusCanceled, "execution terminated", nil)
		if agent.MarkTerminalNotified() {
			n.api.emitBackgroundAgentTerminated(n.sessionID, snapshot.ID, snapshot.Name, "")
		}
	}

}

// workflowSubAgentTrackingNotifier tracks inner workshop sub-agents in the backend
// execution tree and triggers synthetic-turn notifications only when they finish.
type workflowSubAgentTrackingNotifier struct {
	api       *StreamingAPI
	sessionID string
}

func (n *workflowSubAgentTrackingNotifier) OnSubAgentStart(start todo_creation_human.WorkshopExecutionStart) {
	if n == nil || n.api == nil || strings.TrimSpace(start.ID) == "" {
		return
	}
	if n.api.autoNotificationSessionUnreachable(n.sessionID) {
		if start.Cancel != nil {
			start.Cancel()
		}
		return
	}
	kind := strings.TrimSpace(start.Kind)
	if kind == "" {
		kind = "workflow_sub_agent"
	}
	bgAgent := &BackgroundAgent{
		ID:                start.ID,
		ParentExecutionID: start.ParentExecutionID,
		Name:              start.Name,
		SessionID:         n.sessionID,
		Kind:              kind,
		Status:            BGAgentRunning,
		CreatedAt:         time.Now(),
		Metadata:          start.Metadata,
		cancel:            start.Cancel,
	}
	n.api.bgAgentRegistry.Register(n.sessionID, bgAgent)
	suppressAutoNotification := start.Metadata["suppress_auto_notification"] == "true"
	if !suppressAutoNotification {
		// Pre-create the completion channel and loop so a fast sub-agent completion
		// cannot drop its auto-notification. Parent-reconciled children skip this
		// path because their result returns to the owning orchestrator conversation.
		n.api.bgAgentRegistry.GetNotificationChannel(n.sessionID)
		n.api.completionLoopStartedMu.Lock()
		if n.api.completionLoopStarted == nil {
			n.api.completionLoopStarted = make(map[string]bool)
		}
		if !n.api.completionLoopStarted[n.sessionID] {
			n.api.completionLoopStarted[n.sessionID] = true
			go n.api.backgroundCompletionLoop(n.sessionID)
		}
		n.api.completionLoopStartedMu.Unlock()
	}

	n.api.emitBackgroundAgentStarted(n.sessionID, start.ID, start.Name, "", start.ParentExecutionID, orchEvents.ParseExecutionKind(kind))
	if !suppressAutoNotification {
		n.api.notifyBackgroundAgentStarted(n.sessionID, start.ID)
	}
}

func (n *workflowSubAgentTrackingNotifier) OnSubAgentComplete(agentID, name string, result string, err error) {
	if n == nil || n.api == nil || strings.TrimSpace(agentID) == "" {
		return
	}
	agent := n.api.bgAgentRegistry.Get(n.sessionID, agentID)
	if agent == nil {
		return
	}
	if agent.GetStatus() == BGAgentCanceled {
		return
	}
	suppressAutoNotification := agent.GetSnapshot().Metadata["suppress_auto_notification"] == "true"
	if err != nil {
		if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "context deadline exceeded") {
			agent.SetCanceled()
			// Emit a terminal event for context-canceled sub-agents so the
			// completion loop has a consistent signal — mirrors OnExecutionComplete
			// (finding-onsubagentcomplete-context-cancel-silent-drop fix).
			if !n.api.isSessionMarkedStopped(n.sessionID) {
				if agent.MarkTerminalNotified() {
					n.api.emitBackgroundAgentTerminated(n.sessionID, agentID, name, "canceled")
					if !suppressAutoNotification {
						n.api.bgAgentRegistry.NotifyCompletion(n.sessionID, agentID)
					}
				}
			}
			return
		}
		agent.SetError(err.Error())
		if agent.GetStatus() == BGAgentCanceled {
			return
		}
		duration := time.Since(agent.CreatedAt)
		n.api.emitBackgroundAgentCompleted(n.sessionID, agentID, name, "failed", "", err.Error(), duration.Truncate(time.Second).String())
		if !suppressAutoNotification {
			n.api.bgAgentRegistry.NotifyCompletion(n.sessionID, agentID)
		}
		return
	}
	agent.SetResult(result)
	if agent.GetStatus() == BGAgentCanceled {
		return
	}
	duration := time.Since(agent.CreatedAt)
	n.api.emitBackgroundAgentCompleted(n.sessionID, agentID, name, "completed", truncateForToolResponse(result, 500), "", duration.Truncate(time.Second).String())
	if !suppressAutoNotification {
		n.api.bgAgentRegistry.NotifyCompletion(n.sessionID, agentID)
	}
}

// truncateForToolResponse truncates a string for inclusion in tool responses
func truncateForToolResponse(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... (truncated)"
}

// executeDelegatedTask executes a delegated task via a sub-agent.
// onCreated is an optional callback invoked after the sub-agent wrapper is created
// but before Invoke — used by background agents to attach a history func.
func (api *StreamingAPI) executeDelegatedTask(ctx context.Context, parentReq QueryRequest, sessionID string, instruction string, onCreated ...func(wrapper *agent.LLMAgentWrapper)) (string, error) {
	log.Printf("[DELEGATION] Creating sub-agent for delegated task in session %s", sessionID)

	// The full delegation contract (depth, tier, template, servers, skills,
	// browser sharing, background link) arrives as one typed spec.
	spec := virtualtools.SubAgentSpecFromContext(ctx)
	currentDepth := spec.Depth

	if currentDepth >= virtualtools.MaxDelegationDepth {
		return "", fmt.Errorf("maximum delegation depth (%d) reached", virtualtools.MaxDelegationDepth)
	}

	// Generate a unique delegation ID for tracking
	delegationID := fmt.Sprintf("delegation-%d-%d", currentDepth, time.Now().UnixNano())

	// Build sub-agent config from parent request
	// Get provider and model from parent request
	provider := llm.Provider(parentReq.Provider)
	modelID := parentReq.ModelID
	if provider == "" {
		defaultProvider, defaultModelID := defaultPublishedLLMProviderAndModel()
		provider = llm.Provider(defaultProvider)
		if modelID == "" {
			modelID = defaultModelID
		}
	}
	if modelID == "" {
		modelID = llm.GetDefaultModel(provider)
	}

	// Load sub-agent template if specified
	var loadedTemplate *subagents.SubAgent
	agentTemplateName := spec.AgentTemplate
	if agentTemplateName != "" {
		workspaceAPIURL := getWorkspaceAPIURL()
		sa, err := subagents.GetSubAgent(workspaceAPIURL, agentTemplateName)
		if err != nil {
			log.Printf("[DELEGATION] Warning: Failed to load sub-agent template %s: %v", agentTemplateName, err)
		} else {
			loadedTemplate = sa
			log.Printf("[DELEGATION] Loaded sub-agent template: %s (%s)", sa.Frontmatter.Name, agentTemplateName)
		}
	}

	// Resolve reasoning level tier to specific provider/model if configured
	reasoningLevel := spec.ReasoningLevel
	// Apply template defaults if not explicitly set
	if reasoningLevel == "" && loadedTemplate != nil && loadedTemplate.Frontmatter.DefaultReasoningLevel != "" {
		reasoningLevel = loadedTemplate.Frontmatter.DefaultReasoningLevel
		log.Printf("[DELEGATION] Using template default reasoning_level: %s", reasoningLevel)
	}
	connectionID := parentReq.ConnectionID
	if parentReq.LLMConfig != nil {
		connectionID = parentReq.LLMConfig.Primary.ConnectionID
	}
	if string(provider) != parentReq.Provider {
		connectionID = ""
	}
	var tierOptions map[string]interface{}
	if reasoningLevel != "" {
		// Load fresh from workspace file at delegation time so LLM-written tier changes take effect immediately
		tierConfig := LoadAndResolveTierConfig(ctx, parentReq.DelegationTierConfig)
		if tierConfig != nil {
			var tierModel *virtualtools.TierModel
			switch reasoningLevel {
			case "high":
				tierModel = tierConfig.High
			case "medium":
				tierModel = tierConfig.Medium
			case "low":
				tierModel = tierConfig.Low
			default:
				// Custom tier lookup
				if tierConfig.Custom != nil {
					if ct, ok := tierConfig.Custom[reasoningLevel]; ok {
						tierModel = &virtualtools.TierModel{Provider: ct.Provider, ModelID: ct.ModelID, ConnectionID: ct.ConnectionID}
					}
				}
			}
			if tierModel != nil && tierModel.Provider != "" && tierModel.ModelID != "" {
				provider = llm.Provider(tierModel.Provider)
				modelID = tierModel.ModelID
				tierOptions = tierModel.Options
				if tierModel.ConnectionID != "" || string(provider) != parentReq.Provider {
					connectionID = tierModel.ConnectionID
				}
				log.Printf("[DELEGATION] Using tier %s model: %s/%s", reasoningLevel, tierModel.Provider, tierModel.ModelID)
			}
		}
	}

	// Build server name — use delegation-specific servers if provided, otherwise all parent servers
	var serverName string
	var serversList []string
	if len(spec.Servers) > 0 {
		serverName = strings.Join(spec.Servers, ",")
		serversList = spec.Servers
		log.Printf("[DELEGATION] Using sub-agent specific servers: %s", serverName)
	} else if len(parentReq.EnabledServers) > 0 {
		serverName = strings.Join(parentReq.EnabledServers, ",")
		serversList = parentReq.EnabledServers
	} else if len(parentReq.Servers) > 0 {
		serverName = strings.Join(parentReq.Servers, ",")
		serversList = parentReq.Servers
	}

	// Sub-agents always run in code_execution mode (Python harness calling MCP tools via HTTP API).
	useCodeExec := true

	// Extract background agent ID if this delegation was spawned by a background agent
	backgroundAgentID := spec.BackgroundAgentID

	// Emit delegation_start event (after model and server resolution so we can include all info)
	api.emitDelegationStartEvent(sessionID, delegationID, currentDepth, instruction, reasoningLevel, modelID, serversList, backgroundAgentID, agentTemplateName)

	// Get user ID from context for per-user OAuth token isolation
	subAgentUserID := strings.TrimSpace(parentReq.userID)
	if userID, ok := ctx.Value(common.UserIDKey).(string); ok {
		subAgentUserID = userID
	}
	log.Printf("[USER_ID_DEBUGGING] Sub-agent: subAgentUserID=%q (from parent context UserIDKey)", subAgentUserID)
	projectScopedProfile := strings.TrimSpace(parentReq.AgentProfileID) != "" && strings.TrimSpace(parentReq.SelectedFolder) != ""
	subAgentWorkspace := perUserChatsFolderFor(subAgentUserID)
	if projectScopedProfile {
		subAgentWorkspace = agentProfileRuntimeWorkspace(subAgentUserID, parentReq.SelectedFolder)
	}

	// Load provider keys and add the private workflow credential only for
	// workflow-owned delegations. Normal multi-agent chats must not inherit a
	// credential merely because they reference a workflow folder.
	apiKeys := api.withConnectionResolver(MergedProviderAPIKeys(ctx), subAgentUserID)
	workflowOwnedDelegation := parentReq.AgentMode == "workflow" || parentReq.AgentMode == "workflow_phase" || strings.TrimSpace(parentReq.PhaseID) != ""
	workflowDecisionScope := strings.TrimSpace(parentReq.SelectedFolder)
	if workflowOwnedDelegation {
		if workflowDecisionScope == "" && parentReq.PresetQueryID != "" {
			if resolved, resolveErr := api.resolveWorkspacePathFromPreset(context.Background(), parentReq.PresetQueryID); resolveErr == nil {
				workflowDecisionScope = resolved
			}
		}
		var credentialErr error
		apiKeys, credentialErr = api.resolveEffectiveAPIKeys(ctx, subAgentUserID, workflowDecisionScope, apiKeys)
		if credentialErr != nil {
			return "", fmt.Errorf("load delegated workflow provider credentials: %w", credentialErr)
		}
	}

	// Determine sub-agent session ID: isolated when share_browser=false, shared otherwise
	subAgentSessionID := fmt.Sprintf("%s-isolated-%d", sessionID, time.Now().UnixNano())
	log.Printf("[DELEGATION] Browser isolation: sub-agent gets new session ID %s (parent: %s)", subAgentSessionID, sessionID)
	runtimeID := delegationID
	if backgroundAgentID != "" {
		runtimeID = backgroundAgentID
	}
	subAgentRuntimeFolder := delegatedCodingAgentRuntimeFolder(subAgentUserID, runtimeID)
	subAgentRuntimeDir := codingAgentWorkspaceWorkingDir(subAgentRuntimeFolder)
	if err := os.MkdirAll(subAgentRuntimeDir, 0o755); err != nil {
		api.emitDelegationEndEvent(sessionID, delegationID, currentDepth, "", err.Error(), nil)
		return "", fmt.Errorf("failed to create sub-agent runtime directory: %w", err)
	}
	log.Printf("[DELEGATION] Sub-agent coding-agent runtime cwd: %s", subAgentRuntimeDir)

	// Create sub-agent config based on parent request
	subAgentConfig := agent.LLMAgentConfig{
		Name:         fmt.Sprintf("sub-agent-depth-%d", currentDepth),
		ServerName:   serverName,
		ConfigPath:   api.mcpConfigPath,
		Provider:     provider,
		ConnectionID: connectionID,
		ModelID:      modelID,
		Options:      tierOptions,
		Temperature: func() float64 {
			if parentReq.Temperature > 0 {
				return parentReq.Temperature
			}
			return 0.7
		}(),
		MaxTurns: func() int {
			if parentReq.MaxTurns != 0 {
				return parentReq.MaxTurns
			}
			return 500
		}(),
		ToolChoice:         "", // Empty — let the library decide; Azure/OpenAI reject tool_choice when no tools are present
		StreamingChunkSize: 1,
		// No Timeout set — sub-agent lifetime is controlled by the parent context.
		// Sub-agent mode uses the resolved values (from delegate call, template default, or auto-enable).
		UseCodeExecutionMode: useCodeExec,
		APIKeys:              apiKeys,
		SessionID:            subAgentSessionID, // Reuse parent session's MCP connections via registry, unless browser isolation requested
		// MCP credentials are platform-wide. Keep this empty so delegated agents
		// preserve the shared token path from the merged MCP configuration.
		UserID:                "",
		CodingAgentWorkingDir: subAgentRuntimeDir,
		// A delegated sub-agent is never steered mid-turn by a human — the same
		// rationale step_based_workflow's applyWorkflowTransportToAgentConfig
		// uses to force structured JSON transport for workflow steps applies
		// here too. Without this, CLI-provider sub-agents (cursor-cli, etc.)
		// default to raw tmux capture: no clean per-turn events, so the rail's
		// transcript falls back to parsing ANSI text instead of rendering the
		// same event cards every other execution kind gets.
		ForceStructuredCodingAgent: common.IsCLIProvider(string(provider)),
	}
	delegatedAccess, accessErr := api.conversationTargetAccess(ctx, parentReq)
	if accessErr != nil {
		api.emitDelegationEndEvent(sessionID, delegationID, currentDepth, "", accessErr.Error(), nil)
		return "", fmt.Errorf("resolve delegated agent execution access: %w", accessErr)
	}
	subAgentConfig.ToolExecutionContext = api.bindToolExecutionContextForSession(
		ctx,
		sessionID,
		subAgentSessionID,
		parentReq,
		delegatedAccess == WorkflowAccessRead,
	)
	// Tool timeout, context summarization/editing, large-output offloading, and
	// parallel tool execution inherit from the parent request the same way the
	// root chat agent resolves them (no preset at delegation time).
	applySharedLLMAgentTuning(&subAgentConfig, &parentReq, nil)

	// A child must not exceed the tool surface its product declared, or
	// delegating becomes a way around tool_policy. The profile is resolved from
	// the same parent request rather than threaded down, so parent and child
	// derive their surface from one source instead of two — the divergence that
	// let a background child come up short of its parent in the first place.
	// Look up, do not re-enter: the gate below reads only Definition.ID and
	// Definition.ToolPolicy. resolveAgentProfileForQuery would re-run the
	// product's runtime initializer (workspace seeding, plan refresh, workflow
	// DB init, productdeps.Ensure) on every single delegation, and its request
	// mutations are discarded here anyway because parentReq is a copy and
	// subAgentConfig is already built.
	if subProfile, profileErr := api.lookupAgentProfileDefinition(ctx, &parentReq, subAgentUserID); profileErr != nil {
		// Failing open here defeated the rule stated directly above: with no
		// AdmitTool set, the child came up with a WIDER surface than the
		// product declared, so a failed profile resolution turned delegation
		// into the tool_policy bypass this gate exists to prevent. Only abort
		// when the parent actually declared a profile -- an inconsistent
		// request with no profile id has no product surface to enforce, and
		// there the old log-and-continue is still the right behavior.
		if strings.TrimSpace(parentReq.AgentProfileID) != "" {
			log.Printf("[DELEGATION] Refusing to delegate: could not resolve the parent agent profile %q for the sub-agent tool surface: %v", parentReq.AgentProfileID, profileErr)
			api.emitDelegationEndEvent(sessionID, delegationID, currentDepth, "", profileErr.Error(), nil)
			return "", fmt.Errorf("failed to resolve the agent profile tool surface for delegation: %w", profileErr)
		}
		log.Printf("[DELEGATION] Could not resolve the parent agent profile for the sub-agent tool surface: %v", profileErr)
	} else if gate := newProductToolGate(subProfile); gate.enforcing() {
		subAgentConfig.AdmitTool = gate.Admit
		defer gate.logSurface(sessionID)
	}

	// Create sub-agent using the wrapper (same as parent agent creation)
	subAgent, err := agent.NewLLMAgentWrapper(ctx, subAgentConfig, nil, api.logger)
	if err != nil {
		api.emitDelegationEndEvent(sessionID, delegationID, currentDepth, "", err.Error(), nil)
		return "", fmt.Errorf("failed to create sub-agent: %w", err)
	}

	// Resolve conditional folder-guard grants for the sub-agent once.
	// Used by both nested scopes below (prompt assembly + workspace tool folder guard).
	subResolvedGrants := resolveConditionalGrants(parentReq)
	// Preserve configured browser intent for the child. In auto mode the child
	// queries agent_browser status and each action rechecks CDP live.
	browserReq := parentReq
	if mode := getBrowserMode(browserReq); mode == "none" {
		if configured := strings.ToLower(strings.TrimSpace(common.GetSessionBrowserMode(sessionID))); configured != "" {
			browserReq.BrowserMode = configured
		}
	}
	subBrowserCfg := buildChatBrowserConfig(browserReq)

	// Add event observers to sub-agent so its events appear in the UI and
	// its token usage lands in the global cost ledger.
	if underlyingAgent := subAgent.GetUnderlyingAgent(); underlyingAgent != nil {
		subAgentObserver := events.NewDelegationEventObserver(api.eventStore, sessionID, currentDepth, delegationID, api.logger)
		if toolCb, ok := ctx.Value(virtualtools.ToolEventCallbackKey).(events.ToolEventCallback); ok && toolCb != nil {
			subAgentObserver.OnToolEvent = toolCb
		}
		if err := subAgent.AddObserver(subAgentObserver); err != nil {
			return "", fmt.Errorf("attach delegation event observer: %w", err)
		}
		parentUserID, _ := ctx.Value(common.UserIDKey).(string)
		costWorkspace := parentReq.SelectedFolder
		if projectScopedProfile {
			costWorkspace = subAgentWorkspace
		}
		subAgentCostObserver := newCostObserver(
			api.costLedger,
			sessionID,
			parentUserID,
			parentReq.AgentMode,
			withCostModel(string(provider), modelID),
			withCostAttribution(
				inferCostScope(parentReq.AgentMode, parentReq.PhaseID),
				costWorkspace,
				"",
				delegationID,
			),
			withCostSourcePlatform(parentReq.BotPlatform),
		)
		if err := subAgent.AddObserver(subAgentCostObserver); err != nil {
			return "", fmt.Errorf("attach delegation cost observer: %w", err)
		}
		log.Printf("[DELEGATION] Added event observers for sub-agent at depth %d", currentDepth)

		// Chief of Staff delegation inherits the root agent's exact attached
		// skill bundles. This includes the multi-agent reference surface and
		// user-selected skills, with supporting files intact for projection
		// into the child's isolated coding-agent runtime directory.
		inheritedSkills := delegatedParentSkillsFromContext(ctx)
		identitySkills := append([]*llmtypes.Skill(nil), inheritedSkills...)
		// skills=[...] remains an additive override for skills that are not
		// already attached to the Chief of Staff parent. Attach the combined
		// set once so assembly-time identity also deduplicates correctly before
		// the wrapper finalizes its immutable mcpagent definition.
		if len(spec.Skills) > 0 {
			// Sub-agents inherit the parent's workspace, so resolve skills there
			// first — otherwise a delegated agent silently loses every skill the
			// product installs into its project.
			if attached := skills.LoadAttachableIn(getWorkspaceAPIURL(), parentReq.SelectedFolder, spec.Skills); len(attached) > 0 {
				identitySkills = append(identitySkills, attached...)
			}
		}
		attachedCount := 0
		for _, skill := range uniqueDelegatedSkills(nil, identitySkills) {
			if err := subAgent.AttachSkill(skill); err != nil {
				return "", fmt.Errorf("attach delegated agent skill %q: %w", skill.Name, err)
			}
			attachedCount++
		}
		log.Printf("[DELEGATION] Attached %d skill(s) to sub-agent (parent=%d, explicit_names=%d)", attachedCount, len(inheritedSkills), len(spec.Skills))

		// Append prompt sections contributed by active conditional grants
		// (resolved above in subResolvedGrants before this block).
		for _, section := range subResolvedGrants.PromptSections {
			_ = subAgent.AddInstructions(section)
		}
		if len(subResolvedGrants.PromptSections) > 0 {
			log.Printf("[DELEGATION] Appended %d grant prompt section(s) to sub-agent: %v", len(subResolvedGrants.PromptSections), subResolvedGrants.AppliedNames)
		}

		// Inject sub-agent template instructions into system prompt
		if loadedTemplate != nil {
			templatePrompt := fmt.Sprintf("\n## Sub-Agent Role: %s\n\n%s\n",
				loadedTemplate.Frontmatter.Name, loadedTemplate.Content)
			_ = subAgent.AddInstructions(templatePrompt)
			log.Printf("[DELEGATION] Injected sub-agent template instructions: %s", loadedTemplate.Frontmatter.Name)
		}

		// Merge global secrets with parent's decrypted secrets — inject names into prompt (values are in env vars)
		allDelegationSecrets := mergeGlobalSecrets(parentReq.DecryptedSecrets, parentReq.SelectedGlobalSecrets)
		if len(allDelegationSecrets) > 0 {
			_ = subAgent.AddInstructions(buildSecretNamesPrompt(allDelegationSecrets))
			log.Printf("[DELEGATION] Injected %d secret names (not values) into sub-agent system prompt", len(allDelegationSecrets))
		}

		// Give sub-agents the workspace folder structure so they know where to
		// read/write files. Sub-agents are actual file workers that need this orientation.
		// Project products inherit the exact project workspace; ordinary AgentWorks
		// delegation keeps the account Chats root.
		subAgentChatsFolder := subAgentWorkspace
		subShellRoot := fsutil.WorkspaceShellRoot()
		_ = subAgent.AddInstructions(GetWorkspaceMap(subShellRoot, subAgentChatsFolder))
		_ = subAgent.AddInstructions(GetWorkspaceReference(subShellRoot, subAgentChatsFolder))
		if projectScopedProfile {
			_ = subAgent.AddInstructions(governedProjectMemoryInstructions)
		}
		log.Printf("[DELEGATION] Added workspace instructions to sub-agent (chats=%s)", subAgentChatsFolder)

		// [BROWSER] Add browser instructions using standardized builder (same as parent chat agent).
		// Sub-agents need their own transformer registration because each Agent instance has
		// its own toolArgTransformers map — the parent's transformer doesn't propagate.
		if subBrowserPrompt := browserinstructions.BuildBrowserInstructions(subBrowserCfg); subBrowserPrompt != "" {
			_ = subAgent.AddInstructions(subBrowserPrompt)
			log.Printf("[BROWSER] Added agent-browser instructions to sub-agent (configured_mode=%s candidate_cdp_ports=%v)", subBrowserCfg.Mode, subBrowserCfg.CdpPorts)
		}

		// Browser isolation: when share_browser=false, tell the sub-agent to use a unique
		// session name with the agent_browser tool to avoid sharing browser state.
		_ = subAgent.AddInstructions(fmt.Sprintf("## Browser Isolation\nYou have an isolated browser session. When using the agent_browser tool, use a unique session name (e.g., \"isolated-%d\") instead of \"default\" to avoid sharing browser state with other agents.", time.Now().UnixNano()))
		log.Printf("[DELEGATION] Added browser isolation guidance to sub-agent system prompt")
	}

	// Register workspace tools for sub-agent
	if underlyingAgent := subAgent.GetUnderlyingAgent(); underlyingAgent != nil {
		// Sub-agents get the normal LLM-visible workspace tool set (advanced + media/provider tools).
		workspaceRegistry := virtualtools.CreateWorkspaceToolRegistry(virtualtools.WorkspaceToolRegistryConfig{
			WorkspaceAPIURL: getWorkspaceAPIURL(),
			UserID:          subAgentUserID,
			SessionID:       sessionID,
		})
		workspaceTools := workspaceRegistry.Tools
		workspaceExecutors := workspaceRegistry.Executors
		subAgentEnv := workspaceRegistry.Env
		toolCategories := workspaceRegistry.Categories
		log.Printf("[USER_ID_DEBUGGING] Sub-agent workspace executors: created with explicit userID=%q sessionID=%q", subAgentUserID, sessionID)
		// Inject secrets as environment variables for sub-agent shell execution (SECRET_ prefix for whitelist)
		delegationSecrets := mergeGlobalSecrets(parentReq.DecryptedSecrets, parentReq.SelectedGlobalSecrets)
		if subAgentEnv != nil && len(delegationSecrets) > 0 {
			for _, s := range delegationSecrets {
				subAgentEnv["SECRET_"+s.Name] = s.Value
			}
			log.Printf("[SECRETS] Injected %d secrets as env vars for sub-agent shell execution", len(delegationSecrets))
		}
		// Conditional grants already resolved above into subResolvedGrants.
		// Merge parent @context paths and #workflow references into delegated folder-guard access.
		// @context paths get write access; #workflow paths get read-only access.
		contextReadPaths := parentReq.authorizedWorkflowContextReadPaths
		if len(contextReadPaths) == 0 {
			contextReadPaths = parentReq.WorkflowContextPaths
		}
		fileContextWriteFolders, workflowReadOnlyFolders := collectSplitFolderGuardFolders(parentReq.Query, contextReadPaths)
		if len(fileContextWriteFolders) > 0 {
			log.Printf("[DELEGATION] Extracted write folder-guard paths from parent @context: %v", fileContextWriteFolders)
		}
		if len(workflowReadOnlyFolders) > 0 {
			log.Printf("[DELEGATION] Extracted read-only folder-guard paths from parent #workflow: %v", workflowReadOnlyFolders)
		}
		// A project product inherits the root chat's exact guard, including attached
		// Work folders. Ordinary AgentWorks delegation keeps its established Chats
		// scope and conditional grants.
		if projectScopedProfile {
			parentGuard := common.GetSessionShellConfig(sessionID)
			readPaths := []string{subAgentWorkspace + "/"}
			writePaths := []string{subAgentWorkspace + "/"}
			if parentGuard != nil {
				readPaths = append([]string(nil), parentGuard.ReadPaths...)
				writePaths = append([]string(nil), parentGuard.WritePaths...)
			}
			workspaceExecutors = wrapExecutorsWithPlanFolderGuard(workspaceExecutors, subAgentWorkspace, readPaths, writePaths...)
			workspace.SetSessionWorkingDir(subAgentSessionID, subAgentWorkspace)
			workspace.SetSessionFolderGuard(subAgentSessionID, readPaths, writePaths)
			if parentGuard != nil {
				workspace.SetSessionFolderGuardBlockedPaths(subAgentSessionID, parentGuard.BlockedPaths)
				workspace.SetSessionFolderGuardBlockedWritePaths(subAgentSessionID, parentGuard.BlockedWritePaths)
				common.SetSessionSandbox(subAgentSessionID, parentGuard.StrictAllowlist, parentGuard.DenyNetwork)
				common.SetSessionShellEnv(subAgentSessionID, parentGuard.Env)
			}
			log.Printf("[DELEGATION FOLDER GUARD] Inherited project restriction (workspace=%s read=%v write=%v)", subAgentWorkspace, readPaths, writePaths)
		} else {
			subPerUserChatsFolder := perUserChatsFolderFor(subAgentUserID)
			subPerUserChatsWrite := subPerUserChatsFolder + "/"
			subPerUserChatHistory := strings.TrimSuffix(subPerUserChatsFolder, "Chats") + "chat_history/"
			orgPulseWrite := "pulse/"
			extraFolders := append([]string{}, subResolvedGrants.WriteFolders...)
			extraFolders = append(extraFolders, fileContextWriteFolders...)
			extraFolders = append(extraFolders, subPerUserChatHistory)
			extraFolders = append(extraFolders, orgPulseWrite)
			// Delegation path has no #workflow-derived blocked-write prefix (the parent
			// session's blocked paths aren't inherited here; this call path is for sub-agents
			// spawned with their own folder scope). Pass nil.
			workspaceExecutors = wrapExecutorsWithChatModeFolderGuard(workspaceExecutors, workflowReadOnlyFolders, nil, extraFolders...)
			workspace.SetSessionWorkingDir(sessionID, subPerUserChatsFolder)
			readPaths := append([]string{subPerUserChatsWrite, subPerUserChatHistory, "Downloads/", "skills/", "subagents/", "Workflow/"}, extraFolders...)
			readPaths = append(readPaths, subResolvedGrants.ReadOnlyExtra...)
			readPaths = append(readPaths, workflowReadOnlyFolders...)
			workspace.SetSessionFolderGuard(sessionID,
				readPaths,
				append([]string{subPerUserChatsWrite, "Downloads/", subPerUserChatHistory}, extraFolders...),
			)
			if hostDownloads := common.GrantSessionCDPHostDownloadsReadOnly(sessionID, browserReq.BrowserMode); hostDownloads != "" {
				log.Printf("[DELEGATION FOLDER GUARD] Added read-only CDP host Downloads for sub-agent: %s", hostDownloads)
			}
		}

		// Register workspace tools
		for _, tool := range workspaceTools {
			if tool.Function == nil {
				continue
			}
			toolName := tool.Function.Name
			if executor, exists := workspaceExecutors[toolName]; exists {
				var params map[string]interface{}
				if tool.Function.Parameters != nil {
					paramsBytes, err := json.Marshal(tool.Function.Parameters)
					if err == nil {
						json.Unmarshal(paramsBytes, &params)
					}
				}
				if params == nil {
					continue
				}

				toolCategory := toolCategories[toolName]
				if toolCategory == "" {
					continue
				}

				if err := subAgent.RegisterCustomTool(
					toolName,
					tool.Function.Description,
					params,
					executor,
					toolCategory,
				); err != nil {
					log.Printf("[DELEGATION] Warning: Failed to register tool %s for sub-agent: %v", toolName, err)
				}
			}
		}

		// Register browser tools if enabled
		if subBrowserCfg.HasAgentBrowser {
			browserTools := virtualtools.CreateWorkspaceBrowserTools()
			browserRuntime := browser.NewBrowserRuntimeConfig(subBrowserCfg.Mode, subBrowserCfg.CdpPorts)
			browserExecutors := virtualtools.CreateWorkspaceBrowserToolExecutorsWithRuntime(sessionID, browserRuntime)
			browserCategory := virtualtools.GetWorkspaceBrowserToolCategory()

			browserExtraFolders := append([]string{}, subResolvedGrants.WriteFolders...)
			browserExtraFolders = append(browserExtraFolders, fileContextWriteFolders...)
			if projectScopedProfile {
				parentGuard := common.GetSessionShellConfig(sessionID)
				readPaths := []string{subAgentWorkspace + "/"}
				writePaths := []string{subAgentWorkspace + "/"}
				if parentGuard != nil {
					readPaths = append([]string(nil), parentGuard.ReadPaths...)
					writePaths = append([]string(nil), parentGuard.WritePaths...)
				}
				browserExecutors = wrapExecutorsWithPlanFolderGuard(browserExecutors, subAgentWorkspace, readPaths, writePaths...)
			} else {
				browserExecutors = wrapExecutorsWithChatModeFolderGuard(browserExecutors, workflowReadOnlyFolders, nil, browserExtraFolders...)
			}

			for _, tool := range browserTools {
				if tool.Function == nil {
					continue
				}
				toolName := tool.Function.Name
				if executor, exists := browserExecutors[toolName]; exists {
					var params map[string]interface{}
					if tool.Function.Parameters != nil {
						paramsBytes, err := json.Marshal(tool.Function.Parameters)
						if err == nil {
							json.Unmarshal(paramsBytes, &params)
						}
					}
					if params == nil {
						continue
					}

					if err := subAgent.RegisterCustomTool(
						toolName,
						tool.Function.Description,
						params,
						executor,
						browserCategory,
					); err != nil {
						log.Printf("[DELEGATION] Warning: Failed to register browser tool %s for sub-agent: %v", toolName, err)
					}
				}
			}
		}

		// NOTE: Sub-agents do NOT get the delegate tool themselves (v1 design choice)
		// This prevents runaway delegation chains.

		// Minimal worker context — tells the sub-agent its role and output conventions.
		subWorkerChatsFolder := subAgentWorkspace
		_ = subAgent.AddInstructions(fmt.Sprintf(`## Your Role
You are a focused background worker. Complete the assigned task using available tools and return a clear, concise result.
- You cannot spawn further sub-agents
- You do not share hidden context with the caller — all context is in the instruction you received
- Save any output files under %s/ (use the sub-folder specified in your instruction, or create a descriptive one if none is given)
- Return a summary of what you did and what you found`, subWorkerChatsFolder))
		log.Printf("[DELEGATION] Added worker context to sub-agent (chats=%s)", subWorkerChatsFolder)
	}

	log.Printf("[DELEGATION] Sub-agent created, executing instruction at depth %d", currentDepth)

	// Notify caller that the sub-agent wrapper is ready (used by background agents)
	if len(onCreated) > 0 && onCreated[0] != nil {
		onCreated[0](subAgent)
	}

	// Clean up isolated browser session when sub-agent finishes
	if subAgentSessionID != sessionID {
		defer func() {
			mcpagent.CloseSession(subAgentSessionID)
			common.ClearSessionShellConfig(subAgentSessionID)
			log.Printf("[DELEGATION] Closed isolated browser session %s", subAgentSessionID)
		}()
	}

	// Run the sub-agent with the instruction
	startTime := time.Now()
	result, err := subAgent.Invoke(ctx, instruction)
	duration := time.Since(startTime)

	// Collect metrics from sub-agent
	metrics := subAgent.GetMetricsSnapshot()
	stats := &delegationEndStats{
		InputTokens:  metrics.InputTokens,
		OutputTokens: metrics.OutputTokens,
		ToolCalls:    metrics.ToolCallsExecuted,
		Duration:     duration.String(),
		TotalCostUSD: metrics.TotalCostUSD,
	}

	if err != nil {
		api.emitDelegationEndEvent(sessionID, delegationID, currentDepth, "", err.Error(), stats)
		return "", fmt.Errorf("sub-agent execution failed: %w", err)
	}

	// Emit delegation_end event with success
	api.emitDelegationEndEvent(sessionID, delegationID, currentDepth, fmt.Sprintf("Completed in %s", duration), "", stats)

	log.Printf("[DELEGATION] Sub-agent completed at depth %d in %s", currentDepth, duration)
	return result, nil
}

// --- Background Agent Infrastructure for Async Delegation ---

// bgAgentQuerierImpl implements virtualtools.BGAgentQuerier using the registry
type bgAgentQuerierImpl struct {
	registry *BackgroundAgentRegistry
}

func (q *bgAgentQuerierImpl) QueryAgent(sessionID, agentID string, last, offset int) (*virtualtools.BGAgentInfo, error) {
	agent := q.registry.Get(sessionID, agentID)
	if agent == nil {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}
	snap := agent.GetSnapshot()
	elapsed := time.Since(snap.CreatedAt)
	if snap.CompletedAt != nil {
		elapsed = snap.CompletedAt.Sub(snap.CreatedAt)
	}
	info := &virtualtools.BGAgentInfo{
		ID:        snap.ID,
		Name:      snap.Name,
		Status:    string(snap.Status),
		Elapsed:   elapsed.Truncate(time.Second).String(),
		CreatedAt: snap.CreatedAt.Format(time.RFC3339),
	}
	if snap.CompletedAt != nil {
		info.CompletedAt = snap.CompletedAt.Format(time.RFC3339)
	}
	if snap.Status == BGAgentCompleted || snap.Status == BGAgentFailed {
		info.Result = truncateForToolResponse(snap.Result, 4000)
		info.Error = snap.Error
	}
	if snap.Status == BGAgentRunning {
		// Return conversation history with pagination (last N entries, skip offset from end)
		agent := q.registry.Get(sessionID, agentID)
		if agent != nil {
			// Get more entries than needed so we can apply offset
			allHistory := agent.GetRecentHistory(last + offset)
			// Apply offset: trim the last `offset` entries
			if offset > 0 && len(allHistory) > offset {
				allHistory = allHistory[:len(allHistory)-offset]
			} else if offset > 0 {
				allHistory = nil // offset exceeds history length
			}
			// Take only the last `last` entries
			if len(allHistory) > last {
				allHistory = allHistory[len(allHistory)-last:]
			}
			for _, h := range allHistory {
				info.RecentHistory = append(info.RecentHistory, virtualtools.BGAgentHistoryEntry{
					Role: h.Role,
					Text: truncateForToolResponse(h.Text, 1000),
				})
			}
		}
		// Include recent tool calls with timing
		if agent != nil {
			toolCalls := agent.GetRecentToolCalls(5)
			for _, tc := range toolCalls {
				dur := ""
				if tc.Status == "running" {
					dur = time.Since(tc.StartedAt).Truncate(time.Second).String()
				} else if tc.Duration > 0 {
					dur = tc.Duration.Truncate(time.Millisecond).String()
				}
				info.RecentToolCalls = append(info.RecentToolCalls, virtualtools.BGAgentToolCall{
					ToolName: tc.ToolName,
					Duration: dur,
					Status:   tc.Status,
				})
			}
		}
	}
	return info, nil
}

func (q *bgAgentQuerierImpl) ListAgents(sessionID string) ([]*virtualtools.BGAgentInfo, error) {
	agents := q.registry.GetAll(sessionID)
	infos := make([]*virtualtools.BGAgentInfo, 0, len(agents))
	for _, agent := range agents {
		snap := agent.GetSnapshot()
		elapsed := time.Since(snap.CreatedAt)
		if snap.CompletedAt != nil {
			elapsed = snap.CompletedAt.Sub(snap.CreatedAt)
		}
		info := &virtualtools.BGAgentInfo{
			ID:      snap.ID,
			Name:    snap.Name,
			Status:  string(snap.Status),
			Elapsed: elapsed.Truncate(time.Second).String(),
		}
		if snap.Status == BGAgentFailed {
			info.Error = snap.Error
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (q *bgAgentQuerierImpl) TerminateAgent(sessionID, agentID string) error {
	return q.registry.CancelAgent(sessionID, agentID)
}

// emitDelegationStartEvent emits an event when delegation starts
// This event serves as the parent for all sub-agent events (via parent_id linking)
func (api *StreamingAPI) emitDelegationStartEvent(sessionID, delegationID string, depth int, instruction, reasoningLevel, modelID string, servers []string, backgroundAgentID, agentTemplate string) {
	now := time.Now()
	eventID := fmt.Sprintf("%s_delegation_start_%s", sessionID, delegationID)
	eventData := &events.DelegationStartEventData{
		DelegationID:      delegationID,
		Depth:             depth,
		Instruction:       instruction,
		ReasoningLevel:    reasoningLevel,
		ModelID:           modelID,
		Servers:           servers,
		BackgroundAgentID: backgroundAgentID,
		AgentTemplate:     agentTemplate,
		Timestamp:         now.Format(time.RFC3339),
	}
	event := events.Event{
		ID:        eventID,
		Type:      "delegation_start",
		Timestamp: now,
		SessionID: sessionID,
		Data: &unifiedevents.AgentEvent{
			Type:           unifiedevents.EventType("delegation_start"),
			Timestamp:      now,
			HierarchyLevel: depth,
			SessionID:      sessionID,
			Component:      fmt.Sprintf("delegation-%d", depth),
			CorrelationID:  delegationID, // Links all delegation events together
			ParentID: func() string {
				if strings.TrimSpace(backgroundAgentID) == "" {
					return ""
				}
				return fmt.Sprintf("%s_background_agent_started_%s", sessionID, strings.TrimSpace(backgroundAgentID))
			}(),
			Data: eventData,
		},
	}
	api.eventStore.AddEvent(sessionID, event)
	log.Printf("[DELEGATION] Emitted delegation_start event %s for %s at depth %d", eventID, delegationID, depth)
}

type delegationEndStats struct {
	InputTokens  int64
	OutputTokens int64
	ToolCalls    int64
	Duration     string
	TotalCostUSD float64
}

func (api *StreamingAPI) emitDelegationEndEvent(sessionID, delegationID string, depth int, result, errorMsg string, stats *delegationEndStats) {
	now := time.Now()
	eventData := &events.DelegationEndEventData{
		DelegationID: delegationID,
		Depth:        depth,
		Result:       result,
		Error:        errorMsg,
		Success:      errorMsg == "",
		Timestamp:    now.Format(time.RFC3339),
	}
	if stats != nil {
		eventData.InputTokens = stats.InputTokens
		eventData.OutputTokens = stats.OutputTokens
		eventData.ToolCalls = stats.ToolCalls
		eventData.Duration = stats.Duration
		eventData.TotalCostUSD = stats.TotalCostUSD
	}
	api.eventStore.AddEvent(sessionID, events.Event{
		ID: fmt.Sprintf("%s_delegation_end_%s", sessionID, delegationID), Type: "delegation_end", Timestamp: now, SessionID: sessionID,
		Data: &unifiedevents.AgentEvent{
			Type: unifiedevents.EventType("delegation_end"), Timestamp: now, HierarchyLevel: depth, SessionID: sessionID,
			Component: fmt.Sprintf("delegation-%d", depth), CorrelationID: delegationID,
			ParentID: fmt.Sprintf("%s_delegation_start_%s", sessionID, delegationID), Data: eventData,
		},
	})
}
