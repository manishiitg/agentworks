package agycli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAgyPrivateHomeTrustDoesNotChangeGlobalSettings(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	globalPath := filepath.Join(base, ".gemini", "antigravity-cli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workingDir := filepath.Join(base, "workflow")
	home, cleanup, err := agyIsolatedHome(nil, workingDir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	raw, err := os.ReadFile(filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		TrustedWorkspaces []string `json:"trustedWorkspaces"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if len(settings.TrustedWorkspaces) != 1 || settings.TrustedWorkspaces[0] != workingDir {
		t.Fatalf("private trust = %v", settings.TrustedWorkspaces)
	}
	raw, err = os.ReadFile(globalPath)
	if err != nil || string(raw) != "{}\n" {
		t.Fatalf("global settings changed: %q, %v", raw, err)
	}
}

func TestAgyPrivateHomeKeepsConversationFilesWithoutCopyingLiveIndex(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	runtimeDir := filepath.Join(base, ".gemini", "antigravity-cli")
	conversationDir := filepath.Join(runtimeDir, "conversations")
	if err := os.MkdirAll(conversationDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conversationDir, "resume.db"), []byte("native conversation"), 0o600); err != nil {
		t.Fatal(err)
	}
	indexNames := []string{"conversation_summaries.db", "conversation_summaries.db-wal", "conversation_summaries.db-shm"}
	for _, name := range indexNames {
		if err := os.WriteFile(filepath.Join(runtimeDir, name), []byte("live index"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	home, cleanup, err := agyIsolatedHome(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	privateRuntime := filepath.Join(home, ".gemini", "antigravity-cli")
	for _, name := range indexNames {
		if _, err := os.Stat(filepath.Join(privateRuntime, name)); !os.IsNotExist(err) {
			t.Fatalf("private home inherited live conversation index %s: %v", name, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(privateRuntime, "conversations", "resume.db")); err != nil || string(got) != "native conversation" {
		t.Fatalf("native resume conversation unavailable: %q, %v", got, err)
	}
}
