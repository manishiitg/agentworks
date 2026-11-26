package agycli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// An Antigravity account signed in with Google runs on its own sign-in: it never inherits the server's Gemini key
// mode, its login comes from its own home, and agy keeps that sign-in in a file there rather than the one system
// keychain entry every account would share.
func TestAgyGoogleAccountUsesItsOwnSignIn(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "server-key")
	account := t.TempDir()
	acp := filepath.Join(account, ".gemini", "antigravity-acp")
	if err := os.MkdirAll(acp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acp, "login.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := &llmtypes.CallOptions{}
	llmtypes.WithProviderAccountEnvironment(map[string]string{"HOME": account})(opts)

	login := agyLoginFor(opts)
	if login.home != account || login.keyMode {
		t.Fatalf("Google account login = %+v, want its own home and no key mode", login)
	}
	// The person's own agy skills (their real ~/.gemini) reach the account's run too (#3973).
	person := t.TempDir()
	t.Setenv("HOME", person)
	for _, skill := range []string{filepath.Join(person, ".gemini", "config", "skills", "review"), filepath.Join(account, ".gemini", "config", "skills", "deploy")} {
		if err := os.MkdirAll(skill, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	home, cleanup, err := agyIsolatedHomeIn(nil, t.TempDir(), login, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(home, ".gemini", "antigravity-acp", "login.json")); err != nil {
		t.Fatalf("the account's sign-in was not carried into the run: %v", err)
	}
	for _, skill := range []string{"review", "deploy"} {
		if _, err := os.Stat(filepath.Join(home, ".gemini", "config", "skills", skill, "SKILL.md")); err != nil {
			t.Fatalf("skill %s is missing from the run: %v", skill, err)
		}
	}
	settings, _ := os.ReadFile(agySettingsPath(home))
	if strings.Contains(string(settings), `"modelProvider"`) {
		t.Fatalf("a Google account run switched to key mode: %s", settings)
	}
	env := strings.Join(agyAccountEnv([]string{"PATH=/bin"}, opts), "\n")
	if !strings.Contains(env, "AGY_ACP_FORCE_FILE_STORAGE=1") || !strings.Contains(env, "SSH_CONNECTION=") {
		t.Fatalf("account run env misses file sign-in storage: %s", env)
	}

	// A key account runs on its own key.
	llmtypes.WithProviderAccountCredentials(map[string]string{"GEMINI_API_KEY": "account-key"})(opts)
	if !agyLoginFor(opts).keyMode {
		t.Fatal("a key account did not run in key mode")
	}
}
