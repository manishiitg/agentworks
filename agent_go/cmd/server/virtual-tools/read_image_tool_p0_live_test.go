package virtualtools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// TestReadImageToolP0Live drives the real read_image tool surface
// (CreateReadImageProviderTestExecutor -> wrapReadImageWithLLM) end to end
// against every native-CLI provider read_image supports, proving the tool
// this repo actually exposes to agents works -- not just the underlying
// multi-llm-provider-go adapter one layer below (see PLAT-247's live
// verification for that layer). No prior test in this repo exercised this
// tool surface at all.
//
// Requires READ_IMAGE_TOOL_P0_LIVE=1 and READ_IMAGE_TOOL_P0_WORKSPACE_URL
// pointing at a running workspace server whose docs root contains the
// image at READ_IMAGE_TOOL_P0_IMAGE_PATH (a full absolute path under that
// server's own --docs-dir; read_image rejects workspace-relative paths).
// vertex/z-ai/
// kimi are skipped when no credentials are configured for this environment,
// matching cmd/testing/read_image_providers.go's own skip-reason logic.
func TestReadImageToolP0Live(t *testing.T) {
	workspaceURL := strings.TrimSpace(os.Getenv("READ_IMAGE_TOOL_P0_WORKSPACE_URL"))
	if workspaceURL == "" {
		t.Skip("set READ_IMAGE_TOOL_P0_LIVE=1 and READ_IMAGE_TOOL_P0_WORKSPACE_URL to run the live read_image tool P0")
	}
	if os.Getenv("READ_IMAGE_TOOL_P0_LIVE") != "1" {
		t.Skip("set READ_IMAGE_TOOL_P0_LIVE=1 to run the live read_image tool P0")
	}

	imagePath := strings.TrimSpace(os.Getenv("READ_IMAGE_TOOL_P0_IMAGE_PATH"))
	if imagePath == "" {
		t.Fatal("READ_IMAGE_TOOL_P0_IMAGE_PATH is required (full absolute path to a real image under the workspace server's own --docs-dir; read_image rejects workspace-relative paths)")
	}
	expectMarker := strings.ToLower(strings.TrimSpace(os.Getenv("READ_IMAGE_TOOL_P0_EXPECT")))
	if expectMarker == "" {
		expectMarker = "red"
	}

	executor := CreateReadImageProviderTestExecutor(workspaceURL, "default")
	if executor == nil {
		t.Fatal("read_image executor is not available")
	}

	type providerCase struct {
		provider string
		modelID  string
	}
	cases := []providerCase{
		{"codex-cli", "codex-cli"}, // "codex-cli" = codex's own current default model, not a pinned model ID that can go stale/deprecated
		{"cursor-cli", "auto"},
		{"claude-code", "claude-code"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.provider, func(t *testing.T) {
			if reason := readImageToolP0SkipReason(tc.provider); reason != "" {
				t.Skip(reason)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			llmConfig := mcpagent.LLMModel{Provider: tc.provider, ModelID: tc.modelID}
			ctx = context.WithValue(ctx, mcpagent.ToolExecutionLLMConfigKey, llmConfig)
			readDir := workspaceRelativeDir(imagePath, workspaceDocsRootForP0())
			// FolderGuardReadPathsKey alone is inert -- it only supplements
			// FolderGuardAllowedWriteFolderKey once that key is present at all
			// (see resolveEffectiveFolderGuard's "Context System 1" branch in
			// pkg/workspace/client.go), even when granting zero write access.
			ctx = context.WithValue(ctx, common.FolderGuardAllowedWriteFolderKey, []string{})
			ctx = context.WithValue(ctx, common.FolderGuardReadPathsKey, []string{readDir})

			result, err := executor(ctx, map[string]any{
				"filepath": imagePath,
				"query":    "What is the dominant color? Reply with one lowercase English color word.",
			})
			if err != nil {
				t.Fatalf("read_image(%s) failed: %v", tc.provider, err)
			}
			if strings.TrimSpace(result) == "" {
				t.Fatal("read_image returned an empty response")
			}
			if !strings.Contains(strings.ToLower(result), expectMarker) {
				t.Fatalf("read_image(%s) response did not contain %q: %s", tc.provider, expectMarker, truncateMCPTestResult(result, 500))
			}
		})
	}
}

func readImageToolP0SkipReason(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex-cli":
		if _, err := exec.LookPath("codex"); err != nil {
			return "codex CLI is not installed or not on PATH"
		}
	case "cursor-cli":
		if _, err := exec.LookPath("cursor-agent"); err != nil {
			return "cursor-agent CLI is not installed or not on PATH"
		}
	case "claude-code":
		if _, err := exec.LookPath("claude"); err != nil {
			return "claude CLI is not installed or not on PATH"
		}
	case "vertex":
		if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" && os.Getenv("GOOGLE_CLOUD_PROJECT") == "" && os.Getenv("GEMINI_API_KEY") == "" {
			return "no Vertex/Gemini credentials configured in this environment"
		}
	case "z-ai":
		if os.Getenv("Z_AI_API_KEY") == "" && os.Getenv("ZAI_API_KEY") == "" {
			return "no Z.AI API key configured in this environment"
		}
	case "kimi":
		if os.Getenv("KIMI_API_KEY") == "" && os.Getenv("MOONSHOT_API_KEY") == "" {
			return "no Kimi API key configured in this environment"
		}
	}
	return ""
}

// workspaceDocsRootForP0 mirrors the WORKSPACE_DOCS_PATH override that
// pkg/workspace/execute_shell_command.go's workspaceDocsRoots() and
// video_gen_tools.go's workspaceDocumentRoots() both already respect.
func workspaceDocsRootForP0() string {
	return strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH"))
}

// workspaceRelativeDir returns the workspace-relative directory containing
// absPath, for granting via common.FolderGuardReadPathsKey (the folder
// guard checks workspace-relative paths, not absolute filesystem ones).
func workspaceRelativeDir(absPath, docsRoot string) string {
	if docsRoot == "" {
		return "."
	}
	rel, err := filepath.Rel(docsRoot, filepath.Dir(absPath))
	if err != nil {
		return "."
	}
	return filepath.ToSlash(rel)
}

func truncateMCPTestResult(result string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(result))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "…"
}
