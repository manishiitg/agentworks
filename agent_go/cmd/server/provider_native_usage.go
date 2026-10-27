package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	providers "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var readNativeAccountUsage = providers.ReadAccountUsage

func nativeProviderUsage(provider string) bool {
	return provider == "claude-code" || provider == "codex-cli" || provider == "cursor-cli"
}

// Called only after handleStartProviderSetup authorizes the selected account.
// No PTY, startup keystrokes or model prompt participates in a quota query.
func writeNativeProviderUsage(w http.ResponseWriter, r *http.Request, provider string, environment []string) bool {
	// SDK browser login is a user API key. The CLI dashboard token may belong
	// to a different account even under the same HOME; never report its quota.
	if provider == "cursor-cli" && providers.CursorSDKEnabled() {
		environment = append(append([]string{}, environment...), "AGENT_CLI_CREDENTIAL_STORE=memory", "CURSOR_AUTH_TOKEN=")
	}
	usage, err := readNativeAccountUsage(r.Context(), provider, nativeUsageEnvironment(provider, environment))
	reason := ""
	if err != nil {
		reason = "probe_failed"
		var failure llmtypes.AccountUsageFailure
		if errors.As(err, &failure) {
			reason = string(failure)
		}
	}
	if r.Context().Err() != nil {
		return true
	}
	if err != nil && provider != "cursor-cli" {
		log.Printf("[PROVIDER_USAGE] provider=%s transport=native failed=true reason=%s fallback=terminal", provider, reason)
		return false
	}
	text := "Usage limits are unavailable for this account. Check your provider’s account page for plan details."
	if err == nil && len(usage.Windows) > 0 {
		var lines []string
		for _, window := range usage.Windows {
			line := fmt.Sprintf("%s: %.1f%% used", window.Label, window.UsedPercent)
			if window.ResetsAt != nil {
				line += " · resets " + window.ResetsAt.Format(time.RFC1123)
			}
			lines = append(lines, line)
		}
		text = "Account usage limits\n" + strings.Join(lines, "\n") + "\nChecked " + usage.CheckedAt.Format(time.RFC1123)
	}
	// Raw provider errors can include account details; log only outcome and count.
	log.Printf("[PROVIDER_USAGE] provider=%s transport=native success=%t windows=%d reason=%s", provider, err == nil, len(usage.Windows), reason)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"usage_output": text})
	return true
}

// Reuse the child-process allowlist and retain this provider's selected account
// paths/credentials. Native queries need no unrelated server secrets or keys.
func nativeUsageEnvironment(provider string, environment []string) []string {
	allowed := map[string]bool{}
	for _, key := range minimalChildEnvBase {
		allowed[key] = true
	}
	for _, key := range []string{"USERPROFILE", "APPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		allowed[key] = true
	}
	credentials := map[string][]string{
		"claude-code": {"CLAUDE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"},
		"codex-cli":   {"CODEX_HOME", "CODEX_API_KEY", "OPENAI_API_KEY", "OPENAI_BASE_URL"},
		"cursor-cli":  {"CURSOR_API_KEY", "CURSOR_AUTH_TOKEN", "AGENT_CLI_CREDENTIAL_STORE"},
	}
	for _, key := range credentials[provider] {
		allowed[key] = true
	}
	result := []string{}
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if ok && allowed[key] {
			result = append(result, entry)
		}
	}
	return result
}
