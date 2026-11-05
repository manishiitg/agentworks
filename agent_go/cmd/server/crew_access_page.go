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

// crewPublishTabShown brings back the Share page's Publish tab (owner, 5 Nov: the first release shows Mac online and
// Team only). The Publish code, and publishing from chat and MCP, stay as they are.
const crewPublishTabShown = false

var crewAccessMacTabs = func() []crewAccessTab {
	tabs := []crewAccessTab{{ID: "mac", Label: "Mac online", Detail: "you, from anywhere"}}
	if crewPublishTabShown {
		tabs = append(tabs, crewAccessTab{ID: "publish", Label: "Publish", Detail: "agents on the server"})
	}
	return append(tabs, crewAccessTab{ID: "team", Label: "Team", Detail: "work with people"})
}()

var crewAccessTeamTab = []crewAccessTab{{ID: "team", Label: "Team", Detail: "work with people"}}

// crewAccessPage fills the Access page's fields of a crew-sync status reply, from what the reply already says.
func crewAccessPage(out *crewSyncReply) {
	b := out.Binding
	switch {
	case IsLocalMode():
		// The Mac app's own Crew: its Mac online, Publish and Team controls are here. Who can use it once it is online.
		out.AccessHere, out.AccessTabs = "mac", crewAccessMacTabs
		out.AccessShare = b != nil && b.Editing == "server"
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
		line := "Mac online is on, but " + mac + " is off now: this Crew works again when it is back."
		if out.Place == "mac" {
			line = "Mac online is on: " + mac + " does the work while it is on. Messages pass through this server; your files stay on the Mac."
		}
		out.AccessMac = &crewAccessText{Line: line, Note: "Turn Mac online on or off, and choose AgentBridge, in the Mac app: Share, Mac online."}
		if crewPublishTabShown {
			out.AccessPublish = &crewAccessText{Line: "Publish moves this Crew, or one of its agents, from your Mac to this server, so it works with your Mac off.", Note: "Publish from the Mac app: Share, Publish."}
		}
	default:
		// A Crew that lives on this server: its Team tab, laptop access for its owner and who can use it.
		out.AccessTabs = crewAccessTeamTab
		out.AccessShare = true
		out.AccessLaptop = out.Role == "owner"
	}
}
