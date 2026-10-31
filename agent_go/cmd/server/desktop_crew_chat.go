package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Reuse the desktop's saved website sign-in. No inbound port, CLI credential
// export or second login is needed. Sleep/wake simply interrupts the next poll.
func (api *StreamingAPI) startDesktopCrewChats() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	if !IsLocalMode() {
		return cancel
	}
	go func() {
		workers := map[string]context.CancelFunc{}
		defer func() {
			for _, stop := range workers {
				stop()
			}
		}()
		for {
			state, err := workflowCLIStateRoot()
			if err == nil {
				entries, _ := os.ReadDir(filepath.Join(state, "crew-sync"))
				live := map[string]bool{}
				for _, entry := range entries {
					if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
						continue
					}
					id := strings.TrimSuffix(entry.Name(), ".json")
					binding, e := crewroot.ReadSync(id)
					if e != nil || binding == nil || binding.Side != "local" || binding.Editing != "local" || binding.PendingAction != "" {
						continue
					}
					live[id] = true
					if workers[id] == nil {
						child, stop := context.WithCancel(ctx)
						workers[id] = stop
						go api.pollDesktopCrewChat(child, id)
					}
				}
				for id, stop := range workers {
					if !live[id] {
						stop()
						delete(workers, id)
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
	return cancel
}

func (api *StreamingAPI) desktopCrewChatBinding(ctx context.Context, project string) (*UserClaims, *crewroot.SyncBinding, desktopConnection, error) {
	if !IsLocalMode() {
		return nil, nil, desktopConnection{}, fmt.Errorf("local app required")
	}
	// The local single-user account is derived here, never from a server
	// claim. The remote account is independently checked by the server.
	claims := &UserClaims{UserID: GetDefaultUserID(), Username: GetDefaultUserID()}
	if _, err := api.crewSyncRoots(ctx, claims, project); err != nil {
		return nil, nil, desktopConnection{}, err
	}
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.Side != "local" || b.Editing != "local" || b.PendingAction != "" {
		return nil, nil, desktopConnection{}, fmt.Errorf("local editing is unavailable")
	}
	connection, err := desktopConnectionFor(claims.UserID, b.ConnectionID)
	if err != nil || connection.Server != b.PeerServer || connection.User.ID == "" {
		return nil, nil, desktopConnection{}, fmt.Errorf("paired server sign-in is unavailable")
	}
	return claims, b, connection, nil
}

func desktopCrewChatHTTP(ctx context.Context, owner string, connection desktopConnection, b *crewroot.SyncBinding, result *crewChatResult) (*http.Response, error) {
	method := http.MethodGet
	var body io.Reader
	if result != nil {
		method = http.MethodPost
		data, _ := json.Marshal(result)
		body = bytes.NewReader(data)
	}
	r, err := http.NewRequestWithContext(ctx, method, connection.Server+"/api/crew-chat/"+url.PathEscape(b.PeerProjectID)+"/poll?pair="+url.QueryEscape(b.PairID), body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+connection.Token)
	r.Header.Set("Content-Type", "application/json")
	client := desktopHTTPClient()
	client.Timeout = 35 * time.Second
	response, err := client.Do(r)
	if err != nil {
		return nil, err
	}
	if token := response.Header.Get("X-Renewed-Token"); token != "" {
		desktopConnectionsMu.Lock()
		records, e := readDesktopConnections(owner)
		if e == nil {
			for i := range records {
				if records[i].ID == connection.ID && records[i].Token == connection.Token {
					records[i].Token = token
				}
			}
			e = writeDesktopConnections(owner, records)
		}
		desktopConnectionsMu.Unlock()
		if e != nil {
			response.Body.Close()
			return nil, e
		}
	}
	return response, nil
}

func (api *StreamingAPI) pollDesktopCrewChat(ctx context.Context, project string) {
	lastState := ""
	for ctx.Err() == nil {
		claims, b, connection, err := api.desktopCrewChatBinding(ctx, project)
		state := "unavailable"
		if err == nil {
			response, e := desktopCrewChatHTTP(ctx, claims.UserID, connection, b, nil)
			if e == nil {
				state = fmt.Sprintf("http-%d", response.StatusCode)
				if response.StatusCode == 200 {
					var call crewChatCall
					e = json.NewDecoder(io.LimitReader(response.Body, maxAgentProfileRequestBytes)).Decode(&call)
					response.Body.Close()
					if e == nil && call.ID != "" {
						result := api.runDesktopCrewChat(ctx, project, b.PairID, call)
						// Refresh the saved login after a possible poll renewal.
						_, current, fresh, check := api.desktopCrewChatBinding(ctx, project)
						if check == nil && current.PairID == b.PairID {
							reply, e := desktopCrewChatHTTP(ctx, claims.UserID, fresh, current, &result)
							if e == nil {
								reply.Body.Close()
							}
						}
					}
				} else {
					response.Body.Close()
				}
				if response.StatusCode == 200 || response.StatusCode == 204 {
					state = "connected"
				}
			}
		}
		if state != lastState {
			log.Printf("[CREW_CHAT_RELAY] desktop project=%s state=%s", project, state)
			lastState = state
		}
		if state != "connected" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (api *StreamingAPI) runDesktopCrewChat(ctx context.Context, project, pair string, call crewChatCall) crewChatResult {
	fail := func(code int, message string) crewChatResult {
		body, _ := json.Marshal(map[string]string{"error": message})
		return crewChatResult{ID: call.ID, Status: code, Body: body}
	}
	claims, b, _, err := api.desktopCrewChatBinding(ctx, project)
	if err != nil || b.PairID != pair {
		return fail(403, "This Mac is no longer editing the paired Crew.")
	}
	ctx = context.WithValue(ctx, UserContextKey, claims)
	profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID)
	if err != nil {
		return fail(404, "The local Crew is unavailable.")
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/api/agent-profiles/work/conversation", nil)
	r = mux.SetURLVars(r, map[string]string{"id": "work"})
	conversation, err := api.resolveAgentProfileConversation(r, profile, project, "")
	if err != nil {
		return fail(409, err.Error())
	}
	// Neither a session, a workspace path, nor a provider/account choice is
	// accepted from the remote request. All come from this local main chat.
	query := crewChatEventQuery(call.Query)
	query.Set("workspace_path", conversation.WorkspacePath)
	var body []byte
	var handler http.HandlerFunc
	method, path := http.MethodGet, "/api/sessions/"+conversation.SessionID
	switch call.Operation {
	case "resolve":
		data, _ := json.Marshal(AgentProfileConversationResponse{TabOwnerID: claims.UserID, CanWrite: true, IsMainChat: true, ConversationID: conversation.ConversationID, ConversationKey: conversation.ConversationKey, SessionID: conversation.SessionID, Provider: conversation.Provider})
		log.Printf("[CREW_CHAT_RELAY] local project=%s operation=resolve session=%s conversation=%s request=%s", project, conversation.SessionID, conversation.ConversationID, call.ID)
		return crewChatResult{ID: call.ID, Status: 200, Body: data}
	case "send":
		if strings.TrimSpace(call.Message) == "" {
			return fail(400, "Write a message first.")
		}
		body, _ = json.Marshal(AgentProfileChatRequest{Message: call.Message, DeliveryMode: call.DeliveryMode, ConversationKey: conversation.ConversationKey})
		method, path, handler = http.MethodPost, "/api/agent-profiles/work/query", api.handleAgentProfileChatQuery
	case "events":
		path += "/events"
		handler = api.handleGetSessionEvents
	case "status":
		path += "/status"
		handler = api.handleGetSessionStatus
	case "queue":
		path += "/queued-messages"
		handler = api.handleQueuedConversationMessages
	case "cancel":
		method, path, handler = http.MethodPost, "/api/session/cancel-turn", api.handleCancelCurrentTurn
	case "answer":
		method, path, body, handler = http.MethodPost, path+"/coding-agent-question/answer", call.Answer, api.handleCodingAgentQuestionAnswer
	default:
		return fail(400, "This action is unavailable through the website.")
	}
	r, _ = http.NewRequestWithContext(ctx, method, "http://localhost"+path+"?"+query.Encode(), bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Session-ID", conversation.SessionID)
	r = mux.SetURLVars(r, map[string]string{"id": "work", "session_id": conversation.SessionID})
	capture := &internalResponseCapture{}
	handler(capture, r)
	status := capture.status
	if status == 0 {
		status = 200
	}
	data := capture.body.Bytes()
	if len(data) > crewChatMaxReply {
		return fail(413, "This reply is too large. Open a smaller part of the conversation on your Mac.")
	}
	if !json.Valid(data) {
		data, _ = json.Marshal(map[string]string{"error": string(data)})
	}
	if call.Operation == "send" || call.Operation == "cancel" || call.Operation == "answer" {
		log.Printf("[CREW_CHAT_RELAY] local project=%s operation=%s session=%s conversation=%s request=%s status=%d", project, call.Operation, conversation.SessionID, conversation.ConversationID, call.ID, status)
	}
	return crewChatResult{ID: call.ID, Status: status, Body: data}
}
