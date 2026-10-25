package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

func TestAgentWakeEventsRespectAccountAndSenderAccess(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "wake-fixture-secret")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/agents/events", env.api.handleAgentEvents)
	mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
	srv := httptest.NewServer(AuthMiddleware(mux))
	defer srv.Close()
	t.Setenv("PUBLIC_URL", srv.URL)
	issue := func(user string) (*agentworksclient.Client, string, string) {
		t.Helper()
		device, verification, e := store.CreateCLIDevice(context.Background(), "crews:run", "workflows:read", "runs:execute")
		if e != nil {
			t.Fatal(e)
		}
		if e = store.DecideCLIDevice(context.Background(), verification, &UserClaims{UserID: user, Username: user}, true); e != nil {
			t.Fatal(e)
		}
		grant, token, _, e := store.PollCLIDevice(context.Background(), device, srv.URL+"/api/external/v1")
		if e != nil {
			t.Fatal(e)
		}
		c, e := agentworksclient.New(srv.URL, token)
		if e != nil {
			t.Fatal(e)
		}
		return c, mcpOAuthTokenForGrant(grant).ID, token
	}
	call := func(c *agentworksclient.Client, args map[string]any) map[string]any {
		t.Helper()
		raw, e := c.Call(context.Background(), "messages", args)
		if e != nil {
			t.Fatal(e)
		}
		var out map[string]any
		if e = json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	owner, connection, token := issue("owner")
	other, _, _ := issue("other")
	for _, name := range []string{"dormant", "source"} {
		call(owner, map[string]any{"action": "register", "agent_name": name})
	}
	for _, name := range []string{"private-destination", "private-source"} {
		call(other, map[string]any{"action": "register", "agent_name": name})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attachment, err := owner.AttachAgent(ctx, agentworksclient.AgentAttachmentOptions{AgentName: "dormant", Provider: "codex", SessionID: "saved-native-session"})
	if err != nil {
		t.Fatal(err)
	}
	childConfig, err := owner.CreateAgentConnection(ctx, "dormant")
	if err != nil {
		t.Fatal(err)
	}
	child, err := agentworksclient.New(srv.URL, childConfig.Token)
	if err != nil {
		t.Fatal(err)
	}
	boundCtx, stopBound := context.WithTimeout(ctx, time.Second)
	defer stopBound()
	err = child.StreamAgentWakeHints(boundCtx, func(agentworksclient.AgentLiveMessage) error {
		t.Fatal("bound chat read an account stream")
		return nil
	})
	var apiError *agentworksclient.APIError
	if !errors.As(err, &apiError) || apiError.Status != 403 {
		t.Fatalf("bound connection: %v", err)
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/external/v1/agents/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() || !strings.Contains(scanner.Text(), `"connected"`) {
		t.Fatal("no account connection frame")
	}
	frames := make(chan map[string]any, 8)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var frame map[string]any
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame) == nil {
					frames <- frame
				}
			}
		}
	}()
	call(other, map[string]any{"action": "send", "agent_name": "private-source", "target": "agent:private-destination", "message": "other person's private body", "submission_id": "foreign-private"})
	select {
	case frame := <-frames:
		t.Fatalf("other account mail emitted metadata: %v", frame)
	case <-time.After(100 * time.Millisecond):
	}
	call(owner, map[string]any{"action": "send", "agent_name": "source", "target": "agent:dormant", "message": "permitted private body", "submission_id": "own-wake"})
	select {
	case frame := <-frames:
		if len(frame) != 4 || frame["type"] != "inbox_changed" || frame["agent_id"] != attachment.AgentID || frame["attachment_id"] != attachment.ID || frame["sequence"].(float64) <= 0 {
			t.Fatalf("wrong or excess wake metadata: %v", frame)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no permitted wake")
	}
	if err = store.RevokeFamily(ctx, strings.TrimPrefix(connection, "oauth-"), "owner"); err != nil {
		t.Fatal(err)
	}
	notifyAgentLiveChanged()
	select {
	case frame := <-frames:
		if frame["type"] != "login_changed" {
			t.Fatalf("revoked stream still released metadata: %v", frame)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revoked account stream remained open")
	}
	cancel()
	response.Body.Close()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("account reader did not stop")
	}
}
