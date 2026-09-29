package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Per-account status, sign-out and terminal permissions, through the real
// handlers with faked CLIs (the fakes record the HOME they ran with).

func fakeProviderCLI(script string) providerSetupCommand {
	return providerSetupCommand{command: "/bin/sh", args: []string{"-c", script}}
}

func TestProviderAccountActionsStatusSignOutAndTerminal(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	marker := filepath.Join(t.TempDir(), "logout-home")
	origStatus, origLogout, origVerify := providerStatusCommands, providerLogoutCommands, claudeVerifyCommand
	t.Cleanup(func() {
		providerStatusCommands, providerLogoutCommands, claudeVerifyCommand = origStatus, origLogout, origVerify
	})
	providerStatusCommands = map[string]providerSetupCommand{
		"claude-code": fakeProviderCLI(`printf '{"loggedIn":true,"authMethod":"claude.ai","email":"alice@x.com","orgName":"Acme","accessToken":"sk-ant-oat01-SECRETSECRETSECRETSECRET"}'`),
		"codex-cli":   fakeProviderCLI(`printf 'Logged in using an API key - sk-proj-SECRETSECRETSECRET\n'`),
	}
	claudeVerifyCommand = fakeProviderCLI(`printf 'Failed to authenticate. API Error: 401 token sk-ant-oat01-SECRETSECRETSECRETSECRET is invalid\n'; exit 1`)
	providerLogoutCommands = map[string]providerSetupCommand{
		"claude-code": fakeProviderCLI(`printf '%s' "$HOME" > ` + marker),
		"codex-cli":   fakeProviderCLI(`printf '%s' "$HOME" > ` + marker),
	}
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Alice Claude", "auth_method": "cli_login"})
	keyAccount := env.addAccount(t, "alice", map[string]interface{}{"provider": "codex-cli", "display_name": "Alice Codex key", "credential": "alice-codex-key"})
	vars := func(id string) map[string]string { return map[string]string{"connectionID": id} }
	status := func(user, id, query string) (int, string) {
		w := env.do(t, env.api.handleProviderAccountStatus, http.MethodGet, "/status"+query, user, nil, vars(id))
		return w.Code, w.Body.String()
	}
	signOut := func(user, id string) (int, string) {
		w := env.do(t, env.api.handleProviderAccountSignOut, http.MethodPost, "/sign-out", user, nil, vars(id))
		return w.Code, w.Body.String()
	}
	inspect := func(user, id string) int {
		body := map[string]interface{}{"provider": "claude-code", "action": "inspect", "connection_id": id}
		return env.do(t, env.api.handleStartProviderSetup, http.MethodPost, "/", user, body, nil).Code
	}

	// Status: the owner sees state and identity, never a credential.
	code, body := status("alice", account.ID, "")
	var parsed providerAccountStatus
	_ = json.Unmarshal([]byte(body), &parsed)
	if code != http.StatusOK || parsed.State != "signed_in" || parsed.Identity != "alice@x.com" || strings.Contains(body, "SECRET") {
		t.Fatalf("owner status: %d %s", code, body)
	}
	// The real check runs on demand and reports a rejected login, redacted.
	code, body = status("alice", account.ID, "?verify=1")
	_ = json.Unmarshal([]byte(body), &parsed)
	if parsed.State != "key_rejected" || !parsed.Verified || strings.Contains(body, "SECRET") || !strings.Contains(parsed.Detail, "401") {
		t.Fatalf("verified status: %d %s", code, body)
	}
	_, body = status("alice", keyAccount.ID, "")
	_ = json.Unmarshal([]byte(body), &parsed)
	if parsed.State != "signed_in" || parsed.Identity != "an API key" || strings.Contains(body, "SECRET") || strings.Contains(body, "alice-codex-key") {
		t.Fatalf("codex status: %s", body)
	}
	// Someone the account is not available to cannot even see its status.
	if code, _ := status("bob", account.ID, ""); code != http.StatusNotFound {
		t.Fatalf("bob read the status of Alice's private account: %d", code)
	}

	// Shared with Bob: he may check status, but not open a terminal or sign out.
	if w := env.do(t, env.api.handleProviderConnection, http.MethodPatch, "/", "alice", map[string]interface{}{"sharing": map[string]interface{}{"mode": "shared", "users": []string{"bob"}}}, vars(account.ID)); w.Code != http.StatusNoContent {
		t.Fatalf("share: %d", w.Code)
	}
	if code, _ := status("bob", account.ID, ""); code != http.StatusOK {
		t.Fatalf("bob's status of a shared account: %d", code)
	}
	if code := inspect("bob", account.ID); code != http.StatusForbidden {
		t.Fatalf("non-manager opened the account terminal: %d", code)
	}
	if code, _ := signOut("bob", account.ID); code != http.StatusNotFound {
		t.Fatalf("non-manager signed the account out: %d", code)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a refused sign-out still ran the CLI")
	}

	// The owner's sign-out runs the CLI's logout in the account's HOME.
	if code, body := signOut("alice", account.ID); code != http.StatusOK {
		t.Fatalf("owner sign-out: %d %s", code, body)
	}
	ranIn, _ := os.ReadFile(marker)
	accountHome, _ := providerConnectionHome(account.ID)
	if string(ranIn) != accountHome || string(ranIn) == env.home {
		t.Fatalf("sign-out ran in %q, want the account HOME %q (never the service HOME %q)", ranIn, accountHome, env.home)
	}
	if view, ok := findAccountView(env.list(t, "alice", ""), account.ID); !ok || view.AuthMethod != "cli_login" {
		t.Fatal("sign-out removed the account record")
	}
	// A key account has no sign-out.
	if code, _ := signOut("alice", keyAccount.ID); code != http.StatusBadRequest {
		t.Fatalf("sign-out offered for an API-key account: %d", code)
	}

	// The server account: terminal and sign-out are admin only; the logout
	// runs with the service HOME.
	_ = os.Remove(marker)
	if code := inspect("bob", "global:claude-code"); code != http.StatusForbidden {
		t.Fatalf("member opened the server terminal: %d", code)
	}
	if code, _ := signOut("bob", "global:claude-code"); code != http.StatusForbidden {
		t.Fatalf("member signed out the server account: %d", code)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a refused server sign-out still ran the CLI")
	}
	logs := captureLogs(t)
	if code, body := signOut("admin", "global:claude-code"); code != http.StatusOK {
		t.Fatalf("admin server sign-out: %d %s", code, body)
	}
	if ranIn, _ := os.ReadFile(marker); string(ranIn) != env.home {
		t.Fatalf("server sign-out ran in %q, want the service HOME", ranIn)
	}
	if !strings.Contains(logs.String(), "[PROVIDER_SETUP] claude-code sign-out for server account by admin") {
		t.Fatalf("server sign-out not logged: %s", logs.String())
	}
	closeProviderSetups(env.api)
}

// Muse has no status command: its login record is read, never returned.
func TestProviderAccountActionsMuseStatusReadsOnlyTheEmail(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "muse-cli", "display_name": "Alice Muse", "auth_method": "cli_login"})
	home, _ := providerConnectionHome(account.ID)
	target, err := env.api.resolveProviderAccountTarget(t.Context(), account.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if status := checkProviderAccountStatus(t.Context(), target, false); status.State != "signed_out" {
		t.Fatalf("muse without a login: %+v", status)
	}
	dir := filepath.Join(home, ".config", "muse")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"providers":{"meta":{"user_email":"alice@meta.test","access_token":"SECRETSECRETSECRETSECRETSECRET"}}}`), 0o600)
	status := checkProviderAccountStatus(t.Context(), target, false)
	data, _ := json.Marshal(status)
	if status.State != "signed_in" || status.Identity != "alice@meta.test" || strings.Contains(string(data), "SECRET") {
		t.Fatalf("muse status: %s", data)
	}
}
