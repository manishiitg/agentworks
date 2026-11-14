package server

import "net/http"

// The one Access page (owner, 5 Nov: "one Access page, not two"): the Mac app and the website render the same
// component (YourComputerPanel), and this reply decides its shape. AccessHere "mac" means this app holds the Mac's own
// controls (the Mac app's local backend); anywhere else the rows only the Mac can change come as text, worded here.

// Each tab belongs to one of the side menu's two pages (owner, 11 Nov: Share split into Team and Online): "team" is the
// people who work on the Crew with you, "online" is how everyone else reaches it. The page shows one group's tabs.
type crewAccessTab struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Group  string `json:"group"`
}

type crewAccessText struct {
	Line string `json:"line"`
	Note string `json:"note,omitempty"`
}

// crewAccessMacCard is the Mac online tab's "This Mac" card (owner, 6 Nov mockup): one status line and one plain
// sentence, the same on the Mac app and the website.
type crewAccessMacCard struct {
	Online bool              `json:"online"`
	Status string            `json:"status"` // "Online · MacBook Air", "Offline · MacBook Air"
	Line   string            `json:"line"`
	Kept   string            `json:"kept"`  // what the server keeps for this Crew, and nothing else
	Reach  []crewAccessReach `json:"reach"` // who can reach this Crew, and which switch decides it
}

type crewAccessReach struct {
	Who string `json:"who"`
	How string `json:"how"`
}

// Files and chats are saved only on the Mac; the relay holds recent events in memory (crew_chat_relay.go). A Team
// adds the server copy of its shared folder and the chats In team agents answer while the Mac is off (owner, 11 Nov
// approved mockup docs/design/agentbridge-panel-mock-20261111.html).
func crewAccessMacCardFor(online bool, status, line string, teamOn bool) *crewAccessMacCard {
	kept := "Only messages between agents are kept on the server, so replies can find their way back."
	team := "No Team yet. Add people under Team."
	if teamOn {
		kept = "The server keeps messages between agents, the folder you share with your Team, and Team chats answered while this Mac is off."
		team = "Only agents you put In team. Through your Mac while it is on; from the server's copy of the shared folder when it is off."
	}
	return &crewAccessMacCard{Online: online, Status: status, Line: line, Kept: kept, Reach: []crewAccessReach{
		{Who: "You, from your other agents", How: "Always, on your network. Nothing to turn on."},
		{Who: "You, from the website or phone", How: "When Mac online is on and this Mac is on."},
		{Who: "Your Team", How: team},
		{Who: "Other people's agents", How: "Only agents you Publish on AgentBridge. They answer as you, by your rules."},
	}}
}

func crewAccessMacOnline(mac string, teamOn bool) *crewAccessMacCard {
	return crewAccessMacCardFor(true, "Online · "+mac, "Your files and chats are saved only on this Mac, never on the server. Your Mac does the work; use this Crew from the website and phone while it is on.", teamOn)
}

// crewPublishTabShown brings back the Share page's Publish tab (owner, 5 Nov: the first release shows Mac online and
// Team only). The Publish code, and publishing from chat and MCP, stay as they are.
const crewPublishTabShown = false

var crewAccessMacTabs = func() []crewAccessTab {
	tabs := []crewAccessTab{{ID: "mac", Label: "Mac online", Detail: "you, from anywhere", Group: "online"}}
	if crewPublishTabShown {
		tabs = append(tabs, crewAccessTab{ID: "publish", Label: "Publish", Detail: "agents on the server", Group: "online"})
	}
	return append(tabs, crewAccessPeopleTabs...)
}()

// The Crew's Access tabs (owner, 9 Nov: separate tabs for dashboards and for Slack; 11 Nov: Team and Online): Team, the
// people who work on the Crew with you (crew_share_page.go's team option; its Agents tab holds who can ask your agents
// and actions, owner 12 Nov), alone on the Team page; then the Online page: Slack, the Crew's Slack bot (added by
// crewAccessAddSlack only where it is offered), and Dashboards, everything the Crew publishes (crew_dashboards_tab.go).
// A Crew on a Mac has Mac online first.
var crewAccessPeopleTabs = []crewAccessTab{
	{ID: "team", Label: "Team", Detail: "people who work on it with you", Group: "team"},
	{ID: "dashboards", Label: "Artifacts", Detail: "what this Crew publishes", Group: "online"},
}

var crewAccessSlackTab = crewAccessTab{ID: "slack", Label: "Slack", Detail: "talk to this Crew in Slack", Group: "online"}

// crewAccessAddSlack puts the Slack tab before Dashboards when the server offers it; a tab not listed falls
// back to the first one on the page.
func (api *StreamingAPI) crewAccessAddSlack(r *http.Request, out *crewSyncReply, viewer string) {
	if len(out.AccessTabs) == 0 || out.AccessView == "summary" || !api.crewSlackTabOffered(r, viewer, out.ProjectID) {
		return
	}
	tabs := make([]crewAccessTab, 0, len(out.AccessTabs)+1)
	for _, tab := range out.AccessTabs {
		if tab.ID == "dashboards" {
			tabs = append(tabs, crewAccessSlackTab)
		}
		tabs = append(tabs, tab)
	}
	out.AccessTabs = tabs
}

var crewAccessTeamTab = crewAccessPeopleTabs

// crewAccessPage fills the Access page's fields of a crew-sync status reply, from what the reply already says.
func crewAccessPage(out *crewSyncReply) {
	b := out.Binding
	switch {
	case IsLocalMode():
		// The Mac app's own Crew: its Mac online, Publish and Team controls are here.
		out.AccessHere, out.AccessTabs = "mac", crewAccessMacTabs
		teamOn := b != nil && b.Mode == "relay" && b.Editing == "local" && crewTeamKeepsMacOnline(out.ProjectID)
		if teamOn {
			out.AccessMacLocked = crewTeamOnlineLine
		}
		name := "this Mac"
		if b != nil && b.MacName != "" {
			name = b.MacName
		}
		switch {
		case b == nil:
			out.AccessMac = crewAccessMacCardFor(false, "Only on "+name, "Your files and chats are saved only on this Mac, never on the server. Turn on to use this Crew from the website and your phone.", teamOn)
		case b.Editing == "local":
			out.AccessMac = crewAccessMacOnline(name, teamOn)
		}
	case out.AccessView == "summary":
		// one line (access_summary)
	case out.Role == "member":
		out.AccessTabs = crewAccessTeamTab
	case out.AccessView == "team":
		// The website copy of the owner's Mac Crew: the Mac's page, its Mac-only rows as text.
		out.AccessTabs = crewAccessMacTabs
		mac := "your Mac"
		if b != nil && b.MacName != "" {
			mac = b.MacName
		}
		teamOn := crewTeamRecordOn(out.ProjectID)
		if teamOn {
			out.AccessMacLocked = crewTeamOnlineLine
		}
		if out.Place == "mac" {
			out.AccessMac = crewAccessMacOnline(mac, teamOn)
		} else {
			line := "It comes back when the Mac app opens."
			if seen := crewMacOffSince(out.ProjectID); !seen.IsZero() {
				line = "Last seen " + crewAccessAgo(seen) + ". " + line
			}
			if teamOn {
				line += " Meanwhile In team agents answer from the server."
			}
			out.AccessMac = crewAccessMacCardFor(false, "Offline · "+mac, line, teamOn)
		}
		if crewPublishTabShown {
			out.AccessPublish = &crewAccessText{Line: "Publish moves this Crew, or one of its agents, from your Mac to this server, so it works with your Mac off.", Note: "Publish from the Mac app: Share, Publish."}
		}
	default:
		// A Crew that lives on this server: its Share and Dashboards tabs and laptop access for its owner.
		out.AccessTabs = crewAccessTeamTab
		out.AccessLaptop = out.Role == "owner"
	}
}

// Mac online stays on while the Team is on (owner, 6 Nov; decisions log): in-team agents are always on, so a Team Crew
// is never taken offline on its own. The main owner turns the Team off first (only they can).
const (
	crewTeamOnlineLine       = "Stays on while the Team is on"
	crewTeamOnOfflineRefused = "The Team is on, so this Crew stays online. Turn the Team off first to take it offline."
)

// crewTeamKeepsMacOnline: on the Mac, its copy of this Crew's Team record says the Team is on.
func crewTeamKeepsMacOnline(project string) bool {
	return project != "" && readCrewTeamState(project).TeamOn
}

// crewTeamRecordOn: on the server, the online copy's Team record says the Team is on.
func crewTeamRecordOn(project string) bool {
	t, err := readCrewTeam(project)
	return err == nil && t.On
}
