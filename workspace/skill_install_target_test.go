package main

import (
	"path/filepath"
	"testing"
)

func TestSkillInstallTargetDir(t *testing.T) {
	docs := "/docs"
	if got, err := skillInstallTargetDir(docs, ""); err != nil || got != filepath.Join(docs, "skills") {
		t.Fatalf("library target = %q %v", got, err)
	}
	ok := "_users/u1/Chats/Code/projects/app-1234abcd/skills"
	if got, err := skillInstallTargetDir(docs, ok); err != nil || got != filepath.Join(docs, ok) {
		t.Fatalf("project target = %q %v", got, err)
	}
	for _, bad := range []string{
		"skills", "_users/u1/Chats/Code/projects/app/code", "_users/u1/Chats/Code/projects/../../x/skills",
		"Workflow/w/skills", "_users/u1/Chats/Code/projects/app/skills/extra", "/etc/skills",
	} {
		if _, err := skillInstallTargetDir(docs, bad); err == nil {
			t.Fatalf("accepted target %q", bad)
		}
	}
}
