package mcpagent

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/mcpagent/llm"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed claude_native_question_hook.py
var claudeNativeQuestionHook string

// Native questions are useful only when a person is attending a retained
// interactive chat; keeping this separate from Full CLI prevents scheduled
// runs from waiting. The SDK answers AskUserQuestion on the turn's own question
// handler, so it needs nothing else. The terminal (tmux) hook answers through the
// product's registered request_clarification route on the session's bridge; that
// tool is registered for the turn but no longer offered in the model's tool list.
func (a *Agent) claudeNativeQuestionsEnabled() bool {
	contract, _ := llm.GetCodingAgentProviderContractForTransport(llm.ProviderClaudeCode, a.modelID, a.claudeCodeTransport)
	if !a.userAnswersNativeQuestions || a.wantsStructuredTransport() {
		return false
	}
	if string(contract.Transport) == "sdk" {
		return true
	}
	if !a.claudeCodePersistentInteractiveSession {
		return false
	}
	_, registered := a.lookupDirectTool("request_clarification")
	return registered
}

// claudeNativeQuestionsReason says why native questions are off for this turn, for the log.
func (a *Agent) claudeNativeQuestionsReason() string {
	contract, _ := llm.GetCodingAgentProviderContractForTransport(llm.ProviderClaudeCode, a.modelID, a.claudeCodeTransport)
	_, registered := a.lookupDirectTool("request_clarification")
	return fmt.Sprintf("persistent=%t transport=%s attended=%t structured=%t answer_route=%t",
		a.claudeCodePersistentInteractiveSession, contract.Transport, a.userAnswersNativeQuestions, a.wantsStructuredTransport(), registered)
}

// claudeNativeQuestionHookFile is a prepared hook script: the interpreter and the private file holding the
// session-scoped bridge credentials.
type claudeNativeQuestionHookFile struct {
	python string
	path   string
}

// prepareClaudeNativeQuestionHook writes the answer hook for one session-scoped bridge. It fails when
// python3 is missing or the bridge carries no session credentials; callers then leave native questions
// off for the turn (AskUserQuestion not offered) instead of failing it.
func prepareClaudeNativeQuestionHook(bridgeConfig string) (claudeNativeQuestionHookFile, error) {
	python, err := exec.LookPath("python3")
	if err != nil {
		return claudeNativeQuestionHookFile{}, fmt.Errorf("Claude question hook requires python3: %w", err)
	}
	var bridge struct {
		Servers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(bridgeConfig), &bridge); err != nil {
		return claudeNativeQuestionHookFile{}, fmt.Errorf("invalid Claude question bridge configuration")
	}
	env := bridge.Servers["api-bridge"].Env
	apiURL, err := normalizeBridgeAPIURL(env["MCP_API_URL"])
	if err != nil || env["MCP_API_TOKEN"] == "" || env["MCP_SESSION_ID"] == "" {
		return claudeNativeQuestionHookFile{}, fmt.Errorf("Claude question hook needs a session-scoped bridge")
	}
	config, _ := json.Marshal(map[string]string{"api_url": apiURL, "token": env["MCP_API_TOKEN"], "session_id": env["MCP_SESSION_ID"]})
	script := "import base64, json\nCONFIG = json.loads(base64.b64decode(" + fmt.Sprintf("%q", base64.StdEncoding.EncodeToString(config)) + "))\n" + claudeNativeQuestionHook
	dir := filepath.Join(os.TempDir(), "claude-code-hooks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return claudeNativeQuestionHookFile{}, err
	}
	sweepClaudeNativeQuestionHooks(dir, time.Now().Add(-claudeNativeQuestionHookMaxAge))
	hash := sha256.Sum256([]byte(script))
	path := filepath.Join(dir, "ask-user-question-"+hex.EncodeToString(hash[:16])+".py")
	// Write privately and atomically: concurrent chats cannot overwrite one
	// another's credentials, and the token never appears in a command argument.
	file, err := os.CreateTemp(dir, "question-hook-*.py")
	if err != nil {
		return claudeNativeQuestionHookFile{}, err
	}
	defer os.Remove(file.Name())
	if _, err = file.WriteString(script); err != nil {
		_ = file.Close()
		return claudeNativeQuestionHookFile{}, err
	}
	if err = file.Close(); err != nil {
		return claudeNativeQuestionHookFile{}, err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return claudeNativeQuestionHookFile{}, err
	}
	return claudeNativeQuestionHookFile{python: python, path: path}, nil
}

// claudeNativeQuestionHookMaxAge is how long a hook script (which holds a session token) is kept. A chat
// rewrites its script on every turn, so only scripts of chats long gone reach this age.
const claudeNativeQuestionHookMaxAge = 24 * time.Hour

func sweepClaudeNativeQuestionHooks(dir string, olderThan time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "ask-user-question-") || !strings.HasSuffix(entry.Name(), ".py") {
			continue
		}
		if info, err := entry.Info(); err == nil && info.ModTime().Before(olderThan) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

// addClaudeNativeQuestionHook appends the PreToolUse answer hook to settings, keeping existing hooks.
func addClaudeNativeQuestionHook(existingSettings string, hook claudeNativeQuestionHookFile) (string, error) {
	settings := map[string]interface{}{}
	if existingSettings != "" {
		if err := json.Unmarshal([]byte(existingSettings), &settings); err != nil || settings == nil {
			return "", fmt.Errorf("invalid existing Claude hook settings")
		}
	}
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
		settings["hooks"] = hooks
	}
	pre, _ := hooks["PreToolUse"].([]interface{})
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	hooks["PreToolUse"] = append(pre, map[string]interface{}{
		"matcher": "AskUserQuestion",
		"hooks":   []map[string]interface{}{{"type": "command", "command": quote(hook.python) + " " + quote(hook.path), "timeout": 1860}},
	})
	data, err := json.Marshal(settings)
	return string(data), err
}

// BuildClaudeNativeQuestionSettings connects Claude's supported PreToolUse
// answer hook to the same session-scoped clarification bridge as other CLIs.
// The caller must enable AskUserQuestion only for an attended chat with a
// registered request_clarification answer route. Existing settings/hooks are retained.
func BuildClaudeNativeQuestionSettings(bridgeConfig, existingSettings string) (string, error) {
	hook, err := prepareClaudeNativeQuestionHook(bridgeConfig)
	if err != nil {
		return "", err
	}
	return addClaudeNativeQuestionHook(existingSettings, hook)
}
