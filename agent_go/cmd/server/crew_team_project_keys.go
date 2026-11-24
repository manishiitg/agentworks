package server

import (
	"context"
	"maps"
	"slices"
	"strings"
)

// Team keys for an online project (#3664): while a server project is a member of a Team, its turns and workflow runs
// use the Team's keys as well as its own project vault's. The project's own key wins when both have the same name.
// The Team's keys are read as the Team vault's own owner (team-<project>), so nobody's personal Vault changes and the
// keys reach only that project's work. Leaving the Team, or the Team turning off, takes them away at once.

// crewTeamProjectKeyVault is one Team's vault an online project uses, and the Vault names of its keys.
type crewTeamProjectKeyVault struct {
	Team  string
	Vault string
	Names []string
}

// crewTeamProjectKeyVaults are the vaults of the Teams the online project is in now, with the keys members added.
func crewTeamProjectKeyVaults(project string) []crewTeamProjectKeyVault {
	if project == "" || IsLocalMode() {
		return nil
	}
	out := []crewTeamProjectKeyVault{}
	for _, t := range crewTeamsOnServer() {
		if !t.hasProject(project) || ownerOfCrewProjectID(t.ProjectID) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
			continue
		}
		rec, err := readTeamWay(t.ProjectID)
		if err != nil || rec.Vault == "" {
			continue
		}
		v := crewTeamProjectKeyVault{Team: t.ProjectID, Vault: rec.Vault}
		for key := range rec.AddedBy {
			if name, ok := strings.CutPrefix(key, "secret:"); ok {
				if full, err := vaultSecretName(rec.Vault, name); err == nil {
					v.Names = append(v.Names, full)
				}
			}
		}
		slices.Sort(v.Names)
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b crewTeamProjectKeyVault) int { return strings.Compare(a.Team, b.Team) })
	return out
}

// withCrewTeamProjectKeys adds the Team keys of an online project to its own key names. A Team key whose name the
// project already has, or an earlier Team already gave, is left out, so every name reaches the work once.
func withCrewTeamProjectKeys(project string, own []string) []string {
	out := append([]string{}, own...)
	taken := map[string]bool{}
	for _, name := range own {
		taken[vaultSecretShortName(name)] = true
	}
	for _, v := range crewTeamProjectKeyVaults(project) {
		for _, name := range v.Names {
			if short := vaultSecretShortName(name); !taken[short] {
				taken[short] = true
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// crewTeamProjectLentKeys are the Team keys (Vault name -> Team project) that work lent by this person may use: they
// are the main owner of an online project in that Team (a server Crew's turns and runs are lent by its main owner).
// When a project vault is in scope, only that project's Teams count.
func crewTeamProjectLentKeys(ctx context.Context, lender string) map[string]string {
	if lender == "" || IsLocalMode() {
		return nil
	}
	scope := crewVaultScope(ctx)
	out := map[string]string{}
	for _, t := range crewTeamsOnServer() {
		lent := false
		for _, p := range t.Projects {
			if ownerOfCrewProjectID(p.ProjectID) == lender && (scope == "" || crewVaultID(p.ProjectID) == scope) {
				lent = true
			}
		}
		if !lent || ownerOfCrewProjectID(t.ProjectID) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
			continue
		}
		rec, err := readTeamWay(t.ProjectID)
		if err != nil || rec.Vault == "" {
			continue
		}
		for key := range rec.AddedBy {
			if name, ok := strings.CutPrefix(key, "secret:"); ok {
				if full, err := vaultSecretName(rec.Vault, name); err == nil {
					out[full] = t.ProjectID
				}
			}
		}
	}
	return out
}

// crewTeamProjectKeyValues resolves the chosen Team keys as the Team vault's own owner (team-<project>). A short name
// already taken (the project's own key) is skipped.
func crewTeamProjectKeyValues(ctx context.Context, chosen map[string]string, taken map[string]string) []resolvedSecret {
	teams := map[string]bool{}
	for _, team := range chosen {
		teams[team] = true
	}
	var out []resolvedSecret
	for _, team := range slices.Sorted(maps.Keys(teams)) {
		for _, s := range visibleGlobalSecrets(ctx, "team-"+team) {
			short := vaultSecretShortName(s.Name)
			if chosen[s.Name] != team || taken[short] != "" {
				continue
			}
			taken[short] = s.Name
			out = append(out, resolvedSecret{Name: short, Value: s.Value})
		}
	}
	return out
}
