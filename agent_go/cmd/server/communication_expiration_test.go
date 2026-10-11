package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// A retained address explains expiry to its account without becoming an access
// grant. Exercise the real MCP request handler and workspace store after prune.
func TestCommunicationExpirationKeepsAccountAndTargetBounds(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	ctx := internalBotRequestContext(context.Background(), "owner")
	target, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "crew:Beta")
	if err != nil {
		t.Fatal(err)
	}
	c := agentConversation{ID: "inbox-expired", Endpoints: [2]agentMessageEndpoint{
		{UserID: "owner", Kind: triggerCallerUser, ID: "owner", External: true}, targetMessageEndpoint("owner", target)},
		Messages: []agentMessage{{ID: "msg-old", Sequence: 1, Delivery: "delivered", Text: "private body", SentAt: time.Now().Add(-8 * 24 * time.Hour)}}}
	store := agentMessageStore{Conversations: []agentConversation{c}}
	pruneAgentConversations(&store, time.Now(), "")
	if len(store.Conversations) != 0 || len(store.Expired) != 1 {
		t.Fatal("prune did not retain ownership")
	}
	raw, _ := json.Marshal(store)
	if strings.Contains(string(raw), "private body") {
		t.Fatal("expired body retained")
	}
	agentMessageMu.Lock()
	err = saveAgentMessageStore(ctx, store)
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	request := func(user, crew, inbox string) (int, map[string]any) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"name": "messages", "arguments": map[string]any{"action": "read", "inbox_id": inbox}})
		claims := &UserClaims{UserID: user, Username: user, AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{crew}}}
		req := httptest.NewRequest("POST", "/api/external/v1/call", strings.NewReader(string(body)))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		rec := httptest.NewRecorder()
		env.api.handleExternalCall(rec, req)
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	status, out := request("owner", "beta", c.ID)
	errorBody, _ := out["error"].(map[string]any)
	if status != 410 || errorBody["code"] != "expired" {
		t.Fatalf("known deleted inbox = %d %v", status, out)
	}
	for _, tc := range [][3]string{{"other", "beta", c.ID}, {"owner", "alpha", c.ID}, {"owner", "beta", "unknown"}} {
		status, out := request(tc[0], tc[1], tc[2])
		if status == 410 || status == 200 {
			t.Fatalf("address leaked or admitted: %d %v", status, out)
		}
	}
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	call := &crewFunctionCall{ID: "fn-expired", TargetKind: triggerCallerCrew, TargetPath: "Crew/example", Files: []structuredFunctionFile{{Name: "report.txt"}}}
	dir := filepath.Join(docs, call.outputFolder())
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCrewFunctionOutput(ctx, call, "report.txt", 0, 1024); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := readCrewFunctionOutput(ctx, call, "report.txt", 0, 1024); !errors.Is(err, errCommunicationExpired) {
		t.Fatalf("deleted output: %v", err)
	}
	if _, err := readCrewFunctionOutput(ctx, call, "never-created.txt", 0, 1024); errors.Is(err, errCommunicationExpired) {
		t.Fatal("unknown output called expired")
	}
}
