package llmproviders

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCodingAgentContinuationRealE2EAfterTmuxLoss(t *testing.T) {
	if os.Getenv("RUN_CODING_AGENT_CONTINUATION_REAL_E2E") == "" {
		t.Skip("set RUN_CODING_AGENT_CONTINUATION_REAL_E2E=1 to run real coding-agent continuation E2E")
	}

	tests := []struct {
		name       string
		provider   Provider
		model      string
		binaryName string
		require    func(*testing.T)
		setup      func(*testing.T)
		config     func(string) Config
		options    func(string, string) []llmtypes.CallOption
		cleanup    func(context.Context) error
	}{
		{
			name:       "claude-code",
			provider:   ProviderClaudeCode,
			model:      codingAgentContinuationE2EModel("CLAUDE_CODE_TMUX_INTEGRATION_MODEL", "claude-haiku-4-5-20251001"),
			binaryName: "claude",
			config: func(model string) Config {
				return Config{
					Provider:            ProviderClaudeCode,
					ModelID:             model,
					ClaudeCodeTransport: ClaudeCodeTransportTmux,
				}
			},
			options: func(ownerID, workDir string) []llmtypes.CallOption {
				return []llmtypes.CallOption{
					WithClaudeCodeInteractiveSessionID(ownerID),
					WithClaudeCodePersistentInteractiveSession(true),
					WithClaudeCodeWorkingDir(workDir),
					WithClaudeCodeEffort("low"),
				}
			},
			cleanup: CleanupClaudeCodeTmuxSessions,
		},
		{
			name:       "codex-cli",
			provider:   ProviderCodexCLI,
			model:      codingAgentContinuationE2EModel("CODEX_CLI_REAL_CONTRACT_MODEL", "gpt-5.4-mini"),
			binaryName: "codex",
			config: func(model string) Config {
				return Config{
					Provider: ProviderCodexCLI,
					ModelID:  model,
				}
			},
			options: func(ownerID, workDir string) []llmtypes.CallOption {
				return []llmtypes.CallOption{
					WithCodexInteractiveSessionID(ownerID),
					WithCodexPersistentInteractiveSession(true),
					WithCodexProjectDirID(workDir),
					WithCodexApprovalPolicy("never"),
					WithCodexReasoningEffort("low"),
					WithCodexDisableShellTool(),
				}
			},
			cleanup: CleanupCodexCLIInteractiveSessions,
		},
		{
			name:       "cursor-cli",
			provider:   ProviderCursorCLI,
			model:      codingAgentContinuationE2EModel("CURSOR_CLI_REAL_CONTRACT_MODEL", DefaultCursorCLIModel),
			binaryName: "cursor-agent",
			config: func(model string) Config {
				return Config{
					Provider: ProviderCursorCLI,
					ModelID:  model,
				}
			},
			options: func(ownerID, workDir string) []llmtypes.CallOption {
				return []llmtypes.CallOption{
					WithCursorInteractiveSessionID(ownerID),
					WithCursorPersistentInteractiveSession(true),
					WithCursorWorkingDir(workDir),
					WithCursorDenyBuiltinTools(true),
				}
			},
			cleanup: CleanupCursorCLIInteractiveSessions,
		},
		{
			name:     "pi-cli",
			provider: ProviderPiCLI,
			model:    codingAgentContinuationE2EModel("PI_CLI_REAL_CONTRACT_MODEL", DefaultPiCLIModel),
			require: func(t *testing.T) {
				requireCodingAgentContinuationPiRuntime(t)
			},
			setup: func(t *testing.T) {
				t.Setenv("PI_CODING_AGENT_SESSION_DIR", t.TempDir())
			},
			config: func(model string) Config {
				return Config{
					Provider: ProviderPiCLI,
					ModelID:  model,
				}
			},
			options: func(ownerID, workDir string) []llmtypes.CallOption {
				return []llmtypes.CallOption{
					WithPiInteractiveSessionID(ownerID),
					WithPiPersistentInteractiveSession(true),
					WithPiWorkingDir(workDir),
				}
			},
			cleanup: CleanupPiCLIInteractiveSessions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireCodingAgentContinuationBinary(t, "tmux")
			if tt.require != nil {
				tt.require(t)
			} else {
				requireCodingAgentContinuationBinary(t, tt.binaryName)
			}
			if tt.setup != nil {
				tt.setup(t)
			}
			if tt.cleanup != nil {
				t.Cleanup(func() { _ = tt.cleanup(context.Background()) })
			}

			model, err := InitializeLLM(tt.config(tt.model))
			if err != nil {
				t.Fatalf("InitializeLLM(%s) error = %v", tt.provider, err)
			}

			ownerID := fmt.Sprintf("continuation-e2e-%s-%d", strings.ReplaceAll(string(tt.provider), "-", ""), time.Now().UnixNano())
			workDir, err := os.Getwd()
			if err != nil {
				t.Fatalf("get working dir: %v", err)
			}
			token := fmt.Sprintf("CONTINUATION_E2E_%s_%d", strings.ReplaceAll(string(tt.provider), "-", "_"), time.Now().UnixNano())
			options := tt.options(ownerID, workDir)

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()

			first, err := model.GenerateContent(ctx, []llmtypes.MessageContent{
				llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "This is a transport contract test. Do not use tools. Reply exactly as instructed."),
				llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, fmt.Sprintf("Take note of this exact token: %s. Do not save it to memory. Reply exactly: ACK_%s", token, token)),
			}, options...)
			if err != nil {
				t.Fatalf("first GenerateContent error = %v", err)
			}
			if got := strings.TrimSpace(first.Choices[0].Content); !strings.Contains(got, token) {
				t.Fatalf("first content = %q, want token %s", got, token)
			}

			handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(first)
			if !ok {
				t.Fatalf("first response missing coding provider session handle: %#v", first.Choices[0].GenerationInfo)
			}
			if strings.TrimSpace(handle.NativeSessionID) == "" {
				t.Fatalf("first handle missing native session id: %#v", handle)
			}
			if strings.TrimSpace(handle.TmuxSession) == "" {
				t.Fatalf("first handle missing tmux session: %#v", handle)
			}

			killCodingAgentContinuationTmuxSession(t, handle.TmuxSession)

			second, err := ContinueCodingAgentSession(ctx, model, handle, "What exact token did I ask you to take note of? Reply with only that token.", options...)
			if err != nil {
				t.Fatalf("ContinueCodingAgentSession after killed tmux error = %v", err)
			}
			if got := strings.TrimSpace(second.Choices[0].Content); !strings.Contains(got, token) {
				t.Fatalf("continued content = %q, want remembered token %s", got, token)
			}

			nextHandle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(second)
			if !ok {
				t.Fatalf("continued response missing coding provider session handle: %#v", second.Choices[0].GenerationInfo)
			}
			if strings.TrimSpace(nextHandle.TmuxSession) == "" {
				t.Fatalf("continued handle missing tmux session: %#v", nextHandle)
			}
			if nextHandle.TmuxSession == handle.TmuxSession {
				t.Fatalf("continuation reused killed tmux session %q; want fresh tmux session", handle.TmuxSession)
			}
		})
	}
}

func codingAgentContinuationE2EModel(envName, fallback string) string {
	if model := strings.TrimSpace(os.Getenv(envName)); model != "" {
		return model
	}
	return fallback
}

func requireCodingAgentContinuationBinary(t *testing.T, binaryName string) {
	t.Helper()
	if strings.TrimSpace(binaryName) == "" {
		return
	}
	if _, err := exec.LookPath(binaryName); err != nil {
		t.Skipf("%s not found in PATH", binaryName)
	}
}

func requireCodingAgentContinuationPiRuntime(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("PI_BIN")) != "" {
		return
	}
	if _, err := exec.LookPath("pi"); err == nil {
		return
	}
	if _, err := exec.LookPath("npx"); err == nil {
		return
	}
	t.Skip("pi not found in PATH and npx fallback unavailable")
}

func killCodingAgentContinuationTmuxSession(t *testing.T, sessionName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", sessionName).CombinedOutput()
	if err != nil {
		t.Fatalf("kill tmux session %q: %v output=%s", sessionName, err, string(out))
	}
}
