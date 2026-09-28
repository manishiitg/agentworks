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
		Role: "Reviewer", Purpose: "Own release quality",
		StepInstruction: "Review the release.", IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.CrewID == "" || created.SessionID != "work:project:"+created.CrewID {
		t.Fatalf("crew identity = %+v", created)
	}
	if !strings.HasPrefix(created.WorkspacePath, "_users/owner/Chats/Work/projects/release-reviewer-") {
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
	if identity["role"] != "Reviewer" {
		t.Fatalf("product.json identity = %v, want role persisted", product["identity"])
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
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
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
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if third.Duplicate || third.CrewID == first.CrewID || third.AttachmentAlias == first.AttachmentAlias {
		t.Fatalf("new key = %+v, want a distinct crew", third)
	}
}

func TestCreateCrewProjectAppliesWebsiteGrowthTemplateOnBuilderAction(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	req := CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Search Researcher",
		Role: "Buyer-question and search opportunity researcher", Purpose: "Map site content gaps",
		TemplateID: "search-opportunity-mapper", StepInstruction: "Return a sourced opportunity list.",
		IdempotencyKey: "website-growth-specialist-1",
	}
	created, err := svc.CreateCrewProject(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mock.files[created.WorkspacePath+"/skills/search-opportunity-mapper/SKILL.md"]; !ok {
		t.Fatal("Builder-created Crew lacks its local specialist skill")
	}
	if _, ok := mock.files[created.WorkspacePath+"/templates/search-opportunity-mapper/TEMPLATE_SETUP.json"]; !ok {
		t.Fatal("Builder-created Crew lacks its setup checklist")
	}
	var product struct {
		Templates []struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"templates"`
	}
	if err := json.Unmarshal([]byte(mock.files[created.WorkspacePath+"/product.json"]), &product); err != nil {
		t.Fatal(err)
	}
	if len(product.Templates) != 1 || product.Templates[0].ID != req.TemplateID || product.Templates[0].Version != 1 {
		t.Fatalf("Crew template receipt = %+v", product.Templates)
	}
	var runtime struct {
		Capabilities struct {
			SelectedSkills []string `json:"selected_skills"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(mock.files[created.WorkspacePath+"/workflow.json"]), &runtime); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Capabilities.SelectedSkills) != 1 || runtime.Capabilities.SelectedSkills[0] != req.TemplateID {
		t.Fatalf("selected skills = %+v", runtime.Capabilities.SelectedSkills)
	}
	retry, err := svc.CreateCrewProject(ctx, req)
	if err != nil || !retry.Duplicate || retry.CrewID != created.CrewID {
		t.Fatalf("retry = %+v, %v", retry, err)
	}
}

func TestCreateCrewProjectAppliesFinanceTemplateOnBuilderAction(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	req := CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Billing Reviewer",
		Role: "Billing operations coordinator", Purpose: "Review payment exceptions",
		TemplateID: "billing-operations-coordinator", StepInstruction: "Return a source-linked billing queue.",
		IdempotencyKey: "finance-billing-specialist-1",
	}
	created, err := svc.CreateCrewProject(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	base := created.WorkspacePath
	for _, relative := range []string{
		"skills/billing-operations-coordinator/SKILL.md",
		"templates/billing-operations-coordinator/SETUP.md",
		"templates/billing-operations-coordinator/TEMPLATE_SETUP.json",
	} {
		if mock.files[base+"/"+relative] == "" {
			t.Fatalf("Builder-created Finance Crew lacks %s", relative)
		}
	}
	var product struct {
		Templates []struct {
			ID string `json:"id"`
		} `json:"templates"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"/product.json"]), &product); err != nil {
		t.Fatal(err)
	}
	if len(product.Templates) != 1 || product.Templates[0].ID != req.TemplateID {
		t.Fatalf("Finance template receipt = %+v", product.Templates)
	}
	var runtime struct {
		Capabilities struct {
			SelectedSkills  []string `json:"selected_skills"`
			SelectedServers []string `json:"selected_servers"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"/workflow.json"]), &runtime); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Capabilities.SelectedSkills) != 1 || runtime.Capabilities.SelectedSkills[0] != req.TemplateID || len(runtime.Capabilities.SelectedServers) != 0 {
		t.Fatalf("unexpected Finance Crew capabilities = %+v", runtime.Capabilities)
	}
}

func TestCreateCrewProjectAppliesSalesTemplateOnBuilderAction(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	req := CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Lead Intake",
		Role: "Inbound lead qualifier", Purpose: "Review inbound enquiries",
		TemplateID: "lead-intake-qualifier", StepInstruction: "Return a sourced qualification brief.",
		IdempotencyKey: "sales-intake-specialist-1",
	}
	created, err := svc.CreateCrewProject(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	base := created.WorkspacePath
	for _, relative := range []string{
		"skills/lead-intake-qualifier/SKILL.md",
		"templates/lead-intake-qualifier/SETUP.md",
		"templates/lead-intake-qualifier/TEMPLATE_SETUP.json",
	} {
		if mock.files[base+"/"+relative] == "" {
			t.Fatalf("Builder-created Sales Crew lacks %s", relative)
		}
	}
	var product struct {
		Templates []struct {
			ID string `json:"id"`
		} `json:"templates"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"/product.json"]), &product); err != nil {
		t.Fatal(err)
	}
	if len(product.Templates) != 1 || product.Templates[0].ID != req.TemplateID {
		t.Fatalf("Sales template receipt = %+v", product.Templates)
	}
	var runtime struct {
		Capabilities struct {
			SelectedSkills  []string `json:"selected_skills"`
			SelectedServers []string `json:"selected_servers"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"/workflow.json"]), &runtime); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Capabilities.SelectedSkills) != 1 || runtime.Capabilities.SelectedSkills[0] != req.TemplateID || len(runtime.Capabilities.SelectedServers) != 0 {
		t.Fatalf("unexpected Sales Crew capabilities = %+v", runtime.Capabilities)
	}
}

func TestCreateCrewProjectAppliesExpandedSalesTemplateOnBuilderAction(t *testing.T) {
	for _, templateID := range []string{"sales-call-briefing", "proposal-drafter", "pipeline-analyst", "deal-follow-through-coordinator"} {
		t.Run(templateID, func(t *testing.T) {
			svc, mock, ctx := newCrewCreationTestEnv(t)
			created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
				UserID: "owner", WorkflowPath: "Workflow/build", Title: "Sales Operator",
				Role: "Sales operations", Purpose: "Review a sourced sales result",
				TemplateID: templateID, StepInstruction: "Return a sourced result for owner review.",
				IdempotencyKey: "sales-expansion-" + templateID,
			})
			if err != nil {
				t.Fatal(err)
			}
			base := created.WorkspacePath
			for _, relative := range []string{
				"skills/" + templateID + "/SKILL.md",
				"templates/" + templateID + "/SETUP.md",
				"templates/" + templateID + "/TEMPLATE_SETUP.json",
			} {
				if mock.files[base+"/"+relative] == "" {
					t.Fatalf("Sales Crew lacks %s", relative)
				}
			}
			var setup struct {
				CompletedSteps []string `json:"completed_steps"`
			}
			if err := json.Unmarshal([]byte(mock.files[base+"/templates/"+templateID+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
				t.Fatal(err)
			}
			if len(setup.CompletedSteps) != 0 {
				t.Fatalf("Sales setup unexpectedly completed: %+v", setup)
			}
		})
	}
}

func TestCreateCrewProjectAppliesProductFeedbackTemplateOnBuilderAction(t *testing.T) {
	templateID := "product-feedback-coordinator"
	svc, mock, ctx := newCrewCreationTestEnv(t)
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Product Feedback",
		Role: "Product feedback coordinator", Purpose: "Review a sourced feedback theme",
		TemplateID: templateID, StepInstruction: "Return a product owner decision brief.",
		IdempotencyKey: "product-feedback-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	base := created.WorkspacePath
	for _, relative := range []string{
		"skills/" + templateID + "/SKILL.md",
		"templates/" + templateID + "/SETUP.md",
		"templates/" + templateID + "/TEMPLATE_SETUP.json",
	} {
		if mock.files[base+"/"+relative] == "" {
			t.Fatalf("Product Crew lacks %s", relative)
		}
	}
	var setup struct {
		CompletedSteps []string `json:"completed_steps"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"/templates/"+templateID+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if len(setup.CompletedSteps) != 0 {
		t.Fatalf("Product setup unexpectedly completed: %+v", setup)
	}
}

func TestCreateCrewProjectAppliesCustomerSuccessTemplateOnBuilderAction(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	req := CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Onboarding Coordinator",
		Role: "Customer onboarding coordinator", Purpose: "Track first-value milestones",
		TemplateID: "customer-onboarding-coordinator", StepInstruction: "Return an owned milestone register.",
		IdempotencyKey: "customer-success-onboarding-1",
	}
	created, err := svc.CreateCrewProject(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	base := created.WorkspacePath
	for _, relative := range []string{
		"skills/customer-onboarding-coordinator/SKILL.md",
		"templates/customer-onboarding-coordinator/SETUP.md",
		"templates/customer-onboarding-coordinator/TEMPLATE_SETUP.json",
	} {
		if mock.files[base+"/"+relative] == "" {
			t.Fatalf("Builder-created Customer Success Crew lacks %s", relative)
		}
	}
	var product struct {
		Templates []struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"templates"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"/product.json"]), &product); err != nil {
		t.Fatal(err)
	}
	if len(product.Templates) != 1 || product.Templates[0].ID != req.TemplateID || product.Templates[0].Version != 1 {
		t.Fatalf("Customer Success template receipt = %+v", product.Templates)
	}
}

func TestCreateCrewProjectAppliesEngineeringTemplateOnBuilderAction(t *testing.T) {
	for _, templateID := range []string{"incident-investigator", "engineering-delivery-coordinator", "performance-investigator", "cloud-cost-analyst"} {
		t.Run(templateID, func(t *testing.T) {
			svc, mock, ctx := newCrewCreationTestEnv(t)
			created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
				UserID: "owner", WorkflowPath: "Workflow/build", Title: "Engineering Operator",
				Role: "Engineering operator", Purpose: "Investigate a sourced engineering exception",
				TemplateID: templateID, StepInstruction: "Return a source-linked result for review.",
				IdempotencyKey: "engineering-" + templateID,
			})
			if err != nil {
				t.Fatal(err)
			}
			base := created.WorkspacePath
			for _, relative := range []string{
				"skills/" + templateID + "/SKILL.md",
				"templates/" + templateID + "/SETUP.md",
				"templates/" + templateID + "/TEMPLATE_SETUP.json",
			} {
				if mock.files[base+"/"+relative] == "" {
					t.Fatalf("Engineering Crew lacks %s", relative)
				}
			}
			var setup struct {
				CompletedSteps []string `json:"completed_steps"`
			}
			if err := json.Unmarshal([]byte(mock.files[base+"/templates/"+templateID+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
				t.Fatal(err)
			}
			if len(setup.CompletedSteps) != 0 {
				t.Fatalf("Engineering setup unexpectedly completed: %+v", setup.CompletedSteps)
			}
		})
	}
}

func TestCreateCrewProjectAppliesQATemplateOnBuilderAction(t *testing.T) {
	for _, templateID := range []string{"browser-journey-qa-analyst", "flaky-test-investigator", "release-quality-assistant"} {
		t.Run(templateID, func(t *testing.T) {
			svc, mock, ctx := newCrewCreationTestEnv(t)
			created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
				UserID: "owner", WorkflowPath: "Workflow/build", Title: "QA Operator",
				Role: "QA operator", Purpose: "Review exact test evidence",
				TemplateID: templateID, StepInstruction: "Return a source-linked QA decision for review.",
				IdempotencyKey: "qa-" + templateID,
			})
			if err != nil {
				t.Fatal(err)
			}
			base := created.WorkspacePath
			for _, relative := range []string{
				"skills/" + templateID + "/SKILL.md",
				"templates/" + templateID + "/SETUP.md",
				"templates/" + templateID + "/TEMPLATE_SETUP.json",
			} {
				if mock.files[base+"/"+relative] == "" {
					t.Fatalf("QA Crew lacks %s", relative)
				}
			}
			var setup struct {
				CompletedSteps []string `json:"completed_steps"`
			}
			if err := json.Unmarshal([]byte(mock.files[base+"/templates/"+templateID+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
				t.Fatal(err)
			}
			if len(setup.CompletedSteps) != 0 {
				t.Fatalf("QA setup unexpectedly completed: %+v", setup.CompletedSteps)
			}
		})
	}
}

func TestCreateCrewProjectAppliesSecurityTemplateOnBuilderAction(t *testing.T) {
	for _, templateID := range []string{"security-findings-analyst", "access-review-analyst", "security-remediation-coordinator"} {
		t.Run(templateID, func(t *testing.T) {
			svc, mock, ctx := newCrewCreationTestEnv(t)
			created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
				UserID: "owner", WorkflowPath: "Workflow/build", Title: "Security Operator",
				Role: "Security operator", Purpose: "Review authorized security evidence",
				TemplateID: templateID, StepInstruction: "Return a source-linked finding or decision for review.",
				IdempotencyKey: "security-" + templateID,
			})
			if err != nil {
				t.Fatal(err)
			}
			base := created.WorkspacePath
			for _, relative := range []string{
				"skills/" + templateID + "/SKILL.md",
				"templates/" + templateID + "/SETUP.md",
				"templates/" + templateID + "/TEMPLATE_SETUP.json",
			} {
				if mock.files[base+"/"+relative] == "" {
					t.Fatalf("Security Crew lacks %s", relative)
				}
			}
			var setup map[string]interface{}
			if err := json.Unmarshal([]byte(mock.files[base+"/templates/"+templateID+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
				t.Fatal(err)
			}
			completed, ok := setup["completed_steps"].([]interface{})
			if !ok || len(completed) != 0 {
				t.Fatalf("Security setup unexpectedly completed: %+v", setup)
			}
		})
	}
}

func TestCreateCrewProjectAppliesGTMTemplateOnBuilderAction(t *testing.T) {
	for _, templateID := range []string{"gtm-strategy-analyst", "launch-coordinator"} {
		t.Run(templateID, func(t *testing.T) {
			svc, mock, ctx := newCrewCreationTestEnv(t)
			created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
				UserID: "owner", WorkflowPath: "Workflow/build", Title: "GTM Operator",
				Role: "GTM operator", Purpose: "Review a sourced launch plan",
				TemplateID: templateID, StepInstruction: "Return a reviewable launch decision.",
				IdempotencyKey: "gtm-" + templateID,
			})
			if err != nil {
				t.Fatal(err)
			}
			base := created.WorkspacePath
			for _, relative := range []string{
				"skills/" + templateID + "/SKILL.md",
				"templates/" + templateID + "/SETUP.md",
				"templates/" + templateID + "/TEMPLATE_SETUP.json",
			} {
				if mock.files[base+"/"+relative] == "" {
					t.Fatalf("GTM Crew lacks %s", relative)
				}
			}
			var setup map[string]interface{}
			if err := json.Unmarshal([]byte(mock.files[base+"/templates/"+templateID+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
				t.Fatal(err)
			}
			completed, ok := setup["completed_steps"].([]interface{})
			if !ok || len(completed) != 0 {
				t.Fatalf("GTM setup unexpectedly completed: %+v", setup)
			}
		})
	}
}

func TestCreateCrewProjectAppliesCustomerSupportTemplateOnBuilderAction(t *testing.T) {
	for _, templateID := range []string{"support-triage-assistant", "support-reply-drafter", "escalation-coordinator", "feedback-review-analyst"} {
		t.Run(templateID, func(t *testing.T) {
			svc, mock, ctx := newCrewCreationTestEnv(t)
			created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
				UserID: "owner", WorkflowPath: "Workflow/build", Title: "Support Operator",
				Role: "Support operator", Purpose: "Review a sourced customer case",
				TemplateID: templateID, StepInstruction: "Return a source-linked case decision for review.",
				IdempotencyKey: "support-" + templateID,
			})
			if err != nil {
				t.Fatal(err)
			}
			base := created.WorkspacePath
			for _, relative := range []string{
				"skills/" + templateID + "/SKILL.md",
				"templates/" + templateID + "/SETUP.md",
				"templates/" + templateID + "/TEMPLATE_SETUP.json",
			} {
				if mock.files[base+"/"+relative] == "" {
					t.Fatalf("Customer Support Crew lacks %s", relative)
				}
			}
			var setup map[string]interface{}
			if err := json.Unmarshal([]byte(mock.files[base+"/templates/"+templateID+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
				t.Fatal(err)
			}
			completed, ok := setup["completed_steps"].([]interface{})
			if !ok || len(completed) != 0 {
				t.Fatalf("Customer Support setup unexpectedly completed: %+v", setup)
			}
		})
	}
}

func TestCreateCrewProjectAppliesOperationsTemplateOnBuilderAction(t *testing.T) {
	for _, templateID := range []string{"chief-of-staff", "meeting-actions-coordinator", "project-status-reporter", "order-operations-coordinator", "vendor-researcher", "document-intake-assistant"} {
		t.Run(templateID, func(t *testing.T) {
			svc, mock, ctx := newCrewCreationTestEnv(t)
			created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
				UserID: "owner", WorkflowPath: "Workflow/build", Title: "Operations Operator",
				Role: "Operations operator", Purpose: "Review a sourced operations result",
				TemplateID: templateID, StepInstruction: "Return a source-linked decision for review.",
				IdempotencyKey: "operations-" + templateID,
			})
			if err != nil {
				t.Fatal(err)
			}
			base := created.WorkspacePath
			for _, relative := range []string{
				"skills/" + templateID + "/SKILL.md",
				"templates/" + templateID + "/SETUP.md",
				"templates/" + templateID + "/TEMPLATE_SETUP.json",
			} {
				if mock.files[base+"/"+relative] == "" {
					t.Fatalf("Operations Crew lacks %s", relative)
				}
			}
			var setup map[string]interface{}
			if err := json.Unmarshal([]byte(mock.files[base+"/templates/"+templateID+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
				t.Fatal(err)
			}
			completed, ok := setup["completed_steps"].([]interface{})
			if !ok || len(completed) != 0 {
				t.Fatalf("Operations setup unexpectedly completed: %+v", setup)
			}
		})
	}
}

func TestCreateCrewProjectAppliesBillingCapabilityPackOnBuilderAction(t *testing.T) {
	for _, templateID := range []string{"invoice-chasing", "failed-payment-recovery", "refund-review", "dispute-review"} {
		t.Run(templateID, func(t *testing.T) {
			svc, mock, ctx := newCrewCreationTestEnv(t)
			created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
				UserID: "owner", WorkflowPath: "Workflow/build", Title: "Billing Operator",
				Role: "Billing operations", Purpose: "Review a sourced billing case",
				TemplateID: templateID, StepInstruction: "Return a source-linked decision for review.",
				IdempotencyKey: "billing-pack-" + templateID,
			})
			if err != nil {
				t.Fatal(err)
			}
			base := created.WorkspacePath
			for _, relative := range []string{
				"skills/" + templateID + "/SKILL.md",
				"templates/" + templateID + "/SETUP.md",
				"templates/" + templateID + "/TEMPLATE_SETUP.json",
			} {
				if mock.files[base+"/"+relative] == "" {
					t.Fatalf("Billing pack lacks %s", relative)
				}
			}
			var setup map[string]interface{}
			if err := json.Unmarshal([]byte(mock.files[base+"/templates/"+templateID+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
				t.Fatal(err)
			}
			completed, ok := setup["completed_steps"].([]interface{})
			if !ok || len(completed) != 0 {
				t.Fatalf("Billing pack setup unexpectedly completed: %+v", setup)
			}
		})
	}
}

func TestCreateCrewProjectAppliesMarketingTemplateOnBuilderAction(t *testing.T) {
	for _, templateID := range []string{"competitor-intelligence-analyst", "campaign-performance-analyst", "growth-experiment-planner"} {
		t.Run(templateID, func(t *testing.T) {
			svc, mock, ctx := newCrewCreationTestEnv(t)
			created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
				UserID: "owner", WorkflowPath: "Workflow/build", Title: "Marketing Operator",
				Role: "Marketing operator", Purpose: "Review a sourced marketing result",
				TemplateID: templateID, StepInstruction: "Return source-linked evidence and a decision for owner review.",
				IdempotencyKey: "marketing-" + templateID,
			})
			if err != nil {
				t.Fatal(err)
			}
			base := created.WorkspacePath
			for _, relative := range []string{
				"skills/" + templateID + "/SKILL.md",
				"templates/" + templateID + "/SETUP.md",
				"templates/" + templateID + "/TEMPLATE_SETUP.json",
			} {
				if mock.files[base+"/"+relative] == "" {
					t.Fatalf("Marketing Crew lacks %s", relative)
				}
			}
			var setup map[string]interface{}
			if err := json.Unmarshal([]byte(mock.files[base+"/templates/"+templateID+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
				t.Fatal(err)
			}
			completed, ok := setup["completed_steps"].([]interface{})
			if !ok || len(completed) != 0 {
				t.Fatalf("Marketing setup unexpectedly completed: %+v", setup)
			}
		})
	}
}

func TestCreateCrewProjectAppliesShopifyTemplateOnBuilderAction(t *testing.T) {
	for _, templateID := range []string{"store-operations-coordinator", "returns-refunds-coordinator", "catalog-merchandising-analyst", "shopify-growth-analyst", "payment-operations-investigator", "checkout-recovery-coordinator", "replenishment-planner"} {
		t.Run(templateID, func(t *testing.T) {
			svc, mock, ctx := newCrewCreationTestEnv(t)
			created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
				UserID: "owner", WorkflowPath: "Workflow/build", Title: "Store Operator",
				Role: "Shopify store operator", Purpose: "Review a sourced store exception",
				TemplateID: templateID, StepInstruction: "Return a source-linked result for merchant review.",
				IdempotencyKey: "shopify-" + templateID,
			})
			if err != nil {
				t.Fatal(err)
			}
			base := created.WorkspacePath
			for _, relative := range []string{
				"skills/" + templateID + "/SKILL.md",
				"templates/" + templateID + "/SETUP.md",
				"templates/" + templateID + "/TEMPLATE_SETUP.json",
			} {
				if mock.files[base+"/"+relative] == "" {
					t.Fatalf("Shopify Crew lacks %s", relative)
				}
			}
			var setup struct {
				CompletedSteps []string `json:"completed_steps"`
			}
			if err := json.Unmarshal([]byte(mock.files[base+"/templates/"+templateID+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
				t.Fatal(err)
			}
			if len(setup.CompletedSteps) != 0 {
				t.Fatalf("Shopify setup unexpectedly completed: %+v", setup.CompletedSteps)
			}
		})
	}
}

func TestCreateCrewProjectValidatesInput(t *testing.T) {
	svc, _, ctx := newCrewCreationTestEnv(t)
	valid := CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	}
	for name, mutate := range map[string]func(*CreateCrewRequest){
		"empty title":              func(r *CreateCrewRequest) { r.Title = " " },
		"long title":               func(r *CreateCrewRequest) { r.Title = strings.Repeat("a", 61) },
		"long role":                func(r *CreateCrewRequest) { r.Role = strings.Repeat("a", 121) },
		"long purpose":             func(r *CreateCrewRequest) { r.Purpose = strings.Repeat("a", 2001) },
		"missing role":             func(r *CreateCrewRequest) { r.Role = " " },
		"missing purpose":          func(r *CreateCrewRequest) { r.Purpose = " " },
		"long icon":                func(r *CreateCrewRequest) { r.Icon = strings.Repeat("a", 9) },
		"empty key":                func(r *CreateCrewRequest) { r.IdempotencyKey = "" },
		"bad workflow":             func(r *CreateCrewRequest) { r.WorkflowPath = "Chats/other" },
		"other profile":            func(r *CreateCrewRequest) { r.ProfileID = "crewx" },
		"unknown template":         func(r *CreateCrewRequest) { r.TemplateID = "unknown-specialist" },
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
}

func TestCreateCrewRejectsProductDeniedBeforeWriting(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	t.Setenv("AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES", "work")
	before := len(mock.files)
	_, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Search Researcher",
		Role: "Researcher", Purpose: "Research site opportunities", TemplateID: "search-opportunity-mapper",
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
	occupied := "_users/owner/Chats/Work/projects/" + slug + "-" + crewCreationSuffix("proposal-1")
	mock.files[occupied+"/product.json"] = `{"schema_version":1,"product":"work","id":"someone-else","title":"Other","session_id":"work:project:someone-else"}`
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
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
	mock.files["skills/reviewer/SKILL.md"] = "---\nname: reviewer\ndescription: test reviewer\n---\n# Reviewer\n"
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Role: "Reviewer", Purpose: "Own release quality", Instructions: "Check the changelog first.",
		Skills: []string{"reviewer"}, Servers: []string{"github"},
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
	assertStringSet("selected_skills", "reviewer")
	assertStringSet("selected_servers", "github")
	assertStringSet("selected_secrets", "SHARED_GH")
	assertStringSet("selected_global_secret_names", "SHARED_GH")
	// Re-entry converges without duplicating selections.
	if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Role: "Reviewer", Purpose: "Own release quality", Instructions: "Check the changelog first.",
		Skills: []string{"reviewer"}, Servers: []string{"github"},
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
	assertStringSet("selected_skills", "reviewer")
	assertStringSet("selected_servers", "github")
}

func TestCreateCrewProjectSeedsStarterBrief(t *testing.T) {
	svc, mock, ctx := newCrewCreationTestEnv(t)
	created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Briefed Crew",
		Role: "Reviewer", Purpose: "Own release quality",
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
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
		Skills: []string{"ghost"}, IdempotencyKey: "proposal-1",
	}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("unknown skill err = %v, want install-first failure", err)
	}
	for name, req := range map[string]CreateCrewRequest{
		"secret path":   {UserID: "owner", WorkflowPath: "Workflow/build", Title: "T", Role: "R", Purpose: "P", StepInstruction: "S", Secrets: []string{"a/b"}, IdempotencyKey: "k"},
		"server dotdot": {UserID: "owner", WorkflowPath: "Workflow/build", Title: "T", Role: "R", Purpose: "P", StepInstruction: "S", Servers: []string{".."}, IdempotencyKey: "k"},
		"empty global":  {UserID: "owner", WorkflowPath: "Workflow/build", Title: "T", Role: "R", Purpose: "P", StepInstruction: "S", GlobalSecrets: []string{" "}, IdempotencyKey: "k"},
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
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
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
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
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
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
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
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
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
		Role: "Planner", Purpose: "Plan releases", StepInstruction: "Plan the release.",
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
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Same key, changed title: a conflict, never a second directory with
	// the same crew ID.
	if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer Renamed",
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	}); err == nil || !strings.Contains(err.Error(), "new key") {
		t.Fatalf("changed title err = %v, want a new-key conflict", err)
	}
	crews := map[string]bool{}
	for path := range mock.files {
		if strings.HasSuffix(path, "/product.json") && strings.HasPrefix(path, "_users/owner/Chats/Work/projects/") {
			crews[strings.TrimSuffix(path, "/product.json")] = true
		}
	}
	if len(crews) != 1 {
		t.Fatalf("crew dirs = %v, want exactly the original", crews)
	}
	// A verbatim retry still adopts the original.
	again, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
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
	occupied := "_users/owner/Chats/Work/projects/" + slug + "-" + crewCreationSuffix("proposal-1")
	mock.files[occupied+"/product.json"] = `{"schema_version":1,"product":"work","id":"someone-else","title":"Other","session_id":"work:project:someone-else"}`
	first, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
		IdempotencyKey: "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Re-entry adopts the frozen fallback identity instead of minting
	// another random crew.
	second, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer",
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
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
			Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
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
		Alias: "release-reviewer", Role: "R", Purpose: "P", StepInstruction: "S",
		IdempotencyKey: "proposal-3",
	}); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("taken alias err = %v, want an already-used failure", err)
	}
	mock.files["Workflow/build/planning/plan.json"] = `{"steps":[{"id":"custom-step"}]}`
	if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
		UserID: "owner", WorkflowPath: "Workflow/build", Title: "Other",
		StepID: "custom-step", Role: "R", Purpose: "P", StepInstruction: "S",
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
		before := len(mock.files)
		if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
			UserID: "owner", WorkflowPath: "Workflow/build", Title: "T",
			Servers: []string{"ghost"}, Role: "R", Purpose: "P", StepInstruction: "S",
			IdempotencyKey: "k",
		}); err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("unknown server err = %v, want not-configured", err)
		}
		if len(mock.files) != before {
			t.Fatal("failed validation wrote files")
		}
	})
	t.Run("disconnected server is selected and pending", func(t *testing.T) {
		svc, mock, ctx := newEnv(t)
		created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
			UserID: "owner", WorkflowPath: "Workflow/build", Title: "T",
			Servers: []string{"gitlab"}, Role: "R", Purpose: "P", StepInstruction: "S",
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
			Secrets: []string{"GHOST"}, Role: "R", Purpose: "P", StepInstruction: "S",
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
			Secrets: []string{"WF_ONLY"}, Role: "R", Purpose: "P", StepInstruction: "S",
			IdempotencyKey: "k",
		}); err == nil || !strings.Contains(err.Error(), "do not carry over") {
			t.Fatalf("scoped secret err = %v, want a carry-over hint", err)
		}
	})
	t.Run("unknown global fails", func(t *testing.T) {
		svc, _, ctx := newEnv(t)
		if _, err := svc.CreateCrewProject(ctx, CreateCrewRequest{
			UserID: "owner", WorkflowPath: "Workflow/build", Title: "T",
			GlobalSecrets: []string{"GHOST"}, Role: "R", Purpose: "P", StepInstruction: "S",
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
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
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
		return CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "T", Role: "R", Purpose: "P", StepInstruction: "S", IdempotencyKey: key}
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
		Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review the release.",
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

// RTS: create_crew rejected the built-in agent-browser skill because only the
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
