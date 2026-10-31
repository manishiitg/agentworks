package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// PLAT-856-426: extend the real paired-Crew fixture, so refusals cannot pass
// just because the project is absent. An allowed owner roundtrip follows them.
func checkCrewChatRelayOwnerBoundary(t *testing.T, api *StreamingAPI, project, pair string) {
	t.Helper()
	request := func(claims *UserClaims, suppliedPair string, body []byte) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/crew-chat/"+project+"/poll?pair="+suppliedPair, bytes.NewReader(body))
		r = r.WithContext(context.WithValue(t.Context(), UserContextKey, claims))
		return mux.SetURLVars(r, map[string]string{"project": project})
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	for _, tc := range []struct {
		name   string
		claims *UserClaims
		pair   string
		want   int
	}{
		{"other valid login", &UserClaims{UserID: "other", Username: "other"}, pair, 403},
		{"personal MCP token", crewWriter("owner"), pair, 403},
		{"bot", &UserClaims{UserID: "owner", BotRouteGrant: "route"}, pair, 403},
		{"wrong pair", owner, "wrong-pair", 403},
		{"owner", owner, pair, 204},
	} {
		w := httptest.NewRecorder()
		api.handleCrewChatPoll(w, request(tc.claims, tc.pair, []byte(`{"id":"expired","status":200,"body":{}}`)))
		if w.Code != tc.want {
			t.Fatalf("%s: got %d want %d: %s", tc.name, w.Code, tc.want, w.Body.String())
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	r := mux.SetURLVars(request(owner, pair, nil).WithContext(context.WithValue(ctx, UserContextKey, owner)), map[string]string{"project": project})
	result := make(chan crewChatResult, 1)
	go func() { result <- api.callCrewMac(r, project, crewChatCall{Operation: "resolve"}) }()
	poll := httptest.NewRecorder()
	get := r.Clone(r.Context())
	get.Method = http.MethodGet
	api.handleCrewChatPoll(poll, get)
	var call crewChatCall
	if poll.Code != 200 || json.Unmarshal(poll.Body.Bytes(), &call) != nil || call.Operation != "resolve" {
		t.Fatalf("poll: %d %s", poll.Code, poll.Body.String())
	}
	reply, _ := json.Marshal(crewChatResult{ID: call.ID, Status: 200, Body: json.RawMessage(`{"conversation_id":"local-conversation","session_id":"local-session"}`)})
	w := httptest.NewRecorder()
	api.handleCrewChatPoll(w, request(owner, pair, reply))
	got := <-result
	if got.Status != 200 || !bytes.Contains(got.Body, []byte("local-conversation")) || !bytes.Contains(got.Body, []byte(pairedMacSessionPrefix+project)) {
		t.Fatalf("owner response lost local conversation: %+v", got)
	}
	crewChatRelays.Lock()
	box := crewChatRelays.boxes[crewChatMailboxKey("owner", project, pair)]
	box.lastPoll = time.Time{}
	crewChatRelays.Unlock()
	if got := api.callCrewMac(r, project, crewChatCall{Operation: "send", Message: "hello"}); got.Status != 503 {
		t.Fatalf("offline must refuse, never execute on server: %d", got.Status)
	}
}
