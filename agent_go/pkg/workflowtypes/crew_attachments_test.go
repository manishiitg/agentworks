package workflowtypes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCrewAttachmentAlias(t *testing.T) {
	for _, alias := range []string{"reviewer-crew", "project-a_reviewer", "r1", "a"} {
		if err := ValidateCrewAttachmentAlias(alias); err != nil {
			t.Fatalf("alias %q rejected: %v", alias, err)
		}
	}
	for _, tt := range []struct {
		alias string
		want  string
	}{
		{"", "required"},
		{"project-a", "lowercase"},
		{"project-a reviewer", "lowercase"},
		{"project-a/reviewer", "lowercase"},
		{"runs", "shadow"},
		{"planning", "shadow"},
		{"workflow.json", "shadow"},
		{strings.Repeat("a", 65), "lowercase"},
	} {
		if err := ValidateCrewAttachmentAlias(tt.alias); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("alias %q err = %v, want containing %q", tt.alias, err, tt.want)
		}
	}
}

func TestCanonicalCrewAttachmentRoot(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"_users/owner/Chats/Work/projects/project-a", "_users/owner/Chats/Work/projects/project-a"},
		{"/_users/owner/Chats/Work/projects/project-a/", "_users/owner/Chats/Work/projects/project-a"},
		{"  _users/owner/Chats/Work/projects/project-a  ", "_users/owner/Chats/Work/projects/project-a"},
		{"", ""},
	} {
		if got := CanonicalCrewAttachmentRoot(tt.in); got != tt.want {
			t.Fatalf("canonical(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if CanonicalCrewAttachmentRoot("_users/owner/Chats/Work/projects/project-a") == CanonicalCrewAttachmentRoot("_users/other/Chats/Work/projects/project-a") {
		t.Fatal("different owners canonicalized equal")
	}
}

func TestValidateCrewAttachmentBinding(t *testing.T) {
	// Binding validation is filesystem-free: a well-shaped attachment
	// passes even when its crew directory does not exist here. Server
	// paths pair it with a live crew access check instead.
	valid := CrewAttachment{Alias: "project-a", CrewProfileID: "work", CrewProjectID: "project-a", CrewWorkspacePath: "_users/owner/Chats/Work/projects/project-a"}
	if err := ValidateCrewAttachmentBinding(valid); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	// Real crew directories are <slug>-<id-prefix>, so the last segment
	// never equals a UUID project ID; the shape check must accept them.
	uuidCrew := CrewAttachment{Alias: "reviewer", CrewProfileID: "work", CrewProjectID: "550e8400-e29b-41d4-a716-446655440000", CrewWorkspacePath: "_users/owner/Chats/Work/projects/release-reviewer-550e8400"}
	if err := ValidateCrewAttachmentBinding(uuidCrew); err != nil {
		t.Fatalf("uuid crew binding rejected: %v", err)
	}
	// A same-shaped path under another crew passes the shape check: exact
	// binding is enforced server-side by comparing against the freshly
	// authorized project binding (preflight and grants), not here.
	retargeted := valid
	retargeted.CrewWorkspacePath = "_users/other/Chats/Work/projects/evil"
	if err := ValidateCrewAttachmentBinding(retargeted); err != nil {
		t.Fatalf("retargeted shape rejected: %v (shape cannot distinguish crews; equality does)", err)
	}
	for name, mutate := range map[string]func(*CrewAttachment){
		"arbitrary path":  func(a *CrewAttachment) { a.CrewWorkspacePath = "_users/owner/secrets" },
		"missing project": func(a *CrewAttachment) { a.CrewProjectID = "" },
		"missing path":    func(a *CrewAttachment) { a.CrewWorkspacePath = " " },
		"bad alias":       func(a *CrewAttachment) { a.Alias = "Bad Alias!" },
	} {
		bad := valid
		mutate(&bad)
		if err := ValidateCrewAttachmentBinding(bad); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestValidateCrewAttachmentRoot(t *testing.T) {
	root := t.TempDir()
	crewDir := filepath.Join(root, "_users", "owner", "Chats", "Work", "projects", "project-a")
	if err := os.MkdirAll(crewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	valid := CrewAttachment{Alias: "project-a", CrewProfileID: "work", CrewProjectID: "project-a", CrewWorkspacePath: "_users/owner/Chats/Work/projects/project-a"}
	if err := ValidateCrewAttachmentRoot(valid, root); err != nil {
		t.Fatalf("valid attachment rejected: %v", err)
	}
	for name, mutate := range map[string]func(*CrewAttachment){
		"retargeted crew": func(a *CrewAttachment) { a.CrewWorkspacePath = "_users/other/Chats/Work/projects/evil" },
		"arbitrary path":  func(a *CrewAttachment) { a.CrewWorkspacePath = "_users/owner/secrets" },
		"missing project": func(a *CrewAttachment) { a.CrewProjectID = "" },
		"bad alias":       func(a *CrewAttachment) { a.Alias = "Bad Alias!" },
		"deleted crew dir": func(a *CrewAttachment) {
			a.CrewWorkspacePath = "_users/owner/Chats/Work/projects/gone"
			a.CrewProjectID = "gone"
		},
	} {
		bad := valid
		mutate(&bad)
		if err := ValidateCrewAttachmentRoot(bad, root); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestCrewAttachmentEnvKeys(t *testing.T) {
	env := CrewAttachmentEnvKeys([]CrewAttachment{
		{Alias: "project-a", CrewWorkspacePath: "_users/owner/Chats/Work/projects/project-a"},
		{Alias: "project-again", CrewWorkspacePath: "_users/owner/Chats/Work/projects/project-a2"},
		{Alias: "project-a_again", CrewWorkspacePath: "_users/owner/Chats/Work/projects/project-a3"},
	})
	if env["WORKFLOW_CREW_project-a"] != "_users/owner/Chats/Work/projects/project-a" {
		t.Fatalf("env = %v", env)
	}
	if len(env) != 3 {
		t.Fatalf("env = %v, want 3 disambiguated keys", env)
	}
	seen := map[string]bool{}
	for key := range env {
		if !strings.HasPrefix(key, "WORKFLOW_CREW_") || seen[key] {
			t.Fatalf("env = %v, want unique WORKFLOW_CREW_* keys", env)
		}
		seen[key] = true
	}
}

func TestResolveCrewAttachmentPath(t *testing.T) {
	attachments := []CrewAttachment{
		{ID: "a1", Alias: "reviewer-crew", CrewProfileID: "work", CrewProjectID: "project-a", CrewWorkspacePath: "_users/owner/Chats/Work/projects/project-a"},
	}
	if got, ok := ResolveCrewAttachmentPath(attachments, "reviewer-crew/reports/pr-87.md"); !ok || got != "_users/owner/Chats/Work/projects/project-a/reports/pr-87.md" {
		t.Fatalf("nested = %q ok=%v", got, ok)
	}
	if got, ok := ResolveCrewAttachmentPath(attachments, "reviewer-crew"); !ok || got != "_users/owner/Chats/Work/projects/project-a" {
		t.Fatalf("alias-only = %q ok=%v", got, ok)
	}
	if _, ok := ResolveCrewAttachmentPath(attachments, "other/file.md"); ok {
		t.Fatal("unknown alias resolved")
	}
	if _, ok := ResolveCrewAttachmentPath(attachments, "reviewer-crew/../../escape.md"); ok {
		t.Fatal("path escape resolved")
	}
	if _, ok := ResolveCrewAttachmentPath(nil, "reviewer-crew/file.md"); ok {
		t.Fatal("empty attachments resolved")
	}
	if _, ok := ResolveCrewAttachmentPath([]CrewAttachment{{Alias: "broken"}}, "broken/file.md"); ok {
		t.Fatal("empty crew root resolved")
	}
}

func TestReadCrewAttachments(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Workflow", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.json"), []byte(`{"id":"wf-1","crew_attachments":[{"id":"a1","alias":"project-a","crew_profile_id":"work","crew_project_id":"project-a","crew_workspace_path":"_users/owner/Chats/Work/projects/project-a"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, found := ReadCrewAttachments(root, "Workflow/demo")
	if !found || len(got) != 1 || got[0].Alias != "project-a" {
		t.Fatalf("attachments = %+v found=%v", got, found)
	}
	if _, found := ReadCrewAttachments(root, "Workflow/missing"); found {
		t.Fatal("missing manifest reported found")
	}
}
