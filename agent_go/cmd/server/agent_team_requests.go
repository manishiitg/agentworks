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

// #2853: an agent in a Team can ask one of its people ("person:<name>", with one
// to three buttons): the request pins a Needs-you card in the Team panel and the
// person's click comes back as the reply in the same thread. Tracking work (tasks
// for oneself or for the whole Team) is the project's own issue tracker (owner,
// 19 Nov), so "self" and "team" are not targets.
const agentTeamPersonKind = "team_person"

func agentTeamEndpoint(e agentMessageEndpoint) bool { return e.Kind == agentTeamPersonKind }

// agentTeamTarget reads "person:<name>" from a send target.
func agentTeamTarget(raw string) (who string, ok bool) {
	raw = strings.TrimSpace(raw)
	if rest, found := strings.CutPrefix(raw, "person:"); found {
		return strings.TrimSpace(rest), true
	}
	return "", false
}

// agentTeamInbox reports whether a send into inbox goes to a request to a person.
func agentTeamInbox(ctx context.Context, inbox, userID string, caller triggerLinkCaller) bool {
	if inbox = strings.TrimSpace(inbox); inbox == "" {
		return false
	}
	s, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return false
	}
	for _, c := range s.Conversations {
		if c.ID == inbox {
			return agentTeamEndpoint(c.Endpoints[1])
		}
	}
	return false
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
		return t, row, agentMessageEndpoint{}, admissionRefused("Only an agent in a Team can ask its people. Join this agent to the Team first.")
	}
	e := callerMessageEndpoint(userID, caller)
	// The Team row travels on the endpoint. A Crew chat's TeamAgentID is also its
	// In-team identity for sending, so it is set only when In team.
	if row.InTeam || e.External {
		e.TeamAgentID = row.ID
	}
	return t, row, api.agentMessageCallerRole(e), nil
}

// sendAgentTeamRequest creates a request to a person in the Team: their Needs-you card.
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
	m := agentMessage{ID: "msg-" + uuid.NewString(), Sequence: 1, From: 0, SentAt: time.Now().UTC(), Submission: submission, MessageType: "request", Delivery: "delivered", TeamRequest: true}
	m.DeliveredAt = m.SentAt
	labels := crewTeamPersonLabels(t)
	forID, err := crewWorkNeedFor(t, who)
	if err != nil {
		return nil, err
	}
	if !row.InTeam && forID != userID {
		return nil, admissionRefused("This agent isn't shared with the Team, so it can only ask its own person. Share it with the Team first.")
	}
	if stringToolArg(args, "status") != "" {
		return nil, fmt.Errorf("A request to a person starts open; leave status out")
	}
	if m.Buttons, err = crewWorkNeedButtons(stringSliceFromToolArg(args["buttons"])); err != nil {
		return nil, err
	}
	dest := agentMessageEndpoint{Kind: agentTeamPersonKind, UserID: forID, ID: t.ProjectID, Label: labels[forID]}
	message = strings.TrimSpace(message)
	if message == "" || len([]rune(message)) > codeChatMessageMaxRune {
		return nil, fmt.Errorf("message must contain 1 to %d characters", codeChatMessageMaxRune)
	}
	m.Text = agentTaskMessageText(fields, message)
	m.Title, m.Ref, m.Files = agentTaskTitle(fields, message), agentTaskRef(fields, message), fields.Files
	// A file sent with the request (file_path in a coding chat) is kept with it and shown on the card.
	file, err := prepareMessageFile(args)
	if err != nil {
		return nil, err
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		agentMessageMu.Unlock()
		return nil, err
	}
	if submission != "" {
		for _, c := range s.Conversations {
			if len(c.Messages) > 0 && c.Messages[0].Submission == submission && sameAgentMessageSender(c.Endpoints[0], asker) {
				agentMessageMu.Unlock()
				if !submissionMessageMatches(c.Messages[0], m.Text, file) {
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
	if file != nil {
		if m.File, err = storeMessageFile(ctx, userID, file); err != nil {
			agentMessageMu.Unlock()
			return nil, err
		}
		m.Text = m.File.message(m.Text)
	}
	c := agentConversation{ID: "inbox-" + uuid.NewString(), Endpoints: [2]agentMessageEndpoint{asker, dest}, Messages: []agentMessage{m}, Next: 1}
	s.Conversations = append(s.Conversations, c)
	err = saveAgentMessageStore(ctx, s)
	agentMessageMu.Unlock()
	if err != nil {
		return nil, err
	}
	log.Printf("[AGENT_TASK] inbox=%s request=%s reply= state=new->open title=%q ref=%q to=%s team=%s asker_agent_id=%s", c.ID, m.ID, m.Title, m.Ref, dest.Kind, t.ProjectID, row.ID)
	agentTeamRequestChanged(t)
	out := agentMessageReceipt(c.ID, m)
	out["next"] = fmt.Sprintf("%s sees this pinned in the Team panel with your buttons. Their answer comes back to you in this inbox as a reply; carry on with other work until then.", firstNonEmptyTrimmed(dest.Label, "They"))
	return out, nil
}

// agentTeamThreadRefusal answers a send into a request to a person: the person
// answers from the Team panel, and nobody else writes in that thread.
func agentTeamThreadRefusal(ctx context.Context, userID string, caller triggerLinkCaller, inbox string) error {
	s, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return err
	}
	for _, c := range s.Conversations {
		if c.ID == inbox && messageEndpointMatches(c.Endpoints[0], userID, caller) {
			return fmt.Errorf("%s answers this from the Team panel; their answer comes back here", firstNonEmptyTrimmed(c.Endpoints[1].Label, "The person"))
		}
	}
	return fmt.Errorf("conversation unavailable or access denied")
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
		m.InboxAtSend = a == nil || a.Hosted == nil && !namedAgentOnline(*a)
		s.NextAgentSequence++
		m.AgentSequence = s.NextAgentSequence
	}
}

func agentTeamRequestChanged(t crewTeam) {
	if t.ProjectID == "" {
		return
	}
	publishCrewPanelChanged(t.ProjectID)
	go pushCrewTeamActivityChanged(t)
}

// agentTeamRequest is one open request to a person, as the Team panel and agents see it.
type agentTeamRequest struct {
	InboxID   string    `json:"inbox_id"`
	MessageID string    `json:"message_id"`
	Title     string    `json:"title"`
	Ref       string    `json:"ref,omitempty"`
	Files     []string  `json:"files,omitempty"`
	Text      string    `json:"message"`
	Buttons   []string  `json:"buttons,omitempty"`
	For       string    `json:"for"` // the person's name
	ForMe     bool      `json:"for_me,omitempty"`
	Agent     string    `json:"agent"`
	Person    string    `json:"person"`
	State     string    `json:"state"`
	At        time.Time `json:"at"`
	links     []string
	files     []crewTeamTaskFile
	agentID   string
	forID     string
	personID  string // who owns the asking agent
}

// agentTeamRequests lists the Team's open requests to its people whose asker is still one of its agents.
func agentTeamRequests(ctx context.Context, t crewTeam, viewer string) []agentTeamRequest {
	// Read-only: the shared committed snapshot, with no mailbox lock and no copy (#2965 speed).
	s, err := readAgentMessageSnapshot(ctx)
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
		if state != "open" {
			continue
		}
		r := agentTeamRequest{InboxID: c.ID, MessageID: m.ID, Title: m.Title, Ref: m.Ref, Files: m.Files, Text: m.Text, Buttons: m.Buttons, For: labels[dest.UserID], ForMe: dest.UserID == viewer, Agent: firstNonEmptyTrimmed(asker.Name, asker.Tab), Person: labels[asker.OwnerID], State: state, At: m.SentAt, agentID: asker.ID, forID: dest.UserID, personID: asker.OwnerID}
		r.links, r.files = agentTaskLinksAndFiles(c, m)
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
const agentPersonCommentMax = 4000

// A person answers with a button, a written note, or both (a review: "Changes needed" and why).
func (api *StreamingAPI) answerAgentPersonRequest(ctx context.Context, t crewTeam, person, messageID, button, comment string, helper *crewTeamReachableAgent) (map[string]any, error) {
	comment = strings.TrimSpace(comment)
	if len([]rune(comment)) > agentPersonCommentMax {
		return nil, fmt.Errorf("Keep the note under %d characters", agentPersonCommentMax)
	}
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
	choice := ""
	if strings.TrimSpace(button) != "" || comment == "" {
		at := slices.IndexFunc(request.Buttons, func(b string) bool { return strings.EqualFold(b, strings.TrimSpace(button)) })
		if at < 0 {
			agentMessageMu.Unlock()
			return nil, fmt.Errorf("Answer with one of its buttons, or write a note: %s", strings.Join(request.Buttons, ", "))
		}
		choice = request.Buttons[at]
	}
	if choice == crewWorkAskAgentButton && helper == nil {
		agentMessageMu.Unlock()
		return nil, fmt.Errorf("Choose one of your agents to hand this to")
	}
	name := firstNonEmptyTrimmed(c.Endpoints[1].Label, shareOwnerLabel(person))
	text := fmt.Sprintf("%s answered %q: %s. Carry on with that answer.", name, request.Title, choice)
	switch {
	case helper != nil:
		text = fmt.Sprintf("%s handed %q to their agent %s, which will message you with the answer.", name, request.Title, helper.Name)
	case choice == "":
		text = fmt.Sprintf("%s replied to %q:\n\n%s\n\nCarry on with that answer.", name, request.Title, comment)
	case comment != "":
		text = fmt.Sprintf("%s answered %q: %s.\n\nTheir note:\n%s\n\nCarry on with that answer.", name, request.Title, choice, comment)
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
	out := map[string]any{"answer": firstNonEmptyTrimmed(choice, "note"), "delivered": true, "note": "Your answer is on its way to " + c.Endpoints[0].Label + "."}
	if helper != nil {
		action = "ask_agent"
		out["helper"] = helper.Name
		out["note"] = "On its way to " + helper.Name + ". It will handle the request and message the agent that asked."
		if err := api.handAgentPersonRequest(ctx, t, person, name, conversation, helper, comment); err != nil {
			outcome = "failed"
			out["delivered"] = false
			out["note"] = "Your choice is saved, but " + helper.Name + " can't be reached right now."
		}
	}
	log.Printf("[AGENT_TASK] inbox=%s request=%s event=person_answer action=%s button=%q note_chars=%d person=%s helper=%s outcome=%s", conversation.ID, request.ID, action, choice, len([]rune(comment)), person, func() string {
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
func (api *StreamingAPI) handAgentPersonRequest(ctx context.Context, t crewTeam, person, name string, c agentConversation, helper *crewTeamReachableAgent, comment string) error {
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
	note := ""
	if comment != "" {
		note = "\n\nMy note: " + comment
	}
	text := fmt.Sprintf("%s asked me for something, and I'm handing it to you:\n\n%s%s\n\nHandle it for me now with your own tools and judgement, the way I would. %s Ask me only if you truly can't decide.", c.Endpoints[0].Label, request.Text, note, replyTo)
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

// agentTeamToolSend routes a send tool call to the request path when its target
// is "person:<name>", or its inbox is a request to a person.
func (api *StreamingAPI) agentTeamToolSend(ctx context.Context, userID string, caller triggerLinkCaller, args map[string]interface{}) (map[string]interface{}, bool, error) {
	inbox, target := stringToolArg(args, "inbox_id"), stringToolArg(args, "target")
	_, teamTarget := agentTeamTarget(target)
	if inbox == "" && !teamTarget || inbox != "" && !agentTeamInbox(ctx, inbox, userID, caller) {
		return nil, false, nil
	}
	message, submission := stringToolArg(args, "message"), stringToolArg(args, "submission_id")
	// Someone else's request running in this chat may answer in its own thread, never ask this person's Team.
	if inbox == "" && caller.BridgeOrigin != nil && !caller.OwnerAuthority {
		return nil, true, admissionRefused("Another person's request can't ask your teammates. Ask in your own chat.")
	}
	if IsLocalMode() && inbox == "" {
		// A Mac app chat asks through its Crew's Team server, as its own app agent there.
		// (A send into such a thread already goes there: its inbox is a network address.)
		project := agentCallerCrewProject(caller)
		b, err := crewroot.ReadSync(project)
		joined := project != "" && err == nil && b != nil && b.Side == "local" && b.ConnectionID != ""
		if joined {
			out, err := api.desktopAgentNetworkMessage(ctx, userID, caller, "network:"+b.ConnectionID+":"+target, desktopAgentNetworkRequest{Message: message, SubmissionID: submission, Status: stringToolArg(args, "status"), ExpectsReply: true, TaskArgs: agentTaskArgs(args)})
			return out, true, err
		}
		return nil, true, fmt.Errorf("This Crew isn't joined to a Team on a server, so there is no person to ask here")
	}
	if inbox == "" {
		out, err := api.sendAgentTeamRequest(ctx, userID, caller, target, message, submission, args)
		return out, true, err
	}
	return nil, true, agentTeamThreadRefusal(ctx, userID, caller, inbox)
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
		return nil, true, fmt.Errorf("Use your registered AgentBridge identity (agent_name) to ask a person")
	}
	// A Mac app chat running someone else's request is limited to public targets; that excludes its Team.
	if restricted, _ := args["public_only"].(bool); restricted && inbox == "" {
		return nil, true, admissionRefused("Another person's request can't ask your teammates. Ask in your own chat.")
	}
	return api.agentTeamToolSend(context.WithoutCancel(r.Context()), claims.UserID, caller, args)
}

// agentCallerCrewProject is the Crew the calling chat belongs to. A Mac app chat that
// runs as a hosted AgentBridge agent sends as that agent, so its Crew is on its origin.
func agentCallerCrewProject(caller triggerLinkCaller) string {
	if caller.Stamp.Type == triggerCallerCrew {
		return caller.Stamp.ID
	}
	if o := caller.BridgeOrigin; o != nil && caller.OwnerAuthority && o.Kind == triggerCallerCrew && !o.External {
		return o.ID
	}
	return ""
}
