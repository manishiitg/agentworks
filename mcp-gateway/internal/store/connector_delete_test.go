package store

import (
	"testing"
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

	s.DeleteConnector(connectorID)
	s.AddConnector(Connector{ID: "new", WorkspaceID: "w", Provider: "notes"})
	s.UpsertToolSnapshot(ToolSnapshot{ConnectorID: "new", WorkspaceID: "w", PublicName: toolName, Fingerprint: "new", Status: StatusActive})
	if s.HasGrant("u", toolName) || s.HasGroupGrant("u", toolName) || s.HasServerGrant("u", connectorID) {
		t.Fatal("deleted connector policy survived namespace reuse")
	}
	if got := s.GroupGrantsFor("g"); len(got) != 0 {
		t.Fatalf("stale group tool grants: %v", got)
	}

}
