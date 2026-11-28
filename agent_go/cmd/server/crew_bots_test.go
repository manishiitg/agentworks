package server

import "testing"

// A bot opens its project only to its audience (#4545): chosen people, or anyone at the company with the projects
// product; a private project stays closed to everyone else, and stopping the bot closes it again.
func TestCrewBotAdmitsOnlyItsAudience(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","email":"owner@example.com","products":["work"]},{"id":"ann","username":"ann","email":"ann@example.com","products":["work"]},{"id":"bob","username":"bob","email":"bob@example.com","products":["work"]},{"id":"nowork","username":"nowork","email":"nowork@example.com","products":["agentworks"]}]}`)
	registry := newTestRegistry(t)
	prev := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() { defaultProjectOwnersOverride = prev })
	const folder = "botproj-12345678"
	root := "Crew/" + folder
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: folder, ProjectID: "botproj", OwnerID: "owner", Shared: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPrivate("work", folder, true); err != nil {
		t.Fatal(err)
	}
	key := "botproj" + codeChatSideMarker + "helper"
	ref := crewPathRef{Root: root, OwnerID: "owner", Shared: true}
	check := func(stage string, want map[string]bool) {
		t.Helper()
		for user, admitted := range want {
			if got := crewAccessFor(&UserClaims{UserID: user, Username: user}, ref) == crewAccessReader; got != admitted {
				t.Fatalf("%s: %s admitted=%v, want %v", stage, user, got, admitted)
			}
		}
	}
	check("no bot", map[string]bool{"ann": false, "bob": false})

	if err := writeCrewBot(root, key, &crewBot{Maker: "owner", People: []string{"ann"}}); err != nil {
		t.Fatal(err)
	}
	check("chosen people", map[string]bool{"ann": true, "bob": false, "nowork": false})

	if err := writeCrewBot(root, key, &crewBot{Maker: "owner", Everyone: true}); err != nil {
		t.Fatal(err)
	}
	check("everyone", map[string]bool{"ann": true, "bob": true, "nowork": false})

	if err := writeCrewBot(root, key, nil); err != nil {
		t.Fatal(err)
	}
	check("stopped", map[string]bool{"ann": false, "bob": false})
}
