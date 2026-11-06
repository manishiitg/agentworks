package server

import (
	"context"
	"fmt"
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
}

type crewTeamTabAgent struct {
	// Key is the agent's chat key: In team / Only me sends it (agent-visibility).
	Key  string `json:"key"`
	Name string `json:"name"`
	// Detail is whose it is and where it works: "Manish", "tester-b · On their Mac", "you · On your Mac".
	Detail string `json:"detail"`
	InTeam bool   `json:"in_team"`
	// CanChange: the viewer chooses In team / Only me for it here. Otherwise Fixed is shown instead of the choice.
	CanChange bool   `json:"can_change"`
	Fixed     string `json:"fixed,omitempty"`
	// Address is the send_message target for "Ask from my agent".
	Address string `json:"address,omitempty"`
	// Whose agent it is, by account, with the server's display name: screens group people by OwnerID, never by a name.
	OwnerID   string `json:"owner_id"`
	OwnerName string `json:"owner_name"`
	// Place is where it runs ("mac" or "server"); StatusLabel is its availability ("Mac off"), "" when it can answer.
	Place       string `json:"place"`
	StatusLabel string `json:"status_label,omitempty"`
	// PlaceLabel / PlaceServer: where an agent of the owner's Mac answers now (Always on), shown as the row's label.
	PlaceLabel  string `json:"place_label,omitempty"`
	PlaceServer bool   `json:"place_server,omitempty"`
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
	// PermissionsLine is the one-line summary under the purpose (owner, 6 Nov): "Others can: answer only · 1 extra rule".
	// PermissionsPrompt is what its Permissions' "Ask AI to set this up" sends to the viewer's Main (their own agent).
	PermissionsLine   string `json:"permissions_line"`
	PermissionsPrompt string `json:"permissions_prompt,omitempty"`
	// The agent sharing list (owner, 5 Nov, approved mockup "Agent Sharing List"): its one-line purpose, whether the
	// viewer may ask its chat to set one (their own agent), and its Public on AgentBridge (only on the Crew owner's own
	// rows, for the owner; everyone else reads it in Fixed).
	Purpose       string             `json:"purpose"`
	CanSetPurpose bool               `json:"can_set_purpose"`
	AgentBridge   *crewTeamTabBridge `json:"agentbridge,omitempty"`
}

// crewTeamTabBridge is one agent's Public on AgentBridge as this server keeps it: a Mac Crew's in the list its Mac
// pushed (crew_mac_shared_agents.go), a Crew on the server's in its Share's selected agents (crew_shared_agents.go).
type crewTeamTabBridge struct {
	Public bool `json:"public"`
	// CanChange: the viewer switches it. MacOnly: only from the owner's Mac app, which keeps the choice (the
	// crew-sync share_agents action); elsewhere Text is shown with the reply's agents_mac_only line.
	CanChange bool `json:"can_change"`
	MacOnly   bool `json:"mac_only,omitempty"`
	// NeedsPurpose: turning it on first asks the agent's chat for a purpose (an agent is shared with a purpose).
	NeedsPurpose bool   `json:"needs_purpose,omitempty"`
	Line         string `json:"line,omitempty"` // under the switch while it is public
	Text         string `json:"text"`           // the setting in words, where it is not switched
	// A Crew on the server switches it through its Share: /api/dashboards action=share on Workspace, shared_agent=ShareKey.
	ShareKey  string `json:"share_key,omitempty"`
	Workspace string `json:"workspace,omitempty"`
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
	// (owner, 5 Nov). AllInTeam / CanAllMine: the viewer's own All my agents in team, every joined person's own.
	CanSwitch  bool `json:"can_switch"`
	AllInTeam  bool `json:"all_in_team"`
	CanAllMine bool `json:"can_all_mine"`
	// AgentsMacOnly is the Agents list's last line for the owner of a Mac Crew: Public on AgentBridge is switched in
	// their Mac app (its screen there leaves the line out).
	AgentsMacOnly string `json:"agents_mac_only,omitempty"`
	// AgentsLine is the Agents card's line under All my agents in team (Always on, for the owner of a Mac Crew).
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
	"team":    "Look at this Crew's Team: who is in it, who was invited and hasn't joined yet, and whether the Team is on. Explain it in plain words, tell me what each person can see and do, then help me add or remove people.",
	"shares":  "Show me what this Crew's Team shares now: each shared folder with its size, or the whole Crew. Go through the Crew's other folders and what is in them, point out anything private or too big to share (keys, personal notes, large data), and suggest what to share. Change it when I say so.",
	"agents":  "List my agents in this Crew: for each, its purpose, whether it's shared with the Team, and what it uses (apps, secret names, my personal accounts, folders). Suggest which to share and which to keep to myself, and why. Change it when I say so.",
	"lent":    "List the apps and secrets I lend to this Crew's Team (names only, never values) and which teammates' agents use them. Point out which are my personal accounts and suggest what to keep lending or stop.",
	"sync":    "Check this Crew's Team sync: when each person last synced, files still waiting, and any clashes where two people changed the same lines. For each clash show both versions and help me keep mine, keep theirs, or merge them.",
	"mac":     "Explain Mac online for this Crew: is it on, what can reach my Mac (website, phone, ChatGPT or Claude, AgentBridge), and which of my agents are public on AgentBridge. Help me change it.",
	"publish": "Help me publish this Crew to the server.",
}

func (api *StreamingAPI) handleCrewTeamTab(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	t.People = slices.DeleteFunc(t.People, func(p crewTeamPerson) bool { return !crewTeamAccountActive(p.UserID) })
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, crewTeamTabFor(r.Context(), t, claims.UserID))
}

func crewTeamTabFor(ctx context.Context, t crewTeam, viewer string) crewTeamTabReply {
	view := t.view(viewer)
	view.Lent = crewTeamLentNow(ctx, t, viewer)
	twin := crewRelayTwin(t.ProjectID)
	out := crewTeamTabReply{Team: view, People: []crewTeamTabPerson{}, Agents: []crewTeamTabAgent{}, Asks: crewTeamAskPrompts, CanSwitch: view.CanManage && viewer == t.OwnerID,
		AllInTeam: t.allMine(viewer), CanAllMine: t.member(viewer), CanLeave: viewer != t.OwnerID && t.member(viewer),
		Intro: "People work with you here. Everything is kept on the server."}
	if twin {
		out.Intro = "People work with you here. The shared folder is kept on the server. While the Mac is off, shared agents answer from the server; agents that are not shared wait for it."
	}
	bridges := crewTeamBridgesFor(ctx, t)
	if viewer == t.OwnerID && twin {
		out.AgentsMacOnly = "Change these from your Mac app."
		if crewAlwaysOnAnswering {
			out.AgentsLine = crewAlwaysOnAgentsLine
		}
	}
	if !t.On {
		// Team off: only AgentBridge is left to choose, on the owner's own agents.
		if viewer == t.OwnerID {
			out.Agents = crewTeamSoloAgents(ctx, t, bridges)
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
			CanRemove: removable && view.CanManage && id != viewer})
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
		for _, a := range p.Agents {
			detail := []string{p.Name}
			if mine {
				detail[0] = "you"
			}
			if a.Place == "mac" && !p.Owner {
				detail = append(detail, map[bool]string{true: "On your Mac", false: "On their Mac"}[mine])
			}
			if a.StatusLabel != "" && a.PlaceLabel == "" { // the place label already says the Mac is off
				detail = append(detail, a.StatusLabel)
			}
			// Always on: a hand back that found the agent had answered on both the Mac and the server (owner only).
			if mine && p.Owner && a.Place == "mac" {
				if kept := crewHandbackKeptNote(t.ProjectID, a.Tab); kept != "" {
					detail = append(detail, kept)
				}
			}
			row := crewTeamTabAgent{Key: a.ChatKey, Name: a.Name, Detail: strings.Join(detail, " · "), InTeam: a.InTeam,
				OwnerID: p.UserID, OwnerName: p.Name, Place: a.Place, StatusLabel: a.StatusLabel, PlaceLabel: a.PlaceLabel, PlaceServer: a.PlaceServer, Tab: a.Tab}
			// Shared is per person (owner, 5-6 Nov): each person switches their own agents, the owner's Mac ones too.
			// Each person's Main is always shared; someone else's agent just reads Shared.
			if a.Tab == macMainTab || crewTeamMainAgent(t, p.UserID, a.ChatKey) {
				row.InTeam, row.Fixed = true, "Main is always shared"
				if !mine {
					row.Fixed, row.Address = "Shared", a.Address
				}
			} else if mine {
				row.CanChange = true
			} else {
				row.Fixed, row.Address = "Shared", a.Address
			}
			rule := t.agentRule(p.UserID, crewTeamRuleKey(ctx, t, p.UserID, a.ChatKey))
			row.Uses = crewTeamRowUses(uses, p.UserID, t.OwnerID, viewer)
			row.WhenOthersAsk, row.HasRule, row.CanEditRule = rule.WhenOthersAsk, rule.WhenOthersAsk != "", mine
			if onMac {
				changes := rule.CanMakeChanges
				row.CanMakeChanges = &changes
			}
			row.WhenOthersLine, row.PermissionsLine = crewTeamWhenOthersLine(rule, p.Name, mine), crewTeamPermissionsLine(rule)
			if mine {
				row.PermissionsPrompt = crewTeamPermissionsPrompt(a.Name)
			}
			row.Purpose, row.CanSetPurpose = a.Purpose, mine
			// Public on AgentBridge is switched on the Mac online tab (owner, 6 Nov); the row keeps it for that screen.
			if p.UserID == t.OwnerID && mine {
				row.AgentBridge = bridges.row(t, a, viewer, onMac && rule.CanMakeChanges)
			}
			out.Agents = append(out.Agents, row)
		}
	}
	out.Sync = crewTeamTabSyncFor(t, viewer)
	out.CanChoose = view.CanManage && viewer == t.OwnerID
	out.CanAddFolder = view.CanManage
	out.CanRemoveFolder = out.CanChoose && len(t.Folder.Folders) > 1
	return out
}

// crewTeamBridges is what this server keeps of the Crew owner's agents' Public on AgentBridge.
type crewTeamBridges struct {
	twin      bool
	mac       map[string]macSharedAgent // a Mac Crew's, by tab
	shareable map[string]bool           // a Crew on the server's: the chat keys its Share can select
	selected  []string
	workspace string
}

func crewTeamBridgesFor(ctx context.Context, t crewTeam) crewTeamBridges {
	b := crewTeamBridges{twin: crewRelayTwin(t.ProjectID), mac: map[string]macSharedAgent{}, shareable: map[string]bool{}}
	if b.twin {
		for _, agent := range readMacSharedAgents(t.ProjectID) {
			b.mac[agent.Tab] = agent
		}
		return b
	}
	root := crewProjectRootByID(t.ProjectID)
	agents, err := crewShareAgents(ctx, t.OwnerID, root, true)
	if root == "" || err != nil {
		return b
	}
	b.workspace = root
	for _, agent := range agents {
		b.shareable[agent.ChatID] = true
		if agent.Selected {
			b.selected = append(b.selected, agent.ChatID)
		}
	}
	return b
}

// row is one of the owner's agents' AgentBridge setting for the viewer; nil when it has none.
func (b crewTeamBridges) row(t crewTeam, a crewTeamAgent, viewer string, canMakeChanges bool) *crewTeamTabBridge {
	answers := "It only answers their questions."
	if canMakeChanges {
		answers = "It can make changes for them."
	}
	out := &crewTeamTabBridge{CanChange: viewer == t.OwnerID}
	if b.twin {
		if a.Place == "server" { // published from the Mac: it answers on the server, public there
			return &crewTeamTabBridge{Public: true, Text: "AgentBridge: public on the server"}
		}
		agent, ok := b.mac[a.Tab]
		out.Public, out.MacOnly = ok && agent.AgentBridgePublic, true
		out.Line = "Anyone on the AgentBridge network can message it. " + answers
	} else {
		key := a.ChatKey
		if !b.shareable[key] && crewTeamMainAgent(t, t.OwnerID, key) && b.shareable[t.ProjectID] {
			key = t.ProjectID // the Crew's Main, as its Share names it
		}
		if !b.shareable[key] {
			return nil
		}
		out.Public, out.ShareKey, out.Workspace = slices.Contains(b.selected, key), key, b.workspace
		out.Line = "People this Crew is shared with can message it from their agents. " + answers
	}
	out.NeedsPurpose = !out.Public && strings.TrimSpace(a.Purpose) == ""
	out.Text = "AgentBridge: not public"
	if out.Public {
		out.Text = "AgentBridge: public. " + answers
	}
	return out
}

// crewTeamSoloAgents are the owner's own agents while the Team is off, each with its Public on AgentBridge: a Mac
// Crew's from the owner's Mac, a Crew on the server's from its tabs here.
func crewTeamSoloAgents(ctx context.Context, t crewTeam, bridges crewTeamBridges) []crewTeamTabAgent {
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
		row := crewTeamTabAgent{Key: a.ChatKey, Name: a.Name, Detail: "you", Fixed: "Only you", OwnerID: t.OwnerID, OwnerName: name, Place: a.Place, Tab: a.Tab,
			Uses: []crewTeamAgentUse{}, WhenOthersAsk: rule.WhenOthersAsk, HasRule: rule.WhenOthersAsk != "", CanEditRule: true,
			WhenOthersLine: crewTeamWhenOthersLine(rule, name, true), Purpose: a.Purpose, CanSetPurpose: true,
			AgentBridge: bridges.row(t, a, t.OwnerID, bridges.twin && rule.CanMakeChanges)}
		if bridges.twin {
			changes := rule.CanMakeChanges
			row.CanMakeChanges = &changes
		}
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
	out := &crewTeamTabSync{Problem: len(open) > 0}
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
