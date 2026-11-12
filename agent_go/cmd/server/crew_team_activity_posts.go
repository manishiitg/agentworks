package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// crewTeamActivityKept is how many decisions, and separately findings, the Team record keeps.
const crewTeamActivityKept = 200

// Explicit Team posts, not excerpts of a person's private conversation. One
// Doing line per registry agent; the newest decisions/findings remain in the Team record.
type crewTeamActivityPost struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	AgentID  string    `json:"agent_id"`
	OwnerID  string    `json:"owner_id"`
	Agent    string    `json:"agent"`
	Person   string    `json:"person"`
	Line     string    `json:"line"`
	Why      string    `json:"why,omitempty"`
	Schedule string    `json:"schedule,omitempty"`
	At       time.Time `json:"at"`
}
type crewTeamActivityPosts struct {
	Doing     map[string]crewTeamActivityPost `json:"doing"`
	Decisions []crewTeamActivityPost          `json:"decisions"`
	Findings  []crewTeamActivityPost          `json:"findings"`
}

func readCrewTeamActivityPosts(project string) (crewTeamActivityPosts, error) {
	out := crewTeamActivityPosts{Doing: map[string]crewTeamActivityPost{}, Decisions: []crewTeamActivityPost{}, Findings: []crewTeamActivityPost{}}
	dir, err := crewTeamDir(project)
	if err != nil {
		return out, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "activity.json"))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	if out.Doing == nil {
		out.Doing = map[string]crewTeamActivityPost{}
	}
	return out, err
}

// The same writer is used by chat/MCP and the schedule runner. Schedule is
// supplied only by trusted scheduler code; the agent-facing route cannot set it.
func writeCrewTeamActivityPost(ctx context.Context, project, person, agentID, kind, line, why, schedule string) (crewTeamActivityPost, error) {
	var empty crewTeamActivityPost
	if kind != "doing" && kind != "decision" && kind != "finding" {
		return empty, fmt.Errorf("Choose doing or decision")
	}
	line, why = strings.TrimSpace(line), strings.TrimSpace(why)
	if line == "" || utf8.RuneCountInString(line) > 320 || strings.ContainsAny(line, "\r\n") || utf8.RuneCountInString(why) > 800 || strings.ContainsAny(why, "\r\n") || kind == "decision" && why == "" {
		return empty, fmt.Errorf("Use one short line (up to 320 characters), and give a short reason for a decision (up to 800 characters)")
	}
	if kind == "finding" && strings.TrimSpace(schedule) == "" {
		return empty, fmt.Errorf("A schedule finding needs its schedule name")
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return empty, err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	team, err := readCrewTeam(project)
	if err != nil || !crewTeamAgentsVisible(team, person) {
		return empty, fmt.Errorf("This Crew's Team is not available to you")
	}
	agent, ok := team.Agents[agentID]
	if !ok || agent.OwnerID != person || !agent.InTeam {
		return empty, fmt.Errorf("Only an In team agent can post its own Team activity")
	}
	posts, err := readCrewTeamActivityPosts(project)
	if err != nil {
		return empty, err
	}
	post := crewTeamActivityPost{ID: uuid.NewString(), Kind: kind, AgentID: agent.ID, OwnerID: person, Agent: agent.Name, Person: crewTeamFirstName(crewPersonShortName(person)), Line: line, Why: why, Schedule: strings.TrimSpace(schedule), At: time.Now().UTC()}
	switch kind {
	case "doing":
		posts.Doing[agent.ID] = post
	case "decision":
		posts.Decisions = append(posts.Decisions, post)
	case "finding":
		posts.Findings = append(posts.Findings, post)
	}
	// The record keeps the newest posts only; the Team card shows far fewer.
	posts.Decisions = posts.Decisions[max(0, len(posts.Decisions)-crewTeamActivityKept):]
	posts.Findings = posts.Findings[max(0, len(posts.Findings)-crewTeamActivityKept):]
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "activity.json"), posts, 0600); err != nil {
		return empty, err
	}
	log.Printf("[CREW_TEAM_ACTIVITY] project=%s event=posted kind=%s agent_id=%s caller_id=%s post_id=%s", project, kind, agent.ID, person, post.ID)
	publishCrewPanelChanged(project)
	go pushCrewTeamActivityChanged(team)
	return post, nil
}

// A retained post never bypasses current Team membership or Only me. Text is
// explicit Team communication, so chat history visibility is not used as a grant.
func crewTeamActivityPostsFor(team crewTeam) crewTeamActivityPosts {
	posts, err := readCrewTeamActivityPosts(team.ProjectID)
	if err != nil {
		log.Printf("[CREW_TEAM_ACTIVITY] project=%s event=read_failed", team.ProjectID)
		return crewTeamActivityPosts{Doing: map[string]crewTeamActivityPost{}, Decisions: []crewTeamActivityPost{}, Findings: []crewTeamActivityPost{}}
	}
	active := map[string]bool{}
	visible := func(post crewTeamActivityPost) bool {
		agent, ok := team.Agents[post.AgentID]
		enabled, known := active[agent.OwnerID]
		if !known {
			enabled = crewTeamAccountActive(agent.OwnerID)
			active[agent.OwnerID] = enabled
		}
		return team.On && ok && agent.InTeam && agent.OwnerID == post.OwnerID && team.member(agent.OwnerID) && enabled
	}
	for id, post := range posts.Doing {
		if !visible(post) {
			delete(posts.Doing, id)
		}
	}
	posts.Decisions = slices.DeleteFunc(posts.Decisions, func(post crewTeamActivityPost) bool { return !visible(post) })
	posts.Findings = slices.DeleteFunc(posts.Findings, func(post crewTeamActivityPost) bool { return !visible(post) })
	for i := range posts.Decisions {
		posts.Decisions[i].Person = crewTeamFirstName(posts.Decisions[i].Person)
	}
	for i := range posts.Findings {
		posts.Findings[i].Person = crewTeamFirstName(posts.Findings[i].Person)
	}
	newest := func(a, b crewTeamActivityPost) int { return b.At.Compare(a.At) }
	slices.SortFunc(posts.Decisions, newest)
	slices.SortFunc(posts.Findings, newest)
	posts.Decisions = posts.Decisions[:min(50, len(posts.Decisions))]
	posts.Findings = posts.Findings[:min(20, len(posts.Findings))]
	return posts
}

func (api *StreamingAPI) handleCrewTeamActivityPosts(w http.ResponseWriter, r *http.Request) {
	team, claims, ok := crewTeamForTab(w, r, r.Method == http.MethodPost)
	if !ok {
		return
	}
	if !team.On {
		externalError(w, 403, "team_off", "Turn on this Crew's Team first")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		externalJSON(w, crewTeamActivityPostsFor(team))
		return
	}
	var body struct {
		Kind     string `json:"kind"`
		AgentID  string `json:"agent_id"`
		Line     string `json:"line"`
		Why      string `json:"why"`
		Schedule string `json:"schedule"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body) != nil || (body.Kind != "doing" && body.Kind != "decision" && body.Kind != "finding") {
		externalError(w, 400, "invalid_activity", "Choose doing or decision and give one short line")
		return
	}
	// A finding comes from a schedule run on the owner's Mac: only that Mac's own call (the Crew's pair header) may post one,
	// and the schedule name is the one it sends. Any other caller gets the refusal line.
	if body.Kind == "finding" && (claims.UserID != team.OwnerID || !crewMacPairSent(r, team.ProjectID)) {
		externalError(w, 403, "activity_forbidden", crewFindingRefusal)
		return
	}
	// Resolve a current admitted registry identity, not an address alias or a
	// chosen display name. A person cannot post as another person's agent.
	agent, owner, err := crewTeamAgentByID(r.Context(), team.ProjectID, claims.UserID, body.AgentID)
	if err != nil || owner != claims.UserID || !agent.InTeam {
		externalError(w, 403, "activity_forbidden", "Only an In team agent can post its own Team activity")
		return
	}
	post, err := writeCrewTeamActivityPost(r.Context(), team.ProjectID, claims.UserID, agent.ID, body.Kind, body.Line, body.Why, activitySchedule(body.Kind, body.Schedule))
	if err != nil {
		log.Printf("[CREW_TEAM_ACTIVITY] project=%s event=post_failed agent_id=%s", team.ProjectID, agent.ID)
		externalError(w, 400, "activity_failed", "Could not post. Use one short line and give a short reason for a decision.")
		return
	}
	externalJSON(w, post)
}

// Reuse the existing Team-view invalidation notice, with IDs only. No second
// activity cache or file-sync round is needed on a member's Mac.
func pushCrewTeamActivityChanged(team crewTeam) {
	crewMacSockets.Lock()
	targets := []*crewMacSocket{}
	for _, sock := range crewMacSockets.byDevice {
		if sock.send != nil && team.On && team.member(sock.owner) {
			targets = append(targets, sock)
		}
	}
	crewMacSockets.Unlock()
	sent := 0
	active := map[string]bool{}
	for _, sock := range targets {
		enabled, known := active[sock.owner]
		if !known {
			enabled = crewTeamAccountActive(sock.owner)
			active[sock.owner] = enabled
		}
		if enabled && sock.send(crewRelayMessage{Type: "team.busy", Project: team.ProjectID}) == nil {
			sent++
		}
	}
	log.Printf("[CREW_TEAM_ACTIVITY] project=%s event=notice_pushed macs=%d", team.ProjectID, sent)
}

// Only a finding carries a schedule name; a Doing or Decision post never does, whatever the caller sent.
func activitySchedule(kind, schedule string) string {
	if kind == "finding" {
		return schedule
	}
	return ""
}
