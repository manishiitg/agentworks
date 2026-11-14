package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gorilla/mux"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// A real signed-in recipient's dashboard cannot adopt the owner's chat or
// change its recorded owner; reopening resolves the same private recipient chat.
// A shared Crew needs no saved dashboard to open that conversation.
func TestProjectAskConversationKeepsCrewCallerOwnership(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AUTH_SECRET", "project-chat-ownership-test-secret")
	f.api.eventStore = storeevents.NewEventStore(50)
	router := mux.NewRouter()
	router.HandleFunc("/api/project-ask/conversation", f.api.handleProjectAskConversation).Methods(http.MethodPost)
	router.HandleFunc("/api/prompt-links", f.api.handlePromptLinkCreate).Methods(http.MethodPost)
	router.HandleFunc("/api/prompt-links/open", f.api.handlePromptLinkOpen).Methods(http.MethodPost)
	server := httptest.NewServer(AuthMiddleware(router))
	defer server.Close()
	selectedAgent := ""
	call := func(user string) (int, map[string]any) {
		t.Helper()
		token, err := GenerateJWT(user, user, "")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"workspace": externalCommandCrewRoot, "agent_key": selectedAgent})
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/project-ask/conversation", bytes.NewReader(raw))
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		var out map[string]any
		if json.Unmarshal(body, &out) != nil {
			t.Fatalf("bad response: %s", body)
		}
		return response.StatusCode, out
	}
	status, empty := call("owner")
	if status != 200 || empty["choose_agent"] != true {
		t.Fatalf("empty Crew must offer a choice, not create Main: %d %v", status, empty)
	}
	ctx := internalBotRequestContext(t.Context(), "owner")
	profile, err := f.api.agentProfiles.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	selectedAgent = "team:chat:chosen"
	binding, _, err := resolveConversationBindingForUser(ctx, "owner", profile, selectedAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", profile, binding, ""); err != nil {
		t.Fatal(err)
	}
	// Sharing a Crew alone does not share an agent.
	selectedAgent = ""
	if status, none := call("other"); status != 200 || none["choose_agent"] != true || len(none["agents"].([]any)) != 0 {
		t.Fatalf("unshared agent exposed: %d %v", status, none)
	}
	selectedAgent = "team:chat:chosen"
	identity, _ := projectIdentityOf(externalCommandCrewRoot)
	if err = defaultProjectOwners().modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		id := projectOwnerKey(identity.Product, identity.Folder)
		record := projects[id]
		record.SharedAgents = []string{selectedAgent}
		projects[id] = record
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	status, owner := call("owner")
	if status != 200 {
		t.Fatalf("owner: %d %v", status, owner)
	}
	ownerID := owner["conversation"].(map[string]any)["session_id"].(string)
	f.api.eventStore.SetSessionOwner(ownerID, "owner")
	status, reader := call("other")
	if status != 200 || reader["mode"] != "run" {
		t.Fatalf("recipient: %d %v", status, reader)
	}
	readerID := reader["conversation"].(map[string]any)["session_id"].(string)
	if ownerID == readerID || f.api.eventStore.GetSessionOwner(ownerID) != "owner" {
		t.Fatal("recipient acquired or changed the owner's session")
	}
	_, again := call("other")
	if again["conversation"].(map[string]any)["session_id"] != readerID {
		t.Fatal("recipient chat identity changed on reopen")
	}

	listed, err := dashboardWorkspaceRequest(t.Context(), dashboards.Request{Root: externalCommandCrewRoot, Action: "list", IncludeDraft: true})
	if err != nil || len(listed.Dashboards) != 0 {
		t.Fatalf("Ask required or created a dashboard: %+v %v", listed, err)
	}
	// PLAT-856: prompt links reuse Ask admission; they grant no agent access,
	// expose no owner session and open only the recipient's normal conversation.
	t.Run("prompt link keeps recipient ownership and rechecks sharing", func(t *testing.T) {
		t.Setenv("PUBLIC_URL", "https://agents.example.com")
		linkCall := func(user, route string, input map[string]any) (int, map[string]any) {
			t.Helper()
			token, err := GenerateJWT(user, user, "")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(input)
			req, _ := http.NewRequest(http.MethodPost, server.URL+route, bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+token)
			res, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var result map[string]any
			if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
			return res.StatusCode, result
		}
		prompt := "Explain A&B + café?"
		status, generated := linkCall("owner", "/api/prompt-links", map[string]any{"crew": "team", "agent": selectedAgent, "prompt": prompt})
		if status != 200 {
			t.Fatalf("create link: %d %v", status, generated)
		}
		link, err := url.Parse(generated["url"].(string))
		if err != nil || link.Path != "/new" || link.Query().Get("prompt") != prompt || len(link.Query()) != 3 {
			t.Fatalf("invalid draft-only link: %v %v", link, err)
		}
		input := map[string]any{"crew": link.Query().Get("crew"), "agent": link.Query().Get("agent")}
		status, opened := linkCall("other", "/api/prompt-links/open", input)
		if status != 200 || opened["conversation"].(map[string]any)["session_id"] != readerID || f.api.eventStore.GetSessionOwner(ownerID) != "owner" {
			t.Fatalf("link adopted the owner's chat: %d %v", status, opened)
		}
		if err := defaultProjectOwners().modify(func(projects map[string]projectOwnerRecord) (bool, error) {
			id := projectOwnerKey(identity.Product, identity.Folder)
			record := projects[id]
			record.SharedAgents = nil
			projects[id] = record
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := defaultProjectOwners().modify(func(projects map[string]projectOwnerRecord) (bool, error) {
				id := projectOwnerKey(identity.Product, identity.Folder)
				record := projects[id]
				record.SharedAgents = []string{selectedAgent}
				projects[id] = record
				return true, nil
			}); err != nil {
				t.Fatal(err)
			}
		}()
		if status, opened := linkCall("other", "/api/prompt-links/open", input); status != 404 || opened["conversation"] != nil {
			t.Fatalf("link retained revoked agent access: %d %v", status, opened)
		}
	})
	// A conflicting stored owner is a refusal, never repaired by adoption.
	f.api.eventStore.SetSessionOwner(readerID, "owner")
	if status, _ := call("other"); status != 403 && status != 409 {
		t.Fatalf("conflicting session owner accepted: %d", status)
	}
	if f.api.eventStore.GetSessionOwner(readerID) != "owner" {
		t.Fatal("resolver rewrote the stored owner")
	}
}
