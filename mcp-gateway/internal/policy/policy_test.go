package policy

import (
	"errors"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func TestAuthorizeRequiresCurrentWorkspacePrincipal(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	tool := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "p__t", Fingerprint: "v1"})
	if _, ok := s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version); !ok {
		t.Fatal("approve tool")
	}
	s.AddGrant(store.Grant{UserID: "u", PublicName: "p__t"})
	if _, err := Authorize(s, auth.Identity{UserID: "u", WorkspaceID: "w"}, "p__t"); !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("orphaned grant allowed: %v", err)
	}
	s.AddUser(store.User{ID: "u", WorkspaceID: "w"})
	if _, err := Authorize(s, auth.Identity{UserID: "u", WorkspaceID: "w"}, "p__t"); err != nil {
		t.Fatalf("current user denied: %v", err)
	}
	s.AddGroupServerGrant("deleted", "c")
	if _, err := Authorize(s, auth.Identity{WorkspaceID: "w", ViaGroup: "deleted"}, "p__t"); !errors.Is(err, ErrUnknownGroup) {
		t.Fatalf("orphaned group key allowed: %v", err)
	}
}
