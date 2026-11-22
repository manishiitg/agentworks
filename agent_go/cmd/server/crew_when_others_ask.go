package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// When others ask (owner, 5 Nov): when someone other than an agent's own person asks it, the turn is told, by
// instruction only, not to use that person's own accounts or share their personal information, followed by the
// person's own optional rule for the agent. No separate threads, tool or secret changes, or restarts come from it.
// The rule and the agent's Answer only / Make changes are kept in the server's agent registry row, the
// one place for every Crew kind; a Mac agent gets them with each message (withAgentRuleArgs).

// crewTeamAgentRule is one agent's settings for other people's asks, set only by the agent's own person.
type crewTeamAgentRule struct {
	WhenOthersAsk  string `json:"when_others_ask,omitempty"`
	CanMakeChanges bool   `json:"can_make_changes,omitempty"`
	// ChatShare: what teammates see of this agent's chat (crew_team_chat_private.go). Unset is Live chat.
	ChatShare string `json:"chat_share,omitempty"`
	// Audience: "" is the whole Team, "chosen" is only People (crew_team_agent_audience.go).
	Audience string   `json:"audience,omitempty"`
	People   []string `json:"people,omitempty"`
}

// whenOthersAskMaxRunes bounds the rule: it is added to every such turn's instructions.
const whenOthersAskMaxRunes = 1000

// agentRule reads the rule from this person's registered agent.
func (t crewTeam) agentRule(person, key string) crewTeamAgentRule {
	if a, ok := t.registeredAgent(person, key); ok {
		return a.Rule
	}
	return crewTeamAgentRule{}
}

// crewTeamAgentRuleFor reads the rule for one of person's agents from any chat key a turn or a call names; empty when
// the Crew has no Team record here (on a Mac the rule arrives with the call).
func crewTeamAgentRuleFor(ctx context.Context, project, person, key string) crewTeamAgentRule {
	if project == "" || person == "" || IsLocalMode() {
		return crewTeamAgentRule{}
	}
	t, err := readCrewTeam(project)
	if err != nil {
		return crewTeamAgentRule{}
	}
	return t.agentRule(person, crewTeamRuleKey(ctx, t, person, strings.TrimSpace(key)))
}

// withAgentRuleArgs adds the agent person's rule to a call that runs another person's message on a Mac (or a
// published copy): the Mac frames the turn with it.
func withAgentRuleArgs(ctx context.Context, args map[string]any, project, person, key string) map[string]any {
	rule := crewTeamAgentRuleFor(ctx, project, person, key)
	args["when_others_ask"], args["can_make_changes"] = rule.WhenOthersAsk, rule.CanMakeChanges
	return args
}

// errWhenOthersAskTooLong refuses a rule over the limit, with what to do instead.
var errWhenOthersAskTooLong = fmt.Errorf("keep the rule under %d characters", whenOthersAskMaxRunes)

// errCrewChatShareUnknown refuses anything but the two choices.
var errCrewChatShareUnknown = errors.New("choose Live or Hidden")

// setCrewTeamAgentRule saves the person's own rule, Answer only / Make changes and/or Teammates see for one of their
// agents (nil leaves that part as it is). Only the agent's own person may (crewTeamOwnAgent).
func setCrewTeamAgentRule(ctx context.Context, project, person, key string, text *string, changes *bool, chatShare *string) (crewTeamAgentRule, error) {
	if chatShare != nil && crewChatShareName(*chatShare) == "" {
		return crewTeamAgentRule{}, errCrewChatShareUnknown
	}
	if text != nil {
		trimmed := strings.TrimSpace(*text)
		if len([]rune(trimmed)) > whenOthersAskMaxRunes {
			return crewTeamAgentRule{}, errWhenOthersAskTooLong
		}
		text = &trimmed
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return crewTeamAgentRule{}, err
	}
	unlock, err := wf.LockWorkspace(ctx, dir)
	if err != nil {
		return crewTeamAgentRule{}, err
	}
	defer unlock()
	t, stored, err := crewTeamOwnAgent(ctx, project, person, key)
	if err != nil {
		return crewTeamAgentRule{}, err
	}
	rule := t.agentRule(person, stored)
	before := rule
	if text != nil {
		rule.WhenOthersAsk = *text
	}
	if changes != nil {
		rule.CanMakeChanges = *changes
	}
	if chatShare != nil {
		rule.ChatShare = *chatShare
	}
	if err = updateCrewTeamRegisteredAgent(project, person, stored, func(a *crewTeamRegisteredAgent) { a.Rule = rule }); err != nil {
		return crewTeamAgentRule{}, err
	}
	log.Printf("[CREW_TEAM] project=%s event=agent_rule user=%s agent=%s has_rule=%t can_make_changes=%t chat_share=%q revision=%d", project, logUsernameForUserID(person), stored, rule.WhenOthersAsk != "", rule.CanMakeChanges, rule.ChatShare, t.Revision)
	if chatShare != nil {
		publishCrewPanelChanged(project) // a teammate's open tab shows the chat, the note or the private line at once
	}
	if !reflect.DeepEqual(rule, before) {
		noteCrewTeamAgentChanged(project, person, stored)
	}
	return rule, nil
}

// handleCrewTeamAgentRule is POST /crew-teams/{project}/agent-rule: {agent_key, in_team?, audience?, chosen_people?,
// when_others_ask?, can_make_changes?, chat_share?}: one of the caller's own agents' Team settings from its Team tab
// row or a tool, in one call. in_team is the Shared with team switch (crew_team_in_team.go); audience/people are who it
// is shared with (crew_team_agent_audience.go).
func handleCrewTeamAgentRule(w http.ResponseWriter, r *http.Request, project, person string) {
	var in struct {
		Key            string    `json:"agent_key"`
		InTeam         *bool     `json:"in_team"`
		Audience       *string   `json:"audience"`
		People         *[]string `json:"chosen_people"`
		WhenOthersAsk  *string   `json:"when_others_ask"`
		CanMakeChanges *bool     `json:"can_make_changes"`
		ChatShare      *string   `json:"chat_share"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil || in.InTeam == nil && in.Audience == nil && in.People == nil && in.WhenOthersAsk == nil && in.CanMakeChanges == nil && in.ChatShare == nil {
		externalError(w, 400, "invalid_agent", "Choose your agent and give what to change: shared with the Team, who it is shared with, its rule, Answer only / Make changes, or what teammates see of its chat.")
		return
	}
	refuse := func(err error) {
		switch {
		case errors.Is(err, errCrewTeamAgentNotYours):
			externalError(w, 403, "agent_not_yours", "Only the agent's own person changes its Team settings. Choose one of your own agents.")
		case errors.Is(err, errCrewTeamLastShared):
			externalError(w, 400, "last_shared_agent", err.Error())
		case errors.Is(err, errWhenOthersAskTooLong):
			externalError(w, 400, "rule_too_long", "Keep the rule under 1000 characters.")
		case errors.Is(err, errCrewChatShareUnknown):
			externalError(w, 400, "invalid_chat_share", "Choose Live chat or Just the agent.")
		case errors.Is(err, errCrewTeamAudienceUnknown):
			externalError(w, 400, "invalid_audience", "Choose the whole Team (whole_team) or chosen people (chosen).")
		case errors.Is(err, errCrewTeamAudiencePerson):
			externalError(w, 400, "invalid_people", "Choose people who are in this Team (user IDs from directory), at most 200.")
		default:
			externalError(w, 409, "agent_update_failed", "It was not saved. Try again.")
		}
	}
	if in.InTeam != nil {
		if err := setCrewTeamAgentChoice(r.Context(), project, person, in.Key, *in.InTeam); err != nil {
			refuse(err)
			return
		}
	}
	if in.Audience != nil || in.People != nil {
		audience, people := "", []string(nil)
		if in.Audience != nil {
			audience = strings.TrimSpace(*in.Audience)
			if audience == "" {
				refuse(errCrewTeamAudienceUnknown)
				return
			}
		}
		if in.People != nil {
			people = *in.People
		}
		if _, err := setCrewTeamAgentAudience(r.Context(), project, person, in.Key, audience, people, in.People != nil); err != nil {
			refuse(err)
			return
		}
	}
	if in.WhenOthersAsk != nil || in.CanMakeChanges != nil || in.ChatShare != nil {
		if _, err := setCrewTeamAgentRule(r.Context(), project, person, in.Key, in.WhenOthersAsk, in.CanMakeChanges, in.ChatShare); err != nil {
			refuse(err)
			return
		}
	}
	t, stored, err := crewTeamOwnAgent(r.Context(), project, person, in.Key)
	if err != nil {
		refuse(err)
		return
	}
	rule := t.agentRule(person, stored)
	externalJSON(w, map[string]any{"in_team": t.agentInTeam(person, stored), "audience": rule.audienceName(), "chosen_people": t.audiencePeople(rule), "when_others_ask": rule.WhenOthersAsk, "has_rule": rule.WhenOthersAsk != "", "can_make_changes": rule.CanMakeChanges, "chat_share": rule.ChatShare})
}

// setOwnAgentRule is an agent's own rule from its chat (set_my_agent_identity). On a Mac the Team record is on the
// company server, reached through this Crew's Mac online connection; the agent is its Mac tab.
func (api *StreamingAPI) setOwnAgentRule(ctx context.Context, user, chatKey string, text *string, changes *bool) (crewTeamAgentRule, error) {
	project := crewResourceProjectID(chatKey)
	if !IsLocalMode() {
		return setCrewTeamAgentRule(ctx, project, user, chatKey, text, changes, nil)
	}
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.Side != "local" {
		return crewTeamAgentRule{}, fmt.Errorf("turn on this Crew's Team first; it works through Mac online")
	}
	c, err := desktopConnectionFor(user, b.ConnectionID)
	if err != nil {
		return crewTeamAgentRule{}, err
	}
	tab := macSharedAgentTab(project, chatKey)
	if tab == "" {
		return crewTeamAgentRule{}, fmt.Errorf("choose one of this Crew's agents")
	}
	body := map[string]any{"agent_key": macSharedAgentChatKey(b.PeerProjectID, tab)}
	if text != nil {
		body["when_others_ask"] = *text
	}
	if changes != nil {
		body["can_make_changes"] = *changes
	}
	var out crewTeamAgentRule
	err = crewTeamRemoteJSON(ctx, user, c, http.MethodPost, "/api/crew-teams/"+url.PathEscape(b.PeerProjectID)+"/agent-rule", body, &out)
	return out, err
}

// askerIsNotAgentOwner names who asks this turn when it is someone other than the agent's own person (ownerID), from
// what every entry point already records on the turn: another person's message to a shared Mac agent (Team, Ask from
// my agent, AgentBridge, MCP, website), a guest's call or Ask (functions, shared links and dashboards, webhooks), an
// agent message from another person's agent, a Slack bot route's sender, or another person's own turn in this Crew
// (WhatsApp included). The owner's own turns and their own schedules name nobody. It only adds an instruction.
func askerIsNotAgentOwner(ctx context.Context, req *QueryRequest, userID, ownerID string) (askerID, askerName, owner, ownerName string, ok bool) {
	owner = firstNonEmptyTrimmed(ownerID, userID)
	if req == nil || owner == "" {
		return "", "", "", "", false
	}
	switch ask := turnSharedAsk(ctx, req); {
	case ask != nil && strings.TrimSpace(ask.Sender) != "":
		askerID, askerName = ask.Sender, ask.SenderName
	case strings.TrimSpace(req.SharedAgentSender) != "":
		askerID = req.SharedAgentSender
	case strings.TrimSpace(req.CrewGuestCaller) != "":
		askerID = req.CrewGuestCaller
	case strings.TrimSpace(req.AskedBy) != "":
		askerID = req.AskedBy
	case req.BotPlatform != "" && strings.TrimSpace(req.BotUserEmail) != "":
		askerID, askerName = req.BotUserEmail, req.BotUserName
		if record := directoryUserFor("", "", req.BotUserEmail); record != nil {
			askerID = record.ID
		}
	case req.BotPlatform != "" && strings.TrimSpace(req.BotUserID) != "" && GetUserFromContext(ctx) != nil && GetUserFromContext(ctx).Provider == "bot_route":
		askerID, askerName = req.BotPlatform+":"+req.BotUserID, req.BotUserName
	default:
		askerID = userID
	}
	askerID = strings.TrimSpace(askerID)
	if askerID == "" || sanitizeUserIDForPath(askerID) == sanitizeUserIDForPath(owner) {
		return "", "", "", "", false
	}
	return askerID, firstNonEmptyTrimmed(askerName, crewPersonShortName(askerID), askerID), owner, firstNonEmptyTrimmed(crewPersonShortName(owner), owner), true
}

// turnSharedAsk is the turn's message to a shared Mac agent from another person, kept on the request or its context.
func turnSharedAsk(ctx context.Context, req *QueryRequest) *crewMacSharedAsk {
	if req != nil && req.sharedAsk != nil {
		return req.sharedAsk
	}
	ask, _ := ctx.Value(crewMacSharedAskKey{}).(*crewMacSharedAsk)
	return ask
}

// whenOthersAskNote is the one short line for another person's ask (owner, 16 Nov: "make it lighter", decisions log
// "Agent header popover, lighter Answers only"), then the owner's own rule on the next line when set. Answers only
// lets the agent help fully and only holds back changing the owner's things or acting as the owner; Make changes
// only keeps the owner's personal accounts out. An instruction to the agent, on a Mac agent's turn and a server
// agent's alike.
func whenOthersAskNote(askerName, ownerName, rule string, canChange bool) string {
	note := fmt.Sprintf("[%[1]s is asking, not %[2]s. Help fully: read, search, run checks and draft. Don't change %[2]s's files, repos or accounts, or send anything as %[2]s; for that, say %[2]s can allow it.]", askerName, ownerName)
	if canChange {
		note = fmt.Sprintf("[%[1]s is asking, not %[2]s. You may make changes for them; don't use %[2]s's personal accounts.]", askerName, ownerName)
	}
	if rule = strings.TrimSpace(rule); rule != "" {
		note += "\n" + rule
	}
	return note
}

// whenOthersAskSent remembers the note last given for each session (the conversation the agent resumes, the agent
// and the asker), so it is said once per thread
// and again only when the owner's setting or rule changes (owner, 16 Nov). It lives in memory: after a restart the
// first turn says it again.
var whenOthersAskSent = struct {
	mu   sync.Mutex
	last map[string]string
}{last: map[string]string{}}

// whenOthersAskSentKeep bounds whenOthersAskSent; past it the lines are forgotten and each note is said once more.
const whenOthersAskSentKeep = 4096

// forgetWhenOthersAskSent makes the next turn of this session say its note again.
func forgetWhenOthersAskSent(sessionID string) {
	whenOthersAskSent.mu.Lock()
	defer whenOthersAskSent.mu.Unlock()
	for key := range whenOthersAskSent.last {
		if strings.HasPrefix(key, sessionID+"\x00") {
			delete(whenOthersAskSent.last, key)
		}
	}
}

// whenOthersAskSentState returns first, changed or skipped for this thread's note and remembers it.
func whenOthersAskSentState(threadKey, note string) string {
	whenOthersAskSent.mu.Lock()
	defer whenOthersAskSent.mu.Unlock()
	previous, seen := whenOthersAskSent.last[threadKey]
	if !seen && len(whenOthersAskSent.last) >= whenOthersAskSentKeep {
		// Sessions that never close would otherwise keep their lines forever; forgetting only repeats a note once.
		clear(whenOthersAskSent.last)
	}
	whenOthersAskSent.last[threadKey] = note
	switch {
	case !seen:
		return "first"
	case previous != note:
		return "changed"
	}
	return "skipped"
}

// whenOthersAskForTurn is this turn's note ("" for the agent's own person, or when this thread already has it) and
// logs one line per such turn.
func whenOthersAskForTurn(ctx context.Context, req *QueryRequest, userID, ownerID, agentKey, sessionID string) string {
	askerID, askerName, owner, ownerName, ok := askerIsNotAgentOwner(ctx, req, userID, ownerID)
	if !ok {
		return ""
	}
	rule, canChange := "", false
	// A Mac turn gets the owner's setting with the ask from the server. On the server the stored rule is the setting,
	// for every ask (a published or Team ask carries none, which read as Answers only with no rule).
	if ask := turnSharedAsk(ctx, req); IsLocalMode() && ask != nil && ask.Sender != "" {
		rule, canChange = ask.WhenOthersAsk, ask.CanMakeChanges // the Mac sent the owner's setting with this turn
	} else {
		stored := crewTeamAgentRuleFor(ctx, crewResourceProjectID(agentKey), owner, agentKey)
		rule, canChange = stored.WhenOthersAsk, stored.CanMakeChanges
	}
	note := whenOthersAskNote(askerName, ownerName, rule, canChange)
	sent := whenOthersAskSentState(sessionID+"\x00"+agentKey+"\x00"+askerID, note)
	log.Printf("[WHEN_OTHERS_ASK] agent=%s asker=%s owner=%s custom=%t can_make_changes=%t sent=%s", agentKey, logUsernameForUserID(askerID), logUsernameForUserID(owner), strings.TrimSpace(rule) != "", canChange, sent)
	if sent == "skipped" {
		note = ""
	}
	return note
}

// crewTeamAgentUse is one app or secret (by name) the Crew's agents use; Personal marks the owner's own sign-in.
type crewTeamAgentUse struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // "app" or "secret"
	Personal bool   `json:"personal,omitempty"`
}

// crewTeamUses lists what the Crew's agents use from its owner, names only,
// marking the apps from the owner's own Vault (their own sign-ins).
func crewTeamUses(ctx context.Context, t crewTeam) ([]crewTeamAgentUse, string) {
	root := crewProjectRootByID(t.ProjectID)
	out := []crewTeamAgentUse{}
	if IsLocalMode() || root == "" {
		return out, ""
	}
	catalog := crewTeamVaultDisplayFor(t.OwnerID)
	note := ""
	if catalog == nil {
		note = "Connected app names will appear when Vault responds."
	}
	seen := map[string]bool{}
	if catalog != nil {
		selected, chosenOnly, _ := projectAppSelection(ctx, root)
		for _, server := range catalog.servers {
			name := firstNonEmptyTrimmed(server.Label, vaultServerName(server.ID))
			if projectVaultAppInUse(ctx, selected, chosenOnly, server.ID) && !seen["app:"+name] {
				seen["app:"+name] = true
				out = append(out, crewTeamAgentUse{Name: name, Kind: "app", Personal: catalog.personal[server.ID]})
			}
		}
	}
	if names, err := productSelectedGlobalSecrets(ctx, crewProfileID, root); err == nil && names != nil {
		for _, name := range *names {
			short := vaultSecretShortName(name)
			if !seen["secret:"+short] {
				seen["secret:"+short] = true
				out = append(out, crewTeamAgentUse{Name: short, Kind: "secret"})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b crewTeamAgentUse) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, note
}

// crewTeamRowUses is what one agent row shows: the owner's own sign-ins only to the owner, on their own agents.
func crewTeamRowUses(all []crewTeamAgentUse, rowOwner, crewOwner, viewer string) []crewTeamAgentUse {
	out := []crewTeamAgentUse{}
	for _, use := range all {
		if use.Personal && (viewer != rowOwner || rowOwner != crewOwner) {
			continue
		}
		out = append(out, use)
	}
	return out
}

// crewTeamPermissionsPrompt asks the person's own Main to look at what the agent reaches before its rule is chosen.
// The rule is free text and every turn names who asks (whenOthersAskNote), so it may differ per teammate.
func crewTeamPermissionsPrompt(agent string) string {
	return fmt.Sprintf("Help me decide what my %[1]s agent may do when teammates ask it. First list, for %[1]s: the apps (MCP) and secret names it uses (never values), which of them are my personal accounts (Gmail, Drive, Slack and others) and what personal information they reach, and the files and folders it can read and change. Then help me choose, per teammate if needed, what it may share and what it must protect, and whether it may only answer or also make changes. Also tell me what teammates see of %[1]s's chat: Live chat by default (history and live updates), Chat history (the saved conversation, no live view) or Just the agent (no chat); they can always still ask it. Save the result as its rule when others ask (team_sync agent_rule, chat_share for what teammates see).", agent)
}

// crewTeamWhenOthersLine is the row's fixed line: to the agent's person, what it won't do; to anyone else, a summary.
func crewTeamWhenOthersLine(rule crewTeamAgentRule, rowOwnerName string, yours bool) string {
	can := map[bool]string{true: "make changes", false: "answer only"}[rule.CanMakeChanges]
	if yours {
		return "It won't use your own accounts (marked \"yours\": Gmail, Drive, your Slack) or share your personal information."
	}
	line := fmt.Sprintf("It works as %s, without %s's own accounts.", rowOwnerName, rowOwnerName)
	if rule.WhenOthersAsk != "" {
		line += fmt.Sprintf(" %s's rule: %q", rowOwnerName, rule.WhenOthersAsk)
	}
	return line + " It can " + can + "."
}
