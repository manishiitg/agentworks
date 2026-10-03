package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// Drive the production query handler all the way through FinalizeDefinition.
// Only external state is mocked. The capture seam stops before the first turn,
// so no CLI process, model request or business tool executes.
func TestCodePreparedSystemPrompt(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	// Pin the platform: a person's own Mac, where coding CLIs run Full CLI unconfined.
	origOS := cliHostOS
	cliHostOS = "darwin"
	t.Cleanup(func() { cliHostOS = origOS })
	t.Setenv("MULTI_USER_MODE", "false")
	if err := codeproduct.RegisterProductSkills(); err != nil {
		t.Fatal(err)
	}
	const codeRoot = "_users/alice/Chats/Code/projects/site"
	env.mock.files[codeRoot+"/product.json"] = `{"schema_version":1,"product":"code","id":"site","title":"Site","session_id":"code:site"}`
	env.mock.files[codeRoot+"/workflow.json"] = `{"capabilities":{}}`
	env.mock.files["Chats/Code/projects/site/skills/code-reviewer/SKILL.md"] = "---\nname: code-reviewer\ndescription: Review Site changes and report concrete issues\n---\nRead relevant files and check requested behavior before reporting findings.\n"
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
	var err error
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
	captured := make(chan struct {
		prompt     string
		definition mcpagent.AgentDefinitionView
		provider   string
	}, 1)
	env.api.internalPreparedAgent = func(ctx context.Context, agent *mcpagent.Agent) bool {
		captured <- struct {
			prompt     string
			definition mcpagent.AgentDefinitionView
			provider   string
		}{mcpagent.ReadAgentSystemPrompt(ctx, agent), agent.Definition(), string(mcpagent.ReadAgentRuntimeInfo(agent).Provider)}
		return true
	}
	req := QueryRequest{Query: "Inspect the Site project", AgentMode: "multi-agent", AgentProfileID: codeproduct.ProfileID, AgentProfileConversationKey: "site", SelectedFolder: "Chats/Code/projects/site", AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Site"}}
	w := httptest.NewRecorder()
	httpReq := sharedSecretsRequest(http.MethodPost, "/api/query", "alice", req)
	httpReq.Header.Set("X-Session-ID", "code:site")
	env.api.handleQuery(w, httpReq)
	var result struct {
		prompt     string
		definition mcpagent.AgentDefinitionView
		provider   string
	}
	select {
	case result = <-captured:
	case <-time.After(15 * time.Second):
		t.Fatalf("query did not reach finalized prompt: status=%d body=%s events=%v", w.Code, w.Body.String(), env.api.eventStore.GetAllEventsRaw("code:site"))
	}
	// Wait for the detached handler's cleanup before closing mocked services.
	deadline := time.Now().Add(5 * time.Second)
	for env.api.isSessionBusy("code:site") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if env.api.isSessionBusy("code:site") {
		t.Fatal("prepared query did not stop")
	}
	prompt, view := result.prompt, result.definition
	loadedReviewer, registeredProjectTool := false, false
	for _, skill := range view.SkillDefinitions {
		if skill.Name == "code-reviewer" && strings.Contains(skill.Content, "check requested behavior") {
			loadedReviewer = true
		}
	}
	for _, tool := range view.Tools {
		if tool.Name == "create_code_workspace" {
			registeredProjectTool = true
		}
	}
	if !loadedReviewer || !registeredProjectTool {
		t.Fatal("query skipped project skill loading or product tool registration")
	}
	if strings.Contains(prompt, "builder-reference") || !strings.Contains(prompt, "attached `agent-browser` skill") {
		t.Fatal("browser pointer targets an unavailable skill")
	}
	if !strings.Contains(prompt, "native read-only tools") {
		t.Fatal("Code lost its resolved native-tool mode")
	}
	if !strings.Contains(prompt, "runtime-http-tools") || !strings.Contains(prompt, "project-memory") {
		t.Fatal("missing procedure discovery")
	}
	if strings.Contains(prompt, "<available_tools>") {
		t.Fatal("redundant discovery catalog")
	}
	for _, skill := range view.SkillDefinitions {
		if len(skill.Content) > 500 && strings.Contains(prompt, skill.Content) {
			t.Fatalf("inlined skill %s", skill.Name)
		}
	}
	if outDir := os.Getenv("PROMPT_SNAPSHOT_DIR"); outDir != "" {
		if !filepath.IsAbs(outDir) {
			t.Fatal("PROMPT_SNAPSHOT_DIR must be absolute")
		}
		if err := os.MkdirAll(outDir, 0700); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(outDir)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		write := func(name string, content []byte) {
			if err := root.WriteFile(name, content, 0600); err != nil {
				t.Fatal(err)
			}
		}
		write("code.system.md", []byte(prompt))
		raw, err := json.MarshalIndent(view.SkillDefinitions, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		write("code.skills.json", raw)
		raw, err = json.MarshalIndent(map[string]interface{}{"source": "handleQuery -> FinalizeDefinition -> ReadAgentSystemPrompt", "provider": result.provider, "native_tools": "hybrid", "system_bytes": len(prompt), "system_characters": len([]rune(prompt)), "skills": view.Skills, "tools": view.Tools}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		write("summary.json", raw)
		write("README.md", []byte("# Code prompt captured from the production query path\n\nGenerated by TestCodePreparedSystemPrompt. The real handleQuery resolves the Code profile, project access, native-tool mode, tool registration, skills and runtime, then FinalizeDefinition builds the agent. ReadAgentSystemPrompt uses the same outbound composer as model requests. Capture stops before a turn starts; no model call is made.\n\nThe workspace API, Alice's user record, Site project and credentials are test fixtures. Paths and time are resolved dynamically by the handler. This is the exact system text for these inputs, not a capture of a live user's session. Attached skill bodies are in code.skills.json. Provider-owned instructions, native skill indexes, tool schemas and history are separate from this system string.\n"))
		t.Logf("captured Code prompt: %d characters, %d skills, %s", len([]rune(prompt)), len(view.Skills), filepath.Join(outDir, "code.system.md"))
	}
}
