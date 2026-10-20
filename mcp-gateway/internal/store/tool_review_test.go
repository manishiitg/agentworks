package store

import "testing"

// Every discovered tool is active (owner, 2026-10-20); a changed definition is versioned, not held.
func TestDiscoveredToolsAreActiveAndChangesAreVersioned(t *testing.T) {
	s := NewMemoryStore()
	first := s.UpsertToolSnapshot(ToolSnapshot{WorkspaceID: "w1", ConnectorID: "c1", PublicName: "demo__run", Fingerprint: "one"})
	if first.Status != StatusActive || first.ApprovedFingerprint != "one" {
		t.Fatalf("new tool = %+v", first)
	}
	changed := s.UpsertToolSnapshot(ToolSnapshot{WorkspaceID: "w1", ConnectorID: "c1", PublicName: "demo__run", Fingerprint: "two"})
	if changed.Status != StatusActive || changed.ApprovedFingerprint != "two" || changed.Version != 2 {
		t.Fatalf("changed tool = %+v", changed)
	}
	versions := s.ListToolVersions("w1", first.PublicName)
	if len(versions) != 1 || versions[0].Fingerprint != "one" {
		t.Fatalf("history = %+v", versions)
	}
	if got := s.ListToolVersions("w2", first.PublicName); len(got) != 0 {
		t.Fatalf("history leaked across tenant: %+v", got)
	}
}
