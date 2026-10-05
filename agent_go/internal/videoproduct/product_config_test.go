package videoproduct

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestVideoStudioConversationPolicyValidates(t *testing.T) {
	profile := BuiltinAgentProfile()
	if err := agentprofiles.Validate(profile); err != nil {
		t.Fatalf("Video Studio profile failed validation: %v", err)
	}
	if profile.Runtime.Workspace.Mode != agentprofiles.WorkspaceModeProject ||
		profile.Runtime.Workspace.ProjectsRoot != "Chats/Video Studio/projects" ||
		profile.Runtime.Conversation.Mode != agentprofiles.ConversationModeKeyed ||
		profile.Runtime.Conversation.KeyType != agentprofiles.ConversationKeyTypeProject {
		t.Fatalf("Video Studio must bind one durable conversation to each server-resolved project: workspace=%+v conversation=%+v", profile.Runtime.Workspace, profile.Runtime.Conversation)
	}
}

func TestVideoStudioManifestDrivesProfileAndWorkflowCapabilities(t *testing.T) {
	manifest, err := VideoStudioManifest()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Profile.ID != "video-studio" || manifest.Profile.Runtime.Capabilities.Browser != "required" || manifest.Profile.Runtime.Capabilities.Secrets != "required" || manifest.Profile.Runtime.Capabilities.LiveInput != "preferred" {
		t.Fatalf("unexpected declarative profile: %+v", manifest.Profile)
	}
	profileSkills := map[string]bool{}
	for _, skill := range manifest.Profile.Skills {
		profileSkills[skill] = true
	}
	for _, required := range []string{"longform-cinematic-video", "multi-clip-cinematic-generation", "video-stitching", "minimax-h3-video"} {
		if !profileSkills[required] {
			t.Fatalf("Video Studio's H3 cinematic profile omits %q: %v", required, manifest.Profile.Skills)
		}
	}
	for _, redundant := range []string{"video-creation", "html-composition"} {
		if profileSkills[redundant] {
			t.Fatalf("Video Studio's skill-only profile still attaches redundant %q: %v", redundant, manifest.Profile.Skills)
		}
	}
	// Video Studio names its surface rather than blocking a list. Anything not
	// named never reaches the agent, so a new AgentWorks tool cannot widen this
	// product by default — the failure mode a deny list has.
	if !manifest.Profile.ToolPolicy.IsAllowlist() {
		t.Fatalf("Video Studio should declare an allow list: %+v", manifest.Profile.ToolPolicy)
	}
	if len(manifest.Profile.ToolPolicy.Disabled) != 0 {
		t.Fatalf("an allow list is the complete surface; a deny list beside it is a second source: %+v", manifest.Profile.ToolPolicy)
	}
	enabled := map[string]bool{}
	for _, name := range manifest.Profile.ToolPolicy.Enabled {
		enabled[name] = true
	}
	// The product controls and the secret CRUD its ui.secrets surface needs.
	// set_workflow_secret is here because it was registered-but-invisible once.
	for _, name := range []string{
		"show_video", "show_reference", "agent_browser",
		"read_image",
		"list_secrets", "set_workflow_secret",
	} {
		if !enabled[name] {
			t.Fatalf("Video Studio needs %q: %+v", name, manifest.Profile.ToolPolicy)
		}
	}
	for _, name := range []string{
		"run_full_workflow", "execute_step", "query_step", "send_step_message",
		"stop_step", "stop_all_executions", "list_executions",
	} {
		if enabled[name] {
			t.Fatalf("Video Studio must not expose workflow tool %q", name)
		}
	}
	// The bridge shell is how product HTTP APIs are reached. Reaching them from
	// each CLI's own shell instead (api_transport.native_shell) is implemented
	// but deliberately not enabled — every provider can call the bridge as an
	// MCP tool, and Codex can ONLY do that. See
	// docs/design/product_api_transport_for_coding_agents.md.
	if !enabled["execute_shell_command"] {
		t.Fatalf("Video Studio reaches product APIs through the bridge shell; removing it leaves Codex with no path at all: %+v", manifest.Profile.ToolPolicy)
	}
	if mode := strings.TrimSpace(manifest.Profile.Runtime.APITransport.Mode); mode == "native_shell" {
		t.Fatalf("native_shell is intentionally off for now (see docs/design/product_api_transport_for_coding_agents.md); enabling it needs the bridge-shell entry above reconsidered too")
	}
	for id, wantKind := range map[string]string{"video.show-video": "media.video", "video.show-reference": "media.reference"} {
		var binding *agentprofiles.ToolBinding
		for i := range manifest.Profile.Tools {
			if manifest.Profile.Tools[i].ID == id {
				binding = &manifest.Profile.Tools[i]
			}
		}
		if binding == nil || binding.Presentation == nil || binding.Presentation.Kind != wantKind {
			t.Fatalf("%s must declare presentation.kind=%s, got %+v", id, wantKind, binding)
		}
	}
	// Video Studio is MCP-only, so the bridge supplies the guarded editor.
	if manifest.Profile.Runtime.AgentTools.Mode == "mcp_only" && !enabled["diff_patch_workspace_file"] {
		t.Fatalf("agent_tools.mode=%q denies native edits; the bridge must carry diff_patch_workspace_file: %+v",
			manifest.Profile.Runtime.AgentTools.Mode, manifest.Profile.ToolPolicy)
	}
	// AgentWorks-wide administration, the shared media/LLM bridge, sub-agent
	// orchestration, and scheduling are not this product's business.
	for _, name := range []string{
		"set_provider_auth", "install_skill", "add_mcp_server",
		"generate_video",
		"delegate", "query_agent", "create_workflow_schedule", "notify_user",
	} {
		if enabled[name] {
			t.Fatalf("Video Studio should not carry %q: %+v", name, manifest.Profile.ToolPolicy)
		}
	}
	if manifest.UI.Surface != "video-studio" || !manifest.UI.Secrets || manifest.Branding.Favicon != "/video-studio-favicon.svg" {
		t.Fatalf("unexpected UI/branding definition: %+v %+v", manifest.UI, manifest.Branding)
	}
	if manifest.Profile.Runtime.Transport != "auto" {
		t.Fatalf("Video Studio runtime transport = %q, want auto", manifest.Profile.Runtime.Transport)
	}
	if manifest.Profile.Runtime.AgentTools.Mode != "mcp_only" || manifest.Profile.Runtime.Approvals.Mode != "provider_auto" {
		t.Fatalf("Video Studio tool policy = %+v %+v, want mcp_only/provider_auto", manifest.Profile.Runtime.AgentTools, manifest.Profile.Runtime.Approvals)
	}
	if manifest.Workflows.BrowserMode != "auto" || len(manifest.Workflows.SelectedSkills) == 0 {
		t.Fatalf("unexpected workflow definition: %+v", manifest.Workflows)
	}

	// Every tool that can write declares where this product's artifacts belong.
	// Without a declaration the tool description says nothing about layout —
	// and the shared AgentWorks default it used to inherit (plan folders,
	// pulse/goals.html) names concepts Video Studio does not have and
	// contradicts the uploads//work//outputs/ rules in its own system prompt.
	for _, writer := range []string{"execute_shell_command", "diff_patch_workspace_file"} {
		if !enabled[writer] {
			continue
		}
		if len(manifest.Profile.Runtime.Workspace.Placement[writer]) == 0 {
			t.Fatalf("%s is exposed but declares no runtime.workspace.placement: %+v", writer, manifest.Profile.Runtime.Workspace)
		}
	}
}

func TestVideoStudioIsPinnedToClaudeCodeWithoutProviderChoices(t *testing.T) {
	manifest, err := VideoStudioManifest()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Profile.Runtime.Provider != "claude-code" || manifest.Profile.Runtime.ModelID != DefaultClaudeModel {
		t.Fatalf("Video Studio runtime = %s/%s, want claude-code/%s", manifest.Profile.Runtime.Provider, manifest.Profile.Runtime.ModelID, DefaultClaudeModel)
	}
	if manifest.Profile.Runtime.CredentialScope != agentprofiles.CredentialScopeGlobal {
		t.Fatalf("Video Studio credential scope = %q, want global", manifest.Profile.Runtime.CredentialScope)
	}
	if len(manifest.Profile.Runtime.ProviderOptions) != 0 {
		t.Fatalf("Video Studio exposes provider choices despite its Claude-only contract: %+v", manifest.Profile.Runtime.ProviderOptions)
	}
}

// Plan-authoring guidance (frontend/src/commands/builtin-commands.tsx): use
// message_sequence "for every conversational, judgment-heavy, browser-driven, or
// adaptive step, even when it needs only one message. Non-scripted regular steps
// are unsupported." Every production stage here is exactly that — writing a
// brief, a storyboard, a design, a critique — none is a deterministic script,
// so none qualifies for `regular` (which since PLAT-287 means scripted).
//
// They previously authored as `regular` and ran only because the runtime
// rewrites non-scripted regular steps into this same shape
// (normalizeRegularStepToMessageSequence), which left the stored plan
// describing an execution model that no longer exists and forced the plan UI to
// reconstruct the real one.
func TestGeneratedPlanAuthorsStagesAsMessageSequences(t *testing.T) {
	raw, err := json.Marshal(planForAll(pipelineRegistry))
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Steps []struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Items []struct {
				Type string `json:"type"`
			} `json:"items"`
			Routes []struct {
				RouteID string `json:"route_id"`
				Sub     struct {
					ID    string `json:"id"`
					Type  string `json:"type"`
					Items []struct {
						Type string `json:"type"`
					} `json:"items"`
				} `json:"sub_agent_step"`
			} `json:"predefined_routes"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}

	assertStage := func(id, stepType string, items int, firstItem string) {
		t.Helper()
		if stepType != "message_sequence" {
			t.Fatalf("stage %q has type %q; non-scripted stages must author as message_sequence", id, stepType)
		}
		if items == 0 {
			t.Fatalf("stage %q declares no items; the runtime would have to synthesize the turn again", id)
		}
		if firstItem != "user_message" {
			t.Fatalf("stage %q first item is %q, want user_message", id, firstItem)
		}
	}

	stages := 0
	for _, step := range plan.Steps {
		switch step.Type {
		case "routing":
			continue // a router branches; it runs no stage turn
		case "todo_task":
			t.Fatalf("generated Video Studio plan still contains orchestrator step %q; every production task must be an individually runnable message_sequence", step.ID)
		}
		stages++
		first := ""
		if len(step.Items) > 0 {
			first = step.Items[0].Type
		}
		assertStage(step.ID, step.Type, len(step.Items), first)
	}
	if stages == 0 {
		t.Fatal("no stage steps generated")
	}
}

func TestVideoStudioPromptExpandsAllowListedVariables(t *testing.T) {
	prompt := renderProductPrompt("AgentWorks launch", "Friday at 2:30 PM IST")
	for _, required := range []string{"You are Video Studio", "Friday at 2:30 PM IST"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("rendered prompt missing %q: %s", required, prompt)
		}
	}
	if strings.Contains(prompt, "AgentWorks launch") || strings.Contains(prompt, "{{TIME}}") || strings.Contains(prompt, "{{PRODUCT_NAME}}") {
		t.Fatalf("prompt variables were not expanded: %s", prompt)
	}
}

// The creative critique writes "the exact acceptance criteria the builder must
// prove". The builder is the first stage after the individual critique, so if its
// instructions never name the scorecard, those criteria are written and then
// read by nobody until the composition critique — one stage too late to steer
// the build they were written for.
func TestBuilderIsPointedAtTheCriteriaTheCritiqueWrote(t *testing.T) {
	var critique, design *PipelineStage
	for i := range infographicPipeline.Stages {
		switch infographicPipeline.Stages[i].ID {
		case "infographic-creative-critique":
			critique = &infographicPipeline.Stages[i]
		case "infographic-design":
			design = &infographicPipeline.Stages[i]
		}
	}
	if critique == nil || design == nil {
		t.Fatal("creative critique or design stage is missing from the infographic pipeline")
	}
	for _, artifact := range append([]string{critique.Output}, critique.Artifacts...) {
		if !strings.Contains(design.Description, artifact) {
			t.Fatalf("the build stage never mentions %q, so the critique's acceptance criteria reach no builder", artifact)
		}
	}
}

// Every individual stage needs its own step_config entry. A missing entry makes
// that step silently fall back to platform defaults and lose its declared
// production skills.
func TestIndividualStagesKeepTheirSkillEntries(t *testing.T) {
	raw, err := json.Marshal(stepConfigForAll(pipelineRegistry))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Steps []struct {
			ID     string `json:"id"`
			Agents struct {
				Skills []string `json:"enabled_skills"`
			} `json:"agent_configs"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	skillsByID := map[string][]string{}
	for _, step := range config.Steps {
		skillsByID[step.ID] = step.Agents.Skills
	}

	for _, p := range pipelineRegistry {
		for i := range p.Stages {
			stage := &p.Stages[i]
			got, ok := skillsByID[stage.ID]
			if !ok {
				t.Fatalf("individual stage %q has no step_config entry", stage.ID)
			}
			wantAttach, _ := splitStageSkills(stage.Skills)
			if !reflect.DeepEqual(got, wantAttach) {
				t.Fatalf("stage %q attaches %v, want %v", stage.ID, got, wantAttach)
			}
			if len(wantAttach) > 0 && len(got) == 0 {
				t.Fatalf("stage %q declares attachable skills but its config carries none", stage.ID)
			}
		}
	}
}

func TestGeneratedPlanContainsNoOrchestratorSteps(t *testing.T) {
	raw, err := json.Marshal(planForAll(pipelineRegistry))
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Steps []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Next string `json:"next_step_id"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	for _, s := range plan.Steps {
		if s.Type == "todo_task" {
			t.Fatalf("found orchestrator %q in Video Studio plan", s.ID)
		}
	}
}

// product.yaml installs twelve HyperFrames skills and declares attach:
// [hyperframes]; productdeps describes the rest as "ordinary files for
// progressive disclosure", and product-infographic routes the agent to read
// them at skills/<name>/SKILL.md.
//
// enabled_skills drove BOTH attachment and the folder guard, so every stage
// asked to attach all twelve. That failed silently while the skills loader
// could only see user-level skills; once it became workspace-aware the same
// declaration would have loaded eleven specialist skills into every stage —
// exactly what the product prompt forbids. This pins the split.
func TestStagesAttachOnlyDeclaredAttachableSkills(t *testing.T) {
	installed, attachable := managedSkillPolicy()
	if !attachable["hyperframes"] {
		t.Fatal("product.yaml no longer declares hyperframes attachable; this test encodes that contract")
	}

	raw, err := json.Marshal(stepConfigForAll(pipelineRegistry))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Steps []struct {
			ID     string `json:"id"`
			Agents struct {
				Skills []string `json:"enabled_skills"`
				Paths  []string `json:"additional_read_paths"`
			} `json:"agent_configs"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}

	byID := map[string]int{}
	for i, step := range config.Steps {
		byID[step.ID] = i
	}
	for _, p := range pipelineRegistry {
		for _, stage := range p.Stages {
			idx, ok := byID[stage.ID]
			if !ok {
				t.Fatalf("stage %q has no step_config entry", stage.ID)
			}
			got := config.Steps[idx].Agents
			for _, name := range got.Skills {
				if installed[name] && !attachable[name] {
					t.Errorf("stage %q attaches %q, which the product installs as a read-from-disk skill", stage.ID, name)
				}
			}
			// Whatever left enabled_skills must still be reachable: the folder
			// guard is built from enabled_skills, so a skill the agent is told
			// to read is unopenable without an explicit read path.
			for _, name := range stage.Skills {
				if !installed[name] || attachable[name] {
					continue
				}
				want := "skills/" + name
				var granted bool
				for _, p := range got.Paths {
					if p == want {
						granted = true
					}
				}
				if !granted {
					t.Errorf("stage %q must read %s but has no read path for it (paths=%v)", stage.ID, want, got.Paths)
				}
			}
		}
	}
}
