package store

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/pii"
)

func TestDeleteConnectorDoesNotTransferPolicyToReusedNamespace(t *testing.T) {
	s := NewMemoryStore()
	const connectorID = "old"
	const toolName = "notes__read"
	s.AddConnector(Connector{ID: connectorID, WorkspaceID: "w", Provider: "notes"})
	s.UpsertToolSnapshot(ToolSnapshot{ConnectorID: connectorID, WorkspaceID: "w", PublicName: toolName, Fingerprint: "old", Status: StatusActive})
	s.AddUser(User{ID: "u", WorkspaceID: "w"})
	s.AddGroup(Group{ID: "g", WorkspaceID: "w"})
	s.AddMember("g", "u")
	s.AddGrant(Grant{UserID: "u", PublicName: toolName})
	s.AddGroupGrant(GroupGrant{GroupID: "g", PublicName: toolName})
	s.AddGroupServerGrant("g", connectorID)
	s.PutPIIRule(pii.Rule{ID: "tool-rule", WorkspaceID: "w", PublicName: toolName})
	s.PutPIIRule(pii.Rule{ID: "server-rule", WorkspaceID: "w", ConnectorID: connectorID})
	s.PutPIIRule(pii.Rule{ID: "global-rule", WorkspaceID: "w"})
	s.AddPIIReview(PIIReview{ID: "review", WorkspaceID: "w", ConnectorID: connectorID, PublicName: toolName})

	s.DeleteConnector(connectorID)
	s.AddConnector(Connector{ID: "new", WorkspaceID: "w", Provider: "notes"})
	s.UpsertToolSnapshot(ToolSnapshot{ConnectorID: "new", WorkspaceID: "w", PublicName: toolName, Fingerprint: "new", Status: StatusActive})
	if s.HasGrant("u", toolName) || s.HasGroupGrant("u", toolName) || s.HasServerGrant("u", connectorID) {
		t.Fatal("deleted connector policy survived namespace reuse")
	}
	if got := s.GroupGrantsFor("g"); len(got) != 0 {
		t.Fatalf("stale group tool grants: %v", got)
	}
	if got := s.ListPIIRules("w"); len(got) != 1 || got[0].ID != "global-rule" {
		t.Fatalf("connector PII rules survived: %+v", got)
	}
	if got := s.ListPIIReviews("w"); len(got) != 0 {
		t.Fatalf("connector PII review survived: %+v", got)
	}
}
