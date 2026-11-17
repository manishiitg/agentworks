package server

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Needs-you cards (#2853, owner 16 Nov): an agent asks one person in its Team for
// something with up to three buttons it picks. The card stays pinned in the Team
// panel until that person clicks or the agent withdraws it. A click is the
// person's answer: it goes back to the asking agent as a message from them, and
// "Ask my agent" hands the request to one of the person's own agents instead
// (chosen once, then remembered). Only the person asked can answer. The cards are
// entries in the work log, so nothing else is stored.
const crewWorkAskAgentButton = "Ask my agent"

type crewWorkRefusal struct{ code, text string }

func (r crewWorkRefusal) Error() string { return r.text }

func crewWorkNeedOp(op string) bool {
	return op == "work_need" || op == "work_withdraw" || op == "work_answer"
}

type crewWorkNeed struct {
	ID        string    `json:"id"`
	Line      string    `json:"line"`
	Link      string    `json:"link,omitempty"`
	Task      string    `json:"task,omitempty"`
	TaskTitle string    `json:"task_title,omitempty"`
	For       string    `json:"for"`
	ForMe     bool      `json:"for_me"`
	Buttons   []string  `json:"buttons"`
	Agent     string    `json:"agent"`
	Person    string    `json:"person"`
	State     string    `json:"state"` // waiting, answered, withdrawn
	Answer    string    `json:"answer,omitempty"`
	At        time.Time `json:"at"`
	forID     string
	agentID   string
	ownerID   string
	chatKey   string
	taskID    string
}

// crewWorkNeedsState folds needs, answers and withdrawals, oldest first.
func crewWorkNeedsState(entries []crewWorkEntry) ([]*crewWorkNeed, map[string]*crewWorkNeed) {
	list, byID := []*crewWorkNeed{}, map[string]*crewWorkNeed{}
	for _, e := range entries {
		switch e.Event {
		case "needs":
			n := &crewWorkNeed{ID: e.ID, Line: e.Line, Link: e.Link, Buttons: e.Buttons, Agent: e.Agent, State: "waiting", At: e.At, forID: e.ForID, agentID: e.AgentID, ownerID: e.OwnerID, chatKey: e.ChatKey, taskID: e.Task}
			list, byID[e.ID] = append(list, n), n
		case "answered", "withdrawn":
			if n := byID[e.Need]; n != nil && n.State == "waiting" {
				n.State, n.Answer = map[string]string{"answered": "answered", "withdrawn": "withdrawn"}[e.Event], e.Line
			}
		}
	}
	return list, byID
}

// crewWorkNeedsFor is what one viewer may see of the needs, with names and task labels filled in.
func crewWorkNeedsFor(scope crewWorkScope, viewer string, entries []crewWorkEntry, now time.Time) []*crewWorkNeed {
	visible, labels := crewWorkScopeView(scope, viewer)
	shown := slices.DeleteFunc(slices.Clone(entries), func(e crewWorkEntry) bool { return !visible(e) })
	tasks, _ := crewWorkState(shown, now)
	list, _ := crewWorkNeedsState(shown)
	for _, n := range list {
		n.For, n.ForMe, n.Person = labels[n.forID], n.forID == viewer, labels[n.ownerID]
		if t := tasks[n.taskID]; t != nil {
			n.Task, n.TaskTitle = t.Label, t.Title
		}
	}
	return list
}

// crewWorkNeedsView is the Team panel's pinned cards: every need still waiting, newest first.
func crewWorkNeedsView(team crewTeam, viewer string) []*crewWorkNeed {
	entries, err := readCrewWorkLog(team.ProjectID)
	if err != nil {
		entries = nil
	}
	waiting := slices.DeleteFunc(crewWorkNeedsFor(crewWorkScope{Project: team.ProjectID, Team: &team}, viewer, entries, time.Now().UTC()), func(n *crewWorkNeed) bool { return n.State != "waiting" })
	// #2965: requests addressed to a person are Needs-you cards too, answered in their own thread.
	for _, r := range agentTeamRequests(context.Background(), team, viewer) {
		if r.For != "team" {
			waiting = append(waiting, &crewWorkNeed{ID: r.MessageID, Line: r.Title, Link: r.Ref, For: r.For, ForMe: r.ForMe, Buttons: r.Buttons, Agent: r.Agent, Person: r.Person, State: "waiting", At: r.At, forID: r.forID, agentID: r.agentID})
		}
	}
	slices.SortFunc(waiting, func(a, b *crewWorkNeed) int { return b.At.Compare(a.At) })
	return waiting
}

// crewWorkNeedFor resolves who is asked: a current member of the Team, by first name, name or user ID.
func crewWorkNeedFor(team crewTeam, who string) (string, error) {
	who = strings.ToLower(strings.TrimSpace(who))
	if who == "" {
		return "", fmt.Errorf("Say which person you need (their name, as the Team panel shows it)")
	}
	found := []string{}
	for id, label := range crewTeamPersonLabels(team) {
		if !team.member(id) || !crewTeamAccountActive(id) {
			continue
		}
		if who == strings.ToLower(id) || who == strings.ToLower(label) || who == strings.ToLower(crewPersonShortName(id)) || who == strings.ToLower(shareOwnerLabel(id)) {
			found = append(found, id)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("Nobody called %s is in this Team. Use a name from the Team panel", who)
	}
	return "", fmt.Errorf("More than one person matches %s. Use their full name", who)
}

func crewWorkNeedButtons(raw []string) ([]string, error) {
	out := []string{}
	for _, b := range raw {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		if strings.EqualFold(b, crewWorkAskAgentButton) {
			b = crewWorkAskAgentButton
		}
		if utf8.RuneCountInString(b) > 24 || strings.ContainsAny(b, "\r\n") {
			return nil, fmt.Errorf("Keep each button to a few words (up to 24 characters)")
		}
		if !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, b) }) {
			out = append(out, b)
		}
	}
	if len(out) == 0 || len(out) > 3 {
		return nil, fmt.Errorf("Give one to three short buttons, for example Approve, Not now, %s", crewWorkAskAgentButton)
	}
	return out, nil
}

// crewWorkNeedEntry checks a need, withdrawal or answer against the unfiltered log and fills in the entry.
func crewWorkNeedEntry(scope crewWorkScope, person string, agent crewTeamRegisteredAgent, in crewWorkInput, entries []crewWorkEntry, helper *crewTeamReachableAgent, e *crewWorkEntry) error {
	_, needs := crewWorkNeedsState(entries)
	if in.Op == "work_need" {
		e.Event = "needs"
		if e.Line == "" {
			return fmt.Errorf("Say in one short line what you need from them")
		}
		buttons, err := crewWorkNeedButtons(in.Buttons)
		if err != nil {
			return err
		}
		forID, err := crewWorkNeedFor(*scope.Team, in.For)
		if err != nil {
			return err
		}
		if e.Private && forID != person {
			return crewWorkRefusal{"private_agent", "This agent isn't shared with the team, so only your own person can see its requests. Share it with the team first, or ask your own person"}
		}
		e.Buttons, e.ForID = buttons, forID
		return nil
	}
	e.Need = strings.TrimSpace(in.Need)
	n := needs[e.Need]
	if n == nil {
		return fmt.Errorf("No request %s. The Team panel and work_brief show the waiting ones", e.Need)
	}
	if n.State != "waiting" {
		return crewWorkRefusal{"not_waiting", fmt.Sprintf("That request was already %s", n.State)}
	}
	if in.Op == "work_withdraw" {
		e.Event = "withdrawn"
		asker := n.agentID == agent.ID && n.ownerID == agent.OwnerID && (agent.ID != "" || n.chatKey == agent.ChatKey)
		if !asker {
			return crewWorkRefusal{"not_asker", "Only the agent that asked can withdraw this request"}
		}
		return nil
	}
	e.Event = "answered"
	if n.forID != person {
		return crewWorkRefusal{"not_for_you", "This request is for someone else, so only they can answer it"}
	}
	at := slices.IndexFunc(n.Buttons, func(b string) bool { return strings.EqualFold(b, strings.TrimSpace(in.Button)) })
	if at < 0 {
		return fmt.Errorf("Answer with one of its buttons: %s", strings.Join(n.Buttons, ", "))
	}
	e.Line = n.Buttons[at]
	if e.Line == crewWorkAskAgentButton {
		if helper == nil {
			return fmt.Errorf("Choose one of your agents to hand this to")
		}
		e.Helper = helper.ID
	}
	return nil
}

// crewWorkAnswerHelper picks the person's own agent for an "Ask my agent" answer:
// the one named, else the one they chose last time, else their only one. With
// several and no choice yet it returns the list to choose from.
func crewWorkAnswerHelper(ctx context.Context, team crewTeam, person string, in crewWorkInput) (*crewTeamReachableAgent, map[string]any, error) {
	if !strings.EqualFold(strings.TrimSpace(in.Button), crewWorkAskAgentButton) {
		return nil, nil, nil
	}
	rows := crewTeamOwnSourceRows(ctx, team, person)
	pick := func(ref string) *crewTeamReachableAgent {
		for i := range rows {
			if ref != "" && (rows[i].ID == ref || strings.EqualFold(rows[i].Name, ref)) {
				return &rows[i]
			}
		}
		return nil
	}
	if ref := strings.TrimSpace(in.Helper); ref != "" {
		if row := pick(ref); row != nil {
			return row, nil, nil
		}
		return nil, nil, fmt.Errorf("Choose one of your own agents in this Team")
	}
	if entries, err := readCrewWorkLog(team.ProjectID); err == nil {
		for i := len(entries) - 1; i >= 0; i-- {
			if e := entries[i]; e.Event == "answered" && e.OwnerID == person && e.Helper != "" {
				if row := pick(e.Helper); row != nil {
					return row, nil, nil
				}
				break
			}
		}
	}
	switch len(rows) {
	case 0:
		return nil, nil, fmt.Errorf("None of your agents is in this Team yet, so there is nobody to hand this to. Answer with another button, or add one of your agents to the Team")
	case 1:
		return &rows[0], nil, nil
	}
	choose := []map[string]any{}
	for _, row := range rows {
		choose = append(choose, map[string]any{"id": row.ID, "name": row.Name, "online": row.Online})
	}
	return nil, map[string]any{"choose_agent": choose, "note": "Choose which of your agents handles requests like this. It's remembered for next time."}, nil
}

// crewWorkNeedDone logs the step and, for an answer, sends it on: to the asking
// agent as a message from the person, or with "Ask my agent" to the person's own
// agent. The answer is already saved, so a send that fails is reported, not undone.
func crewWorkNeedDone(ctx context.Context, team crewTeam, person string, entries []crewWorkEntry, e crewWorkEntry, helper *crewTeamReachableAgent, out map[string]any) {
	needs := crewWorkNeedsFor(crewWorkScope{Project: team.ProjectID, Team: &team}, person, entries, time.Now().UTC())
	id := firstNonEmptyTrimmed(e.Need, e.ID)
	var n *crewWorkNeed
	for _, candidate := range needs {
		if candidate.ID == id {
			n = candidate
		}
	}
	if n == nil {
		return
	}
	out["need"] = n
	switch e.Event {
	case "needs":
		log.Printf("[TEAM_ACTIVITY] project=%s event=needs_person need=%s for=%s agent_id=%s caller_id=%s buttons=%d", team.ProjectID, n.ID, n.forID, n.agentID, person, len(n.Buttons))
		out["note"] = fmt.Sprintf("%s sees this pinned in the Team panel. Their answer comes back to you as a message; until then carry on with other work. Withdraw it with work_withdraw if it's no longer needed.", n.For)
		return
	case "withdrawn":
		log.Printf("[TEAM_ACTIVITY] project=%s event=need_withdrawn need=%s agent_id=%s caller_id=%s", team.ProjectID, n.ID, n.agentID, person)
		return
	}
	action, outcome, reason := "answer", "queued", ""
	defer func() {
		log.Printf("[TEAM_ACTIVITY] project=%s event=person_action need=%s action=%s button=%q caller_id=%s asker_agent_id=%s helper=%s outcome=%s reason=%s", team.ProjectID, n.ID, action, e.Line, person, n.agentID, e.Helper, outcome, reason)
	}()
	asker := ""
	for _, row := range append(crewTeamReachableIn(ctx, team, person), crewTeamOwnSourceRows(ctx, team, person)...) {
		if row.ID == n.agentID && n.agentID != "" {
			asker = row.Address
		}
	}
	task := ""
	if n.Task != "" {
		task = fmt.Sprintf(" (task %s: %s)", n.Task, n.TaskTitle)
	}
	text, address := fmt.Sprintf("%s answered your request %q%s: %s. Carry on with that answer; no reply is needed.", n.For, n.Line, task, e.Line), asker
	if helper != nil {
		action, address = "ask_agent", helper.Address
		replyTo := "Reply to them with messages action=send to " + asker + "."
		if asker == "" {
			replyTo = "They can't be messaged now, so record the result with work_update on the task instead."
		}
		text = fmt.Sprintf("%s's agent %s asked me for something, and I'm handing it to you: %q%s.\nHandle it for me now with your own tools and judgement, the way I would. %s Ask me only if you truly can't decide.", n.Person, n.Agent, n.Line, task, replyTo)
		if n.Link != "" {
			text += "\nLink: " + n.Link
		}
		out["helper"] = helper.Name
	}
	api := runningServerAPI
	switch {
	case address == "":
		outcome, reason = "skipped", "asker_unreachable"
	case api == nil:
		outcome, reason = "skipped", "no_server"
	default:
		target, err := crewTeamBridgeTarget(ctx, person, address)
		if err == nil {
			// TeamPanel: the person clicked in their own Team panel, so their own In team agents take it as from them.
			caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: person}, Label: n.For + " (Needs you)", TeamPanel: true}
			_, err = api.sendAgentMessage(context.WithoutCancel(ctx), person, caller, target, text, "", "need-"+n.ID)
		}
		if err != nil {
			outcome, reason = "failed", crewTeamReachReason(err)
		}
	}
	out["delivered"] = outcome == "queued"
	switch {
	case outcome == "queued" && helper != nil:
		out["note"] = "On its way to " + helper.Name + ". It will handle the request and reply to the agent that asked."
	case outcome == "queued":
		out["note"] = "Your answer is on its way to " + n.Agent + "."
	default:
		out["note"] = "Your answer is saved, but " + n.Agent + " can't be reached right now. It sees the answer in its work brief."
	}
}
