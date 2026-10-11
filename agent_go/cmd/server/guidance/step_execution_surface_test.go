package guidance

import (
	"strings"
	"testing"
)

// socialMediaSubAgentTools is the exact registered set reported by the
// `tools_unavailable` error that opened PLAT-125, copied from the log line for
// the msgseq-…-sub-execute-allocate session.
var socialMediaSubAgentTools = []string{
	"agent_browser", "diff_patch_workspace_file", "execute_shell_command",
	"mutate_workflow_db", "query_workflow_db", "read_skill",
	"record_run_concern",
}

// TestStepExecutionSurfaceNeverAdvertisesUnheldProviderTools reproduces the
// original defect. The session above was handed llm-provider-config, followed
// it, called list_published_llms, and failed — then guessed provider names.
func TestStepExecutionSurfaceNeverAdvertisesUnheldProviderTools(t *testing.T) {
	skill := MaterializeStepExecutionReferenceSkill(StepExecutionSignals{ToolNames: socialMediaSubAgentTools})
	if skill == nil {
		t.Fatal("expected a reference skill for a step holding eight tools")
	}

	for _, banned := range []string{"references/llm-provider-config.md", "references/llm-selection.md"} {
		if strings.Contains(skill.Content, banned) {
			t.Errorf("step reference TOC advertises %s; this session registers no provider-configuration tool.\nTOC:\n%s", banned, skill.Content)
		}
		for _, file := range skill.SupportingFiles {
			if file.RelPath == banned {
				t.Errorf("step reference bundle ships %s to a session with no provider-configuration tool", banned)
			}
		}
	}
}

// TestStepExecutionSurfaceOmitsDesignAndPulseDocs pins the scope decision: the
// step-type docs are design-time material for the builder, and the Pulse and
// Finalizer docs belong to surfaces that reach guidance through
// workflow_phase_tools.go instead.
func TestStepExecutionSurfaceOmitsDesignAndPulseDocs(t *testing.T) {
	skill := MaterializeStepExecutionReferenceSkill(StepExecutionSignals{
		ToolNames:         socialMediaSubAgentTools,
		CodeExecutionMode: true,
		ScriptedStep:      true,
	})
	if skill == nil {
		t.Fatal("expected a reference skill")
	}

	// Deliberately checked with every signal enabled: if even the most capable
	// step does not get these, no step does.
	for _, banned := range []string{
		"routing", "scripted", "agent", "orchestrator", "step-config",
		"running-steps", "execution-policy", "planning-steps", "plan-design",
		"pulse-gate", "pulse-review-fixer", "pulse-finalizer", "pulse-bug-review",
		"backup-strategy", "publish-strategy", "reporting-policy", "html-output",
		"file-layout", "secret-management", "skill-management", "workflow-tools",
	} {
		path := "references/" + banned + ".md"
		if strings.Contains(skill.Content, path) {
			t.Errorf("step reference bundle includes %q, which no step agent can act on", banned)
		}
	}
}

// TestStepExecutionSurfaceFollowsTheToolsHeld is the positive half: each doc
// appears exactly when the signal that justifies it is present, and disappears
// when it is not. Written as add/remove pairs so the assertion is the
// correspondence itself rather than a hardcoded list.
func TestStepExecutionSurfaceFollowsTheToolsHeld(t *testing.T) {
	cases := []struct {
		name    string
		doc     string
		with    StepExecutionSignals
		without StepExecutionSignals
	}{
		{
			name:    "human-in-the-loop follows urgent feedback capability",
			doc:     "human-in-the-loop",
			with:    StepExecutionSignals{ToolNames: []string{"human_feedback"}},
			without: StepExecutionSignals{ToolNames: []string{"notify_user"}},
		},
		{
			name:    "human-in-the-loop follows durable decision capability",
			doc:     "human-in-the-loop",
			with:    StepExecutionSignals{ToolNames: []string{"create_human_input_request"}},
			without: StepExecutionSignals{ToolNames: []string{"query_workflow_db"}},
		},
		{
			name:    "browser-usage follows agent_browser",
			doc:     "browser-usage",
			with:    StepExecutionSignals{ToolNames: []string{"agent_browser"}},
			without: StepExecutionSignals{ToolNames: []string{"execute_shell_command"}},
		},
		{
			name:    "stores follows the workflow-db tools",
			doc:     "stores",
			with:    StepExecutionSignals{ToolNames: []string{"query_workflow_db"}},
			without: StepExecutionSignals{ToolNames: []string{"agent_browser"}},
		},
		{
			name:    "workspace-media-tools follows generate_text_llm",
			doc:     "workspace-media-tools",
			with:    StepExecutionSignals{ToolNames: []string{"generate_text_llm"}},
			without: StepExecutionSignals{ToolNames: []string{"agent_browser"}},
		},
		{
			name:    "mcp-bridge follows code-execution mode",
			doc:     "mcp-bridge",
			with:    StepExecutionSignals{ToolNames: []string{"agent_browser"}, CodeExecutionMode: true},
			without: StepExecutionSignals{ToolNames: []string{"agent_browser"}},
		},
		{
			name:    "code-authoring follows the scripted step",
			doc:     "code-authoring",
			with:    StepExecutionSignals{ToolNames: []string{"agent_browser"}, ScriptedStep: true},
			without: StepExecutionSignals{ToolNames: []string{"agent_browser"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := "references/" + tc.doc + ".md"
			if got := MaterializeStepExecutionReferenceSkill(tc.with); got == nil || !strings.Contains(got.Content, path) {
				t.Errorf("%s should be attached when its signal is present", tc.doc)
			}
			if got := MaterializeStepExecutionReferenceSkill(tc.without); got != nil && strings.Contains(got.Content, path) {
				t.Errorf("%s attached without its signal", tc.doc)
			}
		})
	}
}

// TestScriptedActiveProviderToolsReceiveBridgeSafeGuidance pins PLAT-246's
// contract: a scripted step that can use either active provider tool must get
// both the tool-specific decision guide and the bridge contract that makes the
// call without raw endpoints or credentials.
func TestScriptedActiveProviderToolsReceiveBridgeSafeGuidance(t *testing.T) {
	skill := MaterializeStepExecutionReferenceSkill(StepExecutionSignals{
		ToolNames:         []string{"execute_shell_command", "generate_text_llm"},
		CodeExecutionMode: true,
		ScriptedStep:      true,
	})
	if skill == nil {
		t.Fatal("expected scripted active-provider tool guidance")
	}
	for _, path := range []string{"references/workspace-media-tools.md", "references/mcp-bridge.md"} {
		if !strings.Contains(skill.Content, path) {
			t.Fatalf("scripted active-provider tool step missing %s\nTOC:\n%s", path, skill.Content)
		}
	}
	mediaTools := materializedFileContent(t, skill, "references/workspace-media-tools.md")
	for _, want := range []string{"## Scripted workflow use", "$MCP_CUSTOM/generate_text_llm", "must never be replaced with a direct provider request"} {
		if !strings.Contains(mediaTools, want) {
			t.Fatalf("scripted active-provider tool guidance missing %q\n%s", want, mediaTools)
		}
	}
}

// TestStepExecutionSurfaceIsNilWhenNothingQualifies keeps the caller's skip
// path honest: a step holding no documented tool must attach no bundle rather
// than an empty one.
func TestStepExecutionSurfaceIsNilWhenNothingQualifies(t *testing.T) {
	if got := MaterializeStepExecutionReferenceSkill(StepExecutionSignals{ToolNames: []string{"record_run_concern"}}); got != nil {
		t.Errorf("expected no bundle for a step with no documented tool, got %d docs:\n%s", len(got.SupportingFiles), got.Content)
	}
}

// TestStepExecutionSurfaceIsMuchSmallerThanTheWorkshopBundle records the size
// change that motivated the ticket — every step of every run previously
// carried the builder's whole corpus.
func TestStepExecutionSurfaceIsMuchSmallerThanTheWorkshopBundle(t *testing.T) {
	workshop := MaterializeReferenceSkill("workshop")
	if workshop == nil {
		t.Fatal("expected the workshop bundle")
	}
	step := MaterializeStepExecutionReferenceSkill(StepExecutionSignals{
		ToolNames:         socialMediaSubAgentTools,
		CodeExecutionMode: true,
	})
	if step == nil {
		t.Fatal("expected a step bundle")
	}
	if len(step.SupportingFiles) >= len(workshop.SupportingFiles) {
		t.Errorf("step bundle (%d docs) should be far smaller than the workshop bundle (%d docs)",
			len(step.SupportingFiles), len(workshop.SupportingFiles))
	}
	t.Logf("workshop=%d docs, step=%d docs", len(workshop.SupportingFiles), len(step.SupportingFiles))
}
