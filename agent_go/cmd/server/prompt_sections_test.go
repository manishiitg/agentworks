package server

import (
	"errors"
	"strings"
	"testing"
)

type recordingAppender struct {
	texts []string
	fail  error
}

func (r *recordingAppender) AddInstructions(sections ...string) error {
	if r.fail != nil {
		return r.fail
	}
	r.texts = append(r.texts, sections...)
	return nil
}

func sectionByName(t *testing.T, name string) promptSection {
	t.Helper()
	for _, section := range promptSections {
		if section.Name == name {
			return section
		}
	}
	t.Fatalf("no prompt section named %q", name)
	return promptSection{}
}

// The defect this registry was written after: a section asserting "Your native
// tools (Bash, Read, Write, etc.) are disabled" was injected into a hybrid
// profile, whose premise is the opposite. It was gated only on "is this a CLI
// provider" while the profile sat unread 118 lines above.
//
// This is the invariant, not the condition — a rewrite that keeps the behavior
// still has to pass.
func TestNoSectionClaimsNativeToolsAreDisabledForAHybridProfile(t *testing.T) {
	hybrid := promptContext{
		Provider:           "codex-cli",
		ProfileID:          "example-product",
		HasProfile:         true,
		NativeCodingTools:  true,
		CLIToolEnvironment: "Your native tools (Bash, Read, Write, etc.) are **disabled**.",
	}

	appender := &recordingAppender{}
	included, _, err := assemblePromptSections(appender, hybrid)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	for _, name := range included {
		if name == "cli-tool-environment" {
			t.Fatal("a hybrid profile keeps its native tools; a section denying that must not apply")
		}
	}
	for _, text := range appender.texts {
		if strings.Contains(text, "native tools") && strings.Contains(text, "disabled") {
			t.Fatalf("assembled prompt tells a hybrid agent its native tools are disabled:\n%s", text)
		}
	}
}

// All 20 previous call sites discarded this error with `_ =`.
func TestAssemblyReportsWhichSectionFailedToAppend(t *testing.T) {
	appender := &recordingAppender{fail: errors.New("definition is immutable")}
	_, _, err := assemblePromptSections(appender, promptContext{
		ShellRoot:          "/root",
		PerUserChatsFolder: "_users/default/Chats",
	})
	if err == nil {
		t.Fatal("an AddInstructions failure must surface, not vanish")
	}
	var sectionErr *promptSectionError
	if !errors.As(err, &sectionErr) || sectionErr.Section != "workspace-map" {
		t.Fatalf("error must name the failing section, got %v", err)
	}
}
