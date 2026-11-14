package server

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gorilla/mux"
)

// A tab can be opened with its model in one call: engine, model_id, reasoning_effort and connection_id, as the model
// setting for a tab takes them. It goes through the same chat-models handler and the same choice check, so what an
// agent may pick when it opens a tab is exactly what it may pick afterwards.
type newTabModel struct{ engine, modelID, effort, connectionID string }

func (m newTabModel) chosen() bool { return m.engine != "" }

// newTabModelFromArgs reads the optional model choice of a tab-open call. Model fields without an engine are
// refused rather than dropped, so a call never opens a tab on a model it did not ask for.
func newTabModelFromArgs(args map[string]any) (newTabModel, error) {
	arg := func(key string) string { return strings.TrimSpace(externalArg(args, key)) }
	m := newTabModel{engine: arg("engine"), modelID: arg("model_id"), effort: arg("reasoning_effort"), connectionID: arg("connection_id")}
	if m.engine == "" && (m.modelID != "" || m.effort != "" || m.connectionID != "") {
		return m, fmt.Errorf("Name the engine (provider) too: a model, reasoning level or account only applies with an engine from models action=list.")
	}
	return m, nil
}

// refuseNewTabModel checks the choice before the tab exists, so a refused choice opens nothing. It returns 0 when the
// choice stands, else the status and the plain reason.
func (api *StreamingAPI) refuseNewTabModel(r *http.Request, profileID, projectID string, m newTabModel) (int, string) {
	if !m.chosen() {
		return 0, ""
	}
	req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/", nil).WithContext(r.Context()), map[string]string{"id": profileID})
	rec := httptest.NewRecorder()
	userID, slot, ok := api.agentProfileConversationSlot(rec, req, projectID)
	if !ok {
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}
	return api.chatModelChoiceRefusal(r.Context(), userID, slot, AgentProfileChatModel{Engine: m.engine, ModelID: m.modelID, ReasoningEffort: m.effort, ConnectionID: m.connectionID})
}

// applyNewTabModel saves the choice on a tab just opened, with the handler behind models action=set scope=tab.
func (api *StreamingAPI) applyNewTabModel(r *http.Request, profileID, projectID, tabID string, m newTabModel) (int, []byte) {
	if !m.chosen() {
		return http.StatusOK, nil
	}
	status, body := scheduleHandler(r, api.handleAgentProfileChatModels, http.MethodPost, "/api/agent-profiles/work/chat-models", map[string]string{"id": profileID}, nil, map[string]string{
		"project_id": projectID, "conversation_key": projectID + codeChatSideMarker + tabID, "engine": m.engine,
		"model_id": m.modelID, "reasoning_effort": m.effort, "connection_id": m.connectionID,
	})
	log.Printf("[TAB_MODEL] project=%s tab=%s engine=%s model_set=%t effort_set=%t account_set=%t status=%d", projectID, tabID, m.engine, m.modelID != "", m.effort != "", m.connectionID != "", status)
	return status, body
}
