package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A send chooses where work runs. Opening a conversation does not move it.
// Offline local work remains usable; divergent journals are preserved on return.
func (api *StreamingAPI) ensureCrewNativeExecution(w http.ResponseWriter, r *http.Request, profile, key string) bool {
	if profile != "work" || !crewChatWebsiteClaims(GetUserFromContext(r.Context())) {
		return false
	}
	project := crewResourceProjectID(key)
	if project == "" {
		return false
	}
	claims := GetUserFromContext(r.Context())
	roots, err := api.crewSyncRoots(r.Context(), claims, project)
	if err != nil {
		return false
	}
	b, err := crewroot.ReadSync(roots.ID)
	if err != nil || b == nil || b.Mode == "manual" {
		return false
	}
	main, err := api.crewHandoffMain(r.Context(), claims, project)
	if err != nil || (main.ConversationKey != key && key != project) {
		return false
	}
	fail := func(message string) bool { externalError(w, 409, "session_sync_unavailable", message); return true }
	if IsLocalMode() {
		if b.Side != "local" {
			return false
		}
		connection, e := desktopConnectionFor(claims.UserID, b.ConnectionID)
		if e != nil {
			return fail("Reconnect this project's server before bringing its work back.")
		}
		check, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		status, e := desktopCrewSyncCall(check, claims.UserID, &connection, b.PeerProjectID, crewSyncRequest{Action: "status", PairID: b.PairID})
		cancel()
		if e != nil {
			if b.Editing == "local" {
				return false
			}
			return fail("Connect to the internet to bring this conversation’s latest work back from the server.")
		}
		if status.Binding == nil || status.Binding.PairID != b.PairID {
			return fail("Reconnect this project's server.")
		}
		if status.Binding.Editing == "local" {
			return false
		}
		b.Editing = "server"
		if e = crewroot.WriteSync(roots.ID, *b); e != nil {
			return fail(e.Error())
		}
		response := api.runInternalCrewHandoff(r.Context(), claims, roots.ID, b.ConnectionID, "local")
		if response.status >= 400 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(response.status)
			w.Write(response.body.Bytes())
			return true
		}
		forwardCrewHandoffNotice(w, response.body.Bytes())
		r.Header.Del("X-Session-ID")
		return false
	}
	if b.Side != "server" || b.Editing != "local" {
		return false
	}
	// A reachable Mac flushes current work before the server resumes. A failed
	// network attempt is not proof that an in-progress turn reached the mirror.
	result := api.callCrewMac(r, project, crewChatCall{Operation: "handoff", ConversationID: main.ConversationID})
	if result.Status < 300 {
		forwardCrewHandoffNotice(w, result.Body)
		r.Header.Del("X-Session-ID")
		return false
	}
	if result.Status != 503 && result.Status != 504 {
		writeCrewChatResult(w, result)
		return true
	}
	out, e := api.crewSync(r.Context(), claims, roots.ID, crewSyncRequest{Action: "activate", PairID: b.PairID})
	if e != nil {
		return fail(e.Error())
	}
	r.Header.Del("X-Session-ID")
	w.Header().Set("X-AgentWorks-Sync-Notice", out.ConversationNotice)
	return false
}

func (api *StreamingAPI) runInternalCrewHandoff(ctx context.Context, claims *UserClaims, project, connection, action string) *internalResponseCapture {
	raw, _ := json.Marshal(map[string]string{"action": action})
	ctx = context.WithValue(ctx, UserContextKey, claims)
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/", bytes.NewReader(raw))
	r = mux.SetURLVars(r, map[string]string{"project": project, "connection": connection})
	response := &internalResponseCapture{}
	api.runDesktopCrewSync(response, r, false)
	return response
}

// Automatic sends use the same notice as the explicit sync response. A native
// conflict can succeed as archive continuation and must remain visible.
func forwardCrewHandoffNotice(w http.ResponseWriter, raw []byte) {
	var reply crewSyncReply
	if json.Unmarshal(raw, &reply) == nil && reply.ConversationNotice != "" {
		w.Header().Set("X-AgentWorks-Sync-Notice", reply.ConversationNotice)
	}
}
