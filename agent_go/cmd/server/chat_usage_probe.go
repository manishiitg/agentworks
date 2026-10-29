package server

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	providers "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
)

// The chat's usage chip is the one place that shows plan limits. When the chat's own session has not reported
// them yet, the server asks the provider directly for the chat's current account (a quota request, no CLI, no
// model call). Only that provider and that account are asked; the answer is kept for a few minutes per account.

var readNativeAccountUsage = providers.ReadAccountUsage

const (
	chatUsageProbeTTL       = 5 * time.Minute
	chatUsageProbeFailedTTL = 2 * time.Minute
	chatUsageProbeTimeout   = 10 * time.Second
)

type chatUsageProbeResult struct {
	limits  []chatUsageLimit
	expires time.Time
}

var chatUsageProbes = struct {
	mu      sync.Mutex
	results map[string]chatUsageProbeResult
	running map[string]bool
}{results: map[string]chatUsageProbeResult{}, running: map[string]bool{}}

// chatProbeProvider is the provider name the quota request knows for a chat's provider; "" when it has none.
func chatProbeProvider(provider string) string {
	for _, known := range []string{"claude-code", "codex-cli", "cursor-cli"} {
		if sameChatProvider(known, provider) {
			return known
		}
	}
	return ""
}

// probedChatLimits is the plan use of one account, asked of its provider when the chat itself reported none.
// owner is the chat's tab owner; the account must be one they may run on. "" connectionID means the chat's
// account is not known, so nothing is asked rather than guessing one.
func (api *StreamingAPI) probedChatLimits(owner, provider, connectionID string) []chatUsageLimit {
	provider, connectionID = chatProbeProvider(provider), strings.TrimSpace(connectionID)
	if provider == "" || connectionID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), chatUsageProbeTimeout)
	defer cancel()
	environment, ok := api.chatUsageProbeAccount(ctx, owner, provider, connectionID)
	if !ok {
		return nil
	}
	key := provider + "|" + connectionID
	chatUsageProbes.mu.Lock()
	if cached, found := chatUsageProbes.results[key]; found && time.Now().Before(cached.expires) {
		chatUsageProbes.mu.Unlock()
		return cached.limits
	}
	if chatUsageProbes.running[key] {
		chatUsageProbes.mu.Unlock()
		return nil
	}
	chatUsageProbes.running[key] = true
	chatUsageProbes.mu.Unlock()

	started := time.Now()
	usage, err := readNativeAccountUsage(ctx, provider, nativeUsageEnvironment(provider, environment()))
	var limits []chatUsageLimit
	for _, window := range usage.Windows {
		var resets time.Time
		if window.ResetsAt != nil {
			resets = *window.ResetsAt
		}
		limits = appendChatUsageLimit(limits, chatProbeWindowName(window.Label), window.UsedPercent, resets)
	}
	ttl, reason := chatUsageProbeTTL, ""
	if err != nil {
		ttl, reason = chatUsageProbeFailedTTL, "probe_failed"
		var failure llmtypes.AccountUsageFailure
		if errors.As(err, &failure) {
			reason = string(failure)
		}
	}
	chatUsageProbes.mu.Lock()
	chatUsageProbes.results[key] = chatUsageProbeResult{limits: limits, expires: time.Now().Add(ttl)}
	delete(chatUsageProbes.running, key)
	chatUsageProbes.mu.Unlock()
	// Provider errors can carry account details; log only the outcome.
	log.Printf("[CHAT_USAGE] probe provider=%s account=%s owner=%s success=%t windows=%d shown=%d reason=%s elapsed_ms=%d", provider, connectionID, owner, err == nil, len(usage.Windows), len(limits), reason, time.Since(started).Milliseconds())
	return limits
}

// chatProbeWindowName maps the quota request's window labels to the names the chip knows. Windows of one model
// ("Weekly · Opus") or of a second Codex limit after the first are left out by appendChatUsageLimit.
func chatProbeWindowName(label string) string {
	if at := strings.LastIndex(label, " · "); at >= 0 && !strings.HasPrefix(label, "Weekly · ") {
		label = label[at+len(" · "):]
	}
	switch label {
	case "5-hour limit", "5h":
		return "five_hour"
	case "Weekly limit", "7d":
		return "seven_day"
	case "Monthly total":
		return "month"
	}
	return ""
}

// chatUsageProbeAccount checks the chat's owner may run on the account and returns how to build its environment:
// the account's own HOME and credentials, or the server account's.
func (api *StreamingAPI) chatUsageProbeAccount(ctx context.Context, owner, provider, connectionID string) (func() []string, bool) {
	if connectionID == "global:"+provider || connectionID == llmguard.ServerDefaultConnectionPrefix+provider {
		if !api.serverAccountAvailableToCaller(ctx, owner, provider) {
			return nil, false
		}
		return func() []string {
			keys := MergedProviderAPIKeys(ctx)
			if provider == "claude-code" {
				return serverClaudeSetupEnvironment(keys)
			}
			return providerConnectionSetupEnvironment(providerConnectionEnvLabel(provider, connectionID), keys, true)
		}, true
	}
	if strings.HasPrefix(connectionID, "global:") || strings.HasPrefix(connectionID, llmguard.ServerDefaultConnectionPrefix) {
		return nil, false // another provider's server account
	}
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(ctx)
	providerConnectionsMu.Unlock()
	if err != nil {
		return nil, false
	}
	for _, record := range records {
		if record.ID != connectionID {
			continue
		}
		if !sameChatProvider(record.Provider, provider) || !providerEnabled(record.Provider) || personalProviderConnectionsLocked(record.Provider) {
			return nil, false
		}
		if record.OwnerUserID != owner {
			if _, err := api.admitProviderAccount(ctx, providerAccountScope{Principal: owner}, record.Provider, record.ID); err != nil {
				return nil, false
			}
		}
		keys, err := providerConnectionRuntimeKeys(record)
		if err != nil {
			return nil, false
		}
		return func() []string {
			return providerConnectionSetupEnvironment(providerConnectionEnvLabel(provider, connectionID), keys)
		}, true
	}
	return nil, false
}

// nativeUsageEnvironment keeps the child-process allowlist and this provider's selected account paths and
// credentials. The quota request needs no unrelated server secrets or keys.
func nativeUsageEnvironment(provider string, environment []string) []string {
	// SDK browser login is a user API key. The CLI dashboard token may belong
	// to a different account even under the same HOME; never report its quota.
	if provider == "cursor-cli" && providers.CursorSDKEnabled() {
		environment = append(append([]string{}, environment...), "AGENT_CLI_CREDENTIAL_STORE=memory", "CURSOR_AUTH_TOKEN=")
	}
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
