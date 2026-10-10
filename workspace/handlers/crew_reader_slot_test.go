package handlers

import (
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
