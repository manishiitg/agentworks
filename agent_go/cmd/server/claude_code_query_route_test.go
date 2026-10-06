package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/clisecurity"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Exercise the actual Code query path past the former HTTP 400 and capture its
// finalized agent before the first model request. Single-product policy keeps
// the authentication gate required even on this test's unconfined Mac host.
func TestCodeQueryPreparesAdmittedPrivateClaudeLogin(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENT_PRODUCTS", "code")
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Private Claude", "auth_method": "cli_login"})
	home, err := providerConnectionHome(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"private-dummy"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	origOS := cliHostOS
	cliHostOS = "darwin"
	t.Cleanup(func() { cliHostOS = origOS })
	t.Setenv("MULTI_USER_MODE", "false")
	if err := codeproduct.RegisterProductSkills(); err != nil {
		t.Fatal(err)
	}
	const root = "_users/alice/Chats/Code/projects/site"
	env.mock.files[root+"/product.json"] = `{"schema_version":1,"product":"code","id":"site","title":"Site","session_id":"code:site"}`
	env.mock.files[root+"/workflow.json"] = `{"capabilities":{}}`
	registry := agentprofiles.NewRegistry()
	for _, profile := range codeproduct.BuiltinAgentProfiles() {
		profile.Product = codeproduct.ProfileID
		if err := registry.RegisterProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	if err := codeproduct.RegisterAgentProfileRuntime(registry, getWorkspaceAPIURL()); err != nil {
		t.Fatal(err)
	}
	env.api.agentProfiles = registry
	env.api.eventStore = events.NewEventStore(100)
	env.api.logger = loggerv2.NewNoop()
	env.api.activeSessions = make(map[string]*ActiveSessionInfo)
	env.api.lastQueryRequests = make(map[string]QueryRequest)
	env.api.cliSecurityStore, err = clisecurity.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env.api.mcpConfigPath = filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(env.api.mcpConfigPath, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	env.api.agentCancelFuncs = make(map[string]context.CancelFunc)
	env.api.sessionQueryIDs = make(map[string][]string)
	env.api.conversationHistory = make(map[string][]llmtypes.MessageContent)
	prepared := make(chan string, 1)
	env.api.internalPreparedAgent = func(_ context.Context, agent *mcpagent.Agent) bool {
		prepared <- string(mcpagent.ReadAgentRuntimeInfo(agent).Provider)
		return true
	}
	req := QueryRequest{Query: "Inspect Site", Provider: "claude-code", ModelID: "sonnet", ConnectionID: account.ID, AgentMode: "multi-agent", AgentProfileID: codeproduct.ProfileID, AgentProfileConversationKey: "site", SelectedFolder: "Chats/Code/projects/site", AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Site"}}
	w := httptest.NewRecorder()
	httpReq := sharedSecretsRequest(http.MethodPost, "/api/query", "alice", req)
	httpReq.Header.Set("X-Session-ID", "code:site")
	env.api.handleQuery(w, httpReq)
	select {
	case provider := <-prepared:
		if provider != "claude-code" {
			t.Fatalf("prepared provider %q", provider)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("query did not reach agent assembly: status=%d body=%s events=%v", w.Code, w.Body.String(), env.api.eventStore.GetAllEventsRaw("code:site"))
	}
	deadline := time.Now().Add(5 * time.Second)
	for env.api.isSessionBusy("code:site") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if env.api.isSessionBusy("code:site") {
		t.Fatal("prepared query did not stop")
	}
}
