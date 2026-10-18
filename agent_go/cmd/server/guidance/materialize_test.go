package guidance

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var topicBracePattern = regexp.MustCompile(`((?:[a-z0-9]+-)+)\{([^}]*)\}`)

var builderReferenceReadPattern = regexp.MustCompile(`"name":"builder-reference","path":"(references/[^"]+\.md)"`)

func TestBuilderSecretReferencesDescribeManagedVaultSharing(t *testing.T) {
	for _, mode := range []string{"workshop", "multi-agent"} {
		t.Run(mode, func(t *testing.T) {
			text := materializedFileContent(t, MaterializeReferenceSkill(mode), "references/secret-management.md")
			for _, want := range []string{"Share to Vault", "manage_vault_secret_access", "project copy stays", "search_tools/get_api_spec"} {
				if !strings.Contains(text, want) {
					t.Errorf("attached secret reference omits %q", want)
				}
			}
			for _, stale := range []string{"set_user_secret", "Global secrets** — operator-managed", "global** (read-only)"} {
				if strings.Contains(text, stale) {
					t.Errorf("attached secret reference retains stale guidance %q", stale)
				}
			}
		})
	}
	workflow := materializedFileContent(t, MaterializeReferenceSkill("workshop"), "references/workflow-tools.md")
	if !strings.Contains(workflow, "Share to Vault") || !strings.Contains(workflow, "keeps the project copy") {
		t.Fatal("workflow tools reference omits managed Vault copy semantics")
	}
}

func TestMegaSkillDiscoveryFitsFormatAndKeepsEveryTopic(t *testing.T) {
	check := func(t *testing.T, skill *llmtypes.Skill) {
		t.Helper()
		if skill == nil {
			return
		}
		if n := utf8.RuneCountInString(skill.Description); n == 0 || n > 1024 {
			t.Fatalf("%s description has %d characters; skill metadata must have 1–1024", skill.Name, n)
		}
		if strings.ContainsAny(skill.Description, "<>") {
			t.Fatalf("%s description contains angle brackets rejected by skill validators", skill.Name)
		}
		// Topics sharing a first word are written once: pulse-{gate,finalizer}.
		discoverable := topicBracePattern.ReplaceAllStringFunc(skill.Description, func(group string) string {
			parts := topicBracePattern.FindStringSubmatch(group)
			names := strings.Split(parts[2], ",")
			for i, rest := range names {
				names[i] = parts[1] + rest
			}
			return strings.Join(names, ",")
		})
		for _, file := range skill.SupportingFiles {
			topic := strings.TrimSuffix(strings.TrimPrefix(file.RelPath, "references/"), ".md")
			if !strings.Contains(discoverable, topic) {
				t.Errorf("%s is missing from %s discovery metadata", topic, skill.Name)
			}
			if !strings.Contains(skill.Content, file.RelPath) || len(file.Content) == 0 {
				t.Errorf("%s must remain readable through the full reference index", file.RelPath)
			}
		}
	}
	for _, mode := range []string{"workshop", "multi-agent", "run", ""} {
		t.Run(mode, func(t *testing.T) {
			check(t, MaterializeReferenceSkill(mode))
			check(t, MaterializeGuidanceSkill(mode))
		})
	}
	var tools []string
	for _, meta := range referenceKinds {
		tools = append(tools, meta.Tools...)
	}
	t.Run("step with all capabilities", func(t *testing.T) {
		check(t, MaterializeStepExecutionReferenceSkill(StepExecutionSignals{
			ToolNames: tools, CodeExecutionMode: true, ScriptedStep: true,
		}))
	})
}

func TestHumanInTheLoopReferenceIsAttachedForWorkflowModes(t *testing.T) {
	const path = "references/human-in-the-loop.md"
	for _, mode := range []string{"workshop", "run"} {
		t.Run(mode, func(t *testing.T) {
			var reference *llmtypes.Skill
			if err := AttachReferenceSurface(mode, func(skill *llmtypes.Skill) error {
				if skill.Name == "builder-reference" {
					reference = skill
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if reference == nil || !strings.Contains(reference.Content, path) {
				t.Fatal("human-in-the-loop reference is not discoverable in the attached skill")
			}
			content := materializedFileContent(t, reference, path)
			canonical, err := renderReferenceKind("human-in-the-loop", tmplData{})
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(content) == "" || content != canonical {
				t.Fatal("attached human-in-the-loop reference differs from canonical rendered guidance")
			}
		})
	}
}

func TestRunReferenceSurfaceExcludesWorkshopMaintenanceSkills(t *testing.T) {
	var attached = map[string]*llmtypes.Skill{}
	if err := AttachReferenceSurface("run", func(skill *llmtypes.Skill) error {
		attached[skill.Name] = skill
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if attached["workflow-commands"] != nil {
		t.Fatal("Run should not receive Workshop slash-command procedures")
	}
	reference := attached["builder-reference"]
	if reference == nil {
		t.Fatal("Run should retain its runtime reference bundle")
	}
	for _, path := range []string{
		"references/pulse-gate.md",
		"references/pulse-review-fixer.md",
		"references/plan-drift-review.md",
		"references/stores.md",
		"references/secret-management.md",
		"references/backup-strategy.md",
	} {
		if strings.Contains(reference.Content, path) {
			t.Fatalf("Run reference bundle still advertises Workshop-only %s", path)
		}
	}
	for _, path := range []string{
		"references/runtime-context.md",
		"references/running-steps.md",
		"references/human-in-the-loop.md",
	} {
		if !strings.Contains(reference.Content, path) {
			t.Fatalf("Run reference bundle lost runtime guidance %s", path)
		}
	}
}

func materializedFileContent(t *testing.T, skill *llmtypes.Skill, relPath string) string {
	t.Helper()
	if skill == nil {
		t.Fatal("skill is nil")
	}
	for _, file := range skill.SupportingFiles {
		if file.RelPath == relPath {
			return string(file.Content)
		}
	}
	t.Fatalf("missing supporting file %q; files=%v", relPath, skill.SupportingFiles)
	return ""
}

func TestMaterializeReferenceKindsAsSkillsRendersEachIndividually(t *testing.T) {
	skills, err := MaterializeReferenceKindsAsSkills("multi-agent", []string{"backup-strategy", "secret-management"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}
	if skills[0].Name != "backup-strategy" || skills[1].Name != "secret-management" {
		t.Fatalf("unexpected skill names/order: %q, %q", skills[0].Name, skills[1].Name)
	}
	for _, skill := range skills {
		if strings.TrimSpace(skill.Content) == "" {
			t.Fatalf("skill %q has empty content", skill.Name)
		}
		if strings.TrimSpace(skill.Description) == "" {
			t.Fatalf("skill %q has empty description", skill.Name)
		}
		if skill.Source.Origin != "builtin" {
			t.Fatalf("skill %q source = %+v, want builtin", skill.Name, skill.Source)
		}
	}
	// Same underlying render as the mode-gated bundle, not duplicated content
	// under a different implementation -- the individually-named skill and
	// the bundle's per-kind supporting file should be byte-for-byte the same.
	bundle := MaterializeReferenceSkill("multi-agent")
	bundled := materializedFileContent(t, bundle, "references/backup-strategy.md")
	if skills[0].Content != bundled {
		t.Fatalf("individually-materialized backup content diverges from the bundle's own copy")
	}
}

func TestMaterializeReferenceKindsAsSkillsRejectsUnknownName(t *testing.T) {
	if _, err := MaterializeReferenceKindsAsSkills("multi-agent", []string{"not-a-real-kind"}); err == nil {
		t.Fatal("expected an error for an unknown reference kind")
	}
}

func TestMaterializeReferenceKindsAsSkillsRejectsModeNotAllowed(t *testing.T) {
	// "workflow-tools" is a real referenceKinds entry, but only allowed in
	// "workshop" mode -- requesting it for "multi-agent" must fail, not
	// silently render it anyway.
	if _, err := MaterializeReferenceKindsAsSkills("multi-agent", []string{"workflow-tools"}); err == nil {
		t.Fatal("expected an error for a kind not allowed in the requested mode")
	}
}

func TestMaterializedReferenceSkillIncludesConfigToolOnlyDocs(t *testing.T) {
	skill := MaterializeReferenceSkill("workshop")
	if skill == nil {
		t.Fatal("expected builder-reference skill")
	}

	for _, want := range []string{"LLM/provider configuration via tools", "not by reading or editing `config/` files", "references/llm-selection.md", "references/workspace-media-tools.md"} {
		if !strings.Contains(skill.Description+skill.Content, want) {
			t.Fatalf("builder-reference skill should contain %q\nDescription:\n%s\nContent:\n%s", want, skill.Description, skill.Content)
		}
	}

	llmSelection := materializedFileContent(t, skill, "references/llm-selection.md")
	for _, want := range []string{"list_published_llms", "set_provider_auth", "never paste API keys into shell or config files"} {
		if !strings.Contains(llmSelection, want) {
			t.Fatalf("llm-selection reference should contain %q\n%s", want, llmSelection)
		}
	}
	for _, banned := range []string{"config/published-llms.json", "config/provider-api-keys.json"} {
		if strings.Contains(llmSelection, banned) {
			t.Fatalf("llm-selection reference should not expose raw config file %q\n%s", banned, llmSelection)
		}
	}

	// PLAT-244 deliberately narrowed the active workspace provider surface to
	// generate_text_llm and hid media tools (image/video/audio/
	// music/transcription); this
	// reference's content, and this test's expectations, changed with it.
	mediaTools := materializedFileContent(t, skill, "references/workspace-media-tools.md")
	for _, want := range []string{"set_provider_auth", "deprecated and hidden", "## Scripted workflow use", "$MCP_CUSTOM/generate_text_llm", "references/mcp-bridge.md"} {
		if !strings.Contains(mediaTools, want) {
			t.Fatalf("workspace-media-tools reference should contain %q\n%s", want, mediaTools)
		}
	}
	for _, banned := range []string{"config/published-llms.json", "config/provider-api-keys.json", "config/image-generation-config.json", "config/image-analysis-config.json"} {
		if strings.Contains(mediaTools, banned) {
			t.Fatalf("workspace-media-tools reference should not expose raw config file %q\n%s", banned, mediaTools)
		}
	}
}

func TestStandaloneBugReviewCommandIsRetired(t *testing.T) {
	if _, exists := allKinds["bug-review"]; exists {
		t.Fatal("standalone bug-review remains registered; Engineering Review owns bug diagnosis and fixes")
	}
	if _, err := os.Stat("templates/review/bug-review.md"); !os.IsNotExist(err) {
		t.Fatalf("retired standalone bug-review template still exists: %v", err)
	}
	if got := MaterializeGuidanceSkill("workshop"); got != nil && strings.Contains(got.Content, "bug-review") {
		t.Fatal("workflow-commands skill still advertises retired bug-review")
	}
}

// Guidance may name a reference only through read_skill. The reference bundle
// is the authority for those paths, so a deleted or renamed file must fail in
// tests rather than reaching an agent as an opaque runtime error.
func TestGuidanceDoesNotRequestMissingBuilderReferenceFile(t *testing.T) {
	available := map[string]bool{}
	for _, mode := range []string{"workshop", "multi-agent"} {
		bundle := MaterializeReferenceSkill(mode)
		if bundle == nil {
			t.Fatalf("expected %s builder-reference bundle", mode)
		}
		for _, file := range bundle.SupportingFiles {
			available[file.RelPath] = true
		}
	}

	registries := []map[string]kindMeta{allKinds, referenceKinds}
	for _, registry := range registries {
		for kind := range registry {
			rendered, err := renderFromRegistry(kind, tmplData{}, registry)
			if err != nil {
				t.Errorf("render %s: %v", kind, err)
				continue
			}
			for _, match := range builderReferenceReadPattern.FindAllStringSubmatch(rendered, -1) {
				if strings.Contains(match[1], "<") {
					continue // documented placeholder, not a literal request
				}
				if !available[match[1]] {
					t.Errorf("%s requests %q from builder-reference, but that file is not bundled", kind, match[1])
				}
			}
		}
	}
}

func TestMaterializedReferenceSkillUsesMultiAgentSurface(t *testing.T) {
	skill := MaterializeReferenceSkill("multi-agent")
	if skill == nil {
		t.Fatal("expected builder-reference skill")
	}
	if skill.Name != "builder-reference" {
		t.Fatalf("skill name = %q, want builder-reference", skill.Name)
	}

	for _, want := range []string{"Product chat reference docs", "references/llm-provider-config.md", "references/secret-management.md"} {
		if !strings.Contains(skill.Description+skill.Content, want) {
			t.Fatalf("builder-reference skill should contain %q\nDescription:\n%s\nContent:\n%s", want, skill.Description, skill.Content)
		}
	}
	if strings.Contains(skill.Content, "references/llm-selection.md") {
		t.Fatalf("builder-reference should not expose workflow-only llm-selection\n%s", skill.Content)
	}

	llmProviderConfig := materializedFileContent(t, skill, "references/llm-provider-config.md")
	for _, want := range []string{"list_published_llms", "list_provider_models", "save_published_llm", "reasoning_effort", "Never inspect or edit `config/` files"} {
		if !strings.Contains(llmProviderConfig, want) {
			t.Fatalf("llm-provider-config reference should contain %q\n%s", want, llmProviderConfig)
		}
	}
	for _, banned := range []string{"context_window", "input_cost_per_1m", "temperature", "tool-call settings"} {
		if strings.Contains(llmProviderConfig, `"`+banned+`"`) {
			t.Fatalf("llm-provider-config should not present %q as a stored JSON field\n%s", banned, llmProviderConfig)
		}
	}
}

func TestSystemToolsSkillTeachesConfigToolOnlyAccess(t *testing.T) {
	skill := BuildSystemToolsSkill("workshop")
	if skill == nil {
		t.Fatal("expected system-tools skill")
	}
	for _, want := range []string{"## Configuration access", "LLM/provider configuration is tool-managed", "Do not read or edit `config/` files", "read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/llm-provider-config.md\"}])", "read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/llm-selection.md\"}])", "read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/workspace-media-tools.md\"}])"} {
		if !strings.Contains(skill.Content, want) {
			t.Fatalf("system-tools skill should contain %q\n%s", want, skill.Content)
		}
	}
}

func TestSystemToolsSkillDoesNotPointMultiAgentAtWorkflowOnlyLLMSelection(t *testing.T) {
	skill := BuildSystemToolsSkill("multi-agent")
	if skill == nil {
		t.Fatal("expected system-tools skill")
	}
	for _, want := range []string{"read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/llm-provider-config.md\"}])", "list_published_llms", "save_published_llm"} {
		if !strings.Contains(skill.Content, want) {
			t.Fatalf("multi-agent system-tools skill should contain %q\n%s", want, skill.Content)
		}
	}
	if strings.Contains(skill.Content, "read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/llm-selection.md\"}])") {
		t.Fatalf("multi-agent system-tools should not mention workflow-only llm-selection\n%s", skill.Content)
	}
}

func TestMCPReferenceFollowsCapabilityAdmission(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		skill := materializeReferenceSkillWithMCP("workshop", allowed)
		found := false
		for _, file := range skill.SupportingFiles {
			if file.RelPath == "references/integration-discovery.md" {
				found = true
			}
		}
		if found != allowed {
			t.Fatalf("integration reference present=%v permission=%v", found, allowed)
		}
	}
}

func TestUserManagementReferenceIsBuilderOnly(t *testing.T) {
	builder, err := MaterializeReferenceKindsAsSkills("workshop", []string{"user-management"})
	if err != nil || len(builder) != 1 {
		t.Fatalf("builder reference: %v", err)
	}
	run, err := MaterializeReferenceKindsAsSkills("run", []string{"user-management"})
	if err == nil && len(run) != 0 {
		t.Fatal("Run receives user-management skill")
	}
}

func TestWebhookReferenceIsBuilderOnly(t *testing.T) {
	builder, err := MaterializeReferenceKindsAsSkills("workshop", []string{"webhook-triggers"})
	if err != nil || len(builder) != 1 {
		t.Fatalf("webhook skill: %v", err)
	}
	content := builder[0].Content
	for _, want := range []string{"payload_mappings", "MAIN_ROUTING_STEP_ID", "COMPONENT_BRANCH_STEP_ID", "deterministic route", "returns 400"} {
		if !strings.Contains(content, want) {
			t.Errorf("webhook skill missing payload-mapping guidance %q", want)
		}
	}
	if run, err := MaterializeReferenceKindsAsSkills("run", []string{"webhook-triggers"}); err == nil && len(run) > 0 {
		t.Fatal("Run receives webhook management skill")
	}
}
