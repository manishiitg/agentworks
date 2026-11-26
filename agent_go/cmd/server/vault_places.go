package server

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// The places a key can live, as an agent sees them (#4144): every vault the person can reach, who each one reaches, the
// key names in it (never values), and where this project reads keys from and saves new ones. The agent picks a place
// from this; the save and copy tools check the same access again.

type secretPlace struct {
	Place   string   `json:"place"` // personal, project, team, company or shared
	Name    string   `json:"name"`
	Reaches string   `json:"reaches"`
	Keys    []string `json:"keys"`
}

type secretPlacesView struct {
	Project     string        `json:"project"` // what this project reads, in plain words
	SavesTo     string        `json:"saves_to"`
	Uses        []string      `json:"uses"`
	Places      []secretPlace `json:"places"`
	NeedsValue  []string      `json:"needs_value,omitempty"`
	Note        string        `json:"note"`
	Unavailable []string      `json:"unavailable,omitempty"`
}

func shortSecretNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, vaultSecretShortName(name))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (api *StreamingAPI) secretPlaces(ctx context.Context, person, root string) secretPlacesView {
	view := secretPlacesView{Places: []secretPlace{}, Uses: []string{}}
	project := serverCrewForPath(root)
	if list, err := personalVaults(ctx, person); err != nil {
		view.Unavailable = append(view.Unavailable, "Your vaults could not be read; try again shortly.")
	} else {
		for _, v := range list.Vaults {
			p := secretPlace{Name: v.Group.Name, Keys: shortSecretNames(v.SecretNames)}
			switch {
			case v.Role == "owner" && v.Group.Name == personalVaultName:
				p.Place, p.Reaches = secretPlacePersonal, "Only you and your own agents."
			case v.Group.Name == personalVaultName:
				// A Team's vault is the Team account's own vault, shared with the Team's people (crew_team_way.go).
				p.Place, p.Name, p.Reaches = secretPlaceTeam, "Team vault", "The people in that Team."
			default:
				p.Place, p.Reaches = "shared", "The people this vault is shared with."
			}
			view.Places = append(view.Places, p)
		}
	}
	company := []string{}
	for _, row := range visibleGlobalSecrets(ctx, person) {
		if !isVaultSecretName(row.Name) {
			company = append(company, row.Name)
		}
	}
	if len(company) > 0 {
		reaches := "People a Vault administrator gave them to."
		if companyKeysOn() {
			reaches = "Everyone on this server. Only Vault admins add or change them."
		}
		view.Places = append(view.Places, secretPlace{Place: secretPlaceCompany, Name: "Company keys", Reaches: reaches, Keys: shortSecretNames(company)})
	}
	if project != "" {
		names := api.crewVaultSecretNames(ctx, project)
		own := []string{}
		for _, n := range names {
			if isVaultSecretName(n) {
				own = append(own, n)
			}
		}
		view.Places = append(view.Places, secretPlace{Place: secretPlaceProject, Name: "This project's vault", Reaches: "Everyone working in this online project: its owner, co-owners and Users, and its schedules and workflows.", Keys: shortSecretNames(own)})
		view.Project = "Online project: its work uses the keys in this project's vault, and company keys (the project's own key wins on the same name)."
		view.SavesTo = secretPlaceProject
		view.Uses = shortSecretNames(names)
		view.Note = "Company keys reach this project directly. To use a key from your own or a Team vault here, copy it into this project's vault: set_workflow_secret with copy_from (the server copies the value; you never see it). Only the owner and co-owners change this project's vault; only Vault admins change company keys."
	} else if strings.TrimSpace(root) != "" {
		selection, err := projectSecretSelection(ctx, root)
		if err != nil {
			view.Unavailable = append(view.Unavailable, "This project's chosen keys could not be read.")
		}
		_, team := teamWayPickup(ctx, root)
		view.Project = "This project uses the keys it chooses from your vaults, its Team's vault and company keys."
		view.SavesTo = secretPlacePersonal
		view.Uses = shortSecretNames(append(selection, team...))
		view.Note = "Save a new key with set_workflow_secret (your own Vault by default). Share a key with your Team with the Team tool (way_add, item secret). Company keys are added by a Vault administrator."
	} else {
		view.Project = "Not in a project."
		view.SavesTo = secretPlacePersonal
	}
	view.NeedsValue = globalSecretNamesWithoutValue(ctx, person)
	return view
}

// copySecretInto copies a key the person can already use (by its short or Vault name) into where, without the agent
// ever seeing the value: into an online project's vault, or into the person's own Vault. It returns the new Vault name,
// the short name and the value for the live shell.
func (api *StreamingAPI) copySecretInto(ctx context.Context, person, root, from, where string) (string, string, string, error) {
	from = strings.TrimSpace(from)
	if from == "" {
		return "", "", "", errors.New("name the key to copy (copy_from)")
	}
	value := ""
	for _, row := range visibleGlobalSecrets(ctx, person) {
		if row.Name == from || vaultSecretShortName(row.Name) == from {
			if value = row.Value; strings.TrimSpace(value) != "" {
				break
			}
		}
	}
	if strings.TrimSpace(value) == "" {
		return "", "", "", errors.New("You have no key called " + from + " with a value in your vaults. Call list_secrets to see the names you can use.")
	}
	short := vaultSecretShortName(from)
	full, _, err := api.saveProjectSecret(ctx, person, root, short, value, where)
	if err != nil {
		return "", "", "", err
	}
	return full, short, value, nil
}
