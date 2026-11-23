package codexcli

import (
	"os"
	"path/filepath"
	"testing"
)

// Excellence 2026-10-15: a chat whose login was linked to the shared account's file had that file overwritten with
// the chat's configured key. A linked login is never touched.
func TestSeedCodexAPIKeyLoginNeverWritesThroughALink(t *testing.T) {
	shared := t.TempDir()
	sharedAuth := filepath.Join(shared, "auth.json")
	const original = `{"auth_mode":"apikey","OPENAI_API_KEY":"sk-shared-original"}`
	if err := os.WriteFile(sharedAuth, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. auth.json in the session's home is a symlink to the shared file.
	home := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sharedAuth, filepath.Join(home, "auth.json")); err != nil {
		t.Fatal(err)
	}
	env := codexLoginEnv([]string{"CODEX_HOME=" + home}, "sk-person-config")
	if err := seedCodexAPIKeyLogin(env, "", ""); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(sharedAuth); string(raw) != original {
		t.Fatalf("the shared login was overwritten through the link: %q", raw)
	}
	if info, err := os.Lstat(filepath.Join(home, "auth.json")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link must stay a link")
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json.agentworks")); err == nil {
		t.Fatal("no marker may be left for a login this function did not write")
	}
	// Removing a key (no key configured) must not remove or follow the link either.
	if err := seedCodexAPIKeyLogin([]string{"CODEX_HOME=" + home}, "", ""); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(sharedAuth); string(raw) != original {
		t.Fatalf("the shared login changed when no key was configured: %q", raw)
	}

	// 2. the .codex folder itself is a symlink to the shared folder.
	linkedHome := filepath.Join(t.TempDir(), ".codex")
	if err := os.Symlink(shared, linkedHome); err != nil {
		t.Fatal(err)
	}
	if err := seedCodexAPIKeyLogin([]string{"CODEX_API_KEY=sk-person-config", "CODEX_HOME=" + linkedHome}, "", ""); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(sharedAuth); string(raw) != original {
		t.Fatalf("the shared login was overwritten through a linked folder: %q", raw)
	}

	// 3. the session's own regular file is still seeded.
	own := filepath.Join(t.TempDir(), ".codex")
	if err := seedCodexAPIKeyLogin([]string{"CODEX_API_KEY=sk-own", "CODEX_HOME=" + own}, "", ""); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(own, "auth.json")); err != nil || len(raw) == 0 {
		t.Fatalf("an own home must still be seeded: %v", err)
	}
}
