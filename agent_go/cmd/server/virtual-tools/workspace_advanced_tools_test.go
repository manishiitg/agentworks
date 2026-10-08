package virtualtools

import (
	"encoding/base64"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
)

func TestImageAnalysisAPIKeysWithEnvSuppliesClaudeCodeOAuthToken(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "test-claude-oauth-token")

	keys := imageAnalysisAPIKeysWithEnv(nil)
	if keys == nil || keys.ClaudeCodeOAuthToken == nil {
		t.Fatal("image analysis keys did not include the Claude Code OAuth token")
	}
	if got := *keys.ClaudeCodeOAuthToken; got != "test-claude-oauth-token" {
		t.Fatalf("Claude Code OAuth token = %q, want injected test token", got)
	}
}

func TestHasWorkspaceDefaultImageAnalysisAuthRequiresClaudeCodeOAuthToken(t *testing.T) {
	if hasWorkspaceDefaultImageAnalysisAuth(string(llm.ProviderClaudeCode), nil) {
		t.Fatal("Claude Code must not be selected as an image-analysis default without an OAuth token")
	}
}

func TestChatAttachmentImageCLIFailsClosedAndStagesOnlyUploadedBytes(t *testing.T) {
	original := chatAttachmentLandlockRunner
	defer func() { chatAttachmentLandlockRunner = original }()
	chatAttachmentLandlockRunner = func() (string, bool) { return "", false }
	image := workspace.ReadImageResult{Filepath: "/private/server/other.png", Data: base64.StdEncoding.EncodeToString([]byte("uploaded fixture"))}
	if _, _, _, err := chatAttachmentImageCLI("claude-code", image); err == nil {
		t.Fatal("unconfined nested CLI accepted")
	}
	chatAttachmentLandlockRunner = func() (string, bool) { return "/test/landlock-runner", true }
	staged, options, cleanup, err := chatAttachmentImageCLI("claude-code", image)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(staged)
	if err != nil || string(data) != "uploaded fixture" || staged == image.Filepath {
		t.Fatal("did not stage safe uploaded bytes")
	}
	var opts llmtypes.CallOptions
	for _, option := range options {
		option(&opts)
	}
	policy := opts.CLISecurity
	if policy == nil || policy.Mode != llmtypes.CLISecurityModeIsolated || policy.LandlockRunner == "" || len(policy.WorkspaceWritePaths) != 0 || len(policy.WorkspaceReadPaths) != 1 || policy.WorkspaceReadPaths[0] != staged || filepath.Dir(policy.PrivateHome) != filepath.Dir(staged) {
		t.Fatalf("unsafe nested policy: %+v", policy)
	}
	cleanup()
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatal("staged image retained")
	}
}

func TestChatAttachmentClaudeReadGuardDeniesOtherFilesAndTools(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	original := chatAttachmentLandlockRunner
	defer func() { chatAttachmentLandlockRunner = original }()
	chatAttachmentLandlockRunner = func() (string, bool) { return "/test/landlock-runner", true }
	staged, options, cleanup, err := chatAttachmentImageCLI("claude-code", workspace.ReadImageResult{Filepath: "screen.png", Data: "Zml4dHVyZQ=="})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var opts llmtypes.CallOptions
	for _, option := range options {
		option(&opts)
	}
	if opts.Metadata.Custom["claude_code_settings"] == nil {
		t.Fatal("read guard settings not attached")
	}
	for _, test := range []struct{ tool, path, want string }{
		{"Read", staged, "allow"}, {"Read", filepath.Join(filepath.Dir(staged), "runtime", ".claude", ".credentials.json"), "deny"},
		{"Read", "/etc/passwd", "deny"}, {"Bash", staged, "deny"},
	} {
		payload, _ := json.Marshal(map[string]any{"tool_name": test.tool, "tool_input": map[string]any{"file_path": test.path}})
		cmd := exec.Command(python, filepath.Join(filepath.Dir(staged), "read-guard.py"))
		cmd.Stdin = strings.NewReader(string(payload))
		output, err := cmd.Output()
		if err != nil || !strings.Contains(string(output), `"permissionDecision": "`+test.want+`"`) {
			t.Fatalf("guard %s %s: %s %v", test.tool, test.path, output, err)
		}
	}
	for _, provider := range []string{"cursor-cli", "pi-cli", "openai"} {
		if _, _, _, err := chatAttachmentImageCLI(provider, workspace.ReadImageResult{}); err == nil {
			t.Fatalf("unrestricted image provider %s accepted", provider)
		}
	}
}
