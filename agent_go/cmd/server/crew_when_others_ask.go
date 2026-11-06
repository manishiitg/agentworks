package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// When others ask (owner, 5 Nov): when someone other than an agent's own person asks it, the turn is told, by
// instruction only, not to use that person's own accounts or share their personal information, followed by the
// person's own optional rule for the agent. No separate threads, tool or secret changes, or restarts come from it.
// The rule and the agent's Answer only / Make changes are kept in the server's Team record (crewTeam.AgentRules), the
// one place for every Crew kind; a Mac agent gets them with each message (withAgentRuleArgs).

// crewTeamAgentRule is one agent's settings for other people's asks, set only by the agent's own person.
type crewTeamAgentRule struct {
	WhenOthersAsk  string `json:"when_others_ask,omitempty"`
	CanMakeChanges bool   `json:"can_make_changes,omitempty"`
}

// whenOthersAskMaxRunes bounds the rule: it is added to every such turn's instructions.
const whenOthersAskMaxRunes = 1000

// agentRule is person's rule for one of their agents by its AgentChoices key.
func (t crewTeam) agentRule(person, key string) crewTeamAgentRule {
	return t.AgentRules[person][key]
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

// setCrewTeamAgentRule saves the person's own rule and/or Answer only / Make changes for one of their agents (nil
// leaves that part as it is). Only the agent's own person may (crewTeamOwnAgent).
func setCrewTeamAgentRule(ctx context.Context, project, person, key string, text *string, changes *bool) (crewTeamAgentRule, error) {
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
	if text != nil {
		rule.WhenOthersAsk = *text
	}
	if changes != nil {
		rule.CanMakeChanges = *changes
	}
	if t.AgentRules == nil {
		t.AgentRules = map[string]map[string]crewTeamAgentRule{}
	}
	if t.AgentRules[person] == nil {
		t.AgentRules[person] = map[string]crewTeamAgentRule{}
	}
	if rule == (crewTeamAgentRule{}) {
		delete(t.AgentRules[person], stored)
	} else {
		t.AgentRules[person][stored] = rule
	}
	t.Revision++
	if err = writeCrewTeam(t); err != nil {
		return crewTeamAgentRule{}, err
	}
	log.Printf("[CREW_TEAM] project=%s event=agent_rule user=%s agent=%s has_rule=%t can_make_changes=%t revision=%d", project, logUsernameForUserID(person), stored, rule.WhenOthersAsk != "", rule.CanMakeChanges, t.Revision)
	return rule, nil
}

// handleCrewTeamAgentRule is POST /crew-teams/{project}/agent-rule: {agent_key, when_others_ask?, can_make_changes?}:
// one of the caller's own agents' settings from its Team tab row.
func handleCrewTeamAgentRule(w http.ResponseWriter, r *http.Request, project, person string) {
	var in struct {
		Key            string  `json:"agent_key"`
		WhenOthersAsk  *string `json:"when_others_ask"`
		CanMakeChanges *bool   `json:"can_make_changes"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil || in.WhenOthersAsk == nil && in.CanMakeChanges == nil {
		externalError(w, 400, "invalid_agent", "Choose your agent and give its rule or Answer only / Make changes.")
		return
	}
	rule, err := setCrewTeamAgentRule(r.Context(), project, person, in.Key, in.WhenOthersAsk, in.CanMakeChanges)
	switch {
	case errors.Is(err, errCrewTeamAgentNotYours):
		externalError(w, 403, "agent_not_yours", "Only the agent's own person sets what it does when others ask. Choose one of your own agents.")
	case errors.Is(err, errWhenOthersAskTooLong):
		externalError(w, 400, "rule_too_long", "Keep the rule under 1000 characters.")
	case err != nil:
		externalError(w, 409, "agent_update_failed", "It was not saved. Try again.")
	default:
		externalJSON(w, map[string]any{"when_others_ask": rule.WhenOthersAsk, "has_rule": rule.WhenOthersAsk != "", "can_make_changes": rule.CanMakeChanges})
	}
}

// setOwnAgentRule is an agent's own rule from its chat (set_my_agent_identity). On a Mac the Team record is on the
// company server, reached through this Crew's Mac online connection; the agent is its Mac tab.
func (api *StreamingAPI) setOwnAgentRule(ctx context.Context, user, chatKey string, text *string, changes *bool) (crewTeamAgentRule, error) {
	project := crewResourceProjectID(chatKey)
	if !IsLocalMode() {
		return setCrewTeamAgentRule(ctx, project, user, chatKey, text, changes)
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

// whenOthersAskNote is the turn's note for another person's ask: the fixed line, then the owner's rule if set.
func whenOthersAskNote(askerName, ownerName, rule string) string {
	note := fmt.Sprintf("## When others ask\n\nThis request is from %s, not %s. Do not use %s's personal accounts (their own Gmail, Drive, personal Slack and other apps they signed in to themselves) or share %s's personal information, unless %s allows it below.", askerName, ownerName, ownerName, ownerName, ownerName)
	if rule = strings.TrimSpace(rule); rule != "" {
		note += "\n\n" + ownerName + "'s rule for when others ask:\n" + rule
	}
	return note
}

// whenOthersAskForTurn is this turn's note ("" for the agent's own person) and logs one line per such turn.
func whenOthersAskForTurn(ctx context.Context, req *QueryRequest, userID, ownerID, agentKey string) string {
	askerID, askerName, owner, ownerName, ok := askerIsNotAgentOwner(ctx, req, userID, ownerID)
	if !ok {
		return ""
	}
	rule := ""
	if ask := turnSharedAsk(ctx, req); ask != nil && ask.Sender != "" {
		rule = ask.WhenOthersAsk
	} else {
		rule = crewTeamAgentRuleFor(ctx, crewResourceProjectID(agentKey), owner, agentKey).WhenOthersAsk
	}
	log.Printf("[WHEN_OTHERS_ASK] agent=%s asker=%s owner=%s custom=%t", agentKey, logUsernameForUserID(askerID), logUsernameForUserID(owner), strings.TrimSpace(rule) != "")
	return whenOthersAskNote(askerName, ownerName, rule)
}

// crewTeamAgentUse is one app or secret (by name) the Crew's agents use; Personal marks the owner's own sign-in.
type crewTeamAgentUse struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // "app" or "secret"
	Personal bool   `json:"personal,omitempty"`
}

// crewTeamUses lists what the Crew's agents use from its owner, names only (the same selection as crewTeamLentNow),
// marking the apps from the owner's own Vault (their own sign-ins).
func crewTeamUses(ctx context.Context, t crewTeam) []crewTeamAgentUse {
	root := crewProjectRootByID(t.ProjectID)
	out := []crewTeamAgentUse{}
	if IsLocalMode() || root == "" {
		return out
	}
	personal := map[string]bool{}
	if own, err := personalVaults(ctx, t.OwnerID); err == nil {
		for _, v := range own.Vaults {
			if v.Role == "owner" {
				for _, s := range v.Connections {
					personal[s.ID] = true
				}
			}
		}
	}
	seen := map[string]bool{}
	if servers, err := vaultServersFor(ctx, t.OwnerID); err == nil {
		selected, chosenOnly, _ := projectAppSelection(ctx, root)
		for _, server := range servers {
			name := firstNonEmptyTrimmed(server.Label, vaultServerName(server.ID))
			if projectVaultAppInUse(selected, chosenOnly, server.ID) && !seen["app:"+name] {
				seen["app:"+name] = true
				out = append(out, crewTeamAgentUse{Name: name, Kind: "app", Personal: personal[server.ID]})
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
	return out
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
	return fmt.Sprintf("Help me decide what my %[1]s agent may do when teammates ask it. First list, for %[1]s: the apps (MCP) and secret names it uses (never values), which of them are my personal accounts (Gmail, Drive, Slack and others) and what personal information they reach, and the files and folders it can read and change. Then help me choose, per teammate if needed, what it may share and what it must protect, and whether it may only answer or also make changes. Save the result as its rule when others ask (team_sync agent_rule).", agent)
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
