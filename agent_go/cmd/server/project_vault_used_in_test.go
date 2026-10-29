package server

import (
	"slices"
	"testing"
)

// PLAT-856-400: "Used in" lists only the caller's own projects that select the item, never someone else's project.
func TestPersonalUsedInListsOnlyOwnSelectingProjects(t *testing.T) {
	newTriggerLinkEnv(t)
	ctx := t.Context()
	write := func(path, caps string) {
		t.Helper()
		if err := writeFileToWorkspace(ctx, path+"/workflow.json", `{"id":"x","capabilities":`+caps+`}`); err != nil {
			t.Fatal(err)
		}
	}
	write(linkAlphaPath, `{"selected_servers":["vault_lent"],"chosen_vault_apps_only":true}`)
	write(linkBetaPath, `{"selected_servers":[],"chosen_vault_apps_only":true}`)
	write(linkGammaPath, `{"selected_servers":["vault_lent"],"chosen_vault_apps_only":true}`) // another person's shared Crew

	got := (&StreamingAPI{}).personalUsedIn(ctx, "owner").forItem("mcp", "lent")
	if !slices.Equal(got, []string{"Alpha"}) {
		t.Fatalf("used in = %v, want only the caller's selecting project", got)
	}
}
