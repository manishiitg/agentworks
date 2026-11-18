package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// #3157: exercise the real local bootstrap, helper socket, HTTP admission,
// durable mailbox and live reply. Only provider/model execution is a fixture.
func TestLocalBootstrapNativeRoundTripWithoutServerLogin(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			env := newTriggerLinkEnv(t)
			root := crewOwnerTabsFixture(t, env)
			env.api.agentProfiles = env.svc.registry
			t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
			t.Setenv("MULTI_USER_MODE", "false")
			t.Setenv("DEFAULT_USER_ID", "owner")
			t.Setenv("AUTH_SECRET", "local-bootstrap-fixture-key")
			router := mux.NewRouter()
			router.HandleFunc("/api/external/v1/call", env.api.handleExternalCall).Methods("POST")
			router.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment).Methods("POST")
			router.HandleFunc("/api/external/v1/agents/events", env.api.handleAgentEvents).Methods("GET")
			router.HandleFunc(cliOAuthTokenPath, env.api.handleCLIOAuthToken).Methods("POST")
			server := httptest.NewServer(AuthMiddleware(router))
			defer server.Close()
			t.Setenv("PUBLIC_URL", server.URL)
			home, err := os.MkdirTemp("", "aw-home-") // short: socket paths have a ~104 byte limit
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(home)
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			localAppLogin.family, localAppLogin.access = "", ""
			socketPath, err := agentworksclient.LocalAppSocketPath()
			if err != nil {
				t.Fatal(err)
			}
			socket, err := serveLocalAgentSocket(AuthMiddleware(router), socketPath)
			if err != nil {
				t.Fatal(err)
			}
			defer socket.Close()
			if info, err := os.Stat(socketPath); err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("app socket is not private", err)
			}
			cfg, config, err := agentworksclient.LoadLocalAppConfig()
			if err != nil || cfg.Server != agentworksclient.LocalAppServer || cfg.Token != "" || cfg.RefreshToken != "" {
				t.Fatal("local connection must hold no credential", err)
			}
			// The socket gives the Mac owner's messaging rights only: the login it
			// attaches has no other scope, and other app pages are not served on it.
			store, err := openMCPOAuthStore()
			if err != nil {
				t.Fatal(err)
			}
			issued, err := store.Authenticate(t.Context(), localAppLogin.access)
			store.Close()
			if err != nil || !slices.Equal(issued.Scopes, []string{"messages:read", "messages:send"}) || issued.UserID != "owner" {
				t.Fatal("socket login is not the owner's messaging-only login", issued.Scopes, err)
			}
			socketHTTP := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			}}}
			page, err := socketHTTP.Get(agentworksclient.LocalAppServer + "/api/workflows")
			if err != nil || page.StatusCode != http.StatusNotFound {
				t.Fatal("non-messaging page served on the socket", err)
			}
			page.Body.Close()

			ctx := internalBotRequestContext(t.Context(), "owner")
			profile, err := env.svc.registry.Resolve("work", 0, "owner")
			if err != nil {
				t.Fatal(err)
			}
			binding, err := resolveProductConversationBinding(ctx, "owner", profile, "beta:chat:local-receiver")
			if err != nil {
				t.Fatal(err)
			}
			record, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", profile, binding, "")
			if err != nil {
				t.Fatal(err)
			}
			name := "Local receiver"
			if _, err := defaultProductConversationRegistryStore().setAgentIdentity(ctx, "owner", "work", record.ConversationKey, &name, nil, "owner"); err != nil {
				t.Fatal(err)
			}
			reg := &recordingRegistrar{}
			caller := func(context.Context) (triggerLinkCaller, error) {
				return triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ProfileID: "work", ID: "beta"}, Path: root, Label: name, Chat: &codeChat{OwnerID: "owner", Key: record.ConversationKey, ID: record.ConversationKey, SessionID: record.SessionID}}, nil
			}
			if err := env.api.registerAgentMessagingTools(reg, "owner", record.SessionID, QueryRequest{}, caller, nil); err != nil {
				t.Fatal(err)
			}
			appRequest := make(chan string, 1)
			oldTurn := agentMessageTurn
			agentMessageTurn = func(_ *StreamingAPI, _ context.Context, args map[string]interface{}, session, owner string) (internalSessionTurnResult, error) {
				if session != record.SessionID || owner != "owner" {
					t.Error("request crossed owner/chat")
				}
				appRequest <- stringField(args["agent_message_inbox"])
				return internalSessionTurnResult{}, nil
			}
			t.Cleanup(func() { agentMessageTurn = oldTurn })
			bridgeRoot, err := os.MkdirTemp("", "aw-local-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(bridgeRoot)
			runCtx, cancel := context.WithCancel(t.Context())
			finished := make(chan error, 1)
			offers := make(chan agentworksclient.LocalBridgeReply, 4)
			go func() {
				finished <- agentworksclient.ServeLocalBridge(runCtx, bridgeRoot, agentworksclient.LocalBridgeOptions{HostAlive: func(string, string) bool { return true }, NativeOffer: func(_ context.Context, _ string, out agentworksclient.LocalBridgeReply) error {
					offers <- out
					return nil
				}})
			}()
			defer func() { cancel(); <-finished }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := agentworksclient.CallLocalBridge(runCtx, bridgeRoot, agentworksclient.LocalBridgeRequest{Action: "ping"}); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("helper not ready")
				}
				time.Sleep(10 * time.Millisecond)
			}
			session := "11111111-2222-4333-8444-555555555555"
			remote := "claude+session://" + session
			if provider == "codex" {
				remote = "unix:///fixture-provider.sock"
			}
			base := agentworksclient.LocalBridgeRequest{Provider: provider, SessionID: session, HostKey: "fixture-own-process", AgentName: "local-native", ConfigPath: config, OwnAttach: provider == "codex", NativeRemote: remote}
			base.Action = "attach"
			attached, err := agentworksclient.CallLocalBridge(runCtx, bridgeRoot, base)
			if err != nil || !attached.Attached {
				t.Fatal("local attach failed", err)
			}
			// An app restart reissues the same login family and revokes its older
			// tokens: one live parent per Mac, and attached chats keep working.
			family := localAppLogin.family
			localAppLogin.family, localAppLogin.access = "", ""
			if _, err := localAppAccess(t.Context()); err != nil || localAppLogin.family != family {
				t.Fatal("restart changed the Mac login family", err)
			}
			store, err = openMCPOAuthStore()
			if err != nil {
				t.Fatal(err)
			}
			var live int
			err = store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM tokens WHERE resource=? AND revoked_at IS NULL AND family_id NOT IN (SELECT family_id FROM agent_connections)`, localAppResource).Scan(&live)
			store.Close()
			if err != nil || live != 2 {
				t.Fatal("parent tokens piled up across restarts", live, err)
			}
			base.Action = "message"
			base.Arguments = map[string]any{"action": "ask", "target": bridgeChatAddress("owner", "work", "beta", record.ConversationKey), "message": "local round trip", "submission_id": "local-question"}
			sent, err := agentworksclient.CallLocalBridge(runCtx, bridgeRoot, base)
			if err != nil {
				t.Fatal("native ask failed", err)
			}
			var receipt map[string]any
			if err := json.Unmarshal(sent.Result, &receipt); err != nil {
				t.Fatal(err)
			}
			inbox := stringField(receipt["inbox_id"])
			select {
			case delivered := <-appRequest:
				if delivered != inbox || inbox == "" {
					t.Fatal("wrong app request thread")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("app was not woken")
			}
			raw, err := reg.tools["send_message"].exec(ctx, map[string]interface{}{"inbox_id": inbox, "message": "local reply", "submission_id": "local-answer"})
			if err != nil || decodeToolJSON(t, raw)["inbox_id"] != inbox {
				t.Fatal("app reply changed thread", err)
			}
			select {
			case out := <-offers:
				if len(out.Messages) != 1 {
					t.Fatal("native reply missing")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("native live offer not delivered")
			}
			t.Logf("[AGENTBRIDGE] event=local_contract provider=%s saved_networks=0 request=delivered reply=live_same_thread provider_boundary=fixture", provider)
		})
	}
}
