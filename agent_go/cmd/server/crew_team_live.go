package server

import (
	"context"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// The Monitor's Team view (PLAT-746-926): names, identity, the server's live
// state, each teammate's presence and an agent's own Doing post (the same post
// Team › Activity shows them). A last-message excerpt is included only for a
// current In-team agent whose chat is Live, never Only me or Just-the-agent.
// Excerpts are visible text only, secret-redacted before shortening.
type crewTeamLiveAgent struct {
	Purpose  string   `json:"purpose,omitempty"`
	Examples []string `json:"examples,omitempty"`
	You      bool     `json:"you,omitempty"`
	Device   string   `json:"device,omitempty"`
	MacName  string   `json:"mac_name,omitempty"`
	userAvatarFields
	CLI      string `json:"cli,omitempty"`
	Engine   string `json:"engine,omitempty"`
	Private  bool   `json:"private,omitempty"`
	ID       string `json:"id"`
	OwnerID  string `json:"owner_id"`
	Tab      string `json:"tab"`
	ChatKey  string `json:"chat_key"`
	Name     string `json:"name"`
	Person   string `json:"person"`
	Label    string `json:"label"`
	Activity string `json:"activity"`
	// Doing is the agent's latest Team "Doing" post, only while it works or waits.
	Doing string `json:"doing,omitempty"`
	// Waiting says who a waiting agent waits on ("Waiting for Laxmi's reply"), only while it waits.
	Waiting string `json:"waiting,omitempty"`
	crewTeamMessagePreview
}

// crewTeamLivePerson is one admitted person (own device metadata is owner-only): whether they are active now ("Active now · Mac app", "Mac app connected", or the
// web") or when they were last seen, from the same reading as the Team tab's people dots.
type crewTeamLivePerson struct {
	You     bool   `json:"you,omitempty"`
	Device  string `json:"device,omitempty"`
	MacName string `json:"mac_name,omitempty"`
	userAvatarFields
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
	Line   string `json:"line"`
}
type crewTeamLiveView struct {
	ProjectID  string               `json:"project_id"`
	Connection string               `json:"connection,omitempty"`
	Name       string               `json:"name"`
	Needs      []*crewWorkNeed      `json:"needs"`
	Recent     *crewTeamFeedItem    `json:"recent"`
	Network    string               `json:"network,omitempty"`
	NetworkID  string               `json:"network_id,omitempty"`
	Agents     []crewTeamLiveAgent  `json:"agents"`
	Idle       []crewTeamLiveAgent  `json:"idle"`
	People     []crewTeamLivePerson `json:"people"`
	Summary    string               `json:"summary"`
}

// Input has already passed current Team membership/In-team admission and the
// one live wait cutoff in teamAgentsByPersonWithMain. Both open and unopened
// Crews use this projection; a screen never computes a teammate's state.
func crewTeamLiveFor(ctx context.Context, t crewTeam, people []crewTeamAgentsPerson, viewer string) crewTeamLiveView {
	project := t.ProjectID
	out := crewTeamLiveView{ProjectID: t.ProjectID, Name: t.Name, Agents: []crewTeamLiveAgent{}, Idle: []crewTeamLiveAgent{}, People: []crewTeamLivePerson{}}
	if !crewTeamReachOpen(t, viewer) {
		return out
	}
	activity := crewTeamActivityFor(ctx, t, viewer)
	out.Recent = activity.CollapsedItem
	for _, need := range activity.Needs {
		if need.ForMe {
			out.Needs = append(out.Needs, need)
		}
	}
	posts := crewTeamActivityPostsFor(t, viewer)
	doing := posts.Doing
	preview := crewTeamAgentMessagePreviews(ctx, t, posts)
	// Teammates' agents come only from the one reach answer (the right panel, bridge discovery and admission use the
	// same rows), so the header can never count an agent the panel or AgentBridge would refuse.
	reach := map[string]bool{}
	reachable, terminals := 0, 0
	runtime := crewTeamTabReply{Team: t}
	for _, row := range crewTeamReachableFrom(ctx, t, viewer, people) {
		runtime.Reachable = append(runtime.Reachable, crewTeamPanelReach{crewTeamReachableAgent: row})
		reachable++
		if row.Kind == "agent" {
			reach[row.ID] = true
			if a, ok := t.Agents[row.ID]; ok && a.Kind == "terminal" {
				terminals++
			}
		}
	}
	for _, person := range people {
		if person.UserID == viewer {
			for _, agent := range person.Agents {
				runtime.Agents = append(runtime.Agents, crewTeamTabAgent{ID: agent.ID, OwnerID: viewer, Key: agent.ChatKey, Mine: true, Address: agent.Address})
			}
		}
	}
	crewTeamPanelRuntimeFor(ctx, viewer, &runtime)
	labels := map[string]crewTeamPanelRuntime{}
	for _, row := range runtime.Reachable {
		labels[row.ID] = row.crewTeamPanelRuntime
	}
	for _, row := range runtime.Agents {
		labels[row.ID] = row.crewTeamPanelRuntime
	}
	for _, person := range people {
		// A home has a real account for execution, not another teammate.
		if t.RosterSource != "" && person.UserID == t.OwnerID {
			continue
		}
		own := person.UserID == viewer
		face := crewTeamLivePerson{userAvatarFields: avatarFieldsFor(person.UserID, project), UserID: person.UserID, Name: person.Name, Online: person.Active, Line: person.ActiveLabel, You: own}
		if own {
			face.Name = "You"
			pair, _ := t.memberMac(viewer)
			if viewer == t.OwnerID {
				if b, e := crewroot.ReadSync(project); e == nil && b != nil && b.Side == "server" {
					pair = b.PairID
					face.Device, face.MacName = b.MacID, b.MacName
				}
			}
			face.Device, face.MacName = crewMacDisplayIdentity(viewer, project, pair, face.Device, face.MacName)
			if face.MacName != "" {
				face.Name = "You (" + face.MacName + ")"
			}
		}
		out.People = append(out.People, face)
		for _, agent := range person.Agents {
			post := doing[agent.ID]
			if agent.ID == "" {
				post = doing[agent.ChatKey]
			}
			if !own && (!agent.InTeam || !reach[agent.ID]) {
				continue
			}
			row := crewTeamLiveAgent{Purpose: agent.Purpose, Examples: agent.Examples, You: own, Device: face.Device, MacName: face.MacName, CLI: labels[agent.ID].CLI, Engine: labels[agent.ID].Engine, userAvatarFields: avatarFieldsFor(person.UserID, project), Private: post.Private, ID: agent.ID, OwnerID: person.UserID, Tab: agent.Tab, ChatKey: agent.ChatKey, Name: agent.Name, Person: face.Name, Activity: agent.Activity}
			if row.ID == "" {
				row.ID = row.ChatKey
			}
			row.Label = row.Person + " · " + row.Name
			row.crewTeamMessagePreview = preview(person.UserID, agent)
			if row.Activity == "running" || row.Activity == "waiting" {
				row.Doing = post.Line
			}
			if row.Activity == "running" {
				row.Label += " working"
				out.Agents = append(out.Agents, row)
			} else if row.Activity == "waiting" {
				row.Label += " waiting"
				row.Waiting = agent.StatusLabel
				out.Agents = append(out.Agents, row)
			} else {
				out.Idle = append(out.Idle, row)
			}
		}
	}
	if len(out.Agents) == 1 {
		out.Summary = out.Agents[0].Person + " working"
	} else if len(out.Agents) > 1 {
		out.Summary = fmt.Sprintf("%d working", len(out.Agents))
	}
	crewTeamLiveLog(project, viewer, out, reachable, terminals)
	return out
}

// Telemetry only, never reused as activity data: one count summary per minute
// instead of one log per navigation request. No names or message/task text.
var crewTeamLiveLogMinutes sync.Map

func crewTeamLiveLog(project, viewer string, live crewTeamLiveView, reachable, terminals int) {
	value, _ := crewTeamLiveLogMinutes.LoadOrStore(project+"/"+viewer, &atomic.Int64{})
	minute := time.Now().Unix() / 60
	previous := value.(*atomic.Int64).Load()
	if previous == minute || !value.(*atomic.Int64).CompareAndSwap(previous, minute) {
		return
	}
	waiting := 0
	for _, row := range live.Agents {
		if row.Activity == "waiting" {
			waiting++
		}
	}
	previews := 0
	for _, rows := range [][]crewTeamLiveAgent{live.Agents, live.Idle} {
		for _, row := range rows {
			if row.LastMessage != "" {
				previews++
			}
		}
	}
	log.Printf("[CREW_TEAM_LIVE] project=%s viewer=%s reachable=%d terminals=%d working=%d waiting=%d idle=%d previews=%d", project, viewer, reachable, terminals, len(live.Agents)-waiting, waiting, len(live.Idle), previews)
}

// Only metadata from admitted Team-to-Team conversations affects the live line.
// A reply closes the preceding outstanding question; it is not a new wait.
// crewTeamWait is one unanswered Team message: when it was sent and the chat it waits on.
type crewTeamWait struct {
	Since time.Time
	On    string // chat key of the agent that has not replied yet
}

func crewTeamMessageWaitStarts(ctx context.Context, project string, people []crewTeamAgentsPerson) map[string]crewTeamWait {
	out := map[string]crewTeamWait{}
	allowed := map[string]bool{}
	for _, person := range people {
		for _, agent := range person.Agents {
			if agent.InTeam {
				allowed[agent.ChatKey] = true
			}
		}
	}
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return out
	}
	for _, conversation := range store.Conversations {
		valid := true
		for _, endpoint := range conversation.Endpoints {
			if endpoint.Kind != triggerCallerCrew || endpoint.ID != project || endpoint.External || endpoint.NamedAgentID != "" || !allowed[endpoint.ChatKey] {
				valid = false
			}
		}
		if !valid {
			continue
		}
		from := -1
		var since time.Time
		for _, message := range conversation.Messages {
			if message.Kind == "ping" || message.From < 0 || message.From > 1 {
				continue
			}
			if from >= 0 && from != message.From {
				from = -1
				continue
			}
			if from < 0 {
				from, since = message.From, message.SentAt
			}
		}
		if from >= 0 {
			key := conversation.Endpoints[from].ChatKey
			if previous, ok := out[key]; !ok || since.Before(previous.Since) {
				out[key] = crewTeamWait{Since: since, On: conversation.Endpoints[1-from].ChatKey}
			}
		}
	}
	return out
}

// Local navigation fetches the same server projection once per saved connection,
// not once per Crew. This memo lives only for this request, not a busy cache.
func crewTeamLocalNavigationLive(ctx context.Context, owner, project string, reads map[string]map[string]crewTeamLiveView) *crewTeamLiveView {
	binding, err := crewroot.ReadSync(project)
	if err != nil || binding == nil || binding.Side != "local" || binding.ConnectionID == "" || ownerOfCrewProjectID(project) != owner {
		return nil
	}
	connection, err := desktopConnectionFor(owner, binding.ConnectionID)
	if err != nil {
		return nil
	}
	rows, read := reads[connection.ID]
	if !read {
		rows = map[string]crewTeamLiveView{}
		reads[connection.ID] = rows
		var reply struct {
			Workspaces []struct {
				ID   string            `json:"id"`
				Live *crewTeamLiveView `json:"team_live"`
			} `json:"workspaces"`
		}
		round, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		if err := crewTeamRemoteJSON(round, owner, connection, http.MethodPost, "/api/workspaces", map[string]string{"action": "list"}, &reply); err != nil {
			log.Printf("[CREW_TEAM_LIVE] project=%s event=navigation_read_failed", project)
			return nil
		}
		for _, row := range reply.Workspaces {
			if row.Live != nil {
				rows[row.ID] = *row.Live
			}
		}
	}
	if live, ok := rows[binding.PeerProjectID]; ok {
		crewTeamLiveLocalSelf(&live, connection)
		live.Connection = connection.ID
		live.Network = connection.Name
		live.NetworkID = connection.Server
		return &live
	}
	return nil
}

// Self is resolved from this Mac's saved account and relay device, never names.
// Device IDs are emitted only on the source owner's rows.
func crewTeamLiveLocalSelf(live *crewTeamLiveView, connection desktopConnection) {
	if live == nil {
		return
	}
	label := func(device, name string) string {
		if device != "" && device == desktopRelayDeviceID(connection.ID) {
			return "You (this Mac)"
		}
		if name != "" {
			return "You (" + name + ")"
		}
		return "You"
	}
	for i := range live.People {
		p := &live.People[i]
		if p.You && p.UserID == connection.User.ID {
			p.Name = label(p.Device, p.MacName)
		}
	}
	for _, rows := range [][]crewTeamLiveAgent{live.Agents, live.Idle} {
		for i := range rows {
			a := &rows[i]
			if a.You && a.OwnerID == connection.User.ID {
				a.Person = label(a.Device, a.MacName)
			}
		}
	}
}
