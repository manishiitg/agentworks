package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

// Background work a chat started (PLAT-705): steps and workflow runs from
// execute_step / run_full_workflow, delegated sub-agents, and calls to other
// chats or Crews. The composer's Stop only interrupts the CLI's current turn;
// this list backs the "N running" pill, where each item has its own Stop.
// Every item lives in bgAgentRegistry, which is keyed by session, so a caller
// can only ever see or stop its own chat's work.

type SessionBackgroundWorkItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // step | workflow_run | sub_agent | crew_call
	Label     string    `json:"label"`
	StartedAt time.Time `json:"started_at"`
	CanStop   bool      `json:"can_stop"`
	// Status is one line on what the item is doing now (its running tool,
	// running step, or latest message), from state the registry already holds.
	Status string `json:"status,omitempty"`
}

func oneLine(text string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return text
}

// backgroundWorkStatus reads the item's live tool calls, its running children
// (a workflow run's current step) and the tail of its own conversation.
func backgroundWorkStatus(agent *BackgroundAgent, children []BackgroundAgentSnapshot) string {
	if calls := agent.GetRecentToolCalls(5); len(calls) > 0 {
		for i := len(calls) - 1; i >= 0; i-- {
			if calls[i].Status == "running" {
				return "Running " + calls[i].ToolName + " (" + time.Since(calls[i].StartedAt).Truncate(time.Second).String() + ")"
			}
		}
	}
	if len(children) > 0 {
		sort.Slice(children, func(i, j int) bool { return children[i].CreatedAt.After(children[j].CreatedAt) })
		name := oneLine(firstSessionExecutionString(children[0].Name, children[0].ID), 80)
		if len(children) > 1 {
			return fmt.Sprintf("Working on %s (+%d more)", name, len(children)-1)
		}
		return "Working on " + name
	}
	if history := agent.GetRecentHistory(3); len(history) > 0 {
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == "assistant" && strings.TrimSpace(history[i].Text) != "" {
				return oneLine(history[i].Text, 100)
			}
		}
	}
	if calls := agent.GetRecentToolCalls(1); len(calls) == 1 {
		return "Last used " + calls[0].ToolName
	}
	return ""
}

func backgroundWorkKind(snap BackgroundAgentSnapshot) string {
	executionType := ""
	if snap.Metadata != nil {
		executionType = strings.TrimSpace(snap.Metadata["execution_type"])
	}
	kind := normalizeTrackedExecutionKind(snap.Kind)
	switch {
	case executionType == "function-call-auto-notify" || strings.HasPrefix(snap.ID, "function-call-"):
		return "crew_call"
	case executionType == "full-workflow" || kind == string(orchEvents.ExecutionKindFullRun) || strings.HasPrefix(snap.ID, "workflow-full-"):
		return "workflow_run"
	case kind == "workflow_step" || kind == "message_sequence_item" || isWorkflowStepTrackingExecution(snap.ID, snap.Name, snap.Metadata):
		return "step"
	default:
		return "sub_agent"
	}
}

// sessionBackgroundWork lists the session's running background items, one per
// top-level execution: a child of another running item (a workflow run's steps
// and their progress mirrors) is stopped with its parent and not listed.
func (api *StreamingAPI) sessionBackgroundWork(sessionID string) []SessionBackgroundWorkItem {
	items := []SessionBackgroundWorkItem{}
	if api == nil || api.bgAgentRegistry == nil {
		return items
	}
	running := map[string]BackgroundAgentSnapshot{}
	agents := map[string]*BackgroundAgent{}
	canStop := map[string]bool{}
	for _, agent := range api.bgAgentRegistry.GetAll(sessionID) {
		if agent == nil {
			continue
		}
		snap := agent.GetSnapshot()
		if snap.Status != BGAgentRunning {
			continue
		}
		running[snap.ID] = snap
		agents[snap.ID] = agent
		agent.mu.RLock()
		canStop[snap.ID] = agent.cancel != nil
		agent.mu.RUnlock()
	}
	for id, snap := range running {
		if _, nested := running[strings.TrimSpace(snap.ParentExecutionID)]; nested {
			continue
		}
		if idx := strings.Index(id, "-step-"); idx > 0 {
			if _, mirror := running[id[:idx]]; mirror {
				continue
			}
		}
		label := strings.TrimSpace(snap.Name)
		if label == "" {
			label = strings.TrimSpace(snap.Instruction)
		}
		if label == "" {
			label = id
		}
		var children []BackgroundAgentSnapshot
		for childID, child := range running {
			if childID != id && (strings.TrimSpace(child.ParentExecutionID) == id || strings.HasPrefix(childID, id+"-step-")) {
				children = append(children, child)
			}
		}
		items = append(items, SessionBackgroundWorkItem{
			ID: id, Kind: backgroundWorkKind(snap), Label: label, StartedAt: snap.CreatedAt, CanStop: canStop[id],
			Status: backgroundWorkStatus(agents[id], children),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt.Before(items[j].StartedAt) })
	return items
}

// ownsChatSession is true only for the session's own user. A session without
// a recorded owner belongs to the single-user default account.
func (api *StreamingAPI) ownsChatSession(r *http.Request, sessionID string) bool {
	currentUserID := GetUserIDFromContext(r.Context())
	session, exists := api.getActiveSession(sessionID)
	if !exists || session == nil {
		return false
	}
	if session.UserID == "" {
		return currentUserID == "" || currentUserID == GetDefaultUserID()
	}
	return session.UserID == currentUserID
}

// GET /api/sessions/{session_id}/background-work
func (api *StreamingAPI) handleGetSessionBackgroundWork(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["session_id"]
	if sessionID == "" || !api.ownsChatSession(r, sessionID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"session_id": sessionID, "items": api.sessionBackgroundWork(sessionID)})
}

// POST /api/sessions/{session_id}/background-work/{execution_id}/stop ends one
// item and its children only; the chat's turn and other items keep running.
func (api *StreamingAPI) handleStopSessionBackgroundWork(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID, execID := vars["session_id"], strings.TrimSpace(vars["execution_id"])
	if sessionID == "" || execID == "" || !api.ownsChatSession(r, sessionID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	if !api.stopSessionBackgroundWorkItem(sessionID, execID) {
		http.Error(w, "No running background work with that ID in this chat", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"stopped": true, "id": execID})
}

func (api *StreamingAPI) stopSessionBackgroundWorkItem(sessionID, execID string) bool {
	if api == nil || api.bgAgentRegistry == nil {
		return false
	}
	agent := api.bgAgentRegistry.Get(sessionID, execID)
	if agent == nil || agent.GetStatus() != BGAgentRunning {
		return false
	}
	// A workflow step or run goes through the workshop's own stop (stop_step):
	// it marks the step cancelled before its context unwinds, then terminates
	// the registry subtree through the execution notifier.
	if raw, ok := api.workshopChatSessions.Load(sessionID); ok {
		if ws, ok := raw.(*stepworkflow.WorkshopChatSession); ok && ws != nil {
			if _, err := ws.StopExecution(execID); err == nil {
				api.completeTrackedExecution(execID, trackedExecutionStatusCanceled, "stopped by user", nil)
				publishSessionsChanged()
				return true
			}
		}
	}
	// Sub-agents and chat/Crew calls: cancel the item and its children.
	for _, stopped := range api.bgAgentRegistry.CancelExecutionTree(sessionID, execID) {
		snap := stopped.GetSnapshot()
		api.completeTrackedExecution(snap.ID, trackedExecutionStatusCanceled, "stopped by user", nil)
		if stopped.MarkTerminalNotified() {
			api.emitBackgroundAgentTerminated(sessionID, snap.ID, snap.Name, "")
		}
	}
	api.observeRuntimeSnapshot(sessionID)
	publishSessionsChanged()
	return true
}
