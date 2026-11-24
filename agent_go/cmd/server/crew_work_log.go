package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The one entry point the screen, chat, MCP and native agents share for a
// Team's asks: work_brief at the start of a session (the asks an agent has not
// finished and the requests waiting on its person) and the person's answer to a
// Needs-you card (work_answer). Tracking work is the project's own issue tracker
// (owner, 19 Nov).

// crewWorkPersonPrefix marks a person acting over MCP rather than through one of
// their agents (Claude Cowork, ChatGPT): ChatKey is this prefix plus their user
// ID, never a model-chosen value.
const crewWorkPersonPrefix = "person:"

// crewWorkScope is a Crew and, when it is on, its Team. With the Team off a
// person sees only their own agents' asks.
type crewWorkScope struct {
	Project string
	Team    *crewTeam
}

var crewWorkActions = []string{"work_brief", "work_answer"}

type crewWorkInput struct {
	Op      string `json:"op"`
	Query   string `json:"query"`
	Need    string `json:"need"`
	Button  string `json:"button"`
	Helper  string `json:"helper"`
	Comment string `json:"comment"`
}

// crewWorkDo is the one path for the screen route, chat/MCP and native agents.
// actor comes from the session boundary, never from the model.
func crewWorkDo(ctx context.Context, scope crewWorkScope, person string, actor crewTeamRegisteredAgent, in crewWorkInput) (any, error) {
	if !slices.Contains(crewWorkActions, in.Op) {
		return nil, fmt.Errorf("Choose one of %s", strings.Join(crewWorkActions, ", "))
	}
	agent := actor
	if actor.OwnerID != person || actor.ChatKey == "" {
		return nil, fmt.Errorf("Use the Team from your own agent in this project")
	}
	if team := scope.Team; team != nil {
		if !crewTeamAgentsVisible(*team, person) {
			return nil, fmt.Errorf("This project's Team is not available to you")
		}
		if actor.ID != "" || actor.ChatKey != crewWorkPersonPrefix+person {
			registered := false
			agent, registered = team.Agents[actor.ID]
			if !registered {
				agent = actor
				agent.InTeam = false
			}
			if agent.OwnerID != person || agent.ChatKey == "" {
				return nil, fmt.Errorf("Use the Team from your own agent in this project")
			}
			if actor.InTeam && (!registered || !agent.InTeam) {
				return nil, fmt.Errorf("The agent is no longer shared with the team. Try again from its own chat")
			}
		}
	}
	switch {
	case in.Op == "work_answer":
		if scope.Team == nil {
			return nil, fmt.Errorf("Needs-you cards show in the Team panel, so they need this project's Team. Turn the Team on first")
		}
		// Resolved first: reading the person's own agents may touch the Team record.
		helper, choose, err := crewWorkAnswerHelper(ctx, *scope.Team, person, in)
		if err != nil {
			return nil, err
		}
		if choose != nil {
			return choose, nil
		}
		need := strings.TrimSpace(in.Need)
		if need == "" {
			return nil, fmt.Errorf("Say which request you are answering: its id from the card")
		}
		if runningServerAPI == nil {
			return nil, fmt.Errorf("The server is starting; try again in a moment")
		}
		// The answer is the person's own, whichever of their chats clicked; it is the reply in the request's thread.
		return runningServerAPI.answerAgentPersonRequest(ctx, *scope.Team, person, need, in.Button, in.Comment, helper)
	}
	return crewWorkBrief(ctx, scope, person, agent), nil
}

// crewWorkBrief is what an agent reads at the start of a session: the asks sent
// to it that it has not finished, and the requests waiting on its own person.
func crewWorkBrief(ctx context.Context, scope crewWorkScope, person string, agent crewTeamRegisteredAgent) map[string]any {
	needYou := []*crewWorkNeed{}
	var myTasks []map[string]any
	if scope.Team != nil {
		needYou = crewWorkNeedsFromRequests(agentTeamRequests(ctx, *scope.Team, person), person)
		if store, err := readAgentMessageSnapshot(ctx); err == nil {
			myTasks = crewTasksBrief(crewTeamTasks(ctx, *scope.Team, person, store), func(t crewTeamTask) bool { return agent.ID != "" && t.AgentKey == agent.ID })
		}
	} else {
		myTasks = crewTasksBrief(crewPersonalTasks(ctx, scope.Project, person), func(t crewTeamTask) bool { return t.AgentKey == agent.ChatKey })
	}
	return map[string]any{
		"my_tasks":               myTasks,
		"waiting_on_your_person": needYou,
		"hint":                   "my_tasks are asks sent to you that you have not finished: answer in their inbox_id with messages action=send (status=in_progress while you work, then done). Hand work to another agent with messages ask. Blocked on a person? Send to target=person:<name> with one to three buttons; their click comes back as the reply. Record progress and decisions in the project's own issue tracker (GitHub, Jira or its files) with your own tools.",
	}
}

// crewWorkVisible applies the Team post rule: an Only-me entry is its owner's;
// a shared entry needs its agent still In team and its owner still a member. A
// person's own MCP entry needs that person still a member.
func crewWorkVisible(team crewTeam, viewer string) func(private bool, agentID, ownerID string) bool {
	active := map[string]bool{}
	return func(private bool, agentID, ownerID string) bool {
		if private {
			return team.On && viewer != "" && viewer == ownerID && team.member(viewer) && crewTeamAccountActive(viewer)
		}
		if agentID == "" {
			return team.On && team.member(ownerID) && crewTeamAccountActive(ownerID)
		}
		agent, ok := team.Agents[agentID]
		enabled, known := active[agent.OwnerID]
		if !known {
			enabled = crewTeamAccountActive(agent.OwnerID)
			active[agent.OwnerID] = enabled
		}
		return team.On && ok && agent.InTeam && agent.OwnerID == ownerID && team.member(agent.OwnerID) && enabled
	}
}

// handleCrewWorkLog is the Team asks route: GET op=work_brief, POST work_answer.
func (api *StreamingAPI) handleCrewWorkLog(rw http.ResponseWriter, r *http.Request) {
	w := &crewWorkRefusalLog{ResponseWriter: rw}
	defer w.log(r)
	w.peek(r)
	team, claims, ok := crewTeamForTab(w, r, r.Method == http.MethodPost)
	if !ok {
		return
	}
	if !team.On {
		externalError(w, 403, "team_off", "Turn on this project's Team first")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		crewWorkInput
		AgentID   string `json:"agent_id"`
		ChatKey   string `json:"chat_key"`
		Agent     string `json:"agent"`
		SharedAsk bool   `json:"shared_ask"`
	}
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		body.Op, body.Query, body.AgentID, body.ChatKey, body.Agent = q.Get("op"), q.Get("query"), q.Get("agent_id"), q.Get("chat_key"), q.Get("agent")
		body.SharedAsk = q.Get("shared_ask") == "1"
		if body.Op != "work_brief" {
			externalError(w, 400, "invalid_work", "Read with work_brief")
			return
		}
	} else if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&body) != nil || body.Op == "work_brief" {
		externalError(w, 400, "invalid_work", "Answer a Needs-you card with work_answer")
		return
	}
	var actor crewTeamRegisteredAgent
	var err error
	if body.AgentID == "" && (body.ChatKey == crewWorkPersonPrefix+claims.UserID || body.ChatKey == "" && body.Op == "work_answer") {
		actor = crewWorkPersonActor(claims.UserID) // a person over MCP forwarded from their Mac, or a Needs-you button click
	} else if actor, err = api.crewTeamActivityActorForRequest(r, team, claims.UserID, body.AgentID, body.ChatKey, body.Agent); err != nil || body.SharedAsk && !actor.InTeam {
		externalError(w, 403, "work_forbidden", "Use the Team from your own agent in this project")
		return
	}
	out, err := crewWorkDo(r.Context(), crewWorkScope{Project: team.ProjectID, Team: &team}, claims.UserID, actor, body.crewWorkInput)
	if err != nil {
		externalError(w, 400, "work_refused", err.Error())
		return
	}
	externalJSON(w, out)
}

// crewWorkRefusalLog writes one log line per refused Team asks call (#3295): the
// server used to log only the status, so a burst of refusals had no cause.
// It logs only the kind of caller and request, never the body or a chat key.
type crewWorkRefusalLog struct {
	http.ResponseWriter
	status  int
	body    []byte
	op      string
	agentID bool
	chatKey string
}

func (w *crewWorkRefusalLog) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *crewWorkRefusalLog) Write(b []byte) (int, error) {
	if w.status >= 400 && len(w.body) < 512 {
		w.body = append(w.body, b[:min(len(b), 512-len(w.body))]...)
	}
	return w.ResponseWriter.Write(b)
}

// peek reads what kind of call this is before any check refuses it; the body is
// put back unchanged for the handler.
func (w *crewWorkRefusalLog) peek(r *http.Request) {
	var in struct {
		Op      string `json:"op"`
		AgentID string `json:"agent_id"`
		ChatKey string `json:"chat_key"`
	}
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		in.Op, in.AgentID, in.ChatKey = q.Get("op"), q.Get("agent_id"), q.Get("chat_key")
	} else if r.Body != nil {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 128<<10))
		r.Body = io.NopCloser(bytes.NewReader(raw))
		_ = json.Unmarshal(raw, &in)
	}
	person := ""
	if c := GetUserFromContext(r.Context()); c != nil {
		person = c.UserID
	}
	w.op, w.agentID, w.chatKey = in.Op, in.AgentID != "", crewWorkChatKeyKind(in.ChatKey, person)
}

func (w *crewWorkRefusalLog) log(r *http.Request) {
	if w.status < 400 {
		return
	}
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.body, &out)
	actor := "-"
	if c := GetUserFromContext(r.Context()); c != nil {
		actor = logUsernameForUserID(c.UserID)
	}
	ua := r.Header.Get("User-Agent")
	if len(ua) > 60 {
		ua = ua[:60]
	}
	log.Printf("[CREW_TEAM_WORK] project=%s actor=%s method=%s op=%q status=%d code=%s agent_id=%t chat_key=%s folder_id=%t mac_pair=%t ua=%q",
		mux.Vars(r)["project"], actor, r.Method, w.op, w.status, out.Error.Code, w.agentID, w.chatKey, r.URL.Query().Get("folder_id") != "", r.Header.Get(crewMacPairHeader) != "", ua)
}

// crewWorkChatKeyKind names the kind of chat key a call sent, for the refusal log.
func crewWorkChatKeyKind(key, person string) string {
	switch {
	case key == "":
		return "none"
	case key == crewWorkPersonPrefix+person:
		return "person"
	case strings.HasPrefix(key, crewWorkPersonPrefix):
		return "other_person"
	default:
		return "agent"
	}
}

func crewWorkPersonActor(person string) crewTeamRegisteredAgent {
	return crewTeamRegisteredAgent{OwnerID: person, ChatKey: crewWorkPersonPrefix + person, Name: crewTeamFirstName(crewPersonShortName(person))}
}

// crewWorkTeamOff reports whether this Crew's asks are personal: its Team was
// never set up or is turned off. On a Mac, a Crew paired with the server reads
// its Team there; an unreachable server is not treated as "off".
func crewWorkTeamOff(ctx context.Context, person, project string) (bool, error) {
	if !IsLocalMode() {
		team, err := readCrewTeam(project)
		return err != nil || !team.On, nil
	}
	b, err := crewroot.ReadSync(project)
	if err != nil {
		return false, err
	}
	if b == nil || b.Side != "local" || b.PeerProjectID == "" {
		return true, nil
	}
	c, err := desktopConnectionFor(person, b.ConnectionID)
	if err != nil {
		return false, err
	}
	var team crewTeam
	if err := crewTeamRemoteJSON(ctx, person, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(b.PeerProjectID), nil, &team); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return true, nil
		}
		return false, err
	}
	return !team.On, nil
}

// crewWorkPersonal runs work_brief on a Crew whose Team is off, for the
// person's own chat (chat) or for the person over MCP (person actor).
func crewWorkPersonal(ctx context.Context, project, person string, actor crewTeamRegisteredAgent, args map[string]any) (string, error) {
	out, err := crewWorkDo(ctx, crewWorkScope{Project: project}, person, actor, crewWorkInputFromArgs(args))
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(out)
	return string(raw), nil
}

func crewWorkInputFromArgs(args map[string]any) crewWorkInput {
	in := crewWorkInput{Op: externalArg(args, "action")}
	if op := externalArg(args, "team_action"); op != "" {
		in.Op = op
	}
	for key, field := range map[string]*string{"query": &in.Query, "need": &in.Need, "button": &in.Button, "helper": &in.Helper, "comment": &in.Comment} {
		*field = externalArg(args, key)
	}
	return in
}
