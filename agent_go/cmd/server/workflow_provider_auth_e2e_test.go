package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/claudeauth"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	claudecodeadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

// TestLiveClaudeUsageAPIDiffersBetweenSavedAndWorkflowCredential is an opt-in,
// machine-local integration check for Claude subscription routing. It verifies:
//
//  1. Claude Code sees the no-token launch as its saved login.
//  2. Claude Code sees the workflow-token launch as oauth_token auth.
//  3. The saved-login and workflow tokens are not the same credential.
//  4. Anthropic's structured usage API differs between the two credentials.
//
// This is not the tmux runtime E2E; that is the next test. This API-level check
// deliberately does not run in ordinary CI. Run it from agent_go with:
//
//	set -a; source .env; set +a
//	RUN_CLAUDE_USAGE_API_E2E=1 go test ./cmd/server \
//	  -run TestLiveClaudeUsageAPIDiffersBetweenSavedAndWorkflowCredential -v
//
// A rate-limited or incomplete usage response fails the test as inconclusive;
// it never turns an inability to compare the accounts into a passing result.
func TestLiveClaudeUsageAPIDiffersBetweenSavedAndWorkflowCredential(t *testing.T) {
	if os.Getenv("RUN_CLAUDE_USAGE_API_E2E") != "1" {
		t.Skip("set RUN_CLAUDE_USAGE_API_E2E=1 to run the live Claude usage API test")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("saved-login credential loading is currently implemented for macOS Keychain")
	}
	if strings.TrimSpace(os.Getenv("AUTH_SECRET")) == "" {
		t.Fatal("AUTH_SECRET is required to decrypt the stored workflow credential")
	}

	repositoryRoot := liveClaudeRepositoryRoot(t)
	workflowToken := liveClaudeWorkflowToken(t, repositoryRoot, "Workflow/rtslatency")

	savedStatus := liveClaudeAuthStatus(t, liveClaudeSavedLoginEnv(os.Environ()))
	if !savedStatus.LoggedIn {
		t.Fatal("Claude Code saved login is not authenticated")
	}
	if savedStatus.AuthMethod == "oauth_token" {
		t.Fatalf("no-token Claude launch reported authMethod=%q; expected the saved login", savedStatus.AuthMethod)
	}

	configDir := t.TempDir()
	scopedStatus := liveClaudeAuthStatus(t, claudeauth.CheckEnv(os.Environ(), configDir, workflowToken))
	if !scopedStatus.LoggedIn || scopedStatus.AuthMethod != "oauth_token" {
		t.Fatalf("workflow-token Claude launch reported loggedIn=%v authMethod=%q; want loggedIn=true authMethod=oauth_token",
			scopedStatus.LoggedIn, scopedStatus.AuthMethod)
	}

	savedToken := liveClaudeSavedLoginToken(t)
	if bytes.Equal([]byte(savedToken), []byte(workflowToken)) {
		t.Fatal("rtslatency workflow token exactly matches Claude Code's saved-login access token")
	}

	// Fetch the workflow usage first. If a previous /usage check has put this
	// credential into Anthropic's polling cooldown, fail without unnecessarily
	// consuming the saved login's usage poll too.
	workflowUsage := liveClaudeFetchUsage(t, "workflow", workflowToken)
	savedUsage := liveClaudeFetchUsage(t, "saved-login", savedToken)

	workflowVector := workflowUsage.vector()
	savedVector := savedUsage.vector()
	if len(workflowVector) == 0 || len(savedVector) == 0 {
		t.Fatalf("incomplete account-specific usage data: saved-login=%s workflow=%s",
			formatLiveClaudeUsage(savedVector), formatLiveClaudeUsage(workflowVector))
	}
	if liveClaudeUsageEqual(savedVector, workflowVector) {
		t.Fatalf("Claude usage signatures are identical; account routing is not proven: saved-login=%s workflow=%s",
			formatLiveClaudeUsage(savedVector), formatLiveClaudeUsage(workflowVector))
	}

	t.Logf("Claude usage routing verified: saved-login=%s workflow=%s",
		formatLiveClaudeUsage(savedVector), formatLiveClaudeUsage(workflowVector))
}

// TestLiveClaudeTmuxUsageDiffersBetweenSavedAndWorkflowCredential exercises
// the user-visible path: two real Claude Code TUI processes in tmux, /usage
// typed into each, and their account-specific usage sections compared.
//
// Run from agent_go:
//
//	set -a; source .env; set +a
//	RUN_CLAUDE_USAGE_E2E=1 go test ./cmd/server \
//	  -run TestLiveClaudeTmuxUsageDiffersBetweenSavedAndWorkflowCredential -v
func TestLiveClaudeTmuxUsageDiffersBetweenSavedAndWorkflowCredential(t *testing.T) {
	if os.Getenv("RUN_CLAUDE_USAGE_E2E") != "1" {
		t.Skip("set RUN_CLAUDE_USAGE_E2E=1 to run the live Claude tmux /usage test")
	}
	if strings.TrimSpace(os.Getenv("AUTH_SECRET")) == "" {
		t.Fatal("AUTH_SECRET is required to decrypt the stored workflow credential")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatal("tmux is required for the live Claude /usage test")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Fatal("Claude Code CLI is required for the live Claude /usage test")
	}

	repositoryRoot := liveClaudeRepositoryRoot(t)
	workflowToken := liveClaudeWorkflowToken(t, repositoryRoot, "Workflow/rtslatency")

	// Start the scoped credential first so a cooldown on that account does not
	// also consume a fresh saved-login /usage poll.
	workflowScreen := liveClaudeTmuxUsageScreen(t, "workflow", workflowToken)
	savedScreen := liveClaudeTmuxUsageScreen(t, "saved-login", "")

	workflowSignature := liveClaudeTmuxUsageSignature(t, "workflow", workflowScreen)
	savedSignature := liveClaudeTmuxUsageSignature(t, "saved-login", savedScreen)
	if workflowSignature == savedSignature {
		t.Fatalf("Claude tmux /usage signatures are identical; account routing is not proven:\n%s", savedSignature)
	}
	t.Logf("Claude tmux /usage routing verified:\nsaved-login:\n%s\nworkflow:\n%s",
		savedSignature, workflowSignature)
}

// TestLiveClaudeProductionAdapterUsesWorkflowCredential runs the same adapter
// and one-shot structured transport used by workflow steps and background
// agents. The machine's saved Claude login may be exhausted; the call must
// still succeed through rtslatency's stored setup token. This catches
// regressions where the token is loaded correctly by Builder but remapped or
// dropped at the final Claude process-launch boundary.
//
// Run from agent_go:
//
//	set -a; source .env; set +a
//	RUN_CLAUDE_WORKFLOW_AUTH_E2E=1 go test ./cmd/server \
//	  -run TestLiveClaudeProductionAdapterUsesWorkflowCredential -v
func TestLiveClaudeProductionAdapterUsesWorkflowCredential(t *testing.T) {
	if os.Getenv("RUN_CLAUDE_WORKFLOW_AUTH_E2E") != "1" {
		t.Skip("set RUN_CLAUDE_WORKFLOW_AUTH_E2E=1 to run the production adapter credential test")
	}
	if strings.TrimSpace(os.Getenv("AUTH_SECRET")) == "" {
		t.Fatal("AUTH_SECRET is required to decrypt the stored workflow credential")
	}

	repositoryRoot := liveClaudeRepositoryRoot(t)
	workflowToken := liveClaudeWorkflowToken(t, repositoryRoot, "Workflow/rtslatency")
	adapter := claudecodeadapter.NewClaudeCodeInteractiveAdapterWithOAuthToken("claude-sonnet-5-5", workflowToken, &e2eMockLogger{})
	t.Cleanup(func() { _ = claudecodeadapter.CleanupClaudeCodeTmuxSessions(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	const marker = "RTS_WORKFLOW_AUTH_OK"
	response, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with exactly: " + marker}},
	}},
		claudecodeadapter.WithWorkingDir(t.TempDir()),
		claudecodeadapter.WithClaudeCodeTools(""),
		claudecodeadapter.WithEffort("low"),
		// Workflow steps use the one-shot structured transport. Keeping this
		// enabled makes the E2E exercise the same final process launcher instead
		// of only proving the retained tmux path.
		claudecodeadapter.WithClaudeStructuredTransport(true),
	)
	if err != nil {
		t.Fatalf("production Claude adapter did not use the RTS workflow credential: %v", err)
	}
	if response == nil || len(response.Choices) == 0 || !strings.Contains(response.Choices[0].Content, marker) {
		t.Fatalf("production Claude adapter response did not contain %q: %#v", marker, response)
	}
}

type liveClaudeAuthStatusResponse struct {
	LoggedIn   bool   `json:"loggedIn"`
	AuthMethod string `json:"authMethod"`
}

func liveClaudeAuthStatus(t *testing.T, env []string) liveClaudeAuthStatusResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "auth", "status", "--json")
	cmd.Env = env
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("claude auth status failed: %v", err)
	}
	var status liveClaudeAuthStatusResponse
	if err := json.Unmarshal(output, &status); err != nil {
		t.Fatalf("decode claude auth status: %v", err)
	}
	return status
}

func liveClaudeSavedLoginEnv(base []string) []string {
	blocked := map[string]bool{
		"ANTHROPIC_API_KEY":       true,
		"ANTHROPIC_AUTH_TOKEN":    true,
		"ANTHROPIC_BASE_URL":      true,
		"CLAUDE_CODE_OAUTH_TOKEN": true,
		"CLAUDE_CONFIG_DIR":       true,
		"CLAUDE_CODE_USE_BEDROCK": true,
		"CLAUDE_CODE_USE_VERTEX":  true,
		"CLAUDE_CODE_USE_FOUNDRY": true,
	}
	out := make([]string, 0, len(base))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[key] {
			out = append(out, entry)
		}
	}
	return out
}

func liveClaudeTmuxUsageScreen(t *testing.T, label, token string) string {
	t.Helper()
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("find Claude Code CLI: %v", err)
	}
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get Claude tmux working directory: %v", err)
	}
	sessionName := fmt.Sprintf("pulse-usage-e2e-%d-%s", time.Now().UnixNano(), strings.ReplaceAll(label, "_", "-"))
	launchPath := filepath.Join(t.TempDir(), "launch-claude")
	launch := `#!/bin/sh
unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL CLAUDE_CODE_OAUTH_TOKEN
unset CLAUDE_CODE_USE_BEDROCK CLAUDE_CODE_USE_VERTEX CLAUDE_CODE_USE_FOUNDRY
export DISABLE_AUTOUPDATER=1
`
	if token != "" {
		launch += "export CLAUDE_CODE_OAUTH_TOKEN=" + liveClaudeShellQuote(token) + "\n"
	}
	launch += "exec " + liveClaudeShellQuote(claudePath) + "\n"
	if err := os.WriteFile(launchPath, []byte(launch), 0o700); err != nil {
		t.Fatalf("write %s Claude tmux launcher: %v", label, err)
	}

	start := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "-x", "160", "-y", "60", "-c", workDir, launchPath)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start %s Claude tmux session: %v\n%s", label, err, output)
	}
	defer exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	readyDeadline := time.Now().Add(30 * time.Second)
	var screen string
	for time.Now().Before(readyDeadline) {
		screen = liveClaudeCaptureTmux(t, sessionName)
		lower := strings.ToLower(screen)
		if strings.Contains(lower, "do you trust the files") {
			t.Fatalf("%s Claude tmux session stopped at a workspace trust prompt", label)
		}
		if strings.Contains(lower, "not logged in") || strings.Contains(lower, "please run /login") {
			t.Fatalf("%s Claude tmux session is not authenticated", label)
		}
		if strings.Contains(screen, "❯") {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !strings.Contains(screen, "❯") {
		t.Fatalf("%s Claude tmux session did not reach the prompt; last screen:\n%s", label, screen)
	}

	if output, err := exec.Command("tmux", "send-keys", "-t", sessionName, "-l", "/usage").CombinedOutput(); err != nil {
		t.Fatalf("type /usage in %s Claude tmux session: %v\n%s", label, err, output)
	}
	if output, err := exec.Command("tmux", "send-keys", "-t", sessionName, "Enter").CombinedOutput(); err != nil {
		t.Fatalf("submit /usage in %s Claude tmux session: %v\n%s", label, err, output)
	}

	usageDeadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(usageDeadline) {
		screen = liveClaudeCaptureTmux(t, sessionName)
		lower := strings.ToLower(screen)
		if strings.Contains(lower, "current session") ||
			strings.Contains(lower, "current week") ||
			strings.Contains(lower, "what's contributing to your limits usage") ||
			strings.Contains(lower, "what’s contributing to your limits usage") ||
			strings.Contains(lower, "please try again later") ||
			strings.Contains(lower, "failed to fetch usage") {
			return screen
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s Claude /usage screen did not render; last screen:\n%s", label, screen)
	return ""
}

func liveClaudeCaptureTmux(t *testing.T, sessionName string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-J", "-t", sessionName).Output()
	if err != nil {
		t.Fatalf("capture Claude tmux session %s: %v", sessionName, err)
	}
	return strings.ReplaceAll(string(output), "\r", "")
}

func liveClaudeTmuxUsageSignature(t *testing.T, label, screen string) string {
	t.Helper()
	lower := strings.ToLower(screen)
	if strings.Contains(lower, "please try again later") || strings.Contains(lower, "failed to fetch usage") {
		t.Fatalf("%s Claude /usage was temporarily unavailable or rate-limited; rerun after the cooldown", label)
	}

	var signature []string
	for _, rawLine := range strings.Split(screen, "\n") {
		line := strings.Join(strings.Fields(rawLine), " ")
		lineLower := strings.ToLower(line)
		if strings.Contains(lineLower, "what's contributing to your limits usage") ||
			strings.Contains(lineLower, "what’s contributing to your limits usage") {
			break
		}
		if strings.Contains(lineLower, "current session") ||
			strings.Contains(lineLower, "current week") ||
			strings.Contains(lineLower, "% used") ||
			strings.Contains(lineLower, "resets") ||
			strings.Contains(lineLower, "claude api") ||
			strings.Contains(lineLower, "extra usage") ||
			strings.Contains(lineLower, "fable") {
			signature = append(signature, line)
		}
	}
	if len(signature) == 0 {
		t.Fatalf("%s Claude /usage did not expose an account-specific signature; screen:\n%s", label, screen)
	}
	return strings.Join(signature, "\n")
}

func liveClaudeShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func liveClaudeRepositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if liveClaudeIsDir(filepath.Join(dir, "agent_go")) && liveClaudeIsDir(filepath.Join(dir, "workspace-docs")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root containing agent_go and workspace-docs")
		}
		dir = parent
	}
}

func liveClaudeIsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func liveClaudeWorkflowToken(t *testing.T, repositoryRoot, workflowPath string) string {
	t.Helper()
	credentialDir := filepath.Join(repositoryRoot, "workspace-docs", "_users", "default", "workflow_provider_credentials")
	entries, err := os.ReadDir(credentialDir)
	if err != nil {
		t.Fatalf("read workflow provider credentials: %v", err)
	}
	type credentialValue struct {
		EncryptedValue string `json:"encrypted_value"`
	}
	type credentialRecord struct {
		WorkflowPath string                     `json:"workflow_path"`
		Credentials  map[string]credentialValue `json:"credentials"`
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(credentialDir, entry.Name()))
		if err != nil {
			t.Fatalf("read workflow provider credential record: %v", err)
		}
		var record credentialRecord
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatalf("decode workflow provider credential record: %v", err)
		}
		if normalizeWorkflowCredentialPath(record.WorkflowPath) != normalizeWorkflowCredentialPath(workflowPath) {
			continue
		}
		encrypted := strings.TrimSpace(record.Credentials[claudeCodeProviderID].EncryptedValue)
		if encrypted == "" {
			t.Fatalf("%s has no stored Claude Code credential", workflowPath)
		}
		token, err := decryptSecretValue(encrypted, "default")
		if err != nil {
			t.Fatalf("decrypt %s Claude Code credential: %v", workflowPath, err)
		}
		token = strings.TrimSpace(token)
		if token == "" {
			t.Fatalf("%s Claude Code credential decrypted to an empty token", workflowPath)
		}
		return token
	}
	t.Fatalf("no Claude Code credential record found for %s", workflowPath)
	return ""
}

func liveClaudeSavedLoginToken(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("read Claude Code saved login from macOS Keychain: %v", err)
	}
	var record struct {
		ClaudeAIOAuth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(output, &record); err != nil {
		t.Fatalf("decode Claude Code Keychain credential: %v", err)
	}
	token := strings.TrimSpace(record.ClaudeAIOAuth.AccessToken)
	if token == "" {
		t.Fatal("Claude Code Keychain credential has no claudeAiOauth.accessToken")
	}
	return token
}

type liveClaudeUsageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

type liveClaudeUsageResponse struct {
	FiveHour       *liveClaudeUsageWindow `json:"five_hour"`
	SevenDay       *liveClaudeUsageWindow `json:"seven_day"`
	SevenDaySonnet *liveClaudeUsageWindow `json:"seven_day_sonnet"`
	SevenDayOpus   *liveClaudeUsageWindow `json:"seven_day_opus"`
}

type liveClaudeUsagePoint struct {
	Utilization float64
	ResetsAt    string
}

func (u liveClaudeUsageResponse) vector() map[string]liveClaudeUsagePoint {
	out := map[string]liveClaudeUsagePoint{}
	for name, window := range map[string]*liveClaudeUsageWindow{
		"five_hour":        u.FiveHour,
		"seven_day":        u.SevenDay,
		"seven_day_sonnet": u.SevenDaySonnet,
		"seven_day_opus":   u.SevenDayOpus,
	} {
		if window != nil && window.Utilization != nil {
			out[name] = liveClaudeUsagePoint{
				Utilization: *window.Utilization,
				ResetsAt:    strings.TrimSpace(window.ResetsAt),
			}
		}
	}
	return out
}

func liveClaudeFetchUsage(t *testing.T, label, token string) liveClaudeUsageResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		t.Fatalf("create %s usage request: %v", label, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("anthropic-beta", "oauth-2025-04-20")
	request.Header.Set("User-Agent", "pulse-claude-usage-routing-e2e/1.0")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("fetch %s Claude usage: %v", label, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatalf("read %s Claude usage response: %v", label, err)
	}
	if response.StatusCode != http.StatusOK {
		var apiError struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &apiError)
		t.Fatalf("fetch %s Claude usage returned HTTP %d (error_type=%q retry_after=%q); rerun after the cooldown",
			label, response.StatusCode, apiError.Error.Type, response.Header.Get("Retry-After"))
	}
	var usage liveClaudeUsageResponse
	if err := json.Unmarshal(body, &usage); err != nil {
		t.Fatalf("decode %s Claude usage response: %v", label, err)
	}
	return usage
}

func liveClaudeUsageEqual(left, right map[string]liveClaudeUsagePoint) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftPoint := range left {
		rightPoint, ok := right[key]
		if !ok || leftPoint != rightPoint {
			return false
		}
	}
	return true
}

func formatLiveClaudeUsage(usage map[string]liveClaudeUsagePoint) string {
	keys := make([]string, 0, len(usage))
	for key := range usage {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		point := usage[key]
		if point.ResetsAt == "" {
			parts = append(parts, fmt.Sprintf("%s=%.1f%%", key, point.Utilization))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%.1f%%@%s", key, point.Utilization, point.ResetsAt))
	}
	if len(parts) == 0 {
		return "<none>"
	}
	return strings.Join(parts, ",")
}
