package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// #2965: a request can go to a person in the agent's Team ("person:<name>",
// with one to three buttons: the Needs-you card) or to the whole Team ("team").
// Either is an ordinary request thread whose other end is the person, or the
// Team until one of its agents takes it. Nothing else is stored.
//
// A Team request is taken by the first Team agent that replies in its thread
// with status=in_progress: the thread is then between the asker and that agent.
// An agent records work nobody asked for by sending to "team" with
// status=in_progress itself; it then updates that thread with its own sends.
const (
	agentTeamPersonKind = "team_person"
	agentTeamOpenKind   = "team_open"
)

func agentTeamEndpoint(e agentMessageEndpoint) bool {
	return e.Kind == agentTeamPersonKind || e.Kind == agentTeamOpenKind
}

// agentTeamTarget reads "team" or "person:<name>" from a send target.
func agentTeamTarget(raw string) (who string, ok bool) {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "team") {
		return "", true
	}
	if rest, found := strings.CutPrefix(raw, "person:"); found {
		return strings.TrimSpace(rest), true
	}
	return "", false
}

// agentTeamInbox reports whether a send into inbox goes through the Team request
// path: the thread's other end is a person or the Team, or someone other than
// its two agents replies to a Team request that was already taken.
func agentTeamInbox(ctx context.Context, inbox, userID string, caller triggerLinkCaller) bool {
	if inbox = strings.TrimSpace(inbox); inbox == "" {
		return false
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	agentMessageMu.Unlock()
	if err != nil {
		return false
	}
	for _, c := range s.Conversations {
		if c.ID == inbox {
			return agentTeamEndpoint(c.Endpoints[1]) || agentTeamRequestTaken(c) && !messageEndpointMatches(c.Endpoints[0], userID, caller) && !messageEndpointMatches(c.Endpoints[1], userID, caller)
		}
	}
	return false
}

// agentTeamRequestTaken: a Team request that an agent took; later claims get "already taken".
func agentTeamRequestTaken(c agentConversation) bool {
	return len(c.Messages) > 0 && c.Messages[0].TeamRequest && !agentTeamEndpoint(c.Endpoints[1])
}

// agentTeamOfCaller is the calling agent's Team, its row and its message endpoint.
func (api *StreamingAPI) agentTeamOfCaller(ctx context.Context, userID string, caller triggerLinkCaller) (crewTeam, crewTeamRegisteredAgent, agentMessageEndpoint, error) {
	var t crewTeam
	var row crewTeamRegisteredAgent
	found := false
	var err error
	switch {
	case caller.NamedAgentID != "":
		t, row, found, err = terminalCrewForNamed(caller.NamedAgentID)
		if !found && err == nil {
			t, row, found, err = appAgentTeamRow(ctx, userID, caller.NamedAgentID)
		}
	case caller.Stamp.Type == triggerCallerCrew && caller.Chat != nil:
		t, err = readCrewTeam(caller.Stamp.ID)
		if err == nil {
			row, found = t.registeredAgent(userID, caller.Chat.Key)
		}
	}
	if err != nil {
		return t, row, agentMessageEndpoint{}, err
	}
	if !found || row.OwnerID != userID || !t.On || !t.member(userID) || !crewTeamAccountActive(userID) || ownerOfCrewProjectID(t.ProjectID) != t.OwnerID {
		return t, row, agentMessageEndpoint{}, admissionRefused("Only an agent in a Team can ask its people or the Team. Join this agent to the Team first.")
	}
	e := callerMessageEndpoint(userID, caller)
	// The Team row travels on the endpoint. A Crew chat's TeamAgentID is also its
	// In-team identity for sending, so it is set only when In team.
	if row.InTeam || e.External {
		e.TeamAgentID = row.ID
	}
	return t, row, api.agentMessageCallerRole(e), nil
}

// sendAgentTeamRequest creates a request to a person in the Team or to the whole Team.
func (api *StreamingAPI) sendAgentTeamRequest(ctx context.Context, userID string, caller triggerLinkCaller, rawTarget, message, submission string, args map[string]interface{}) (map[string]interface{}, error) {
	who, _ := agentTeamTarget(rawTarget)
	t, row, asker, err := api.agentTeamOfCaller(ctx, userID, caller)
	if err != nil {
		return nil, err
	}
	fields, err := agentTaskFieldsFromArgs(args)
	if err != nil {
		return nil, err
	}
	if fields.Decision {
		return nil, fmt.Errorf("A decision goes in its task's thread, not to the Team")
	}
	status := agentReplyStatus(stringToolArg(args, "status"))
	m := agentMessage{ID: "msg-" + uuid.NewString(), Sequence: 1, From: 0, SentAt: time.Now().UTC(), Submission: submission, MessageType: "request", Delivery: "delivered", TeamRequest: true}
	m.DeliveredAt = m.SentAt
	labels := crewTeamPersonLabels(t)
	var dest agentMessageEndpoint
	if strings.HasPrefix(strings.TrimSpace(rawTarget), "person:") {
		forID, err := crewWorkNeedFor(t, who)
		if err != nil {
			return nil, err
		}
		if !row.InTeam && forID != userID {
			return nil, admissionRefused("This agent isn't shared with the Team, so it can only ask its own person. Share it with the Team first.")
		}
		if status != "" {
			return nil, fmt.Errorf("A request to a person starts open; leave status out")
		}
		if m.Buttons, err = crewWorkNeedButtons(stringSliceFromToolArg(args["buttons"])); err != nil {
			return nil, err
		}
		dest = agentMessageEndpoint{Kind: agentTeamPersonKind, UserID: forID, ID: t.ProjectID, Label: labels[forID]}
	} else {
		if !row.InTeam {
			return nil, admissionRefused("This agent isn't shared with the Team. Share it with the Team first.")
		}
		if status != "" && status != "in_progress" {
			return nil, fmt.Errorf("A Team request starts open, or with status=in_progress when you are doing it yourself")
		}
		if len(stringSliceFromToolArg(args["buttons"])) > 0 {
			return nil, fmt.Errorf("Buttons are for a request to one person")
		}
		m.SelfTaken = status == "in_progress"
		dest = agentMessageEndpoint{Kind: agentTeamOpenKind, UserID: t.OwnerID, ID: t.ProjectID, Label: "Team"}
	}
	message = strings.TrimSpace(message)
	if message == "" || len([]rune(message)) > codeChatMessageMaxRune {
		return nil, fmt.Errorf("message must contain 1 to %d characters", codeChatMessageMaxRune)
	}
	m.Text = agentTaskMessageText(fields, message)
	m.Title, m.Ref, m.Files = agentTaskTitle(fields, message), agentTaskRef(fields, message), fields.Files
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		agentMessageMu.Unlock()
		return nil, err
	}
	if submission != "" {
		for _, c := range s.Conversations {
			if len(c.Messages) > 0 && c.Messages[0].Submission == submission && sameAgentMessageEndpoint(c.Endpoints[0], asker) {
				agentMessageMu.Unlock()
				if c.Messages[0].Text != m.Text {
					return nil, fmt.Errorf("submission_id already used for a different message")
				}
				return agentMessageReceipt(c.ID, c.Messages[0]), nil
			}
		}
	}
	pruneAgentConversations(&s, time.Now().UTC(), userID)
	if len(s.Conversations) >= agentConversationKeep {
		agentMessageMu.Unlock()
		return nil, fmt.Errorf("conversation capacity reached")
	}
	c := agentConversation{ID: "inbox-" + uuid.NewString(), Endpoints: [2]agentMessageEndpoint{asker, dest}, Messages: []agentMessage{m}, Next: 1}
	s.Conversations = append(s.Conversations, c)
	err = saveAgentMessageStore(ctx, s)
	agentMessageMu.Unlock()
	if err != nil {
		return nil, err
	}
	to := "open"
	if m.SelfTaken {
		to = "in_progress"
	}
	log.Printf("[AGENT_TASK] inbox=%s request=%s reply= state=new->%s status=%s title=%q ref=%q to=%s team=%s asker_agent_id=%s", c.ID, m.ID, to, status, m.Title, m.Ref, dest.Kind, t.ProjectID, row.ID)
	agentTeamRequestChanged(t)
	out := agentMessageReceipt(c.ID, m)
	switch {
	case dest.Kind == agentTeamPersonKind:
		out["next"] = fmt.Sprintf("%s sees this pinned in the Team panel with your buttons. Their answer comes back to you in this inbox as a reply; carry on with other work until then.", firstNonEmptyTrimmed(dest.Label, "They"))
	case m.SelfTaken:
		out["next"] = "This is your task now, in the Team's To-dos. Send progress in this inbox with status=in_progress, and status=done with the outcome when it's finished."
	default:
		out["next"] = "This is in the Team's To-dos. The first Team agent to reply in this inbox with status=in_progress takes it, and its reply comes back to you here."
	}
	return out, nil
}

// sendAgentTeamThread handles a send into a Team or person request thread:
// a Team agent taking an open Team request, or the asker updating its own task.
func (api *StreamingAPI) sendAgentTeamThread(ctx context.Context, userID string, caller triggerLinkCaller, inbox, message, submission string, args map[string]interface{}) (map[string]interface{}, error) {
	fields, err := agentTaskFieldsFromArgs(args)
	if err != nil {
		return nil, err
	}
	status := agentReplyStatus(stringToolArg(args, "status"))
	if !validAgentReplyStatus(status) {
		return nil, fmt.Errorf("status must be done, in_progress, blocked or needs_you")
	}
	message = strings.TrimSpace(message)
	if message == "" || len([]rune(message)) > codeChatMessageMaxRune {
		return nil, fmt.Errorf("message must contain 1 to %d characters", codeChatMessageMaxRune)
	}
	t, row, me, teamErr := api.agentTeamOfCaller(ctx, userID, caller)
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		agentMessageMu.Unlock()
		return nil, err
	}
	index := slices.IndexFunc(s.Conversations, func(c agentConversation) bool { return c.ID == inbox })
	if index < 0 {
		agentMessageMu.Unlock()
		return nil, fmt.Errorf("conversation unavailable or access denied")
	}
	c := &s.Conversations[index]
	request := c.Messages[0]
	now := time.Now().UTC()
	before, _ := agentTaskState(*c, request, now)
	unlock := func(err error) (map[string]interface{}, error) { agentMessageMu.Unlock(); return nil, err }
	asker := messageEndpointMatches(c.Endpoints[0], userID, caller)
	reply := agentMessage{ID: "msg-" + uuid.NewString(), SentAt: now, Submission: submission, Text: agentTaskMessageText(fields, message), Title: fields.Title, Ref: fields.Ref, Files: fields.Files, Decision: fields.Decision, Why: fields.Why}
	if !fields.Decision {
		reply.ReplyStatus = firstNonEmptyTrimmed(status, "done")
	}
	_, sameTeam := agentTeamAskerRow(t, c.Endpoints[0])
	switch {
	case !asker && (teamErr != nil || !sameTeam):
		agentMessageMu.Unlock()
		if teamErr != nil {
			return nil, teamErr
		}
		return nil, fmt.Errorf("conversation unavailable or access denied")
	case agentTeamRequestTaken(*c):
		// The thread is an ordinary one now; only its two agents use it, through the normal send.
		taker := c.Endpoints[1]
		agentMessageMu.Unlock()
		return nil, admissionRefused(fmt.Sprintf("%s (%s) already took this. Message them if you want to help.", taker.Label, firstNonEmptyTrimmed(crewTeamPersonLabels(t)[taker.UserID], shareOwnerLabel(taker.UserID))))
	case c.Endpoints[1].Kind == agentTeamPersonKind:
		agentMessageMu.Unlock()
		if asker {
			return nil, fmt.Errorf("%s answers this from the Team panel; their answer comes back here", firstNonEmptyTrimmed(c.Endpoints[1].Label, "The person"))
		}
		return nil, fmt.Errorf("conversation unavailable or access denied")
	case asker && request.SelfTaken:
		reply.From = 0
	case asker:
		return unlock(fmt.Errorf("Nobody has taken this yet. It waits in the Team's To-dos"))
	case t.ProjectID != c.Endpoints[1].ID || !row.InTeam:
		return unlock(fmt.Errorf("conversation unavailable or access denied"))
	case request.SelfTaken:
		return unlock(admissionRefused(fmt.Sprintf("%s is doing this itself. Message it if you want to help.", c.Endpoints[0].Label)))
	case status != "in_progress" || fields.Decision:
		return unlock(fmt.Errorf("To take this Team request, reply with status=in_progress and say what you will do"))
	case before != "open":
		return unlock(fmt.Errorf("This request is %s; it can't be taken now", before))
	default:
		// Take it: the thread is now between the asker and this agent, under the normal Team reach checks.
		taken := *c
		taken.Endpoints[1] = me
		if err := recheckAgentConversationContact(ctx, taken); err != nil {
			return unlock(err)
		}
		c.Endpoints[1] = me
		c.Messages[0].TaskProgress.Session = me.Session
		reply.From = 1
	}
	if submission != "" {
		for _, prior := range c.Messages {
			if prior.From == reply.From && prior.Submission == submission {
				agentMessageMu.Unlock()
				return agentMessageReceipt(c.ID, prior), nil
			}
		}
	}
	c.Next++
	reply.Sequence = c.Next
	reply.MessageType = "notification"
	agentTeamDeliveryFor(&s, c, &reply)
	if reply.ReplyStatus != "" && reply.ReplyStatus != "in_progress" {
		reply.TaskReplyTo = request.ID
	}
	c.Messages = append(c.Messages, reply)
	after, _ := agentTaskState(*c, c.Messages[0], now)
	id := c.ID
	conversation := *c
	err = saveAgentMessageStore(ctx, s)
	agentMessageMu.Unlock()
	if err != nil {
		return nil, err
	}
	if before == "open" && reply.From == 1 {
		log.Printf("[AGENT_TASK] inbox=%s request=%s event=taken taker_agent_id=%s team=%s", id, request.ID, row.ID, t.ProjectID)
	}
	logAgentTask(conversation, request, before, after, reply)
	agentTeamRequestChanged(t)
	api.kickAgentMessages(id)
	return agentMessageReceipt(id, reply), nil
}

// agentTeamDeliveryFor sets a reply's delivery the way sendAgentMessage does
// for its receiver: the Team side receives nothing, a named agent gets it in
// its mailbox, a Crew chat through the delivery worker.
func agentTeamDeliveryFor(s *agentMessageStore, c *agentConversation, m *agentMessage) {
	dest := c.Endpoints[1-m.From]
	m.Delivery = "pending"
	if agentTeamEndpoint(dest) {
		m.Delivery, m.DeliveredAt = "delivered", m.SentAt
		return
	}
	if dest.External && dest.Hosted == nil || quietAgentAttachedMessage(*m, namedMessageAgent{}) {
		m.Delivery = "stored"
	}
	if dest.NamedAgentID != "" {
		a, _ := s.destAgentOf(dest)
		m.InboxAtSend = a == nil || !namedAgentOnline(*a)
		s.NextAgentSequence++
		m.AgentSequence = s.NextAgentSequence
	}
}

func agentTeamRequestChanged(t crewTeam) {
	publishCrewPanelChanged(t.ProjectID)
	go pushCrewTeamActivityChanged(t)
}

// agentTeamRequest is one open request to a person or to the Team, as the Team views and agents see it.
type agentTeamRequest struct {
	InboxID   string    `json:"inbox_id"`
	MessageID string    `json:"message_id"`
	Title     string    `json:"title"`
	Ref       string    `json:"ref,omitempty"`
	Files     []string  `json:"files,omitempty"`
	Text      string    `json:"message"`
	Buttons   []string  `json:"buttons,omitempty"`
	For       string    `json:"for"` // "team", or the person's name
	ForMe     bool      `json:"for_me,omitempty"`
	Agent     string    `json:"agent"`
	Person    string    `json:"person"`
	State     string    `json:"state"`
	At        time.Time `json:"at"`
	agentID   string
	forID     string
}

// agentTeamRequests lists the Team's open person and Team requests whose asker is still one of its agents.
func agentTeamRequests(ctx context.Context, t crewTeam, viewer string) []agentTeamRequest {
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	agentMessageMu.Unlock()
	out := []agentTeamRequest{}
	if err != nil || !t.member(viewer) {
		return out
	}
	labels := crewTeamPersonLabels(t)
	now := time.Now().UTC()
	for _, c := range s.Conversations {
		dest := c.Endpoints[1]
		if !agentTeamEndpoint(dest) || dest.ID != t.ProjectID || len(c.Messages) == 0 {
			continue
		}
		asker, ok := agentTeamAskerRow(t, c.Endpoints[0])
		if !ok {
			continue
		}
		m := c.Messages[0]
		state, _ := agentTaskState(c, m, now)
		if state != "open" && !(m.SelfTaken && state == "in_progress") {
			continue
		}
		r := agentTeamRequest{InboxID: c.ID, MessageID: m.ID, Title: m.Title, Ref: m.Ref, Files: m.Files, Text: m.Text, Buttons: m.Buttons, For: "team", Agent: firstNonEmptyTrimmed(asker.Name, asker.Tab), Person: labels[asker.OwnerID], State: state, At: m.SentAt, agentID: asker.ID, forID: dest.UserID}
		if dest.Kind == agentTeamPersonKind {
			r.For, r.ForMe = labels[dest.UserID], dest.UserID == viewer
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b agentTeamRequest) int { return b.At.Compare(a.At) })
	return out
}

// appAgentTeamRow is a Mac app chat's Team row: the app reports its Team
// project and tab, and the row must be that same person's Mac agent there.
func appAgentTeamRow(ctx context.Context, userID, named string) (crewTeam, crewTeamRegisteredAgent, bool, error) {
	a, err := namedAgentByID(ctx, userID, named)
	if err != nil || a.UserID != userID || a.Attachment == nil || a.Attachment.DeliveryRoute != appAgentDeliveryRoute || a.Attachment.RuntimeInfo == nil || a.Attachment.RuntimeInfo.TeamProject == "" {
		return crewTeam{}, crewTeamRegisteredAgent{}, false, nil
	}
	info := a.Attachment.RuntimeInfo
	t, err := readCrewTeam(info.TeamProject)
	if err != nil {
		return t, crewTeamRegisteredAgent{}, false, err
	}
	for _, row := range t.Agents {
		if row.OwnerID == userID && row.Place == "mac" && row.Tab == info.TeamTab {
			return t, row, true, nil
		}
	}
	return t, crewTeamRegisteredAgent{}, false, nil
}

// agentTeamAskerRow is the Team agent behind a request's asking endpoint, In team or the person's own.
func agentTeamAskerRow(t crewTeam, e agentMessageEndpoint) (crewTeamRegisteredAgent, bool) {
	if row, ok := t.Agents[e.TeamAgentID]; ok && e.TeamAgentID != "" && row.OwnerID == e.UserID && t.member(row.OwnerID) && crewTeamAccountActive(row.OwnerID) {
		return row, true
	}
	for _, row := range t.Agents {
		if row.OwnerID != e.UserID || !t.member(row.OwnerID) || !crewTeamAccountActive(row.OwnerID) {
			continue
		}
		if e.NamedAgentID != "" && row.Terminal != nil && row.Terminal.NamedAgentID == e.NamedAgentID || e.NamedAgentID == "" && e.Kind == triggerCallerCrew && e.ID == t.ProjectID && e.ChatKey != "" && e.ChatKey == row.ChatKey {
			return row, true
		}
	}
	return crewTeamRegisteredAgent{}, false
}

// answerAgentPersonRequest is the person's button click on a request addressed to them.
// The answer is a reply in the thread, which reaches and wakes the asker. "Ask my agent"
// also hands the request to one of the person's own agents, which answers the asker itself.
func (api *StreamingAPI) answerAgentPersonRequest(ctx context.Context, t crewTeam, person, messageID, button string, helper *crewTeamReachableAgent) (map[string]any, error) {
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		agentMessageMu.Unlock()
		return nil, err
	}
	index := slices.IndexFunc(s.Conversations, func(c agentConversation) bool {
		return len(c.Messages) > 0 && c.Messages[0].ID == messageID && c.Endpoints[1].Kind == agentTeamPersonKind && c.Endpoints[1].ID == t.ProjectID
	})
	if index < 0 {
		agentMessageMu.Unlock()
		return nil, fmt.Errorf("No request %s. The Team panel shows the waiting ones", messageID)
	}
	c := &s.Conversations[index]
	request := c.Messages[0]
	now := time.Now().UTC()
	before, _ := agentTaskState(*c, request, now)
	if c.Endpoints[1].UserID != person {
		agentMessageMu.Unlock()
		return nil, crewWorkRefusal{"not_for_you", "This request is for someone else, so only they can answer it"}
	}
	if before != "open" {
		agentMessageMu.Unlock()
		return nil, crewWorkRefusal{"not_waiting", "That request was already answered"}
	}
	at := slices.IndexFunc(request.Buttons, func(b string) bool { return strings.EqualFold(b, strings.TrimSpace(button)) })
	if at < 0 {
		agentMessageMu.Unlock()
		return nil, fmt.Errorf("Answer with one of its buttons: %s", strings.Join(request.Buttons, ", "))
	}
	choice := request.Buttons[at]
	if choice == crewWorkAskAgentButton && helper == nil {
		agentMessageMu.Unlock()
		return nil, fmt.Errorf("Choose one of your agents to hand this to")
	}
	name := firstNonEmptyTrimmed(c.Endpoints[1].Label, shareOwnerLabel(person))
	text := fmt.Sprintf("%s answered %q: %s. Carry on with that answer.", name, request.Title, choice)
	if helper != nil {
		text = fmt.Sprintf("%s handed %q to their agent %s, which will message you with the answer.", name, request.Title, helper.Name)
	}
	c.Next++
	reply := agentMessage{ID: "msg-" + uuid.NewString(), Sequence: c.Next, From: 1, SentAt: now, Submission: "answer-" + request.ID, MessageType: "notification", ReplyStatus: "done", TaskReplyTo: request.ID, Text: text}
	agentTeamDeliveryFor(&s, c, &reply)
	c.Messages = append(c.Messages, reply)
	conversation := *c
	err = saveAgentMessageStore(ctx, s)
	agentMessageMu.Unlock()
	if err != nil {
		return nil, err
	}
	after, _ := agentTaskState(conversation, request, now)
	logAgentTask(conversation, request, before, after, reply)
	action, outcome := "answer", "queued"
	out := map[string]any{"answer": choice, "delivered": true, "note": "Your answer is on its way to " + c.Endpoints[0].Label + "."}
	if helper != nil {
		action = "ask_agent"
		out["helper"] = helper.Name
		out["note"] = "On its way to " + helper.Name + ". It will handle the request and message the agent that asked."
		if err := api.handAgentPersonRequest(ctx, t, person, name, conversation, helper); err != nil {
			outcome = "failed"
			out["delivered"] = false
			out["note"] = "Your choice is saved, but " + helper.Name + " can't be reached right now."
		}
	}
	log.Printf("[AGENT_TASK] inbox=%s request=%s event=person_answer action=%s button=%q person=%s helper=%s outcome=%s", conversation.ID, request.ID, action, choice, person, func() string {
		if helper != nil {
			return helper.ID
		}
		return ""
	}(), outcome)
	agentTeamRequestChanged(t)
	api.kickAgentMessages(conversation.ID)
	return out, nil
}

// handAgentPersonRequest sends the request to the person's own agent as a new request from them.
func (api *StreamingAPI) handAgentPersonRequest(ctx context.Context, t crewTeam, person, name string, c agentConversation, helper *crewTeamReachableAgent) error {
	request := c.Messages[0]
	askerRow, _ := agentTeamAskerRow(t, c.Endpoints[0])
	reach := ""
	for _, row := range append(crewTeamReachableIn(ctx, t, person), crewTeamOwnSourceRows(ctx, t, person)...) {
		if row.ID == askerRow.ID && askerRow.ID != "" {
			reach = row.Address
		}
	}
	replyTo := "Message them the answer with messages action=send to " + reach + "."
	if reach == "" {
		replyTo = "They can't be messaged from here now; tell me the answer instead."
	}
	text := fmt.Sprintf("%s asked me for something, and I'm handing it to you:\n\n%s\n\nHandle it for me now with your own tools and judgement, the way I would. %s Ask me only if you truly can't decide.", c.Endpoints[0].Label, request.Text, replyTo)
	target, err := crewTeamBridgeTarget(ctx, person, helper.Address)
	if err != nil {
		return err
	}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: person}, Label: name + " (Needs you)", TeamPanel: true}
	taskCtx := agentTaskSendContext(context.WithoutCancel(ctx), "", true)
	send, _ := taskCtx.Value(agentTaskSendKey{}).(agentTaskSend)
	send.Fields = agentTaskFields{Title: request.Title, Ref: request.Ref, Files: request.Files}
	_, err = api.sendAgentMessage(context.WithValue(taskCtx, agentTaskSendKey{}, send), person, caller, target, text, "", "hand-"+request.ID)
	return err
}

// agentTeamToolSend routes a send tool call to the Team request path when its
// target is "team" or "person:<name>", or its inbox is a Team request thread.
func (api *StreamingAPI) agentTeamToolSend(ctx context.Context, userID string, caller triggerLinkCaller, args map[string]interface{}) (map[string]interface{}, bool, error) {
	inbox, target := stringToolArg(args, "inbox_id"), stringToolArg(args, "target")
	_, teamTarget := agentTeamTarget(target)
	if inbox == "" && !teamTarget || inbox != "" && !agentTeamInbox(ctx, inbox, userID, caller) {
		return nil, false, nil
	}
	message, submission := stringToolArg(args, "message"), stringToolArg(args, "submission_id")
	// Someone else's request running in this chat may answer in its own thread, never ask this person's Team.
	if inbox == "" && caller.BridgeOrigin != nil && !caller.OwnerAuthority {
		return nil, true, admissionRefused("Another person's request can't ask your Team or your teammates. Ask in your own chat.")
	}
	if IsLocalMode() {
		// A Mac app chat asks through its Crew's Team server, as its own app agent there.
		// (A send into such a thread already goes there: its inbox is a network address.)
		b, err := crewroot.ReadSync(caller.Stamp.ID)
		if err != nil || b == nil || b.Side != "local" || b.ConnectionID == "" || caller.Stamp.Type != triggerCallerCrew {
			return nil, true, fmt.Errorf("This Crew isn't joined to a Team on a server, so there is no person or Team to ask here")
		}
		out, err := api.desktopAgentNetworkMessage(ctx, userID, caller, "network:"+b.ConnectionID+":"+target, desktopAgentNetworkRequest{Message: message, SubmissionID: submission, Status: stringToolArg(args, "status"), ExpectsReply: true, TaskArgs: agentTaskArgs(args)})
		return out, true, err
	}
	if inbox == "" {
		out, err := api.sendAgentTeamRequest(ctx, userID, caller, target, message, submission, args)
		return out, true, err
	}
	out, err := api.sendAgentTeamThread(ctx, userID, caller, inbox, message, submission, args)
	return out, true, err
}

// externalAgentTeamSend is agentTeamToolSend for an AgentBridge agent over MCP or the CLI.
func (api *StreamingAPI) externalAgentTeamSend(r *http.Request, claims *UserClaims, args map[string]any) (map[string]any, bool, error) {
	inbox, target := strings.TrimSpace(externalArg(args, "inbox_id")), externalArg(args, "target")
	if _, teamTarget := agentTeamTarget(target); inbox == "" && !teamTarget {
		return nil, false, nil
	}
	caller, err := api.externalMessageCaller(r.Context(), claims, args)
	if err != nil {
		if inbox != "" {
			return nil, false, nil
		}
		return nil, true, err
	}
	if caller.NamedAgentID == "" {
		if inbox != "" {
			return nil, false, nil
		}
		return nil, true, fmt.Errorf("Use your registered AgentBridge identity (agent_name) to ask a person or the Team")
	}
	// A Mac app chat running someone else's request is limited to public targets; that excludes its Team.
	if restricted, _ := args["public_only"].(bool); restricted && inbox == "" {
		return nil, true, admissionRefused("Another person's request can't ask your Team or your teammates. Ask in your own chat.")
	}
	return api.agentTeamToolSend(context.WithoutCancel(r.Context()), claims.UserID, caller, args)
}
