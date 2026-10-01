package store

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
)

func TestDeletingConnectorKeepsOtherPackageRules(t *testing.T) {
	s := NewMemoryStore()
	s.AddWorkspace(Workspace{ID: "w"})
	s.AddConnector(Connector{ID: "one", WorkspaceID: "w", Status: StatusActive})
	s.AddConnector(Connector{ID: "two", WorkspaceID: "w", Status: StatusActive})
	s.UpsertToolSnapshot(ToolSnapshot{WorkspaceID: "w", ConnectorID: "one", PublicName: "a__read", Fingerprint: "v1"})
	s.UpsertToolSnapshot(ToolSnapshot{WorkspaceID: "w", ConnectorID: "two", PublicName: "b__read", Fingerprint: "v1"})
	p, _ := s.SavePackageDraft(access.Package{ID: "p", WorkspaceID: "w", GroupID: "g", Rules: []access.ToolRule{{PublicName: "a__read"}, {PublicName: "b__read"}}}, 0)
	s.PublishPackage("w", p.ID, p.Version)
	s.DeleteConnector("one")
	packages, governed := s.PolicyForTool("w", "b__read")
	if !governed || len(packages) != 1 || len(packages[0].Rules) != 1 || packages[0].Rules[0].PublicName != "b__read" {
		t.Fatalf("other connector lost policy: %+v governed=%v", packages, governed)
	}
	if _, governed := s.PolicyForTool("w", "a__read"); governed {
		t.Fatal("deleted connector left governed name")
	}
}
