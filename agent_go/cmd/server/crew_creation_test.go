package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func newCrewCreationTestEnv(t *testing.T) (*ProductScheduleService, *mockWorkspaceAPI, context.Context) {
	t.Helper()
	return newCrewCreationTestEnvWithOptions(t, nil)
}

func registerWorkCrewProfile(t *testing.T, registry *agentprofiles.Registry) {
	t.Helper()
	if err := registry.RegisterProfile(workCrewTestProfile(nil)); err != nil {
		t.Fatal(err)
	}
}

func workCrewTestProfile(providerOptions []agentprofiles.ProviderOption) agentprofiles.Profile {
	return agentprofiles.Profile{
		ID: "work", Name: "Work", Version: 2, SystemPromptTemplate: "hi", BuiltIn: true, Product: "work",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:       "auto",
			Conversation:    agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace:       agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: "Chats/Work/projects"},
			ProviderOptions: providerOptions,
		},
		Features: []agentprofiles.FeatureBinding{{ID: "triggers"}},
		UIPanels: agentprofiles.UIPanels{Schedules: true},
	}
}

func newCrewCreationTestEnvWithOptions(t *testing.T, providerOptions []agentprofiles.ProviderOption) (*ProductScheduleService, *mockWorkspaceAPI, context.Context) {
	t.Helper()
	ensureProjectStateRoot(t)
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true}]}`)
	registry := agentprofiles.NewRegistry()
	profile := workCrewTestProfile(providerOptions)
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	svc := NewProductScheduleService(nil, registry)
	// Hermetic availability fixtures: github is connected, gitlab is
	// configured but disconnected, SHARED_GH is global, and the creating
	// workflow holds no scoped secrets.
	svc.crewAvailability = &crewCreationAvailability{
		MCPServer: func(name string) (string, bool, error) {
			switch name {
			case "github":
				return "github", true, nil
			case "gitlab":
				return "gitlab", false, nil
			default:
				return "", false, fmt.Errorf("MCP server %q is not configured; use list_mcp_servers or search_mcp_catalog first", name)
			}
		},
		ScopedSecrets: func(context.Context, string, string) (map[string]bool, error) {
			return map[string]bool{}, nil
		},
		GlobalSecrets: func() map[string]bool {
			return map[string]bool{"SHARED_GH": true}
		},
	}
	manifest := NewWorkflowManifest("Builder pipeline")
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{manifestPath("Workflow/build"): string(manifestRaw)}}
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	return svc, mock, ctx
}

func TestCreateCrewProjectWritesUILayout(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose:         "Own release quality",
		StepInstruction: "Review the release.", IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.CrewID == "" || created.SessionID != "work:project:"+created.CrewID {
		t.Fatalf("crew identity = %+v", created)
	}
	if !strings.HasPrefix(created.WorkspacePath, "Crew/release-reviewer-") {
		t.Fatalf("workspace = %q", created.WorkspacePath)
	}
	productRaw, ok := mock.files[created.WorkspacePath+"/product.json"]
	if !ok {
		t.Fatal("product.json was not written")
	}
	var product map[string]interface{}
	if err := json.Unmarshal([]byte(productRaw), &product); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema_version", "product", "id", "title", "description", "session_id", "created_at", "updated_at", "identity"} {
		if _, ok := product[key]; !ok {
			t.Fatalf("product.json missing %q: %v", key, product)
		}
	}
	if product["product"] != "work" || product["id"] != created.CrewID || product["title"] != "Release Reviewer" {
		t.Fatalf("product.json = %v", product)
	}
	// Purpose is the Crew's whole identity statement: it fills the stored
	// description slot the Identity panel reads (issue 205, BUG_ID_003).
	if product["description"] != "Own release quality" {
		t.Fatalf("product.json description = %v, want the purpose", product["description"])
	}
	identity, _ := product["identity"].(map[string]interface{})
	if _, hasRole := identity["role"]; hasRole || identity["name"] == "" {
		t.Fatalf("product.json identity = %v, want a name and no role", product["identity"])
	}
	runtimeRaw, ok := mock.files[created.WorkspacePath+"/workflow.json"]
	if !ok {
		t.Fatal("workflow.json was not written")
	}
	var runtime map[string]interface{}
	if err := json.Unmarshal([]byte(runtimeRaw), &runtime); err != nil {
		t.Fatal(err)
	}
	caps, _ := runtime["capabilities"].(map[string]interface{})
	// The wiring trigger save round-trips the manifest through the Go
	// struct, so empty plain-slice selections serialize as absent (the same
	// as any trigger save); populated selections and scalar defaults
	// survive, and pointer-held secret lists keep their explicit empties.
	if caps["browser_mode"] != "auto" {
		t.Fatalf("browser_mode = %v, want auto", caps["browser_mode"])
	}
	for _, key := range []string{"selected_secrets", "selected_global_secret_names"} {
		raw, _ := caps[key].([]interface{})
		if len(raw) != 0 {
			t.Fatalf("%s = %v, want explicit empty", key, raw)
		}
	}
	for _, key := range []string{"selected_servers", "selected_skills", "selected_tools", "use_code_execution_mode"} {
		if raw, ok := caps[key]; ok && fmt.Sprint(raw) != "[]" && fmt.Sprint(raw) != "false" && fmt.Sprint(raw) != "" {
			t.Fatalf("%s = %v, want absent or empty", key, raw)
		}
	}
	contexts, _ := runtime["workflow_context_paths"].([]interface{})
	if len(contexts) != 1 || contexts[0] != "Workflow/build" {
		t.Fatalf("workflow_context_paths = %v, want the creating workflow", contexts)
	}
	if !mock.hasFolder(created.WorkspacePath + "/code") {
		t.Fatal("code/ folder was not created")
	}
}

func TestCreateCrewProjectIsIdempotent(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	req := CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	}
	first, err := svc.CreateCrewProject(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Duplicate {
		t.Fatal("first creation reported duplicate")
	}
	filesAfterFirst := len(mock.files)
	second, err := svc.CreateCrewProject(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate || second.CrewID != first.CrewID || second.WorkspacePath != first.WorkspacePath {
		t.Fatalf("retry = %+v, want the original crew %+v", second, first)
	}
	if len(mock.files) != filesAfterFirst {
		t.Fatal("retry created additional files")
	}
	// A new key mints a distinct crew; it needs its own title because
	// attachment aliases are unique per workflow.
	third, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer Two",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if third.Duplicate || third.CrewID == first.CrewID || third.AttachmentAlias == first.AttachmentAlias {
		t.Fatalf("new key = %+v, want a distinct crew", third)
	}
}

func TestCreateCrewProjectValidatesInput(t *testing.T) {
	svc, _, ctx := newCrewCreationTestEnv(t)
	valid := CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	}
	for name, mutate := range map[string]func(*CreateCrewRequest){
		"empty title":              func(r *CreateCrewRequest) { r.Title = " " },
		"long title":               func(r *CreateCrewRequest) { r.Title = strings.Repeat("a", 61) },
		"long purpose":             func(r *CreateCrewRequest) { r.Purpose = strings.Repeat("a", 2001) },
		"long icon":                func(r *CreateCrewRequest) { r.Icon = strings.Repeat("a", 9) },
		"empty key":                func(r *CreateCrewRequest) { r.IdempotencyKey = "" },
		"bad workflow":             func(r *CreateCrewRequest) { r.WorkflowPath = "Chats/other" },
		"other profile":            func(r *CreateCrewRequest) { r.ProfileID = "crewx" },
		"missing step instruction": func(r *CreateCrewRequest) { r.StepInstruction = " " },
		"missing trigger text": func(r *CreateCrewRequest) {
			r.Purpose, r.Instructions, r.TriggerMessage = "", "", ""
		},
	} {
		req := valid
		mutate(&req)
		if _, err := svc.CreateCrewProject(ctx, req); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
	if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/missing", Title: "Ghost",
		IdempotencyKey: "proposal-1",
	}); err == nil {
		t.Fatal("missing workflow: expected rejection")
	}
	// The purpose is optional. Instructions provide the existing trigger
	// fallback; the required workflow step instruction remains present.
	optional := valid
	optional.Purpose, optional.Instructions = "", "Review the release."
	if _, err := svc.CreateCrewProject(ctx, optional); err != nil {
		t.Fatalf("optional purpose with trigger instructions: %v", err)
	}
}

func TestCreateCrewRejectsProductDeniedBeforeWriting(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	t.Setenv("AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES", "work")
	before := len(mock.files)
	_, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Search Researcher",
		Purpose:         "Research site opportunities",
		StepInstruction: "Return opportunities", IdempotencyKey: "denied-product-proposal",
	})
	if err == nil {
		t.Error("non-admin created a Crew in an admin-only product")
	}
	if len(mock.files) != before {
		t.Errorf("denied creation wrote %d files", len(mock.files)-before)
	}
}

func TestCreateCrewProjectAvoidsOccupiedPath(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	slug := slugifyCrewTitle("Release Reviewer")
	occupied := "Crew/" + slug + "-" + crewCreationSuffix("proposal-1")
	mock.files[occupied+"/product.json"] = `{"schema_version":1,"product":"work","id":"someone-else","title":"Other","session_id":"work:project:someone-else"}`
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Duplicate || created.WorkspacePath == occupied || created.CrewID == "someone-else" {
		t.Fatalf("collision = %+v, want a fresh crew elsewhere", created)
	}
	if _, err := uuid.Parse(created.CrewID); err != nil {
		t.Fatalf("fallback id = %q, want a uuid: %v", created.CrewID, err)
	}
}

func TestCreateCrewProjectSeedsStarterAndSelections(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", Instructions: "Check the changelog first.",
		Skills: []string{"agent-browser"}, Servers: []string{"github"},
		Secrets: []string{"SHARED_GH"}, GlobalSecrets: []string{"SHARED_GH"},
		StepInstruction: "Review the release.", IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	memory, ok := mock.files[created.WorkspacePath+"/MEMORY.md"]
	if !ok {
		t.Fatal("MEMORY.md was not seeded")
	}
	for _, want := range []string{"# Release Reviewer", "Crew brief", "Workflow/build", "Own release quality", "Check the changelog first."} {
		if !strings.Contains(memory, want) {
			t.Fatalf("MEMORY.md missing %q:\n%s", want, memory)
		}
	}
	var runtime map[string]interface{}
	if err := json.Unmarshal([]byte(mock.files[created.WorkspacePath+"/workflow.json"]), &runtime); err != nil {
		t.Fatal(err)
	}
	caps, _ := runtime["capabilities"].(map[string]interface{})
	assertStringSet := func(key string, want ...string) {
		t.Helper()
		raw, _ := caps[key].([]interface{})
		if len(raw) != len(want) {
			t.Fatalf("%s = %v, want %v", key, raw, want)
		}
		for i, w := range want {
			if raw[i] != w {
				t.Fatalf("%s = %v, want %v", key, raw, want)
			}
		}
	}
	assertStringSet("selected_skills", "agent-browser")
	assertStringSet("selected_servers", "github")
	assertStringSet("selected_secrets", "SHARED_GH")
	assertStringSet("selected_global_secret_names", "SHARED_GH")
	// Re-entry converges without duplicating selections.
	if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", Instructions: "Check the changelog first.",
		Skills: []string{"agent-browser"}, Servers: []string{"github"},
		Secrets: []string{"SHARED_GH"}, GlobalSecrets: []string{"SHARED_GH"},
		StepInstruction: "Review the release.", IdempotencyKey: "proposal-1",
	}); err != nil {
		t.Fatal(err)
	}
	runtime = nil
	if err := json.Unmarshal([]byte(mock.files[created.WorkspacePath+"/workflow.json"]), &runtime); err != nil {
		t.Fatal(err)
	}
	caps, _ = runtime["capabilities"].(map[string]interface{})
	assertStringSet("selected_skills", "agent-browser")
	assertStringSet("selected_servers", "github")
}

func TestCreateCrewProjectSeedsStarterBrief(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Briefed Crew",
		Purpose:        "Own release quality",
		TriggerMessage: "Review releases.", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	brief, ok := mock.files[created.WorkspacePath+"/MEMORY.md"]
	if !ok {
		t.Fatal("MEMORY.md was not seeded")
	}
	if !strings.Contains(brief, "Own release quality") {
		t.Fatalf("starter brief missing purpose:\n%s", brief)
	}
}

func TestCreateCrewProjectRejectsUnknownSkillAndBadNames(t *testing.T) {
	svc, _, ctx := newCrewCreationTestEnv(t)
	if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Ghost Skills",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		Skills: []string{"ghost"}, IdempotencyKey: "proposal-1",
	}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("unknown skill err = %v, want install-first failure", err)
	}
	for name, req := range map[string]CreateCrewRequest{
		"secret path":   {UserID: "owner", WorkflowPath: "Workflow/build", Title: "T", Purpose: "P", StepInstruction: "S", Secrets: []string{"a/b"}, IdempotencyKey: "k"},
		"server dotdot": {UserID: "owner", WorkflowPath: "Workflow/build", Title: "T", Purpose: "P", StepInstruction: "S", Servers: []string{".."}, IdempotencyKey: "k"},
		"empty global":  {UserID: "owner", WorkflowPath: "Workflow/build", Title: "T", Purpose: "P", StepInstruction: "S", GlobalSecrets: []string{" "}, IdempotencyKey: "k"},
	} {
		if _, err := svc.CreateCrewProject(ctx, req); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestCreateCrewProjectDefaultLLMConfig(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnvWithOptions(t, []agentprofiles.ProviderOption{
		{ID: "codex-cli", Label: "Codex", Provider: "codex-cli", ModelID: "gpt-6", ReasoningEfforts: []string{"high"}},
		{ID: "claude-code", Label: "Claude", Provider: "claude-code", ModelID: "claude-1", Default: true, Options: map[string]interface{}{"reasoning_effort": "medium"}},
	})
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Modeled Crew",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var runtime map[string]interface{}
	if err := json.Unmarshal([]byte(mock.files[created.WorkspacePath+"/workflow.json"]), &runtime); err != nil {
		t.Fatal(err)
	}
	caps, _ := runtime["capabilities"].(map[string]interface{})
	llm, _ := caps["llm_config"].(map[string]interface{})
	builder, _ := llm["builder_llm"].(map[string]interface{})
	if llm["mode"] != "explicit" || builder["provider"] != "claude-code" || builder["model_id"] != "claude-1" {
		t.Fatalf("llm_config = %v, want the default provider option", llm)
	}
	options, _ := builder["options"].(map[string]interface{})
	if options["reasoning_effort"] != "medium" {
		t.Fatalf("llm options = %v", options)
	}
}

func TestCreateCrewProjectWiring(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	mock.files["Workflow/build/planning/plan.json"] = `{"objective":"t","steps":[]}`
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		ContextDependencies: []string{"release-notes"}, IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.TriggerID == "" || created.AttachmentAlias != "release-reviewer" {
		t.Fatalf("wiring = %+v", created)
	}
	triggers, err := svc.projectWebhookConfigs(ctx, "owner", "work", created.CrewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 1 {
		t.Fatalf("triggers = %+v, want one", triggers)
	}
	trigger := triggers[0]
	if trigger.ID != created.TriggerID || !trigger.IsInternal() || !trigger.Enabled ||
		trigger.RunDestination != runDestinationIsolated || trigger.Caller == nil ||
		trigger.Caller.Type != triggerCallerWorkflow {
		t.Fatalf("trigger = %+v, want enabled isolated internal binding", trigger)
	}
	var manifest WorkflowManifest
	if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/build")]), &manifest); err != nil {
		t.Fatal(err)
	}
	if trigger.Caller.ID != manifest.ID {
		t.Fatalf("trigger caller = %+v, want workflow %q", trigger.Caller, manifest.ID)
	}
	if len(manifest.CrewAttachments) != 1 {
		t.Fatalf("attachments = %+v, want one", manifest.CrewAttachments)
	}
	attachment := manifest.CrewAttachments[0]
	if attachment.Alias != "release-reviewer" || attachment.CrewProjectID != created.CrewID ||
		attachment.CrewWorkspacePath != created.WorkspacePath {
		t.Fatalf("attachment = %+v", attachment)
	}
	step := created.Step
	if step.StepID != "crew-release-reviewer" || step.CrewProjectID != created.CrewID ||
		step.TriggerID != created.TriggerID || step.Instruction != "Review the release." ||
		len(step.ContextDependencies) != 1 || step.ContextDependencies[0] != "release-notes" {
		t.Fatalf("step config = %+v", step)
	}
	// The created wiring satisfies the hardened run preflight: the stored
	// root equals the freshly authorized binding.
	if err := preflightCrewSteps(ctx, svc, "owner", "Workflow/build"); err != nil {
		t.Fatalf("preflight on created wiring: %v", err)
	}
}

func TestCreateCrewProjectWiringIdempotent(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	req := CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	}
	first, err := svc.CreateCrewProject(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateCrewProject(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate || second.TriggerID != first.TriggerID || second.Step.StepID != first.Step.StepID {
		t.Fatalf("retry = %+v, want adopted wiring %+v", second, first)
	}
	triggers, err := svc.projectWebhookConfigs(ctx, "owner", "work", first.CrewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 1 {
		t.Fatalf("triggers = %d, want 1", len(triggers))
	}
	var manifest WorkflowManifest
	if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/build")]), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.CrewAttachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(manifest.CrewAttachments))
	}
}

func TestCrewManifestRewritePreservesContextPaths(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// A later trigger save rewrites the runtime manifest through the Go
	// struct; the seeded context paths and UI-written capability keys must
	// survive the round-trip.
	if _, _, err := svc.saveProductWebhookConfig(ctx, "owner", productWebhookRequest{
		ProfileID: "work", ProjectID: created.CrewID, Name: "Second trigger",
		Message: "Another entrypoint.", Enabled: false,
	}, ""); err != nil {
		t.Fatal(err)
	}
	var runtime map[string]interface{}
	if err := json.Unmarshal([]byte(mock.files[created.WorkspacePath+"/workflow.json"]), &runtime); err != nil {
		t.Fatal(err)
	}
	contexts, _ := runtime["workflow_context_paths"].([]interface{})
	if len(contexts) != 1 || contexts[0] != "Workflow/build" {
		t.Fatalf("workflow_context_paths = %v after rewrite", contexts)
	}
	caps, _ := runtime["capabilities"].(map[string]interface{})
	if caps["browser_mode"] != "auto" {
		t.Fatalf("browser_mode = %v after rewrite", caps["browser_mode"])
	}
	triggers, err := svc.projectWebhookConfigs(ctx, "owner", "work", created.CrewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 2 {
		t.Fatalf("triggers = %d, want both", len(triggers))
	}
}

func TestCreateCrewProjectReservedAlias(t *testing.T) {
	svc, _, ctx := newCrewCreationTestEnv(t)
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Planning",
		Purpose: "Plan releases", StepInstruction: "Plan the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.AttachmentAlias != "planning-crew" {
		t.Fatalf("alias = %q, want the reserved word suffixed", created.AttachmentAlias)
	}
}

func TestCreateCrewProjectRejectsChangedPayload(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	first, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Same key, changed title: a conflict, never a second directory with
	// the same crew ID.
	if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer Renamed",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	}); err == nil || !strings.Contains(err.Error(), "new key") {
		t.Fatalf("changed title err = %v, want a new-key conflict", err)
	}
	crews := map[string]bool{}
	for path := range mock.files {
		if strings.HasSuffix(path, "/product.json") && strings.HasPrefix(path, "Crew/") {
			crews[strings.TrimSuffix(path, "/product.json")] = true
		}
	}
	if len(crews) != 1 {
		t.Fatalf("crew dirs = %v, want exactly the original", crews)
	}
	// A verbatim retry still adopts the original.
	again, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Duplicate || again.CrewID != first.CrewID || again.WorkspacePath != first.WorkspacePath {
		t.Fatalf("retry = %+v, want the original %+v", again, first)
	}
}

func TestCreateCrewProjectAdoptsOccupiedFallback(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	slug := slugifyCrewTitle("Release Reviewer")
	occupied := "Crew/" + slug + "-" + crewCreationSuffix("proposal-1")
	mock.files[occupied+"/product.json"] = `{"schema_version":1,"product":"work","id":"someone-else","title":"Other","session_id":"work:project:someone-else"}`
	first, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Re-entry adopts the frozen fallback identity instead of minting
	// another random crew.
	second, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate || second.CrewID != first.CrewID || second.WorkspacePath != first.WorkspacePath {
		t.Fatalf("retry = %+v, want the fallback crew %+v", second, first)
	}
}

func TestCreateCrewProjectDuplicateTitles(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	make := func(key string) CreatedCrew {
		t.Helper()
		created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
			UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
			Purpose: "Own release quality", StepInstruction: "Review the release.",
			IdempotencyKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	first := make("proposal-1")
	second := make("proposal-2")
	if first.AttachmentAlias != "release-reviewer" || second.AttachmentAlias != "release-reviewer-2" {
		t.Fatalf("aliases = %q, %q, want slug and slug-2", first.AttachmentAlias, second.AttachmentAlias)
	}
	if first.Step.StepID != "crew-release-reviewer" || second.Step.StepID != "crew-release-reviewer-2" {
		t.Fatalf("step ids = %q, %q, want paired suffixes", first.Step.StepID, second.Step.StepID)
	}
	// Both crews finish complete: one trigger each, both attached.
	for _, created := range []CreatedCrew{first, second} {
		triggers, err := svc.projectWebhookConfigs(ctx, "owner", "work", created.CrewID)
		if err != nil {
			t.Fatal(err)
		}
		if len(triggers) != 1 || triggers[0].ID != created.TriggerID {
			t.Fatalf("triggers for %s = %+v", created.CrewID, triggers)
		}
	}
	var manifest WorkflowManifest
	if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/build")]), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.CrewAttachments) != 2 {
		t.Fatalf("attachments = %+v, want both crews", manifest.CrewAttachments)
	}
	// Explicit collisions fail before writing anything.
	if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Other",
		Alias: "release-reviewer", Purpose: "P", StepInstruction: "S",
		IdempotencyKey: "proposal-3",
	}); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("taken alias err = %v, want an already-used failure", err)
	}
	mock.files["Workflow/build/planning/plan.json"] = `{"steps":[{"id":"custom-step"}]}`
	if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Other",
		StepID: "custom-step", Purpose: "P", StepInstruction: "S",
		IdempotencyKey: "proposal-4",
	}); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("taken step err = %v, want an already-used failure", err)
	}
}

func TestCreateCrewProjectAvailability(t *testing.T) {
	newEnv := func(t *testing.T) (*ProductScheduleService, *mockWorkspaceAPI, context.Context) {
		t.Helper()
		return newCrewCreationTestEnv(t)
	}
	t.Run("unknown server fails before creating", func(t *testing.T) {
		svc, mock, ctx := newEnv(t)
		if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
			UserID: "owner", WorkflowPath: "Workflow/build", Title: "T",
			Servers: []string{"ghost"}, Purpose: "P", StepInstruction: "S",
			IdempotencyKey: "k",
		}); err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("unknown server err = %v, want not-configured", err)
		}
		for path := range mock.files {
			if strings.HasPrefix(path, "Crew/") {
				t.Fatalf("failed validation created %s", path)
			}
		}
	})
	t.Run("disconnected server is selected and pending", func(t *testing.T) {
		svc, mock, ctx := newEnv(t)
		created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
			UserID: "owner", WorkflowPath: "Workflow/build", Title: "T",
			Servers: []string{"gitlab"}, Purpose: "P", StepInstruction: "S",
			IdempotencyKey: "k",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(created.Pending) != 1 || created.Pending[0].Name != "gitlab" || created.Pending[0].Kind != "mcp_server" {
			t.Fatalf("pending = %+v, want gitlab", created.Pending)
		}
		var runtime map[string]interface{}
		if err := json.Unmarshal([]byte(mock.files[created.WorkspacePath+"/workflow.json"]), &runtime); err != nil {
			t.Fatal(err)
		}
		caps, _ := runtime["capabilities"].(map[string]interface{})
		raw, _ := caps["selected_servers"].([]interface{})
		if len(raw) != 1 || raw[0] != "gitlab" {
			t.Fatalf("selected_servers = %v, want gitlab recorded", raw)
		}
	})
	t.Run("unknown secret fails", func(t *testing.T) {
		svc, _, ctx := newEnv(t)
		if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
			UserID: "owner", WorkflowPath: "Workflow/build", Title: "T",
			Secrets: []string{"GHOST"}, Purpose: "P", StepInstruction: "S",
			IdempotencyKey: "k",
		}); err == nil || !strings.Contains(err.Error(), "no stored value") {
			t.Fatalf("unknown secret err = %v, want no-stored-value", err)
		}
	})
	t.Run("workflow scoped secret does not carry over", func(t *testing.T) {
		svc, _, ctx := newEnv(t)
		svc.crewAvailability.ScopedSecrets = func(context.Context, string, string) (map[string]bool, error) {
			return map[string]bool{"WF_ONLY": true}, nil
		}
		if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
			UserID: "owner", WorkflowPath: "Workflow/build", Title: "T",
			Secrets: []string{"WF_ONLY"}, Purpose: "P", StepInstruction: "S",
			IdempotencyKey: "k",
		}); err == nil || !strings.Contains(err.Error(), "do not carry over") {
			t.Fatalf("scoped secret err = %v, want a carry-over hint", err)
		}
	})
	t.Run("unknown global fails", func(t *testing.T) {
		svc, _, ctx := newEnv(t)
		if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
			UserID: "owner", WorkflowPath: "Workflow/build", Title: "T",
			GlobalSecrets: []string{"GHOST"}, Purpose: "P", StepInstruction: "S",
			IdempotencyKey: "k",
		}); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("unknown global err = %v, want does-not-exist", err)
		}
	})
}

func TestCreateCrewProjectSucceedsUnderBuilderFolderGuard(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	// Mirror a Builder session: the agent's own writes are confined to
	// the workflow folder, but server-side creation must still initialize
	// the crew tree outside that sandbox.
	ctx = context.WithValue(ctx, common.FolderGuardAllowedWriteFolderKey, []string{"Workflow/build"})
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mock.hasFolder(created.WorkspacePath + "/code") {
		t.Fatal("code/ folder was not created under the guard")
	}
	if _, ok := mock.files[created.WorkspacePath+"/product.json"]; !ok {
		t.Fatal("product.json was not written under the guard")
	}
}

func TestCreateCrewProjectInheritsWorkflowLLM(t *testing.T) {
	setWorkflowLLM := func(t *testing.T, mock *mockWorkspaceAPI, llm *workflowtypes.PresetLLMConfig) {
		t.Helper()
		var manifest WorkflowManifest
		if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/build")]), &manifest); err != nil {
			t.Fatal(err)
		}
		manifest.Capabilities.LLMConfig = llm
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		mock.files[manifestPath("Workflow/build")] = string(raw)
	}
	crewBuilderLLM := func(t *testing.T, mock *mockWorkspaceAPI, created CreatedCrew) map[string]interface{} {
		t.Helper()
		var runtime map[string]interface{}
		if err := json.Unmarshal([]byte(mock.files[created.WorkspacePath+"/workflow.json"]), &runtime); err != nil {
			t.Fatal(err)
		}
		caps, _ := runtime["capabilities"].(map[string]interface{})
		llm, _ := caps["llm_config"].(map[string]interface{})
		builder, _ := llm["builder_llm"].(map[string]interface{})
		return builder
	}
	baseReq := func(key string) CreateCrewRequest {
		return CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "T", Purpose: "P", StepInstruction: "S", IdempotencyKey: key}
	}
	t.Run("provider profile resolves", func(t *testing.T) {
		svc, mock, ctx := newCrewCreationTestEnv(t)
		profile := &workflowtypes.PresetLLMConfig{SchemaVersion: 2, Mode: workflowtypes.LLMConfigModeProviderProfile, Provider: "muse-cli"}
		setWorkflowLLM(t, mock, profile)
		created, err := svc.CreateCrewProject(ctx, baseReq("k"))
		if err != nil {
			t.Fatal(err)
		}
		expected, _, ok := workflowtypes.ResolveProviderProfileConfig(profile)
		if !ok || expected == nil {
			t.Fatal("muse-cli has no tier defaults")
		}
		builder := crewBuilderLLM(t, mock, created)
		if builder["provider"] != expected.Provider || builder["model_id"] != expected.ModelID {
			t.Fatalf("builder_llm = %v, want %s/%s", builder, expected.Provider, expected.ModelID)
		}
	})
	t.Run("explicit builder carries over", func(t *testing.T) {
		svc, mock, ctx := newCrewCreationTestEnv(t)
		setWorkflowLLM(t, mock, &workflowtypes.PresetLLMConfig{SchemaVersion: 2, Mode: workflowtypes.LLMConfigModeExplicit,
			BuilderLLM: &workflowtypes.AgentLLMConfig{Provider: "custom", ModelID: "m1", ConnectionID: "c9", Options: map[string]interface{}{"temperature": "0.1"}}})
		created, err := svc.CreateCrewProject(ctx, baseReq("k"))
		if err != nil {
			t.Fatal(err)
		}
		builder := crewBuilderLLM(t, mock, created)
		if builder["provider"] != "custom" || builder["model_id"] != "m1" || builder["connection_id"] != "c9" {
			t.Fatalf("builder_llm = %v, want the explicit workflow model", builder)
		}
	})
	t.Run("unknown provider falls back to profile default", func(t *testing.T) {
		svc, mock, ctx := newCrewCreationTestEnv(t)
		setWorkflowLLM(t, mock, &workflowtypes.PresetLLMConfig{SchemaVersion: 2, Mode: workflowtypes.LLMConfigModeProviderProfile, Provider: "nope-nope"})
		created, err := svc.CreateCrewProject(ctx, baseReq("k"))
		if err != nil {
			t.Fatal(err)
		}
		if builder := crewBuilderLLM(t, mock, created); len(builder) != 0 {
			t.Fatalf("builder_llm = %v, want profile-default fallback (none in test env)", builder)
		}
	})
}

func TestCreateCrewProjectResolves(t *testing.T) {
	svc, _, ctx := newCrewCreationTestEnv(t)
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	profile, binding, manifest, err := svc.projectManifest(ctx, "owner", "work", created.CrewID)
	if err != nil {
		t.Fatalf("created crew does not resolve: %v", err)
	}
	if binding.WorkspacePath != created.WorkspacePath || manifest.Title != "Release Reviewer" {
		t.Fatalf("binding = %+v manifest = %+v", binding, manifest)
	}
	if len(binding.ProjectWorkflowContextPaths) != 1 || binding.ProjectWorkflowContextPaths[0] != "Workflow/build" {
		t.Fatalf("context paths = %v", binding.ProjectWorkflowContextPaths)
	}
	_ = profile
}

// server A: create_crew rejected the built-in agent-browser skill because only the
// workspace skills/ folder was consulted.
func TestCrewCreationAcceptsBuiltinSkills(t *testing.T) {
	t.Setenv("WORKSPACE_API_URL", "http://127.0.0.1:1")
	got, err := validateCrewCreationSkills([]string{" agent-browser "})
	if err != nil {
		t.Fatalf("built-in skill rejected: %v", err)
	}
	if len(got) != 1 || got[0] != "agent-browser" {
		t.Fatalf("validated skills = %v", got)
	}
	if _, err := validateCrewCreationSkills([]string{"definitely-not-a-skill-xyz"}); err == nil {
		t.Fatal("unknown skill was accepted")
	}
}
