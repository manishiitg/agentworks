package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeClientSecretFile(t *testing.T, path, id, secret string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"installed": map[string]string{"client_id": id, "client_secret": secret},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// The default client_secret.json location follows XDG_CONFIG_HOME like the
// Gmail refresh-token directory, the MCP connector tokens, and the DCR client
// cache -- on server A ~/.config is root-owned and only the XDG tree is writable.
func TestReadGmailClientSecretFileHonoursXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg-config")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("GMAIL_CLIENT_SECRET_FILE", "")

	writeClientSecretFile(t, filepath.Join(xdg, "gws", "client_secret.json"), "id-under-xdg", "secret-under-xdg")

	id, secret, err := readGmailClientSecretFile()
	if err != nil {
		t.Fatalf("readGmailClientSecretFile: %v", err)
	}
	if id != "id-under-xdg" || secret != "secret-under-xdg" {
		t.Fatalf("got (%q, %q), want the file under XDG_CONFIG_HOME", id, secret)
	}
}

func TestReadGmailClientSecretFileFallsBackToHomeConfigWithoutXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GMAIL_CLIENT_SECRET_FILE", "")

	writeClientSecretFile(t, filepath.Join(home, ".config", "gws", "client_secret.json"), "id-under-home", "secret-under-home")

	id, secret, err := readGmailClientSecretFile()
	if err != nil {
		t.Fatalf("readGmailClientSecretFile: %v", err)
	}
	if id != "id-under-home" || secret != "secret-under-home" {
		t.Fatalf("got (%q, %q), want the file under ~/.config", id, secret)
	}
}

func TestReadGmailClientSecretFileExplicitOverrideWins(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg-config")
	explicit := filepath.Join(t.TempDir(), "custom-secret.json")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("GMAIL_CLIENT_SECRET_FILE", explicit)

	writeClientSecretFile(t, filepath.Join(xdg, "gws", "client_secret.json"), "id-under-xdg", "secret-under-xdg")
	writeClientSecretFile(t, explicit, "id-explicit", "secret-explicit")

	id, secret, err := readGmailClientSecretFile()
	if err != nil {
		t.Fatalf("readGmailClientSecretFile: %v", err)
	}
	if id != "id-explicit" || secret != "secret-explicit" {
		t.Fatalf("got (%q, %q), want GMAIL_CLIENT_SECRET_FILE to win", id, secret)
	}
}
