package virtualtools

import "testing"

func TestCreateWorkspaceToolRegistryIncludesActiveTextSearchAndImageTools(t *testing.T) {
	t.Setenv("MCP_API_URL", "http://example.test")
	t.Setenv("MCP_API_TOKEN", "registry-token")

	registry := CreateWorkspaceToolRegistry(WorkspaceToolRegistryConfig{
		UserID:    "default",
		SessionID: "registry-test-session",
	})

	toolDefs := map[string]bool{}
	for _, tool := range registry.Tools {
		if tool.Function != nil {
			toolDefs[tool.Function.Name] = true
		}
	}

	for _, name := range []string{
		"execute_shell_command",
		"diff_patch_workspace_file",
		"generate_text_llm",
		"read_image",
		"image_gen",
		"image_edit",
	} {
		if !toolDefs[name] {
			t.Fatalf("registry tool definitions missing %q", name)
		}
		if _, ok := registry.Executors[name]; !ok {
			t.Fatalf("registry executors missing %q", name)
		}
		if got := registry.Categories[name]; got != GetWorkspaceAdvancedToolCategory() {
			t.Fatalf("registry category for %q = %q, want %q", name, got, GetWorkspaceAdvancedToolCategory())
		}
	}
	for _, name := range []string{"generate_video", "text_to_speech", "speech_to_text", "generate_music"} {
		if toolDefs[name] {
			t.Fatalf("deprecated media tool %q must be hidden from the registry", name)
		}
		if _, ok := registry.Executors[name]; ok {
			t.Fatalf("deprecated media tool %q must not have a registry executor", name)
		}
	}
	if mediaCategory := WorkspaceToolNamesByCategory("workspace_image"); len(mediaCategory) == 0 {
		t.Fatalf("workspace_image category should resolve image_gen/image_edit, got none")
	}

	if got := registry.Env["MCP_SESSION_ID"]; got != "registry-test-session" {
		t.Fatalf("registry env MCP_SESSION_ID = %q, want registry-test-session", got)
	}
	if got := registry.Env["MCP_AUTH"]; got != "Authorization: Bearer registry-token" {
		t.Fatalf("registry env MCP_AUTH = %q", got)
	}
	if got := registry.Env["MCP_CUSTOM"]; got != "http://example.test/s/registry-test-session/tools/custom" {
		t.Fatalf("registry env MCP_CUSTOM = %q", got)
	}
}
