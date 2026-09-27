package store

import "testing"

func TestToolReviewRequiresCurrentVersionAndWorkspace(t *testing.T) {
	s := NewMemoryStore()
	first := s.UpsertToolSnapshot(ToolSnapshot{WorkspaceID: "w1", ConnectorID: "c1", PublicName: "demo__run", Fingerprint: "one"})
	if first.Status != StatusQuarantined {
		t.Fatalf("new tool status = %q", first.Status)
	}
	if _, ok := s.ApproveTool("w2", first.PublicName, first.Fingerprint, first.Version); ok {
		t.Fatal("other workspace approved tool")
	}
	if _, ok := s.ApproveTool("w1", first.PublicName, "wrong", first.Version); ok {
		t.Fatal("wrong fingerprint approved")
	}
	if _, ok := s.ApproveTool("w1", first.PublicName, first.Fingerprint, first.Version); !ok {
		t.Fatal("exact definition was rejected")
	}

	changed := s.UpsertToolSnapshot(ToolSnapshot{WorkspaceID: "w1", ConnectorID: "c1", PublicName: "demo__run", Fingerprint: "two"})
	if changed.Status != StatusQuarantined || changed.Version != 2 {
		t.Fatalf("changed tool = %+v", changed)
	}
	if _, ok := s.ApproveTool("w1", first.PublicName, first.Fingerprint, first.Version); ok {
		t.Fatal("stale approval activated changed tool")
	}
	versions := s.ListToolVersions("w1", first.PublicName)
	if len(versions) != 1 || versions[0].Fingerprint != "one" {
		t.Fatalf("history = %+v", versions)
	}
	if got := s.ListToolVersions("w2", first.PublicName); len(got) != 0 {
		t.Fatalf("history leaked across tenant: %+v", got)
	}
}
