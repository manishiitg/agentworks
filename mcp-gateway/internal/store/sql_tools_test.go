package store

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
	"path/filepath"
	"strings"
	"testing"
)

func sqlFixture(t *testing.T) (*MemoryStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.AddWorkspace(Workspace{ID: "w"})
	s.AddUser(User{ID: "u", WorkspaceID: "w", Email: "u@example.test"})
	s.AddGroup(Group{ID: "g", WorkspaceID: "w", Name: "Readers"})
	s.AddConnector(Connector{ID: "c", WorkspaceID: "w", Status: StatusActive})
	tool := s.UpsertToolSnapshot(ToolSnapshot{PublicName: "c__read", UpstreamName: "read", WorkspaceID: "w", ConnectorID: "c", Fingerprint: "f", InputSchema: []byte(`{"type":"object","properties":{"project":{"type":"string"}}}`)})
	s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	if err = s.EnableSQLWorkspace("w"); err != nil {
		t.Fatal(err)
	}
	return s, path
}
func TestSQLMutationsReachRuntimeAndSurviveRestart(t *testing.T) {
	s, path := sqlFixture(t)
	ctx := context.Background()
	out, err := s.MutateSQL(ctx, "w", "admin", SQLMutation{Statements: []SQLStatement{{SQL: `UPDATE groups SET name=? WHERE id=?`, Params: []any{"SQL Readers", "g"}}, {SQL: `INSERT INTO group_members VALUES(?,?)`, Params: []any{"g", "u"}}, {SQL: `INSERT INTO group_tool_grants VALUES(?,?)`, Params: []any{"g", "c__read"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalRowsAffected != 3 {
		t.Fatal(out)
	}
	g, _ := s.GetGroup("g")
	if g.Name != "SQL Readers" || !s.HasGroupGrant("u", "c__read") {
		t.Fatal("SQL changes did not reach store")
	}
	q, err := s.QuerySQL(ctx, "w", SQLQuery{SQL: `SELECT name FROM groups WHERE id=?`, Params: []any{"g"}})
	if err != nil || q.Rows[0]["name"] != "SQL Readers" {
		t.Fatal(q, err)
	}
	s.Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableSQLWorkspace("w"); err != nil {
		t.Fatal(err)
	}
	if !s.HasGroupGrant("u", "c__read") || len(s.ListPolicyEvents("w")) != 1 {
		t.Fatal("SQL mutation lost on restart")
	}
	if _, err = s.MutateSQL(ctx, "w", "admin", SQLMutation{SQL: `DELETE FROM group_tool_grants WHERE group_id=?`, Params: []any{"g"}}); err != nil {
		t.Fatal(err)
	}
	if s.HasGroupGrant("u", "c__read") {
		t.Fatal("SQL revoke not applied")
	}
}
func TestSQLBatchesRollbackAndProtectedTablesStayProtected(t *testing.T) {
	s, _ := sqlFixture(t)
	defer s.Close()
	ctx := context.Background()
	for _, req := range []SQLMutation{
		{Statements: []SQLStatement{{SQL: `UPDATE groups SET name='bad' WHERE id='g'`}, {SQL: `INSERT INTO group_members VALUES('g','unknown')`}}},
		{SQL: `UPDATE tools SET status='active'`}, {SQL: `UPDATE gateway_configuration SET revision=0`}, {SQL: `DELETE FROM published_permissions`}, {SQL: `ATTACH DATABASE '/tmp/anything' AS other`}, {SQL: `UPDATE groups SET name='bad';DELETE FROM groups`}, {SQL: `WITH "UPDATE" AS (SELECT 1) UPDATE gateway_configuration SET revision=0`}, {SQL: `INSERT INTO groups(id,workspace_id,name) VALUES('other','foreign','Foreign')`},
	} {
		if _, err := s.MutateSQL(ctx, "w", "admin", req); err == nil {
			t.Fatalf("unsafe mutation accepted: %+v", req)
		}
		g, _ := s.GetGroup("g")
		if g.Name != "Readers" {
			t.Fatal("failed batch partially applied")
		}
	}
	if s.PersistenceError() != nil {
		t.Fatal("validation error latched storage fault")
	}
	if _, err := s.QuerySQL(ctx, "w", SQLQuery{SQL: `WITH x AS (SELECT 1) DELETE FROM groups`}); err == nil {
		t.Fatal("read tool mutated")
	}
	if _, err := s.QuerySQL(ctx, "foreign", SQLQuery{SQL: `SELECT * FROM groups`}); err == nil {
		t.Fatal("cross-workspace query")
	}
	q, err := s.QuerySQL(ctx, "w", SQLQuery{Action: "describe", Table: "groups"})
	if err != nil || len(q.Rows) != 4 {
		t.Fatal(q, err)
	}
}
func TestSQLCannotCreateDraftsOrBypassSavedPermissions(t *testing.T) {
	s, _ := sqlFixture(t)
	defer s.Close()
	ctx := context.Background()
	rules, _ := json.Marshal([]access.ToolRule{{PublicName: "c__read", Fingerprint: "f", Conditions: []access.Condition{{Path: "/project", Op: "equals", Value: "one"}}}})
	if _, err := s.MutateSQL(ctx, "w", "admin", SQLMutation{SQL: `INSERT INTO permission_drafts(id,workspace_id,group_id,name,rules_json) VALUES(?,?,?,?,?)`, Params: []any{"p", "w", "g", "One project", string(rules)}}); err == nil {
		t.Fatal("SQL created a draft")
	}
	_, err := s.SaveAccessPackage(access.Package{ID: "p", WorkspaceID: "w", GroupID: "g", Name: "One project", Rules: []access.ToolRule{{PublicName: "c__read", Fingerprint: "f", Conditions: []access.Condition{{Path: "/project", Op: "equals", Value: "one"}}}}}, 0, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MutateSQL(ctx, "w", "admin", SQLMutation{SQL: `INSERT INTO group_tool_grants VALUES('g','c__read')`}); err == nil {
		t.Fatal("SQL bypassed governed grant")
	}
	if _, err := s.MutateSQL(ctx, "w", "admin", SQLMutation{SQL: `DELETE FROM groups WHERE id='g'`}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GetGroup("g"); ok {
		t.Fatal("group not deleted")
	}
	packages, governed := s.PolicyForTool("w", "c__read")
	if !governed || len(packages) != 0 {
		t.Fatal("group removal lost denial")
	}
	policies := s.ListPackages("w")
	if len(policies) != 1 || policies[0].Status != "revoked" {
		t.Fatal("group removal lost revoked policy history")
	}
}
func TestExternalSQLWriteDeniesUntilRestart(t *testing.T) {
	s, _ := sqlFixture(t)
	defer s.Close()
	if _, err := s.persistence.db.Exec(`UPDATE groups SET name='outside' WHERE id='g'`); err != nil {
		t.Fatal(err)
	}
	if s.PersistenceError() == nil {
		t.Fatal("external mutation did not invalidate authorization")
	}
}

func TestGroupDescriptionMigratesAndSurvivesSQLAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.AddWorkspace(Workspace{ID: "w"})
	s.AddGroup(Group{ID: "g", WorkspaceID: "w", Name: "Readers"})
	// Simulate the pre-description public projection of an existing workspace.
	if _, err = s.persistence.db.Exec(`CREATE TABLE groups(id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL,name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err = s.EnableSQLWorkspace("w"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = s.MutateSQL(ctx, "w", "admin", SQLMutation{SQL: `UPDATE groups SET description=? WHERE id=?`, Params: []any{"Read-only tools for support", "g"}}); err != nil {
		t.Fatal(err)
	}
	group, _ := s.GetGroup("g")
	if group.Description != "Read-only tools for support" || group.Name != "Readers" {
		t.Fatal(group)
	}
	s.RenameGroup("g", "Support")
	if err = s.PersistenceError(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableSQLWorkspace("w"); err != nil {
		t.Fatal(err)
	}
	q, err := s.QuerySQL(ctx, "w", SQLQuery{SQL: `SELECT name,description FROM groups WHERE id='g'`})
	if err != nil || len(q.Rows) != 1 || q.Rows[0]["description"] != "Read-only tools for support" || q.Rows[0]["name"] != "Support" {
		t.Fatal(q, err)
	}
	if _, err = s.MutateSQL(ctx, "w", "admin", SQLMutation{SQL: `UPDATE groups SET description=? WHERE id='g'`, Params: []any{strings.Repeat("x", MaxGroupDescriptionLength+1)}}); err == nil {
		t.Fatal("oversized description accepted")
	}
	group, _ = s.GetGroup("g")
	if group.Description != "Read-only tools for support" {
		t.Fatal("failed update changed description")
	}
}
