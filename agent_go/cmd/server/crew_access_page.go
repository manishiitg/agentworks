package server

// The one Access page (owner, 5 Nov: "one Access page, not two"): the Mac app and the website render the same
// component (YourComputerPanel), and this reply decides its shape. AccessHere "mac" means this app holds the Mac's own
// controls (the Mac app's local backend); anywhere else the rows only the Mac can change come as text, worded here.

type crewAccessTab struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

type crewAccessText struct {
	Line string `json:"line"`
	Note string `json:"note,omitempty"`
}

// crewAccessMacCard is the Mac online tab's "This Mac" card (owner, 6 Nov mockup): one status line and one plain
// sentence, the same on the Mac app and the website.
type crewAccessMacCard struct {
	Online bool   `json:"online"`
	Status string `json:"status"` // "Online · MacBook Air", "Offline · MacBook Air"
	Line   string `json:"line"`
}

func crewAccessMacOnline(mac string, teamOn bool) *crewAccessMacCard {
	line := "Your Mac does the work and your files stay on it. Turn off to keep this Crew on the Mac only."
	if teamOn {
		line = "Your Mac does the work and your files stay on it. When it is off, In team agents keep answering from the server."
	}
	return &crewAccessMacCard{Online: true, Status: "Online · " + mac, Line: line}
}

// crewPublishTabShown brings back the Share page's Publish tab (owner, 5 Nov: the first release shows Mac online and
// Team only). The Publish code, and publishing from chat and MCP, stay as they are.
const crewPublishTabShown = false

var crewAccessMacTabs = func() []crewAccessTab {
	tabs := []crewAccessTab{{ID: "mac", Label: "Mac online", Detail: "you, from anywhere"}}
	if crewPublishTabShown {
		tabs = append(tabs, crewAccessTab{ID: "publish", Label: "Publish", Detail: "agents on the server"})
	}
	return append(tabs, crewAccessPeopleTabs...)
}()

// The Share page's two Crew tabs (owner, 6 Nov, approved mockups): People (who works here and how: the Team and the
// Share as one list; its id stays "team") and Dashboards (what the Crew publishes). A Crew on the server, and a Team
// member, have these two; a Crew on a Mac has Mac online before them.
var crewAccessPeopleTabs = []crewAccessTab{
	{ID: "team", Label: "People", Detail: "who works here"},
	{ID: "dashboards", Label: "Dashboards", Detail: "what this Crew publishes"},
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
			out.AccessMac = &crewAccessMacCard{Status: "Only on " + name, Line: "Turn on to use this Crew from the website, your phone and your other apps. Your files stay on this Mac."}
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
			out.AccessMac = &crewAccessMacCard{Status: "Offline · " + mac, Line: line}
		}
		if crewPublishTabShown {
			out.AccessPublish = &crewAccessText{Line: "Publish moves this Crew, or one of its agents, from your Mac to this server, so it works with your Mac off.", Note: "Publish from the Mac app: Share, Publish."}
		}
	default:
		// A Crew that lives on this server: its People and Dashboards tabs and laptop access for its owner.
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
