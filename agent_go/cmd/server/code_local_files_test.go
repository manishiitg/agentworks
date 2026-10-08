package server

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/mcpagent/mcpclient"
)

func TestCodeLocalFilesDisableServerProductFeaturesAndTools(t *testing.T) {
	serverProfile := codeproduct.BuiltinAgentProfile()
	localProfile := serverProfile
	blockedSkills, blockedTools := restrictCodeLocalFeatures(&localProfile)
	for _, feature := range []string{"dashboard", "database", "schedules", "triggers", "bots", "mcp", "skills", "secrets", "background-work", "browser", "terminal", "attached-folders", "workflow-references", "knowledgebase"} {
		if agentprofiles.HasFeature(localProfile, feature) || !agentprofiles.HasFeature(serverProfile, feature) {
			t.Fatalf("local feature %s was not narrowed independently", feature)
		}
	}
	for _, feature := range []string{"files", "models", "costs", "workspace-ui", "live-chat"} {
		if !agentprofiles.HasFeature(localProfile, feature) {
			t.Fatalf("coding feature %s was removed", feature)
		}
	}
	if !blockedSkills["code-dashboard"] || !blockedSkills["code-schedules-and-bots"] {
		t.Fatalf("disabled feature guidance retained: %v", blockedSkills)
	}
	if !reflect.DeepEqual(codeproduct.BuiltinAgentProfile(), serverProfile) {
		t.Fatal("local policy mutated the reusable Code profile")
	}
	resolved := &resolvedAgentProfile{Definition: localProfile, CodeLocalFiles: &codeLocalFileTarget{DeviceID: "offline-laptop", ResourceID: "project"}, CodeLocalDisabledTools: blockedTools}
	gate := newProductToolGate(resolved)
	for _, tool := range []string{"create_project_schedule", "create_project_trigger", "preview_report", "query_workflow_db", "get_report_link", "google_workspace_cli", "configure_slack_bot", "slack", "list_gmail_connections", "manage_gmail_trigger", "manage_my_mcp_servers", "list_skills", "update_project_skill_selection", "list_secrets", "read_skill", "run_in_background", "delegate", "list_local_devices", "list_local_files", "read_local_file", "write_local_file", "execute_local_shell_command", "arbitrary_external_mcp_tool"} {
		gate.Declare(tool) // Another registration path must not restore it.
		if gate.Admit(tool) {
			t.Fatalf("disabled local-mode tool %s was admitted", tool)
		}
	}
	for _, tool := range []string{"execute_shell_command", "diff_patch_workspace_file", "list_ui_capabilities", "get_ui_state", "perform_ui_action"} {
		gate.Declare(tool)
		if !gate.Allows(tool) {
			t.Fatalf("coding tool %s was disabled", tool)
		}
	}
}

func TestCodeLocalFilesUIActionsCannotOpenDisabledViews(t *testing.T) {
	api := &StreamingAPI{}
	reg := &recordingRegistrar{}
	if err := api.registerOpenWorkWorkspaceViewTool(reg, "owner", "local-code", "Chats/Code/projects/demo", true); err != nil {
		t.Fatal(err)
	}
	capabilities, err := reg.tools["list_ui_capabilities"].exec(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{"report", "database", "workshop", "schedules", "bots", "email", "browser", "skills", "secrets", "mcp", "folders", "identity"} {
		if strings.Contains(capabilities, `"id":"`+view+`"`) {
			t.Fatalf("disabled view %s advertised", view)
		}
		out, err := reg.tools["perform_ui_action"].exec(context.Background(), map[string]interface{}{"view": view, "action": "open"})
		if err != nil || !strings.Contains(out, "unsupported_view") {
			t.Fatalf("disabled view %s accepted: %s %v", view, out, err)
		}
	}
	if !strings.Contains(capabilities, `"id":"files"`) || !strings.Contains(capabilities, `"id":"costs"`) || !strings.Contains(capabilities, `"id":"llm"`) {
		t.Fatal("local UI lost coding views")
	}
	if !validUIViewForContract(codeUIControlContract, "report") {
		t.Fatal("local UI policy mutated the normal Code contract")
	}
}

func TestCodeLocalFilesResolveNarrowsSavedSessionAndDisconnectRestoresFeatures(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	const root = "_users/alice/Chats/Code/projects/site"
	manifest := `{"schema_version":1,"product":"code","id":"site","title":"Site","session_id":"code:site"}`
	env.mock.mu.Lock()
	env.mock.files[root+"/product.json"] = manifest
	env.mock.files[root+"/workflow.json"] = `{"capabilities":{}}`
	env.mock.mu.Unlock()
	registry := agentprofiles.NewRegistry()
	profile := codeproduct.BuiltinAgentProfile()
	profile.Product = codeproduct.ProfileID
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	env.api.agentProfiles = registry
	for _, mode := range []string{"local", "local-bound", "server"} {
		local := mode != "server"
		req := QueryRequest{
			AgentMode: "multi-agent", AgentProfileID: codeproduct.ProfileID,
			AgentProfileConversationKey: "site", SelectedFolder: "Chats/Code/projects/site",
			AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Site"},
			SelectedSkills:      []string{"code-dashboard", "code-schedules-and-bots", "custom-coding-skill"},
			EnabledServers:      []string{"saved-mcp"}, Servers: []string{"fallback-mcp"},
		}
		if local {
			req.CodeChatMode = "local"
			globals := []string{"saved-global-secret"}
			req.SelectedGlobalSecrets = &globals
			req.DecryptedSecrets = append(req.DecryptedSecrets, struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}{Name: "saved-project-secret", Value: "test-value"})
			if mode == "local-bound" {
				req.CodeLocalFiles = &codeLocalFileTarget{DeviceID: "offline-laptop", ResourceID: "project"}
			}
		}
		resolved, err := env.api.resolveAgentProfileForQuery(context.Background(), &req, "alice", "code:site")
		if err != nil {
			t.Fatal(err)
		}
		for _, feature := range []string{"dashboard", "schedules", "triggers", "bots", "mcp", "skills", "secrets", "background-work"} {
			if agentprofiles.HasFeature(resolved.Definition, feature) == local {
				t.Fatalf("feature %s available=%v local=%v", feature, agentprofiles.HasFeature(resolved.Definition, feature), local)
			}
		}
		if local {
			if len(req.SelectedSkills) != 0 || len(resolved.Definition.Skills) != 0 || len(req.DecryptedSecrets) != 0 || req.SelectedGlobalSecrets == nil || len(*req.SelectedGlobalSecrets) != 0 || len(resolved.ChatSecrets) != 0 {
				t.Fatal("local turn retained skills or secrets")
			}
			if !reflect.DeepEqual(req.EnabledServers, []string{mcpclient.NoServers}) || len(req.Servers) != 0 || len(resolved.ChatConnections) != 0 || !reflect.DeepEqual(resolved.SelectedServers, []string{mcpclient.NoServers}) {
				t.Fatal("local turn retained an MCP selection")
			}
			if agentProfileToolsMode(resolved) != "mcp_only" || resolved.Definition.Runtime.Capabilities.Secrets != agentprofiles.CapabilityDisabled || req.BrowserMode != "none" || req.EnableBrowserAccess == nil || *req.EnableBrowserAccess {
				t.Fatal("local turn retained native server tools or browser/secret capabilities")
			}
		} else if agentProfileToolsMode(resolved) != "full" || !strings.Contains(strings.Join(req.SelectedSkills, ","), "custom-coding-skill") {
			t.Fatal("disconnect did not restore the normal Code tool/skill policy")
		}
		if req.SelectedFolder != "Chats/Code/projects/site" {
			t.Fatal("local mode changed the server runtime folder")
		}
	}
	env.mock.mu.Lock()
	defer env.mock.mu.Unlock()
	if env.mock.files[root+"/product.json"] != manifest {
		t.Fatal("local session changed the server project configuration")
	}
}

func TestCodeLocalFilesOfflineSelectionDoesNotBlockOrdinaryChat(t *testing.T) {
	api := &StreamingAPI{}
	target := &codeLocalFileTarget{DeviceID: "offline-laptop", ResourceID: "project"}
	if err := api.validateCodeLocalFiles(&UserClaims{UserID: "owner"}, target); err != nil {
		t.Fatalf("offline selection blocked chat: %v", err)
	}
	if err := api.validateCodeLocalFiles(&UserClaims{UserID: "owner", Provider: "bot_route"}, target); err == nil {
		t.Fatal("connector acquired local file context")
	}
	profile := routeTestProfile("code", true, "")
	conversation := ProductConversationRecord{SessionID: "same-session", WorkspacePath: "Chats/Code/projects/project"}
	baseline, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hello"}, conversation)
	if err != nil {
		t.Fatal(err)
	}
	connected, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hello", CodeLocalFiles: target}, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.RestoredConversationSessionID != connected.RestoredConversationSessionID || baseline.Provider != connected.Provider || baseline.ModelID != connected.ModelID || baseline.SelectedFolder != connected.SelectedFolder {
		t.Fatal("file binding changed the chat or server runtime")
	}
}

func TestCodeLocalFilesRequireInteractiveCodeAndReplaceRetainedToolBinding(t *testing.T) {
	profile := routeTestProfile("code", true, "")
	target := &codeLocalFileTarget{DeviceID: "laptop", ResourceID: "project"}
	conversation := ProductConversationRecord{SessionID: "session", WorkspacePath: "Chats/Code/projects/project"}
	query, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "read my code", CodeLocalFiles: target}, conversation)
	if err != nil || query.CodeLocalFiles != target {
		t.Fatalf("Code selection lost: %+v %v", query, err)
	}
	resolved := &resolvedAgentProfile{Definition: profile}
	if !codeLocalFileTurn(query, resolved) {
		t.Fatal("interactive Code denied")
	}
	serverKey := agentProfileSessionKey(resolved)
	resolved.CodeLocalFiles = target
	localKey := agentProfileSessionKey(resolved)
	if serverKey == localKey {
		t.Fatal("retained server tools reused for local folder")
	}
	resolved.CodeLocalFiles = &codeLocalFileTarget{DeviceID: "laptop", ResourceID: "different-project"}
	if localKey == agentProfileSessionKey(resolved) {
		t.Fatal("retained folder binding was reused")
	}
	beforePolicy := agentProfileSessionKey(resolved)
	resolved.CodeLocalFilePolicy = "read-only grant"
	if beforePolicy == agentProfileSessionKey(resolved) {
		t.Fatal("changed local permissions reused retained tools")
	}
	for _, kind := range []string{"cron", "webhook", "chat_tool"} {
		query.TriggeredBy = kind
		if codeLocalFileTurn(query, resolved) {
			t.Fatalf("automated %s acquired local tools", kind)
		}
	}
	query.TriggeredBy = "interactive"
	if !codeLocalFileTurn(query, resolved) {
		t.Fatal("website continuation denied")
	}
	query.BotPlatform = "slack"
	if codeLocalFileTurn(query, resolved) {
		t.Fatal("connector acquired local tools")
	}
	for _, id := range []string{"work", "knowledgebase", "agentworks"} {
		profile.ID = id
		if _, err = queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "read", CodeLocalFiles: target}, conversation); err == nil {
			t.Fatalf("%s accepted local Code hint", id)
		}
	}
	profile.ID = "code"
	if _, err = queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "read", CodeLocalFiles: &codeLocalFileTarget{DeviceID: "../escape", ResourceID: "project"}}, conversation); err == nil {
		t.Fatal("invalid device accepted")
	}
}

func TestCodeLocalModeBeforeFolderSelectionHasMinimalPolicy(t *testing.T) {
	profile := routeTestProfile("code", true, "")
	conversation := ProductConversationRecord{SessionID: "session", WorkspacePath: "Chats/Code/projects/project"}
	query, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hello", CodeChatMode: "local"}, conversation)
	if err != nil || query.CodeChatMode != "local" || query.CodeLocalFiles != nil {
		t.Fatalf("local setup mode lost: %+v %v", query, err)
	}
	resolved := &resolvedAgentProfile{Definition: profile, CodeChatMode: "local"}
	if !codeLocalModeTurn(query, resolved) || codeLocalFileTurn(query, resolved) {
		t.Fatal("setup mode acquired file tools without a folder")
	}
	gate := newProductToolGate(resolved)
	for _, tool := range []string{"list_skills", "list_secrets", "manage_my_mcp_servers", "run_in_background", "execute_shell_command", "read_workspace_file"} {
		gate.Declare(tool)
		if gate.Admit(tool) {
			t.Fatalf("setup mode admitted %s", tool)
		}
	}
	if !strings.Contains(codeLocalFilesInstructions(nil), "no folder selected") {
		t.Fatal("missing setup instructions")
	}
	for _, input := range []AgentProfileChatRequest{
		{CodeChatMode: "unknown"},
		{CodeChatMode: "server", CodeLocalFiles: &codeLocalFileTarget{DeviceID: "laptop", ResourceID: "project"}},
	} {
		if _, err := queryRequestForAgentProfileChat(profile, input, conversation); err == nil {
			t.Fatal("invalid mode accepted")
		}
	}
}
