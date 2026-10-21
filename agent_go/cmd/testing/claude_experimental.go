package testing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const minClaudeExperimentalRuntimeMajorVersion = 3

var claudeExperimentalTestCmd = &cobra.Command{
	Use:   "claude-experimental",
	Short: "Smoke test Claude Code experimental mode with real mcpbridge",
	Long: `Tests Claude Code through the normal mcpagent provider path.

This launches the real mcpbridge stdio binary, points it at a mock HTTP tool
endpoint, calls that MCP tool through Claude Code experimental mode, and records
whether the provider emits streaming tool chunks or only final content.`,
	RunE: runClaudeExperimentalTest,
}

func init() {
	claudeExperimentalTestCmd.Flags().Bool("require-tool-stream", false, "fail unless streaming tool start/end chunks are observed")
	claudeExperimentalTestCmd.Flags().Bool("extended", false, "run multi-turn, multi-session, large workspace prompt, and resume smoke checks")
	_ = viper.BindPFlag("claude-experimental.require-tool-stream", claudeExperimentalTestCmd.Flags().Lookup("require-tool-stream"))
	_ = viper.BindPFlag("claude-experimental.extended", claudeExperimentalTestCmd.Flags().Lookup("extended"))
}

func runClaudeExperimentalTest(cmd *cobra.Command, args []string) error {
	logFile := viper.GetString("log-file")
	if logFile == "" {
		logFile = filepath.Join(os.TempDir(), fmt.Sprintf("claude-experimental-%d.log", time.Now().UnixNano()))
	}
	logLevel := viper.GetString("log-level")
	if logLevel == "" {
		logLevel = "debug"
	}
	InitTestLogger(logFile, logLevel)
	logger := GetTestLogger()

	logger.Info("=== Claude Code experimental + real mcpbridge smoke test ===")
	logger.Info(fmt.Sprintf("Debug log file: %s", logFile))

	if err := preflightClaudeExperimental(); err != nil {
		return err
	}

	bridgePath, cleanupBridge, err := findOrBuildMCPBridge()
	if err != nil {
		return err
	}
	defer cleanupBridge()
	logger.Info(fmt.Sprintf("Using mcpbridge: %s", bridgePath))

	mockServer, err := startClaudeExperimentalBridgeMockServer()
	if err != nil {
		return err
	}
	defer mockServer.close()
	logger.Info(fmt.Sprintf("Mock bridge HTTP endpoint: %s", mockServer.url))

	modelID := viper.GetString("test.model")
	if modelID == "" {
		modelID = "claude-code"
	}

	llmInstance, err := llm.InitializeLLM(llm.Config{
		Provider:    llm.ProviderClaudeCode,
		ModelID:     modelID,
		Temperature: 0,
		Logger:      logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize Claude Code LLM: %w", err)
	}

	timeout, err := parseClaudeExperimentalTestingTimeout()
	if err != nil {
		return err
	}

	if err := verifyClaudeExperimentalMode(llmInstance, timeout); err != nil {
		return err
	}
	logger.Info("Verified Claude Code provider returned experimental mode metadata")

	if err := verifyClaudeExperimentalNativeResume(llmInstance, timeout); err != nil {
		return err
	}
	logger.Info("Verified Claude Code experimental native resume metadata and continuity")

	result, err := verifyClaudeExperimentalRealMCPBridgeWithRetry(llmInstance, bridgePath, mockServer, timeout)
	if err != nil {
		return err
	}

	logger.Info(fmt.Sprintf("Streaming chunks: content=%d tool_start=%d tool_end=%d",
		result.contentChunks, result.toolStartChunks, result.toolEndChunks))
	if result.toolStartChunks == 0 || result.toolEndChunks == 0 {
		msg := "Claude experimental mode completed the real mcpbridge tool call, but did not emit structured streaming tool chunks"
		if viper.GetBool("claude-experimental.require-tool-stream") {
			return fmt.Errorf("%s", msg)
		}
		logger.Warn(msg)
		fmt.Println(msg)
	} else {
		fmt.Println("Claude experimental mode emitted streaming tool chunks for the real mcpbridge call")
	}

	if viper.GetBool("claude-experimental.extended") {
		if err := verifyClaudeExperimentalExtendedConversations(llmInstance, timeout); err != nil {
			return err
		}
		logger.Info("Verified Claude Code experimental extended multi-turn, multi-session, large-input resume flow")
	}

	logger.Info("✅ Claude Code experimental + real mcpbridge smoke test passed")
	fmt.Println("Claude Code experimental + real mcpbridge smoke test passed")
	return nil
}

func preflightClaudeExperimental() error {
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("claude CLI not found in PATH; install and authenticate Claude Code first")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("Claude Code experimental runtime dependency not found; install tmux %d.x or newer", minClaudeExperimentalRuntimeMajorVersion)
	}

	out, err := exec.Command("tmux", "-V").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to check tmux version: %w: %s", err, strings.TrimSpace(string(out)))
	}
	major, ok := parseTmuxMajorFromVersion(string(out))
	if !ok {
		return fmt.Errorf("failed to parse tmux version from %q", strings.TrimSpace(string(out)))
	}
	if major < minClaudeExperimentalRuntimeMajorVersion {
		return fmt.Errorf("Claude Code experimental runtime dependency is too old (%s); install tmux %d.x or newer", strings.TrimSpace(string(out)), minClaudeExperimentalRuntimeMajorVersion)
	}
	return nil
}

func parseClaudeExperimentalTestingTimeout() (time.Duration, error) {
	raw := strings.TrimSpace(viper.GetString("test.timeout"))
	if raw == "" {
		return 5 * time.Minute, nil
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return 0, fmt.Errorf("invalid --timeout value %q", raw)
	}
	return timeout, nil
}

func verifyClaudeExperimentalMode(llmInstance llmtypes.Model, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	expected := "mode check passed"
	resp, err := llmInstance.GenerateContent(ctx, []llmtypes.MessageContent{
		textMessage(llmtypes.ChatMessageTypeHuman, "Reply with this short phrase: "+expected+"."),
	})
	if err != nil {
		return fmt.Errorf("experimental mode generation failed: %w", err)
	}
	content, additional, err := claudeExperimentalChoice(resp)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(content), expected) {
		return fmt.Errorf("experimental mode response %q did not include expected phrase %q", content, expected)
	}
	if additional["claude_code_mode"] != "experimental" {
		return fmt.Errorf("expected claude_code_mode=experimental, got %#v", additional["claude_code_mode"])
	}
	if additional["claude_code_uses_print_flag"] != false {
		return fmt.Errorf("expected claude_code_uses_print_flag=false, got %#v", additional["claude_code_uses_print_flag"])
	}
	return nil
}

func verifyClaudeExperimentalNativeResume(llmInstance llmtypes.Model, timeout time.Duration) error {
	codeword := "green lantern"

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	first, err := llmInstance.GenerateContent(ctx, []llmtypes.MessageContent{
		textMessage(llmtypes.ChatMessageTypeHuman, "For this chat session only, set scratch variable TEST_PHRASE to this exact value: "+codeword+". Reply exactly: saved."),
	})
	if err != nil {
		return fmt.Errorf("failed to establish Claude experimental resume session: %w", err)
	}

	firstContent, firstAdditional, err := claudeExperimentalChoice(first)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(firstContent), "saved") {
		return fmt.Errorf("resume setup response %q did not include saved", firstContent)
	}
	sessionID, _ := firstAdditional["claude_code_session_id"].(string)
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("resume setup response did not include claude_code_session_id: %#v", firstAdditional)
	}
	GetTestLogger().Info(fmt.Sprintf("Claude experimental resume setup metadata: session_id=%#v native_session_id=%#v close_resume_ref=%#v",
		firstAdditional["claude_code_session_id"],
		firstAdditional["claude_code_native_session_id"],
		firstAdditional["claude_code_close_resume_ref"]))

	second, err := llmInstance.GenerateContent(
		ctx,
		[]llmtypes.MessageContent{
			textMessage(llmtypes.ChatMessageTypeHuman, "OLD_CONTEXT_SHOULD_NOT_BE_SENT"),
			textMessage(llmtypes.ChatMessageTypeAI, "OLD_ASSISTANT_SHOULD_NOT_BE_SENT"),
			textMessage(llmtypes.ChatMessageTypeHuman, "What is the exact value of scratch variable TEST_PHRASE?"),
		},
		llm.WithResumeSessionID(sessionID),
	)
	if err != nil {
		return fmt.Errorf("failed to resume Claude experimental session %s: %w", sessionID, err)
	}

	secondContent, secondAdditional, err := claudeExperimentalChoice(second)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(secondContent), codeword) {
		return fmt.Errorf("resumed response %q did not include codeword %q", secondContent, codeword)
	}
	if resumedID, _ := secondAdditional["claude_code_resumed_session_id"].(string); resumedID != sessionID {
		return fmt.Errorf("expected claude_code_resumed_session_id=%s, got %#v", sessionID, secondAdditional["claude_code_resumed_session_id"])
	}
	if nextSessionID, _ := secondAdditional["claude_code_session_id"].(string); strings.TrimSpace(nextSessionID) == "" {
		return fmt.Errorf("resumed response did not include next claude_code_session_id: %#v", secondAdditional)
	}
	return nil
}

func verifyClaudeExperimentalExtendedConversations(llmInstance llmtypes.Model, timeout time.Duration) error {
	logger := GetTestLogger()
	workspaceDigest := buildClaudeExperimentalWorkspaceDigest()
	keyA := "session-a-" + strings.ToLower(randomTestHex(4))
	keyB := "session-b-" + strings.ToLower(randomTestHex(4))

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	logger.Info(fmt.Sprintf("Extended smoke workspace digest size: %d bytes", len(workspaceDigest)))
	firstA, err := llmInstance.GenerateContent(ctx, []llmtypes.MessageContent{
		textMessage(llmtypes.ChatMessageTypeSystem, "You are running an end-to-end smoke test. Keep answers short and preserve exact test keys."),
		textMessage(llmtypes.ChatMessageTypeHuman, fmt.Sprintf(`This is session A.
The session A token is: %s

Large workspace digest:
%s

Reply exactly: A saved %s`, keyA, workspaceDigest, keyA)),
	})
	if err != nil {
		return fmt.Errorf("extended session A initial turn failed: %w", err)
	}
	firstAContent, firstAAdditional, err := claudeExperimentalChoice(firstA)
	if err != nil {
		return err
	}
	if !containsFold(firstAContent, keyA) {
		return fmt.Errorf("extended session A initial response %q did not include key %q", firstAContent, keyA)
	}
	sessionA, _ := firstAAdditional["claude_code_session_id"].(string)
	if strings.TrimSpace(sessionA) == "" {
		return fmt.Errorf("extended session A did not return claude_code_session_id: %#v", firstAAdditional)
	}

	secondA, err := llmInstance.GenerateContent(
		ctx,
		[]llmtypes.MessageContent{
			textMessage(llmtypes.ChatMessageTypeHuman, "What session A token appeared in your previous answer? Reply with only the token."),
		},
		llm.WithResumeSessionID(sessionA),
	)
	if err != nil {
		return fmt.Errorf("extended session A resume turn failed: %w", err)
	}
	secondAContent, secondAAdditional, err := claudeExperimentalChoice(secondA)
	if err != nil {
		return err
	}
	if !containsFold(secondAContent, keyA) {
		return fmt.Errorf("extended session A resumed response %q did not include key %q", secondAContent, keyA)
	}
	if resumedID, _ := secondAAdditional["claude_code_resumed_session_id"].(string); resumedID != sessionA {
		return fmt.Errorf("extended session A expected resumed id %s, got %#v", sessionA, secondAAdditional["claude_code_resumed_session_id"])
	}

	firstB, err := llmInstance.GenerateContent(ctx, []llmtypes.MessageContent{
		textMessage(llmtypes.ChatMessageTypeHuman, fmt.Sprintf(`This is a separate session B.
The session B token is: %s
Do not mention any key from any other session.
Reply exactly: B saved %s`, keyB, keyB)),
	})
	if err != nil {
		return fmt.Errorf("extended session B initial turn failed: %w", err)
	}
	firstBContent, firstBAdditional, err := claudeExperimentalChoice(firstB)
	if err != nil {
		return err
	}
	if !containsFold(firstBContent, keyB) {
		return fmt.Errorf("extended session B initial response %q did not include key %q", firstBContent, keyB)
	}
	if containsFold(firstBContent, keyA) {
		return fmt.Errorf("extended session B leaked session A key in response %q", firstBContent)
	}
	sessionB, _ := firstBAdditional["claude_code_session_id"].(string)
	if strings.TrimSpace(sessionB) == "" {
		return fmt.Errorf("extended session B did not return claude_code_session_id: %#v", firstBAdditional)
	}

	thirdA, err := llmInstance.GenerateContent(
		ctx,
		[]llmtypes.MessageContent{
			textMessage(llmtypes.ChatMessageTypeHuman, "We are back in session A. Reply with only the exact session A key."),
		},
		llm.WithResumeSessionID(sessionA),
	)
	if err != nil {
		return fmt.Errorf("extended session A second resume turn failed: %w", err)
	}
	thirdAContent, _, err := claudeExperimentalChoice(thirdA)
	if err != nil {
		return err
	}
	if !containsFold(thirdAContent, keyA) {
		return fmt.Errorf("extended session A second resumed response %q did not include key %q", thirdAContent, keyA)
	}
	if containsFold(thirdAContent, keyB) {
		return fmt.Errorf("extended session A leaked session B key in response %q", thirdAContent)
	}

	secondB, err := llmInstance.GenerateContent(
		ctx,
		[]llmtypes.MessageContent{
			textMessage(llmtypes.ChatMessageTypeHuman, "We are back in session B. Reply with only the exact session B key."),
		},
		llm.WithResumeSessionID(sessionB),
	)
	if err != nil {
		return fmt.Errorf("extended session B resume turn failed: %w", err)
	}
	secondBContent, secondBAdditional, err := claudeExperimentalChoice(secondB)
	if err != nil {
		return err
	}
	if !containsFold(secondBContent, keyB) {
		return fmt.Errorf("extended session B resumed response %q did not include key %q", secondBContent, keyB)
	}
	if containsFold(secondBContent, keyA) {
		return fmt.Errorf("extended session B leaked session A key in response %q", secondBContent)
	}
	if resumedID, _ := secondBAdditional["claude_code_resumed_session_id"].(string); resumedID != sessionB {
		return fmt.Errorf("extended session B expected resumed id %s, got %#v", sessionB, secondBAdditional["claude_code_resumed_session_id"])
	}

	logger.Info(fmt.Sprintf("Extended smoke sessions: A=%s B=%s", sessionA, sessionB))
	return nil
}

func buildClaudeExperimentalWorkspaceDigest() string {
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	var lines []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || len(lines) >= 220 {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", "node_modules", "dist", "build", "tmp", ".next", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		lines = append(lines, fmt.Sprintf("- %s (%d bytes)", filepath.ToSlash(rel), info.Size()))
		return nil
	})
	if len(lines) == 0 {
		lines = []string{"- workspace digest unavailable"}
	}

	var b strings.Builder
	b.WriteString("Workspace root: ")
	b.WriteString(root)
	b.WriteString("\nFiles:\n")
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for b.Len() < 12000 {
		b.WriteString("Context padding: verify that large user messages are accepted without prompt paste timeout or resume corruption.\n")
	}
	return b.String()
}

func randomTestHex(bytesLen int) string {
	if bytesLen <= 0 {
		bytesLen = 4
	}
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

type claudeExperimentalBridgeStreamResult struct {
	contentChunks   int
	toolStartChunks int
	toolEndChunks   int
}

func verifyClaudeExperimentalRealMCPBridgeWithRetry(llmInstance llmtypes.Model, bridgePath string, mockServer *claudeExperimentalBridgeMockServer, timeout time.Duration) (*claudeExperimentalBridgeStreamResult, error) {
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		result, err := verifyClaudeExperimentalRealMCPBridge(llmInstance, bridgePath, mockServer, timeout)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isClaudeExperimentalMCPUnavailable(err) || attempt == 2 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	return nil, lastErr
}

func isClaudeExperimentalMCPUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not available") || strings.Contains(msg, "do not have access")
}

func verifyClaudeExperimentalRealMCPBridge(llmInstance llmtypes.Model, bridgePath string, mockServer *claudeExperimentalBridgeMockServer, timeout time.Duration) (*claudeExperimentalBridgeStreamResult, error) {
	sentinel := "mcp bridge pong from local test"
	mockServer.setResult(sentinel)

	toolsJSON, err := json.Marshal([]map[string]interface{}{
		{
			"name":         "ping",
			"description":  "Return the Claude experimental mcpbridge probe string.",
			"input_schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			"type":         "custom",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build MCP tools JSON: %w", err)
	}
	toolsFile := filepath.Join(os.TempDir(), "claude-experimental-mcp-tools.json")
	if err := os.WriteFile(toolsFile, toolsJSON, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write MCP tools file: %w", err)
	}

	mcpConfig, err := json.Marshal(map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"api-bridge": map[string]interface{}{
				"command": bridgePath,
				"env": map[string]string{
					"MCP_API_URL":    mockServer.url,
					"MCP_API_TOKEN":  "test-token",
					"MCP_TOOLS_FILE": toolsFile,
					"MCP_BRIDGE_LOG": filepath.Join(os.TempDir(), "claude-experimental-mcpbridge.log"),
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build MCP config: %w", err)
	}

	streamChan := make(chan llmtypes.StreamChunk, 128)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resp, err := llmInstance.GenerateContent(
		ctx,
		[]llmtypes.MessageContent{
			textMessage(llmtypes.ChatMessageTypeHuman, "Use the MCP tool named mcp__api-bridge__ping once, then tell me the tool output."),
		},
		llm.WithMCPConfig(string(mcpConfig)),
		llm.WithClaudeCodeTools(""),
		llm.WithAllowedTools("mcp__api-bridge__ping"),
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		return nil, fmt.Errorf("real mcpbridge generation failed: %w", err)
	}

	result := &claudeExperimentalBridgeStreamResult{}
	for chunk := range streamChan {
		switch chunk.Type {
		case llmtypes.StreamChunkTypeContent:
			result.contentChunks++
		case llmtypes.StreamChunkTypeToolCallStart:
			result.toolStartChunks++
		case llmtypes.StreamChunkTypeToolCallEnd:
			result.toolEndChunks++
		}
	}

	content, _, err := claudeExperimentalChoice(resp)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(content, sentinel) {
		return nil, fmt.Errorf("expected MCP bridge sentinel %q, got response %q", sentinel, content)
	}
	if got := mockServer.callCount(); got == 0 {
		return nil, fmt.Errorf("mock bridge endpoint was never called")
	}
	return result, nil
}

func findOrBuildMCPBridge() (string, func(), error) {
	if path, err := exec.LookPath("mcpbridge"); err == nil {
		return path, func() {}, nil
	}

	tmpDir, err := os.MkdirTemp("", "claude-experimental-mcpbridge-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir for mcpbridge: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	outPath := filepath.Join(tmpDir, "mcpbridge")
	cmd := exec.Command("go", "build", "-o", outPath, "github.com/manishiitg/mcpagent/cmd/mcpbridge")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("mcpbridge not found in PATH and build failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return outPath, cleanup, nil
}

type claudeExperimentalBridgeMockServer struct {
	url    string
	server *http.Server
	mu     sync.Mutex
	result string
	calls  int
}

func startClaudeExperimentalBridgeMockServer() (*claudeExperimentalBridgeMockServer, error) {
	mock := &claudeExperimentalBridgeMockServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/tools/custom/ping", func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		mock.calls++
		result := mock.result
		mock.mu.Unlock()

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result":  result,
		})
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	mock.url = "http://" + listener.Addr().String()
	mock.server = &http.Server{Handler: mux}
	go func() {
		_ = mock.server.Serve(listener)
	}()
	return mock, nil
}

func (m *claudeExperimentalBridgeMockServer) setResult(result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.result = result
}

func (m *claudeExperimentalBridgeMockServer) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *claudeExperimentalBridgeMockServer) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = m.server.Shutdown(ctx)
}

func claudeExperimentalChoice(resp *llmtypes.ContentResponse) (string, map[string]interface{}, error) {
	if resp == nil || len(resp.Choices) == 0 {
		return "", nil, fmt.Errorf("response has no choices")
	}
	choice := resp.Choices[0]
	additional := map[string]interface{}{}
	if choice.GenerationInfo != nil && choice.GenerationInfo.Additional != nil {
		additional = choice.GenerationInfo.Additional
	}
	return strings.TrimSpace(choice.Content), additional, nil
}

func parseTmuxMajorFromVersion(version string) (int, bool) {
	fields := strings.Fields(strings.TrimSpace(version))
	if len(fields) < 2 || fields[0] != "tmux" {
		return 0, false
	}
	raw := fields[1]
	var digits strings.Builder
	for _, r := range raw {
		if r < '0' || r > '9' {
			break
		}
		digits.WriteRune(r)
	}
	if digits.Len() == 0 {
		return 0, false
	}
	major, err := strconv.Atoi(digits.String())
	return major, err == nil
}
