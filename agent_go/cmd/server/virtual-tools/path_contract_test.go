package virtualtools

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestNormalizeRequiredAbsoluteWorkspaceDocumentPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	input := filepath.Join(root, "_users", "default", "Chats", "generated-images", "hero.png")
	got, err := normalizeRequiredAbsoluteWorkspaceDocumentPath(input, "output_path")
	if err != nil {
		t.Fatalf("normalizeRequiredAbsoluteWorkspaceDocumentPath returned error: %v", err)
	}
	want := "_users/default/Chats/generated-images/hero.png"
	if got != want {
		t.Fatalf("normalized path = %q, want %q", got, want)
	}
}

func TestNormalizeRequiredAbsoluteWorkspaceDocumentPathRejectsRelative(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	_, err := normalizeRequiredAbsoluteWorkspaceDocumentPath("Chats/generated-images/hero.png", "output_path")
	if err == nil {
		t.Fatal("expected relative path to be rejected")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("error = %q, want mention of absolute path", err.Error())
	}
}

func TestNormalizeRequiredAbsoluteWorkspaceDocumentPathRejectsOutsideWorkspaceDocs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", filepath.Join(root, "workspace-docs"))

	_, err := normalizeRequiredAbsoluteWorkspaceDocumentPath(filepath.Join(root, "outside.png"), "output_path")
	if err == nil {
		t.Fatal("expected path outside workspace docs root to be rejected")
	}
	if !strings.Contains(err.Error(), "workspace docs root") {
		t.Fatalf("error = %q, want mention of workspace docs root", err.Error())
	}
}

func TestImageGenAndImageEditHaveNoProviderOrModelIDArgs(t *testing.T) {
	for _, tool := range []func() llmtypes.Tool{GetImageEditToolDefinition} {
		encoded, err := json.Marshal(tool())
		if err != nil {
			t.Fatalf("marshal tool definition: %v", err)
		}
		text := string(encoded)
		for _, field := range []string{`"provider"`, `"model_id"`} {
			if strings.Contains(text, field) {
				t.Fatalf("codex-cli is the only generation provider; tool definition should not expose %s: %s", field, text)
			}
		}
		if strings.Contains(text, "list_llm_capabilities") {
			t.Fatalf("nothing to discover with only one provider; tool definition should not mention list_llm_capabilities: %s", text)
		}
	}
}
