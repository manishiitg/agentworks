package server

import "testing"

// A private account can sign in with the CLI's own login for every CLI whose
// login lives under the account's HOME / XDG / CLAUDE_CONFIG_DIR, and the
// keys it yields never carry a credential (the stored login is used).
func TestPrivateAccountCLILoginProviders(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex-cli", "cursor-cli", "muse-cli"} {
		keys, err := connectionCredentialKeys(storedProviderConnection{ProviderConnection: ProviderConnection{Provider: provider, AuthMethod: "cli_login"}})
		if err != nil {
			t.Fatalf("%s: browser login refused: %v", provider, err)
		}
		for name, value := range map[string]*string{"claude": keys.ClaudeCodeOAuthToken, "codex": keys.CodexCLI, "cursor": keys.CursorCLI, "muse": keys.MuseCLI} {
			if value == nil || *value != "" {
				t.Fatalf("%s: %s key must be present and empty so the stored login is used", provider, name)
			}
		}
	}
	for _, provider := range []string{"pi-cli", "agy-cli"} {
		if _, err := connectionCredentialKeys(storedProviderConnection{ProviderConnection: ProviderConnection{Provider: provider, AuthMethod: "cli_login"}}); err == nil {
			t.Fatalf("%s: browser login must stay unavailable", provider)
		}
	}
}
