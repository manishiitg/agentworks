package server

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
		team = "Only agents you share with the team. Through your Mac while it is on; from the server's copy of the shared folder when it is off."
	}
	return &crewAccessMacCard{Online: online, Status: status, Line: line, Kept: kept, Reach: []crewAccessReach{
		{Who: "You, from your other agents", How: "Always, on your network. Nothing to turn on."},
		{Who: "You, from the website or phone", How: "When Phone & web is on and this Mac is on."},
		{Who: "Your Team", How: team},
		{Who: "Other people's agents", How: "Only agents with Discovery on in AgentBridge. They answer as you, by your rules."},
	}}
}

func crewAccessMacOnline(mac string, teamOn bool) *crewAccessMacCard {
	return crewAccessMacCardFor(true, "Online · "+mac, "Your files and chats are saved only on this Mac, never on the server. Your Mac does the work; use this project from the website and phone while it is on.", teamOn)
}

var crewAccessMacTabs = func() []crewAccessTab {
	tabs := []crewAccessTab{
		{ID: "mac", Label: "Phone & web", Detail: "you, from anywhere", Group: "online"},
		{ID: "agentbridge", Label: "AgentBridge", Detail: "your agents and discovery", Group: "online"},
	}
	return append(tabs, crewAccessPeopleTabs...)
}()

// Team and Artifacts have their own pages. Online contains Mac online
// and AgentBridge. Slack stays in Connections (owner, 15 Nov).
var crewAccessPeopleTabs = []crewAccessTab{
	{ID: "team", Label: "Team", Detail: "people who work on it with you", Group: "team"},
	{ID: "dashboards", Label: "Artifacts", Detail: "what this project publishes", Group: "online"},
}

var crewAccessTeamTab = crewAccessPeopleTabs

// A Crew that lives on the server has co-owners instead of a Team (owner, 21 Nov): the Team page is its Co-owners page,
// the Access panel with Shared with and Co-owners.
var crewAccessCoOwnerTabs = []crewAccessTab{
	{ID: "team", Label: "Co-owners", Detail: "people who build it with you", Group: "team"},
	{ID: "dashboards", Label: "Artifacts", Detail: "what this project publishes", Group: "online"},
}

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
			out.AccessMac = crewAccessMacCardFor(false, "Only on "+name, "Your files and chats are saved only on this Mac, never on the server. Turn on to use this project from the website and your phone.", teamOn)
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
				line += " Meanwhile shared agents answer from the server."
			}
			out.AccessMac = crewAccessMacCardFor(false, "Offline · "+mac, line, teamOn)
		}
	default:
		// A Crew that lives on this server: its Co-owners and Artifacts tabs and laptop access for its owner.
		out.AccessTabs = crewAccessTeamTab
		if crewLivesOnServer(out.ProjectID) {
			out.AccessTabs, out.AccessCoOwners = crewAccessCoOwnerTabs, true
		}
		out.AccessLaptop = out.Role == "owner"
	}
}

// Mac online stays on while the Team is on (owner, 6 Nov; decisions log): in-team agents are always on, so a Team Crew
// is never taken offline on its own. The main owner turns the Team off first (only they can).
const (
	crewTeamOnlineLine       = "Stays on while the Team is on"
	crewTeamOnOfflineRefused = "The Team is on, so this project stays online. Turn the Team off first to take it offline."
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
