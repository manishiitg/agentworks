package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// A real issued sign-in outlives its access token for existing streams and mail,
// but cannot use the expired bearer for a new HTTP request or survive sign-out.
func TestAgentSignedInFamilyOutlivesAccess(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "signed-in-delivery-fixture-secret")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
	srv := httptest.NewServer(AuthMiddleware(mux))
	defer srv.Close()
	t.Setenv("PUBLIC_URL", srv.URL)
	ctx := context.Background()
	device, verification, err := store.CreateCLIDevice(ctx, "crews:run", "runs:execute")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.DecideCLIDevice(ctx, verification, &UserClaims{UserID: "owner", Username: "owner"}, true); err != nil {
		t.Fatal(err)
	}
	parent, bearer, _, err := store.PollCLIDevice(ctx, device, srv.URL+"/api/external/v1")
	if err != nil {
		t.Fatal(err)
	}
	client, _ := agentworksclient.New(srv.URL, bearer)
	if _, err = client.Call(ctx, "messages", map[string]any{"action": "register", "agent_name": "worker"}); err != nil {
		t.Fatal(err)
	}
	childConfig, err := client.CreateAgentConnection(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	child, _ := agentworksclient.New(srv.URL, childConfig.Token)
	lease, err := child.AttachAgent(ctx, agentworksclient.AgentAttachmentOptions{AgentName: "worker", Provider: "codex", SessionID: "exact-session"})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.Authenticate(ctx, childConfig.Token)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := accessTokenClaims(mcpOAuthTokenForGrant(grant))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE tokens SET expires_at=? WHERE kind='access' AND family_id IN (?,?)`, time.Now().Add(-time.Minute).Unix(), parent.FamilyID, grant.FamilyID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = currentAgentAttachment(ctx, claims, "worker", lease.ID); err != nil {
		t.Fatal("access expiry ended a signed-in exact lease", err)
	}
	if _, err = agentMessageConnectionClaims(ctx, "oauth-"+parent.FamilyID); err != nil {
		t.Fatal("idle sender sign-in unavailable", err)
	}
	if _, err = child.Call(ctx, "messages", map[string]any{"action": "list_agents"}); err == nil {
		t.Fatal("expired HTTP bearer accepted")
	}
	mail, err := readAgentMessageStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range mail.Agents {
		if mail.Agents[i].Name == "worker" {
			mail.Agents[i].Connections = nil
		}
	}
	if err = saveAgentMessageStore(ctx, mail); err != nil {
		t.Fatal(err)
	}
	if _, _, err = currentAgentAttachment(ctx, claims, "worker", lease.ID); err == nil {
		t.Fatal("removed allowed connection remained authorized")
	}
	if _, err = store.db.Exec(`UPDATE tokens SET expires_at=? WHERE family_id=?`, time.Now().Add(-time.Minute).Unix(), parent.FamilyID); err != nil {
		t.Fatal(err)
	}
	if _, err = agentMessageConnectionClaims(ctx, "oauth-"+parent.FamilyID); err == nil {
		t.Fatal("expired sign-in accepted")
	}
	if _, err = store.db.Exec(`UPDATE tokens SET expires_at=?,revoked_at=? WHERE family_id=?`, time.Now().Add(time.Hour).Unix(), time.Now().Unix(), parent.FamilyID); err != nil {
		t.Fatal(err)
	}
	if _, err = agentMessageConnectionClaims(ctx, "oauth-"+parent.FamilyID); err == nil {
		t.Fatal("disconnected sign-in accepted")
	}
}

func TestAgentPresenceDoesNotQueueBehindDelivery(t *testing.T) {
	agentMessageMu.Lock()
	done := make(chan struct{})
	go func() {
		recordNamedAgentActivity(context.Background(), "fixture-agent", "fixture-lease", true)
		close(done)
	}()
	select {
	case <-done:
		agentMessageMu.Unlock()
	case <-time.After(time.Second):
		agentMessageMu.Unlock()
		<-done
		t.Fatal("presence waited behind message I/O")
	}
}
