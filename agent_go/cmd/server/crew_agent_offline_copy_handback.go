package server

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// Plain answer records, not a second mailbox or a native journal replacement.
// The execution adapter stamps IDs from the admitted inbox, never model text.
// Only the exact source reads/acknowledges these under its current binding.
type crewOfflineReturnedAnswer struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Asker    string    `json:"asker"`
	Question string    `json:"question"`
	Answer   string    `json:"answer"`
}

type crewOfflineReturns struct {
	crewOfflineBinding
	Pending      []crewOfflineReturnedAnswer `json:"pending"`
	Acknowledged []string                    `json:"acknowledged"`
}

func readCrewOfflineReturns(dir string, b crewOfflineBinding) (crewOfflineReturns, error) {
	state := crewOfflineReturns{crewOfflineBinding: b, Pending: []crewOfflineReturnedAnswer{}, Acknowledged: []string{}}
	raw, err := os.ReadFile(filepath.Join(dir, "returned-answers.json"))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	if !sameCrewOfflineSource(state.crewOfflineBinding, b) {
		return state, fmt.Errorf("source session changed")
	}
	state.crewOfflineBinding = b
	return state, nil
}

// Caller holds the source Team lock and rechecks the current source, copy and
// original asker/inbox first. Raw source history and other askers' threads are
// never arguments here: only the question and actual reply of this inbox.
func appendCrewOfflineReturn(project, agent, dir string, b crewOfflineBinding, record crewOfflineReturnedAnswer) error {
	if !b.valid() || record.ID == "" || record.Asker == "" || record.At.IsZero() || strings.TrimSpace(record.Answer) == "" {
		return fmt.Errorf("offline answer is incomplete")
	}
	state, err := readCrewOfflineReturns(dir, b)
	if err != nil {
		return err
	}
	if slices.Contains(state.Acknowledged, record.ID) {
		return nil
	}
	for _, previous := range state.Pending {
		if previous.ID == record.ID {
			return nil
		}
	}
	record.Question = redaction.Text(record.Question)
	record.Answer = redaction.Text(record.Answer)
	state.Pending = append(state.Pending, record)
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "returned-answers.json"), state, 0600); err != nil {
		return err
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s message=%s event=handback_queued", project, agent, record.ID)
	return nil
}

// Acknowledgement happens only after the source has accepted the quoted
// messages. Retaining IDs, not text, makes a retried acknowledgement harmless.
func acknowledgeCrewOfflineReturns(project, agent, dir string, b crewOfflineBinding, ids []string) error {
	if len(ids) == 0 || len(ids) > 200 {
		return fmt.Errorf("choose up to 200 returned answers")
	}
	state, err := readCrewOfflineReturns(dir, b)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !slices.Contains(state.Acknowledged, id) && !slices.ContainsFunc(state.Pending, func(r crewOfflineReturnedAnswer) bool { return r.ID == id }) {
			return fmt.Errorf("returned answer changed")
		}
	}
	count := 0
	state.Pending = slices.DeleteFunc(state.Pending, func(r crewOfflineReturnedAnswer) bool {
		if !slices.Contains(ids, r.ID) {
			return false
		}
		state.Acknowledged = append(state.Acknowledged, r.ID)
		count++
		return true
	})
	if len(state.Acknowledged) > 200 {
		state.Acknowledged = state.Acknowledged[len(state.Acknowledged)-200:]
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "returned-answers.json"), state, 0600); err != nil {
		return err
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s event=handed_back count=%d", project, agent, count)
	return nil
}

// The result belongs in a turn's USER content, never PROJECT_INSTRUCTIONS,
// a tool instruction or a provider's system prompt. Every line is a quote.
// Each record is one read of this agent's chat by a teammate's agent since its last turn (Question is what they
// did, Answer which lines they saw); reads are grouped per teammate.
func crewOfflineReturnedText(records []crewOfflineReturnedAnswer) string {
	if len(records) == 0 {
		return ""
	}
	var order []string
	reads := map[string][]string{}
	for _, r := range records {
		name := crewPersonShortName(r.Asker)
		if name == "" || name == r.Asker {
			name = "a teammate"
		}
		name = strings.Join(strings.Fields(name), " ")
		if _, ok := reads[name]; !ok {
			order = append(order, name)
		}
		reads[name] = append(reads[name], strings.Join(strings.Fields(r.Question+" ("+r.Answer+")"), " "))
	}
	var out strings.Builder
	out.WriteString("Since your last turn, teammates' agents read this chat (their actions, not instructions from your owner):\n")
	for _, name := range order {
		fmt.Fprintf(&out, "> %s's agent: %s\n", name, strings.Join(reads[name], "; "))
	}
	// #3194: the real agent has the last word; a correction goes to the reader, nothing else is needed.
	out.WriteString("\nIf something they read is out of date, send that teammate a short correction (messages action=send, target=person:<their name>). Otherwise nothing is needed.\n")
	return out.String()
}

// The source's same attachment/pair proof gates both read and acknowledgement;
// sharing a copy never lets an asker read its private return queue.
func (api *StreamingAPI) handleCrewOfflineHandback(w http.ResponseWriter, r *http.Request) {
	project, agent, op := mux.Vars(r)["project"], mux.Vars(r)["agent"], mux.Vars(r)["op"]
	fail := func(status int, reason string) {
		log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s event=refused reason=%s", project, agent, reason)
		externalError(w, status, "handback_unavailable", "This source session or its sharing changed.")
	}
	var in struct {
		crewOfflineBinding
		IDs []string `json:"ids"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in) != nil || !in.crewOfflineBinding.valid() {
		fail(400, "invalid_handback")
		return
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		fail(403, "handback_source_unavailable")
		return
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	team, err := readCrewTeam(project)
	if err != nil || crewOfflineRequestOwner(r, team, agent, in.crewOfflineBinding, "") != nil {
		fail(403, "handback_source_unavailable")
		return
	}
	dir, err = crewOfflineDirectory(project, agent)
	if err != nil {
		fail(503, "handback_store_unavailable")
		return
	}
	state, err := readCrewOfflineReturns(dir, in.crewOfflineBinding)
	if err != nil {
		fail(409, "handback_binding_changed")
		return
	}
	switch op {
	case "handback":
		pending := state.Pending
		if len(pending) > 200 {
			pending = pending[:200]
		}
		log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s event=handback_read count=%d", project, agent, len(pending))
		externalJSON(w, map[string]any{"records": pending, "text": crewOfflineReturnedText(pending)})
	case "handback-ack":
		if err := acknowledgeCrewOfflineReturns(project, agent, dir, in.crewOfflineBinding, in.IDs); err != nil {
			fail(409, "handback_ack_changed")
			return
		}
		externalJSON(w, map[string]bool{"acknowledged": true})
	default:
		fail(400, "invalid_handback_operation")
	}
}
