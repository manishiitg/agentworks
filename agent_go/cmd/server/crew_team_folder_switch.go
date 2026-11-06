package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The Folders / Whole Crew switch asks first (owner, 6 Nov, approved mockup "Whole Crew asks first"): before the
// Crew's owner switches, the server writes what changes: who gets it, which folders are added or kept, the sizes, what
// stays private. The Team tab's confirm box, the Mac app and the chat answer (team_sync configure without confirm)
// all show these same lines; nothing changes until the owner confirms and the ordinary save runs.

// crewTeamFolderChange is the confirm box for one switch, fully worded. Refusal is set (and ConfirmLabel empty) when
// the Whole Crew is over the Team's limits.
type crewTeamFolderChange struct {
	Whole        bool     `json:"whole"`
	Title        string   `json:"title"`
	Sub          string   `json:"sub"`
	People       []string `json:"people"`
	FoldersAdded []string `json:"folders_added,omitempty"`
	FoldersKept  []string `json:"folders_kept,omitempty"`
	Means        []string `json:"means"`
	Note         string   `json:"note,omitempty"`
	ConfirmLabel string   `json:"confirm_label,omitempty"`
	Refusal      string   `json:"refusal,omitempty"`
}

// crewTeamWholeListing reads the Crew's top-level folders the Team may share and the Whole Crew's size where the Crew
// is: its folder on the server, or its owner's Mac over the Mac online relay. ok is false when it cannot be read now.
func crewTeamWholeListing(ctx context.Context, owner, project string) (folders []string, count, bytes int64, ok bool) {
	var listing struct {
		Folders []string `json:"folders"`
		Files   int64    `json:"files"`
		Bytes   int64    `json:"bytes"`
	}
	if !crewRelayTwin(project) {
		roots, found, err := crewStorageRoots(crewProjectRootByID(project))
		if err != nil || !found {
			return nil, 0, 0, false
		}
		out, _, err := crewTeamFolderListing(roots.Files, "", true)
		if err != nil {
			return nil, 0, 0, false
		}
		folders, _ = out["folders"].([]string)
		count, _ = out["files"].(int64)
		bytes, _ = out["bytes"].(int64)
		return folders, count, bytes, true
	}
	pair, found := crewMacPairFor(owner, project)
	if !found {
		return nil, 0, 0, false
	}
	result := callCrewMacPair(ctx, owner, project, pair, crewChatCall{Operation: "team_folders", Query: url.Values{"whole": {"1"}}}, 20*time.Second)
	if result.Status != http.StatusOK || json.Unmarshal(result.Body, &listing) != nil {
		return nil, 0, 0, false
	}
	return listing.Folders, listing.Files, listing.Bytes, true
}

// crewTeamSizeText is a size as people read it: "12,840 files · 188 MB".
func crewTeamSizeText(count, bytes int64) string {
	files := "files"
	if count == 1 {
		files = "file"
	}
	return crewTeamCount(count) + " " + files + " · " + crewTeamBytesText(bytes)
}

func crewTeamBytesText(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes < 10<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	}
	return fmt.Sprintf("%.0f MB", float64(bytes)/(1<<20))
}

func crewTeamCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// crewTeamNameList joins names as a sentence: "a", "a and b", "a, b and c".
func crewTeamNameList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// crewTeamFolderChangePreview is the confirm box for switching t to the Whole Crew (whole) or back to Folders. keep
// names the folders to share instead of the server's choice (chat's share=folders folders=[…]).
func crewTeamFolderChangePreview(ctx context.Context, t crewTeam, whole bool, keep []string) (crewTeamFolderChange, error) {
	if whole == t.Folder.Whole {
		if whole {
			return crewTeamFolderChange{}, fmt.Errorf("The Team shares the Whole Crew already.")
		}
		return crewTeamFolderChange{}, fmt.Errorf("The Team shares folders already.")
	}
	chips, names := []string{}, []string{}
	for _, p := range t.People {
		if p.UserID == t.OwnerID || !crewTeamAccountActive(p.UserID) {
			continue
		}
		name := crewPersonShortName(p.UserID)
		names = append(names, name)
		if p.Invited {
			name += " (invited)"
		}
		chips = append(chips, name)
	}
	out := crewTeamFolderChange{Whole: whole, People: chips, Means: []string{}}
	if !whole {
		// The same folders the save picks when going back: the ones shared before the Whole Crew, or team/.
		kept := t.LastFolders
		if len(keep) > 0 {
			var err error
			if kept, err = cleanCrewTeamFolders(keep, nil); err != nil {
				return crewTeamFolderChange{}, err
			}
		}
		if len(kept) == 0 {
			kept = []string{crewTeamDefaultFolder}
		}
		out.Title = "Go back to sharing folders?"
		out.Sub = "The Team will share only " + (crewTeamFolder{Folders: kept}).display() + "."
		out.FoldersKept = make([]string, 0, len(kept))
		for _, folder := range kept {
			out.FoldersKept = append(out.FoldersKept, folder+"/")
		}
		who := "the Team"
		if len(names) > 0 {
			who = crewTeamNameList(names)
		}
		out.Means = append(out.Means, "Other folders stop syncing for "+who+".", "Their copies of those files stay on their computers but no longer update.")
		out.ConfirmLabel = "Share folders only"
		return out, nil
	}
	out.Title = "Share the whole Crew with the Team?"
	if name := strings.TrimSpace(t.Name); name != "" {
		out.Title = fmt.Sprintf("Share the whole %q Crew with the Team?", name)
	}
	today := "Today the Team shares nothing yet."
	if t.Folder.shared() {
		today = "Today the Team shares only " + t.Folder.display() + "."
	}
	out.Sub = today + " After this, everyone in the Team reads and edits every file in this Crew."
	folders, count, bytes, measured := crewTeamWholeListing(ctx, t.OwnerID, t.ProjectID)
	if measured {
		for _, folder := range folders {
			if !strings.Contains(folder, "/") && !slices.Contains(t.Folder.Folders, folder) {
				out.FoldersAdded = append(out.FoldersAdded, folder+"/")
			}
		}
		if line := crewTeamTooBigLine("This Crew", count, bytes); line != "" {
			out.Refusal = line
			return out, nil
		}
		// What each person has already is what the Team's store holds of today's share.
		var shared int64
		if s, err := readCrewTeamFileState(t.ProjectID); err == nil {
			for _, v := range s.held(t.Folder) {
				shared += v.File.Size
			}
		}
		out.Means = append(out.Means, "The whole Crew: "+crewTeamSizeText(count, bytes)+" (Git history and package folders are never shared).",
			"The first sync copies about "+crewTeamBytesText(max(bytes-shared, 0))+" to each person's computer.")
	} else {
		out.Means = append(out.Means, "Your Mac is offline, so the whole Crew can't be measured now; its files start syncing when your Mac is back.")
	}
	home := "this Crew on the server"
	if crewRelayTwin(t.ProjectID) {
		home = "your Mac"
	}
	out.Means = append(out.Means, "Their edits come back to "+home+"; a same-line clash is fixed through an agent.", "Their agents can read these files when they ask your agents.")
	out.Note = "Still private: your chats and memory, app files, passwords and keys. You can go back to Folders any time; files already copied stay on their computers."
	out.ConfirmLabel = "Share the whole Crew"
	return out, nil
}

// handleCrewTeamFolderChange is GET /crew-teams/{project}/folder-change?whole=1|0[&folder=…], the confirm box for the
// Folders / Whole Crew switch. Only the Crew's main owner switches, so only they get it.
func (api *StreamingAPI) handleCrewTeamFolderChange(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, true)
	if !ok {
		return
	}
	if claims.UserID != t.OwnerID {
		externalError(w, http.StatusForbidden, "team_owner_only", fmt.Sprintf("Only %s can switch between Folders and the Whole Crew.", crewPersonShortName(t.OwnerID)))
		return
	}
	out, err := crewTeamFolderChangePreview(r.Context(), t, r.URL.Query().Get("whole") == "1", r.URL.Query()["folder"])
	if err != nil {
		externalError(w, http.StatusBadRequest, "no_change", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, out)
}
