package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestVaultSecretGrantsPersistAndRevoke(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.sqlite")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.AddWorkspace(Workspace{ID: "w"})
	s.AddWorkspace(Workspace{ID: "foreign"})
	s.AddUser(User{ID: "alice", WorkspaceID: "w"})
	s.AddUser(User{ID: "bob", WorkspaceID: "w"})
	s.AddUser(User{ID: "other", WorkspaceID: "foreign"})
	s.AddGroup(Group{ID: "finance", WorkspaceID: "w", Name: "Finance"})
	s.AddGroup(Group{ID: "foreign", WorkspaceID: "foreign", Name: "Other"})
	s.AddMember("finance", "alice")
	if err = s.RegisterSecrets("w", []SecretResource{{Name: "FINANCE_KEY", Managed: true}}); err != nil {
		t.Fatal(err)
	}
	if len(s.ListSecrets("w", "alice", "", false)) != 0 {
		t.Fatal("registration auto-granted")
	}
	if err = s.SetSecretGrant("w", "finance", "FINANCE_KEY", "admin", true); err != nil {
		t.Fatal(err)
	}
	if len(s.ListSecrets("w", "alice", "", false)) != 1 || len(s.ListSecrets("w", "bob", "", false)) != 0 {
		t.Fatal("user isolation failed")
	}
	if err = s.SetSecretGrant("w", "foreign", "FINANCE_KEY", "admin", true); err == nil {
		t.Fatal("cross-workspace grant allowed")
	}
	s.AddMember("finance", "other") // Even corrupt cross-workspace membership must fail closed.
	if len(s.ListSecrets("w", "other", "", false)) != 0 {
		t.Fatal("foreign user admitted")
	}
	if err = s.EnableSQLWorkspace("w"); err != nil {
		t.Fatal(err)
	}
	q, err := s.QuerySQL(context.Background(), "w", SQLQuery{SQL: "SELECT secret_name FROM group_secret_grants"})
	if err != nil || len(q.Rows) != 1 {
		t.Fatalf("projection missing: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.ListSecrets("w", "alice", "", false)) != 1 {
		t.Fatal("grant lost after restart")
	}
	s.RemoveMember("finance", "alice")
	if len(s.ListSecrets("w", "alice", "", false)) != 0 {
		t.Fatal("revocation ignored")
	}
	s.AddMember("finance", "alice")
	if err = s.DeleteSecret("w", "FINANCE_KEY", "admin"); err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterSecrets("w", []SecretResource{{Name: "FINANCE_KEY", Managed: true}}); err != nil {
		t.Fatal(err)
	}
	if len(s.ListSecrets("w", "alice", "", false)) != 0 {
		t.Fatal("recreated secret inherited deleted grants")
	}
}
