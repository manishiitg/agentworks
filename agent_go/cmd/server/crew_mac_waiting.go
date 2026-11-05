package server

import (
	"context"
	"encoding/json"
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

func readCrewMacWaiting(project string) crewMacWaitingState {
	var s crewMacWaitingState
	if p, err := crewMacWaitingPath(project); err == nil {
		if raw, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(raw, &s)
		}
	}
	return s
}

func updateCrewMacWaiting(project string, change func(*crewMacWaitingState)) (crewMacWaitingState, error) {
	crewMacWaitingMu.Lock()
	defer crewMacWaitingMu.Unlock()
	s := readCrewMacWaiting(project)
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
			Args: call.Args, Inbox: inbox, From: from, At: time.Now().UTC().Format(time.RFC3339Nano)})
	})
	log.Printf("[CREW_MAC_WAITING] project=%s tab=%s action=held_for_mac reason=%s inbox=%q err=%v", project, call.Tab, reason, inbox, err)
	return err
}

// Recheck current sharing before a queued message reaches the Mac.
func (api *StreamingAPI) heldMacMessageAllowed(owner, project string, held crewMacWaitingMessage) bool {
	if owner == "" {
		return false
	}
	var args struct {
		SenderID string `json:"sender_id"`
	}
	if json.Unmarshal(held.Args, &args) != nil || args.SenderID == "" {
		return false
	}
	if held.Inbox == "" {
		_, reason := macSharedAgentFor(project, held.Tab, args.SenderID, owner)
		return reason == ""
	}
	store, err := readAgentMessageSnapshot(context.Background())
	if err != nil {
		return false
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
			return source.UserID == args.SenderID && dest.UserID == owner && dest.ID == project &&
				macSharedAgentTab(project, dest.ChatKey) == held.Tab && api.recheckAgentMessageAdmission(context.Background(), c, m) == nil
		}
	}
	return false
}

// Deliver held messages when the Mac reconnects or an agent has been published.
func (api *StreamingAPI) deliverCrewMacWaiting(owner, project, pair string) {
	if IsLocalMode() {
		return
	}
	s := readCrewMacWaiting(project)
	if len(s.Waiting) == 0 {
		return
	}
	// One hand-back per Crew at a time: the Mac's connect and its next offer both start one.
	if _, busy := crewMacDelivering.LoadOrStore(project, true); busy {
		return
	}
	defer crewMacDelivering.Delete(project)
	// Without the Mac, only what now belongs to a published agent moves on: its held messages go to it on the server.
	macOn := crewMacSocketFor(owner, project, pair) != nil
	s = readCrewMacWaiting(project)
	delivered := map[string]bool{}
	for _, held := range s.Waiting {
		// An agent published while the message waited is answered on the server from then on.
		if !api.heldMacMessageAllowed(owner, project, held) {
			delivered[held.ID] = true
			log.Printf("[CREW_MAC_WAITING] project=%s tab=%s action=refused inbox=%q reason=access_removed", project, held.Tab, held.Inbox)
			continue
		}
		published := crewPublishedTarget(project, held.Tab)
		if !published && !macOn {
			continue
		}
		send := func(call crewChatCall, timeout time.Duration) crewChatResult {
			if published {
				return api.publishedAgentCall(context.Background(), owner, project, call, held.Inbox != "") // an inbox means an agent message
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
	_, err := updateCrewMacWaiting(project, func(next *crewMacWaitingState) {
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
			held := readCrewMacWaiting(project).Waiting
			if len(held) == 0 {
				continue
			}
			if b, err := crewroot.ReadSync(project); err == nil && b != nil {
				api.deliverCrewMacWaiting(ownerOfCrewProjectID(project), project, b.PairID)
			}
		}
	}
}
