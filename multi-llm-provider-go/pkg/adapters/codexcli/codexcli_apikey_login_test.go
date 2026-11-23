package codexcli

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedCodexAPIKeyLogin(t *testing.T) {
	home := t.TempDir()
	env := []string{"PATH=/bin", "CODEX_API_KEY=sk-test-123", "CODEX_HOME=" + home}
	if err := seedCodexAPIKeyLogin(env, "", ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil || !strings.Contains(string(raw), `"auth_mode": "apikey"`) || !strings.Contains(string(raw), "sk-test-123") {
		t.Fatalf("auth.json = %q, %v", raw, err)
	}
	info, _ := os.Stat(filepath.Join(home, "auth.json"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
	// a changed key replaces the saved one; no key leaves the login alone
	_ = seedCodexAPIKeyLogin([]string{"CODEX_API_KEY=sk-new", "CODEX_HOME=" + home}, "", "")
	raw, _ = os.ReadFile(filepath.Join(home, "auth.json"))
	if !strings.Contains(string(raw), "sk-new") {
		t.Fatal("key not replaced")
	}
	// the key removed from the configuration: the saved key login goes too
	_ = seedCodexAPIKeyLogin([]string{"CODEX_HOME=" + home}, "", "")
	if _, err := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("stale key login kept")
	}
	// a key the user pasted into Codex themselves (no marker) is never touched
	own := `{"auth_mode":"apikey","OPENAI_API_KEY":"sk-theirs"}`
	_ = os.WriteFile(filepath.Join(home, "auth.json"), []byte(own), 0o600)
	_ = seedCodexAPIKeyLogin([]string{"CODEX_HOME=" + home}, "", "")
	if raw, _ := os.ReadFile(filepath.Join(home, "auth.json")); string(raw) != own {
		t.Fatal("the user's own key login changed")
	}
	// a browser login is never touched
	oauth := `{"auth_mode":"chatgpt","tokens":{"access_token":"x"}}`
	_ = os.WriteFile(filepath.Join(home, "auth.json"), []byte(oauth), 0o600)
	_ = seedCodexAPIKeyLogin([]string{"CODEX_HOME=" + home}, "", "")
	if raw, _ := os.ReadFile(filepath.Join(home, "auth.json")); string(raw) != oauth {
		t.Fatal("browser login changed")
	}
}

// A key that reaches the adapter through the app's configuration (not an account environment) must still
// become Codex's saved login, or the terminal UI sits on its sign-in screen.
func TestCodexLoginEnvUsesTheConfiguredKeyOnlyWhenTheEnvironmentHasNone(t *testing.T) {
	home := t.TempDir()
	env := codexLoginEnv([]string{"PATH=/bin", "CODEX_HOME=" + home}, " sk-config ")
	if err := seedCodexAPIKeyLogin(env, "", ""); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(home, "auth.json")); err != nil || !strings.Contains(string(raw), "sk-config") {
		t.Fatalf("auth.json = %q, %v", raw, err)
	}
	withKey := []string{"CODEX_API_KEY=sk-account", "CODEX_HOME=" + home}
	if got := codexLoginEnv(withKey, "sk-config"); len(got) != 2 || got[0] != "CODEX_API_KEY=sk-account" {
		t.Fatalf("an account key must win over the configured one: %v", got)
	}
	if got := codexLoginEnv(withKey, ""); len(got) != 2 {
		t.Fatalf("no configured key leaves the environment alone: %v", got)
	}
}

// A ChatGPT sign-in the person made in this home is never replaced by a configured key.
func TestSeedCodexAPIKeyLoginKeepsAnExistingChatGPTSignIn(t *testing.T) {
	home := t.TempDir()
	signIn := `{"auth_mode":"chatgpt","tokens":{"access_token":"x","refresh_token":"y"}}`
	path := filepath.Join(home, "auth.json")
	if err := os.WriteFile(path, []byte(signIn), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedCodexAPIKeyLogin([]string{"CODEX_API_KEY=sk-config", "CODEX_HOME=" + home}, "", ""); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != signIn {
		t.Fatalf("the person's own sign-in was replaced: %q", raw)
	}
	if _, err := os.Stat(path + ".agentworks"); err == nil {
		t.Fatal("no marker may be left beside a login this function did not write")
	}
}

// PLAT-872-55 T2: selecting a file-login account must never seed the server key.
func TestCodexTmuxLoginUsesOnlySelectedAccountCredential(t *testing.T) {
	home := t.TempDir()
	opts := &llmtypes.CallOptions{}
	llmtypes.WithProviderAccountEnvironment(map[string]string{"HOME": home, "CODEX_HOME": home})(opts)
	c := NewCodexCLIAdapter("server-test-key", "", &MockLogger{})
	if err := c.seedTmuxAccountLogin(opts, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("server key seeded into personal home")
	}
	llmtypes.WithProviderAccountCredentials(map[string]string{"OPENAI_API_KEY": "account-test-key"})(opts)
	if err := c.seedTmuxAccountLogin(opts, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil || !strings.Contains(string(raw), "account-test-key") || strings.Contains(string(raw), "server-test-key") {
		t.Fatal("selected account key was not used")
	}
}
