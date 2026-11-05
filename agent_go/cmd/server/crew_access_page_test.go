package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// One Access page (owner, 5 Nov): the website's owner of a Mac Crew got the Mac app's controls and its "move it
// online first" page. On a server the Mac-only rows are text, and only the Mac app's own backend holds the controls.
func TestCrewAccessPageMacControlsOnlyOnTheMac(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	twin := &crewroot.SyncBinding{Side: "server", Editing: "local", MacName: "Example Mac"}
	owner := crewSyncReply{Role: "owner", AccessView: "team", Place: "mac", Binding: twin}
	crewAccessPage(&owner)
	if owner.AccessHere != "" || len(owner.AccessTabs) != len(crewAccessMacTabs) || owner.AccessMac == nil || (owner.AccessPublish == nil) == crewPublishTabShown || owner.AccessLaptop {
		t.Fatalf("website copy of a Mac Crew: %+v", owner)
	}
	member := crewSyncReply{Role: "member", AccessView: "team", Binding: twin}
	crewAccessPage(&member)
	if member.AccessHere != "" || len(member.AccessTabs) != 1 || member.AccessMac != nil || member.AccessShare || member.AccessLaptop {
		t.Fatalf("member: %+v", member)
	}
	server := crewSyncReply{Role: "owner", AccessView: "server_crew"}
	crewAccessPage(&server)
	if server.AccessHere != "" || len(server.AccessTabs) != 1 || !server.AccessShare || !server.AccessLaptop {
		t.Fatalf("Crew on the server: %+v", server)
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	mac := crewSyncReply{Binding: &crewroot.SyncBinding{Side: "local", Editing: "local"}}
	crewAccessPage(&mac)
	if mac.AccessHere != "mac" || len(mac.AccessTabs) != len(crewAccessMacTabs) || mac.AccessShare {
		t.Fatalf("Mac app's own Crew: %+v", mac)
	}
}
