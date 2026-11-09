package server

import (
	"net/http"
	"os"
	"path/filepath"
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
}

type crewDashboardsTabFilter struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
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
	crewDashboardsMacOwnerNote    = "Buttons on these work on your Mac. To share with other people, or make a public link, put this Crew online."
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
	tab := crewDashboardsTab{Workspace: root, Intro: "Everything published from this Crew.", CanChange: owner && !crewOwnerOnlineOfMacCrew(r, t.ProjectID, GetUserIDFromContext(r.Context())), OnMac: onMac, PutOnline: owner && onMac, Filter: filter, Items: []crewDashboardsTabItem{}}
	who := api.crewDashboardsTabWho(r, root, owner && !onMac)
	updatedOnMac := ""
	if updated, ok := crewMacDashboardsUpdated(t.ProjectID); ok && crewRelayTwin(t.ProjectID) {
		updatedOnMac = "copy on the server " + roughAgo(time.Since(updated))
	}
	counts := map[string]int{}
	for _, row := range rows {
		key, label := crewDashboardKind(row)
		counts[key]++
		if filter != "all" && filter != key {
			continue
		}
		when := updatedOnMac
		if when == "" && root != "" && row.Path != "" {
			if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(row.Path))); err == nil {
				when = "updated " + roughAgo(time.Since(info.ModTime()))
			}
		}
		line := label
		if when != "" {
			line += " · " + when
		}
		item := crewDashboardsTabItem{ID: row.ID, Title: row.Title, Path: row.Path, Kind: key, Line: line, Who: who}
		if !onMac && owner {
			item.PublicLink = row.PublicLink
		}
		tab.Items = append(tab.Items, item)
	}
	tab.Filters = []crewDashboardsTabFilter{{Key: "all", Label: "All", Count: len(rows)}}
	for _, kind := range []struct{ key, label string }{{"dashboard", "Dashboards"}, {"artifact", "Artifacts"}, {"motion", "Motion"}} {
		if counts[kind.key] > 0 {
			tab.Filters = append(tab.Filters, crewDashboardsTabFilter{Key: kind.key, Label: kind.label, Count: counts[kind.key]})
		}
	}
	switch {
	case !owner && onMac:
		tab.Note = crewDashboardsMacTeammateNote + " " + crewDashboardsTeammateNote
	case !owner:
		tab.Note = crewDashboardsTeammateNote
	case onMac:
		tab.Note = crewDashboardsMacOwnerNote
	}
	if len(rows) == 0 {
		// Nothing to open: no lines about items that are not there, and one line for a teammate.
		tab.Intro, tab.Note, tab.PutOnline = "", "", false
		tab.Empty = &crewDashboardsTabEmpty{Line: crewPersonShortName(t.OwnerID) + " hasn't published anything from this Crew yet."}
		if owner {
			// The Dashboards tab (owner, 9 Nov mock): one line and "Ask the Crew", which opens Main with this draft.
			tab.Empty = &crewDashboardsTabEmpty{Title: "No dashboard yet", Line: "Ask the Crew to make one from its results.",
				Ask: "Make a dashboard for this Crew from its results, so people can look at them without changing anything. Show me before you publish it."}
		}
	}
	return tab, true
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
