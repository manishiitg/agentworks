package mcpagent

import (
	"log"
	"strings"
	"sync/atomic"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// invalidAPIKeyPhrases are the provider texts for a rejected key. A permission error without one of them keeps its
// own text.
var invalidAPIKeyPhrases = []string{"incorrect api key provided", "invalid_api_key", "invalid api key", "invalid x-api-key", "api key not valid"}

// accountPhraseResolver lets the application name the account a turn ran on, as a short noun phrase ("the shared
// (admin-managed) Codex account", "your Codex account \u201cWork key\u201d"). The agent knows only the session.
var accountPhraseResolver atomic.Pointer[func(sessionID string) string]

// SetAccountPhraseResolver installs the application's session -> account phrase lookup (nil removes it).
func SetAccountPhraseResolver(fn func(sessionID string) string) {
	if fn == nil {
		accountPhraseResolver.Store(nil)
		return
	}
	accountPhraseResolver.Store(&fn)
}

func accountPhraseFor(sessionID string) string {
	if fn := accountPhraseResolver.Load(); fn != nil {
		return strings.TrimSpace((*fn)(sessionID))
	}
	return ""
}

// plainAuthFailureMessage turns a provider's rejection of the API key into words a person can act on, and says so in the
// log (PLAT-861-220). It returns "" for any other error, which then shows as before.
func plainAuthFailureMessage(provider, sessionID string, err error) string {
	if err == nil || llmerrors.KindOf(err) != llmerrors.KindAuth {
		return ""
	}
	text := strings.ToLower(err.Error())
	for _, phrase := range invalidAPIKeyPhrases {
		if strings.Contains(text, phrase) {
			account := accountPhraseFor(sessionID)
			log.Printf("[TURN] stalled cause=invalid_api_key provider=%s session=%s account=%q", provider, sessionID, account)
			if account == "" {
				account = "this account"
			}
			return providerLabel(provider) + " rejected the API key on " + account + ". Update the key in Accounts, or pick another agent. Your message is saved."
		}
	}
	return ""
}

func providerLabel(provider string) string {
	switch provider {
	case "codex-cli":
		return "OpenAI (Codex)"
	case "claude-code":
		return "Anthropic (Claude)"
	case "cursor-cli":
		return "Cursor"
	case "muse-cli":
		return "Muse"
	case "agy-cli":
		return "Antigravity"
	case "pi-cli":
		return "Pi"
	}
	if provider == "" {
		return "The provider"
	}
	return provider
}
