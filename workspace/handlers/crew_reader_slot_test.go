package handlers

import (
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

// The shared Crew/<id> root: the Crew folder is found only when the guard grants that very Crew.
func TestSharedCrewRootNeedsAGrantForTheSameCrew(t *testing.T) {
	granted := &models.FolderGuardConfig{Enabled: true, ReadPaths: []string{"Crew/qa-37137164/"}}
	if got := sharedCrewDirForCommand("/docs", "/docs/Crew/qa-37137164/work", granted); got != "/docs/Crew/qa-37137164" {
		t.Fatalf("granted shared Crew = %q", got)
	}
	for name, c := range map[string]struct {
		dir   string
		guard *models.FolderGuardConfig
	}{
		"guard off":       {"/docs/Crew/qa-37137164", &models.FolderGuardConfig{ReadPaths: []string{"Crew/qa-37137164"}}},
		"another Crew":    {"/docs/Crew/other-11111111", granted},
		"the root itself": {"/docs/Crew", &models.FolderGuardConfig{Enabled: true, ReadPaths: []string{"Crew"}}},
		"lookalike":       {"/docs/Crew/qa-37137164x", granted},
		"outside":         {"/docs/Workflow/x", granted},
	} {
		if got := sharedCrewDirForCommand("/docs", c.dir, c.guard); got != "" {
			t.Fatalf("%s must keep the caller's slot, got %q", name, got)
		}
	}
}

// A Team member's workflow run in the owner's Crew was refused before its script started: it ran as the member's slot
// in the Crew's private storage ("working folder: ... permission denied", PLAT-746-355). The private root is found
// only when the guard grants a folder in that very Crew's private storage that holds the working folder.
func TestCrewPrivateRootNeedsAGrantInTheSameCrew(t *testing.T) {
	docs := t.TempDir()
	root := filepath.Join(docs, "_system", "crew-data", "f35eea54-0000-0000-0000-000000000000")
	run := filepath.Join(root, "workflows", "wf_1", "runs", "run-1", "execution", "wf_1", "code")
	granted := &models.FolderGuardConfig{Enabled: true, WritePaths: []string{filepath.Join(root, "workflows", "wf_1", "runs", "run-1")}}
	if got := crewPrivateDirForCommand(docs, run, granted); got != root {
		t.Fatalf("granted Crew run folder = %q, want %q", got, root)
	}
	other := filepath.Join(docs, "_system", "crew-data", "other-crew", "workflows", "wf_2", "runs", "run-2")
	for name, c := range map[string]struct {
		dir   string
		guard *models.FolderGuardConfig
	}{
		"guard off":             {run, &models.FolderGuardConfig{WritePaths: granted.WritePaths}},
		"another Crew":          {other, granted},
		"outside the grant":     {filepath.Join(root, "workflows", "wf_1", "runs", "run-9"), granted},
		"the private dir root":  {filepath.Join(docs, "_system", "crew-data"), granted},
		"grant in another Crew": {run, &models.FolderGuardConfig{Enabled: true, WritePaths: []string{filepath.Dir(other)}}},
	} {
		if got := crewPrivateDirForCommand(docs, c.dir, c.guard); got != "" {
			t.Fatalf("%s must keep the caller's slot, got %q", name, got)
		}
	}
}
