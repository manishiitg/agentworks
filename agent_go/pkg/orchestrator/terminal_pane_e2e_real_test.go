package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	openaisdk "github.com/openai/openai-go/v3"
	openaisdkoption "github.com/openai/openai-go/v3/option"
	"google.golang.org/genai"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	anthropicadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/anthropic"
	azureadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/azure"
	bedrockadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/bedrock"
	claudecodeadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	codexcliadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	cursorcliadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	minimaxadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/minimax"
	openaiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/openai"
	vertexadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"
)

// TestTerminalPaneCrossTransportReal proves the unified terminal-pane
// contract: regardless of transport (tmux pane scrape / structured CLI
// stream-json / direct API), every adapter call surfaces at least one
// non-empty StreamChunkTypeTerminal chunk on the caller's StreamChan.
//
// The frontend's terminal panel listens for these chunks. If any
// transport falls silent — the regression that landed in 298c1b66
// and blanked the panel for every tmux step — this test fires.
//
// Each sub-test gates independently:
//
//   - api/anthropic:      RUN_ANTHROPIC_REAL_E2E=1 + ANTHROPIC_API_KEY
//   - api/openai:         RUN_OPENAI_REAL_E2E=1 + OPENAI_API_KEY
//   - api/vertex:         RUN_VERTEX_REAL_E2E=1 + GEMINI_API_KEY
//   - structured_cli/claudecode: RUN_CLAUDECODE_REAL_E2E=1 + claude binary in PATH
//   - structured_cli/codex:      RUN_CODEX_REAL_E2E=1 + codex binary in PATH
//
// Skipping is silent — running with no env exercises nothing, runs
// fast, and never falses. CI sets the relevant flag per environment.
func TestTerminalPaneCrossTransportReal(t *testing.T) {
	cases := []terminalPaneCase{
		{
			class:    "api",
			provider: "anthropic",
			gate:     "RUN_ANTHROPIC_REAL_E2E",
			build:    buildAnthropicTerminalAdapter,
		},
		{
			class:    "api",
			provider: "openai",
			gate:     "RUN_OPENAI_REAL_E2E",
			build:    buildOpenAITerminalAdapter,
		},
		{
			class:    "api",
			provider: "vertex",
			gate:     "RUN_VERTEX_REAL_E2E",
			build:    buildVertexTerminalAdapter,
		},
		{
			class:    "structured_cli",
			provider: "claudecode",
			gate:     "RUN_CLAUDECODE_REAL_E2E",
			build:    buildClaudeCodeTerminalAdapter,
		},
		{
			class:    "structured_cli",
			provider: "codex",
			gate:     "RUN_CODEX_REAL_E2E",
			build:    buildCodexTerminalAdapter,
		},
		{
			class:    "tmux",
			provider: "codex",
			gate:     "RUN_CODEX_CLI_INTERACTIVE_E2E",
			build:    buildCodexTmuxTerminalAdapter,
		},
		{
			class:    "api",
			provider: "azure",
			gate:     "RUN_AZURE_REAL_E2E",
			build:    buildAzureTerminalAdapter,
		},
		{
			class:    "api",
			provider: "bedrock",
			gate:     "RUN_BEDROCK_REAL_E2E",
			build:    buildBedrockTerminalAdapter,
		},
		{
			class:    "api",
			provider: "minimax",
			gate:     "RUN_MINIMAX_REAL_E2E",
			build:    buildMinimaxTerminalAdapter,
		},
		{
			class:    "structured_cli",
			provider: "cursor-cli",
			gate:     "RUN_CURSOR_CLI_REAL_E2E",
			build:    buildCursorCLITerminalAdapter,
		},
		{
			class:    "tmux",
			provider: "cursor-cli",
			gate:     "RUN_CURSOR_CLI_INTERACTIVE_E2E",
			build:    buildCursorCLITmuxTerminalAdapter,
		},
		{
			class:    "tmux",
			provider: "claudecode",
			gate:     "RUN_CLAUDECODE_INTERACTIVE_E2E",
			build:    buildClaudeCodeTmuxTerminalAdapter,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.class+"/"+tc.provider, func(t *testing.T) {
			if os.Getenv(tc.gate) == "" {
				t.Skipf("set %s=1 to run this terminal-pane e2e", tc.gate)
			}
			adapter, extraOpts, skipReason := tc.build(t)
			if skipReason != "" {
				t.Skip(skipReason)
			}

			streamChan := make(chan llmtypes.StreamChunk, 256)
			var (
				mu             sync.Mutex
				terminalChunks []llmtypes.StreamChunk
				done           = make(chan struct{})
			)
			go func() {
				for chunk := range streamChan {
					if chunk.Type == llmtypes.StreamChunkTypeTerminal {
						mu.Lock()
						terminalChunks = append(terminalChunks, chunk)
						mu.Unlock()
					}
				}
				close(done)
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			callOpts := []llmtypes.CallOption{
				llmtypes.WithMaxTokens(32),
				llmtypes.WithStreamingChan(streamChan),
			}
			callOpts = append(callOpts, extraOpts...)
			// Multi-turn history: user → asst → tool call → tool result
			// → user (latest). Exercises the synthetic terminal's
			// formatConversationLines path, so the pane shows assistant
			// text, tool calls, and tool results — not just the final
			// user line. This is the case the old single-message test
			// failed to catch (real workflow steps run many turns).
			multiTurn := []llmtypes.MessageContent{
				{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "What is 2+2?"}}},
				{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Let me check using a calculator tool."}}},
				{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{
					ID: "call_1", Type: "function",
					FunctionCall: &llmtypes.FunctionCall{Name: "calculator", Arguments: `{"expr":"2+2"}`},
				}}},
				{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{
					ToolCallID: "call_1", Name: "calculator", Content: "4",
				}}},
				{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with: OK"}}},
			}
			_, err := adapter.GenerateContent(ctx, multiTurn, callOpts...)
			// Adapters own the close of StreamChan (some defer-close
			// from inside their streaming goroutine). Drain via a safe
			// close — recover catches the "close of closed channel"
			// race if the adapter beat us to it.
			func() {
				defer func() { _ = recover() }()
				close(streamChan)
			}()
			<-done

			if err != nil {
				t.Fatalf("%s/%s GenerateContent: %v", tc.class, tc.provider, err)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(terminalChunks) == 0 {
				t.Fatalf("%s/%s emitted zero StreamChunkTypeTerminal chunks; the terminal pane would be empty for this transport", tc.class, tc.provider)
			}

			// Assert at least one chunk carries non-empty content.
			// (Empty snapshots would render as a blank pane — same
			// failure mode as no chunks at all, just stealthier.)
			sawNonEmpty := false
			for _, c := range terminalChunks {
				if strings.TrimSpace(c.Content) != "" {
					sawNonEmpty = true
					break
				}
			}
			if !sawNonEmpty {
				t.Fatalf("%s/%s emitted %d terminal chunks but all had empty content", tc.class, tc.provider, len(terminalChunks))
			}

			// Assert the multi-turn conversation rendered into the
			// pane. The last snapshot should carry markers for user,
			// assistant, tool call, and tool result lines. Tmux
			// providers scrape a real pane so they bypass synthetic
			// rendering — skip the markers for those.
			if tc.class != "tmux" {
				lastContent := terminalChunks[len(terminalChunks)-1].Content
				required := []struct {
					marker string
					what   string
				}{
					{"> user:", "user message"},
					{"< asst:", "assistant message"},
					{"→ tool:", "tool call"},
					{"✓ result", "tool result"},
				}
				for _, r := range required {
					if !strings.Contains(lastContent, r.marker) {
						t.Fatalf("%s/%s pane missing %s marker %q in final snapshot:\n%s",
							tc.class, tc.provider, r.what, r.marker, lastContent)
					}
				}
			}

			t.Logf("✅ %s/%s: %d terminal chunks, last content length=%d",
				tc.class, tc.provider, len(terminalChunks),
				len(terminalChunks[len(terminalChunks)-1].Content))
		})
	}
}

// terminalPaneCase is one row in the cross-transport matrix. The
// builder returns the adapter, any per-call CallOptions the transport
// needs (e.g. WithInteractiveSessionID for tmux), and a skip reason
// so missing binaries / unconfigured providers skip cleanly without
// failing the suite.
type terminalPaneCase struct {
	class    string
	provider string
	gate     string
	build    func(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string)
}

func buildAnthropicTerminalAdapter(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string) {
	t.Helper()
	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if apiKey == "" {
		return nil, nil, "ANTHROPIC_API_KEY required"
	}
	model := strings.TrimSpace(os.Getenv("ANTHROPIC_REAL_E2E_MODEL"))
	if model == "" {
		model = "claude-haiku-4-5"
	}
	client := anthropic.NewClient(anthropicoption.WithAPIKey(apiKey))
	return anthropicadapter.NewAnthropicAdapter(client, model, silentLogger{}), nil, ""
}

func buildOpenAITerminalAdapter(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string) {
	t.Helper()
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil, nil, "OPENAI_API_KEY required"
	}
	model := strings.TrimSpace(os.Getenv("OPENAI_REAL_E2E_MODEL"))
	if model == "" {
		model = "gpt-5.4-mini"
	}
	client := openaisdk.NewClient(openaisdkoption.WithAPIKey(apiKey))
	return openaiadapter.NewOpenAIAdapter(&client, model, silentLogger{}), nil, ""
}

func buildVertexTerminalAdapter(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string) {
	t.Helper()
	var apiKey string
	for _, name := range []string{"GEMINI_API_KEY", "VERTEX_API_KEY", "GOOGLE_API_KEY"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			apiKey = v
			break
		}
	}
	if apiKey == "" {
		return nil, nil, "GEMINI_API_KEY (or VERTEX_API_KEY/GOOGLE_API_KEY) required"
	}
	model := strings.TrimSpace(os.Getenv("VERTEX_REAL_E2E_MODEL"))
	if model == "" {
		model = "gemini-3.1-flash-lite-preview"
	}
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		t.Fatalf("genai.NewClient: %v", err)
	}
	return vertexadapter.NewGoogleGenAIAdapter(client, model, silentLogger{}), nil, ""
}

func buildClaudeCodeTerminalAdapter(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string) {
	t.Helper()
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, nil, "claude CLI not in PATH"
	}
	model := strings.TrimSpace(os.Getenv("CLAUDECODE_REAL_E2E_MODEL"))
	if model == "" {
		model = "claude-haiku-4-5"
	}
	return claudecodeadapter.NewClaudeCodeAdapter("", model, silentLogger{}), nil, ""
}

func buildCodexTerminalAdapter(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string) {
	t.Helper()
	if _, err := exec.LookPath("codex"); err != nil {
		return nil, nil, "codex CLI not in PATH"
	}
	model := strings.TrimSpace(os.Getenv("CODEX_REAL_E2E_MODEL"))
	if model == "" {
		model = "codex-cli"
	}
	return codexcliadapter.NewCodexCLIAdapter("", model, silentLogger{}), nil, ""
}

func buildAzureTerminalAdapter(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string) {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("AZURE_OPENAI_ENDPOINT"))
	apiKey := strings.TrimSpace(os.Getenv("AZURE_OPENAI_API_KEY"))
	if endpoint == "" || apiKey == "" {
		return nil, nil, "AZURE_OPENAI_ENDPOINT + AZURE_OPENAI_API_KEY required"
	}
	apiVersion := strings.TrimSpace(os.Getenv("AZURE_OPENAI_API_VERSION"))
	if apiVersion == "" {
		apiVersion = "2024-02-01"
	}
	model := strings.TrimSpace(os.Getenv("AZURE_REAL_E2E_MODEL"))
	if model == "" {
		model = "gpt-5.4-mini"
	}
	return azureadapter.NewAzureAdapter(azureadapter.AzureConfig{
		Endpoint:   endpoint,
		APIKey:     apiKey,
		APIVersion: apiVersion,
	}, model, silentLogger{}), nil, ""
}

func buildBedrockTerminalAdapter(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID")) == "" && strings.TrimSpace(os.Getenv("AWS_PROFILE")) == "" {
		return nil, nil, "AWS_ACCESS_KEY_ID or AWS_PROFILE required"
	}
	region := strings.TrimSpace(os.Getenv("AWS_REGION"))
	if region == "" {
		region = "us-east-1"
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(region))
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	client := bedrockruntime.NewFromConfig(cfg)
	model := strings.TrimSpace(os.Getenv("BEDROCK_REAL_E2E_MODEL"))
	if model == "" {
		model = "anthropic.claude-haiku-4-5-v1:0"
	}
	return bedrockadapter.NewBedrockAdapter(client, model, silentLogger{}), nil, ""
}

func buildMinimaxTerminalAdapter(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string) {
	t.Helper()
	apiKey := strings.TrimSpace(os.Getenv("MINIMAX_API_KEY"))
	if apiKey == "" {
		return nil, nil, "MINIMAX_API_KEY required"
	}
	model := strings.TrimSpace(os.Getenv("MINIMAX_REAL_E2E_MODEL"))
	if model == "" {
		model = "minimax-m1"
	}
	return minimaxadapter.NewMiniMaxAdapter(apiKey, model, silentLogger{}), nil, ""
}

func buildCursorCLITerminalAdapter(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string) {
	t.Helper()
	if _, err := exec.LookPath("cursor-agent"); err != nil {
		return nil, nil, "cursor-agent CLI not in PATH"
	}
	model := strings.TrimSpace(os.Getenv("CURSOR_CLI_REAL_E2E_MODEL"))
	if model == "" {
		model = "cursor-cli"
	}
	return cursorcliadapter.NewCursorCLIAdapter("", model, silentLogger{}), nil, ""
}

func buildCursorCLITmuxTerminalAdapter(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string) {
	t.Helper()
	for _, bin := range []string{"cursor-agent", "tmux", "node"} {
		if _, err := exec.LookPath(bin); err != nil {
			return nil, nil, "tmux interactive requires " + bin + " in PATH"
		}
	}
	model := strings.TrimSpace(os.Getenv("CURSOR_CLI_REAL_E2E_MODEL"))
	if model == "" {
		model = "cursor-cli"
	}
	t.Cleanup(func() {
		_ = cursorcliadapter.CleanupCursorCLIInteractiveSessions(context.Background())
	})
	owner := "cursor-tmux-terminal-pane-" + strings.ToLower(strings.ReplaceAll(time.Now().Format("150405.000"), ".", ""))
	return cursorcliadapter.NewCursorCLIAdapter("", model, silentLogger{}), []llmtypes.CallOption{
		cursorcliadapter.WithInteractiveSessionID(owner),
		cursorcliadapter.WithPersistentInteractiveSession(true),
	}, ""
}

func buildClaudeCodeTmuxTerminalAdapter(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string) {
	t.Helper()
	for _, bin := range []string{"claude", "tmux"} {
		if _, err := exec.LookPath(bin); err != nil {
			return nil, nil, "tmux interactive requires " + bin + " in PATH"
		}
	}
	model := strings.TrimSpace(os.Getenv("CLAUDECODE_REAL_E2E_MODEL"))
	if model == "" {
		model = "claude-haiku-4-5"
	}
	t.Cleanup(func() {
		_ = claudecodeadapter.CleanupClaudeCodeTmuxSessions(context.Background())
	})
	owner := "claudecode-tmux-terminal-pane-" + strings.ToLower(strings.ReplaceAll(time.Now().Format("150405.000"), ".", ""))
	return claudecodeadapter.NewClaudeCodeInteractiveAdapter(model, silentLogger{}), []llmtypes.CallOption{
		claudecodeadapter.WithInteractiveSessionID(owner),
		claudecodeadapter.WithPersistentInteractiveSession(true),
	}, ""
}

// buildCodexTmuxTerminalAdapter wires the codex interactive (tmux)
// transport — codex runs as a long-lived process inside a tmux
// session, the adapter scrapes the real pane via `tmux capture-pane`
// and forwards snapshots as StreamChunkTypeTerminal. This is the
// transport the synthetic-terminal helper does NOT cover (it doesn't
// need to — real pane content IS the terminal).
//
// Requires codex + tmux + node in PATH (mirrors the gate used by
// codexcli_real_contract_test.go). t.Cleanup tears down the tmux
// session so we don't leak across runs.
func buildCodexTmuxTerminalAdapter(t *testing.T) (llmtypes.Model, []llmtypes.CallOption, string) {
	t.Helper()
	for _, bin := range []string{"codex", "tmux", "node"} {
		if _, err := exec.LookPath(bin); err != nil {
			return nil, nil, "tmux interactive requires " + bin + " in PATH"
		}
	}
	model := strings.TrimSpace(os.Getenv("CODEX_REAL_E2E_MODEL"))
	if model == "" {
		model = "gpt-5.4-mini"
	}
	t.Cleanup(func() {
		_ = codexcliadapter.CleanupCodexCLIInteractiveSessions(context.Background())
	})
	adapter := codexcliadapter.NewCodexCLIAdapter("", model, silentLogger{})
	ownerSessionID := "codex-tmux-terminal-pane-" + strings.ToLower(strings.ReplaceAll(time.Now().Format("150405.000"), ".", ""))
	extraOpts := []llmtypes.CallOption{
		codexcliadapter.WithInteractiveSessionID(ownerSessionID),
		codexcliadapter.WithPersistentInteractiveSession(true),
		codexcliadapter.WithApprovalPolicy("never"),
		codexcliadapter.WithReasoningEffort("low"),
	}
	return adapter, extraOpts, ""
}
