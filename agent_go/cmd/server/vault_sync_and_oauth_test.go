package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestEnsureCrewVaultDoesNotCacheWhenSeedingTimesOut(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	secret := strings.Repeat("s", 32)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]}]}`)

	registry := newTestRegistry(t)
	prev := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() { defaultProjectOwnersOverride = prev })

	const projectID = "srv-vault-seed-timeout"
	const folder = "srv-vault-seed-timeout-12345678"
	if err := registry.Register(projectOwnerRecord{
		Product:   crewProfileID,
		Folder:    folder,
		OwnerID:   "owner",
		Shared:    true,
		ProjectID: projectID,
	}); err != nil {
		t.Fatal(err)
	}
	crewVaultCache.Delete(projectID)
	t.Cleanup(func() { crewVaultCache.Delete(projectID) })

	var created atomic.Bool
	var blockSeed atomic.Bool
	blockSeed.Store(true)
	var seedCalls atomic.Int32

	vaultSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/admin/crew-vaults":
			created.Store(true)
			_, _ = w.Write([]byte(`{"id":"v-seed-timeout","created":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/vaults":
			if created.Load() {
				seedCalls.Add(1)
				if blockSeed.Load() {
					<-r.Context().Done()
					return
				}
			}
			_, _ = w.Write([]byte(`{"vaults":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/vaults":
			_, _ = w.Write([]byte(`{"id":"v-personal"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer vaultSrv.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", vaultSrv.URL)

	api := &StreamingAPI{}
	shortCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if _, err := api.ensureCrewVault(shortCtx, projectID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded during timed-out seeding, got: %v", err)
	}
	if crewVaultSeeded(projectID) {
		t.Fatal("crew vault was marked seeded despite seeding timeout")
	}
	if _, cached := crewVaultCache.Load(projectID); cached {
		t.Fatal("crew vault was cached despite incomplete seeding, preventing retry")
	}

	// Retry once the Vault service responds in time: seeding must run again and complete.
	blockSeed.Store(false)
	beforeRetry := seedCalls.Load()
	id, err := api.ensureCrewVault(context.Background(), projectID)
	if err != nil {
		t.Fatalf("retry ensureCrewVault failed: %v", err)
	}
	if id != "v-seed-timeout" {
		t.Fatalf("vault id = %q, want v-seed-timeout", id)
	}
	if seedCalls.Load() <= beforeRetry {
		t.Fatal("retry did not re-run seedCrewVault")
	}
	if !crewVaultSeeded(projectID) {
		t.Fatal("crew vault was not marked seeded after successful retry")
	}
	if _, cached := crewVaultCache.Load(projectID); !cached {
		t.Fatal("crew vault was not cached after successful seeding")
	}
}
