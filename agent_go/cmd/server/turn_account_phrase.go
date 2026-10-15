package server

import (
	"context"
	"strings"
)

// accountPhrase names the account a turn ran on in the words used when its key is rejected: the shared server
// account or a connection by its display name. "" when the account is unknown.
func accountPhrase(provider, accountName string) string {
	label := map[string]string{"codex-cli": "Codex", "claude-code": "Claude", "cursor-cli": "Cursor", "muse-cli": "Muse", "agy-cli": "Antigravity", "pi-cli": "Pi"}[strings.TrimSpace(provider)]
	if label == "" {
		label = strings.TrimSpace(provider)
	}
	switch name := strings.TrimSpace(accountName); name {
	case "":
		return ""
	case adminManagedProviderAccountName:
		return "the shared (admin-managed) " + label + " account"
	default:
		return "the " + label + " account “" + name + "”"
	}
}

// accountPhraseForSession is the account phrase of a session's latest turn, for the agent's plain error message.
func (api *StreamingAPI) accountPhraseForSession(sessionID string) string {
	api.lastQueryMu.RLock()
	req, ok := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	if !ok {
		return ""
	}
	provider, connectionID := api.queryTurnConnectionForSession(req, sessionID)
	if strings.TrimSpace(provider) == "" {
		return ""
	}
	_, name, err := accountAllowedModels(context.Background(), provider, connectionID)
	if err != nil {
		return ""
	}
	return accountPhrase(provider, name)
}
