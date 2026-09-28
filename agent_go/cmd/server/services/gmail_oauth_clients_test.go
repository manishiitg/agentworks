package services

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestCreateOAuthClientRequiresName(t *testing.T) {
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	isolateGogClientStore(t)
	if _, err := CreateOAuthClient(context.Background(), "", gmailOAuthClientJSON(t, "id", "secret"), false); err == nil {
		t.Fatal("expected an error for an empty name")
	}
}

func TestCreateOAuthClientValidatesNameShape(t *testing.T) {
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	isolateGogClientStore(t)
	// Leading/trailing whitespace is trimmed before validation (a pasted name
	// with an accidental space should not be rejected), so it is not tested
	// here as an invalid case.
	for _, bad := range []string{"Primary", "primary_one", "-primary", "primary-"} {
		if _, err := CreateOAuthClient(context.Background(), bad, gmailOAuthClientJSON(t, "id", "secret"), false); err == nil {
			t.Errorf("name %q: expected a validation error", bad)
		}
	}
}

func TestCreateOAuthClientRejectsMalformedSecret(t *testing.T) {
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	isolateGogClientStore(t)
	if _, err := CreateOAuthClient(context.Background(), "primary", []byte(`{"neither":"installed nor web"}`), false); err == nil {
		t.Fatal("expected an error for a secret with no installed/web client")
	}
	if _, err := CreateOAuthClient(context.Background(), "primary", []byte(`not json`), false); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

// This is the direct fix for the bug this registry replaces: a second upload
// under the same name must never silently replace the first.
func TestCreateOAuthClientRefusesToOverwriteWithoutReplace(t *testing.T) {
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	isolateGogClientStore(t)
	ctx := context.Background()

	first, err := CreateOAuthClient(ctx, "primary", gmailOAuthClientJSON(t, "id-1", "secret-1"), false)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := CreateOAuthClient(ctx, "primary", gmailOAuthClientJSON(t, "id-2", "secret-2"), false); err == nil {
		t.Fatal("expected the second create (replace=false) to be refused")
	}

	// Refused: the original credential must still be intact.
	id, secret, err := GetOAuthClientSecret("primary")
	if err != nil {
		t.Fatalf("GetOAuthClientSecret after refused overwrite: %v", err)
	}
	if id != "id-1" || secret != "secret-1" {
		t.Fatalf("refused overwrite changed the stored credential: got (%q, %q)", id, secret)
	}

	// Explicit replace=true is honored.
	second, err := CreateOAuthClient(ctx, "primary", gmailOAuthClientJSON(t, "id-2", "secret-2"), true)
	if err != nil {
		t.Fatalf("explicit replace: %v", err)
	}
	if second.CreatedAt != first.CreatedAt {
		t.Errorf("replace should preserve CreatedAt, got %v want %v", second.CreatedAt, first.CreatedAt)
	}
	id, secret, err = GetOAuthClientSecret("primary")
	if err != nil {
		t.Fatalf("GetOAuthClientSecret after replace: %v", err)
	}
	if id != "id-2" || secret != "secret-2" {
		t.Fatalf("replace did not take effect: got (%q, %q)", id, secret)
	}
}

func TestListAndDeleteOAuthClients(t *testing.T) {
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	isolateGogClientStore(t)
	ctx := context.Background()

	if clients, err := ListOAuthClients(); err != nil || len(clients) != 0 {
		t.Fatalf("expected an empty list on a fresh registry, got %+v, err=%v", clients, err)
	}

	if _, err := CreateOAuthClient(ctx, "beta", gmailOAuthClientJSON(t, "id-beta", "secret-beta"), false); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	if _, err := CreateOAuthClient(ctx, "alpha", gmailOAuthClientJSON(t, "id-alpha", "secret-alpha"), false); err != nil {
		t.Fatalf("create alpha: %v", err)
	}

	clients, err := ListOAuthClients()
	if err != nil {
		t.Fatalf("ListOAuthClients: %v", err)
	}
	if len(clients) != 2 || clients[0].Name != "alpha" || clients[1].Name != "beta" {
		t.Fatalf("expected [alpha, beta] sorted by name, got %+v", clients)
	}

	if !OAuthClientExists("alpha") {
		t.Error("expected alpha to exist")
	}
	if OAuthClientExists("gamma") {
		t.Error("did not expect gamma to exist")
	}

	if err := DeleteOAuthClient("alpha"); err != nil {
		t.Fatalf("DeleteOAuthClient: %v", err)
	}
	if OAuthClientExists("alpha") {
		t.Error("expected alpha to be gone after delete")
	}
	if err := DeleteOAuthClient("alpha"); err == nil {
		t.Fatal("expected deleting an already-deleted client to error")
	}
}

func TestImportLegacyOAuthClientWithNoLegacySecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GMAIL_CLIENT_SECRET_FILE", filepath.Join(home, "does-not-exist.json"))
	t.Setenv("GOOGLE_WORKSPACE_CLI_CLIENT_ID", "")
	t.Setenv("GOOGLE_WORKSPACE_CLI_CLIENT_SECRET", "")
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	isolateGogClientStore(t)

	g := &GmailService{}
	if _, _, err := g.ImportLegacyOAuthClient(context.Background(), "legacy"); err == nil {
		t.Fatal("expected an error when there is no legacy client_secret.json to import")
	}
}

func TestImportLegacyOAuthClientWithNoConnectionsToBackfill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GMAIL_CLIENT_SECRET_FILE", "")
	t.Setenv("GOOGLE_WORKSPACE_CLI_CLIENT_ID", "")
	t.Setenv("GOOGLE_WORKSPACE_CLI_CLIENT_SECRET", "")
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	isolateGogClientStore(t)
	writeClientSecretFile(t, filepath.Join(home, ".config", "gws", "client_secret.json"), "legacy-id", "legacy-secret")

	// An empty service (no connections) never calls SaveConfig — its
	// backfill loop finds nothing to do — so this stays a pure unit test
	// with no workspace API involved.
	g := &GmailService{}
	client, backfilled, err := g.ImportLegacyOAuthClient(context.Background(), "legacy")
	if err != nil {
		t.Fatalf("ImportLegacyOAuthClient: %v", err)
	}
	if client.Name != "legacy" || client.ClientID != "legacy-id" {
		t.Fatalf("unexpected client: %+v", client)
	}
	if backfilled != 0 {
		t.Fatalf("expected 0 connections backfilled on an empty registry, got %d", backfilled)
	}
	if !OAuthClientExists("legacy") {
		t.Error("expected the imported client to be registered")
	}
}

func TestImportLegacyOAuthClientDerivesStableNameForReconnect(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GMAIL_CLIENT_SECRET_FILE", "")
	t.Setenv("GOOGLE_WORKSPACE_CLI_CLIENT_ID", "")
	t.Setenv("GOOGLE_WORKSPACE_CLI_CLIENT_SECRET", "")
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	isolateGogClientStore(t)
	writeClientSecretFile(t, filepath.Join(home, ".config", "gws", "client_secret.json"), "legacy-id", "legacy-secret")

	g := &GmailService{}
	first, _, err := g.ImportLegacyOAuthClient(context.Background(), "")
	if err != nil {
		t.Fatalf("automatic legacy import: %v", err)
	}
	if !strings.HasPrefix(first.Name, "legacy-") {
		t.Fatalf("automatic legacy client name = %q", first.Name)
	}
	second, _, err := g.ImportLegacyOAuthClient(context.Background(), "")
	if err != nil {
		t.Fatalf("idempotent automatic legacy import: %v", err)
	}
	if second.Name != first.Name || second.ClientID != first.ClientID {
		t.Fatalf("automatic legacy import drifted: first=%+v second=%+v", first, second)
	}
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
