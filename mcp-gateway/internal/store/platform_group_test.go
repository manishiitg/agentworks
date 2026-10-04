package store

import (
	"context"
	"testing"
)

func TestPlatformGroupInstallationIsIdempotentAndMembershipAutomatic(t *testing.T) {
	s, path := sqlFixture(t)
	if err := s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	id := PlatformGroupID("w")
	group, ok := s.GetGroup(id)
	if !ok || !group.BuiltIn || group.Name != "Platform" || len(s.MembersOf(id)) != 1 {
		t.Fatal(group)
	}
	if len(s.GroupGrantsFor(id)) != 0 {
		t.Fatal("installation wrote per-tool grants")
	}
	s.AddWorkspace(Workspace{ID: "other"})
	if err := s.EnsurePlatformGroup("other"); err != nil {
		t.Fatal(err)
	}
	s.AddUser(User{ID: "outsider", WorkspaceID: "other"})
	if PlatformGroupID("other") == id || len(s.MembersOf(id)) != 1 {
		t.Fatal("workspace membership crossed")
	}
	if err := s.EnsurePlatformUser("w", "outsider"); err == nil {
		t.Fatal("foreign identity moved")
	}
	if err := s.EnsurePlatformUser("w", "new-user"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterSecrets("w", []SecretResource{{Name: "TEAM_KEY", Managed: true}}); err != nil {
		t.Fatal(err)
	}
	if len(s.ListSecrets("w", "new-user", "", false)) != 1 {
		t.Fatal("new shared secret was not granted to Platform")
	}
	s.AddGroupGrant(GroupGrant{GroupID: id, PublicName: "c__read"})
	s.AddUser(User{ID: "later", WorkspaceID: "w"})
	s.RemoveMember(id, "later")
	if !s.HasGroupGrant("later", "c__read") || len(s.ListSecrets("w", "later", "", false)) != 1 {
		t.Fatal("new user did not inherit assigned resources")
	}
	if len(s.ListSecrets("other", "outsider", "", false)) != 0 {
		t.Fatal("foreign secret exposed")
	}
	// Upgrade the original display name without replacing the group or grants.
	s.mu.Lock()
	legacy := s.groups[id]
	legacy.Name = "Everyone"
	legacy.Description = "Company shared access"
	s.groups[id] = legacy
	s.persistUnlock()
	if err := s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	updated, _ := s.GetGroup(id)
	if updated.Name != "Platform" || updated.Description != "Company shared access" {
		t.Fatal("name migration lost group metadata", updated)
	}
	if len(s.ListGroups("w")) != 2 || !s.ListGroups("w")[0].BuiltIn {
		t.Fatal("reinstall duplicated or reordered default group")
	}
	s.Close()
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableSQLWorkspace("w"); err != nil {
		t.Fatal(err)
	}
	if err = s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	if !s.HasGroupGrant("later", "c__read") || len(s.ListSecrets("w", "later", "", false)) != 1 {
		t.Fatal("restart lost default grants")
	}
	if err = s.SetSecretGrant("w", id, "TEAM_KEY", "admin", false); err != nil {
		t.Fatal(err)
	}
	if len(s.ListSecrets("w", "later", "", false)) != 0 {
		t.Fatal("secret revoke did not apply")
	}
}

func TestPlatformGroupCannotBeDeletedRenamedOrManuallyRemovedBySQL(t *testing.T) {
	s, _ := sqlFixture(t)
	defer s.Close()
	if err := s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	id := PlatformGroupID("w")
	for _, sql := range []string{`DELETE FROM groups WHERE id=?`, `UPDATE groups SET name='Hidden' WHERE id=?`, `DELETE FROM group_members WHERE group_id=?`} {
		if _, err := s.MutateSQL(context.Background(), "w", "admin", SQLMutation{SQL: sql, Params: []any{id}}); err == nil {
			t.Fatalf("protected group mutation accepted: %s", sql)
		}
	}
	if _, err := s.MutateSQL(context.Background(), "w", "admin", SQLMutation{SQL: `UPDATE groups SET description='Company shared access' WHERE id=?`, Params: []any{id}}); err != nil {
		t.Fatal(err)
	}
	group, ok := s.GetGroup(id)
	if !ok || !group.BuiltIn || group.Description != "Company shared access" || len(s.MembersOf(id)) != 1 {
		t.Fatal(group)
	}
}
