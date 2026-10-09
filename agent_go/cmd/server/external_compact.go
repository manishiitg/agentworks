package server

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// External MCP clients are agents with small context windows. These helpers
// keep the status and listing tools compact and bounded (MCP feedback,
// 2026-10-08): a size budget on event pages, the final answer at the top level,
// compact listing views, and a wait on chat.

// externalRunPageBytes bounds one run_status or builder_status event page.
const externalRunPageBytes = 100 << 10

// externalFinalAnswerRunes bounds final_answer.
const externalFinalAnswerRunes = 20000

func externalBoolArg(args map[string]any, name string) bool {
	v, _ := args[name].(bool)
	return v
}

// externalCompact reports whether a listing or status call wants the compact
// view. Compact is the default (owner, 2026-10-08: nothing relies on the full
// shape yet); compact: false returns the full one.
func externalCompact(args map[string]any) bool {
	v, set := args["compact"].(bool)
	return !set || v
}

// externalTurnFields adds the compact answer to a status response: turn_status
// (running, waiting_for_input or idle) and, once the session is idle,
// final_answer, the newest assistant reply.
func (api *StreamingAPI) externalTurnFields(response map[string]interface{}, sessionID string, waiting bool) {
	status := "idle"
	switch {
	case waiting:
		status = "waiting_for_input"
	case api.isSessionBusy(sessionID):
		status = "running"
	}
	response["turn_status"] = status
	if status != "idle" || api.eventStore == nil {
		return
	}
	if answer, ok := api.eventStore.LatestAnswer(sessionID, -1); ok {
		runes := []rune(answer)
		if len(runes) > externalFinalAnswerRunes {
			answer = string(runes[:externalFinalAnswerRunes])
			response["final_answer_truncated"] = true
		}
		response["final_answer"] = answer
	}
}

// externalBufferedResponse holds a handler's response so the caller can add to
// it before it is sent.
type externalBufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *externalBufferedResponse) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *externalBufferedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *externalBufferedResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}
func (w *externalBufferedResponse) flushTo(target http.ResponseWriter) {
	for key, values := range w.header {
		target.Header()[key] = append([]string(nil), values...)
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	target.WriteHeader(w.status)
	_, _ = target.Write(w.body.Bytes())
}

func externalPendingInputs(sessionID string) []virtualtools.HumanFeedbackRequest {
	return virtualtools.GetHumanFeedbackStore().PendingForSession(sessionID, time.Now())
}

// externalWaitForTurn waits up to wait for a turn started after afterIndex to
// finish or ask a question, then adds the outcome to the started response.
func (api *StreamingAPI) externalWaitForTurn(started map[string]interface{}, sessionID string, afterIndex int, wait time.Duration) {
	deadline := time.Now().Add(wait)
	for {
		pending := externalPendingInputs(sessionID)
		if len(pending) > 0 {
			started["turn_status"] = "waiting_for_input"
			started["pending_inputs"] = pending
			started["needs_user_input"] = true
			return
		}
		if api.eventStore != nil && !api.isSessionBusy(sessionID) {
			if answer, ok := api.eventStore.LatestAnswer(sessionID, afterIndex); ok {
				started["turn_status"] = "idle"
				started["final_answer"] = answer
				return
			}
		}
		if !time.Now().Before(deadline) {
			started["turn_status"] = "running"
			started["next"] = "Still running: poll runs action=status with this session_id for the answer or a pending question."
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// externalCompactWorkflow is one workflow without its manifest: enough to pick
// one (get_workflow returns the full manifest of the chosen one).
func externalCompactWorkflow(item DiscoveredWorkflow) map[string]any {
	view := map[string]any{"workspace_path": item.WorkspacePath, "my_access": item.MyAccess}
	if m := item.Manifest; m != nil {
		enabled := 0
		for _, s := range m.Schedules {
			if s.Enabled {
				enabled++
			}
		}
		view["id"], view["label"] = m.ID, m.Label
		if m.Icon != "" {
			view["icon"] = m.Icon
		}
		if m.Kind != "" {
			view["kind"] = m.Kind
		}
		if m.Access != nil {
			view["owners"] = m.Access.Owners
		}
		view["schedules"], view["schedules_enabled"] = len(m.Schedules), enabled
		view["pulse_enabled"] = m.PulseEnabled()
	}
	return view
}

// externalCompactScheduleRun is one run history row without its group list,
// final response or usage.
func externalCompactScheduleRun(run ScheduleRunEntry) map[string]any {
	view := map[string]any{"id": run.ID, "status": run.Status, "started_at": run.StartedAt}
	if run.CompletedAt != nil {
		view["completed_at"] = run.CompletedAt
	}
	if run.DurationMs != nil {
		view["duration_ms"] = *run.DurationMs
	}
	if run.Error != "" {
		view["error"] = strings.TrimSpace(run.Error)
	}
	if run.RunFolder != "" {
		view["run_folder"] = run.RunFolder
	}
	if run.TriggerSource != "" {
		view["trigger"] = run.TriggerSource
	}
	if run.RanWorkflow != nil {
		view["ran_workflow"] = *run.RanWorkflow
	}
	// Webhook deploy metadata is small and is what a webhook run is read for.
	if run.Webhook != nil {
		view["webhook"] = run.Webhook
	}
	return view
}
