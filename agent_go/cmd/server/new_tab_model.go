package server

import (
	"encoding/json"
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
	save := func(m newTabModel) (int, []byte) {
		return scheduleHandler(r, api.handleAgentProfileChatModels, http.MethodPost, "/api/agent-profiles/work/chat-models", map[string]string{"id": profileID}, nil, map[string]string{
			"project_id": projectID, "conversation_key": projectID + codeChatSideMarker + tabID, "engine": m.engine,
			"model_id": m.modelID, "reasoning_effort": m.effort, "connection_id": m.connectionID,
		})
	}
	asked := m.connectionID
	m.connectionID = api.newTabAccount(r, profileID, projectID, m)
	status, body := save(m)
	if status >= 400 && asked == "" && m.connectionID != "" {
		// The filled-in account was refused: keep the tab's choice as asked rather than fail the open.
		m.connectionID = ""
		status, body = save(m)
	}
	log.Printf("[TAB_MODEL] project=%s tab=%s engine=%s model_set=%t effort_set=%t account_set=%t status=%d", projectID, tabID, m.engine, m.modelID != "", m.effort != "", m.connectionID != "", status)
	return status, body
}

// newTabAccount names the account a tab opened with an engine but no account runs on. A saved engine with no account
// reads as the company account when a turn is checked, while the turn itself fills in the person's own account when
// they have one, so a new tab on a program the person has their own account for refused every message as "This
// chat's provider account changed". Saving the account the turn would use keeps the two the same, as fork acceptance
// does (PLAT-746-1057).
func (api *StreamingAPI) newTabAccount(r *http.Request, profileID, projectID string, m newTabModel) string {
	if m.connectionID != "" {
		return m.connectionID
	}
	req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/", nil).WithContext(r.Context()), map[string]string{"id": profileID})
	userID, slot, ok := api.agentProfileConversationSlot(httptest.NewRecorder(), req, projectID)
	if !ok {
		return ""
	}
	option, offered := findProviderOptionByID(slot.profile.Runtime.ProviderOptions, m.engine)
	if !offered || strings.TrimSpace(option.Provider) == "" {
		return ""
	}
	return productChatDefaultAccount(r.Context(), userID, slot.profile, option.Provider)
}

// applyForkModel runs a fork on the model its caller asked for. Fork copies the source's model, so before this an
// engine passed to fork was dropped without a word and a "Cursor" fork came out as Claude. The choice was checked
// before the fork was made; the switch goes through the same handler as models action=set, so the fork starts a new
// native session of that program from the copied conversation. It returns the fork's reply with the model it runs on,
// or the fork's reply with a plain note when the model could not be saved (the fork itself stands).
func (api *StreamingAPI) applyForkModel(r *http.Request, profileID, projectID string, m newTabModel, forkBody []byte) []byte {
	if !m.chosen() {
		return forkBody
	}
	var out map[string]any
	if json.Unmarshal(forkBody, &out) != nil {
		return forkBody
	}
	chatID, _ := out["chat_id"].(string)
	if chatID == "" {
		return forkBody
	}
	status, body := api.applyNewTabModel(r, profileID, projectID, chatID, m)
	if status >= 400 {
		out["model_not_applied"] = "The fork was made but still runs on its source's model: " + strings.TrimSpace(string(body)) + ". Change it in the fork's model menu (in its message box), or delete the fork."
	} else {
		out["engine"], out["model_id"], out["reasoning_effort"] = m.engine, m.modelID, m.effort
		delete(out, "connection_id")
		delete(out, "provider")
		out["next"] = "Forked onto " + m.engine + ". It starts a new " + m.engine + " session from the copied conversation on its first message."
	}
	log.Printf("[CHAT_FORK] fork %s engine=%s status=%d", chatID, m.engine, status)
	raw, err := json.Marshal(out)
	if err != nil {
		return forkBody
	}
	return raw
}
