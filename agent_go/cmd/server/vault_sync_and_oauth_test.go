package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCrewVaultReaderAndScopeSurviveEmptyCache(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]},{"id":"member","username":"member","products":["work"]}]}`)

	registry := newTestRegistry(t)
	prev := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() { defaultProjectOwnersOverride = prev })

	const projectID = "srv-vault-cold"
	const folder = "srv-vault-cold-12345678"
	if err := registry.Register(projectOwnerRecord{
		Product:   crewProfileID,
		Folder:    folder,
		OwnerID:   "owner",
		Shared:    true,
		ProjectID: projectID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.AddUser(crewProfileID, folder, "member"); err != nil {
		t.Fatal(err)
	}

	// Ensure the 20s contents cache has no entry for this project (cold boot or post-edit).
	forgetCrewVaultContents(projectID)

	vaultID := crewVaultID(projectID)
	secretName, err := vaultSecretName(vaultID, "API_KEY")
	if err != nil {
		t.Fatal(err)
	}

	scopedCtx := withCrewVaultScopeForNames(context.Background(), []string{secretName})
	if got := crewVaultScope(scopedCtx); got != vaultID {
		t.Fatalf("crewVaultScope = %q, want %q", got, vaultID)
	}
	if got := crewVaultReader(scopedCtx, "member"); got != "owner" {
		t.Fatalf("crewVaultReader(member) = %q, want owner", got)
	}
	if got := crewVaultReader(scopedCtx, "stranger"); got != "stranger" {
		t.Fatalf("crewVaultReader(stranger) = %q, want stranger", got)
	}
}

func TestWriteDesktopConnectionsAtomic(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", strings.Repeat("test-desktop-secret-", 3))

	want := []desktopConnection{{
		ID:     "conn-1",
		Name:   "Test Server",
		Server: "https://server.example.com",
		Token:  "tok-123",
	}}
	if err := writeDesktopConnections("alice", want); err != nil {
		t.Fatalf("writeDesktopConnections: %v", err)
	}
	got, err := readDesktopConnections("alice")
	if err != nil {
		t.Fatalf("readDesktopConnections: %v", err)
	}
	if len(got) != 1 || got[0].ID != "conn-1" || got[0].Token != "tok-123" {
		t.Fatalf("readDesktopConnections = %+v, want %+v", got, want)
	}
	path, err := desktopConnectionFile("alice")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file %q", e.Name())
		}
	}
}
