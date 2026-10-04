package store

import (
	"bytes"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteConfigurationSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.sqlite")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.AddWorkspace(Workspace{ID: "w"})
	s.AddUser(User{ID: "u", WorkspaceID: "w", Email: "user@example.test"})
	s.AddGroup(Group{ID: "g", WorkspaceID: "w", Name: "Readers"})
	s.AddMember("g", "u")
	s.AddConnector(Connector{ID: "c", WorkspaceID: "w", Status: StatusActive})
	s.SetConnectorBearer("c", "private-upstream-secret")
	tool := s.UpsertToolSnapshot(ToolSnapshot{ConnectorID: "c", WorkspaceID: "w", PublicName: "files__read", Fingerprint: "f1"})
	s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	s.AddGroupGrant(GroupGrant{GroupID: "g", PublicName: tool.PublicName})
	s.AddGroupServerGrant("g", "c")
	s.AddGrant(Grant{UserID: "u", PublicName: tool.PublicName})
	s.AddAPIKey(APIKey{ID: "key", GroupID: "g", Token: "group-api-secret"})
	p, _ := s.SavePackageDraft(access.Package{ID: "p", WorkspaceID: "w", GroupID: "g", Name: "Restricted", Rules: []access.ToolRule{{PublicName: tool.PublicName, Fingerprint: tool.Fingerprint}}}, 0)
	s.PublishPackage("w", p.ID, p.Version)
	draft, _ := s.SavePackageDraft(p, p.Version)
	s.AppendPolicyEvent("w", PolicyEvent{PackageID: "p", Actor: "admin", Action: "publish"})
	if err = s.PersistenceError(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if group, ok := s.GetGroup("g"); !ok || group.Name != "Readers" {
		t.Fatal("group not restored")
	}
	if len(s.MembersOf("g")) != 1 || !s.GroupHasTool("g", tool.PublicName) || !s.GroupHasServer("g", "c") || !s.HasGrant("u", tool.PublicName) {
		t.Fatal("membership or grants lost")
	}
	if key, ok := s.APIKeyByToken("group-api-secret"); !ok || key.ID != "key" || key.Token != "" {
		t.Fatal("API key hash not restored")
	}
	if s.ConnectorBearer("c") != "private-upstream-secret" {
		t.Fatal("credential not restored")
	}
	restored, _ := s.GetTool(tool.PublicName)
	if restored.ApprovedFingerprint != "f1" {
		t.Fatal("tool approval lost")
	}
	if len(s.ListPolicyEvents("w")) != 1 {
		t.Fatal("policy history lost")
	}
	got, ok := s.GetPackageDraft("w", "p")
	if !ok || got.Version != draft.Version {
		t.Fatal("draft not restored")
	}
	s.RemoveGroupConnectorAccess("w", "g", "c", "admin")
	if err = s.PersistenceError(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.GroupHasTool("g", tool.PublicName) || s.GroupHasServer("g", "c") {
		t.Fatal("removed grants returned")
	}
	packages, governed := s.PolicyForTool("w", tool.PublicName)
	if !governed {
		t.Fatal("policy tombstone lost")
	}
	for _, p := range packages {
		if p.Status == "published" {
			t.Fatal("removed policy returned")
		}
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte("private-upstream-secret")) || bytes.Contains(data, []byte("group-api-secret")) {
		t.Fatal("database exposes secrets")
	}
	for _, file := range []string{path, path + ".key"} {
		info, _ := os.Stat(file)
		if info.Mode().Perm() != 0600 {
			t.Fatal("unsafe file permissions")
		}
	}
}

func TestSQLiteWriteFailureRollsBackAndFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.sqlite")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.AddWorkspace(Workspace{ID: "w"})
	s.Close()
	s.AddGroup(Group{ID: "bad", WorkspaceID: "w"})
	if s.PersistenceError() == nil {
		t.Fatal("failed write not reported")
	}
	if _, ok := s.GetGroup("bad"); ok {
		t.Fatal("failed change remained in memory")
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, ok := s.GetGroup("bad"); ok {
		t.Fatal("failed change persisted")
	}
}

func TestSQLiteRejectsStaleWriterAndMissingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.sqlite")
	one, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if two, err := NewSQLiteStore(path); err == nil {
		two.Close()
		t.Fatal("second store could serve stale permissions")
	}
	one.AddWorkspace(Workspace{ID: "w"})
	// The revision check also catches unexpected writes outside the gateway.
	if _, err := one.persistence.db.Exec("UPDATE gateway_configuration SET revision=revision+1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	one.AddWorkspace(Workspace{ID: "wrong"})
	if one.PersistenceError() == nil {
		t.Fatal("stale writer overwrote configuration")
	}
	one.Close()
	os.Remove(path + ".key")
	if s, err := NewSQLiteStore(path); err == nil {
		s.Close()
		t.Fatal("missing key reset configuration")
	}
}

func TestSQLiteRelocationKeepsConfigurationAndKeyOutsideChat(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "private", "gateway.sqlite")
	newPath := filepath.Join(root, "Chats", "CapLayer", "db", "gateway.sqlite")
	keyPath := oldPath + ".key"
	s, err := NewSQLiteStore(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	s.AddGroup(Group{ID: "g", WorkspaceID: "w", Name: "Readers"})
	if err := MigrateSQLiteConfiguration(oldPath, newPath, keyPath); err == nil {
		t.Fatal("relocated an active gateway")
	}
	s.Close()
	if err := MigrateSQLiteConfiguration(oldPath, newPath, keyPath); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStoreWithKey(newPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if g, ok := s.GetGroup("g"); !ok || g.Name != "Readers" {
		t.Fatal("relocation lost group")
	}
	if _, err := os.Stat(newPath + ".key"); !os.IsNotExist(err) {
		t.Fatal("private key copied into chat")
	}
}

// Old snapshots may contain removed fields. They must still restore identities,
// permissions and credentials and omit those fields on the next saved mutation.
func TestSQLiteLoadsLegacySnapshotAfterFeatureRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.sqlite")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.AddWorkspace(Workspace{ID: "w"})
	s.AddUser(User{ID: "u", WorkspaceID: "w"})
	s.AddGroup(Group{ID: "g", WorkspaceID: "w", Name: "Readers"})
	s.AddMember("g", "u")
	s.AddConnector(Connector{ID: "c", WorkspaceID: "w", Status: StatusActive})
	s.SetConnectorBearer("c", "fixture-credential")
	s.AddGroupGrant(GroupGrant{GroupID: "g", PublicName: "memory__read"})
	data, err := json.Marshal(s.durableState())
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["PII"] = map[string]any{"old-rule": map[string]any{"ID": "old-rule", "WorkspaceID": "w", "DataType": "email", "Action": "block"}}
	data, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.persistence.seal(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.persistence.db.Exec("UPDATE gateway_configuration SET payload=? WHERE id=1", sealed); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.GroupHasTool("g", "memory__read") || len(s.MembersOf("g")) != 1 || s.ConnectorBearer("c") != "fixture-credential" {
		t.Fatal("legacy migration lost active configuration")
	}
	s.AddGroup(Group{ID: "other", WorkspaceID: "w", Name: "Other"})
	if err := s.PersistenceError(); err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(s.persistence.saved, &saved); err != nil {
		t.Fatal(err)
	}
	if _, exists := saved["PII"]; exists {
		t.Fatal("removed feature configuration was saved again")
	}
}
