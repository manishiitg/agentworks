package server

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Canonical routing and private-state identity are different: preserving an
// encrypted namespace must never reopen the old filesystem route or share it.
func TestCanonicalCrewKeepsPrivateBindingsAndAudience(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	root := "Crew/example-12345678"
	old := workspaceref.PhysicalPathOf("owner", workspaceref.CodeProjectsRoot, "example-12345678")
	rec := projectOwnerRecord{Product: crewProfileID, Folder: "example-12345678", ProjectID: "stable-project-id", OwnerID: "owner", Shared: true, CanonicalRoot: root, BindingRoot: old, Private: true, AudienceConfigured: true}
	if err := defaultProjectOwners().Register(rec); err != nil {
		t.Fatal(err)
	}
	if got := projectBindingRoot(root); got != old {
		t.Fatalf("private namespace changed: %q", got)
	}
	if _, ok := resolveCrewPath(context.Background(), "owner", old); ok {
		t.Fatal("retired filesystem path accepted")
	}
	if _, _, ok := projectProductForPath(old); ok {
		t.Fatal("retired path became a product")
	}
	if crewAdminInspectionAllowed(root) {
		t.Fatal("merge created an admin inspection grant")
	}
	if crewSharedWith(root, "other") {
		t.Fatal("private migrated Crew became shared")
	}
	if !crewProjectOwnedByCallerStrict("owner", root) || crewProjectOwnedByCallerStrict("other", root) {
		t.Fatal("registered owner changed")
	}
	if len(projectProducts) != 1 || projectProducts[0].ProfileID != crewProfileID {
		t.Fatal("duplicate project product remains")
	}
}
