package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-509: Team agents stay on their maker's Mac. Messages wait here while
// it is off and return their answer to the original inbox. Publish is the only
// way to move that agent's execution onto the server (decisions log, 5 Nov).

type crewMacWaitingMessage struct {
	ID           string          `json:"id"`
	Tab          string          `json:"tab"`
	Caller       string          `json:"caller"`
	Message      string          `json:"message"`
	SubmissionID string          `json:"submission_id,omitempty"`
	Args         json.RawMessage `json:"args"`
	Inbox        string          `json:"inbox,omitempty"`
	From         int             `json:"from,omitempty"`
	At           string          `json:"at"`
	// Person is the Team member whose Mac holds the agent ("" for the Crew owner's Mac; crew_team_where.go).
	Person string `json:"person,omitempty"`
}

type crewMacWaitingState struct {
	Waiting []crewMacWaitingMessage `json:"waiting,omitempty"`
}

var (
	crewMacWaitingMu  sync.Mutex
	crewMacDelivering sync.Map
)

func crewMacWaitingPath(project string) (string, error) {
	if project == "" || strings.ContainsAny(project, `/\`) || project == "." || project == ".." {
		return "", fmt.Errorf("invalid Crew identity")
	}
	state, err := crewroot.StateRoot()
	return filepath.Join(state, "crew-sync", project+"-mac-waiting.json"), err
}

func readCrewMacWaiting(project string) (crewMacWaitingState, error) {
	var s crewMacWaitingState
	p, err := crewMacWaitingPath(project)
	if err != nil {
		return s, err
	}
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(raw, &s)
	return s, err
}

func updateCrewMacWaiting(project string, change func(*crewMacWaitingState)) (crewMacWaitingState, error) {
	crewMacWaitingMu.Lock()
	defer crewMacWaitingMu.Unlock()
	s, err := readCrewMacWaiting(project)
	if err != nil {
		return s, err
	}
	change(&s)
	p, err := crewMacWaitingPath(project)
	if err != nil {
		return s, err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return s, err
	}
	raw, _ := json.MarshalIndent(s, "", "  ")
	if err = os.WriteFile(p+".tmp", raw, 0600); err != nil {
		return s, err
	}
	return s, os.Rename(p+".tmp", p)
}

func holdForMac(project, reason string, call crewChatCall, inbox string, from int) error {
	return holdForPersonMac(project, "", reason, call, inbox, from)
}

// holdForPersonMac keeps a message for one person's Mac ("" is the Crew owner's) until it is back.
func holdForPersonMac(project, person, reason string, call crewChatCall, inbox string, from int) error {
	if call.SubmissionID == "" {
		call.SubmissionID = uuid.NewString()
	}
	_, err := updateCrewMacWaiting(project, func(s *crewMacWaitingState) {
		if call.SubmissionID != "" && slices.ContainsFunc(s.Waiting, func(w crewMacWaitingMessage) bool {
			return w.SubmissionID == call.SubmissionID && w.Inbox == inbox && w.Tab == call.Tab && string(w.Args) == string(call.Args)
		}) {
			return
		}
		s.Waiting = append(s.Waiting, crewMacWaitingMessage{ID: uuid.NewString(), Tab: call.Tab, Caller: call.Caller, Message: call.Message, SubmissionID: call.SubmissionID,
			Args: call.Args, Inbox: inbox, From: from, At: time.Now().UTC().Format(time.RFC3339Nano), Person: person})
	})
	log.Printf("[CREW_MAC_WAITING] project=%s tab=%s action=held_for_mac reason=%s inbox=%q err=%v", project, call.Tab, reason, inbox, err)
	return err
}

// Recheck current sharing before a queued message reaches the Mac.
func (api *StreamingAPI) heldMacMessageAllowed(owner, project string, held crewMacWaitingMessage) (allowed, permanent bool) {
	if owner == "" {
		return false, false
	}
	var args struct {
		SenderID string `json:"sender_id"`
	}
	if json.Unmarshal(held.Args, &args) != nil || args.SenderID == "" {
		return false, false
	}
	if held.Person != "" {
		// A member's Mac agent: the sender must still be in the Team and the agent not Only me (its Mac is on now).
		t, err := readCrewTeam(project)
		if err != nil {
			return false, false
		}
		if !t.member(args.SenderID) {
			return false, true
		}
		tabs := crewMacTabsFor(context.Background(), held.Person, project, args.SenderID)
		if len(tabs) == 0 {
			return false, false // their Mac's agents could not be read now: try again later
		}
		return slices.ContainsFunc(tabs, func(tab crewMacTeamTab) bool { return tab.Tab == held.Tab }), true
	}
	// The audience helpers treat an unreadable record as empty. For queued mail,
	// distinguish that from a confirmed removal before deciding to discard it.
	path, err := macSharedAgentsPath(project)
	if err != nil {
		return false, false
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, true
	}
	if err != nil {
		return false, false
	}
	var shared []macSharedAgent
	if json.Unmarshal(raw, &shared) != nil {
		return false, false
	}
	if _, err := readCrewTeam(project); err != nil && !os.IsNotExist(err) {
		return false, false
	}
	var reason string
	if held.Inbox == "" {
		_, reason = macSharedAgentFor(project, held.Tab, args.SenderID, owner)
	} else {
		_, reason = macSharedAgentForAgent(project, held.Tab, args.SenderID, owner)
	}
	if reason != "" {
		return false, true
	}
	if held.Inbox == "" {
		return true, false
	}
	store, err := readAgentMessageSnapshot(context.Background())
	if err != nil {
		return false, false
	}
	for _, c := range store.Conversations {
		if c.ID != held.Inbox {
			continue
		}
		for _, m := range c.Messages {
			if m.ID != held.SubmissionID || 1-m.From != held.From {
				continue
			}
			source, dest := c.Endpoints[m.From], c.Endpoints[1-m.From]
			if source.UserID != args.SenderID || dest.UserID != owner || dest.ID != project || macSharedAgentTab(project, dest.ChatKey) != held.Tab {
				return false, true
			}
			// Admission can fail on a transient runtime/store error. Only a confirmed
			// audience or identity removal above discards the waiting message.
			return api.recheckAgentMessageAdmission(context.Background(), c, m) == nil, false
		}
	}
	return false, true
}

// Deliver held messages when the Mac reconnects or an agent has been published.
func (api *StreamingAPI) deliverCrewMacWaiting(owner, project, pair string) {
	if IsLocalMode() {
		return
	}
	s, err := readCrewMacWaiting(project)
	if err != nil {
		log.Printf("[CREW_MAC_WAITING] project=%s action=retry reason=read_failed err=%q", project, err.Error())
		return
	}
	if len(s.Waiting) == 0 {
		return
	}
	// One hand-back per Crew and Mac at a time: the Mac's connect and its next offer both start one.
	person := ""
	if owner != ownerOfCrewProjectID(project) {
		person = owner // a Team member's Mac: only messages held for their agents
	}
	if _, busy := crewMacDelivering.LoadOrStore(project+"\x00"+person, true); busy {
		return
	}
	defer crewMacDelivering.Delete(project + "\x00" + person)
	// Without the Mac, only what now belongs to a published agent moves on: its held messages go to it on the server.
	macOn := crewMacSocketFor(owner, project, pair) != nil
	s, err = readCrewMacWaiting(project)
	if err != nil {
		return
	}
	delivered := map[string]bool{}
	for _, held := range s.Waiting {
		if held.Person != person {
			continue
		}
		// An agent published while the message waited is answered on the server from then on.
		if allowed, permanent := api.heldMacMessageAllowed(owner, project, held); !allowed {
			if !permanent {
				log.Printf("[CREW_MAC_WAITING] project=%s tab=%s action=retry inbox=%q reason=admission_unavailable", project, held.Tab, held.Inbox)
				continue
			}
			if held.Inbox != "" {
				err := api.appendMacAgentReply(held.Inbox, held.From, "This agent didn't get your message because it isn't shared with you anymore. Ask its owner to share it again.")
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					log.Printf("[CREW_MAC_WAITING] project=%s action=retry inbox=%q reason=notice_failed", project, held.Inbox)
					continue
				}
			}
			delivered[held.ID] = true
			log.Printf("[CREW_MAC_WAITING] project=%s tab=%s action=refused inbox=%q reason=access_removed", project, held.Tab, held.Inbox)
			continue
		}
		published := person == "" && crewPublishedTarget(project, held.Tab)
		if !published && !macOn {
			continue
		}
		send := func(call crewChatCall, timeout time.Duration) crewChatResult {
			if published {
				return api.publishedAgentCall(context.Background(), owner, project, call, held.Inbox != "") // an inbox means an agent message
			}
			if person != "" {
				return callCrewMacPair(context.Background(), person, project, pair, call, timeout)
			}
			return callCrewMacFor(context.Background(), owner, project, call, timeout)
		}
		result := send(crewChatCall{Operation: "agent_ask", Tab: held.Tab, Message: held.Message, Caller: held.Caller, Args: held.Args, SubmissionID: held.SubmissionID}, 60*time.Second)
		log.Printf("[CREW_MAC_WAITING] project=%s tab=%s action=deliver_held inbox=%q status=%d published=%t", project, held.Tab, held.Inbox, result.Status, published)
		if result.Status != http.StatusOK {
			continue
		}
		delivered[held.ID] = true
		if held.Inbox != "" {
			var args struct {
				SenderID string `json:"sender_id"`
			}
			_ = json.Unmarshal(held.Args, &args)
			held := held
			read := func(after, wait int) (map[string]any, int) {
				raw, _ := json.Marshal(map[string]any{"sender_id": args.SenderID, "after": after, "wait_seconds": wait, "submission": held.SubmissionID})
				result := send(crewChatCall{Operation: "agent_read", Tab: held.Tab, Caller: held.Caller, Args: raw}, time.Duration(wait+40)*time.Second)
				var out map[string]any
				_ = json.Unmarshal(result.Body, &out)
				return out, result.Status
			}
			go api.awaitMacAgentAnswer(held.Inbox, held.SubmissionID, held.From, read)
		}
	}
	_, err = updateCrewMacWaiting(project, func(next *crewMacWaitingState) {
		next.Waiting = slices.DeleteFunc(next.Waiting, func(w crewMacWaitingMessage) bool { return delivered[w.ID] })
	})
	if err != nil {
		log.Printf("[CREW_MAC_WAITING] project=%s action=save_failed reason=%q", project, err.Error())
	}
}

const teamMacSeenWithin = 2 * time.Minute

// teamMacsSeen is when each team member's Mac last synced the team folder, in memory only.
var teamMacsSeen = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

func noteTeamMacSeen(project, user string) {
	teamMacsSeen.Lock()
	teamMacsSeen.at[project+"/"+user] = time.Now()
	teamMacsSeen.Unlock()
}

func teamMacSeen(project, user string) time.Time {
	teamMacsSeen.Lock()
	defer teamMacsSeen.Unlock()
	return teamMacsSeen.at[project+"/"+user]
}

func (api *StreamingAPI) deliverCrewMacWaitingAll(sock *crewMacSocket) {
	sock.mu.Lock()
	twins := map[string]string{}
	for project, pair := range sock.twins {
		twins[project] = pair
	}
	sock.mu.Unlock()
	for project, pair := range twins {
		// Each Mac gets only the messages held for its own agents: the owner's, or a member's (held.Person).
		go api.deliverCrewMacWaiting(sock.owner, project, pair)
	}
}

// Retry existing waiting messages while their Mac is online, or after Publish.
func (api *StreamingAPI) startCrewMacWaiting() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	if !IsLocalMode() {
		go api.sweepCrewMacWaiting(ctx)
	}
	return cancel
}

func (api *StreamingAPI) sweepCrewMacWaiting(ctx context.Context) {
	tick := time.NewTicker(2 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		state, err := crewroot.StateRoot()
		if err != nil {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(state, "crew-sync", "*-mac-waiting.json"))
		for _, file := range files {
			project := strings.TrimSuffix(filepath.Base(file), "-mac-waiting.json")
			state, err := readCrewMacWaiting(project)
			if err != nil {
				log.Printf("[CREW_MAC_WAITING] project=%s action=retry reason=read_failed err=%q", project, err.Error())
				continue
			}
			if len(state.Waiting) == 0 {
				continue
			}
			if b, err := crewroot.ReadSync(project); err == nil && b != nil {
				api.deliverCrewMacWaiting(ownerOfCrewProjectID(project), project, b.PairID)
			}
		}
	}
}
