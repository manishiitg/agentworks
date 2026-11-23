package mcpagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	claudecode "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"
)

func TestRestoredAgyTransportLaunchEnablesInteractiveLane(t *testing.T) {
	a := &Agent{provider: llm.ProviderAgyCLI, modelID: "gemini-3.8-flash-high", sessionID: "work:project:test"}
	if err := a.prepareCodingAgentTransportLaunch(); err != nil {
		t.Fatal(err)
	}
	got := metadataFromCallOptions(a.appendCodingAgentInteractiveOptions(nil))
	if got[agycli.MetadataKeyInteractiveSessionID] != "work:project:test" || got[agycli.MetadataKeyPersistentInteractive] != "true" {
		t.Fatalf("restored AGY launch metadata = %#v, want an owned persistent interactive session", got)
	}
}

func TestAppendCodingAgentInteractiveOptions(t *testing.T) {
	tests := []struct {
		name            string
		agent           *Agent
		wantSessionKey  string
		wantPersistKey  string
		wantSessionID   string
		wantPersistence bool
		wantWorkingKey  string
		wantWorkingDir  string
	}{
		{
			name: "claude code persistent chat",
			agent: &Agent{
				provider:                               llm.ProviderClaudeCode,
				sessionID:                              " chat-session-1 ",
				claudeCodePersistentInteractiveSession: true,
				codingAgentWorkingDir:                  " /tmp/user-chat ",
			},
			wantSessionKey:  claudecode.MetadataKeyInteractiveSessionID,
			wantPersistKey:  claudecode.MetadataKeyPersistentInteractive,
			wantSessionID:   "chat-session-1",
			wantPersistence: true,
			wantWorkingKey:  claudecode.MetadataKeyWorkingDir,
			wantWorkingDir:  "/tmp/user-chat",
		},
		{
			name: "codex persistent chat",
			agent: &Agent{
				provider:                          llm.ProviderCodexCLI,
				sessionID:                         "chat-session-2",
				codexPersistentInteractiveSession: true,
				codingAgentWorkingDir:             "/tmp/codex-chat",
			},
			wantSessionKey:  codexcli.MetadataKeyInteractiveSessionID,
			wantPersistKey:  codexcli.MetadataKeyPersistentInteractive,
			wantSessionID:   "chat-session-2",
			wantPersistence: true,
			wantWorkingKey:  codexcli.MetadataKeyProjectDirID,
			wantWorkingDir:  "/tmp/codex-chat",
		},
		{
			name: "cursor persistent chat",
			agent: &Agent{
				provider:                           llm.ProviderCursorCLI,
				sessionID:                          "chat-session-4",
				cursorPersistentInteractiveSession: true,
				codingAgentWorkingDir:              "/tmp/cursor-chat",
			},
			wantSessionKey:  cursorcli.MetadataKeyInteractiveSessionID,
			wantPersistKey:  cursorcli.MetadataKeyPersistentInteractive,
			wantSessionID:   "chat-session-4",
			wantPersistence: true,
			wantWorkingKey:  cursorcli.MetadataKeyWorkingDir,
			wantWorkingDir:  "/tmp/cursor-chat",
		},
		{
			name: "codex workflow uses bounded interactive lifecycle",
			agent: &Agent{
				provider:                          llm.ProviderCodexCLI,
				sessionID:                         "workflow-session",
				codexPersistentInteractiveSession: false,
			},
			wantSessionKey:  codexcli.MetadataKeyInteractiveSessionID,
			wantPersistKey:  codexcli.MetadataKeyPersistentInteractive,
			wantSessionID:   "workflow-session",
			wantPersistence: false,
		},
		{
			name: "pi persistent chat",
			agent: &Agent{
				provider:                       llm.ProviderPiCLI,
				sessionID:                      "chat-session-6",
				piPersistentInteractiveSession: true,
				codingAgentWorkingDir:          "/tmp/pi-chat",
			},
			wantSessionKey:  picli.MetadataKeyInteractiveSessionID,
			wantPersistKey:  picli.MetadataKeyPersistentInteractive,
			wantSessionID:   "chat-session-6",
			wantPersistence: true,
			wantWorkingKey:  picli.MetadataKeyWorkingDir,
			wantWorkingDir:  "/tmp/pi-chat",
		},
		{
			name: "missing owner session produces no coding-agent metadata",
			agent: &Agent{
				provider:                          llm.ProviderCodexCLI,
				codexPersistentInteractiveSession: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := metadataFromCallOptions(tt.agent.appendCodingAgentInteractiveOptions(nil))

			if tt.wantSessionKey == "" && tt.wantWorkingKey == "" {
				if len(got) != 0 {
					t.Fatalf("metadata = %#v, want empty", got)
				}
				return
			}

			if tt.wantSessionKey != "" && got[tt.wantSessionKey] != tt.wantSessionID {
				t.Fatalf("session metadata %q = %#v, want %q", tt.wantSessionKey, got[tt.wantSessionKey], tt.wantSessionID)
			}
			if tt.wantPersistKey != "" {
				persistentValue, hasPersistentValue := got[tt.wantPersistKey]
				if tt.wantPersistence {
					if persistentValue != true {
						t.Fatalf("persistent metadata %q = %#v, want true", tt.wantPersistKey, persistentValue)
					}
				} else if hasPersistentValue && persistentValue != false {
					t.Fatalf("persistent metadata %q = %#v, want absent or false", tt.wantPersistKey, persistentValue)
				}
			}
			if tt.wantWorkingKey != "" && got[tt.wantWorkingKey] != tt.wantWorkingDir {
				t.Fatalf("working dir metadata %q = %#v, want %q", tt.wantWorkingKey, got[tt.wantWorkingKey], tt.wantWorkingDir)
			}
		})
	}
}

func TestAppendCodingAgentWorkingDirOptionCleansInactiveGeneratedArtifacts(t *testing.T) {
	workDir := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(workDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite(".claude/skills/system-tools/SKILL.md", "generated")
	mustWrite(".claude/skills/system-tools/"+projectfile.SkillMarkerFile, "x")
	mustWrite(".pi/skills/system-tools/SKILL.md", "generated")
	mustWrite(".pi/skills/system-tools/"+projectfile.SkillMarkerFile, "x")
	mustWrite(".pi/APPEND_SYSTEM.md", "<!-- mlp-session-instructions -->\n")
	mustWrite(".pi/mcp.json", `{"mcpServers":{"api-bridge":{"command":"mcpbridge"}}}`)
	mustWrite(".agents/skills/system-tools/SKILL.md", "generated")
	mustWrite(".agents/skills/system-tools/"+projectfile.SkillMarkerFile, "x")
	mustWrite(".agents/worker/keep.txt", "background runtime")
	mustWrite("CLAUDE.md", "<!-- mlp-session-instructions -->\n")
	mustWrite("AGENTS.md", "<!-- mlp-session-instructions -->\n")

	agent := &Agent{
		provider:                           llm.ProviderCursorCLI,
		sessionID:                          "cursor-session",
		cursorPersistentInteractiveSession: true,
		codingAgentWorkingDir:              workDir,
	}
	_ = metadataFromCallOptions(agent.appendCodingAgentWorkingDirOptionForProvider(nil, llm.ProviderCursorCLI, "cursor-cli"))

	for _, rel := range []string{".claude", ".pi", ".agents/skills", "CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(workDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed as inactive generated artifact, stat err=%v", rel, err)
		}
	}
	if body, err := os.ReadFile(filepath.Join(workDir, ".agents/worker/keep.txt")); err != nil || string(body) != "background runtime" {
		t.Fatalf("unmarked runtime content must survive: body=%q err=%v", body, err)
	}
}

func TestAppendCodingAgentWorkingDirOptionPreservesUnmarkedProviderFilesInWorkflow(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "workspace-docs", "Workflow", "demo")
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(workDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite(".claude/skills/system-tools/SKILL.md", "generated")
	mustWrite(".pi/APPEND_SYSTEM.md", "generated")
	mustWrite(".gemini/settings.json", `{"generated":true}`)
	mustWrite(".agents/skills/system-tools/SKILL.md", "generated")
	mustWrite(".agents/save-0001/marker.txt", "stale runtime")
	mustWrite(".cursor/mcp.json", `{"mcpServers":{"api-bridge":{"command":"mcpbridge"}}}`)

	agent := &Agent{
		provider:              llm.ProviderCursorCLI,
		modelID:               "cursor-cli",
		sessionID:             "cursor-session",
		codingAgentWorkingDir: workDir,
	}
	_ = metadataFromCallOptions(agent.appendCodingAgentWorkingDirOptionForProvider(nil, llm.ProviderCursorCLI, "cursor-cli"))

	for rel, expected := range map[string]string{
		".claude/skills/system-tools/SKILL.md": "generated",
		".pi/APPEND_SYSTEM.md":                 "generated",
		".gemini/settings.json":                `{"generated":true}`,
		".agents/skills/system-tools/SKILL.md": "generated",
		".agents/save-0001/marker.txt":         "stale runtime",
	} {
		body, err := os.ReadFile(filepath.Join(workDir, rel))
		if err != nil || string(body) != expected {
			t.Fatalf("unmarked file %s changed: body=%q err=%v", rel, body, err)
		}
	}
	if _, err := os.Stat(filepath.Join(workDir, ".cursor", "mcp.json")); err != nil {
		t.Fatalf("active cursor folder should be preserved before cursor adapter rewrites it: %v", err)
	}
}

// Only what a session projected is removed from an inactive provider's folder;
// the project's own files (a Cursor mcp.json with the user's servers) stay.
func TestCleanupInactiveCodingAgentArtifactsRemovesInactiveCursorDir(t *testing.T) {
	workDir := t.TempDir()
	cursorDir := filepath.Join(workDir, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	userMCP := filepath.Join(cursorDir, "mcp.json")
	body := `{"mcpServers":{"github":{"command":"docker","args":["run","github-mcp"]}}}`
	if err := os.WriteFile(userMCP, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cursorDir, "skills", "system-tools"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cursorDir, "skills", "system-tools", "SKILL.md"), []byte("generated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cursorDir, "skills", "system-tools", projectfile.SkillMarkerFile), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cleanupInactiveCodingAgentProjectArtifacts(workDir, llm.ProviderClaudeCode)

	if _, err := os.Stat(filepath.Join(cursorDir, "skills", "system-tools")); !os.IsNotExist(err) {
		t.Fatalf("a projected skill should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(userMCP); err != nil {
		t.Fatalf("the project's own .cursor/mcp.json must survive: %v", err)
	}
}

func TestCodingCLIWorkingDirOptionCoverage(t *testing.T) {
	cases := []struct {
		name        string
		provider    llm.Provider
		modelID     string
		metadataKey string
	}{
		{name: "claude code", provider: llm.ProviderClaudeCode, metadataKey: claudecode.MetadataKeyWorkingDir},
		{name: "codex cli", provider: llm.ProviderCodexCLI, metadataKey: codexcli.MetadataKeyProjectDirID},
		{name: "cursor cli", provider: llm.ProviderCursorCLI, metadataKey: cursorcli.MetadataKeyWorkingDir},
		{name: "pi cli", provider: llm.ProviderPiCLI, metadataKey: picli.MetadataKeyWorkingDir},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !isCodingCLIProvider(tc.provider, tc.modelID) {
				t.Fatalf("%s/%s should be recognized as a coding CLI provider", tc.provider, tc.modelID)
			}
			option, ok := codingAgentWorkingDirOptionForProvider(tc.provider, tc.modelID)
			if !ok {
				t.Fatalf("missing working-directory option for coding CLI provider=%s model=%s", tc.provider, tc.modelID)
			}
			got := metadataFromCallOptions([]llmtypes.CallOption{option("/tmp/workdir")})
			if got[tc.metadataKey] != "/tmp/workdir" {
				t.Fatalf("metadata %q = %#v, want /tmp/workdir", tc.metadataKey, got[tc.metadataKey])
			}
		})
	}
}

func TestCodingAgentIntegrationAppenderCoverage(t *testing.T) {
	for provider := range codingAgentIntegrationAppenders {
		contract, ok := llm.GetCodingAgentProviderContract(provider, "")
		if !ok {
			t.Errorf("integration appender has %s but no coding-agent contract", provider)
			continue
		}
		if !contract.UsesMCPBridge {
			t.Errorf("integration appender has %s but contract does not use MCP bridge", provider)
		}
	}
	// Reverse direction: every contract that requires bridge config must
	// have a registered binding. Iterating only the map cannot detect a
	// missing entry for a newly onboarded provider.
	for _, contract := range llm.CodingAgentProviderContracts() {
		if !contract.RequiresMCPBridgeConfig {
			continue
		}
		if _, ok := codingAgentIntegrationAppenders[contract.Provider]; !ok {
			t.Errorf("contract requires MCP bridge config for %s but no integration appender is registered", contract.Provider)
		}
	}
}

func TestApplyCodingAgentIntegrationOptionsRejectsMissingBinding(t *testing.T) {
	// A bridge-required provider without a registered binding must fail
	// before launch, not start without its orchestration context.
	codex, ok := codingAgentIntegrationAppenders[llm.ProviderCodexCLI]
	if !ok {
		t.Fatal("test requires a registered codex-cli appender to remove")
	}
	delete(codingAgentIntegrationAppenders, llm.ProviderCodexCLI)
	defer func() { codingAgentIntegrationAppenders[llm.ProviderCodexCLI] = codex }()

	_, err := applyCodingAgentIntegrationOptions(nil, nil, LLMModel{Provider: "codex-cli", ModelID: "gpt-5.3-codex-spark"})
	if err == nil || !strings.Contains(err.Error(), "requires an MCP bridge integration binding") {
		t.Fatalf("err = %v, want a missing-binding rejection", err)
	}
	// Non-coding providers pass through untouched.
	if _, err := applyCodingAgentIntegrationOptions(nil, nil, LLMModel{Provider: "openai", ModelID: "gpt-5"}); err != nil {
		t.Fatalf("non-coding provider err = %v, want nil", err)
	}
}

func TestCodingAgentNativeSessionBindingsCoverResumeContracts(t *testing.T) {
	// Every contract claiming native resume must have native session ID
	// getter/setter bindings. Missing entries would silently drop the
	// provider's native identity on restore.
	for _, contract := range llm.CodingAgentProviderContracts() {
		if !contract.SupportsNativeResume {
			continue
		}
		if _, ok := codingAgentNativeSessionIDSetters[contract.Provider]; !ok {
			t.Errorf("contract claims native resume for %s but no native session ID setter is registered", contract.Provider)
		}
		if _, ok := codingAgentNativeSessionIDGetters[contract.Provider]; !ok {
			t.Errorf("contract claims native resume for %s but no native session ID getter is registered", contract.Provider)
		}
	}
}

func TestAnnotateUnifiedCompletionEventMarksCodingAgentTerminalFormat(t *testing.T) {
	agent := &Agent{
		provider: llm.ProviderCodexCLI,
		modelID:  "gpt-5.3-codex-spark",
	}
	event := events.NewUnifiedCompletionEvent("simple", "simple", "question", "answer", "completed", time.Second, 1)

	agent.annotateUnifiedCompletionEvent(event)

	if event.Metadata["provider"] != string(llm.ProviderCodexCLI) {
		t.Fatalf("provider metadata = %#v, want %q", event.Metadata["provider"], llm.ProviderCodexCLI)
	}
	if event.Metadata["model_id"] != "gpt-5.3-codex-spark" {
		t.Fatalf("model_id metadata = %#v, want gpt-5.3-codex-spark", event.Metadata["model_id"])
	}
	if event.Metadata["coding_agent_terminal_format"] != true {
		t.Fatalf("coding_agent_terminal_format metadata = %#v, want true", event.Metadata["coding_agent_terminal_format"])
	}
}

func TestFinalAssistantTextFromCodingTrailUsesLastTextualAssistantMessage(t *testing.T) {
	messages := []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "I am checking the files."}},
		},
		{
			Role:  llmtypes.ChatMessageTypeTool,
			Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{Content: "tool output"}},
		},
		{
			Role: llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{
				llmtypes.ToolCall{Type: "function"},
				llmtypes.TextContent{Text: "\nFinal answer from the sidecar.\n"},
			},
		},
	}

	if got := finalAssistantTextFromCodingTrail(messages); got != "Final answer from the sidecar." {
		t.Fatalf("final sidecar text = %q", got)
	}
}

func TestWithClaudeCodeTransport(t *testing.T) {
	agent := &Agent{}
	withClaudeCodeTransport(llm.ClaudeCodeTransportTmux)(agent)
	if agent.claudeCodeTransport != llm.ClaudeCodeTransportTmux {
		t.Fatalf("ClaudeCodeTransport = %q, want %q", agent.claudeCodeTransport, llm.ClaudeCodeTransportTmux)
	}
	// PLAT-872-100: on a server whose default is the Agent SDK, a chat with the terminal on resumed its saved
	// conversation on the SDK (the handle took the server-wide contract) and never showed a live terminal.
	t.Setenv("CLAUDE_CODE_TRANSPORT", "sdk")
	agent.provider, agent.modelID = llm.ProviderClaudeCode, "claude-sonnet-5-5"
	saved := llmtypes.CodingProviderSessionHandle{Provider: "claude-code", Transport: "sdk", NativeSessionID: "native-1"}
	if got := agent.withCurrentCodingTransport(saved).Transport; got != llmtypes.CodingProviderTransportTmux {
		t.Fatalf("terminal chat resumes on %q, want tmux", got)
	}
	if !agent.supportsSteering() {
		t.Fatal("a terminal chat must take live input")
	}
	agent.claudeCodeTransport = ""
	if got := agent.withCurrentCodingTransport(saved).Transport; got != "sdk" {
		t.Fatalf("chat without a choice resumes on %q, want the server's sdk", got)
	}
}

// Cursor's --mode ask is a conversational stance that refuses natural-language
// writes with "Switch to Agent mode", which makes the chat unusable for any
// turn that requires writes. CursorBridgeToolsMode must NOT set --mode ask
// from the interactive-session option layer. Cursor runs in default agent mode;
// MCP bridge config, --approve-mcps, and deny-builtin hooks are mounted later
// by appendCursorCLIIntegrationOptions.
func TestCursorBridgeToolsModeDoesNotForceAskMode(t *testing.T) {
	agent := &Agent{
		provider:                           llm.ProviderCursorCLI,
		sessionID:                          "chat-session-bridge",
		cursorPersistentInteractiveSession: true,
		cursorBridgeToolsMode:              true,
	}

	got := metadataFromCallOptions(agent.appendCodingAgentInteractiveOptions(nil))

	if mode, ok := got[cursorcli.MetadataKeyMode]; ok {
		t.Fatalf("mode metadata should NOT be set under bridge tools mode (ask mode breaks natural-language writes), got %#v", mode)
	}
	if approve, ok := got[cursorcli.MetadataKeyApproveMCPs]; ok {
		t.Fatalf("approve-mcps metadata should be added by Cursor integration options, not interactive options; got %#v", approve)
	}
	if approveWeb, ok := got[cursorcli.MetadataKeyAutoApproveWebSearch].(bool); !ok || !approveWeb {
		t.Fatalf("auto web approval metadata = %#v, want true", got[cursorcli.MetadataKeyAutoApproveWebSearch])
	}
}

func metadataFromCallOptions(options []llmtypes.CallOption) map[string]interface{} {
	opts := &llmtypes.CallOptions{}
	for _, option := range options {
		option(opts)
	}
	if opts.Metadata == nil || opts.Metadata.Custom == nil {
		return map[string]interface{}{}
	}
	return opts.Metadata.Custom
}
