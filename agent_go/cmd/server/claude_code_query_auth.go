package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// admittedAccount must be the result of this turn's account admission, never
// request metadata. An explicit account's login is scoped; the service HOME's
// ambient login cannot satisfy this preflight.
func claudeCodeQueryAuthenticationError(profile *resolvedAgentProfile, provider string, admittedAccount *storedProviderConnection) error {
	if !claudeCodeTokenMissingForSingleProductDeployment(profile, provider) {
		return nil
	}
	if admittedAccount == nil || admittedAccount.Provider != "claude-code" {
		// Shown to the person in the chat, who cannot set a service variable: say what they can do. (The operator's fix, a
		// CLAUDE_CODE_OAUTH_TOKEN for the service, stays in the message for the one who runs it.)
		return fmt.Errorf("Claude Code is not signed in for you. Open Providers, choose Claude Code and add your own account (Add my account, Browser login), or switch this chat to another model. An operator can instead set CLAUDE_CODE_OAUTH_TOKEN for the whole service.")
	}
	keys, err := providerConnectionRuntimeKeys(*admittedAccount)
	if err != nil {
		return err
	}
	if keys.ClaudeCodeOAuthToken != nil && strings.TrimSpace(*keys.ClaudeCodeOAuthToken) != "" {
		return nil
	}
	// Private multi-user Claude logins use file storage, including on hosts
	// whose ordinary desktop CLI uses a keychain. Read only the admitted HOME;
	// do not invoke a login probe that can fall back to the service account.
	if admittedAccount.AuthMethod == "cli_login" {
		data, readErr := os.ReadFile(filepath.Join(keys.RuntimeEnvironment["CLAUDE_CONFIG_DIR"], ".credentials.json"))
		var credentials struct {
			OAuth struct {
				AccessToken string `json:"accessToken"`
			} `json:"claudeAiOauth"`
		}
		if readErr == nil && json.Unmarshal(data, &credentials) == nil && strings.TrimSpace(credentials.OAuth.AccessToken) != "" {
			return nil
		}
	}
	return fmt.Errorf("The selected Claude account is not signed in. Sign in to that account in Providers, then retry.")
}
