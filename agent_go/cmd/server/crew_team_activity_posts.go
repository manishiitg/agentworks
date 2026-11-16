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

// crewTeamActivityKept is how many schedule findings the Team record keeps.
const crewTeamActivityKept = 200

// Explicit Team posts, not excerpts of a person's private conversation. Only
// schedule findings are stored here; the Doing line per agent and the decisions
// come from the work log (crew_work_log.go).
type crewTeamActivityPost struct {
	NeedsFor string `json:"-"` // derived work-log recipient, not persisted

	Private  bool   `json:"private,omitempty"`
	ChatKey  string `json:"chat_key,omitempty"`
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	AgentID  string `json:"agent_id"`
	OwnerID  string `json:"owner_id"`
	Agent    string `json:"agent"`
	Person   string `json:"person"`
	Line     string `json:"line"`
	Why      string `json:"why,omitempty"`
	Schedule string `json:"schedule,omitempty"`
	// Task is the work-log task's label and TaskTitle its title, for entries about a task.
	Task      string    `json:"task,omitempty"`
	TaskTitle string    `json:"task_title,omitempty"`
	TaskState string    `json:"task_state,omitempty"`
	Event     string    `json:"event,omitempty"`
	At        time.Time `json:"at"`
}
type crewTeamActivityPosts struct {
	Doing     map[string]crewTeamActivityPost `json:"doing"`
	Decisions []crewTeamActivityPost          `json:"decisions"`
	Findings  []crewTeamActivityPost          `json:"findings"`
	// Work is the work log's task events (added, started, done) this viewer may see.
	Work []crewTeamActivityPost `json:"work,omitempty"`
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
	team, err := readCrewTeam(project)
	if err != nil {
		return crewTeamActivityPost{}, err
	}
	agent, ok := team.Agents[agentID]
	if !ok || agent.OwnerID != person {
		return crewTeamActivityPost{}, fmt.Errorf("Post activity from your own agent in this Crew")
	}
	return writeCrewTeamActivityPostAs(ctx, project, person, agent, kind, line, why, schedule)
}

// actor is resolved by the session boundary, never from a model-chosen identity.
func writeCrewTeamActivityPostAs(ctx context.Context, project, person string, actor crewTeamRegisteredAgent, kind, line, why, schedule string) (crewTeamActivityPost, error) {
	var empty crewTeamActivityPost
	if kind != "finding" {
		return empty, fmt.Errorf("Post work through the work log (work_update, work_decide)")
	}
	line, why = strings.TrimSpace(line), strings.TrimSpace(why)
	if line == "" || utf8.RuneCountInString(line) > 320 || strings.ContainsAny(line, "\r\n") {
		return empty, fmt.Errorf("Use one short line (up to 320 characters)")
	}
	if strings.TrimSpace(schedule) == "" {
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
	agent, registered := team.Agents[actor.ID]
	if !registered {
		agent = actor
		agent.InTeam = false
	}
	if agent.OwnerID != person || agent.ChatKey == "" || isSharedThreadKey(agent.ChatKey) {
		return empty, fmt.Errorf("Post activity from your own agent in this Crew")
	}
	private := !registered || !agent.InTeam
	if actor.InTeam && private {
		return empty, fmt.Errorf("The agent is no longer shared with the team. Try again from its own chat")
	}

	posts, err := readCrewTeamActivityPosts(project)
	if err != nil {
		return empty, err
	}
	post := crewTeamActivityPost{ID: uuid.NewString(), Kind: kind, Private: private, ChatKey: agent.ChatKey, AgentID: agent.ID, OwnerID: person, Agent: agent.Name, Person: crewTeamFirstName(crewPersonShortName(person)), Line: line, Schedule: strings.TrimSpace(schedule), At: time.Now().UTC()}
	// The record keeps the newest findings only; the Team card shows far fewer.
	findings := append(posts.Findings, post)
	stored := crewTeamActivityPosts{Doing: map[string]crewTeamActivityPost{}, Decisions: []crewTeamActivityPost{}, Findings: findings[max(0, len(findings)-crewTeamActivityKept):]}
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "activity.json"), stored, 0600); err != nil {
		return empty, err
	}
	log.Printf("[CREW_TEAM_ACTIVITY] project=%s event=posted kind=%s agent_id=%s caller_id=%s post_id=%s", project, kind, agent.ID, person, post.ID)
	publishCrewPanelChanged(project)
	go pushCrewTeamActivityChanged(team)
	return post, nil
}

// Private posts are owner-only, even if the agent is later shared. Public posts
// still require current Team membership and In-team admission. Text is
// explicit Team communication, so chat history visibility is not used as a grant.
func crewTeamActivityPostsFor(team crewTeam, viewer string) crewTeamActivityPosts {
	posts, err := readCrewTeamActivityPosts(team.ProjectID)
	if err != nil {
		log.Printf("[CREW_TEAM_ACTIVITY] project=%s event=read_failed", team.ProjectID)
		return crewTeamActivityPosts{Doing: map[string]crewTeamActivityPost{}, Decisions: []crewTeamActivityPost{}, Findings: []crewTeamActivityPost{}}
	}
	posts.Doing, posts.Decisions, posts.Work = crewWorkActivityViews(crewWorkScope{Project: team.ProjectID, Team: &team}, viewer)
	allowed := crewWorkVisible(team, viewer)
	visible := func(post crewTeamActivityPost) bool { return allowed(post.Private, post.AgentID, post.OwnerID) }
	posts.Findings = slices.DeleteFunc(posts.Findings, func(post crewTeamActivityPost) bool { return !visible(post) })
	labels := crewTeamPersonLabels(team)
	for i := range posts.Findings {
		posts.Findings[i].Person = labels[posts.Findings[i].OwnerID]
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
		externalJSON(w, crewTeamActivityPostsFor(team, claims.UserID))
		return
	}
	var body struct {
		Kind      string `json:"kind"`
		AgentID   string `json:"agent_id"`
		Line      string `json:"line"`
		Why       string `json:"why"`
		Schedule  string `json:"schedule"`
		ChatKey   string `json:"chat_key"`
		Agent     string `json:"agent"`
		SharedAsk bool   `json:"shared_ask"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body) != nil || body.Kind != "finding" {
		externalError(w, 400, "invalid_activity", "Post work through the work log (work_update, work_decide); this route takes schedule findings only")
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
	actor, err := api.crewTeamActivityActorForRequest(r, team, claims.UserID, body.AgentID, body.ChatKey, body.Agent)
	if err != nil || body.SharedAsk && !actor.InTeam {
		externalError(w, 403, "activity_forbidden", "Post activity from your own agent in this Crew")
		return
	}

	post, err := writeCrewTeamActivityPostAs(r.Context(), team.ProjectID, claims.UserID, actor, body.Kind, body.Line, body.Why, activitySchedule(body.Kind, body.Schedule))
	if err != nil {
		log.Printf("[CREW_TEAM_ACTIVITY] project=%s event=post_failed agent_id=%s", team.ProjectID, actor.ID)
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
