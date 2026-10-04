package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

func TestOAuthNotificationSessionRequiresOwnership(t *testing.T) {
	store := events.NewEventStore(10)
	defer store.Stop()
	store.SetSessionOwner("retained-own", "alice")
	store.SetSessionOwner("retained-other", "bob")
	api := &StreamingAPI{eventStore: store, activeSessions: map[string]*ActiveSessionInfo{
		"active-own":   {SessionID: "active-own", UserID: "alice"},
		"active-other": {SessionID: "active-other", UserID: "bob"},
	}}
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/documents/_users/alice/chat_history/product-conversations.json" {
			w.WriteHeader(404)
			return
		}
		data, _ := json.Marshal(productConversationRegistryDocument{Version: 1, Entries: map[string]ProductConversationRecord{
			"caplayer/main": {ProfileID: "caplayer", SessionID: "restored-own"},
		}})
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"content": string(data)}})
	}))
	defer registry.Close()
	t.Setenv("WORKSPACE_API_URL", registry.URL)
	for _, item := range []struct {
		session string
		allowed bool
	}{
		{"", true}, {"active-own", true}, {"active-other", false}, {"retained-own", true}, {"retained-other", false}, {"restored-own", true}, {"unknown", false},
	} {
		t.Run(item.session, func(t *testing.T) {
			got, err := api.oauthNotificationSession(requestWithUserForSessionAccess("alice"), item.session)
			if (err == nil) != item.allowed || (item.allowed && got != item.session) {
				t.Fatalf("session=%q allowed=%v got=%q err=%v", item.session, item.allowed, got, err)
			}
		})
	}
}

func TestOAuthStartRejectsForeignNotificationTargetBeforeConnecting(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{"other": {SessionID: "other", UserID: "another-user"}}}
	r := requestWithUserForSessionAccess("default")
	r.Body = http.NoBody
	// The product route must reject the target before touching connector tokens.
	r = httptest.NewRequest("POST", "/api/oauth/start", strings.NewReader(`{"server_name":"Notion","scope":"vault","connection_id":"c-11111111","session_id":"other"}`)).WithContext(r.Context())
	w := httptest.NewRecorder()
	api.handleOAuthStart(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign target: %d %s", w.Code, w.Body.String())
	}
}
