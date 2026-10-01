package step_based_workflow

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/instructions"
)

// executeRealisticWorkshopPromptForMode covers the template plus bridge only.
// The complete server assembly budget is tested by TestAssembledWorkflowPrompt
// in cmd/server, including workspace, capability, channel, and runtime additions.
func executeRealisticWorkshopPromptForMode(t *testing.T, mode string) string {
	t.Helper()
	name := "interactiveWorkshopSystem"
	if mode != "workshop" {
		name = "interactiveRunSystem"
	}
	rendered, err := ExecuteTemplate(name, map[string]string{
		"AbsDocsRoot":                       "/app/workspace-docs",
		"AbsWorkspacePath":                  "/app/workspace-docs/Workflow/example",
		"AvailableGroups":                   "group-1",
		"BrowserPrompt":                     "",
		"Focus":                             "",
		"GroupName":                         "",
		"Instruction":                       "",
		"IsCodeExecutionMode":               "true",
		"UseProjectedReferenceSkills":       "true",
		"MainPyAuthoringRules":              "",
		"Mode":                              "",
		"PlanJSON":                          "{}",
		"ProgressSummary":                   "",
		"RunFolder":                         "",
		"SecretPrompt":                      "",
		"SkillPrompt":                       "",
		"SpecialWorkspaceToolsInstructions": instructions.GetSpecialWorkspaceToolsPointer(),
		"StepConfigSummary":                 "",
		"StepID":                            "",
		"StepSummary":                       "",
		"StepsToReview":                     "",
		"TargetRunFolder":                   "",
		"UseKnowledgebase":                  "false",
		"UserRequest":                       "",
		"WorkflowObjective":                 "Build a reliable workflow.",
		"WorkflowSuccessCriteria":           "It runs end to end.",
		"WorkshopMode":                      mode,
		"WorkspacePath":                     "Workflow/example",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate returned error: %v", err)
	}

	// The workshop execution path appends this after rendering the template
	// for every coding CLI. It must be included in the budget: omitting it
	// previously let the test pass while the actual CLAUDE.md could cross the
	// provider's 40k-character limit.
	return rendered + "\n\n" + BuildCodeExecutionSection(true, "Workflow/example")
}

// Template + bridge budget only. The server's production composer has its
// own stricter regression against the fully assembled fixture, including all
// optional sections. Keep this local check for template regressions.
const MaxWorkshopPromptBytes = 11_000

// Structural/content checks cover critical rules separately; this lower floor
// catches accidental empty rendering without requiring verbose instructions.
const MinWorkshopPromptBytes = 4_500

// shellVerbs are command names that, when they open an inline code span in the
// workshop prompt, mark that span as a shell command the agent may paste.
//
// Every such span must carry an absolute path. The shell's working directory is
// NOT guaranteed: workspace/execute_shell_command.go resolves cwd through a
// four-level fallback (explicit param → session config → client default →
// _DEFAULT_WORKING_DIR → unset), and mcpagent's codeexec/shell.go leaves
// cmd.Dir unset when no working_directory is passed, so the child inherits the
// server process cwd rather than the workspace. A relative example that happens
// to resolve under one fallback silently reads the wrong file under another.
// The prompt therefore states one rule — always prefix with
// {{.AbsWorkspacePath}}/ — and this test keeps the prompt's own examples honest.
var shellVerbs = []string{
	"jq", "cat", "ls", "sqlite3", "python3", "python", "head", "tail",
	"grep", "sed", "awk", "rm", "mv", "cp", "mkdir", "touch", "cd",
}

// inlineCodeSpans returns the contents of every single-line `inline code` span
// in text, skipping fenced blocks (illustrative code, not commands pasted as-is)
// and multi-line spans.
func inlineCodeSpans(text string) []string {
	var spans []string
	// Segments at odd indices sit inside a fence — drop them.
	for i, segment := range strings.Split(text, "```") {
		if i%2 == 1 {
			continue
		}
		parts := strings.Split(segment, "`")
		for j := 1; j < len(parts); j += 2 {
			if s := strings.TrimSpace(parts[j]); s != "" && !strings.Contains(s, "\n") {
				spans = append(spans, s)
			}
		}
	}
	return spans
}

// TestWorkshopPromptShellExamplesUseAbsolutePaths locks in the path convention
// declared under "## File layout". The prompt previously said "always use
// absolute paths… do not use relative paths" while handing out seven relative
// example commands (jq/cat over planning/plan.json, ls over builder/, cat over
// variables/variables.json) — a self-contradiction of exactly the kind that
// makes a model burn reasoning reconciling its own instructions.
//
// Nouns are still allowed to be bare: `planning/plan.json` naming a file for
// discussion is fine, and this test ignores it. Only spans that open with a
// shell verb are treated as commands.
func TestWorkshopPromptShellExamplesUseAbsolutePaths(t *testing.T) {
	// Must match the AbsWorkspacePath passed by the size-test helper.
	const absWorkspace = "/app/workspace-docs/Workflow/example"

	prompt := executeRealisticWorkshopPromptForMode(t, "workshop")
	if !strings.Contains(prompt, absWorkspace) {
		t.Fatalf("rendered prompt never mentions %q — executeRealisticWorkshopPromptForMode's "+
			"AbsWorkspacePath changed; update absWorkspace in this test to match", absWorkspace)
	}

	// A stray unmatched backtick silently re-pairs every span after it, which
	// would both mangle the prompt's own formatting and blind the scan below.
	// Catch it as its own failure rather than letting it degrade this test.
	if n := strings.Count(prompt, "`"); n%2 != 0 {
		t.Errorf("rendered prompt has %d backticks (odd) — an unmatched backtick mangles "+
			"inline-code formatting and mis-pairs every span after it", n)
	}

	isShellVerb := func(s string) bool {
		for _, v := range shellVerbs {
			if s == v {
				return true
			}
		}
		return false
	}

	for _, span := range inlineCodeSpans(prompt) {
		verb, rest, _ := strings.Cut(span, " ")
		if rest == "" || !isShellVerb(verb) {
			continue // a bare tool name or a filename noun, not a command
		}
		// This is a syntax-pattern label, not a complete shell command and it
		// neither reads nor writes a filesystem path.
		if span == "jq -n --arg" {
			continue
		}
		if span == "cd project && ..." {
			continue // explicit private linked-CLI exception, never a bridge cwd assumption
		}
		if verb == "cd" {
			t.Errorf("workshop prompt contains a `cd` command (%q), which the same prompt forbids. "+
				"Use an absolute path instead.", span)
			continue
		}
		// $-anchored paths ($DB_PATH, $STEP_OUTPUT_DIR, $MCP_*) are absolute by
		// construction — the runtime exports them.
		if strings.Contains(span, absWorkspace) || strings.Contains(span, "$") {
			continue
		}
		t.Errorf("shell example %q uses a relative path. The shell working directory is not "+
			"guaranteed — prefix the path with {{.AbsWorkspacePath}}/ (renders as %s).",
			span, absWorkspace)
	}
}

// TestWorkshopPromptSize logs the current rendered size for the canonical
// workshop mode and fails if it exceeds MaxWorkshopPromptBytes. The legacy
// modes ("builder", "optimizer", "reporting") were merged into "workshop"
// in the prompt-restructure migration and no longer exist as distinct
// template branches; persisted callers passing those strings are normalized
// at the input boundary before reaching the template.
func TestWorkshopPromptSize(t *testing.T) {
	for _, mode := range []string{"workshop"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			prompt := executeRealisticWorkshopPromptForMode(t, mode)
			size := len(prompt)
			estTokens := size / 4

			// Always log — gives visibility on every CI run.
			t.Logf("Workshop prompt (mode=%s): %d bytes (~%d tokens). Ceiling=%d (~%d tokens), floor=%d.",
				mode, size, estTokens,
				MaxWorkshopPromptBytes, MaxWorkshopPromptBytes/4,
				MinWorkshopPromptBytes)

			if size > MaxWorkshopPromptBytes {
				t.Errorf("workshop prompt (mode=%s) %d bytes exceeds ceiling %d (~%d tokens). "+
					"Move sections to templates/system/*.md and reference them via read_skill.",
					mode, size, MaxWorkshopPromptBytes, estTokens)
			}
			if size < MinWorkshopPromptBytes {
				t.Errorf("workshop prompt (mode=%s) %d bytes below floor %d — sections likely missing.",
					mode, size, MinWorkshopPromptBytes)
			}
		})
	}
}

func TestWorkshopCLIPromptUsesProjectedWorkspaceToolReference(t *testing.T) {
	prompt := executeRealisticWorkshopPromptForMode(t, "workshop")
	for _, reference := range []string{
		"references/workspace-media-tools.md",
		"references/workflow-chat.md",
	} {
		if !strings.Contains(prompt, reference) {
			t.Fatalf("coding-CLI workshop prompt must point to projected reference %q", reference)
		}
	}
	for _, duplicatedCatalogMarker := range []string{
		"generate_video(prompt, output_path",
		"- **Schedule management**:",
	} {
		if strings.Contains(prompt, duplicatedCatalogMarker) {
			t.Fatalf("coding-CLI workshop prompt still embeds projected catalog marker %q", duplicatedCatalogMarker)
		}
	}
	for _, routingContract := range []string{
		"current runtime's declared tools and discovery contract",
		"read_skill",
	} {
		if !strings.Contains(prompt, routingContract) {
			t.Fatalf("coding-CLI workshop prompt is missing bridge routing contract %q", routingContract)
		}
	}
}

func TestWorkflowToolsReferenceDistinguishesLogicalFromNativeBridgeTools(t *testing.T) {
	body, err := guidance.RenderReferenceKindForTest("workflow-tools", "workshop")
	if err != nil {
		t.Fatalf("render workflow-tools reference: %v", err)
	}
	for _, routingContract := range []string{
		"logical workflow tool names",
		"Never try",
		"`api-bridge.list_executions`",
		`get_api_spec(tool_name="<name>")`,
		"$MCP_MCP`/`$MCP_CUSTOM",
	} {
		if !strings.Contains(body, routingContract) {
			t.Fatalf("workflow-tools reference is missing bridge routing contract %q", routingContract)
		}
	}
}

func TestPhaseChatWorkshopSelectsWorkspaceToolGuidanceByTransport(t *testing.T) {
	for _, projected := range []string{"true", "false"} {
		prompt := PhaseChatSystemPrompt("workflow-builder", map[string]string{"WorkspacePath": "Workflow/example", "WorkshopMode": "workshop", "IsCodeExecutionMode": "true", "UseProjectedReferenceSkills": projected})
		if !strings.Contains(prompt, "references/workflow-chat.md") || !strings.Contains(prompt, "provided schemas directly") {
			t.Fatal("every transport must discover procedures through skills and retain native schema routing")
		}
		for _, duplicate := range []string{"generate_text_llm(user_message, tier)", "search_web_llm(query, provider)", "- **Schedule management**:"} {
			if strings.Contains(prompt, duplicate) {
				t.Fatalf("inline tutorial/catalog remains: %s", duplicate)
			}
		}
	}
}

func TestHumanInteractionGuidanceReachesBothWorkflowPromptTransports(t *testing.T) {
	for _, mode := range []string{"workshop", "run"} {
		for _, projected := range []string{"true", "false"} {
			t.Run(mode+"/projected="+projected, func(t *testing.T) {
				prompt := PhaseChatSystemPrompt("workflow-builder", map[string]string{
					"WorkspacePath": "Workflow/example", "WorkshopMode": mode,
					"IsCodeExecutionMode": "true", "UseProjectedReferenceSkills": projected,
				})
				for _, required := range []string{
					"references/workflow-chat.md",
				} {
					if !strings.Contains(prompt, required) {
						t.Errorf("rendered prompt lost human-interaction contract %q", required)
					}
				}
				if strings.Contains(prompt, "scheduled Pulse renders them in `builder/improve.html`") {
					t.Error("rendered prompt still points decisions to the retired HTML page")
				}
				operations, err := guidance.RenderReferenceKindForTest("workflow-chat", mode)
				if err != nil || !strings.Contains(operations, "references/human-in-the-loop.md") {
					t.Fatal("operations must trigger the human-input reference")
				}
				// Verify that the reference named by this mode's system prompt is
				// actually available, rather than a pointer to an absent skill file.
				body, err := guidance.RenderReferenceKindForTest("human-in-the-loop", mode)
				if err != nil {
					t.Fatalf("human-in-the-loop reference unavailable in %s: %v", mode, err)
				}
				// Mechanism details belong to the loaded skill, not every system
				// prompt. Guard both discoverability and the lean prompt boundary.
				for _, detail := range []string{"window.report.sendChatMessage", "timeout_seconds <= 45"} {
					if !strings.Contains(body, detail) {
						t.Errorf("human interaction detail missing from skill: %s", detail)
					}
					if strings.Contains(prompt, detail) {
						t.Errorf("human interaction detail duplicated in system prompt: %s", detail)
					}
				}
			})
		}
	}
}

func TestCanonicalWorkshopMode(t *testing.T) {
	for input, want := range map[string]string{
		"":          "",
		"workshop":  "workshop",
		"builder":   "workshop",
		"optimizer": "workshop",
		"reporting": "workshop",
		"run":       "run",
		"ask":       "run",
		"debugger":  "run",
	} {
		if got := canonicalWorkshopMode(input); got != want {
			t.Fatalf("canonicalWorkshopMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDetectWorkshopModeDefaultsToWorkshop(t *testing.T) {
	if got := detectWorkshopMode(nil, nil); got != "workshop" {
		t.Fatalf("detectWorkshopMode(nil, nil) = %q, want workshop", got)
	}
}

// TestWorkshopModeIsMergedSuperset verifies the canonical "workshop" mode
// produces the complete editable workflow surface AND
// the new phase-detection directive.
func TestWorkshopModeIsMergedSuperset(t *testing.T) {
	prompt := executeRealisticWorkshopPromptForMode(t, "workshop")

	// Should declare workshop mode explicitly.
	if !strings.Contains(prompt, "## CURRENT MODE: WORKSHOP") {
		t.Errorf("workshop mode prompt should start with `## CURRENT MODE: WORKSHOP`")
	}

	// Should include the phase-detection directive (only renders in workshop
	// mode — neither builder nor optimizer had this guidance).
	operations, err := guidance.RenderReferenceKindForTest("workflow-chat", "workshop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "references/workflow-chat.md") || !strings.Contains(operations, "First, determine the current phase from workspace state") {
		t.Errorf("workshop mode prompt should include the phase-detection directive")
	}

	// Should expose the compact inline strategy/eval entry points. The full
	// workflow-tool catalog is projected as a reference skill for coding CLIs,
	// so do not duplicate individual catalog entries in the system prompt.
	mustContain := []string{
		"references/human-in-the-loop.md",
		`builder-reference/references/optimize-playbook.md`, // shorthand for read_skill(skills=[{name:builder-reference,path:references/optimize-playbook.md}])
	}
	for _, s := range mustContain {
		if !strings.Contains(operations, s) {
			t.Errorf("workshop operations skill missing editable-workflow content: %q", s)
		}
	}

	workflowTools, err := guidance.RenderReferenceKindForTest("workflow-tools", "workshop")
	if err != nil {
		t.Fatalf("render workflow-tools reference: %v", err)
	}
	if !strings.Contains(workflowTools, "goal-advisor") {
		t.Error("projected workflow-tools reference is missing goal-advisor guidance")
	}
}

// TestWorkshopPromptKeepsCriticalRules verifies that the rules and dynamic
// state that MUST stay inline (cannot be lazy-loaded) are still in the
// rendered prompt after migration. If any of these go missing, the agent
// loses behavior the system depends on.
//
// Note: this test only checks snippets that appear in the inline template
// literally (not via template vars). Sections like "## Special Workspace
// Tools" come from {{.SpecialWorkspaceToolsInstructions}} — they're real in
// production but absent in this test helper because the helper passes empty
// vars. Adding them here would just couple the test to var content the
// helper synthesizes.
func TestWorkshopPromptKeepsCriticalRules(t *testing.T) {
	prompt := executeRealisticWorkshopPromptForMode(t, "workshop")

	// Things that MUST stay inline — hard rules / identity / dynamic state.
	// All of these are inline template literals, not template-var injections.
	mustContain := []string{
		"Workflow Builder Agent", // identity
		"## CURRENT STATE",       // dynamic state injection
		"Groups run sequentially unless the user explicitly requests parallel execution",
		"parallel schedules require explicit human approval",
		"Notifications are not new user authorization",
		"A refused action remains refused",
	}
	for _, s := range mustContain {
		if !strings.Contains(prompt, s) {
			t.Errorf("workshop prompt missing required snippet: %q", s)
		}
	}
}

// TestWorkshopPromptReferencesNewToolForLazyDocs verifies the inline prompt
// mentions read_skill so the agent knows how to load the migrated
// content.
func TestWorkshopPromptReferencesNewToolForLazyDocs(t *testing.T) {
	prompt := executeRealisticWorkshopPromptForMode(t, "workshop")
	if !strings.Contains(prompt, "read_skill") {
		t.Errorf("workshop prompt does not reference read_skill — agent will not know to load templates/system/*.md docs. " +
			"Add a pointer to at least one migrated section (e.g. 'For full main.py rules call read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/code-authoring.md\"}])').")
	}
}

// TestWorkshopPromptMovedSectionsAreReferencedNotInlined locks in the
// migration outcome. For each section that should move to templates/system/,
// it asserts:
//  1. A unique marker from the old inlined block is GONE from the prompt
//  2. The kind name IS mentioned somewhere in the prompt (so the agent
//     knows to call read_skill with that kind)
//
// This will fail until each section is actually moved. That's the point —
// it makes "did we migrate yet?" a green/red signal in CI.
func TestWorkshopPromptMovedSectionsAreReferencedNotInlined(t *testing.T) {
	prompt := executeRealisticWorkshopPromptForMode(t, "workshop")

	type migration struct {
		kind          string // referenceKinds key
		oldBodyMarker string // a string unique to the inline section
	}
	// Dynamic tool discovery supplies names/schemas; the attached index supplies procedure triggers.
	migrations := []migration{
		{kind: "code-authoring", oldBodyMarker: "## main.py authoring rules"},
		{kind: "stores", oldBodyMarker: "Three persistent stores — skill vs knowledgebase vs db"},
		{kind: "message-sequence", oldBodyMarker: "## MESSAGE SEQUENCE ROUTE PATTERNS"},
		{kind: "optimize-playbook", oldBodyMarker: "## OPTIMIZATION GUIDELINES"},
		{kind: "file-layout", oldBodyMarker: "## FILE LAYOUT"},
	}

	for _, m := range migrations {
		if strings.Contains(prompt, m.oldBodyMarker) {
			t.Errorf("section %q still inlined (found %q); should be in templates/system/%s.md and referenced via read_skill",
				m.kind, m.oldBodyMarker, m.kind)
		}
		bundle := guidance.MaterializeReferenceSkill("workshop")
		if !strings.Contains(prompt, "references/workflow-chat.md") || !strings.Contains(bundle.Content, "references/"+m.kind+".md") {
			t.Errorf("workshop prompt does not reference kind %q — agent will not know to load templates/system/%s.md",
				m.kind, m.kind)
		}
	}
}

// TestReferenceKindsAllRenderable verifies every kind declared in
// referenceKinds renders without error and is not accidentally empty. Focused
// references are loaded on demand, so correctness/completeness is asserted by
// their contract tests rather than an arbitrary byte ceiling.
func TestReferenceKindsAllRenderable(t *testing.T) {
	for _, kind := range guidance.ListReferenceKindsForTest() {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			body, err := guidance.RenderReferenceKindForTest(kind, "workshop")
			if err != nil {
				t.Fatalf("render %q failed: %v", kind, err)
			}
			if len(body) < 200 {
				t.Errorf("%s rendered to %d bytes — suspiciously short. Ensure the placeholder content has at least an intro paragraph.",
					kind, len(body))
			}
		})
	}
}
