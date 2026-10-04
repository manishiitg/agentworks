package main

import "testing"

// PLAT-442 step 3: the project skills-folder shape is parsed through workspaceref. Both users' physical spellings of
// a project skills folder are the accepted shape (who may write there is decided above this service); the logical
// spelling, a non-canonical one and anything else is refused.
func TestIsProjectSkillsDirShapes(t *testing.T) {
	for target, want := range map[string]bool{
		"_users/alice/Chats/Code/projects/app-1/skills":      true,
		"_users/bob/Chats/Code/projects/app-1/skills":        true,
		"_users/a.b@c/Chats/Work/projects/p/skills":          true,
		"Chats/Code/projects/app-1/skills":                   false, // the logical spelling names no tree
		"_users/alice/Chats/Code/projects/app-1/skills/more": false,
		"_users/alice/Chats/Code/projects/app-1":             false,
		"_users/alice//Chats/Code/projects/app-1/skills":     false, // not canonical
		"_users/./Chats/Code/projects/app-1/skills":          false,
		"_users/alice/Chats/Code/projects/../x/skills":       false,
		"x/_users/alice/Chats/Code/projects/p/skills":        false,
		"_users/alice/Chats/Code/projects/p q/skills":        false,
		"_users/bad owner/Chats/Code/projects/p/skills":      false,
	} {
		if got := isProjectSkillsDir(target); got != want {
			t.Errorf("isProjectSkillsDir(%q) = %v, want %v", target, got, want)
		}
	}
}
