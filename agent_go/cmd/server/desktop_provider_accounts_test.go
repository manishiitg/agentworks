package server

import "testing"

// Only the caller's own accounts (and, for the installation's owner, this Mac's own login) may be added to a
// server; a browser login is never sent as a credential.
func TestLocalAccountForServerOnlyOwnAccounts(t *testing.T) {
	records := []storedProviderConnection{
		{ProviderConnection: ProviderConnection{ID: "mine-key", Provider: "codex-cli", DisplayName: "Work key", OwnerUserID: "me", AuthMethod: "api_key"}, Credential: "key-value"},
		{ProviderConnection: ProviderConnection{ID: "mine-login", Provider: "codex-cli", DisplayName: "My login", OwnerUserID: "me", AuthMethod: "cli_login"}, Credential: "stale"},
		{ProviderConnection: ProviderConnection{ID: "theirs", Provider: "codex-cli", DisplayName: "Their key", OwnerUserID: "someone", AuthMethod: "api_key"}, Credential: "their-value"},
	}
	t.Setenv("CODEX_API_KEY", "")
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "")
	for _, tc := range []struct {
		name, id    string
		admin       bool
		auth, value string
		refused     bool
	}{
		{"own key is copied", "mine-key", false, "api_key", "key-value", false},
		{"own browser login is signed in there", "mine-login", false, "cli_login", "", false},
		{"someone else's account", "theirs", true, "", "", true},
		{"this Mac's login, not its owner", "global:codex-cli", false, "", "", true},
		{"this Mac's login, its owner", "global:codex-cli", true, "cli_login", "", false},
	} {
		account, err := localAccountForServer(records, nil, "me", tc.id, tc.admin)
		if (err != nil) != tc.refused {
			t.Fatalf("%s: err=%v, refused want %v", tc.name, err, tc.refused)
		}
		if !tc.refused && (account.AuthMethod != tc.auth || account.Credential != tc.value) {
			t.Fatalf("%s: got %s with credential %q", tc.name, account.AuthMethod, account.Credential)
		}
	}
}
