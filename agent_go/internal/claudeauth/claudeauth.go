// Package claudeauth validates Claude Code setup tokens.
//
// A `claude setup-token` credential scopes a session to one person's own Claude
// subscription. This package exists because there are two callers — AgentWorks
// workflows and Video Studio projects — and a weakened check in either one
// silently reintroduces the failure it prevents: falling back to whichever
// Claude Code login happens to be on the machine, and billing them for it.
package claudeauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ValidateOAuthToken reports whether Anthropic accepts token. Both checks run
// against a throwaway config directory, so neither reads nor disturbs the
// caller's own saved login.
//
// It costs one tiny Haiku request, and that expense is the whole point:
// `claude auth status` reports loggedIn/oauth_token for ANY non-empty
// CLAUDE_CODE_OAUTH_TOKEN, including outright garbage, because it never
// contacts the server. Verified 2026-08-05 — a made-up token passes the status
// check and is rejected 401 by the first real request. Structural checks alone
// would store a dead token and surface it as a failed turn later.
func ValidateOAuthToken(parent context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("Claude Code token is required")
	}
	configDir, err := os.MkdirTemp("", "claude-auth-check-*")
	if err != nil {
		return fmt.Errorf("could not prepare Claude Code credential check")
	}
	defer os.RemoveAll(configDir)
	env := CheckEnv(os.Environ(), configDir, token)

	// Cheap structural pass first, purely so "CLI not installed" and "this is
	// not an OAuth credential" get their own clear messages.
	statusCtx, cancelStatus := context.WithTimeout(parent, 15*time.Second)
	defer cancelStatus()
	statusCmd := exec.CommandContext(statusCtx, "claude", "auth", "status", "--json")
	statusCmd.Env = env
	statusOut, err := statusCmd.Output()
	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return fmt.Errorf("Claude Code CLI is not installed")
		}
		return fmt.Errorf("Claude Code did not accept this token")
	}
	var status struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if err := json.Unmarshal(statusOut, &status); err != nil {
		return fmt.Errorf("Claude Code returned an unreadable authentication status")
	}
	// authMethod matters as much as loggedIn: a machine-wide login would also
	// report loggedIn, and accepting it is exactly the fallback being prevented.
	if !status.LoggedIn || status.AuthMethod != "oauth_token" {
		return fmt.Errorf("Claude Code did not recognize this as an OAuth token")
	}

	// The pass that actually proves the token works. --strict-mcp-config keeps
	// the caller's MCP servers out of a check that only needs one round trip.
	askCtx, cancelAsk := context.WithTimeout(parent, 90*time.Second)
	defer cancelAsk()
	askCmd := exec.CommandContext(askCtx, "claude", "-p", "hi",
		"--model", verifyModel, "--max-turns", "1", "--strict-mcp-config", "--permission-mode", "dontAsk")
	askCmd.Env = env
	askOut, err := askCmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if errors.Is(askCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("Claude Code timed out while checking this token")
	}
	// The CLI reports auth failures on stdout ("Failed to authenticate. API
	// Error: 401 OAuth access token is invalid."), so pass its own wording
	// through — it distinguishes expired from revoked from mistyped.
	if detail := firstLine(string(askOut)); detail != "" {
		return fmt.Errorf("Anthropic rejected this token: %s", detail)
	}
	return fmt.Errorf("Anthropic rejected this token")
}

// verifyModel is the cheapest model that still proves the credential works.
const verifyModel = "claude-haiku-5-5"

func firstLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// CheckEnv builds the environment for a credential check: base with every
// ambient Anthropic credential removed, plus the token under test.
//
// The removals are the point. An inherited ANTHROPIC_API_KEY would let the
// check pass on the wrong credential and shift billing from the Claude
// subscription to an Anthropic API account, and an inherited CLAUDE_CONFIG_DIR
// would point the CLI back at the machine's saved login.
func CheckEnv(base []string, configDir, token string) []string {
	blocked := map[string]bool{
		"ANTHROPIC_API_KEY":       true,
		"ANTHROPIC_AUTH_TOKEN":    true,
		"ANTHROPIC_BASE_URL":      true,
		"CLAUDE_CODE_OAUTH_TOKEN": true,
		"CLAUDE_CONFIG_DIR":       true,
	}
	out := make([]string, 0, len(base)+2)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if blocked[key] {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "CLAUDE_CONFIG_DIR="+configDir, "CLAUDE_CODE_OAUTH_TOKEN="+token)
}
