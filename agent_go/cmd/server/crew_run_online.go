package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Run online (owner, 12 Nov, PLAT-856-564): a copy, not a move. The agent's chat is forked into a new tab
// "<name> · online", the automations the agent picked (schedules, webhooks, watchers) are pointed at that tab, and the new tab is published
// to the server with the existing publish transfer. The original keeps running on this Mac. The copy starts once
// the source agent's reply has ended, so the fork holds its finished conversation (the handover note included),
// and the fork is idle when it is published. The person's request (or the Run online button) is the yes.

const crewRunOnlineSuffix = " · online"

var crewRunOnlineBusy sync.Map // project/tab -> struct{}

// crewRunOnlineWait is how long a copy waits for its source agent's reply to end.
var crewRunOnlineWait = 30 * time.Minute

func (api *StreamingAPI) crewRunOnlineChatAction(ctx context.Context, claims *UserClaims, project, sessionID string, args map[string]any) (string, error) {
	binding, err := crewroot.ReadSync(project)
	if err != nil || binding == nil || binding.Side != "local" {
		return "", fmt.Errorf("turn on Use from phone and web first; the online agent runs on that server")
	}
	connection, err := desktopConnectionFor(claims.UserID, binding.ConnectionID)
	if err != nil {
		return "", err
	}
	roots, err := api.crewSyncRoots(ctx, claims, project)
	if err != nil {
		return "", err
	}
	rows, err := api.macAgentRows(ctx, claims, project)
	if err != nil {
		return "", err
	}
	tab, err := api.crewRunOnlineSource(ctx, claims, project, sessionID, strings.TrimSpace(externalArg(args, "agent")), rows)
	if err != nil {
		return "", err
	}
	if place, e := crewPublishedAgent(project, tab); e != nil || place != nil {
		return "", fmt.Errorf("this is already the online agent; run online from the original on this Mac")
	}
	record, identity, err := api.crewRunOnlineRecord(ctx, claims, roots, tab)
	if err != nil {
		return "", err
	}
	// One online agent per agent: a second Run online would make another copy, not move more automations.
	if placements, e := readCrewAgentPlacements(project); e == nil {
		online := firstNonEmptyTrimmed(identity.Name, "Main") + crewRunOnlineSuffix
		for _, place := range placements {
			if place.Name == online && place.Location == "server" {
				return "", fmt.Errorf("this agent already has its online agent, %q. Moving more automations to an online agent is not built yet; for now set them up on the website for %q", online, online)
			}
		}
	}
	selection := crewAgentPublishSelection{Tab: tab, AccountID: externalArg(args, "account_id")}
	if raw, ok := args["paths"].([]any); ok {
		for _, item := range raw {
			p, ok := item.(string)
			if !ok {
				return "", fmt.Errorf("paths must be folder or file names inside this Crew")
			}
			selection.Paths = append(selection.Paths, p)
		}
	}
	if selection, err = cleanCrewAgentPublishSelection(selection); err != nil {
		return "", err
	}
	var automationNames []string
	if raw, ok := args["automations"].([]any); ok {
		for _, item := range raw {
			if name, ok := item.(string); ok && strings.TrimSpace(name) != "" {
				automationNames = append(automationNames, strings.TrimSpace(name))
			}
		}
	}
	automations, err := api.crewRunOnlineAutomations(ctx, claims, roots, project, tab, automationNames)
	if err != nil {
		return "", err
	}
	// The server account is settled before anything is copied, so a missing login leaves no stray tab behind.
	remote, err := desktopCrewSyncCall(ctx, claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "publish_accounts", PairID: binding.PairID, Provider: record.Provider, PublishAgent: &selection})
	if err != nil {
		return "", err
	}
	if remote.Publish == nil {
		return "", fmt.Errorf("update this server to run agents online")
	}
	if !remote.Publish.Ready {
		return "", fmt.Errorf("%s", remote.Publish.Message)
	}
	selection.AccountID = remote.Publish.Selection.AccountID
	key := project + "/" + tab
	if _, busy := crewRunOnlineBusy.LoadOrStore(key, struct{}{}); busy {
		return "", fmt.Errorf("the online agent for this agent is already being made")
	}
	name := firstNonEmptyTrimmed(identity.Name, "Main")
	go func() {
		defer crewRunOnlineBusy.Delete(key)
		background := context.WithoutCancel(ctx)
		started := time.Now()
		copyName, note, err := api.runCrewAgentOnline(background, claims, connection, roots, record.SessionID, tab, name, selection, automations)
		result := "copied"
		if err != nil {
			result = "failed"
		}
		log.Printf("[CREW_RUN_ONLINE] owner=%s project=%s tab=%s result=%s automations=%d paths=%d elapsed_ms=%d", claims.UserID, project, tab, result, automations.count(), len(selection.Paths), time.Since(started).Milliseconds())
		message := fmt.Sprintf("[Run online] %q is now running on the server as its own agent. It has this chat's history up to now, your memory and skills, %d chosen path(s) and %d automation(s); this original stays on this Mac. Tell the person in one or two lines: open %q on the website to try it, and the two have separate memory and files from now on.%s", name, len(selection.Paths), automations.count(), copyName, note+crewRunOnlineWebhookNote(automations))
		if err != nil {
			message = fmt.Sprintf("[Run online] The online agent for %q was not made: %s. Tell the person in one line what happened and what they can do.", name, err.Error())
		}
		if !api.executeSyntheticTurn(record.SessionID, message) {
			log.Printf("[CREW_RUN_ONLINE] project=%s tab=%s report=undelivered", project, tab)
		}
	}()
	account := ""
	for _, o := range remote.Publish.Accounts {
		if o.ID == selection.AccountID {
			account = crewServerAccountLabel(o)
		}
	}
	raw, _ := json.Marshal(map[string]any{"status": "starting", "agent": name, "online_name": name + crewRunOnlineSuffix, "account": account, "paths": selection.Paths, "automations": automationNames, "next": "The online agent is made when this reply ends; a message arrives in this chat when it runs. The original stays on this Mac. Call it the online agent, never a copy or backup (the backup is the Team's, for when the Mac is off). Say which account it runs on."})
	return string(raw), nil
}

// crewRunOnlineSource is the agent being copied: the one named, or the calling chat itself.
func (api *StreamingAPI) crewRunOnlineSource(ctx context.Context, claims *UserClaims, project, sessionID, name string, rows []macAgentRow) (string, error) {
	if name != "" {
		tab := ""
		for _, row := range rows {
			if row.Tab == name || strings.EqualFold(row.Name, name) {
				if tab != "" {
					return "", fmt.Errorf("more than one agent has that name; choose it by tab from agents")
				}
				tab = row.Tab
			}
		}
		if tab == "" {
			return "", fmt.Errorf("choose this Crew's agent by name from agents")
		}
		return tab, nil
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(claims.UserID))
	if err != nil {
		return "", err
	}
	for _, record := range doc.Entries {
		if record.ProfileID == crewProfileID && record.SessionID == sessionID && crewResourceProjectID(record.ConversationKey) == project {
			return crewAgentPublishTab(record.ConversationKey), nil
		}
	}
	return "", fmt.Errorf("say which agent to run online (agent by name from agents)")
}

// crewRunOnlineRecord is crewAgentPublishRecord without the busy check: the copy waits for the reply instead.
func (api *StreamingAPI) crewRunOnlineRecord(ctx context.Context, claims *UserClaims, roots crewroot.Roots, tab string) (ProductConversationRecord, ProductAgentIdentity, error) {
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(claims.UserID))
	if err != nil {
		return ProductConversationRecord{}, ProductAgentIdentity{}, err
	}
	key := productConversationRegistryEntryKey(crewProfileID, crewAgentPublishKey(roots.ID, tab))
	record, ok := doc.Entries[key]
	identity := doc.Agents[key]
	if tab == macMainTab {
		if main, ok := readCrewMainIdentity(ctx, roots.Virtual); ok {
			identity.Name = "Main"
			identity.Purpose = firstNonEmptyTrimmed(main.Purpose, identity.Purpose)
		}
	}
	if !ok || canonicalCrewWorkspaceRoot(record.WorkspacePath) != roots.Virtual || record.ProfileID != crewProfileID || hiddenFromTabStrip(record.ConversationKey) {
		return record, identity, fmt.Errorf("choose one of your Crew's agents")
	}
	if tab != macMainTab && strings.TrimSpace(identity.Name) == "" {
		return record, identity, fmt.Errorf("give this agent a name first; ask it to set its name")
	}
	return record, identity, nil
}

// crewRunOnlineAutomations are what the agent picked to go online with it, by name: schedules (on a timer, with or
// without a watcher condition), webhooks and watchers that send their work to this agent (owner, 13 Nov).
type crewRunOnlineAutomations struct {
	Schedules []string // schedule IDs
	Webhooks  []string // webhook IDs
	Watchers  []string // watcher (condition caller) schedule IDs
}

func (a crewRunOnlineAutomations) count() int {
	return len(a.Schedules) + len(a.Webhooks) + len(a.Watchers)
}

func (api *StreamingAPI) crewRunOnlineAutomations(ctx context.Context, claims *UserClaims, roots crewroot.Roots, project, tab string, names []string) (crewRunOnlineAutomations, error) {
	var out crewRunOnlineAutomations
	if len(names) == 0 {
		return out, nil
	}
	schedules, err := readProjectSchedules(ctx, roots.Virtual)
	if err != nil {
		return out, err
	}
	hooks, err := readProjectWebhookDefinitions(ctx, roots.Virtual)
	if err != nil {
		return out, err
	}
	states, err := api.crewMoveDispatcher().loadState(ctx, claims.UserID)
	if err != nil {
		return out, err
	}
	var watchers []productScheduleJob
	for _, job := range conditionCallerJobs(claims.UserID, states) {
		if job.Profile.ID == crewProfileID && job.ProjectID == project {
			watchers = append(watchers, job)
		}
	}
	main, err := api.crewHandoffProjectTemplate(ctx, claims, project)
	if err != nil {
		return out, err
	}
	h := crewMoveHeader{AgentTab: tab, SourceID: project, MainKey: main.ConversationKey}
	for _, name := range names {
		matched, other := false, false
		for _, s := range schedules {
			if strings.EqualFold(s.Name, name) {
				if crewAgentScheduleMatches(s, h) {
					out.Schedules, matched = append(out.Schedules, s.ID), true
				} else {
					other = true
				}
			}
		}
		for _, hook := range hooks {
			if strings.EqualFold(hook.Name, name) {
				if hook.Action.Type == "message" && crewAgentTargetMatches(hook.Action.Tab, h) {
					out.Webhooks, matched = append(out.Webhooks, hook.ID), true
				} else {
					other = true
				}
			}
		}
		for _, job := range watchers {
			if strings.EqualFold(job.Schedule.Name, name) {
				if c := job.Schedule.Condition; c != nil && crewAgentTargetMatches(c.Target.ChatID, h) {
					out.Watchers, matched = append(out.Watchers, job.Schedule.ID), true
				} else {
					other = true
				}
			}
		}
		if !matched && other {
			return out, fmt.Errorf("%q sends its work to another agent or a workflow; it stays on this Mac", name)
		}
		if !matched {
			return out, fmt.Errorf("no schedule, webhook or watcher is named %q", name)
		}
	}
	return out, nil
}

// retargetCrewRunOnline points the picked automations at the online agent's tab, so its publish moves them with it and
// switches them off here. The returned restore puts them back when the publish fails.
func (api *StreamingAPI) retargetCrewRunOnline(ctx context.Context, owner string, roots crewroot.Roots, picked crewRunOnlineAutomations, to string) (func(), error) {
	var undo []func() error
	restore := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			if e := undo[i](); e != nil {
				log.Printf("[CREW_RUN_ONLINE] project=%s automations_restored=false reason=%q", roots.ID, e.Error())
			}
		}
	}
	if len(picked.Schedules) > 0 {
		all, err := readProjectSchedules(ctx, roots.Virtual)
		if err != nil {
			return restore, err
		}
		before := slices.Clone(all)
		for i := range all {
			if !slices.Contains(picked.Schedules, all[i].ID) {
				continue
			}
			var input scheduleExecutionInput
			if json.Unmarshal(all[i].Input, &input) == nil && input.Condition != nil {
				input.Condition.Target.ChatID = to
				raw, err := json.Marshal(input)
				if err != nil {
					return restore, err
				}
				all[i].Input = raw
			} else {
				all[i].Action.Tab = to
			}
		}
		if err = writeProjectSchedules(ctx, roots.Virtual, all); err != nil {
			return restore, err
		}
		undo = append(undo, func() error { return writeProjectSchedules(ctx, roots.Virtual, before) })
	}
	if len(picked.Webhooks) > 0 {
		hooks, err := readProjectWebhookDefinitions(ctx, roots.Virtual)
		if err != nil {
			return restore, err
		}
		before := slices.Clone(hooks)
		for i := range hooks {
			if slices.Contains(picked.Webhooks, hooks[i].ID) {
				hooks[i].Action.Tab = to
			}
		}
		if err = writeProjectWebhookDefinitions(ctx, roots.Virtual, hooks); err != nil {
			return restore, err
		}
		undo = append(undo, func() error { return writeProjectWebhookDefinitions(ctx, roots.Virtual, before) })
	}
	dispatcher := api.crewMoveDispatcher()
	for _, id := range picked.Watchers {
		key, old := projectScheduleStateKey(crewProfileID, roots.ID, id), ""
		if err := dispatcher.updateStateByKey(ctx, owner, key, func(st *productScheduleUserState) {
			if st.Watcher != nil && st.Watcher.Schedule.Condition != nil {
				old, st.Watcher.Schedule.Condition.Target.ChatID = st.Watcher.Schedule.Condition.Target.ChatID, to
			}
		}); err != nil {
			return restore, err
		}
		undo = append(undo, func() error {
			return dispatcher.updateStateByKey(ctx, owner, key, func(st *productScheduleUserState) {
				if st.Watcher != nil && st.Watcher.Schedule.Condition != nil {
					st.Watcher.Schedule.Condition.Target.ChatID = old
				}
			})
		})
	}
	return restore, nil
}

func (api *StreamingAPI) runCrewAgentOnline(ctx context.Context, claims *UserClaims, connection desktopConnection, roots crewroot.Roots, source, tab, name string, selection crewAgentPublishSelection, automations crewRunOnlineAutomations) (string, string, error) {
	deadline := time.Now().Add(crewRunOnlineWait)
	for api.crewAgentTurnRunning(source) {
		if time.Now().After(deadline) {
			return "", "", fmt.Errorf("its reply did not end within %s", crewRunOnlineWait)
		}
		time.Sleep(2 * time.Second)
	}
	ctx = context.WithValue(ctx, UserContextKey, claims)
	copyName := name + crewRunOnlineSuffix
	fork, err := api.forkCrewTab(ctx, crewAgentPublishKey(roots.ID, tab), copyName)
	if err != nil {
		return copyName, "", err
	}
	copyName = fork.Name
	restore, err := api.retargetCrewRunOnline(ctx, claims.UserID, roots, automations, fork.ChatID)
	if err == nil {
		err = api.publishCrewRunOnlineCopy(ctx, claims, connection, roots.ID, selection, fork.ChatID)
	}
	if err != nil {
		restore()
	}
	if err != nil {
		// A failed Run online leaves nothing behind: the copy's tab goes (owner, 13 Nov: four leftover tabs after retries).
		if e := api.removeCrewRunOnlineFork(ctx, crewAgentPublishKey(roots.ID, fork.ChatID)); e != nil {
			log.Printf("[CREW_RUN_ONLINE] project=%s tab=%s fork=%s remove=failed reason=%q", roots.ID, tab, fork.ChatID, e.Error())
			return copyName, "", fmt.Errorf("%s (the tab %q stayed on this Mac; you can delete it)", err.Error(), copyName)
		}
		log.Printf("[CREW_RUN_ONLINE] project=%s tab=%s fork=%s removed=true", roots.ID, tab, fork.ChatID)
		return copyName, "", err
	}
	if e := api.copyCrewRunOnlineTeamSettings(ctx, claims, roots.ID, tab, fork.ChatID); e != nil {
		log.Printf("[CREW_RUN_ONLINE] project=%s tab=%s team_settings=failed reason=%q", roots.ID, tab, e.Error())
		return copyName, " Its Team settings could not be copied; set them in Team > Agents.", nil
	}
	return copyName, "", nil
}

// The copy starts with the original's Team settings (Team Module Manager, 12 Nov): In team, When others ask and what
// teammates see of its chat. From then on the two are separate agents with their own settings.
func (api *StreamingAPI) copyCrewRunOnlineTeamSettings(ctx context.Context, claims *UserClaims, project, from, to string) error {
	team, err := liveCrewTeam(project)
	if err != nil || !team.On {
		return nil
	}
	if !team.agentInTeam(team.OwnerID, from) {
		_, err = api.crewAccessChatAction(ctx, project, "unshare", map[string]any{"agent": to})
		return err
	}
	rule := team.agentRule(team.OwnerID, from)
	if _, err = api.crewAccessChatAction(ctx, project, "share", map[string]any{"agent": to, "can_make_changes": rule.CanMakeChanges, "when_others_ask": rule.WhenOthersAsk}); err != nil {
		return err
	}
	if rule.ChatShare == "" {
		return nil
	}
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.Side != "local" {
		return fmt.Errorf("this Crew is not connected to its server")
	}
	c, err := desktopConnectionFor(claims.UserID, b.ConnectionID)
	if err != nil {
		return err
	}
	var out crewTeamAgentRule
	return crewTeamRemoteJSON(ctx, claims.UserID, c, http.MethodPost, "/api/crew-teams/"+url.PathEscape(b.PeerProjectID)+"/agent-rule", map[string]any{"agent_key": macSharedAgentChatKey(b.PeerProjectID, to), "chat_share": rule.ChatShare}, &out)
}

type crewForkedTab struct {
	ChatID string `json:"chat_id"`
	Name   string `json:"name"`
}

// forkCrewTab is the Fork chat action (crew_chat_fork.go) called from inside the server.
func (api *StreamingAPI) forkCrewTab(ctx context.Context, conversationKey, name string) (crewForkedTab, error) {
	body, _ := json.Marshal(map[string]string{"conversation_key": conversationKey, "name": name})
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)).WithContext(ctx)
	request = mux.SetURLVars(request, map[string]string{"id": crewProfileID})
	response := httptest.NewRecorder()
	api.handleForkCrewChat(response, request)
	var out crewForkedTab
	if response.Code >= 400 {
		return out, fmt.Errorf("%s", laptopActionErrorText(response.Body.String()))
	}
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || !crewMacTabID(out.ChatID) {
		return out, fmt.Errorf("the online agent's chat could not be made")
	}
	return out, nil
}

// removeCrewRunOnlineFork closes a copy's tab that never went online, through the Close action (preview, then confirm).
// A tab something else already points at stays for the person to decide.
func (api *StreamingAPI) removeCrewRunOnlineFork(ctx context.Context, conversationKey string) error {
	close := func(in AgentProfileConversationRequest) (*httptest.ResponseRecorder, error) {
		body, _ := json.Marshal(in)
		request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)).WithContext(ctx)
		request = mux.SetURLVars(request, map[string]string{"id": crewProfileID})
		response := httptest.NewRecorder()
		api.handleCloseAgentProfileSideChat(response, request)
		if response.Code >= 400 {
			return response, fmt.Errorf("%s", laptopActionErrorText(response.Body.String()))
		}
		return response, nil
	}
	response, err := close(AgentProfileConversationRequest{ConversationKey: conversationKey, PreviewRemoval: true})
	if err != nil {
		return err
	}
	var preview crewAgentRemovalPreview
	if err = json.Unmarshal(response.Body.Bytes(), &preview); err != nil || preview.Confirmation == "" {
		return fmt.Errorf("the removal preview was unreadable")
	}
	if len(preview.Dependencies) > 0 {
		return fmt.Errorf("other work points at this tab")
	}
	_, err = close(AgentProfileConversationRequest{ConversationKey: conversationKey, RemovalConfirmation: preview.Confirmation})
	return err
}

// publishCrewRunOnlineCopy sends the idle fork to the server with the existing publish (preview, then confirm).
func (api *StreamingAPI) publishCrewRunOnlineCopy(ctx context.Context, claims *UserClaims, connection desktopConnection, project string, selection crewAgentPublishSelection, tab string) error {
	selection.Tab = tab
	step := func(action, transfer string) (crewSyncReply, error) {
		request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
		response := httptest.NewRecorder()
		api.runDesktopCrewAgentPublish(response, request, connection, project, action, transfer, &selection)
		var out crewSyncReply
		if response.Code >= 400 {
			return out, fmt.Errorf("%s", laptopActionErrorText(response.Body.String()))
		}
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			return out, err
		}
		return out, nil
	}
	preview, err := step("publish_preview", "")
	if err != nil {
		return err
	}
	if preview.Publish == nil || !preview.Publish.Ready || preview.Publish.TransferID == "" {
		message := "the server is not ready for this agent"
		if preview.Publish != nil && preview.Publish.Message != "" {
			message = preview.Publish.Message
		}
		return fmt.Errorf("%s", message)
	}
	selection.AccountID = preview.Publish.Selection.AccountID
	_, err = step("publish_agent", preview.Publish.TransferID)
	return err
}

// crewRunOnlineWebhookNote: a webhook gets a new address and signing secret on the server, so its sender needs updating.
func crewRunOnlineWebhookNote(a crewRunOnlineAutomations) string {
	if len(a.Webhooks) == 0 {
		return ""
	}
	return " Its webhooks have new addresses and secrets on the server: tell the person to update the apps that call them (Automations > Webhooks on the website)."
}
