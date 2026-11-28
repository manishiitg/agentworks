package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMutateSQLPreservesVaultsAndBlocksTamperingAndDDL(t *testing.T) {
	dir := t.TempDir()
	st, err := NewSQLiteStore(filepath.Join(dir, "gateway.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	st.AddWorkspace(Workspace{ID: "w", Name: "w"})
	st.AddUser(User{ID: "alice", WorkspaceID: "w", Email: "alice@example.com"})
	st.AddUser(User{ID: "bob", WorkspaceID: "w", Email: "bob@example.com"})
	if err := st.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	st.AddGroup(Group{ID: "g-eng", WorkspaceID: "w", Name: "Engineering"})

	personalID, err := st.EnsurePersonalVault("w", "alice", "v-personal-alice")
	if err != nil {
		t.Fatal(err)
	}
	crewID := "v-crew-p1"
	if _, err := st.EnsureCrewVault("w", crewID, CrewVaultRootPrefix+"p1", "Crew p1", []string{"alice"}); err != nil {
		t.Fatal(err)
	}
	projectID := "v-proj-1"
	if err := st.EnsureProjectVault("w", "alice", projectID, "Workflow/demo", "Demo"); err != nil {
		t.Fatal(err)
	}

	crewConn := Connector{ID: "c-crew-1", WorkspaceID: "w", Provider: "notion", Label: "Crew Notion", Status: StatusActive, VaultID: crewID}
	st.AddConnector(crewConn)
	st.UpsertToolSnapshot(ToolSnapshot{
		WorkspaceID:         "w",
		ConnectorID:         crewConn.ID,
		PublicName:          "notion_crew__search",
		UpstreamName:        "search",
		Status:              StatusActive,
		Fingerprint:         "fp1",
		ApprovedFingerprint: "fp1",
	})
	if err := st.VaultAttachConnector("alice", crewID, crewConn.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.VaultAddSecret("alice", crewID, "VLT_CREW__TOKEN"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnableSQLWorkspace("w"); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// 1. An ordinary SQL mutation on a company group must NOT wipe vault metadata (Kind, ProjectRoot, Owners).
	if _, err := st.MutateSQL(ctx, "w", "admin", SQLMutation{
		SQL:    "INSERT INTO group_members (group_id, user_id) VALUES (?, ?)",
		Params: []any{"g-eng", "bob"},
	}); err != nil {
		t.Fatalf("ordinary SQL mutation failed: %v", err)
	}
	if !st.VaultOwnedBy("alice", personalID) {
		t.Fatal("ordinary SQL mutation wiped personal vault ownership")
	}
	if !st.IsCrewVault(crewID) || !st.VaultOwnedBy("alice", crewID) {
		t.Fatal("ordinary SQL mutation wiped Crew vault kind/root/ownership")
	}
	if !st.VaultOwnedBy("alice", projectID) {
		t.Fatal("ordinary SQL mutation wiped project vault ownership")
	}
	if secrets := st.ListSecrets("w", "alice", "", false); len(secrets) != 0 {
		t.Fatalf("Crew vault secret leaked into account-wide ListSecrets after SQL mutation: %+v", secrets)
	}

	// 2. Additive schema DDL (CREATE UNIQUE INDEX / CREATE TABLE) must be rejected so fixed tables cannot be bricked.
	if _, err := st.MutateSQL(ctx, "w", "admin", SQLMutation{
		SQL: "CREATE UNIQUE INDEX IF NOT EXISTS idx_brick ON groups(workspace_id)",
	}); err == nil {
		t.Fatal("MutateSQL accepted CREATE UNIQUE INDEX on groups")
	}

	// 3. Direct SQL tampering with vault groups, memberships, or vault tool grants must be rejected.
	for name, sqlStmt := range map[string]SQLMutation{
		"delete vault group": {
			SQL:    "DELETE FROM groups WHERE id = ?",
			Params: []any{crewID},
		},
		"rename vault group": {
			SQL:    "UPDATE groups SET name = 'Tampered' WHERE id = ?",
			Params: []any{personalID},
		},
		"add member to vault group": {
			SQL:    "INSERT INTO group_members (group_id, user_id) VALUES (?, ?)",
			Params: []any{crewID, "bob"},
		},
		"grant crew vault tool to company group": {
			SQL:    "INSERT INTO group_tool_grants (group_id, public_name) VALUES (?, ?)",
			Params: []any{"g-eng", "notion_crew__search"},
		},
	} {
		if _, err := st.MutateSQL(ctx, "w", "admin", sqlStmt); err == nil {
			t.Fatalf("%s unexpectedly succeeded via MutateSQL", name)
		}
	}
	if st.PersistenceError() != nil {
		t.Fatalf("persistence latched error after rejected mutations: %v", st.PersistenceError())
	}
}

func TestEnsureProjectVaultRejectsCrewVaultRoot(t *testing.T) {
	st := NewMemoryStore()
	st.AddWorkspace(Workspace{ID: "w"})
	st.AddUser(User{ID: "alice", WorkspaceID: "w"})
	st.AddUser(User{ID: "bob", WorkspaceID: "w"})

	crewID := "v-crew-p1"
	if _, err := st.EnsureCrewVault("w", crewID, CrewVaultRootPrefix+"p1", "Crew p1", []string{"alice"}); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureProjectVault("w", "bob", crewID, CrewVaultRootPrefix+"p1", "Hijacked"); err == nil {
		t.Fatal("EnsureProjectVault accepted a Crew vault root prefix")
	} else if !strings.Contains(strings.ToLower(err.Error()), "crew") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !st.VaultOwnedBy("alice", crewID) || st.VaultOwnedBy("bob", crewID) {
		t.Fatal("Crew vault ownership changed after rejected EnsureProjectVault")
	}
}
