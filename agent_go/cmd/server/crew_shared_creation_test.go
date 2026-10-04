package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The switch decides where NEW Crews are created; it defaults to off, so shipping the code changes nothing.
func TestCrewSharedRootSwitchDefaultsOff(t *testing.T) {
	t.Setenv("AGENTWORKS_CREW_SHARED_ROOT", "")
	if crewSharedRootEnabled() {
		t.Fatal("the shared-root switch is on by default")
	}
	for _, v := range []string{"off", "0", "true", "yes", "1"} {
		t.Setenv("AGENTWORKS_CREW_SHARED_ROOT", v)
		if crewSharedRootEnabled() {
			t.Fatalf("AGENTWORKS_CREW_SHARED_ROOT=%q turned the switch on; only \"on\" does", v)
		}
	}
	t.Setenv("AGENTWORKS_CREW_SHARED_ROOT", "ON")
	if !crewSharedRootEnabled() {
		t.Fatal("\"on\" does not turn the switch on")
	}
	if got := crewCreationRoot("work", "_users/a/Chats/Work/projects"); got != "Crew" {
		t.Fatalf("Crew creation root with the switch on = %q", got)
	}
	if got := crewCreationRoot("code", "_users/a/Chats/Code/projects"); got != "_users/a/Chats/Code/projects" {
		t.Fatalf("Code creation root moved with the Crew switch: %q", got)
	}
}

func TestCreateCrewProjectLocationFollowsTheSwitch(t *testing.T) {
	registry := withProjectOwnerRegistry(t)
	t.Run("off: in the owner's tree, as today", func(t *testing.T) {
		t.Setenv("AGENTWORKS_CREW_SHARED_ROOT", "")
		svc, mock, ctx := newCrewCreationTestEnv(t)
		created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer", Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review.", IdempotencyKey: "off-1"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(created.WorkspacePath, "_users/owner/Chats/Work/projects/release-reviewer-") {
			t.Fatalf("workspace = %q", created.WorkspacePath)
		}
		if _, ok := mock.files[created.WorkspacePath+"/product.json"]; !ok {
			t.Fatal("product.json was not written")
		}
		if rec, ok := registry.Lookup("work", filepath.Base(created.WorkspacePath)); !ok || rec.OwnerID != "owner" || rec.Shared {
			t.Fatalf("registry entry = %+v ok=%v", rec, ok)
		}
	})
	t.Run("on: at Crew/<folder>, registered to its creator", func(t *testing.T) {
		t.Setenv("AGENTWORKS_CREW_SHARED_ROOT", "on")
		svc, mock, ctx := newCrewCreationTestEnv(t)
		created, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer Two", Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review.", IdempotencyKey: "on-1"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(created.WorkspacePath, "Crew/release-reviewer-two-") {
			t.Fatalf("workspace = %q", created.WorkspacePath)
		}
		folder := filepath.Base(created.WorkspacePath)
		if rec, ok := registry.Lookup("work", folder); !ok || rec.OwnerID != "owner" || !rec.Shared {
			t.Fatalf("registry entry = %+v ok=%v", rec, ok)
		}
		raw := mock.files[created.WorkspacePath+"/product.json"]
		if !strings.Contains(raw, `"owner_id": "owner"`) {
			t.Fatalf("product.json lacks the informational owner: %s", raw)
		}
		// The creator is its owner; the workflow's attachment names the new root.
		if owner, ok := crewProjectOwnerID(created.WorkspacePath); !ok || owner != "owner" {
			t.Fatalf("owner of the new Crew = %q ok=%v", owner, ok)
		}
		manifest, _, err := ReadWorkflowManifest(ctx, "Workflow/build")
		if err != nil || manifest == nil {
			t.Fatal(err)
		}
		attached := false
		for _, attachment := range manifest.CrewAttachments {
			if attachment.CrewWorkspacePath == created.WorkspacePath {
				attached = true
			}
		}
		if !attached {
			t.Fatalf("the workflow's attachments do not name %s: %+v", created.WorkspacePath, manifest.CrewAttachments)
		}
		// Re-entry with the same key adopts the same Crew.
		again, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Release Reviewer Two", Role: "Reviewer", Purpose: "Own release quality", StepInstruction: "Review.", IdempotencyKey: "on-1"})
		if err != nil || again.CrewID != created.CrewID || again.WorkspacePath != created.WorkspacePath {
			t.Fatalf("re-entry = %+v err=%v", again, err)
		}
	})
}

func TestEnsureSharedCrewRootIsTraversableNotListable(t *testing.T) {
	docs := t.TempDir()
	if err := ensureSharedCrewRoot(docs); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(docs, "Crew"))
	if err != nil || info.Mode().Perm() != 0o711 {
		t.Fatalf("Crew/ mode = %v err=%v, want 0711", info.Mode(), err)
	}
	// Idempotent, and it repairs a lax mode.
	if err := os.Chmod(filepath.Join(docs, "Crew"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureSharedCrewRoot(docs); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(docs, "Crew")); info.Mode().Perm() != 0o711 {
		t.Fatalf("Crew/ mode after repair = %v", info.Mode())
	}
}
