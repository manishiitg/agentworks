package server

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Crew on its owner's Mac is changed on that Mac (owner, 6 Nov, PLAT-856-509): "i should not be able to change settings
// etc for it online.. when its on my mac", "mac online.. just reach my agent.. from phone.. but not update settings etc..
// like the right menu should not be visible". The owner reaching it through this server, on the website or phone, gets
// its chat and no side panel (crewSyncReply.ChatOnly); over MCP or a chat tool every settings write is refused, whether
// the Mac is on or off, and the Team replies say nothing is changeable. Teammates keep what they can do.
// The Mac's own calls say so with the Crew's pair ID, which only the pair knows: this is who is asking, not a lock
// against the owner's own sign-in.

const (
	// crewChangeOnMacLine is the one line of the refusal and of the screen.
	crewChangeOnMacLine = "This Crew lives on your Mac. Change it in the app on your Mac."
	// crewMacPairHeader carries the Crew's pair ID on the Mac's own calls to this server.
	crewMacPairHeader = "X-AgentWorks-Mac-Pair"
)

// crewOwnerOnlineOfMacCrew: the caller is the owner of a Crew that lives on their Mac, reaching it through this server
// rather than from that Mac.
func crewOwnerOnlineOfMacCrew(r *http.Request, project, user string) bool {
	if IsLocalMode() || user == "" || user != ownerOfCrewProjectID(project) {
		return false
	}
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.Side != "server" || b.Editing != "local" {
		return false
	}
	sent := r.Header.Get(crewMacPairHeader)
	return sent == "" || b.PairID == "" || subtle.ConstantTimeCompare([]byte(sent), []byte(b.PairID)) != 1
}

// refuseCrewOwnerOnline answers a settings write of the owner through the server with the one plain line.
func refuseCrewOwnerOnline(w http.ResponseWriter, r *http.Request, project, user string) bool {
	if !crewOwnerOnlineOfMacCrew(r, project, user) {
		return false
	}
	log.Printf("[CREW_TEAM] project=%s event=change_on_mac path=%s", project, r.URL.Path)
	externalError(w, http.StatusForbidden, "change_on_mac", crewChangeOnMacLine)
	return true
}

// crewProjectIDOfRoot is the project ID of a Crew workspace root ("" when it is not a Crew).
func crewProjectIDOfRoot(root string) string {
	identity, ok := projectIdentityOf(canonicalCrewWorkspaceRoot(root))
	if !ok {
		return ""
	}
	record, _ := defaultProjectOwners().Lookup(identity.Product, identity.Folder)
	return record.ProjectID
}

// refuseCrewOwnerTurnOnMacTwin is the same refusal for a chat tool, which has no request: the owner's agent on a server
// turn of a Crew that lives on their Mac. The Mac's own turns run in local mode and pass; a teammate is not the owner;
// an MCP/CLI call (external direct call) was already checked, with its request, in externalCrewCapabilityCall.
func refuseCrewOwnerTurnOnMacTwin(ctx context.Context, root, user string) error {
	if IsLocalMode() || isExternalCrewDirectCall(ctx) {
		return nil
	}
	if project := crewProjectIDOfRoot(root); project != "" && user != "" && user == ownerOfCrewProjectID(project) && crewRelayTwin(project) {
		return fmt.Errorf("%s", crewChangeOnMacLine)
	}
	return nil
}

// changeOnMac makes the Team tab reply read only for the owner reaching it through the server: every row stays, nothing
// is changeable. The tab strip and MCP read this reply too.
func (out *crewTeamTabReply) changeOnMac() {
	out.Team.ChangeOnMac, out.Team.CanManage = crewChangeOnMacLine, false
	out.CanSwitch, out.CanChoose, out.CanAddFolder, out.CanRemoveFolder = false, false, false, false
	out.Asks = map[string]string{} // an Ask card's message offers changes
	for i := range out.People {
		out.People[i].CanRemove = false
	}
	for i := range out.Agents {
		agent := &out.Agents[i]
		agent.CanChange, agent.CanUnshare, agent.CanEditRule, agent.CanSetPurpose = false, false, false, false
		agent.Fixed, agent.PermissionsPrompt = crewChangeOnMacLine, ""
		if agent.AgentBridge != nil {
			agent.AgentBridge.CanChange, agent.AgentBridge.NeedsPurpose = false, false
		}
	}
}

// changeOnMac is the same for the People list.
func (view *crewPeopleView) changeOnMac() {
	view.ChangeOnMac, view.CanAdd, view.CanShare, view.AddEveryone, view.PutOnline = crewChangeOnMacLine, false, false, "", ""
	for i := range view.People {
		view.People[i].Moves = nil
	}
}

// setCrewMacPair marks one of the Mac's calls about its own Crew's Team (route "/api/crew-teams/<peer>…"), and leaves any
// other call as it is. It runs on the Mac.
func setCrewMacPair(header http.Header, owner string, c desktopConnection, route string) {
	rest, ok := strings.CutPrefix(route, "/api/crew-teams/")
	if !ok {
		return
	}
	peer, _, _ := strings.Cut(rest, "?")
	peer, _, _ = strings.Cut(peer, "/")
	peer, err := url.PathUnescape(peer)
	if err != nil {
		return
	}
	local := crewLocalProjectForTeam(owner, c, peer)
	if local == "" {
		return
	}
	if b, err := crewroot.ReadSync(local); err == nil && b != nil && b.PairID != "" {
		header.Set(crewMacPairHeader, b.PairID)
	}
}
