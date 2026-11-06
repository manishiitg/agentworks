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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
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
// member's copy read it and mark the SESSIONS of that turn (kept in a file, crewStandbySessions). The identity points that
// launch the CLI, pick the Vault lender or the AI account ask by session (crewStandbySessionIdentity); a session that was
// not marked here keeps the old owner resolver. Nothing a caller sends over HTTP or MCP can mark a session.
type crewStandbyTurnKey struct{}

// The marked sessions are kept in a small file (crew-sync/standby-sessions.json in the app's state), written BEFORE
// the turn is submitted, so a restart never turns a member's queued standby turn into an owner-identity turn. A mark
// stays for good (a session id is never reused by another conversation); the member and their slot are read fresh
// from the Team record and slot table at every lookup. A mark that cannot be written, or a file that cannot be read,
// refuses the standby turn (the caller holds the message for the member's Mac) and refuses any Crew launch until it
// reads again (crewStandbySessionsErr): the unsafe direction, the owner's slot and Vault, is never the fallback.
type crewStandbyMark struct {
	Project string `json:"project"`
	Person  string `json:"person"`
}

var crewStandbySessions = struct {
	sync.Mutex
	path  string
	marks map[string]crewStandbyMark
}{}

func crewStandbySessionsPath() (string, error) {
	state, err := crewroot.StateRoot()
	return filepath.Join(state, "crew-sync", "standby-sessions.json"), err
}

// crewStandbySessionsLoadLocked reads the file once per state folder; the caller holds the lock.
func crewStandbySessionsLoadLocked() (map[string]crewStandbyMark, error) {
	path, err := crewStandbySessionsPath()
	if err != nil {
		return nil, err
	}
	if crewStandbySessions.marks != nil && crewStandbySessions.path == path {
		return crewStandbySessions.marks, nil
	}
	marks := map[string]crewStandbyMark{}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && json.Unmarshal(raw, &marks) != nil {
		return nil, fmt.Errorf("the standby sessions file %s cannot be read; delete it to continue", path)
	}
	crewStandbySessions.path, crewStandbySessions.marks = path, marks
	return marks, nil
}

// crewStandbySessionsReset forgets what is in memory (a restart); the file stays.
func crewStandbySessionsReset() {
	crewStandbySessions.Lock()
	crewStandbySessions.path, crewStandbySessions.marks = "", nil
	crewStandbySessions.Unlock()
}

// crewStandbySessionsErr: the marked sessions cannot be read now.
func crewStandbySessionsErr() error {
	crewStandbySessions.Lock()
	defer crewStandbySessions.Unlock()
	_, err := crewStandbySessionsLoadLocked()
	return err
}

// withCrewStandbyTurn: the context of a standby turn started by serverAgentCall.
func withCrewStandbyTurn(ctx context.Context) context.Context {
	return context.WithValue(ctx, crewStandbyTurnKey{}, true)
}

// markCrewStandbySession marks a session as part of a member's standby turn (its thread, the conversation it starts
// from, the imported copy). It does nothing outside a standby turn or for a person who is not such a member; an error
// means the mark is not durable and the turn must not run.
func markCrewStandbySession(ctx context.Context, project, user, session string) error {
	if on, _ := ctx.Value(crewStandbyTurnKey{}).(bool); !on || session == "" {
		return nil
	}
	if _, ok := crewMemberStandbyTurn(project, user); !ok {
		return nil
	}
	crewStandbySessions.Lock()
	defer crewStandbySessions.Unlock()
	marks, err := crewStandbySessionsLoadLocked()
	if err != nil {
		return err
	}
	if marks[session] == (crewStandbyMark{Project: project, Person: user}) {
		return nil
	}
	next := make(map[string]crewStandbyMark, len(marks)+1)
	for k, v := range marks {
		next[k] = v
	}
	next[session] = crewStandbyMark{Project: project, Person: user}
	if err := fsutil.WriteJSONAtomic(crewStandbySessions.path, next, 0o600); err != nil {
		return err
	}
	crewStandbySessions.marks = next
	return nil
}

// errCrewStandbyCaller refuses a launch on a marked session by anyone but the member it was marked for: it must not run
// in the member's slot, and must not fall back to the owner's either.
var errCrewStandbyCaller = errors.New("this conversation belongs to another person's agent")

// crewStandbySessionIdentity is the member a marked session runs as, for user. A marked session asked for by anyone
// else is marked=true with errCrewStandbyCaller. An unreadable marks file is an error too.
func crewStandbySessionIdentity(session, user string) (id crewStandbyIdentity, marked bool, err error) {
	if session == "" {
		return crewStandbyIdentity{}, false, nil
	}
	crewStandbySessions.Lock()
	marks, err := crewStandbySessionsLoadLocked()
	mark, ok := marks[session]
	crewStandbySessions.Unlock()
	if err != nil {
		return crewStandbyIdentity{}, false, err
	}
	if !ok {
		return crewStandbyIdentity{}, false, nil
	}
	if mark.Person != user {
		return crewStandbyIdentity{}, true, errCrewStandbyCaller
	}
	id = crewStandbyRunAs(mark.Project, mark.Person)
	return id, id.Member, nil
}

// crewStandbyMemberTurn: this session is the marked standby turn of user's own agent.
func crewStandbyMemberTurn(session, user string) bool {
	_, ok, err := crewStandbySessionIdentity(session, user)
	return ok && err == nil
}

// crewTurnSlot is the slot a Crew turn of user runs in and the person it runs as: the Crew owner's for everyone (a Run
// person's turn included, AGENTS.md), except a marked standby turn of a member's agent, which is the member's slot, or
// none (the shared app account) when the member holds none. A marked session of someone else, or marks that cannot be
// read, is an error: never the member's slot and never the owner's.
func crewTurnSlot(folder, session, user string) (slot, person string, err error) {
	id, marked, err := crewStandbySessionIdentity(session, user)
	if err != nil {
		return "", "", err
	}
	if marked {
		return id.Slot, id.Person, nil
	}
	slot, person = crewOwnerSlot(folder)
	return slot, person, nil
}

// turnSecretsOwner is projectSecretsOwner for a turn: the member for the marked standby turn of their agent, else the
// Crew's usual lender. A marked session asked for by someone else lends nothing but the caller's own (the launch is
// refused anyway).
func (api *StreamingAPI) turnSecretsOwner(session, userID, workspacePath string) string {
	if _, marked, err := crewStandbySessionIdentity(session, userID); marked {
		return userID
	} else if err != nil {
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
