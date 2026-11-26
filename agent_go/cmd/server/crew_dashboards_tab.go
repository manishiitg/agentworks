package server

import (
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
)

// The Access page's Dashboards tab, next to Share (owner, 6 Nov mockup; its own tab again 9 Nov): everything the Crew
// publishes (dashboards, artifacts and motion), each with its kind, when it was updated, who can open it and, on a server Crew, its public link. The
// server writes every line; the card renders them. It reads the same rows and the same Team and Share records as the Team
// card's Dashboards list (crew_team_dashboards.go), so it adds no access rule: the Crew's owner or a Team member opens
// it, and only the owner changes who can open an item or its public link.

type crewDashboardsTabItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
	Kind  string `json:"kind"` // dashboard, artifact or motion
	// Line is "Dashboard · updated 5 min ago", written here.
	Line string `json:"line"`
	// Who says who can open it: the Team always, and the people the owner shared the Crew with.
	Who string `json:"who"`
	// PublicLink is the owner's, on a Crew that lives on the server only.
	PublicLink *dashboardPublicView `json:"public_link,omitempty"`
	// When is "2 h ago" and Group is Today, This week or Older (owner, 25 Nov mockup); items come newest first.
	When  string `json:"when,omitempty"`
	Group string `json:"group"`
	// CanDelete: this person may delete it (the owner); the page then offers Delete with Undo.
	CanDelete bool `json:"can_delete,omitempty"`
	// Team sharing (owner, 25 Nov): CanShareTeam offers Share with Team on an Artifact of a project whose Team is on;
	// TeamShared says its copy is in the Team's folder. FromTeam marks a copy someone shared, opened from that folder.
	CanShareTeam bool `json:"can_share_team,omitempty"`
	TeamShared   bool `json:"team_shared,omitempty"`
	FromTeam     bool `json:"from_team,omitempty"`
	updated      time.Time
}

type crewDashboardsTabFilter struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
	// Hint says in one line what this kind is and how it works (owner, 9 Nov); Ask, for the owner with none yet,
	// is the draft "Ask the Crew" puts in Main.
	Hint string `json:"hint,omitempty"`
	Ask  string `json:"ask,omitempty"`
}

// The three kinds a Crew publishes, always listed (owner, 9 Nov: "show motion dashboards zero"), each with its hint.
var crewDashboardsTabKinds = []struct{ key, label, hint, ask string }{
	{"dashboard", "Dashboards", "Live pages built from this project's results, with Ask and buttons. They stay up to date as the project works.",
		"Make a dashboard for this project from its results, so people can look at them without changing anything. Show me before you publish it."},
	{"artifact", "Artifacts", "A finished page made in a chat to show one idea: a proposal, plan or comparison. No live data.",
		"Make an artifact: one page that sums up this project's latest results. Show me before you publish it."},
	{"motion", "Motion", "Plays changes over time, or walks through an explanation one step at a time. Same link and sharing as a dashboard.",
		"Make a motion dashboard that plays this project's results over time. Show me before you publish it."},
}

type crewDashboardsTabEmpty struct {
	Title string `json:"title"`
	Line  string `json:"line"`
	// Kinds says in short what can be published, for the owner who has nothing yet.
	Kinds   []crewDashboardsTabKind `json:"kinds,omitempty"`
	Ask     string                  `json:"ask,omitempty"`
	Example string                  `json:"example,omitempty"`
}

type crewDashboardsTabKind struct {
	Name string `json:"name"`
	Line string `json:"line"`
}

type crewDashboardsTab struct {
	// Workspace is the Crew's folder, which the card opens a row's file in.
	Workspace string `json:"workspace"`
	Intro     string `json:"intro"`
	// CanChange: this person is the Crew's main owner (who can open it and the public link are theirs).
	CanChange bool `json:"can_change"`
	// OnMac: the Crew lives on its owner's Mac, so its public link and sharing outside the Team need it online first.
	OnMac bool `json:"on_mac,omitempty"`
	// PutOnline: the owner may put it online from the card (Mac Crews only).
	PutOnline bool                      `json:"put_online,omitempty"`
	Note      string                    `json:"note,omitempty"`
	Filter    string                    `json:"filter"`
	Filters   []crewDashboardsTabFilter `json:"filters"`
	Items     []crewDashboardsTabItem   `json:"items"`
	Empty     *crewDashboardsTabEmpty   `json:"empty,omitempty"`
}

const (
	crewDashboardsMacOwnerNote    = "Buttons on these work on your Mac. To share with other people, or make a public link, put this project online."
	crewDashboardsMacTeammateNote = "Buttons on these work on the owner's Mac for now."
	crewDashboardsTeammateNote    = "Only the owner changes who can open these."
)

func crewDashboardKind(row crewTeamDashboard) (key, label string) {
	switch {
	case row.Motion:
		return "motion", "Motion"
	case row.Artifact:
		return "artifact", "Artifact"
	}
	return "dashboard", "Dashboard"
}

// crewDashboardsTabWho is the "Who can open it" line: the people who work with the owner (the Team), and the people or
// everyone the owner also shared the Crew with. Only the owner reads the Share record, as for the Share tree.
func (api *StreamingAPI) crewDashboardsTabWho(r *http.Request, root string, owner bool) string {
	team := "people who work with you"
	if !owner {
		return team
	}
	var share struct {
		Mode   string
		People []struct{ Username, Email string }
	}
	if err := crewPanelRead(r, api.handleDashboards, http.MethodPost, "/api/dashboards", nil, nil, map[string]string{"workspace": root, "action": "share_status"}, &share); err != nil {
		return team
	}
	switch share.Mode {
	case runShareEveryone:
		return team + " and everyone on this server"
	case runSharePeople:
		names := []string{}
		for _, person := range share.People {
			if name := firstNonEmptyTrimmed(person.Username, person.Email); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			return team + ", " + strings.Join(names, ", ")
		}
	}
	return team
}

// crewDashboardsTabFor builds the tab for a Team member or the owner; the filter is "", all, dashboard, artifact or motion.
func (api *StreamingAPI) crewDashboardsTabFor(w http.ResponseWriter, r *http.Request, filter string) (crewDashboardsTab, bool) {
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return crewDashboardsTab{}, false
	}
	root := crewProjectRootByID(t.ProjectID)
	owner := t.OwnerID == claims.UserID
	onMac := dashboardPublicMacCrewBlocked(t.ProjectID)
	rows, ok := api.crewTeamDashboardRows(w, r, t, root, owner)
	if !ok {
		return crewDashboardsTab{}, false
	}
	switch filter {
	case "dashboard", "artifact", "motion":
	default:
		filter = "all"
	}
	tab := crewDashboardsTab{Workspace: root, Intro: "Everything published from this project.", CanChange: owner && !crewOwnerOnlineOfMacCrew(r, t.ProjectID, GetUserIDFromContext(r.Context())), OnMac: onMac, PutOnline: owner && onMac, Filter: filter, Items: []crewDashboardsTabItem{}}
	who := api.crewDashboardsTabWho(r, root, owner && !onMac)
	counts := map[string]int{}
	teamItems := 0
	for _, row := range rows {
		key, label := crewDashboardKind(row)
		counts[key]++
		if filter != "all" && filter != key {
			continue
		}
		var updated time.Time
		if row.UpdatedAt > 0 {
			updated = time.Unix(row.UpdatedAt, 0)
		}
		line, when := label, ""
		if !updated.IsZero() {
			when = roughAgo(time.Since(updated))
			line += " · updated " + when
		}
		item := crewDashboardsTabItem{ID: row.ID, Title: row.Title, Path: row.Path, Kind: key, Line: line, Who: who, When: when, Group: crewDashboardsGroup(updated, time.Now()), CanDelete: tab.CanChange, updated: updated}
		if !onMac && owner {
			item.PublicLink = row.PublicLink
		}
		tab.Items = append(tab.Items, item)
	}
	sort.SliceStable(tab.Items, func(i, j int) bool { return tab.Items[i].updated.After(tab.Items[j].updated) })
	// Pages in the Team's shared artifacts/ folder: this project's own copies mark their row; the rest are listed
	// under From the Team, newest first, after this project's own pages.
	if roots, found, err := crewStorageRoots(root); err == nil && found {
		_, teamErr := crewTeamArtifactsDir(roots.ID)
		shared := crewTeamArtifacts(dashboardTeamRoots{ID: roots.ID, Files: roots.Files})
		sort.SliceStable(shared, func(i, j int) bool { return shared[i].Updated.After(shared[j].Updated) })
		for i := range tab.Items {
			tab.Items[i].CanShareTeam = teamErr == nil && tab.CanChange && tab.Items[i].Kind == "artifact"
		}
		for _, a := range shared {
			if at := slices.IndexFunc(tab.Items, func(item crewDashboardsTabItem) bool { return item.ID == a.ID }); at >= 0 {
				tab.Items[at].TeamShared = true
				continue
			}
			if at := slices.IndexFunc(rows, func(row crewTeamDashboard) bool { return row.ID == a.ID }); at >= 0 {
				continue // this project's own page, filtered out of view
			}
			counts["artifact"]++
			teamItems++
			if filter != "all" && filter != "artifact" {
				continue
			}
			line, when := "Artifact · shared with the Team", ""
			if !a.Updated.IsZero() {
				when = roughAgo(time.Since(a.Updated))
			}
			if a.By != "" {
				line = "Artifact · shared by " + a.By
			}
			tab.Items = append(tab.Items, crewDashboardsTabItem{ID: a.ID, Title: a.Title, Path: a.Path, Kind: "artifact", Line: line, Who: "Everyone in the Team", When: when, Group: "From the Team", FromTeam: true, CanShareTeam: tab.CanChange, TeamShared: true, updated: a.Updated})
		}
	}
	// Filters only when they help (owner, 25 Nov mockup): one per kind that has items, and none when there is one kind.
	tab.Filters = []crewDashboardsTabFilter{}
	for _, kind := range crewDashboardsTabKinds {
		if counts[kind.key] > 0 {
			tab.Filters = append(tab.Filters, crewDashboardsTabFilter{Key: kind.key, Label: kind.label, Count: counts[kind.key], Hint: kind.hint})
		}
	}
	if len(tab.Filters) > 1 {
		tab.Filters = append([]crewDashboardsTabFilter{{Key: "all", Label: "All", Count: len(rows) + teamItems}}, tab.Filters...)
	} else {
		tab.Filters = []crewDashboardsTabFilter{}
	}
	switch {
	case !owner && onMac:
		tab.Note = crewDashboardsMacTeammateNote + " " + crewDashboardsTeammateNote
	case !owner:
		tab.Note = crewDashboardsTeammateNote
	case onMac:
		tab.Note = crewDashboardsMacOwnerNote
	}
	if len(rows) == 0 && teamItems == 0 {
		// Nothing to open: no lines about items that are not there, and one line for a teammate.
		tab.Intro, tab.Note, tab.PutOnline = "", "", false
		tab.Empty = &crewDashboardsTabEmpty{Line: crewPersonShortName(t.OwnerID) + " hasn't published anything from this project yet."}
		if owner {
			// The Dashboards tab (owner, 9 Nov mock): one line and "Ask the Crew", which opens Main with this draft.
			tab.Empty = &crewDashboardsTabEmpty{Title: "No artifacts yet", Line: "Ask the project to make a report, proposal, demo or dashboard.",
				Ask: "Make an artifact for this project from its results. Show me before you publish it."}
		}
	}
	return tab, true
}

// crewDashboardsGroup is the heading an item sits under: Today, This week (the last seven days) or Older.
func crewDashboardsGroup(updated, now time.Time) string {
	switch {
	case updated.IsZero():
		return "Older"
	case updated.Year() == now.Year() && updated.YearDay() == now.YearDay():
		return "Today"
	case now.Sub(updated) < 7*24*time.Hour:
		return "This week"
	}
	return "Older"
}

// handleCrewTeamDashboardsTab is GET /crew-teams/{project}/dashboards-tab?kind=all|dashboard|artifact|motion.
func (api *StreamingAPI) handleCrewTeamDashboardsTab(w http.ResponseWriter, r *http.Request) {
	tab, ok := api.crewDashboardsTabFor(w, r, r.URL.Query().Get("kind"))
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, tab)
}
