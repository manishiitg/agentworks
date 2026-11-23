package server

import (
	"context"
	"slices"
	"strings"
)

// PLAT-746-1026: members' agents pick up the Team's way of working while they work in the Team's Crew. Shared apps and
// secrets are the ones members added to the Team's vault; a personal sign-in is never among them. The Crew's
// own saved selection is not changed: the Team's items are added when the runtime reads it, so removing one from the
// Team takes it away from everyone at once. Each use still checks Vault: the Crew owner is a member of the Team's
// vault, and the Team's people are its other members.

// teamWayPickup returns the Team's shared app names and secret names for the Crew at workspacePath; both are empty for a
// Crew whose Team has shared nothing.
func teamWayPickup(ctx context.Context, workspacePath string) (apps, secrets []string) {
	project := crewPanelProjectForPath(workspacePath)
	if project == "" {
		return nil, nil
	}
	team, err := readCrewTeam(project)
	if err != nil || !team.On {
		return nil, nil
	}
	// The Team's items are lent by the Crew's owner, so the owner must still be a current member (the owner of this
	// Crew and an active account); otherwise nothing of the Team's is picked up. Vault membership checks the same
	// again on every use.
	if owner := ownerOfCrewProjectID(project); owner == "" || owner != team.OwnerID || !crewTeamAccountActive(owner) {
		return nil, nil
	}
	rec, err := readTeamWay(project)
	if err != nil || rec.Vault == "" {
		return nil, nil
	}
	for _, id := range rec.Apps {
		apps = append(apps, vaultServerName(id))
	}
	for key := range rec.AddedBy {
		if name, ok := strings.CutPrefix(key, "secret:"); ok {
			if full, err := vaultSecretName(rec.Vault, name); err == nil {
				secrets = append(secrets, full)
			}
		}
	}
	slices.Sort(secrets)
	return apps, secrets
}

// teamWayServers is the Crew's app list as the runtime takes it, plus the Team's shared apps when the Crew uses chosen
// apps only (with every Vault app on, the runtime already adds all the owner may use, the Team's included).
func teamWayServers(ctx context.Context, workspacePath string, saved []string, chosenOnly bool) []string {
	servers := runtimeAppSelection(saved, chosenOnly)
	if !chosenOnly {
		return servers
	}
	apps, _ := teamWayPickup(ctx, workspacePath)
	return appendUniqueStrings(servers, apps...)
}
