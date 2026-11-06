package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
)

// The Team tab (owner, 5 Nov, approved mockup "Single Team Tab"): one tab for the owner and every teammate, in the Mac
// app and on the website, for a Mac Crew and a Crew on the server. The server writes every row and says what the
// viewer may change; the screen only shows it. People are grouped by account, never by a name.

type crewTeamTabPerson struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	// Note is the muted word after the name: owner, you or invited.
	Note        string `json:"note,omitempty"`
	Active      bool   `json:"active"`
	ActiveLabel string `json:"active_label,omitempty"`
	CanRemove   bool   `json:"can_remove"`
	// Presence is the dot by a teammate's name on the tab bar and left menu; nil for the viewer, an invited person and
	// anyone whose agents work on the server (no Mac to speak of).
	Presence *crewTeamTabPresence `json:"presence,omitempty"`
}

// crewTeamTabPresence is whether a teammate's Mac is online now. Line is the dot's hover title and the agent card's Now
// line. Tone picks the dot's colour: "on" (Mac on), "server" (Mac off, their shared agents answer from the server) or
// "away" (Mac off, their agents wait for it).
type crewTeamTabPresence struct {
	Online bool   `json:"online"`
	Line   string `json:"line"`
	Tone   string `json:"tone"`
}

type crewTeamTabAgent struct {
	// Key is the agent's chat key: In team / Only me sends it (agent-visibility).
	Key  string `json:"key"`
	Name string `json:"name"`
	// Detail is whose it is and where it works: "Manish", "tester-b · On their Mac". The viewer's own rows (the Agents
	// card lists only those, all in the same place) carry only a state worth reading, such as "Mac off · answering
	// from the server", never "you" or "Mac" (owner, 6 Nov).
	Detail string `json:"detail"`
	InTeam bool   `json:"in_team"`
	// CanChange: the viewer chooses In team / Only me for it here. Otherwise Fixed is shown instead of the choice.
	// CanUnshare is false only on the viewer's last shared agent (each person keeps one, owner 6 Nov); Fixed then says
	// why, as the switch's title.
	CanChange  bool   `json:"can_change"`
	CanUnshare bool   `json:"can_unshare"`
	Fixed      string `json:"fixed,omitempty"`
	// Address is the send_message target for "Ask from my agent".
	Address string `json:"address,omitempty"`
	// Whose agent it is, by account, with the server's display name: screens group people by OwnerID, never by a name.
	// Mine: the viewer's own agent. The Agents card lists only these (owner, 6 Nov: "i cannot setup permissions etc
	// another user"); everyone's rows stay in the list for the tab bar, left menu and watching lines.
	OwnerID   string `json:"owner_id"`
	OwnerName string `json:"owner_name"`
	Mine      bool   `json:"mine"`
	// Place is where it runs ("mac" or "server"); StatusLabel is its availability ("Mac off"), "" when it can answer.
	Place       string `json:"place"`
	StatusLabel string `json:"status_label,omitempty"`
	// PlaceLabel / PlaceTone: where an agent of the owner's Mac answers now and where its server copy stands (Always
	// on, crewAlwaysOnPlaceLabel), shown as the row's chip; the tone ("ok", "wait", "server" or "") only picks its colour.
	PlaceLabel string `json:"place_label,omitempty"`
	PlaceTone  string `json:"place_tone,omitempty"`
	// Activity is "running" while a turn is going in the agent now (crew_team_activity.go): the chip's working mark.
	Activity string `json:"activity,omitempty"`
	// NotSharedLine is the one muted line of the viewer's own agent that is not shared (owner, 6 Nov mockup).
	NotSharedLine string `json:"not_shared_line,omitempty"`
	// Tab is the agent's tab in its person's copy ("main" for a Mac's Main), so the Mac app finds its own tabs' rows.
	Tab string `json:"tab"`
	// When others ask (crew_when_others_ask.go), the row's details: what it uses (names only; the owner's own sign-ins
	// only to the owner, marked personal), its person's rule, Answer only / Make changes (an agent on a Mac only; nil
	// elsewhere), whether the viewer may edit them (the agent's own person), and the line the screen shows.
	Uses           []crewTeamAgentUse `json:"uses"`
	WhenOthersAsk  string             `json:"when_others_ask"`
	HasRule        bool               `json:"has_rule"`
	CanMakeChanges *bool              `json:"can_make_changes,omitempty"`
	CanEditRule    bool               `json:"can_edit_rule"`
	WhenOthersLine string             `json:"when_others_line"`
	// Teammates can read this chat (crew_team_chat_private.go). ChatVisibleLabel is the switch's line, on the viewer's
	// own agent; ChatPrivateLine replaces the chat on someone else's agent they keep private.
	ChatVisible      bool   `json:"chat_visible"`
	ChatVisibleLabel string `json:"chat_visible_label,omitempty"`
	ChatPrivateLine  string `json:"chat_private_line,omitempty"`
	// WatchKey is the key of its tab for someone else, where that differs from Key: the Crew owner's Mac Main.
	WatchKey string `json:"watch_key,omitempty"`
	// PermissionsPrompt is what its rule editor's "Ask AI to set this up" sends to the viewer's Main (their own agent).
	PermissionsPrompt string `json:"permissions_prompt,omitempty"`
	// The agent sharing list (owner, 6 Nov, approved mockup): its one-line purpose, whether the viewer edits it in the
	// row (their own agent; POST agent-rule {purpose}), and, for the owner of a Mac Crew, its Public on AgentBridge
	// for the Mac online tab's AgentBridge card.
	Purpose       string             `json:"purpose"`
	CanSetPurpose bool               `json:"can_set_purpose"`
	AgentBridge   *crewTeamTabBridge `json:"agentbridge,omitempty"`
	// Setup is the tab's setup icon and its card rows (crew_tab_setup.go), for everyone who sees the agent.
	Setup *crewTabSetup `json:"setup,omitempty"`
}

// crewTeamTabBridge is one of a Mac Crew's agents' Public on AgentBridge, from the list its Mac pushed
// (crew_mac_shared_agents.go). The Mac app switches it on the Mac; the website through this server to the Mac
// (handleCrewTeamAgentBridge).
type crewTeamTabBridge struct {
	Public bool `json:"public"`
	// CanChange: the viewer (the Crew's owner) switches it. NeedsPurpose: no purpose yet, so the card shows "Set
	// purpose" instead of the switch (an agent is made public with a purpose).
	CanChange    bool `json:"can_change"`
	NeedsPurpose bool `json:"needs_purpose,omitempty"`
	// State is the pill beside its name while it is public: "public · online", "public · Mac offline".
	State string `json:"state,omitempty"`
	// CopyLine is what the owner pastes in their own coding app (Claude Code, Codex, Cursor) to reach this one agent
	// (owner, 6 Nov: one line per agent). The owner reaches their own agent through its Crew and chat, Public or not;
	// its agent:<owner>/<address> is for other people's agents only (macSharedAudience refuses the owner there).
	// CopyLineWhy says why there is no line yet.
	CopyLine    string `json:"copy_line,omitempty"`
	CopyLineWhy string `json:"copy_line_why,omitempty"`
}

// crewTeamTabSync is the shared folder's state as this server sees it for the viewer.
type crewTeamTabSync struct {
	Line     string         `json:"line"`
	Problem  bool           `json:"problem,omitempty"`
	Last     string         `json:"last,omitempty"` // "Last change 4 min ago by tester-b: team/notes.md"
	Progress *crewTeamRound `json:"progress,omitempty"`
}

type crewTeamTabReply struct {
	Team   crewTeam            `json:"team"`
	Intro  string              `json:"intro"`
	People []crewTeamTabPerson `json:"people"`
	Agents []crewTeamTabAgent  `json:"agents"`
	Sync   *crewTeamTabSync    `json:"sync,omitempty"`
	// The Team's settings are the server's record, changed from the website, the Mac app, chat or MCP; the owner's Mac
	// applies it when it syncs (crew_team_apply.go). CanSwitch: the Team on/off switch, the Crew's main owner only
	// (owner, 5 Nov).
	CanSwitch bool `json:"can_switch"`
	// AgentsLine is the Agents card's line: new agents are shared (owner, 6 Nov: each agent's switch is the only
	// control), and Always on for the owner of a Mac Crew.
	AgentsLine string `json:"agents_line,omitempty"`
	CanLeave   bool   `json:"can_leave"`
	// CanChoose: the viewer switches Folders / Whole Crew and stops sharing a folder, the main owner only (owner, 5 Nov).
	// CanAddFolder: the viewer adds a folder to share, anyone in the Team, picked from the Crew's folders (GET
	// /folders, crew_team_folders.go) or by name while a Mac Crew's Mac is off; a missing one is made.
	// CanRemoveFolder: the owner stops sharing a folder; never the last one (Folders shares at least one, 6 Nov).
	CanChoose       bool `json:"can_choose"`
	CanAddFolder    bool `json:"can_add_folder"`
	CanRemoveFolder bool `json:"can_remove_folder"`
	// Asks are the cards' Ask messages, sent to the viewer's own Main (crewTeamAskPrompts).
	Asks map[string]string `json:"asks"`
}

// crewTeamAskPrompts are the Access and Team cards' Ask messages (owner, 6 Nov: each looks at the current state
// first, explains it plainly, suggests, and changes only when the person says so). The screen sends them as given.
var crewTeamAskPrompts = map[string]string{
	"team":        "Look at this Crew's Team: who is in it, who was invited and hasn't joined yet, and whether the Team is on. Explain it in plain words, tell me what each person can see and do, then help me add or remove people.",
	"shares":      "Show me what this Crew's Team shares now: each shared folder with its size, or the whole Crew. Go through the Crew's other folders and what is in them, point out anything private or too big to share (keys, personal notes, large data), and suggest what to share. Change it when I say so.",
	"agents":      "List my agents in this Crew: for each, its purpose, whether it's shared with the Team, and what it uses (apps, secret names, my personal accounts, folders). Suggest which to share and which to keep to myself, and why. Change it when I say so.",
	"lent":        "List the apps and secrets I lend to this Crew's Team (names only, never values) and which teammates' agents use them. Point out which are my personal accounts and suggest what to keep lending or stop.",
	"sync":        "Check this Crew's Team sync: when each person last synced, files still waiting, and any clashes where two people changed the same lines. For each clash show both versions and help me keep mine, keep theirs, or merge them.",
	"mac":         "Check this Crew's Mac online: is my Mac online now, when was it last seen, and does it stay on because the Team is on. Explain what works while my Mac is off and what is kept on the server, in plain words, and give me the link to open this Crew in a browser or on my phone. Change it when I say so.",
	"agentbridge": "List my agents in this Crew with their purpose and whether each is public on AgentBridge. Explain what public means (other people's agents on this server can find and message it, and it answers as me with my rules for when others ask), suggest which to make public and which purposes to improve, and change it when I say so. Also explain each agent's Copy line: pasted in Claude Code, Codex or Cursor, it lets my coding app reach that agent, public or not.",
	"publish":     "Help me publish this Crew to the server.",
	// An agent card's Automations…, sent to that agent's own chat (crew_tab_setup.go).
	"automations": "Show this agent's schedules, watchers and webhooks and help me change them.",
}

func (api *StreamingAPI) handleCrewTeamTab(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	started := time.Now()
	t.People = slices.DeleteFunc(t.People, func(p crewTeamPerson) bool { return !crewTeamAccountActive(p.UserID) })
	w.Header().Set("Cache-Control", "no-store")
	reply := crewTeamTabFor(r.Context(), t, claims.UserID)
	// Every screen re-reads this on each crew_panel notice: a slow reply shows here with the Crew and its size.
	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		log.Printf("[CREW_TEAM_TAB] project=%s viewer=%s team_on=%v agents=%d elapsed_ms=%d", t.ProjectID, claims.UserID, t.On, len(reply.Agents), elapsed.Milliseconds())
	}
	externalJSON(w, reply)
}

func crewTeamTabFor(ctx context.Context, t crewTeam, viewer string) crewTeamTabReply {
	view := t.view(viewer)
	view.Lent = crewTeamLentNow(ctx, t, viewer)
	twin := crewRelayTwin(t.ProjectID)
	out := crewTeamTabReply{Team: view, People: []crewTeamTabPerson{}, Agents: []crewTeamTabAgent{}, Asks: crewTeamAskPrompts, CanSwitch: view.CanManage && viewer == t.OwnerID,
		CanLeave: viewer != t.OwnerID && t.member(viewer), Intro: "People work with you here. Everything is kept on the server.",
		AgentsLine: crewTeamAgentsLine}
	if twin {
		out.Intro = "People work with you here. The shared folder is kept on the server. While the Mac is off, shared agents answer from the server; agents that are not shared wait for it."
	}
	bridges := crewTeamBridgesFor(t)
	setups := crewTabSetupSourceFor(ctx, t, twin)
	if viewer == t.OwnerID && twin {
		out.AgentsLine = crewAlwaysOnAgentsLine
	}
	if !t.On {
		// Team off: only AgentBridge is left to choose, on the owner's own agents.
		if viewer == t.OwnerID {
			out.Agents = crewTeamSoloAgents(ctx, t, bridges, setups)
		}
		return out
	}
	byPerson := teamAgentsByPerson(ctx, t, viewer)
	seen := map[string]crewTeamAgentsPerson{}
	for _, p := range byPerson {
		seen[p.UserID] = p
	}
	person := func(id, note string, removable bool) {
		p := seen[id]
		out.People = append(out.People, crewTeamTabPerson{UserID: id, Name: crewPersonShortName(id), Note: note, Active: p.Active, ActiveLabel: p.ActiveLabel,
			CanRemove: removable && view.CanManage && id != viewer, Presence: crewTeamPersonPresence(t, p, viewer)})
	}
	person(t.OwnerID, "owner", false) // the owner is never removed
	for _, p := range t.People {
		note := ""
		switch {
		case p.Invited:
			note = "invited"
		case p.UserID == viewer:
			note = "you" // they leave with Leave Team, not a remove button
		}
		person(p.UserID, note, true)
	}
	uses := crewTeamUses(ctx, t)
	for _, p := range byPerson {
		mine := p.UserID == viewer
		onMac := crewTeamAgentOnMac(t, p.UserID)
		shared := 0
		for _, a := range p.Agents {
			if a.InTeam {
				shared++
			}
		}
		for _, a := range p.Agents {
			detail := []string{}
			if !mine {
				detail = append(detail, p.Name)
				if a.Place == "mac" && !p.Owner {
					detail = append(detail, "On their Mac")
				}
			}
			// Always on: where an agent of the owner's Mac answers and where its server copy stands is the row's own
			// chip (place_label, place_tone), so the detail never repeats it.
			if a.StatusLabel != "" && a.PlaceLabel == "" {
				detail = append(detail, a.StatusLabel)
			}
			// Always on: a hand back that found the agent had answered on both the Mac and the server (owner only).
			if mine && p.Owner && a.Place == "mac" {
				if kept := crewHandbackKeptNote(t.ProjectID, a.Tab); kept != "" {
					detail = append(detail, kept)
				}
			}
			row := crewTeamTabAgent{Key: a.ChatKey, Name: a.Name, Detail: strings.Join(detail, " · "), InTeam: a.InTeam,
				OwnerID: p.UserID, OwnerName: p.Name, Mine: mine, Place: a.Place, StatusLabel: a.StatusLabel, PlaceLabel: a.PlaceLabel, PlaceTone: a.PlaceTone, Activity: a.Activity, Tab: a.Tab}
			if mine && !a.InTeam {
				row.NotSharedLine = crewTeamNotSharedLine(a.Place == "mac")
			}
			// Shared is per person (owner, 5-6 Nov): each person switches their own agents, Main too, but keeps one
			// shared (setCrewTeamAgentChoice); someone else's agent just reads Shared.
			if mine {
				row.CanChange, row.CanUnshare = true, !a.InTeam || shared > 1
				if !row.CanUnshare {
					row.Fixed = errCrewTeamLastShared.Error()
				}
			} else {
				row.Fixed, row.Address = "Shared", a.Address
			}
			rule := t.agentRule(p.UserID, crewTeamRuleKey(ctx, t, p.UserID, a.ChatKey))
			row.Uses = crewTeamRowUses(uses, p.UserID, t.OwnerID, viewer)
			row.WhenOthersAsk, row.HasRule, row.CanEditRule = rule.WhenOthersAsk, rule.WhenOthersAsk != "", mine
			// Answer only / Make changes for every agent, Mac or server alike (one row for every own agent, owner 6 Nov).
			changes := rule.CanMakeChanges
			row.CanMakeChanges = &changes
			row.WhenOthersLine = crewTeamWhenOthersLine(rule, p.Name, mine)
			row.ChatVisible = t.agentChatVisible(ctx, p.UserID, a.ChatKey)
			if mine {
				row.PermissionsPrompt = crewTeamPermissionsPrompt(a.Name)
				row.ChatVisibleLabel = crewTeamChatVisibleLabel
			} else {
				if !row.ChatVisible {
					row.ChatPrivateLine = crewChatPrivateLine(p.UserID)
				}
				if p.Owner && onMac && a.Tab == macMainTab {
					row.WatchKey = crewMacTabRow(t.ProjectID, crewMacTeamTab{Person: p.UserID, Tab: macMainTab}).ConversationKey
				}
			}
			row.Purpose, row.CanSetPurpose = a.Purpose, mine
			// Public on AgentBridge is switched on the Mac online tab (owner, 6 Nov); the row keeps it for that screen.
			var bridge *crewTeamTabBridge
			if p.UserID == t.OwnerID {
				bridge = bridges.row(t, a, viewer)
			}
			if p.UserID == t.OwnerID && mine {
				row.AgentBridge = bridge
			}
			row.Setup = setups.setup(t, a, p.UserID, viewer, row.InTeam, bridge)
			out.Agents = append(out.Agents, row)
		}
	}
	out.Sync = crewTeamTabSyncFor(t, viewer)
	out.CanChoose = view.CanManage && viewer == t.OwnerID
	out.CanAddFolder = view.CanManage
	out.CanRemoveFolder = out.CanChoose && len(t.Folder.Folders) > 1
	return out
}

// crewTeamPersonPresence words a teammate's Mac for the viewer, from the same Mac connection as their agents' rows. With
// the Mac off it says what happens to their agents: the Crew owner's In team agents answer from the server, a member's
// wait for their Mac (crew_mac_waiting.go).
func crewTeamPersonPresence(t crewTeam, p crewTeamAgentsPerson, viewer string) *crewTeamTabPresence {
	if p.UserID == "" || p.UserID == viewer || !crewTeamAgentOnMac(t, p.UserID) {
		return nil
	}
	if p.MacOn || (p.Owner && IsLocalMode()) {
		return &crewTeamTabPresence{Online: true, Line: p.Name + "'s Mac is on", Tone: "on"}
	}
	for _, a := range p.Agents {
		if a.Place == "server" || a.PlaceServer {
			return &crewTeamTabPresence{Line: p.Name + "'s Mac is off · shared agents answer from the server", Tone: "server"}
		}
	}
	return &crewTeamTabPresence{Line: p.Name + "'s Mac is off · their agents wait for it", Tone: "away"}
}

// crewTeamNotSharedLine is the muted line of the viewer's own agent that is not shared: what turning sharing on gives.
func crewTeamNotSharedLine(onMac bool) string {
	if onMac {
		return "Only you can use it. Turn on sharing to let teammates ask it, even while your Mac is off."
	}
	return "Only you can use it. Turn on sharing to let teammates ask it."
}

// crewTeamBridges is what this server keeps of a Mac Crew's agents' Public on AgentBridge (the Mac online tab's
// AgentBridge card); a Crew on the server chooses it in its Share instead, so it has none here.
type crewTeamBridges struct {
	twin  bool
	macOn bool                      // the owner's Mac is online now
	mac   map[string]macSharedAgent // by tab
}

func crewTeamBridgesFor(t crewTeam) crewTeamBridges {
	b := crewTeamBridges{twin: crewRelayTwin(t.ProjectID), mac: map[string]macSharedAgent{}}
	if !b.twin {
		return b
	}
	for _, agent := range readMacSharedAgents(t.ProjectID) {
		b.mac[agent.Tab] = agent
	}
	if pair, ok := crewMacPairFor(t.OwnerID, t.ProjectID); ok {
		b.macOn = crewMacSocketFor(t.OwnerID, t.ProjectID, pair) != nil
	}
	return b
}

// row is one of the owner's agents' AgentBridge setting for the viewer; nil when it has none.
func (b crewTeamBridges) row(t crewTeam, a crewTeamAgent, viewer string) *crewTeamTabBridge {
	if !b.twin {
		return nil
	}
	out := b.state(t, a, viewer)
	out.CopyLine, out.CopyLineWhy = crewAgentCopyLine(t, a)
	return out
}

func (b crewTeamBridges) state(t crewTeam, a crewTeamAgent, viewer string) *crewTeamTabBridge {
	if a.Place == "server" { // published from the Mac: it answers on the server, public there
		return &crewTeamTabBridge{Public: true, State: "public · on the server"}
	}
	agent, ok := b.mac[a.Tab]
	out := &crewTeamTabBridge{CanChange: viewer == t.OwnerID, Public: ok && agent.AgentBridgePublic}
	out.NeedsPurpose = !out.Public && strings.TrimSpace(a.Purpose) == ""
	if out.Public {
		out.State = map[bool]string{true: "public · online", false: "public · Mac offline"}[b.macOn || IsLocalMode()]
	}
	return out
}

// crewAgentCopyLine is the owner's coding-app line for one of their agents: this Crew on this server, that agent's tab
// by its name. The owner's ask is passed to the Mac, which finds the tab by its (unique) name or Main
// (desktopCrewWorkspace, findCodeChat); the server's chat key would not match the Mac's own tab ids.
func crewAgentCopyLine(t crewTeam, a crewTeamAgent) (string, string) {
	name := strings.TrimSpace(a.Name)
	if name == "" {
		return "", "Give this agent a name first, then copy its line."
	}
	return fmt.Sprintf("Connect to AgentWorks, then ask the agent %q in my %q Crew to help me (workspace action=ask, workspace_id=%s:%s, chat_id=%q).",
		name, firstNonEmptyTrimmed(t.Name, "Crew"), crewProfileID, t.ProjectID, name), ""
}

// crewTeamSoloAgents are the owner's own agents while the Team is off, each with its Public on AgentBridge: a Mac
// Crew's from the owner's Mac, a Crew on the server's from its tabs here.
func crewTeamSoloAgents(ctx context.Context, t crewTeam, bridges crewTeamBridges, setups crewTabSetupSource) []crewTeamTabAgent {
	agents := []crewTeamAgent{}
	if bridges.twin {
		mainKey := crewTeamOwnerMainKey(ctx, t)
		for _, tab := range ownerMacTabsAll(ctx, t.ProjectID) {
			a := crewTeamAgent{Tab: tab.Tab, Name: firstNonEmptyTrimmed(tab.Name, "Agent"), Purpose: firstNonEmptyTrimmed(tab.Purpose, bridges.mac[tab.Tab].Purpose), Place: "mac", ChatKey: macSharedAgentChatKey(t.ProjectID, tab.Tab)}
			if tab.Tab == macMainTab {
				a.Name, a.ChatKey = "Main", mainKey
			}
			if crewPublishedTarget(t.ProjectID, tab.Tab) {
				a.Place = "server"
			}
			agents = append(agents, a)
		}
	} else {
		agents = memberTeamAgents(ctx, t, t.OwnerID, t.OwnerID)
	}
	name := crewPersonShortName(t.OwnerID)
	out := []crewTeamTabAgent{}
	for _, a := range agents {
		rule := t.agentRule(t.OwnerID, crewTeamRuleKey(ctx, t, t.OwnerID, a.ChatKey))
		row := crewTeamTabAgent{Key: a.ChatKey, Name: a.Name, Fixed: "Only you", OwnerID: t.OwnerID, OwnerName: name, Mine: true, Place: a.Place, Tab: a.Tab,
			Uses: []crewTeamAgentUse{}, WhenOthersAsk: rule.WhenOthersAsk, HasRule: rule.WhenOthersAsk != "", CanEditRule: true,
			WhenOthersLine: crewTeamWhenOthersLine(rule, name, true), Purpose: a.Purpose, CanSetPurpose: true,
			ChatVisible: t.agentChatVisible(ctx, t.OwnerID, a.ChatKey), ChatVisibleLabel: crewTeamChatVisibleLabel,
			AgentBridge: bridges.row(t, a, t.OwnerID)}
		changes := rule.CanMakeChanges
		row.CanMakeChanges = &changes
		row.Setup = setups.setup(t, a, t.OwnerID, t.OwnerID, false, row.AgentBridge)
		out = append(out, row)
	}
	return out
}

// crewTeamTabSyncFor is the shared folder's state for the viewer from this server: a teammate working on the website
// has their own copy here (crew_team_server_copy.go), so its real state is shown; everyone else sees the folder kept
// here. A Mac shows its own copy's state instead (TeamSyncPanel reads it from the Mac).
func crewTeamTabSyncFor(t crewTeam, viewer string) *crewTeamTabSync {
	if !t.On || !t.edits(viewer) {
		return nil
	}
	s, _ := readCrewTeamFileState(t.ProjectID)
	files := len(s.held(t.Folder))
	// An open clash stays on the line, by file, until someone sorts it out (owner, 5 Nov).
	open := crewTeamOpenClashPaths(s.Clashes)
	clashLine := "no clashes"
	if len(open) > 0 {
		clashLine = crewTeamClashLine(open)
	}
	out := &crewTeamTabSync{} // open clashes are on the line, not a failure
	if n := len(s.Events); n > 0 {
		e := s.Events[n-1]
		if at, err := time.Parse(time.RFC3339Nano, e.At); err == nil {
			out.Last = fmt.Sprintf("Last change %s by %s: %s", crewAccessAgo(at), crewPersonShortName(firstNonEmptyTrimmed(e.Author, e.Actor)), e.Path)
		}
	}
	if _, onMac := t.memberMac(viewer); viewer != t.OwnerID && !onMac && crewRelayTwin(t.ProjectID) && t.Folder.ID != "" {
		if dir, err := crewTeamServerCopyDir(viewer, t.ProjectID, t.Folder.ID); err == nil {
			if p := crewTeamRoundFor(dir); p != nil {
				out.Line, out.Progress = crewAccessProgressLine(*p), p
				return out
			}
			if b, err := readCrewTeamLocal(dir); err == nil {
				line, problem := crewTeamCopyLine(b)
				out.Line, out.Problem = line+" · "+clashLine, out.Problem || problem
				return out
			}
			out.Line = "Your copy is made when you first use this Crew · " + clashLine
			return out
		}
	}
	out.Line = fmt.Sprintf("Kept on the server · %d %s · %s", files, plural(files, "file", "files"), clashLine)
	return out
}

// crewTeamCopyLine is one copy's sync state in plain words, and whether it is a problem: the one line the Mac app and
// the website show (a Mac's own copy from its status, a member's server copy from the Team tab), with what it leaves out.
func crewTeamCopyLine(b crewTeamLocal) (string, bool) {
	line, problem := crewTeamCopyState(b)
	if b.Root != "" {
		op := b.onPurpose()
		skipped := 0
		for _, rel := range crewTeamSkipped(b.Root) {
			if !op[rel] {
				skipped++
			}
		}
		if skipped > 0 {
			line += fmt.Sprintf(" · %d private-looking %s skipped", skipped, plural(skipped, "file", "files"))
		}
	}
	if n := len(b.TooLarge); n > 0 {
		line += fmt.Sprintf(" · %d %s over 64 MB not synced", n, plural(n, "file", "files"))
	}
	return line, problem
}

func crewTeamCopyState(b crewTeamLocal) (string, bool) {
	files := fmt.Sprintf("%d %s", len(b.Files), plural(len(b.Files), "file", "files"))
	ago := ""
	if at, err := time.Parse(time.RFC3339Nano, b.LastSynced); err == nil {
		ago = crewAccessAgo(at)
	}
	switch {
	case b.Error != "":
		return "Can't sync: " + b.Error + " · trying again soon", true
	case b.Status == "up_to_date":
		return strings.TrimSuffix("Up to date · checked "+ago, " · checked ") + " · " + files, false
	case !b.Downloaded:
		return "Getting the shared files · " + files + " so far", false
	case ago != "":
		return "Changes waiting to send · last synced " + ago + " · " + files, false
	}
	return "Changes waiting to send · " + files, false
}
