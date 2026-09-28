package server

import "testing"

// A Crew or Code reader must never open credentials or tool state: the
// project's HOME (.sandbox-cache/home, with git credentials and CLI logins),
// .git, .env and every other dot-path, at any depth.
func TestSharedProjectReadersNeverSeeDotPaths(t *testing.T) {
	for _, hidden := range []string{
		".sandbox-cache/home/.git-credentials",
		".sandbox-cache/home/.config/gh/hosts.yml",
		".sandbox-cache/home/.ssh/id_ed25519",
		".git/config",
		"code/rts-website/.git/config",
		"code/app/.env",
		".claude/settings.json",
		"builder/conversation/x.json",
		"db/db.sqlite",
		"product.json",
	} {
		if _, ok := confineSharedProjectPath("_users/o/Chats/Work/projects/p", hidden); ok {
			t.Errorf("reader could open %q", hidden)
		}
		if crewRootListingVisible(hidden) {
			t.Errorf("reader could list %q", hidden)
		}
	}
	for _, visible := range []string{"code/app.py", "notes/plan.md", "code/product.json"} {
		if _, ok := confineSharedProjectPath("_users/o/Chats/Work/projects/p", visible); !ok {
			t.Errorf("reader could not open %q", visible)
		}
	}
}
