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
// ("working folder: ... permission denied", PLAT-746-355). Its grant names the run folder, which lives in the Crew's
// .agentworks (PLAT-746-924); the Crew is found only when the guard grants a folder in that very Crew that holds the
// working folder.
func TestCrewWorkflowRunNeedsAGrantInTheSameCrew(t *testing.T) {
	docs := t.TempDir()
	root := filepath.Join(docs, "Crew", "automation-testing-f35eea54")
	run := filepath.Join(root, ".agentworks", "workflows", "wf_1", "runs", "run-1")
	work := filepath.Join(run, "execution", "wf_1", "code")
	granted := &models.FolderGuardConfig{Enabled: true, WritePaths: []string{run}}
	if got := sharedCrewDirForCommand(docs, work, granted); got != root {
		t.Fatalf("granted Crew run folder = %q, want %q", got, root)
	}
	other := filepath.Join(docs, "Crew", "other-0000aaaa", ".agentworks", "workflows", "wf_2", "runs", "run-2")
	for name, c := range map[string]struct {
		dir   string
		guard *models.FolderGuardConfig
	}{
		"guard off":             {work, &models.FolderGuardConfig{WritePaths: granted.WritePaths}},
		"another Crew":          {other, granted},
		"outside the grant":     {filepath.Join(root, ".agentworks", "workflows", "wf_1", "runs", "run-9"), granted},
		"grant in another Crew": {work, &models.FolderGuardConfig{Enabled: true, WritePaths: []string{filepath.Dir(other)}}},
	} {
		if got := sharedCrewDirForCommand(docs, c.dir, c.guard); got != "" {
			t.Fatalf("%s must keep the caller's slot, got %q", name, got)
		}
	}
}
