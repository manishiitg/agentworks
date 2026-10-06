package handlers

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

// Only a command working in Brain/ whose folder guard grants Brain/ skips the caller's slot account (PLAT-633);
// anything else keeps running as the caller.
func TestOnlyGrantedBrainFolderCommandsRunAsTheService(t *testing.T) {
	granted := &models.FolderGuardConfig{Enabled: true, WritePaths: []string{"_users/u/Chats/Knowledgebase/", "Brain/"}}
	if !isBrainFolderCommand("/docs", "/docs/Brain", granted) || !isBrainFolderCommand("/docs", "/docs/Brain/RTS", granted) {
		t.Fatal("the granted Brain chat must run in Brain's folder")
	}
	for name, c := range map[string]struct {
		dir   string
		guard *models.FolderGuardConfig
	}{
		"no grant":      {"/docs/Brain", &models.FolderGuardConfig{Enabled: true, WritePaths: []string{"_users/u/Chats/"}}},
		"guard off":     {"/docs/Brain", &models.FolderGuardConfig{WritePaths: []string{"Brain/"}}},
		"outside Brain": {"/docs/_users/u/Chats", granted},
		"lookalike":     {"/docs/BrainStorm", granted},
	} {
		if isBrainFolderCommand("/docs", c.dir, c.guard) {
			t.Fatalf("%s must keep the caller's slot", name)
		}
	}
}
