package mcpagent

import (
	"strings"
	"testing"

	llm "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func mustAttachSkill(t *testing.T, agent *Agent, skill *llmtypes.Skill) {
	t.Helper()
	if err := agent.attachSkill(skill); err != nil {
		t.Fatalf("attach skill %q: %v", skill.Name, err)
	}
}

func TestAttachSkillIdempotentOnName(t *testing.T) {
	a := &Agent{}
	mustAttachSkill(t, a, &llmtypes.Skill{Name: "alpha", Description: "first"})
	mustAttachSkill(t, a, &llmtypes.Skill{Name: "beta", Description: "second"})
	mustAttachSkill(t, a, &llmtypes.Skill{Name: "alpha", Description: "replaced"})
	got := a.attachedSkillsSnapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(got))
	}
	if got[0].Description != "replaced" {
		t.Errorf("expected alpha to be replaced, got %q", got[0].Description)
	}
	if got[1].Name != "beta" {
		t.Errorf("expected beta as second, got %q", got[1].Name)
	}
}

func TestDetachSkill(t *testing.T) {
	a := &Agent{}
	mustAttachSkill(t, a, &llmtypes.Skill{Name: "alpha"})
	mustAttachSkill(t, a, &llmtypes.Skill{Name: "beta"})
	a.detachSkill("alpha")
	got := a.attachedSkillsSnapshot()
	if len(got) != 1 || got[0].Name != "beta" {
		t.Errorf("expected only beta remaining, got %+v", got)
	}
}

func TestClearSkills(t *testing.T) {
	a := &Agent{}
	mustAttachSkill(t, a, &llmtypes.Skill{Name: "alpha"})
	a.clearSkills()
	if len(a.attachedSkillsSnapshot()) != 0 {
		t.Errorf("expected no skills after ClearSkills")
	}
}

func TestRenderSkillListingFormat(t *testing.T) {
	got := renderSkillListing([]*llmtypes.Skill{
		{Name: "agent-browser", Description: "Drive a browser"},
		{Name: "pdf-extract", Description: "Extract from PDFs"},
	})
	for _, want := range []string{
		"## Available Skills",
		"- **agent-browser**: Drive a browser",
		"- **pdf-extract**: Extract from PDFs",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderSkillListingEmpty(t *testing.T) {
	if got := renderSkillListing(nil); got != "" {
		t.Errorf("expected empty for nil skills, got %q", got)
	}
	if got := renderSkillListing([]*llmtypes.Skill{}); got != "" {
		t.Errorf("expected empty for empty slice, got %q", got)
	}
}

func TestRenderSkillListingSkipsNamelessSkills(t *testing.T) {
	got := renderSkillListing([]*llmtypes.Skill{
		nil,
		{Name: ""},
		{Name: "real", Description: "kept"},
	})
	if !strings.Contains(got, "- **real**") {
		t.Errorf("expected real skill to render, got %q", got)
	}
	if strings.Contains(got, "- **`**") || strings.Contains(got, "- ****") {
		t.Errorf("expected nameless skills to be skipped, got %q", got)
	}
}

// TestEnsureSystemPromptAppendsAttachedSkills covers the transport-
// layer integration: AttachSkill on the agent must surface as a
// "## Available Skills" block in the system message that
// ensureSystemPrompt produces for the outgoing LLM call. That's the
// API-transport path; if this regresses, API providers silently lose
// skill visibility (CLI providers would still have files on disk).
func TestEnsureSystemPromptAppendsAttachedSkills(t *testing.T) {
	a := &Agent{systemPrompt: "BASE PROMPT"}
	mustAttachSkill(t, a, &llmtypes.Skill{Name: "pdf-extract", Description: "Extract PDFs"})

	out := ensureSystemPrompt(a, nil)
	if len(out) == 0 || out[0].Role != llmtypes.ChatMessageTypeSystem {
		t.Fatalf("expected leading system message, got %+v", out)
	}
	parts := out[0].Parts
	if len(parts) == 0 {
		t.Fatalf("expected non-empty parts")
	}
	text, ok := parts[0].(llmtypes.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", parts[0])
	}
	got := text.Text
	for _, want := range []string{
		"BASE PROMPT",
		"## Available Skills",
		"- **pdf-extract**: Extract PDFs",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in system prompt:\n%s", want, got)
		}
	}
}

func TestCodingCLISystemPromptDoesNotDuplicateNativeSkillListing(t *testing.T) {
	a := &Agent{systemPrompt: "BASE PROMPT", provider: llm.ProviderMuseCLI, modelID: "muse-spark-1.3-contributor"}
	mustAttachSkill(t, a, &llmtypes.Skill{Name: "work-dashboard", Description: "Build dashboards"})

	out := ensureSystemPrompt(a, nil)
	got := out[0].Parts[0].(llmtypes.TextContent).Text
	if strings.Contains(got, "## Available Skills") || strings.Contains(got, "- **work-dashboard**") {
		t.Fatalf("coding CLI prompt duplicated native skill projection:\n%s", got)
	}
	if !strings.Contains(got, "BASE PROMPT") {
		t.Fatalf("coding CLI prompt lost base instructions:\n%s", got)
	}
}

// TestEnsureSystemPromptWithoutAttachedSkillsLeavesBaseUntouched is the
// corollary: no attached skills means no listing appended. Belt-and-
// suspenders against future code adding an "always emit empty heading"
// regression that would pollute every API request with dead headers.
func TestEnsureSystemPromptWithoutAttachedSkillsLeavesBaseUntouched(t *testing.T) {
	a := &Agent{systemPrompt: "JUST THE BASE"}

	out := ensureSystemPrompt(a, nil)
	if len(out) == 0 {
		t.Fatalf("expected at least one message")
	}
	text := out[0].Parts[0].(llmtypes.TextContent).Text
	if text != "JUST THE BASE" {
		t.Errorf("expected base prompt unchanged when no skills attached; got %q", text)
	}
	if strings.Contains(text, "Available Skills") {
		t.Errorf("expected no skills heading when none attached; got %q", text)
	}
}

// TestEnsureSystemPromptReplacesExistingSystemMessage covers the
// message-replacement branch — if conversation history already carries
// a stale system message, ensureSystemPrompt must overwrite it with
// the current prompt (including skill listing), not append a second
// system message. Otherwise APIs that disallow multiple system
// messages reject the request.
func TestEnsureSystemPromptReplacesExistingSystemMessage(t *testing.T) {
	a := &Agent{systemPrompt: "NEW BASE"}
	mustAttachSkill(t, a, &llmtypes.Skill{Name: "fresh", Description: "fresh skill"})

	stale := []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "STALE OLD PROMPT"}},
		},
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hi"}},
		},
	}

	out := ensureSystemPrompt(a, stale)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages (system replaced + human kept), got %d", len(out))
	}
	systemText := out[0].Parts[0].(llmtypes.TextContent).Text
	if strings.Contains(systemText, "STALE OLD PROMPT") {
		t.Errorf("expected stale prompt replaced; got %q", systemText)
	}
	if !strings.Contains(systemText, "NEW BASE") {
		t.Errorf("expected new base in replaced system message; got %q", systemText)
	}
	if !strings.Contains(systemText, "- **fresh**") {
		t.Errorf("expected attached skill in replaced system message; got %q", systemText)
	}
}
