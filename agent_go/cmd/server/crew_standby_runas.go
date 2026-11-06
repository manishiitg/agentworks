package server

// Always on for a Team member's Mac agent (owner, 6 Nov, decisions log "A member's agent answers from the server as the
// member when they have a server account, else as the shared app account"): while the member's Mac is off, another
// person's ask of the member's In team agent is answered on the server from the member's standby copy, AS THE MEMBER:
//   - the member holds a server slot: the turn runs in that slot, on the member's own AI account limits;
//   - the member holds none: it runs as the shared app account under the same sandbox (Landlock), never in the Crew
//     owner's slot;
//   - either way the Vault apps and secrets are the member's own, the files are the member's own copy of the Team's
//     shared folder, and nothing of the Crew owner's accounts or secrets reaches the turn.
// The Crew owner's own agents keep the owner's (crewStandbyRunAs returns Member=false and nothing else changes).
//
// crewStandbyRunAs is the ONE place that says whom a standby turn runs as. Every identity point asks it through
// crewMemberStandbyTurn / crewMemberStandbyTurnAt, so there is no second copy of the rule:
//   crewTurnSlot (the slot and runtime folder of the CLI), decideTurnRunAs (the declared account), projectSecretsOwner
//   (whose Vault), coOwnerTurnOwner (whose AI account limits), the import of the member's copy
//   (importCrewStandby), the note the agent is given (crewStandbyServerNote) and the member's copy of the Team files
//   (crewStandbyCopyWanted).
// The rule is read from the Team record and the member's standby rows on every call, never remembered, so it holds
// after a restart and is gone the moment the member no longer works on their Mac with a copy here.

import (
	"context"
	"log"
	"slices"
	"strings"
)

// crewStandbyIdentity is whom a server turn of person's agent runs as.
type crewStandbyIdentity struct {
	Person string
	// Member is false for the Crew owner's own agents: the owner path, unchanged.
	Member bool
	// Slot is the member's slot account; "" runs the turn as the shared app account.
	Slot string
}

// Account names what the turn runs on, for the log and the agent's note.
func (i crewStandbyIdentity) Account() string {
	switch {
	case !i.Member:
		return "owner"
	case i.Slot != "":
		return "member_slot"
	}
	return "shared_account"
}

// crewStandbyRunAs: the owner's agents run as the owner; a member's as the member (their slot when they hold one,
// else the shared app account), never as the Crew owner.
func crewStandbyRunAs(project, person string) crewStandbyIdentity {
	if person == "" || person == ownerOfCrewProjectID(project) {
		return crewStandbyIdentity{Person: person}
	}
	return crewStandbyIdentity{Person: person, Member: true, Slot: slotHeldBy(person)}
}

// crewMemberStandbyTurn: user is a joined Team member of this Mac Crew who works on their Mac and has a standby copy
// of an agent here. A server turn of theirs in this Crew is a standby turn (their own chat waits for their Mac, so
// nothing else of theirs runs on the server). An ordinary Run person, a member working on the website and the Crew's
// owner are not.
func crewMemberStandbyTurn(project, user string) (crewStandbyIdentity, bool) {
	if IsLocalMode() || project == "" || user == "" || !crewRelayTwin(project) {
		return crewStandbyIdentity{}, false
	}
	t, err := readCrewTeam(project)
	if err != nil || !t.On || t.OwnerID == "" || user == t.OwnerID || ownerOfCrewProjectID(project) != t.OwnerID || !t.member(user) {
		return crewStandbyIdentity{}, false
	}
	if _, onMac := t.memberMac(user); !onMac {
		return crewStandbyIdentity{}, false
	}
	if rows, err := readCrewStandby(project, user); err != nil || len(rows) == 0 {
		return crewStandbyIdentity{}, false
	}
	return crewStandbyRunAs(project, user), true
}

// crewMemberStandbyTurnAt is crewMemberStandbyTurn for a Crew named by a workspace path.
func crewMemberStandbyTurnAt(workspacePath, user string) (crewStandbyIdentity, bool) {
	if IsLocalMode() || user == "" || strings.TrimSpace(workspacePath) == "" {
		return crewStandbyIdentity{}, false
	}
	rec, ok := crewOwnerRecordAt(workspacePath)
	if !ok || rec.ProjectID == "" || sanitizeUserIDForPath(rec.OwnerID) == sanitizeUserIDForPath(user) {
		return crewStandbyIdentity{}, false
	}
	return crewMemberStandbyTurn(rec.ProjectID, user)
}

// crewTurnSlot is the slot a Crew turn of user runs in and the person it runs as: the Crew owner's for everyone (a Run
// person's turn included, AGENTS.md), except a member's own agent answering from the server (a standby turn), which
// is the member's slot, or none (the shared app account) when the member holds none.
func crewTurnSlot(folder, user string) (slot, person string) {
	if id, ok := crewMemberStandbyTurnAt(folder, user); ok {
		return id.Slot, id.Person
	}
	return crewOwnerSlot(folder)
}

// crewStandbyTurnLog is the one line per standby turn: ids and roles only.
func crewStandbyTurnLog(project, agentPerson, asker string) {
	id := crewStandbyRunAs(project, agentPerson)
	log.Printf("[STANDBY_TURN] project=%s agent_person=%s asker=%s account=%s", project, logUsernameForUserID(agentPerson), logUsernameForUserID(asker), id.Account())
}

// crewMemberStandbyAgent is a member's agent as their Mac last listed it (name, purpose), for the turn and its note.
func crewMemberStandbyAgent(project, person, tab string) (macSharedAgent, bool) {
	var tabs []crewMacTeamTab
	if v, ok := crewMacTeamTabsCache.Load(project + "\x00" + person); ok {
		tabs = v.(crewMacTeamTabsRead).tabs
	} else if saved, ok := readSavedCrewMacTabs(project, person); ok {
		tabs = saved
	}
	for _, t := range tabs {
		if t.Tab == tab {
			return macSharedAgent{Tab: tab, Name: t.Name, Purpose: t.Purpose, Audience: "team"}, true
		}
	}
	return macSharedAgent{}, false
}

// crewMemberStandbyAudience is the server's audience decision for an ask of a member's agent, like macSharedAgentAccess
// for the owner's: the asker is another joined person of the Team (the owner included, Team members are equal), the
// agent is one the member shares (not Only me) and the asker's account is active. A non-empty reason refuses.
func crewMemberStandbyAudience(ctx context.Context, project, person, tab, sender string) string {
	if sender == "" || sender == person {
		return "self"
	}
	t, err := liveCrewTeam(project)
	if err != nil || !t.On || !t.member(sender) {
		return "not_in_audience"
	}
	if !slices.ContainsFunc(crewMacTabsFor(ctx, person, project, sender), func(m crewMacTeamTab) bool { return m.Tab == tab }) {
		return "not_shared"
	}
	claims := &UserClaims{UserID: sender}
	if userAccessForClaims(claims).Disabled || !userAllowedProduct(claims, crewProfileID) {
		return "no_access"
	}
	return ""
}
