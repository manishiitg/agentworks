package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Real website authentication and the existing login wake/inbox paths, with no
// Crew pair: an offline message wakes only its exact device and survives until ack.
func TestAppAgentLoginStreamReplaysInboxAndDeletionDoesNotRetarget(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AUTH_SECRET", "app-login-stream-test-signing-secret")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","can_create":true}]}`)
	api := &StreamingAPI{}
	router := http.NewServeMux()
	router.HandleFunc("/api/external/v1/agents/app", api.handleAppAgentRegistration)
	router.HandleFunc("/api/external/v1/agents/events", api.handleAgentEvents)
	server := httptest.NewServer(AuthMiddleware(router))
	defer server.Close()
	token, err := generateSessionJWT("owner", "owner", "", "", "app-stream-login")
	if err != nil {
		t.Fatal(err)
	}
	call := func(args map[string]any) (int, map[string]any) {
		t.Helper()
		data, _ := json.Marshal(args)
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/external/v1/agents/app", bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		out := map[string]any{}
		if err := json.NewDecoder(response.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, out
	}
	register := func(name, device, chat string) map[string]any {
		code, out := call(map[string]any{"action": "register", "agent_name": name, "device_id": device, "conversation_id": chat})
		if code != 200 {
			t.Fatalf("registration: %d %v", code, out)
		}
		return out
	}
	sender := register("source-app", "first-device", "source-chat")
	receiver := register("target-app", "second-device", "target-chat")
	code, sent := call(map[string]any{"action": "send", "agent_name": "source-app", "attachment_id": sender["attachment_id"], "target": "agent:target-app", "message": "pending private note", "submission_id": "app-stream-submit"})
	if code != 200 {
		t.Fatalf("offline send: %d %v", code, sent)
	}
	streamCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(streamCtx, http.MethodGet, server.URL+"/api/external/v1/agents/events?device_id=second-device", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("login stream: %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	hinted := false
	for scanner.Scan() {
		raw, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		if strings.Contains(raw, "pending private note") {
			t.Fatal("wake stream leaked a message body")
		}
		var event map[string]any
		if json.Unmarshal([]byte(raw), &event) != nil {
			continue
		}
		if event["type"] == "inbox_changed" {
			if event["agent_id"] != receiver["agent_id"] {
				t.Fatalf("wrong device hint: %v", event)
			}
			hinted = true
			break
		}
	}
	if !hinted {
		t.Fatalf("pending message did not wake: %v", scanner.Err())
	}
	snapshot, err := readAgentMessageSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range snapshot.Agents {
		if a.ID == sender["agent_id"] && namedAgentOnline(a) {
			t.Fatal("receiver stream invented sender device presence")
		}
		if a.ID == receiver["agent_id"] && !namedAgentOnline(a) {
			t.Fatal("receiver stream not represented online")
		}
	}
	inbox := map[string]any{"action": "inbox", "agent_name": "target-app", "attachment_id": receiver["attachment_id"]}
	code, body := call(inbox)
	if code != 200 || len(body["messages"].([]any)) != 1 {
		t.Fatalf("pending inbox: %d %v", code, body)
	}
	message := body["messages"].([]any)[0].(map[string]any)
	if message["own_agent_message"] != true {
		t.Fatal("same-login delivery lost own-agent metadata")
	}
	// Removing a local chat retires its registered ID. Reusing its name cannot
	// redirect the previous conversation or pending message to a new ID.
	code, removed := call(map[string]any{"action": "remove", "device_id": "second-device", "agent_id": receiver["agent_id"], "conversation_id": "target-chat"})
	if code != 200 || removed["removed"] != true {
		t.Fatalf("exact removal: %d %v", code, removed)
	}
	if code, _ := call(inbox); code != 403 {
		t.Fatalf("removed attachment read pending body: %d", code)
	}
	replacement := register("target-app", "second-device", "replacement-chat")
	if replacement["agent_id"] == receiver["agent_id"] {
		t.Fatal("deleted registration identity reused")
	}
	inbox["attachment_id"] = replacement["attachment_id"]
	code, body = call(inbox)
	if code != 200 || len(body["messages"].([]any)) != 0 {
		t.Fatalf("old inbox reached replacement: %d %v", code, body)
	}
	cancel()
	response.Body.Close()
}
