package server

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/sealbox"
)

const (
	rotateTestOld = "old-auth-secret-for-rotation-tests"
	rotateTestNew = "new-auth-secret-for-rotation-tests"
)

// setupRotationDocs builds a temp docs tree with a provider keys file, one
// shared workflow secrets doc, one legacy per-user doc, and one provider
// credentials doc, all sealed with oldSecret. It returns the docs dir, the
// provider keys path, and an env file holding the old secret.
func setupRotationDocs(t *testing.T, oldSecret string) (docsDir, keysFile, envFile string) {
	t.Helper()
	docsDir = t.TempDir()
	ctx := context.Background()

	keysFile = filepath.Join(docsDir, "config", "provider-api-keys.json")
	if err := os.MkdirAll(filepath.Dir(keysFile), 0755); err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"openai":"test-openai-key"}`)
	encrypted, err := encryptProviderKeysWithSecret(plaintext, []byte(oldSecret))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keysFile, []byte(base64.StdEncoding.EncodeToString(encrypted)), 0600); err != nil {
		t.Fatal(err)
	}

	store, err := chathistory.NewFilesystemStore(docsDir)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := deriveSecretsKeyFromSecret([]byte(oldSecret))
	sharedAAD, err := sharedWorkflowSecretAAD("Workflow/demo")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealbox.Seal(oldKey, "shared-plain", sharedAAD)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertWorkflowSecret(ctx, chathistory.SharedWorkflowSecretsUserID, "Workflow/demo", "API_TOKEN", sealed); err != nil {
		t.Fatal(err)
	}
	legacySealed, err := sealbox.Seal(oldKey, "legacy-plain", []byte("owner-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertWorkflowSecret(ctx, "owner-1", "Workflow/demo", "LEGACY", legacySealed); err != nil {
		t.Fatal(err)
	}
	credSealed, err := sealbox.Seal(oldKey, "cursor-token", []byte("owner-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertWorkflowProviderCredential(ctx, "owner-1", "Workflow/demo", "cursor", credSealed); err != nil {
		t.Fatal(err)
	}

	envFile = filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("OTHER_VAR=keep\nAUTH_SECRET="+oldSecret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return docsDir, keysFile, envFile
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRotateAuthSecretEndToEnd(t *testing.T) {
	docsDir, keysFile, envFile := setupRotationDocs(t, rotateTestOld)

	report, err := rotateAuthSecret(authSecretRotationOptions{
		DocsDir:          docsDir,
		ProviderKeysFile: keysFile,
		OldSecret:        rotateTestOld,
		NewSecret:        rotateTestNew,
		EnvFile:          envFile,
		WriteEnv:         true,
		Backup:           true,
	}, io.Discard)
	if err != nil {
		t.Fatalf("rotateAuthSecret: %v", err)
	}
	if !report.ProviderKeysRotated || report.SecretDocs != 3 || report.SecretsRotated != 3 || !report.EnvUpdated {
		t.Fatalf("unexpected report: %+v", report)
	}

	// Provider keys open with the new secret only.
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(readFileString(t, keysFile)))
	if _, err := decryptProviderKeysWithSecret(raw, []byte(rotateTestNew)); err != nil {
		t.Fatalf("provider keys do not open with new secret: %v", err)
	}
	if _, err := decryptProviderKeysWithSecret(raw, []byte(rotateTestOld)); err == nil {
		t.Fatal("provider keys still open with old secret")
	}

	// Every workflow secret opens with the new secret only.
	newKey := deriveSecretsKeyFromSecret([]byte(rotateTestNew))
	oldKey := deriveSecretsKeyFromSecret([]byte(rotateTestOld))
	docs, err := collectWorkflowSecretDocs(docsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("got %d docs, want 3", len(docs))
	}
	for _, doc := range docs {
		aad, err := doc.aad()
		if err != nil {
			t.Fatal(err)
		}
		for name, rec := range doc.records {
			if _, err := sealbox.Open(newKey, rec.EncryptedValue, aad); err != nil {
				t.Errorf("%s %q does not open with new secret: %v", doc.describe(), name, err)
			}
			if _, err := sealbox.Open(oldKey, rec.EncryptedValue, aad); err == nil {
				t.Errorf("%s %q still opens with old secret", doc.describe(), name)
			}
		}
	}

	// Backups exist next to every rewritten file.
	for _, pattern := range []string{
		keysFile + ".bak-*",
		filepath.Join(docsDir, "_users", "*", "workflow_secrets", "*.bak-*"),
		filepath.Join(docsDir, "_users", "*", "workflow_provider_credentials", "*.bak-*"),
	} {
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			t.Errorf("no backup for %s", pattern)
		}
	}

	// Env file updated, other vars preserved.
	envContent := readFileString(t, envFile)
	if !strings.Contains(envContent, "AUTH_SECRET="+rotateTestNew+"\n") {
		t.Errorf("env file missing new secret: %q", envContent)
	}
	if !strings.Contains(envContent, "OTHER_VAR=keep") {
		t.Errorf("env file lost other vars: %q", envContent)
	}
}

func TestRotateAuthSecretDryRunWritesNothing(t *testing.T) {
	docsDir, keysFile, envFile := setupRotationDocs(t, rotateTestOld)
	beforeKeys := readFileString(t, keysFile)
	beforeEnv := readFileString(t, envFile)
	docsBefore, err := collectWorkflowSecretDocs(docsDir)
	if err != nil {
		t.Fatal(err)
	}
	beforeDoc := readFileString(t, docsBefore[0].path)

	report, err := rotateAuthSecret(authSecretRotationOptions{
		DocsDir:          docsDir,
		ProviderKeysFile: keysFile,
		OldSecret:        rotateTestOld,
		NewSecret:        rotateTestNew,
		EnvFile:          envFile,
		WriteEnv:         true,
		Backup:           true,
		DryRun:           true,
	}, io.Discard)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if report.SecretDocs != 3 || report.SecretsRotated != 3 {
		t.Fatalf("dry run counts wrong: %+v", report)
	}
	if got := readFileString(t, keysFile); got != beforeKeys {
		t.Error("dry run rewrote the provider keys file")
	}
	if got := readFileString(t, envFile); got != beforeEnv {
		t.Error("dry run rewrote the env file")
	}
	if got := readFileString(t, docsBefore[0].path); got != beforeDoc {
		t.Error("dry run rewrote a secret document")
	}
	matches, _ := filepath.Glob(filepath.Join(docsDir, "_users", "*", "workflow_secrets", "*.bak-*"))
	if len(matches) != 0 {
		t.Error("dry run created backups")
	}
}

func TestRotateAuthSecretWrongOldSecretWritesNothing(t *testing.T) {
	docsDir, keysFile, _ := setupRotationDocs(t, rotateTestOld)
	beforeKeys := readFileString(t, keysFile)
	docsBefore, err := collectWorkflowSecretDocs(docsDir)
	if err != nil {
		t.Fatal(err)
	}
	beforeDoc := readFileString(t, docsBefore[0].path)

	_, err = rotateAuthSecret(authSecretRotationOptions{
		DocsDir:          docsDir,
		ProviderKeysFile: keysFile,
		OldSecret:        "wrong-old-secret",
		NewSecret:        rotateTestNew,
		WriteEnv:         false,
		Backup:           true,
	}, io.Discard)
	if err == nil {
		t.Fatal("expected an error with the wrong old secret")
	}
	if got := readFileString(t, keysFile); got != beforeKeys {
		t.Error("failed rotation rewrote the provider keys file")
	}
	if got := readFileString(t, docsBefore[0].path); got != beforeDoc {
		t.Error("failed rotation rewrote a secret document")
	}
}

func TestRotateAuthSecretWithoutProviderFile(t *testing.T) {
	docsDir, _, envFile := setupRotationDocs(t, rotateTestOld)
	missing := filepath.Join(docsDir, "config", "provider-api-keys.json")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	report, err := rotateAuthSecret(authSecretRotationOptions{
		DocsDir:          docsDir,
		ProviderKeysFile: missing,
		OldSecret:        rotateTestOld,
		NewSecret:        rotateTestNew,
		EnvFile:          envFile,
		WriteEnv:         false,
		Backup:           false,
	}, io.Discard)
	if err != nil {
		t.Fatalf("rotation without provider file: %v", err)
	}
	if report.ProviderKeysRotated {
		t.Error("reported provider rotation with no file present")
	}
	if report.SecretsRotated != 3 {
		t.Fatalf("got %d secrets, want 3", report.SecretsRotated)
	}
}
