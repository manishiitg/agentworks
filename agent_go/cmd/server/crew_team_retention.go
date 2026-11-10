package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"time"
)

var errCrewTeamHistoryCatchUp = errors.New("This copy needs the current Team files before sending more changes. Refresh Team sync and try again.")

// Keep a complete recent revision range (30 days or 500 events, whichever
// reaches further back), plus the exact bases of current files and open clashes.
// Protected old rows do not lower the ordinary readable-history boundary.
func (s *crewTeamFileState) retainHistory(now time.Time) (int, int) {
	if len(s.Events) == 0 {
		return 0, 0
	}
	cutoff := now.Add(-30 * 24 * time.Hour)
	first := len(s.Events)
	for i, e := range s.Events {
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if i >= len(s.Events)-500 || err != nil || !at.Before(cutoff) {
			first = i
			break
		}
	}
	boundary := s.Events[first].Revision
	for first > 0 && s.Events[first-1].Revision >= boundary {
		first--
	}
	protected := map[int]bool{}
	byPath := map[string][]int{}
	for i, e := range s.Events {
		byPath[e.Path] = append(byPath[e.Path], i)
	}
	atVersion := func(path string, revision uint64) int {
		indices := byPath[path]
		return sort.Search(len(indices), func(i int) bool { return s.Events[indices[i]].Revision > revision }) - 1
	}
	// Every revision above the floor must remain reconstructible, including
	// a file unchanged between the boundary and its next retained update.
	// Current/previous versions alone do not preserve that starting base.
	floor := s.HistoryFloor
	if first > 0 && boundary > 0 {
		floor = max(floor, boundary-1)
	}
	for path, indices := range byPath {
		if position := atVersion(path, floor); position >= 0 {
			protected[indices[position]] = true
		}
	}
	for path, v := range s.Versions {
		position := atVersion(path, v.Revision)
		if position < 0 {
			continue
		}
		indices := byPath[path]
		i := indices[position]
		protected[i] = true
		// A current file's previous distinct version is its immediate merge base.
		for j := position - 1; j >= 0; j-- {
			previous := indices[j]
			if !crewTeamSame(s.Events[previous].File, s.Events[i].File) {
				protected[previous] = true
				break
			}
		}
	}
	for _, c := range s.Clashes {
		if !c.Resolved {
			for _, rev := range []uint64{c.WinningRevision, c.LosingRevision} {
				if position := atVersion(c.Path, rev); position >= 0 {
					protected[byPath[c.Path][position]] = true
				}
			}
		}
	}
	kept := make([]crewTeamEvent, 0, len(s.Events)-first+len(protected))
	removed, bytes := 0, 0
	for i, e := range s.Events {
		if i >= first || protected[i] {
			kept = append(kept, e)
		} else {
			removed++
			raw, _ := json.Marshal(e)
			bytes += len(raw)
		}
	}
	s.Events = kept
	if removed > 0 && boundary > 0 && boundary-1 > s.HistoryFloor {
		s.HistoryFloor = boundary - 1
	}
	return removed, bytes
}

// This is the complete current manifest for this caller, including deletions.
// As everywhere else, explicit file choices, Team folders and token guards apply.
func crewTeamCatchUpManifest(claims *UserClaims, team crewTeam, state crewTeamFileState) crewTeamChangesReply {
	out := crewTeamChangesReply{Revision: state.Revision, Through: state.Revision, CatchUp: true, Files: []crewTeamRemoteFile{}, OnPurpose: sortedOnPurpose(state.onPurpose())}
	op := state.onPurpose()
	for path, v := range state.Versions {
		if !crewTeamAllowed(path, op) || !team.Folder.holds(path) || !crewTeamGuardAllows(claims, path, false) {
			continue
		}
		f := crewTeamRemoteFile{Path: path}
		if v.File != nil {
			f.Version = &chunkedFile{Hash: v.File.Hash, Size: v.File.Size, Exec: v.File.Executable, Chunks: v.Chunks}
		}
		out.Files = append(out.Files, f)
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	return out
}

func writeCrewTeamCatchUp(w http.ResponseWriter, project string, claims *UserClaims, team crewTeam, state crewTeamFileState, requested uint64) {
	out := crewTeamCatchUpManifest(claims, team, state)
	log.Printf("[CREW_TEAM_LEDGER] project=%s event=history_catch_up status=409 requested_revision=%d current_revision=%d history_floor=%d", project, requested, state.Revision, state.HistoryFloor)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "team_history_catch_up", "message": errCrewTeamHistoryCatchUp.Error()}, "revision": state.Revision, "catch_up": true, "manifest": out.Files})
}
