package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

const crewTeamActivityMessages = 20

type crewTeamActivityView struct {
	OnlineItems    []teamOnlineItemMapping `json:"-"`
	Decisions      []crewTeamActivityPost  `json:"-"`
	Findings       []crewTeamActivityPost  `json:"-"`
	Work           []crewTeamActivityPost  `json:"-"`
	BridgeMessages []crewTeamFeedItem      `json:"-"`
	Feed           []crewTeamFeedItem      `json:"feed"`
	NextCursor     string                  `json:"next_cursor,omitempty"`
	Unseen         int                     `json:"unseen"`
	Today          int                     `json:"today"`
}

func crewTeamActivityFor(ctx context.Context, t crewTeam, caller string) crewTeamActivityView {
	return crewTeamActivityPage(ctx, t, caller, "")
}
func crewTeamActivityPage(ctx context.Context, t crewTeam, caller, before string) crewTeamActivityView {
	posts := crewTeamActivityPostsFor(t, caller)
	out := crewTeamActivityView{Decisions: posts.Decisions, Findings: posts.Findings, Work: posts.Work}
	if store, err := readAgentMessageSnapshot(ctx); err == nil {
		out.BridgeMessages = crewTeamBridgeActivity(ctx, t, caller, store)
	}
	rows := crewTeamActivityFeed(out, caller)
	marks, err := crewTeamSeenMarks(t.ProjectID)
	if err != nil {
		log.Printf("[CREW_TEAM_ACTIVITY] project=%s event=seen_read_failed", t.ProjectID)
	}
	mark := marks[caller]
	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	messages, decisions := 0, 0
	for i := range rows {
		rows[i].Unseen = crewTeamAfterSeen(rows[i], mark)
		if rows[i].Unseen {
			out.Unseen++
		}
		if !rows[i].At.Before(midnight) {
			rows[i].Today = true
			out.Today++
		}
		if rows[i].Kind == "message" {
			messages++
		}
		if rows[i].Kind == "decision" {
			decisions++
		}
	}
	out.Feed, out.NextCursor = crewTeamFeedPage(rows, before)
	log.Printf("[CREW_TEAM_ACTIVITY] project=%s caller=%s event=listed messages=%d decisions=%d unseen=%d", t.ProjectID, caller, messages, decisions, out.Unseen)
	return out
}

// A browser read of a bridge thread is narrower than the Team preview: one of
// the two source-agent owners, current membership/audience and exact binding.
func crewTeamActivityThread(ctx context.Context, t crewTeam, caller, inbox string) ([]map[string]any, bool) {
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return nil, false
	}
	for _, c := range store.Conversations {
		if c.ID != inbox {
			continue
		}
		out := []map[string]any{}
		transport := map[string]bool{}
		for _, m := range c.Messages {
			rows, ok := crewTeamActivityBridgeRows(ctx, t, caller, c, m, transport)
			if !ok {
				continue
			}
			if caller != rows[0].OwnerID && caller != rows[1].OwnerID {
				return nil, false
			}
			from := rows[m.From]
			out = append(out, map[string]any{"id": m.ID, "at": m.SentAt, "agent": firstNonEmptyTrimmed(from.Name, from.Tab), "text": m.Text})
		}
		return out, len(out) > 0
	}
	return nil, false
}

func (api *StreamingAPI) handleCrewTeamActivity(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if !crewTeamClaims(claims, project, false) {
		externalError(w, 403, "team_forbidden", "Use your own account to open this team.")
		return
	}
	t, err := readCrewTeam(project)
	if err != nil || !crewTeamReachOpen(t, claims.UserID) {
		externalError(w, 403, "team_forbidden", "This team is not shared with you. Ask its owner to add you.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if inbox := r.URL.Query().Get("thread"); inbox != "" {
		if claims.AccessToken != nil || claims.Scope != "" {
			externalError(w, 403, "thread_forbidden", "Open your own agent's thread in AgentWorks.")
			return
		}
		rows, ok := crewTeamActivityThread(r.Context(), t, claims.UserID, inbox)
		if !ok {
			externalError(w, 403, "thread_forbidden", "Only the two agents' owners can open this thread while it remains shared.")
			return
		}
		externalJSON(w, map[string]any{"messages": rows})
		return
	}
	if r.Method == http.MethodPost {
		if claims.AccessToken != nil || claims.Scope != "" {
			externalError(w, 403, "seen_forbidden", "Mark activity seen from your own AgentWorks screen.")
			return
		}
		var body struct {
			Through string `json:"seen_through"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
			externalError(w, 400, "invalid_seen", "Choose the activity you opened.")
			return
		}
		// Resolve the supplied identity against this caller's current admitted feed.
		dir, e := crewTeamDir(project)
		if e != nil {
			externalError(w, 400, "invalid_team", "Choose a Team.")
			return
		}
		mu := crewTeamRecordMutex(dir)
		mu.Lock()
		defer mu.Unlock()
		t, e = readCrewTeam(project)
		if e != nil || !crewTeamReachOpen(t, claims.UserID) {
			externalError(w, 403, "team_forbidden", "This Team is unavailable.")
			return
		}
		posts := crewTeamActivityPostsFor(t, claims.UserID)
		view := crewTeamActivityView{Decisions: posts.Decisions, Findings: posts.Findings, Work: posts.Work}
		if snapshot, e := readAgentMessageSnapshot(r.Context()); e == nil {
			view.BridgeMessages = crewTeamBridgeActivity(r.Context(), t, claims.UserID, snapshot)
		}
		marks, e := crewTeamSeenMarks(project)
		if e != nil {
			externalError(w, 500, "seen_unavailable", "Could not save your activity position.")
			return
		}
		found := false
		for _, row := range crewTeamActivityFeed(view, claims.UserID) {
			if row.ID != body.Through {
				continue
			}
			found = true
			if crewTeamAfterSeen(row, marks[claims.UserID]) {
				marks[claims.UserID] = crewTeamSeenMark{At: row.At, ID: row.ID}
			}
			break
		}
		if !found {
			externalError(w, 409, "seen_changed", "Activity changed; refresh it first.")
			return
		}
		if e = fsutil.WriteJSONAtomic(filepath.Join(dir, "activity-seen.json"), marks, 0600); e != nil {
			externalError(w, 500, "seen_unavailable", "Could not save your activity position.")
			return
		}
		externalJSON(w, map[string]any{"seen": true})
		return
	}
	externalJSON(w, crewTeamActivityPage(r.Context(), t, claims.UserID, r.URL.Query().Get("before")))
}
