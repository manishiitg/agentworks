package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/tmuxlaunch"
)

func TestClaudeInteractiveStreamTmuxScreenFlag(t *testing.T) {
	if !claudeInteractiveStreamTmuxScreenEnabled(nil) {
		t.Fatal("tmux screen streaming should be enabled by default")
	}

	var opts llmtypes.CallOptions
	WithStreamTmuxScreen(true)(&opts)
	if !claudeInteractiveStreamTmuxScreenEnabled(&opts) {
		t.Fatal("tmux screen streaming should be enabled when explicitly set true")
	}

	WithStreamTmuxScreen(false)(&opts)
	if claudeInteractiveStreamTmuxScreenEnabled(&opts) {
		t.Fatal("tmux screen streaming should be disabled when explicitly set false")
	}
}

func TestClaudeTerminalStreamCapturesRawScreenRows(t *testing.T) {
	fakeBin := t.TempDir()
	argsPath := fakeBin + "/capture-args.log"
	tmuxPath := fakeBin + "/tmux"
	script := `#!/bin/sh
if [ "$1" = "capture-pane" ]; then
  printf '%s\n' "$*" > "$TMUX_TEST_CAPTURE_ARGS"
  printf 'screen row one\nscreen row two\n'
  exit 0
fi
exit 1
`
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_TEST_CAPTURE_ARGS", argsPath)

	stream := make(chan llmtypes.StreamChunk, 1)
	var last string
	if !streamClaudeTerminalSnapshot(context.Background(), "raw-display-session", stream, &last) {
		t.Fatal("streamClaudeTerminalSnapshot returned false")
	}
	chunk := <-stream
	if chunk.Type != llmtypes.StreamChunkTypeTerminal {
		t.Fatalf("chunk type = %q, want terminal", chunk.Type)
	}
	if !strings.Contains(chunk.Content, "screen row one\nscreen row two") {
		t.Fatalf("chunk content = %q, want raw screen rows", chunk.Content)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read capture args: %v", err)
	}
	if !strings.Contains(string(args), " -J") {
		t.Fatalf("terminal display capture did not use joined rows (-J): %q", string(args))
	}
	if strings.Contains(string(args), " -S ") {
		t.Fatalf("terminal display capture should use visible pane only, got args: %q", string(args))
	}
}

func TestClaudeStartSessionDisablesPromptSuggestions(t *testing.T) {
	got := claudePromptSuggestionEnvArgs()
	want := []string{
		"-e", "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false",
		"-e", "CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1",
		"-e", "CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT=5400000",
		"-e", "CLAUDE_CODE_DISABLE_ALTERNATE_SCREEN=1",
		"-e", "ANTHROPIC_API_KEY=",
		"-e", "ANTHROPIC_AUTH_TOKEN=",
		"-e", "ANTHROPIC_BASE_URL=",
		"-e", "CLAUDE_CODE_OAUTH_TOKEN=",
	}
	for _, name := range claudeParentSessionEnvNames {
		want = append(want, "-e", name+"=")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claude prompt suggestion env args = %v, want %v", got, want)
	}
}

// The fullscreen renderer draws in the alternate screen (no scrollback), so
// the AgentWorks terminal viewer could not scroll Claude panes.
func TestClaudeStartSessionKeepsClassicRendererForScrollback(t *testing.T) {
	if got := claudePromptSuggestionEnvArgs(); !containsArg(got, "CLAUDE_CODE_DISABLE_ALTERNATE_SCREEN=1") {
		t.Fatalf("Claude env args = %v, want the classic (normal-screen) renderer", got)
	}
}

func TestClaudeStartSessionUsesSharedMCPTimeoutOverride(t *testing.T) {
	t.Setenv("CODING_AGENT_MCP_TOOL_TIMEOUT", "17m")
	got := claudePromptSuggestionEnvArgs()
	if !containsArg(got, "CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT=1020000") {
		t.Fatalf("Claude env args = %v, want 17-minute MCP timeout in milliseconds", got)
	}
}

func TestClaudeStartSessionKeepsOAuthTokenOutOfTmuxArgs(t *testing.T) {
	fakeBin := t.TempDir()
	argsPath := fakeBin + "/tmux-args.log"
	tmuxPath := fakeBin + "/tmux"
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_TEST_ARGS"
exit 0
`
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_TEST_ARGS", argsPath)

	const token = "workflow-oauth-secret"
	adapter := NewClaudeCodeInteractiveAdapterWithOAuthToken("claude-code", token, nil)
	if err := adapter.startSession(context.Background(), "oauth-session", []string{"claude"}, "", nil); err != nil {
		t.Fatalf("startSession returned error: %v", err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read tmux args: %v", err)
	}
	// The fake tmux reports success without launching the self-deleting wrapper.
	// Remove that private test artifact rather than relying on the production
	// delayed-cleanup timer, which may not fire before this test process exits.
	var launchScriptPath string
	for _, part := range strings.Split(string(args), "'") {
		if strings.Contains(part, "mlp-coding-agent-launch-") {
			launchScriptPath = part
			defer os.Remove(part)
		}
	}
	if strings.Contains(string(args), token) {
		t.Fatalf("tmux argv leaked OAuth token: %s", string(args))
	}
	for _, want := range []string{"ANTHROPIC_API_KEY=", "ANTHROPIC_AUTH_TOKEN=", "CLAUDE_CODE_OAUTH_TOKEN="} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("tmux args missing ambient credential reset %q: %s", want, string(args))
		}
	}
	launchScript, err := os.ReadFile(launchScriptPath)
	if err != nil {
		t.Fatalf("read private launch script: %v", err)
	}
	if !strings.Contains(string(launchScript), `export CLAUDE_CODE_OAUTH_TOKEN="$__MLP_CODING_AGENT_ENV_0"`) {
		t.Fatalf("launch script did not apply the workflow setup token through Claude Code's OAuth path: %s", launchScript)
	}
	if strings.Contains(string(launchScript), `export ANTHROPIC_AUTH_TOKEN="$__MLP_CODING_AGENT_ENV_0"`) {
		t.Fatalf("launch script incorrectly remapped the workflow OAuth token to a generic bearer token: %s", launchScript)
	}
}

func TestClaudeCredentialFingerprintDoesNotExposeToken(t *testing.T) {
	const token = "workflow-oauth-secret"
	fingerprint := claudeCredentialFingerprint(token)
	if fingerprint == token || strings.Contains(fingerprint, token) {
		t.Fatalf("fingerprint exposed token: %q", fingerprint)
	}
	if fingerprint != claudeCredentialFingerprint(token) {
		t.Fatal("fingerprint is not deterministic")
	}
	if fingerprint == claudeCredentialFingerprint("another-token") {
		t.Fatal("different tokens produced the same fingerprint")
	}
	if got := claudeCredentialFingerprint(""); got != "saved-login" {
		t.Fatalf("empty token fingerprint = %q, want saved-login", got)
	}
}

func TestClaudeInteractiveFinalEnvUsesScopedOAuthPath(t *testing.T) {
	const token = "workflow-oauth-secret"
	got := claudeInteractiveFinalEnv("  " + token + "  ")
	want := []string{"CLAUDE_CODE_OAUTH_TOKEN=" + token}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claude interactive final env = %v, want %v", got, want)
	}
	if containsArg(got, "ANTHROPIC_AUTH_TOKEN="+token) {
		t.Fatalf("interactive final env remapped the OAuth token to a generic bearer token: %v", got)
	}
	if got := claudeInteractiveFinalEnv("  "); got != nil {
		t.Fatalf("empty token final env = %v, want nil", got)
	}
}

func TestStripClaudeAmbientAuthEnvRemovesOnlyAuthKeys(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=stale-placeholder",
		"ANTHROPIC_AUTH_TOKEN=stale-token",
		"ANTHROPIC_BASE_URL=https://stale.example.com",
		"CLAUDE_CODE_OAUTH_TOKEN=stale-oauth",
		"HOME=/Users/example",
	}
	got := stripClaudeAmbientAuthEnv(base)
	want := []string{"PATH=/usr/bin", "HOME=/Users/example"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stripped env = %v, want %v", got, want)
	}
}

func TestClaudeInteractiveAuthenticationFailureDetection(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "expired setup token",
			content: "Please run /login · API Error: 401 OAuth access token has expired. Re-authenticate to continue.",
			want:    true,
		},
		{
			name:    "invalid setup token",
			content: "Failed to authenticate. API Error: 401 OAuth access token is invalid.",
			want:    true,
		},
		{
			name:    "ordinary discussion of a 401",
			content: "The tool returned API Error: 401, so I updated its request headers.",
			want:    false,
		},
		{
			name:    "login guidance without request failure",
			content: "Please run /login if you want to change accounts.",
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClaudeInteractiveAuthenticationFailure(tt.content); got != tt.want {
				t.Fatalf("isClaudeInteractiveAuthenticationFailure(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestClaudeStartSessionSetsHistoryLimit(t *testing.T) {
	fakeBin := t.TempDir()
	argsPath := fakeBin + "/tmux-args.log"
	tmuxPath := fakeBin + "/tmux"
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_TEST_ARGS"
exit 0
`
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_TEST_ARGS", argsPath)

	adapter := NewClaudeCodeInteractiveAdapter("claude-code", nil)
	if err := adapter.startSession(context.Background(), "history-session", []string{"claude"}, "", nil); err != nil {
		t.Fatalf("startSession returned error: %v", err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read tmux args: %v", err)
	}
	want := "set-option -t history-session history-limit " + defaultTmuxHistoryLimit
	if !strings.Contains(string(args), want) {
		t.Fatalf("tmux args missing history limit %q:\n%s", want, string(args))
	}
	wantBeforeCreate := "set-option -g history-limit " + defaultTmuxHistoryLimit + " ; new-session"
	if !strings.Contains(string(args), wantBeforeCreate) {
		t.Fatalf("tmux args did not set history limit before pane creation %q:\n%s", wantBeforeCreate, string(args))
	}
}

func TestClaudeInteractiveShellCommandUsesCallerWorkingDir(t *testing.T) {
	shell := writeExecutableTestShell(t, "zsh")
	t.Setenv("CODING_AGENT_LOGIN_SHELL", shell)
	t.Setenv("CODING_AGENT_SHELL_MODE", "")

	got := claudeInteractiveShellCommand([]string{"claude", "--system-prompt-file", "/tmp/sys.md"}, "/tmp/user chat")
	if !strings.HasPrefix(got, "'"+shell+"' '-ilc' ") {
		t.Fatalf("shell command = %q, want login shell prefix", got)
	}
	if !strings.Contains(got, "'/tmp/user chat'") {
		t.Fatalf("shell command = %q, want caller cwd passed to login shell", got)
	}
}

func TestClaudeInteractiveShellCommandDirectMode(t *testing.T) {
	t.Setenv("CODING_AGENT_SHELL_MODE", "direct")

	got := claudeInteractiveShellCommand([]string{"claude", "--system-prompt-file", "/tmp/sys.md"}, "/tmp/user chat")
	if !strings.HasPrefix(got, "cd '/tmp/user chat' && exec ") {
		t.Fatalf("shell command = %q, want direct cwd before exec", got)
	}
}

func writeExecutableTestShell(t *testing.T, name string) string {
	t.Helper()
	path := t.TempDir() + "/" + name
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write shell: %v", err)
	}
	return path
}

func TestTmuxBuildClaudeArgsDefaultsToNoInternalTools(t *testing.T) {
	adapter := NewClaudeCodeInteractiveAdapter("claude-code", &MockLogger{})
	args, tempFiles, err := adapter.buildClaudeArgs(&llmtypes.CallOptions{}, "", "7aa21987-0003-4d71-b887-ad73e29d2faf", "")
	if err != nil {
		t.Fatalf("buildClaudeArgs error = %v", err)
	}
	if len(tempFiles) != 0 {
		t.Fatalf("tempFiles = %v, want none", tempFiles)
	}

	if containsArg(args, "-p") || containsArg(args, "--print") {
		t.Fatalf("experimental args must not include print mode: %v", args)
	}
	if !containsArgPair(args, "--tools", "") {
		t.Fatalf("args = %v, want --tools empty string to disable internal tools", args)
	}
	if !containsArgPair(args, "--permission-mode", "dontAsk") {
		t.Fatalf("args = %v, want permission mode dontAsk", args)
	}
	if !containsArgPair(args, "--session-id", "7aa21987-0003-4d71-b887-ad73e29d2faf") {
		t.Fatalf("args = %v, want caller-provided native session id", args)
	}
	if !containsArg(args, "--name") {
		t.Fatalf("args = %v, want display name for Claude resume picker", args)
	}
	if containsArg(args, "--system-prompt") {
		t.Fatalf("args = %v, should not pass empty system prompt", args)
	}
}

func TestTmuxRejectsImageContent(t *testing.T) {
	adapter := NewClaudeCodeInteractiveAdapter("claude-code", &MockLogger{})

	_, err := adapter.GenerateContent(context.Background(), []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Describe this image."},
				llmtypes.ImageContent{SourceType: "base64", MediaType: "image/png", Data: "iVBORw0KGgo="},
			},
		},
	})
	if err == nil {
		t.Fatal("GenerateContent() error = nil, want unsupported image content error")
	}
	if !strings.Contains(err.Error(), "does not support llmtypes.ImageContent") {
		t.Fatalf("GenerateContent() error = %v, want image content unsupported error", err)
	}
}

func TestClaudeInteractiveSessionsFromTmuxListOnlyMatchesAdapterPrefix(t *testing.T) {
	out := strings.Join([]string{
		"mlp-claude-code-exp-111-aaaa",
		"user-work",
		"mlp-claude-code-experimental-other",
		"mlp-claude-code-exp",
		"mlp-claude-code-exp2-222-bbbb",
	}, "\n")

	got := claudeInteractiveSessionsFromTmuxList(out, "mlp-claude-code-exp")
	want := []string{
		"mlp-claude-code-exp-111-aaaa",
		"mlp-claude-code-exp",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sessions = %v, want %v", got, want)
	}
}

func TestClaudeTmuxSessionLostErrorDetection(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil},
		{name: "no server", err: errors.New("failed to capture Claude Code tmux session: exit status 1: no server running on /private/tmp/tmux-501/default"), want: true},
		{name: "missing pane", err: errors.New("failed to capture Claude Code tmux session: exit status 1: can't find pane: mlp-claude-code-1"), want: true},
		{name: "missing session", err: errors.New("tmux kill-session failed: can't find session: mlp-claude-code-exp-1"), want: true},
		{name: "typed lost session", err: llmtypes.WrapCodingAgentTmuxSessionLostError(errors.New("wrapped"), "claude-code", "mlp-claude-code-1", "tmux session lost"), want: true},
		{name: "ordinary timeout", err: context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClaudeTmuxSessionLostError(tt.err); got != tt.want {
				t.Fatalf("isClaudeTmuxSessionLostError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestTmuxBuildClaudeArgsPassesBridgeOptions(t *testing.T) {
	adapter := NewClaudeCodeInteractiveAdapter("claude-sonnet-4-6", &MockLogger{})
	opts := &llmtypes.CallOptions{}
	WithMCPConfig(`{"mcpServers":{"api-bridge":{"command":"/tmp/mcpbridge"}}}`)(opts)
	WithClaudeCodeTools("WebSearch")(opts)
	WithAllowedTools("mcp__api-bridge__*,WebSearch")(opts)
	WithResumeSessionID("7aa21987-0003-4d71-b887-ad73e29d2faf")(opts)
	WithEffort("high")(opts)

	args, tempFiles, err := adapter.buildClaudeArgs(opts, "", "7aa21987-0003-4d71-b887-ad73e29d2faf", "native system prompt")
	if err != nil {
		t.Fatalf("buildClaudeArgs error = %v", err)
	}
	defer removeFiles(tempFiles)

	if !containsArgPair(args, "--tools", "WebSearch") {
		t.Fatalf("args = %v, want --tools WebSearch", args)
	}
	if !containsArgPair(args, "--allowed-tools", "mcp__api-bridge__*,WebSearch") {
		t.Fatalf("args = %v, want allowed MCP bridge tools", args)
	}
	if !containsArg(args, "--strict-mcp-config") {
		t.Fatalf("args = %v, want --strict-mcp-config", args)
	}
	if !containsArgPair(args, "--model", "claude-sonnet-4-6") {
		t.Fatalf("args = %v, want model override", args)
	}
	if !containsArgPair(args, "--resume", "7aa21987-0003-4d71-b887-ad73e29d2faf") {
		t.Fatalf("args = %v, want resume session id", args)
	}
	if containsArg(args, "--session-id") {
		t.Fatalf("args = %v, resumed invocation must use --resume instead of starting a new session id", args)
	}
	if !containsArgPair(args, "--effort", "high") {
		t.Fatalf("args = %v, want effort", args)
	}
	systemPromptPath := argValue(args, "--system-prompt-file")
	if systemPromptPath == "" {
		t.Fatalf("args = %v, want native --system-prompt-file", args)
	}
	systemPromptBytes, err := os.ReadFile(systemPromptPath)
	if err != nil {
		t.Fatalf("read system prompt file: %v", err)
	}
	if string(systemPromptBytes) != "native system prompt" {
		t.Fatalf("system prompt file = %q, want native system prompt", string(systemPromptBytes))
	}
	if containsArg(args, "--system-prompt") {
		t.Fatalf("args = %v, system prompt must be passed by file to avoid command length limits", args)
	}
	if containsArg(args, "--append-system-prompt") {
		t.Fatalf("args = %v, tmux mode should replace with --system-prompt, not append", args)
	}
	if len(tempFiles) != 2 {
		t.Fatalf("tempFiles = %v, want MCP config and system prompt temp files", tempFiles)
	}
}

func TestSplitSystemPromptRemovesSystemMessagesFromConversation(t *testing.T) {
	systemPrompt, conversation := splitSystemPrompt([]llmtypes.MessageContent{
		textPartMessage(llmtypes.ChatMessageTypeSystem, "SYSTEM ONE"),
		textPartMessage(llmtypes.ChatMessageTypeHuman, "hello"),
		textPartMessage(llmtypes.ChatMessageTypeSystem, "SYSTEM TWO"),
		textPartMessage(llmtypes.ChatMessageTypeAI, "hi"),
	})

	if systemPrompt != "SYSTEM ONE\n\nSYSTEM TWO" {
		t.Fatalf("systemPrompt = %q, want joined system prompts", systemPrompt)
	}
	if len(conversation) != 2 {
		t.Fatalf("conversation len = %d, want 2", len(conversation))
	}
	for _, msg := range conversation {
		if msg.Role == llmtypes.ChatMessageTypeSystem {
			t.Fatalf("conversation contains system message: %+v", msg)
		}
	}
}

func TestBuildTmuxPromptResumeSendsOnlyLatestHumanMessage(t *testing.T) {
	opts := &llmtypes.CallOptions{}
	WithResumeSessionID("7aa21987-0003-4d71-b887-ad73e29d2faf")(opts)

	_, conversation := splitSystemPrompt([]llmtypes.MessageContent{
		textPartMessage(llmtypes.ChatMessageTypeSystem, "SYSTEM_SHOULD_BE_NATIVE"),
		textPartMessage(llmtypes.ChatMessageTypeHuman, "OLD_USER_SHOULD_NOT_BE_SENT"),
		textPartMessage(llmtypes.ChatMessageTypeAI, "OLD_ASSISTANT_SHOULD_NOT_BE_SENT"),
		textPartMessage(llmtypes.ChatMessageTypeHuman, "LATEST_USER_SHOULD_BE_SENT"),
	})
	prompt, err := buildTmuxPrompt(conversation, opts, claudeResumeIDFromOptions(opts), false)
	if err != nil {
		t.Fatalf("buildTmuxPrompt error = %v", err)
	}
	if !strings.Contains(prompt, "LATEST_USER_SHOULD_BE_SENT") {
		t.Fatalf("prompt = %q, want latest user message", prompt)
	}
	if strings.TrimSpace(prompt) != "LATEST_USER_SHOULD_BE_SENT" {
		t.Fatalf("resume prompt = %q, want only latest user message with no adapter wrapper", prompt)
	}
	if strings.Contains(prompt, "OLD_USER_SHOULD_NOT_BE_SENT") || strings.Contains(prompt, "OLD_ASSISTANT_SHOULD_NOT_BE_SENT") {
		t.Fatalf("resume prompt should not replay old conversation: %q", prompt)
	}
	if strings.Contains(prompt, "SYSTEM_SHOULD_BE_NATIVE") || strings.Contains(prompt, "SYSTEM:") {
		t.Fatalf("tmux prompt should not contain system messages; they are passed via --system-prompt: %q", prompt)
	}
	for _, forbidden := range []string{
		"tmux adapter",
		"Final answer format",
		"Start marker",
		"End marker",
		"MLP_CLAUDE_EXPERIMENTAL",
		"GitHub-flavored Markdown",
		"Markdown tables",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt contains adapter instruction %q: %q", forbidden, prompt)
		}
	}
}

func TestBuildTmuxPromptPersistentSendsOnlyLatestHumanMessage(t *testing.T) {
	_, conversation := splitSystemPrompt([]llmtypes.MessageContent{
		textPartMessage(llmtypes.ChatMessageTypeSystem, "SYSTEM_SHOULD_BE_NATIVE"),
		textPartMessage(llmtypes.ChatMessageTypeHuman, "OLD_USER_SHOULD_NOT_BE_SENT"),
		textPartMessage(llmtypes.ChatMessageTypeAI, "OLD_ASSISTANT_SHOULD_NOT_BE_SENT"),
		textPartMessage(llmtypes.ChatMessageTypeHuman, "LATEST_USER_SHOULD_BE_SENT"),
	})

	prompt, err := buildTmuxPrompt(conversation, &llmtypes.CallOptions{}, "", true)
	if err != nil {
		t.Fatalf("buildTmuxPrompt error = %v", err)
	}
	if strings.TrimSpace(prompt) != "LATEST_USER_SHOULD_BE_SENT" {
		t.Fatalf("persistent prompt = %q, want only latest user message", prompt)
	}
	if strings.Contains(prompt, "OLD_USER_SHOULD_NOT_BE_SENT") || strings.Contains(prompt, "OLD_ASSISTANT_SHOULD_NOT_BE_SENT") {
		t.Fatalf("persistent prompt should not replay old conversation: %q", prompt)
	}
	if strings.Contains(prompt, "SYSTEM_SHOULD_BE_NATIVE") || strings.Contains(prompt, "Previous conversation context") {
		t.Fatalf("persistent prompt should not contain native system/context wrappers: %q", prompt)
	}
}

func TestBuildTmuxPromptFreshSingleTurnSendsOnlyUserText(t *testing.T) {
	prompt, err := buildTmuxPrompt(
		[]llmtypes.MessageContent{
			textPartMessage(llmtypes.ChatMessageTypeHuman, "hi"),
		},
		&llmtypes.CallOptions{},
		"",
		false,
	)
	if err != nil {
		t.Fatalf("buildTmuxPrompt error = %v", err)
	}
	if strings.TrimSpace(prompt) != "hi" {
		t.Fatalf("fresh single-turn prompt = %q, want only user text", prompt)
	}
	for _, forbidden := range []string{"Conversation:", "HUMAN:", "User message:"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("fresh single-turn prompt contains wrapper %q: %q", forbidden, prompt)
		}
	}
}

func TestBuildTmuxPromptFreshSingleTurnPreservesUserTextVerbatim(t *testing.T) {
	userText := strings.Join([]string{
		"hi",
		"",
		"line with punctuation: !@#$%^&*()_+-=[]{}|;:'\",.<>/?`~",
		"unicode: नमस्ते こんにちは Привет café 🚀",
		"shell-looking text that must not execute: $(echo nope) && rm -rf /",
		"json-looking text: {\"message\":\"hello\\nworld\",\"ok\":true}",
		"role-looking text:",
		"HUMAN:",
		"Assistant:",
	}, "\n")

	prompt, err := buildTmuxPrompt(
		[]llmtypes.MessageContent{
			textPartMessage(llmtypes.ChatMessageTypeHuman, userText),
		},
		&llmtypes.CallOptions{},
		"",
		false,
	)
	if err != nil {
		t.Fatalf("buildTmuxPrompt error = %v", err)
	}
	if strings.TrimSuffix(prompt, "\n") != userText {
		t.Fatalf("fresh single-turn prompt did not preserve user text verbatim:\ngot  %q\nwant %q", prompt, userText)
	}
}

func TestBuildTmuxPromptHandlesJSONRehydratedTextParts(t *testing.T) {
	raw := []byte(`{
		"conversation_history": [
			{"Role":"human","Parts":[{"Text":"hi"}]},
			{"Role":"ai","Parts":[{"Text":"hello"}]},
			{"Role":"human","Parts":[{"text":"next"}]}
		]
	}`)
	var saved struct {
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}
	if got := tmuxMessagePartsToText(saved.History[0].Parts); got != "hi" {
		t.Fatalf("tmuxMessagePartsToText saved first human = %q, want hi", got)
	}

	prompt, err := buildTmuxPrompt(saved.History, &llmtypes.CallOptions{}, "", false)
	if err != nil {
		t.Fatalf("buildTmuxPrompt error = %v", err)
	}
	for _, want := range []string{"Previous conversation context:", "User:\nhi", "Assistant:\nhello", "Current user message:\nnext"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want %q", prompt, want)
		}
	}
	if strings.Contains(prompt, "HUMAN:") || strings.Contains(prompt, "AI:") || strings.Contains(prompt, "Conversation:") {
		t.Fatalf("prompt contains old role wrapper: %q", prompt)
	}
	if strings.Contains(prompt, "[unsupported content part") {
		t.Fatalf("prompt contains unsupported marker: %q", prompt)
	}
}

func TestBuildTmuxPromptResumeHandlesJSONRehydratedLatestHuman(t *testing.T) {
	opts := &llmtypes.CallOptions{}
	WithResumeSessionID("7aa21987-0003-4d71-b887-ad73e29d2faf")(opts)

	raw := []byte(`{
		"conversation_history": [
			{"Role":"human","Parts":[{"Text":"old"}]},
			{"Role":"ai","Parts":[{"Text":"previous"}]},
			{"Role":"human","Parts":[{"Text":"hi"}]}
		]
	}`)
	var saved struct {
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	prompt, err := buildTmuxPrompt(saved.History, opts, claudeResumeIDFromOptions(opts), false)
	if err != nil {
		t.Fatalf("buildTmuxPrompt error = %v", err)
	}
	if strings.TrimSpace(prompt) != "hi" {
		t.Fatalf("resume prompt = %q, want only latest rehydrated human text", prompt)
	}
}

func TestIsClaudeResumeCompressionPrompt(t *testing.T) {
	pane := `
This resumed conversation is large.
Would you like to compact the conversation or continue without compacting?
`
	if !isClaudeResumeCompressionPrompt(pane) {
		t.Fatal("isClaudeResumeCompressionPrompt = false, want true")
	}
	got := claudeResumeCompressionPromptSubmitKeys(pane)
	want := []string{"continue", "C-m"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeResumeCompressionPromptSubmitKeys = %#v, want %#v", got, want)
	}
}

// Previously this asserted a bare Enter, accepting Claude Code's own default
// selection -- "Resume from summary (recommended)", which discards full
// conversation detail in favor of a compressed summary. That silently chose
// compression on every resume of a long conversation, the opposite of what a
// caller resuming to preserve full context wants. Fixed to navigate to
// "Resume full session as-is" instead, the same way the sibling
// compact-vs-continue menu already does.
func TestClaudeResumeSummaryMenuNavigatesAwayFromCompressedDefault(t *testing.T) {
	pane := `
────────────────────────────────────────────────────────────────────────────────────────────────── mcp-agent ──

────────────────────────────────────────────────────────────────────────────────────────────────────────────────
  This session is 2h 17m old and 167.9k tokens.

  Resuming the full session will consume a substantial portion of your usage limits. We recommend resuming from a summary.

  ❯ 1. Resume from summary (recommended)
    2. Resume full session as-is
    3. Don't ask me again

  Enter to confirm · Esc to cancel
`
	if !isClaudeResumeSummaryMenu(pane) {
		t.Fatal("isClaudeResumeSummaryMenu = false, want true")
	}
	got := claudeResumeCompressionPromptSubmitKeys(pane)
	want := []string{"Down", "C-m"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeResumeCompressionPromptSubmitKeys = %#v, want %#v -- must navigate to \"Resume full session as-is\", not accept the compressed-summary default", got, want)
	}
}

func TestIsClaudeResumeCompressionPromptIgnoresNormalPrompt(t *testing.T) {
	pane := `
─────────────────────────────────────────────────── mcp-agent ──
❯
`
	if isClaudeResumeCompressionPrompt(pane) {
		t.Fatal("isClaudeResumeCompressionPrompt = true for normal prompt")
	}
	if isClaudeResumeSummaryMenu(pane) {
		t.Fatal("isClaudeResumeSummaryMenu = true for normal prompt")
	}
	if got := claudeResumeCompressionPromptSubmitKeys(pane); got != nil {
		t.Fatalf("claudeResumeCompressionPromptSubmitKeys = %#v, want nil", got)
	}
}

func TestIsClaudeResumeSelectMenuCursorOnCompact(t *testing.T) {
	// ❯ is on the compact option — adapter should navigate down to "run as is".
	pane := `
──────────────────────────────────────────────────────── mcp-agent ──

  Your conversation history is using a substantial portion of the context window.

  ❯ Compact conversation (recommended)
    Continue without compacting

  ↑↓ to navigate · Enter to select
`
	if !isClaudeResumeSelectMenu(pane) {
		t.Fatal("isClaudeResumeSelectMenu = false, want true")
	}
	got := claudeResumeCompressionPromptSubmitKeys(pane)
	want := []string{"Down", "C-m"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeResumeCompressionPromptSubmitKeys = %#v, want %#v", got, want)
	}
}

func TestIsClaudeResumeSelectMenuCursorOnRunAsIs(t *testing.T) {
	// ❯ is already on the "run as is" option — Enter is enough.
	pane := `
──────────────────────────────────────────────────────── mcp-agent ──

  Your conversation history is using a substantial portion of the context window.

    Compact conversation
  ❯ Continue without compacting

  ↑↓ to navigate · Enter to select
`
	if !isClaudeResumeSelectMenu(pane) {
		t.Fatal("isClaudeResumeSelectMenu = false, want true")
	}
	got := claudeResumeCompressionPromptSubmitKeys(pane)
	want := []string{"C-m"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeResumeCompressionPromptSubmitKeys = %#v, want %#v", got, want)
	}
}

func TestIsClaudeResumeSelectMenuRunAsIsVariant(t *testing.T) {
	// "run as is" phrasing instead of "continue without compacting".
	pane := `
──────────────────────────────────────────────────────── mcp-agent ──

  ❯ Compact and summarize
    Run as is

  ↑↓ to navigate · Enter to select
`
	if !isClaudeResumeSelectMenu(pane) {
		t.Fatal("isClaudeResumeSelectMenu = false for run-as-is variant")
	}
	got := claudeResumeCompressionPromptSubmitKeys(pane)
	want := []string{"Down", "C-m"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeResumeCompressionPromptSubmitKeys = %#v, want %#v", got, want)
	}
}

func TestIsClaudeResumeSelectMenuDoesNotMatchNormalPrompt(t *testing.T) {
	pane := `
─────────────────────────────────────────────────── mcp-agent ──
❯
`
	if isClaudeResumeSelectMenu(pane) {
		t.Fatal("isClaudeResumeSelectMenu = true for normal ready prompt")
	}
}

// The cursor may already be on "Resume full session as-is" (e.g. the user
// had manually navigated, or a prior poll already moved it) -- Enter alone
// must be enough, no extra navigation.
func TestClaudeResumeSummaryMenuAcceptsWhenCursorAlreadyOnFullSession(t *testing.T) {
	pane := `
────────────────────────────────────────────────────────────────────────────────────────────────── mcp-agent ──

  This session is 2h 17m old and 167.9k tokens.

  Resuming the full session will consume a substantial portion of your usage limits. We recommend resuming from a summary.

    1. Resume from summary (recommended)
  ❯ 2. Resume full session as-is
    3. Don't ask me again

  Enter to confirm · Esc to cancel
`
	got := claudeResumeCompressionPromptSubmitKeys(pane)
	want := []string{"C-m"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeResumeCompressionPromptSubmitKeys = %#v, want %#v", got, want)
	}
}

func TestIsClaudeResumeSelectMenuDoesNotMatchSummaryMenu(t *testing.T) {
	// The old summary menu has ❯ but no compact/compress option — must not match.
	pane := `
────────────────────────────────────────────────────────────────────────────────────────────────── mcp-agent ──

  This session is 2h 17m old and 167.9k tokens.

  Resuming the full session will consume a substantial portion of your usage limits. We recommend resuming from a summary.

  ❯ 1. Resume from summary (recommended)
    2. Resume full session as-is
    3. Don't ask me again

  Enter to confirm · Esc to cancel
`
	if isClaudeResumeSelectMenu(pane) {
		t.Fatal("isClaudeResumeSelectMenu = true for summary menu (no compact option)")
	}
}

func TestOldTextBasedCompressionPromptStillHandled(t *testing.T) {
	// The older text-based format (no ❯ cursor) must still route to the
	// text-input path, not the new TUI select-menu path.
	pane := `
This resumed conversation is large.
Would you like to compact the conversation or continue without compacting?
`
	if isClaudeResumeSelectMenu(pane) {
		t.Fatal("isClaudeResumeSelectMenu = true for old text-based prompt (no ❯)")
	}
	if !isClaudeResumeCompressionPrompt(pane) {
		t.Fatal("isClaudeResumeCompressionPrompt = false for old text-based prompt")
	}
	got := claudeResumeCompressionPromptSubmitKeys(pane)
	want := []string{"continue", "C-m"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeResumeCompressionPromptSubmitKeys = %#v, want %#v", got, want)
	}
}

// TestResumeCompressionPromptIgnoresScrollbackKeywords guards the false-positive
// that typed a stray "continue" into a normal prompt (garbling e.g. a "1. do it"
// draft into "1. do itcontinue"). A pane whose scrollback merely mentions
// compact/continue/context/conversation — but is NOT the actual resume prompt —
// must not trigger the text-"continue" path.
func TestResumeCompressionPromptIgnoresScrollbackKeywords(t *testing.T) {
	panes := []string{
		// Normal numbered prompt with keyword-bearing scrollback above it.
		`
⏺ I can compact the report or continue with the current context window.
  Here's the conversation so far...

❯ 1. do it
─────────────────────────────────────────────────── mcp-agent ──
  ⏵⏵ don't ask on (shift+tab to cycle)
`,
		// Assistant prose that happens to use all the loose keywords.
		`
⏺ To continue, I'll compact the context and resume the conversation flow.
❯
`,
	}
	for i, pane := range panes {
		if isClaudeResumeCompressionPrompt(pane) {
			t.Fatalf("pane[%d]: isClaudeResumeCompressionPrompt = true for non-prompt scrollback", i)
		}
		if got := claudeResumeCompressionPromptSubmitKeys(pane); got != nil {
			t.Fatalf("pane[%d]: claudeResumeCompressionPromptSubmitKeys = %#v, want nil (no stray continue)", i, got)
		}
	}
}

func TestTmuxTimeoutEnv(t *testing.T) {
	t.Setenv(EnvClaudeTmuxTimeoutSeconds, "")
	if got := tmuxTimeout(); got != 0 {
		t.Fatalf("tmuxTimeout default = %v, want 0", got)
	}

	t.Setenv(EnvClaudeTmuxTimeoutSeconds, "0")
	if got := tmuxTimeout(); got != 0 {
		t.Fatalf("tmuxTimeout zero env = %v, want 0", got)
	}

	t.Setenv(EnvClaudeTmuxTimeoutSeconds, "2")
	if got := tmuxTimeout(); got != 2*time.Second {
		t.Fatalf("tmuxTimeout = %v, want 2s", got)
	}

	t.Setenv(EnvClaudeTmuxTimeoutSeconds, "bad")
	if got := tmuxTimeout(); got != defaultTmuxTimeout {
		t.Fatalf("tmuxTimeout invalid env = %v, want default %v", got, defaultTmuxTimeout)
	}
}

func TestTmuxPromptWaitEnv(t *testing.T) {
	t.Setenv(tmuxlaunch.EnvPromptWaitSeconds, "")
	t.Setenv(EnvClaudeTmuxPromptWaitSeconds, "")
	if got := promptReadyTimeout(); got != 300*time.Second {
		t.Fatalf("promptReadyTimeout default = %v, want 300s", got)
	}

	t.Setenv(tmuxlaunch.EnvPromptWaitSeconds, "3")
	t.Setenv(EnvClaudeTmuxPromptWaitSeconds, "")
	if got := promptReadyTimeout(); got != 3*time.Second {
		t.Fatalf("promptReadyTimeout global env = %v, want 3s", got)
	}

	t.Setenv(EnvClaudeTmuxPromptWaitSeconds, "2")
	if got := promptReadyTimeout(); got != 2*time.Second {
		t.Fatalf("promptReadyTimeout provider env = %v, want 2s", got)
	}

	t.Setenv(EnvClaudeTmuxPromptWaitSeconds, "0")
	if got := promptReadyTimeout(); got != 3*time.Second {
		t.Fatalf("promptReadyTimeout zero provider env = %v, want global 3s", got)
	}

	t.Setenv(EnvClaudeTmuxPromptWaitSeconds, "bad")
	if got := promptReadyTimeout(); got != 3*time.Second {
		t.Fatalf("promptReadyTimeout invalid provider env = %v, want global 3s", got)
	}
}

func TestClaudeCallContextIgnoresParentDeadline(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelParent()

	callCtx, cancelCall := newClaudeCallContext(parent, time.Second)
	defer cancelCall()

	time.Sleep(60 * time.Millisecond)
	select {
	case <-callCtx.Done():
		t.Fatalf("call context ended from parent deadline: %v", callCtx.Err())
	default:
	}
}

func TestClaudeCallContextHonorsExplicitParentCancel(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	callCtx, cancelCall := newClaudeCallContext(parent, time.Second)
	defer cancelCall()

	cancelParent()
	select {
	case <-callCtx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("call context did not end after explicit parent cancel")
	}
}

func TestTmuxDoesNotAddVerboseFlagByDefault(t *testing.T) {
	adapter := NewClaudeCodeInteractiveAdapter("claude-code", &MockLogger{})
	args, tempFiles, err := adapter.buildClaudeArgs(&llmtypes.CallOptions{}, "", "7aa21987-0003-4d71-b887-ad73e29d2faf", "")
	if err != nil {
		t.Fatalf("buildClaudeArgs error = %v", err)
	}
	if len(tempFiles) != 0 {
		t.Fatalf("tempFiles = %v, want none", tempFiles)
	}
	if containsArg(args, "--verbose") {
		t.Fatalf("args = %v, want no --verbose by default", args)
	}
}

func TestHasReadyInputPromptRejectsRunningStatus(t *testing.T) {
	pane := `
⏺ Calling api-bridge…
✶ Precipitating… (1s · ↓ 2 tokens)
──────────────────────────────────────────────────
❯
──────────────────────────────────────────────────
  ⏵⏵ don't ask on (shift+tab to cycle) · esc to interrupt
`
	if hasReadyInputPrompt(pane) {
		t.Fatal("hasReadyInputPrompt = true while Claude is still running")
	}
}

func TestHasReadyInputPromptAcceptsPromptWithDraftText(t *testing.T) {
	pane := `
⏺ Done
─────────────────────────────────────────────────── mcp-agent ──
❯ run the workflow
────────────────────────────────────────────────────────────────
  ⏵⏵ don't ask on (shift+tab to cycle)
`
	if !hasReadyInputPrompt(pane) {
		t.Fatal("hasReadyInputPrompt = false for idle prompt with draft text")
	}
	if hasReadyEmptyInputPrompt(pane) {
		t.Fatal("hasReadyEmptyInputPrompt = true for idle prompt with unsubmitted draft text")
	}
}

func TestHasReadyInputPromptAcceptsIdlePromptWithEscFooter(t *testing.T) {
	pane := `
⏺ Done
─────────────────────────────────────────────────── mcp-agent ──
❯
────────────────────────────────────────────────────────────────
  ⏵⏵ don't ask on (shift+tab to cycle) · esc to interrupt
`
	if !hasReadyInputPrompt(pane) {
		t.Fatal("hasReadyInputPrompt = false for idle prompt with esc footer")
	}
}

// TestHasReadyInputPromptAcceptsIdlePromptWithRemoteControlFooter locks in
// Claude Code 2.1.251's Remote Control marker. It appears below the ordinary
// composer as a right-aligned "/rc" line, which must not be treated as a
// typed command or prevent the adapter from finding the ready ❯ prompt.
func TestHasReadyInputPromptAcceptsIdlePromptWithRemoteControlFooter(t *testing.T) {
	pane := `
─────────────────────────────────────────────────── mcp-agent ──
❯
────────────────────────────────────────────────────────────────
                                                                                                             /rc
  ⏵⏵ don't ask on (shift+tab to cycle) · PR #187 · ← for agents
`
	if !hasReadyInputPrompt(pane) {
		t.Fatal("hasReadyInputPrompt = false when CLI shows the Remote Control footer")
	}
}

func TestHasReadyInputPromptAcceptsRemoteControlContextPill(t *testing.T) {
	pane := `
 ▐▛███▜▌   Claude Code v2.1.251
 Sonnet 5 with high effort · Claude Pro

────────────────────────────────────────────── mcp-agent ──
❯
────────────────────────────────────────────────────────────────
  │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │ │   In go.mod · /rc
  ⏵⏵ don't ask on (shift+tab to cycle) · PR #187 · ← for agents
`
	if !hasReadyInputPrompt(pane) {
		t.Fatal("hasReadyInputPrompt = false when Claude shows the Remote Control context pill")
	}
}

func TestSaveClaudeTmuxPromptDiagnostic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvClaudeTmuxDiagnosticsDir, dir)

	note := saveClaudeTmuxPromptDiagnostic("mlp-claude-code:test", "❯ hello\n")
	if !strings.Contains(note, "full tmux pane saved to") {
		t.Fatalf("diagnostic note = %q", note)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("diagnostic entries = %v, err = %v; want one file", entries, err)
	}
	content, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read diagnostic: %v", err)
	}
	if string(content) != "❯ hello\n" {
		t.Fatalf("diagnostic content = %q", content)
	}
}

// TestHasReadyInputPromptAcceptsIdlePromptWithUpgradeNotice locks in
// a fix for a real production hang. Claude Code CLI shows an upgrade
// notice line at the very bottom of the TUI when a newer release is
// available ("current: 2.1.149 · latest: 2.1.150"). Before the fix,
// hasReadyInputPrompt walked up from the last line, hit this line
// BEFORE finding ❯, and returned false — so every chat turn hung
// indefinitely (terminal view showed claude was done, but mcpagent's
// wait loop never saw the agent as ready and never returned). The
// fix recognizes the "current:/latest:" pair as an ignorable footer.
func TestHasReadyInputPromptAcceptsIdlePromptWithUpgradeNotice(t *testing.T) {
	pane := `
⏺ Hi! How can I help you today?

✻ Baked for 2s

─────────────────────────────────────────────────── mcp-agent ──
❯
────────────────────────────────────────────────────────────────
  ⏵⏵ don't ask on (shift+tab to cycle) · ← for agents            14048 tokens
                                                                                              current: 2.1.149 · latest: 2.1.150
`
	if !hasReadyInputPrompt(pane) {
		t.Fatal("hasReadyInputPrompt = false when CLI shows upgrade notice — wait loop will hang indefinitely")
	}
}

// TestHasReadyInputPromptAcceptsIdlePromptWithAutoUpdateFailureNotice pins
// a fix for a real production hang, the same failure shape as the upgrade-
// notice case above: confirmed live on the Dominion Hetzner deployment
// 2026-08-30, where an unwritable npm prefix (fixed separately — see
// deploy/dedicated-vm/dominion-hetzner.md) made Claude Code print a
// "✘ Auto-update failed: ..." footer at the very bottom of the pane, below
// the actual idle ❯ prompt. hasReadyInputPrompt walked up from the last
// line, hit this footer BEFORE finding ❯, and returned false — the CLI
// itself answered in 8 seconds, but the wait loop stayed wedged for over 3
// hours until manually canceled, since it has no default timeout.
func TestHasReadyInputPromptAcceptsIdlePromptWithAutoUpdateFailureNotice(t *testing.T) {
	pane := `
⏺ Sorry for the jargon — let me put that simply.

✻ Churned for 7s

──────────────────────────────────────────── mcp-agent-20260828-200914 ──
❯
────────────────────────────────────────────────────────────────────────
  ⏵⏵ don't ask on (shift+tab to cycle) · ← for agents
                                           ✘ Auto-update failed: no write permission to npm prefix · Run claude doctor
`
	if !hasReadyInputPrompt(pane) {
		t.Fatal("hasReadyInputPrompt = false when CLI shows the auto-update-failure notice — wait loop will hang indefinitely")
	}
}

func TestHasReadyInputPromptAcceptsClaudeSuggestionWithTrailingBlankFill(t *testing.T) {
	pane := `
 ▐▛███▜▌   Claude Code v2.1.141
▝▜█████▛▘  Sonnet 4.6 with high effort · Claude Max

─────────────────────────────────────────────────── mcp-agent ──
❯ Try "how do I log an error?"
────────────────────────────────────────────────────────────────
  ⏵⏵ don't ask on (shift+tab to cycle)

`
	if !hasReadyInputPrompt(pane) {
		t.Fatal("hasReadyInputPrompt = false for idle prompt with Claude suggestion text and trailing blank fill")
	}
	if !hasReadyEmptyInputPrompt(pane) {
		t.Fatal("hasReadyEmptyInputPrompt = false for Claude suggestion placeholder")
	}
}

func TestWaitForTmuxPromptAcceptsNormalPromptWithoutResumePrompt(t *testing.T) {
	fakeBin := t.TempDir()
	capturePath := fakeBin + "/capture.txt"
	sendKeysPath := fakeBin + "/send-keys.log"
	tmuxPath := fakeBin + "/tmux"
	script := `#!/bin/sh
if [ "$1" = "capture-pane" ]; then
  cat "$TMUX_TEST_CAPTURE"
  exit 0
fi
if [ "$1" = "send-keys" ]; then
  printf '%s\n' "$*" >> "$TMUX_TEST_SEND_KEYS"
  exit 0
fi
exit 0
`
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	pane := `
 ▐▛███▜▌   Claude Code v2.1.144
▝▜█████▛▘  Opus 4.6 with high effort · Claude Max
  ▘▘ ▝▝    ~/ai-work/mcp-agent-builder-go/workspace-docs/Workflow/instagram

─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────── mcp-agent-20260519-220956 ──
❯ Try "write a test for Perform Sheet Update Verification_code.go"
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ don't ask on (shift+tab to cycle) · ← for agents                                                                                                 0 tokens
`
	if err := os.WriteFile(capturePath, []byte(pane), 0o644); err != nil {
		t.Fatalf("write capture: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_TEST_CAPTURE", capturePath)
	t.Setenv("TMUX_TEST_SEND_KEYS", sendKeysPath)

	stream := make(chan llmtypes.StreamChunk, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := waitForTmuxPrompt(ctx, "test-session", stream, true); err != nil {
		t.Fatalf("waitForTmuxPrompt returned error for normal ready prompt: %v", err)
	}
	if got, err := os.ReadFile(sendKeysPath); err == nil && strings.TrimSpace(string(got)) != "" {
		t.Fatalf("waitForTmuxPrompt sent resume keys for normal prompt: %q", string(got))
	}
	select {
	case chunk := <-stream:
		if chunk.Type != llmtypes.StreamChunkTypeTerminal {
			t.Fatalf("streamed chunk type = %q, want terminal", chunk.Type)
		}
		if chunk.Content == "" {
			t.Fatal("streamed terminal content is empty")
		}
		if chunk.Metadata["tmux_session"] != "test-session" {
			t.Fatalf("tmux_session metadata = %#v, want test-session", chunk.Metadata["tmux_session"])
		}
	default:
		t.Fatal("waitForTmuxPrompt did not stream the startup pane")
	}
}

func TestClaudeTrustFolderPromptAcceptKeysSelectsAffirmativeText(t *testing.T) {
	tests := []struct {
		name string
		pane string
		want []string
	}{
		{
			name: "current arrow menu starts on decline",
			pane: `
Quick safety check: Is this a project you created or one you trust?

❯ No, exit
  Yes, I trust this folder

Enter to confirm · Esc to cancel`,
			want: []string{"Down"},
		},
		{
			name: "arrow menu confirms only after affirmative row is selected",
			pane: `
Quick safety check: Is this a project you created or one you trust?

  No, exit
❯ Yes, I trust this folder

Enter to confirm · Esc to cancel`,
			want: []string{"Enter"},
		},
		{
			name: "numbered variant does not rely on number key",
			pane: `
Quick safety check: Is this a project you created or one you trust?

❯ 1. No, exit
  2. Yes, I trust this folder

Enter to confirm`,
			want: []string{"Down"},
		},
		{
			name: "punctuation and folder wording can vary",
			pane: `
Quick safety check
❯ No, exit
  Yes - I trust this project
Enter to confirm`,
			want: []string{"Down"},
		},
		{
			name: "unrelated pane is ignored",
			pane: `
❯ Write a test for the payment form
  ⏵⏵ don't ask on`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeTrustFolderPromptAcceptKeys(tt.pane); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("claudeTrustFolderPromptAcceptKeys() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestHasReadyInputPromptAcceptsCompletedStatusAfterBackgroundAgents(t *testing.T) {
	pane := `
⏺ Both agents running in parallel now:

  ┌────────────────────────────────────────┬───────────────────────────────────────────────────────────┬────────────┐
  │                 Agent                  │                           Task                            │   Status   │
  ├────────────────────────────────────────┼───────────────────────────────────────────────────────────┼────────────┤
  │ bg-sentry-login-&-token-creation-79000 │ Browser login → create token in Sentry UI                 │ 🔄 Running │
  ├────────────────────────────────────────┼───────────────────────────────────────────────────────────┼────────────┤
  │ bg-github-.env-file-search-19000       │ Search repos for .env / Sentry vars                       │ 🔄 Running │
  └────────────────────────────────────────┴───────────────────────────────────────────────────────────┴────────────┘

  Will wait for both notifications.

✻ Baked for 27s

─────────────────────────────────────────────────────────────────── mcp-agent-20260517-143605 ──
❯ wait for the results
────────────────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ don't ask on (shift+tab to cycle)
`
	if !hasReadyInputPrompt(pane) {
		t.Fatal("hasReadyInputPrompt = false for idle prompt after Claude-created background agents")
	}
}

// TestHasReadyInputPromptAcceptsIdlePromptBelowFinishedToolCallSummary
// reproduces a live production stall: a session's resumed pane always
// redraws with a completed, collapsed tool-call summary
// ("Called ... (ctrl+o to expand)") sitting just above an otherwise
// genuinely idle empty prompt. hasReadyInputPrompt previously matched that
// past-tense "Called" line via isClaudeToolProgressLine and treated it as
// still-running, so the prompt was never judged ready, waitForTmuxPrompt hit
// its 5-minute inactivity timeout, and the tmux session self-discarded --
// deterministically, since a relaunch redraws the identical pane tail.
func TestHasReadyInputPromptAcceptsIdlePromptBelowFinishedToolCallSummary(t *testing.T) {
	pane := `
✻ Crunched for 1h 40m 28s

❯ yes check

  Called api-bridge 7 times (ctrl+o to expand)

⏺ Let me actually render this template with real data and look at it directly, rather
  than guess from CSS.

  Called api-bridge 2 times (ctrl+o to expand)

─────────────────────────────────────────────────── mcp-agent-20260822-173432 ─
❯
────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ don't ask on (shift+tab to cycle) · ← for agents
`
	if !hasReadyInputPrompt(pane) {
		t.Fatal("hasReadyInputPrompt = false for an idle prompt sitting below a finished, collapsed tool-call summary")
	}
}

// TestIsClaudeToolInProgressLineDistinguishesLiveFromFinishedCalls guards the
// exact discriminator the fix above depends on: "Calling" is a live,
// still-in-flight call and must gate readiness; "Called ... (ctrl+o to
// expand)" is already-finished chrome and must never gate it, regardless of
// how many times the pane redraws the same line.
func TestIsClaudeToolInProgressLineDistinguishesLiveFromFinishedCalls(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"Calling api-bridge…", true},
		{"Called api-bridge 2 times (ctrl+o to expand)", false},
		{"Called api-bridge (ctrl+o to expand)", false},
		{"Called api-bridge", false},
	}
	for _, tc := range cases {
		if got := isClaudeToolInProgressLine(tc.line); got != tc.want {
			t.Errorf("isClaudeToolInProgressLine(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestHasClaudeActivityIgnoresCompletedAssistantOutput(t *testing.T) {
	pane := `
⏺ Final answer

✻ Worked for 1s · ↓ 2 tokens
──────────────────────────────────────────────────
❯
`
	if hasClaudeActivity(pane) {
		t.Fatal("hasClaudeActivity = true for completed assistant output")
	}
}

func TestClaudeCompletedWorkStatusIsNotRunningProgress(t *testing.T) {
	completed := []string{
		"✻ Baked for 27s",
		"✻ Cogitated for 1m 2s",
		"✻ Worked for 1s · ↓ 2 tokens",
		"✶ Sautéed for 8s",
	}
	for _, line := range completed {
		if isClaudeRunningProgressLine(line) {
			t.Fatalf("isClaudeRunningProgressLine(%q) = true, want false for completed status", line)
		}
		if cleaned := cleanClaudeTerminalProgressLine(line); cleaned != "" {
			t.Fatalf("cleanClaudeTerminalProgressLine(%q) = %q, want empty completed status", line, cleaned)
		}
	}
}

func TestHasClaudeActivityDetectsRunningStatus(t *testing.T) {
	pane := `
✶ Precipitating… (1s · ↓ 2 tokens) · esc to interrupt
`
	if !hasClaudeActivity(pane) {
		t.Fatal("hasClaudeActivity = false for running Claude status")
	}
}

func TestHasClaudeActivityDetectsRenamedRunningStatus(t *testing.T) {
	pane := `
✢ Flibbertigibbeting… (10m 46s · ↑ 19.1k tokens · thought for 3s)
  ⎿  Tip: Use /btw to ask a quick side question without interrupting Claude's current work
`
	if !hasClaudeActivity(pane) {
		t.Fatal("hasClaudeActivity = false for renamed Claude running status")
	}
}

func TestClaudeRunningPaneWithLiveInputIsNotReady(t *testing.T) {
	pane := `
⏺ Calling api-bridge…
✶ Precipitating… (1s · ↓ 2 tokens) · esc to interrupt
──────────────────────────────────────────────────
❯ ## Pre-validation failed (retry attempt 3)
──────────────────────────────────────────────────
  ⏵⏵ don't ask on (shift+tab to cycle) · esc to interrupt
`
	if !hasClaudeActivity(pane) {
		t.Fatal("running pane with live input should count as active")
	}
	if hasReadyInputPrompt(pane) {
		t.Fatal("running pane with live input must not be treated as ready")
	}
}

func TestClaudeCompletedPaneWithStaleToolCallsIsNotActive(t *testing.T) {
	pane := `
⏺ Calling api-bridge 4 times… (ctrl+o to expand)

⏺ All 29 validation checks pass. Here's a summary of what was produced:

  STATUS: COMPLETED

✻ Sautéed for 7m 10s

────────────────────────────────────────────────────────────────────────────────
❯ run the next step in the workflow
────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ don't ask on (shift+tab to cycle)
`
	if hasClaudeActivity(pane) {
		t.Fatal("completed pane with stale tool-call text above the prompt should not count as active")
	}
}

func TestParseTmuxMajorVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    int
		wantOK  bool
	}{
		{name: "modern", version: "tmux 3.6a", want: 3, wantOK: true},
		{name: "old", version: "tmux 2.9", want: 2, wantOK: true},
		{name: "two digits", version: "tmux 10.1", want: 10, wantOK: true},
		{name: "invalid", version: "not tmux", wantOK: false},
		{name: "empty", version: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseTmuxMajorVersion(tt.version)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("parseTmuxMajorVersion(%q) = (%d, %t), want (%d, %t)", tt.version, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestIsContextCanceledErrorHandlesNil(t *testing.T) {
	if isContextCanceledError(nil) {
		t.Fatalf("nil error should not be treated as context canceled")
	}
	if !isContextCanceledError(context.Canceled) {
		t.Fatalf("context.Canceled should be detected")
	}
}

func textPartMessage(role llmtypes.ChatMessageType, text string) llmtypes.MessageContent {
	return llmtypes.MessageContent{
		Role:  role,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}},
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func containsArgPair(args []string, key, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

func argValue(args []string, key string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key {
			return args[i+1]
		}
	}
	return ""
}

func TestIsClaudeDismissableFeaturePrompt(t *testing.T) {
	chrome := `  Claude in Chrome extension detected
  ❯ 1. Yes, use my browser
    2. No, keep browser tools off
  Enter to confirm · Esc to keep browser tools off`
	fullscreen := `  Try the new fullscreen renderer?
  ❯ 1. Yes, try it
    2. Not now`
	trust := "Is this a project you trust?\n❯ 1. Yes, I trust this folder\n  2. No, exit"
	if !isClaudeDismissableFeaturePrompt(chrome) {
		t.Fatal("chrome extension modal should be dismissable")
	}
	if !isClaudeDismissableFeaturePrompt(fullscreen) {
		t.Fatal("fullscreen renderer modal should be dismissable")
	}
	if isClaudeDismissableFeaturePrompt(trust) {
		t.Fatal("trust-folder security prompt must NOT be auto-dismissed")
	}
	if isClaudeDismissableFeaturePrompt("> ready prompt, no modal") {
		t.Fatal("false positive on a normal pane")
	}
}

func TestIsClaudeBlockingChoiceMenu(t *testing.T) {
	prompt := `This session is 2h 2m old and 203.9k tokens.

  Resuming the full session will consume a substantial portion of your usage limits. We recommend resuming
  from a summary.

  ❯ 1. Resume from summary (recommended)
    2. Resume full session as-is
    3. Don't ask me again

  Enter to confirm · Esc to cancel`
	if !isClaudeBlockingChoiceMenu(prompt) {
		t.Fatal("expected the resume choice menu to be detected by the generic catch-all")
	}
	// Must NOT be handled as a feature prompt — that would Esc-cancel the resume.
	if isClaudeDismissableFeaturePrompt(prompt) {
		t.Fatal("resume prompt must not be Esc-dismissed as a feature prompt")
	}
	if isClaudeBlockingChoiceMenu("> ready prompt, no menu") {
		t.Fatal("false positive on a normal pane")
	}
}

func TestIsClaudeThemeSelectionPrompt(t *testing.T) {
	theme := `Welcome to Claude Code v2.1.233

Choose the text style that looks best with your terminal
To change this later, run /theme

  1. Auto (match terminal)
❯ 2. Dark mode
  3. Light mode`
	if !isClaudeThemeSelectionPrompt(theme) {
		t.Fatal("Claude Code theme picker should be detected")
	}
	if isClaudeThemeSelectionPrompt("❯ ready prompt") {
		t.Fatal("normal input prompt must not be treated as a theme picker")
	}
}

func TestPrepareClaudeUserConfigCompletesOnboardingOnlyForExplicitToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := t.TempDir()

	prepareClaudeUserConfig(workDir, "")
	configPath := filepath.Join(home, ".claude.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after trust setup: %v", err)
	}
	var withoutToken map[string]interface{}
	if err := json.Unmarshal(raw, &withoutToken); err != nil {
		t.Fatalf("parse config without token: %v", err)
	}
	if _, found := withoutToken["hasCompletedOnboarding"]; found {
		t.Fatal("saved-login session must retain Claude's normal onboarding flow")
	}

	prepareClaudeUserConfig(workDir, "setup-token")
	raw, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after token setup: %v", err)
	}
	var withToken map[string]interface{}
	if err := json.Unmarshal(raw, &withToken); err != nil {
		t.Fatalf("parse config with token: %v", err)
	}
	if completed, _ := withToken["hasCompletedOnboarding"].(bool); !completed {
		t.Fatalf("hasCompletedOnboarding = %#v, want true", withToken["hasCompletedOnboarding"])
	}
}

func TestClaudeRetainedInputRefreshInvalidatesPriorIdleExpiry(t *testing.T) {
	session := &claudeInteractivePersistentSession{
		ownerSessionID:  t.Name(),
		tmuxSessionName: "mlp-claude-code-retained-idle-test",
	}
	previous := claudeInteractivePersistentRegistry.Replace(map[string]*claudeInteractivePersistentSession{
		session.ownerSessionID: session,
	})
	t.Cleanup(func() {
		session.idleLease.Stop()
		claudeInteractivePersistentRegistry.Replace(previous)
	})

	if !armClaudePersistentIdleLease(session, nil) {
		t.Fatal("initial idle timer was not armed")
	}
	firstUsed := session.idleLease.LastActivity()

	time.Sleep(time.Millisecond)
	if !armClaudePersistentIdleLease(session, nil) {
		t.Fatal("retained input did not refresh the idle timer")
	}

	if current, ok := claudeInteractivePersistentRegistry.Get(session.ownerSessionID); !ok || current != session {
		t.Fatal("refresh removed the retained session")
	}
	if current := session.idleLease.LastActivity(); !current.After(firstUsed) {
		t.Fatalf("lease activity was not refreshed: first=%s current=%s", firstUsed, current)
	}
}

func TestWithoutClaudeParentSessionEnv(t *testing.T) {
	got := withoutClaudeParentSessionEnv([]string{"PATH=/bin", "CLAUDE_CODE_CHILD_SESSION=1", "CLAUDECODE=1", "HOME=/h", "CLAUDE_CODE_MESSAGING_SOCKET=/s"})
	if !reflect.DeepEqual(got, []string{"PATH=/bin", "HOME=/h"}) {
		t.Fatalf("parent Claude session env not stripped: %v", got)
	}
}
