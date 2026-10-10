package handlers

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

// A reader's shell in a Crew whose project the server's guard grants runs as the owner's slot; everything else keeps
// the caller's slot (PLAT-810).
func TestReaderShellInGrantedCrewRunsAsTheOwnersSlot(t *testing.T) {
	project := "_users/owner/Chats/Work/projects/qa"
	granted := &models.FolderGuardConfig{Enabled: true, ReadPaths: []string{project + "/"}}
	if got := crewProjectOwnerForCommand("/docs", "/docs/"+project, "reader", granted); got != "owner" {
		t.Fatalf("reader in a granted Crew = %q, want owner", got)
	}
	if got := crewProjectOwnerForCommand("/docs", "/docs/"+project+"/sub", "reader", granted); got != "owner" {
		t.Fatalf("reader in a subfolder = %q, want owner", got)
	}
	for name, c := range map[string]struct {
		dir, caller string
		guard       *models.FolderGuardConfig
	}{
		"the owner's own call":  {"/docs/" + project, "owner", granted},
		"guard off":             {"/docs/" + project, "reader", &models.FolderGuardConfig{ReadPaths: []string{project}}},
		"guard names no Crew":   {"/docs/" + project, "reader", &models.FolderGuardConfig{Enabled: true, ReadPaths: []string{"_users/reader/Chats/"}}},
		"another Crew's grant":  {"/docs/" + project, "reader", &models.FolderGuardConfig{Enabled: true, ReadPaths: []string{"_users/owner/Chats/Work/projects/other"}}},
		"lookalike project":     {"/docs/" + project + "x", "reader", granted},
		"a private Code":        {"/docs/_users/owner/Chats/Code/projects/qa", "reader", &models.FolderGuardConfig{Enabled: true, ReadPaths: []string{"_users/owner/Chats/Code/projects/qa"}}},
		"another person's chat": {"/docs/_users/owner/Chats/Work", "reader", &models.FolderGuardConfig{Enabled: true, ReadPaths: []string{"_users/owner/Chats/Work"}}},
	} {
		if got := crewProjectOwnerForCommand("/docs", c.dir, c.caller, c.guard); got != "" {
			t.Fatalf("%s must keep the caller's slot, got owner %q", name, got)
		}
	}
}
