package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// More stored-reference kinds after a move: a place connection's root, the browser's profile key, a workflow's attached
// Crew, a cost row, and what an unmoved Crew keeps.
func TestMovedCrewStoredReferencesPlacesBrowserAttachmentsAndCosts(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	oldPhysical := workspaceref.PhysicalPath(fixtureUserA, workspaceref.CrewProjectsRoot, fixtureCrewFolder)
	oldLogical := workspaceref.CrewProjectsRoot + "/" + fixtureCrewFolder
	moved := f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: fixtureCrewFolder, OwnerID: fixtureUserA, Shared: true, Aliases: []string{oldPhysical}}); err != nil {
		t.Fatal(err)
	}
	resetCrewLocationCaches()

	t.Run("a place connection recorded under the old root belongs to the Crew's new root", func(t *testing.T) {
		for _, spelling := range []string{oldPhysical, moved} {
			if got := cleanAttachRoot(spelling); got != moved {
				t.Errorf("cleanAttachRoot(%q) = %q, want %q", spelling, got, moved)
			}
		}
		if got := attachRootForCaller(fixtureUserA, oldLogical); got != moved {
			t.Errorf("attachRootForCaller(old logical) = %q", got)
		}
		// The stored index is read with the old key folded into the new one, so connections added before the move are the
		// Crew's now.
		withMCPConnectionsRoot(t)
		if path, err := placeMCPAttachmentsPath(); err == nil {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		placeMCPMu.Lock()
		defer placeMCPMu.Unlock()
		if err := writePlaceMCPAttachmentsLocked(map[string][]placeMCPAttachment{
			oldPhysical:      {{Owner: fixtureUserA, Server: "github"}},
			moved:            {{Owner: fixtureUserA, Server: "notion"}},
			"Workflow/other": {{Owner: fixtureUserA, Server: "slack"}},
		}); err != nil {
			t.Fatal(err)
		}
		all, err := readPlaceMCPAttachmentsLocked()
		if err != nil {
			t.Fatal(err)
		}
		if len(all[oldPhysical]) != 0 || len(all[moved]) != 2 || len(all["Workflow/other"]) != 1 {
			t.Errorf("attachments after the move = %v", all)
		}
	})

	t.Run("the browser profile keeps the key the Crew always had", func(t *testing.T) {
		for _, spelling := range []string{oldPhysical, oldLogical, moved} {
			if got := browserProjectKey(fixtureUserA, spelling); got != oldPhysical {
				t.Errorf("browserProjectKey(%q) = %q, want the original %q (a new key logs the Crew out of every site)", spelling, got, oldPhysical)
			}
		}
		// A Crew created at the shared root has no old key.
		if got := browserProjectKey(fixtureUserA, "Crew/fresh-77777777"); got != "Crew/fresh-77777777" {
			t.Errorf("a new shared Crew's browser key = %q", got)
		}
		a, b := browserSessionForWorkspace(fixtureUserA, oldPhysical), browserSessionForWorkspace(fixtureUserA, moved)
		if a == "" || a != b {
			t.Errorf("browser sessions differ across the move: %q vs %q", a, b)
		}
	})

	t.Run("a workflow's attached Crew keeps resolving, and compares equal to the new root", func(t *testing.T) {
		attachment := workflowtypes.CrewAttachment{Alias: "alpha", CrewProfileID: "work", CrewProjectID: fixtureCrewID, CrewWorkspacePath: oldPhysical}
		if err := workflowtypes.ValidateCrewAttachmentBinding(attachment); err != nil {
			t.Fatal(err)
		}
		attachment.CrewWorkspacePath = moved
		if err := workflowtypes.ValidateCrewAttachmentBinding(attachment); err != nil {
			t.Fatalf("an attachment of a Crew at the shared root is refused: %v", err)
		}
		attachment.CrewWorkspacePath = "Crew/.migrating/x"
		if err := workflowtypes.ValidateCrewAttachmentBinding(attachment); err == nil {
			t.Fatal("a hidden shared-root entry was accepted as a Crew")
		}
		old := workflowtypes.CrewAttachment{Alias: "alpha", CrewProjectID: fixtureCrewID, CrewWorkspacePath: oldPhysical}
		if got := workflowtypes.CanonicalCrewAttachmentRoot(old.CrewWorkspacePath); got != moved {
			t.Errorf("the old attachment root folds to %q, want %q", got, moved)
		}
		if got := workflowtypes.CanonicalCrewAttachmentRoot(oldLogical); got != moved {
			t.Errorf("the logical attachment root folds to %q", got)
		}
		// The read path of an attached Crew from a workflow lands in the new folder.
		if got, ok := workflowtypes.ResolveCrewAttachmentPath([]workflowtypes.CrewAttachment{old}, "alpha/code/notes.md"); !ok || got != moved+"/code/notes.md" {
			t.Errorf("ResolveCrewAttachmentPath = %q %v", got, ok)
		}
		if env := workflowtypes.CrewAttachmentEnvKeys([]workflowtypes.CrewAttachment{old}); env["WORKFLOW_CREW_ALPHA"] != moved {
			t.Errorf("the attachment's environment names %q", env["WORKFLOW_CREW_ALPHA"])
		}
		// Live validation reads the folder from disk: the OLD stored path validates because it folds to the new folder.
		if err := os.MkdirAll(filepath.Join(f.Docs, "Crew", fixtureCrewFolder), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := workflowtypes.ValidateCrewAttachmentRoot(old, f.Docs); err != nil {
			t.Errorf("an old attachment of a moved Crew no longer validates: %v", err)
		}
		other := workflowtypes.CrewAttachment{Alias: "other", CrewProjectID: "x", CrewWorkspacePath: workspaceref.PhysicalPath(fixtureUserA, workspaceref.CrewProjectsRoot, "never-moved-11112222")}
		if got := workflowtypes.CanonicalCrewAttachmentRoot(other.CrewWorkspacePath); got != other.CrewWorkspacePath {
			t.Errorf("an unmoved Crew's attachment root changed to %q", got)
		}
	})

	t.Run("cost rows recorded before the move fold into the Crew's row", func(t *testing.T) {
		for _, workflowID := range []string{oldPhysical + "/builder", oldLogical, moved, moved + "/code"} {
			root, kind, name, owner := costOverviewRoot(workflowID)
			if root != moved || kind != costOverviewKindCrew || name != fixtureCrewFolder || owner != fixtureUserA {
				t.Errorf("costOverviewRoot(%q) = %q %q %q %q", workflowID, root, kind, name, owner)
			}
		}
		root, kind, _, owner := costOverviewRoot(workspaceref.PhysicalPath(fixtureUserA, workspaceref.CrewProjectsRoot, "never-moved-11112222"))
		if !strings.HasPrefix(root, "_users/") || kind != costOverviewKindCrew || owner != fixtureUserA {
			t.Errorf("an unmoved Crew's cost row = %q %q %q", root, kind, owner)
		}
	})
}
