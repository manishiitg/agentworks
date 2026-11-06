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
// the session marker below (crewStandbySessionIdentity), so there is no second copy of the rule:
//   crewTurnSlot (the slot and runtime folder of the CLI), decideTurnRunAs (the declared account), projectSecretsOwner
//   (whose Vault), coOwnerTurnOwner (whose AI account limits), the import of the member's copy
//   (importCrewStandby), the note the agent is given (crewStandbyServerNote) and the member's copy of the Team files
//   (crewStandbyCopyWanted).
// Only a MARKED standby turn is the member's (see the marker below); eligibility is re-read at every mark.

import (
	"context"
	"log"
	"slices"
	"sync"
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

// The marker. Who a turn runs as is NOT a property of the person: a member's own settings, their ordinary Run turns in
// the Crew (MCP ask, dashboard Ask) and the Vault screens all keep the Crew owner's rules. Only a standby turn
// (another person's ask of the member's agent, answered from the server) is the member's, and only
// serverAgentCall can start one: it puts crewStandbyTurnKey in the context, desktopMacSharedAgent and the import of the
// member's copy read it and mark the SESSIONS of that turn (crewStandbyTurnSessions). The identity points that
// launch the CLI, pick the Vault lender or the AI account ask by session (crewStandbySessionIdentity); a session that was
// not marked here keeps the old owner resolver. Nothing a caller sends over HTTP or MCP can mark a session.
type crewStandbyTurnKey struct{}

var crewStandbyTurnSessions sync.Map // session id -> crewStandbyIdentity (Member only)

// withCrewStandbyTurn: the context of a standby turn started by serverAgentCall.
func withCrewStandbyTurn(ctx context.Context) context.Context {
	return context.WithValue(ctx, crewStandbyTurnKey{}, true)
}

// markCrewStandbySession marks a session as part of a member's standby turn (its thread, the conversation it starts
// from, the imported copy). It does nothing outside a standby turn or for a person who is not such a member.
func markCrewStandbySession(ctx context.Context, project, user, session string) {
	if on, _ := ctx.Value(crewStandbyTurnKey{}).(bool); !on || session == "" {
		return
	}
	if id, ok := crewMemberStandbyTurn(project, user); ok {
		crewStandbyTurnSessions.Store(session, id)
	}
}

// crewStandbySessionIdentity is the member a marked session runs as.
func crewStandbySessionIdentity(session string) (crewStandbyIdentity, bool) {
	if session == "" {
		return crewStandbyIdentity{}, false
	}
	v, ok := crewStandbyTurnSessions.Load(session)
	if !ok {
		return crewStandbyIdentity{}, false
	}
	id, _ := v.(crewStandbyIdentity)
	return id, id.Member
}

// crewStandbyMemberTurn: this session is the standby turn of user's own agent.
func crewStandbyMemberTurn(session, user string) bool {
	id, ok := crewStandbySessionIdentity(session)
	return ok && id.Person == user
}

// crewTurnSlot is the slot a Crew turn runs in and the person it runs as: the Crew owner's for everyone (a Run person's
// turn included, AGENTS.md), except a marked standby turn of a member's agent, which is the member's slot, or none
// (the shared app account) when the member holds none.
func crewTurnSlot(folder, session string) (slot, person string) {
	if id, ok := crewStandbySessionIdentity(session); ok {
		return id.Slot, id.Person
	}
	return crewOwnerSlot(folder)
}

// turnSecretsOwner is projectSecretsOwner for a turn: the member for the marked standby turn of their agent, else the
// Crew's usual lender.
func (api *StreamingAPI) turnSecretsOwner(session, userID, workspacePath string) string {
	if crewStandbyMemberTurn(session, userID) {
		return userID
	}
	return api.projectSecretsOwner(userID, workspacePath)
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
