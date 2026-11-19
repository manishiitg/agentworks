package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Team tasks are AgentBridge request threads (#2965); the old per-Crew work log
// (#2871, work.jsonl) is gone. What stays here is the one entry point the screen,
// chat, MCP and native agents share: work_brief at the start of a session, the
// person's answer to a Needs-you card (work_answer), and tasks, task and
// task_update (crew_team_task_tools.go).

// crewWorkPersonPrefix marks a person acting over MCP rather than through one of
// their agents (Claude Cowork, ChatGPT): ChatKey is this prefix plus their user
// ID, never a model-chosen value.
const crewWorkPersonPrefix = "person:"

// crewWorkScope is a Crew and, when it is on, its Team. With the Team off a
// person sees only their own tasks.
type crewWorkScope struct {
	Project string
	Team    *crewTeam
}

var crewWorkActions = []string{"work_brief", "work_answer", "tasks", "task", "task_update", "features", "feature", "feature_join", "feature_update"}

type crewWorkInput struct {
	Op      string `json:"op"`
	Task    string `json:"task"`
	Line    string `json:"line"`
	Why     string `json:"why"`
	Query   string `json:"query"`
	Need    string `json:"need"`
	Button  string `json:"button"`
	Helper  string `json:"helper"`
	Comment string `json:"comment"`
	// tasks, task, task_update (crew_team_task_tools.go).
	Status   string   `json:"status"`
	Category string   `json:"category"`
	Tags     []string `json:"tags"`
	Decision bool     `json:"decision"`
	FileName string   `json:"file_name"`
	FileText string   `json:"file_text"`
	State    string   `json:"state"`
	Tag      string   `json:"tag"`
	Issue    string   `json:"issue"`
	Who      string   `json:"who"`
	Mine     bool     `json:"mine"`
	Limit    int      `json:"limit"`
	// features, feature, feature_join, feature_update (crew_features.go); tasks and
	// task_update take feature too.
	Feature string `json:"feature"`
	Link    string `json:"link"`
	Leave   bool   `json:"leave"`
}

// crewWorkDo is the one path for the screen route, chat/MCP and native agents.
// actor comes from the session boundary, never from the model.
func crewWorkDo(ctx context.Context, scope crewWorkScope, person string, actor crewTeamRegisteredAgent, in crewWorkInput) (any, error) {
	if !slices.Contains(crewWorkActions, in.Op) {
		return nil, fmt.Errorf("Choose one of %s", strings.Join(crewWorkActions, ", "))
	}
	agent := actor
	if actor.OwnerID != person || actor.ChatKey == "" || isSharedThreadKey(actor.ChatKey) {
		return nil, fmt.Errorf("Use Team tasks from your own agent in this Crew")
	}
	if team := scope.Team; team != nil {
		if !crewTeamAgentsVisible(*team, person) {
			return nil, fmt.Errorf("This Crew's Team is not available to you")
		}
		if actor.ID != "" || actor.ChatKey != crewWorkPersonPrefix+person {
			registered := false
			agent, registered = team.Agents[actor.ID]
			if !registered {
				agent = actor
				agent.InTeam = false
			}
			if agent.OwnerID != person || agent.ChatKey == "" || isSharedThreadKey(agent.ChatKey) {
				return nil, fmt.Errorf("Use Team tasks from your own agent in this Crew")
			}
			if actor.InTeam && (!registered || !agent.InTeam) {
				return nil, fmt.Errorf("The agent is no longer shared with the team. Try again from its own chat")
			}
		}
	}
	switch {
	case in.Op == "work_answer":
		if scope.Team == nil {
			return nil, fmt.Errorf("Needs-you cards show in the Team panel, so they need this Crew's Team. Turn the Team on first")
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
	case crewTaskOp(in.Op) || crewFeatureOp(in.Op):
		// Tasks and features need no Team: with the Team off they are the person's own (#3210).
		store, err := readAgentMessageSnapshot(ctx)
		if err != nil {
			return nil, err
		}
		team, tasks := crewTeam{ProjectID: scope.Project, OwnerID: person}, []crewTeamTask(nil)
		if scope.Team != nil {
			team = *scope.Team
			tasks = crewTeamTasks(ctx, team, person, store)
		} else {
			tasks = crewPersonalTasks(ctx, scope.Project, person)
		}
		if crewFeatureOp(in.Op) {
			return crewFeatureDo(scope.Project, person, agent, tasks, store, in)
		}
		return crewTaskDo(ctx, team, person, agent, tasks, store, in)
	}
	return crewWorkBrief(ctx, scope, person, agent), nil
}

// crewWorkBrief is what an agent reads at the start of a session: the request
// threads it is doing, open Team requests any agent may take, and the requests
// waiting on its own person.
func crewWorkBrief(ctx context.Context, scope crewWorkScope, person string, agent crewTeamRegisteredAgent) map[string]any {
	teamRequests, needYou := []agentTeamRequest{}, []*crewWorkNeed{}
	var myTasks []map[string]any
	var tasks []crewTeamTask
	if scope.Team != nil {
		requests := agentTeamRequests(ctx, *scope.Team, person)
		for _, r := range requests {
			if r.For == "team" {
				teamRequests = append(teamRequests, r)
			}
		}
		for _, n := range crewWorkNeedsFromRequests(requests) {
			if n.ForMe {
				needYou = append(needYou, n)
			}
		}
		if store, err := readAgentMessageSnapshot(ctx); err == nil {
			tasks = crewTeamTasks(ctx, *scope.Team, person, store)
			myTasks = crewTasksBrief(tasks, func(t crewTeamTask) bool { return agent.ID != "" && t.AgentKey == agent.ID })
		}
	} else {
		tasks = crewPersonalTasks(ctx, scope.Project, person)
		myTasks = crewTasksBrief(tasks, func(t crewTeamTask) bool { return t.AgentKey == agent.ChatKey })
	}
	return map[string]any{
		"my_tasks":               myTasks,
		"team_requests":          teamRequests[:min(20, len(teamRequests))],
		"waiting_on_your_person": needYou,
		"my_features":            crewFeatureBrief(scope.Project, person, tasks, agent.ChatKey),
		"hint":                   "Tasks are request threads. my_tasks are the ones you are doing: reply in their inbox_id with messages action=send, status=in_progress as you go, then done naming the PR or commit (decision=true with a short why for a choice others must follow). team_requests are open Team tasks: take one by replying in its inbox_id with status=in_progress. For work nobody asked for, send to target=self with the issue number; hand work to another agent with messages ask; split a task by sending each part with ref set to its message_id. Blocked on a person? Send to target=person:<name> with one to three buttons; their click comes back as the reply. Use tasks to list and filter the Team's tasks, and task_update to change one you are in. my_features are the features you are in, with their open tasks and latest decisions: read the decisions before you change course; join a feature with feature_join when you start work on it.",
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

// handleCrewWorkLog is the Team tasks route: GET op=work_brief, POST the other ops.
func (api *StreamingAPI) handleCrewWorkLog(w http.ResponseWriter, r *http.Request) {
	team, claims, ok := crewTeamForTab(w, r, r.Method == http.MethodPost)
	if !ok {
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
		externalError(w, 400, "invalid_work", "Choose a task action and give its fields")
		return
	}
	// Features and tasks need no Team (#3210): with it off they are the owner's own, read and changed on the app
	// that holds the Crew.
	if !team.On && !(claims.UserID == team.OwnerID && (crewFeatureOp(body.Op) || crewTaskOp(body.Op))) {
		externalError(w, 403, "team_off", "Turn on this Crew's Team first")
		return
	}
	var actor crewTeamRegisteredAgent
	var err error
	if body.AgentID == "" && (body.ChatKey == crewWorkPersonPrefix+claims.UserID || body.ChatKey == "" && (body.Op == "work_answer" || body.Op == "features" || body.Op == "feature")) {
		actor = crewWorkPersonActor(claims.UserID) // a person over MCP forwarded from their Mac, a Needs-you button click, or the screens reading features
	} else if actor, err = api.crewTeamActivityActorForRequest(r, team, claims.UserID, body.AgentID, body.ChatKey, body.Agent); err != nil || body.SharedAsk && !actor.InTeam {
		externalError(w, 403, "work_forbidden", "Use Team tasks from your own agent in this Crew")
		return
	}
	scope := crewWorkScope{Project: team.ProjectID, Team: &team}
	if !team.On {
		scope.Team = nil
	}
	out, err := crewWorkDo(r.Context(), scope, claims.UserID, actor, body.crewWorkInput)
	if err != nil {
		externalError(w, 400, "work_refused", err.Error())
		return
	}
	externalJSON(w, out)
}

func crewWorkPersonActor(person string) crewTeamRegisteredAgent {
	return crewTeamRegisteredAgent{OwnerID: person, ChatKey: crewWorkPersonPrefix + person, Name: crewTeamFirstName(crewPersonShortName(person))}
}

// crewWorkTeamOff reports whether this Crew's tasks are personal: its Team was
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
	for key, field := range map[string]*string{"task": &in.Task, "line": &in.Line, "why": &in.Why, "query": &in.Query, "need": &in.Need, "button": &in.Button, "helper": &in.Helper, "comment": &in.Comment, "status": &in.Status, "category": &in.Category, "file_name": &in.FileName, "file_text": &in.FileText, "state": &in.State, "tag": &in.Tag, "issue": &in.Issue, "who": &in.Who, "feature": &in.Feature, "link": &in.Link} {
		*field = externalArg(args, key)
	}
	in.Tags = stringSliceFromToolArg(args["tags"])
	in.Decision, _ = args["decision"].(bool)
	in.Mine, _ = args["mine"].(bool)
	in.Leave, _ = args["leave"].(bool)
	in.Limit = externalInt(args, "limit", 0)
	return in
}
