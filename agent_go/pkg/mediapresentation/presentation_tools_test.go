package mediapresentation

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Same contract show_video is held to: the presentation kind comes from the
// profile's declaration, so a product.yaml that forgets tools[].presentation.kind
// fails loudly at the first call instead of silently writing a kind nobody
// declared. Checked before any workspace read, so no fake server is needed.
func TestPresentationToolsRequireADeclaredKind(t *testing.T) {
	for _, tc := range []struct {
		name    string
		factory agentprofiles.ToolFactory
		args    map[string]interface{}
	}{
		{
			name:    "show_character",
			factory: showCharacterFactory("http://unused"),
			args: map[string]interface{}{
				"name": "Aang", "image_path": "work/characters/aang.png", "spec_path": "work/characters/aang.md",
			},
		},
		{
			name:    "show_document",
			factory: showDocumentFactory("http://unused"),
			args:    map[string]interface{}{"path": "work/longform-script.md", "title": "Script"},
		},
		{
			name:    "show_reference",
			factory: showReferenceFactory("http://unused"),
			args:    map[string]interface{}{"path": "references/cafe.png", "title": "Cafe", "role": "location"},
		},
	} {
		spec, err := tc.factory(agentprofiles.ToolRuntimeContext{
			UserID: "u1", SessionID: "s1", WorkspacePath: "Crew/example",
			// Presentation intentionally left nil.
		}, nil)
		if err != nil {
			t.Fatalf("%s: building the tool spec itself should not fail: %v", tc.name, err)
		}
		result, err := spec.Execute(context.Background(), tc.args)
		if err == nil {
			t.Fatalf("%s: expected an error with no declared presentation kind, got result %q", tc.name, result)
		}
		if !strings.Contains(err.Error(), "presentation kind") {
			t.Fatalf("%s: error should name the missing declaration, got: %v", tc.name, err)
		}
	}
}

// Input validation runs before any workspace read, so these exercise the
// early-return path without a fake server. A character shown without a real
// reference image is the exact failure the panel exists to prevent, and a
// document tool that accepted any file would make the panel a file browser.
func TestPresentationToolsRejectUnusableInput(t *testing.T) {
	declared := agentprofiles.ToolRuntimeContext{
		UserID: "u1", SessionID: "s1", WorkspacePath: "Crew/example",
		Presentation: &agentprofiles.PresentationBinding{Kind: "test.kind", Activity: &agentprofiles.PresentationActivityBinding{Label: "Item ready", Destination: "Production panel", Detail: "Ready to review"}},
	}

	character, err := showCharacterFactory("http://unused")(declared, nil)
	if err != nil {
		t.Fatalf("building show_character: %v", err)
	}
	for _, tc := range []struct {
		why  string
		args map[string]interface{}
		want string
	}{
		{
			why:  "a character with no name has no stable identity to update against",
			args: map[string]interface{}{"name": "  ", "image_path": "a.png", "spec_path": "a.md"},
			want: "character's name",
		},
		{
			why:  "the reference image is the whole point; a .md in its place is not one",
			args: map[string]interface{}{"name": "Aang", "image_path": "a.md", "spec_path": "a.md"},
			want: "reference image",
		},
		{
			why:  "the spec must be readable prose, not another image",
			args: map[string]interface{}{"name": "Aang", "image_path": "a.png", "spec_path": "a.png"},
			want: "character spec",
		},
		{
			why:  "a path escaping the project must not resolve",
			args: map[string]interface{}{"name": "Aang", "image_path": "../../etc/passwd.png", "spec_path": "a.md"},
			want: "reference image",
		},
	} {
		result, err := character.Execute(context.Background(), tc.args)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.why, err)
		}
		if !strings.Contains(result, tc.want) {
			t.Fatalf("%s: got %q, want a message naming %q", tc.why, result, tc.want)
		}
	}

	document, err := showDocumentFactory("http://unused")(declared, nil)
	if err != nil {
		t.Fatalf("building show_document: %v", err)
	}
	for _, tc := range []struct {
		why  string
		args map[string]interface{}
	}{
		{why: "a rendered video is not a document", args: map[string]interface{}{"path": "outputs/final.mp4", "title": "x"}},
		{why: "a path escaping the project must not resolve", args: map[string]interface{}{"path": "../../secrets.md", "title": "x"}},
	} {
		result, err := document.Execute(context.Background(), tc.args)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.why, err)
		}
		if !strings.Contains(result, "project-relative .md path") {
			t.Fatalf("%s: got %q, want the .md validation message", tc.why, result)
		}
	}
}

// Slash commands are declared in product.yaml with their prompts in separate
// files, so two things can silently break: a declared command whose file went
// missing, and the internal file path leaking to the browser. The manifest
// loader treats a missing or empty prompt as a broken product rather than
// dropping the command, because a command that appears in the menu and submits
// nothing is worse than one that was never offered.
