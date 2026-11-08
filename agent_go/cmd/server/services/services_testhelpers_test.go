package services

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readArgvLines(t *testing.T, argvFile string) []string {
	t.Helper()
	data, err := os.ReadFile(argvFile)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Exercise registry semantics without invoking an installed CLI or touching
// the developer's real credentials. The CLI contract is tested separately.
func isolateGogClientStore(t *testing.T) {
	t.Helper()
	t.Setenv("GOG_HOME", t.TempDir())
	original := storeGogClient
	t.Cleanup(func() { storeGogClient = original })
	storeGogClient = func(ctx context.Context, _ string, name string, raw []byte) error {
		id, secret, err := parseGmailClientSecretJSON(raw)
		if err != nil {
			return err
		}
		path, err := gogClientCredentialsPath(name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]string{"client_id": id, "client_secret": secret})
		return os.WriteFile(path, body, 0600)
	}
}

func gmailOAuthClientJSON(t *testing.T, id, secret string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"installed": map[string]string{"client_id": id, "client_secret": secret},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}
