package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Run online (owner, 12 Nov, PLAT-856-564): a copy, not a move. The agent's chat is forked into a new tab
// "<name> · online", the message schedules the agent picked are pointed at that tab, and the new tab is published
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
		return "", fmt.Errorf("turn on Use from phone and web first; the online copy runs on that server")
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
		return "", fmt.Errorf("this agent is already the online copy; make the copy from the original on this Mac")
	}
	record, identity, err := api.crewRunOnlineRecord(ctx, claims, roots, tab)
	if err != nil {
		return "", err
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
	var scheduleNames []string
	if raw, ok := args["schedules"].([]any); ok {
		for _, item := range raw {
			if name, ok := item.(string); ok && strings.TrimSpace(name) != "" {
				scheduleNames = append(scheduleNames, strings.TrimSpace(name))
			}
		}
	}
	schedules, err := api.crewRunOnlineSchedules(ctx, claims, roots, project, tab, scheduleNames)
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
		return "", fmt.Errorf("an online copy of this agent is already being made")
	}
	name := firstNonEmptyTrimmed(identity.Name, "Main")
	go func() {
		defer crewRunOnlineBusy.Delete(key)
		background := context.WithoutCancel(ctx)
		started := time.Now()
		copyName, err := api.runCrewAgentOnline(background, claims, connection, roots, record.SessionID, tab, name, selection, schedules)
		result := "copied"
		if err != nil {
			result = "failed"
		}
		log.Printf("[CREW_RUN_ONLINE] owner=%s project=%s tab=%s result=%s schedules=%d paths=%d elapsed_ms=%d", claims.UserID, project, tab, result, len(schedules), len(selection.Paths), time.Since(started).Milliseconds())
		message := fmt.Sprintf("[Run online] %q is now running on the server as its own agent. It has this chat's history up to now, your memory and skills, %d chosen path(s) and %d schedule(s); this original stays on this Mac. Tell the person in one or two lines: open %q on the website to try it, and the two have separate memory and files from now on.", name, len(selection.Paths), len(schedules), copyName)
		if err != nil {
			message = fmt.Sprintf("[Run online] The online copy of %q did not finish: %s. Tell the person in one line what happened and what they can do.", name, err.Error())
		}
		if !api.executeSyntheticTurn(record.SessionID, message) {
			log.Printf("[CREW_RUN_ONLINE] project=%s tab=%s report=undelivered", project, tab)
		}
	}()
	raw, _ := json.Marshal(map[string]any{"status": "starting", "agent": name, "online_name": name + crewRunOnlineSuffix, "paths": selection.Paths, "schedules": scheduleNames, "next": "The copy starts when this reply ends; a message arrives in this chat when it runs online. The original stays on this Mac."})
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

// crewRunOnlineSchedules are the IDs of the named message schedules that send work to this agent.
func (api *StreamingAPI) crewRunOnlineSchedules(ctx context.Context, claims *UserClaims, roots crewroot.Roots, project, tab string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	all, err := readProjectSchedules(ctx, roots.Virtual)
	if err != nil {
		return nil, err
	}
	main, err := api.crewHandoffProjectTemplate(ctx, claims, project)
	if err != nil {
		return nil, err
	}
	h := crewMoveHeader{AgentTab: tab, SourceID: project, MainKey: main.ConversationKey}
	var ids []string
	for _, name := range names {
		at := slices.IndexFunc(all, func(s projectactions.Schedule) bool { return strings.EqualFold(s.Name, name) })
		if at < 0 {
			return nil, fmt.Errorf("no schedule is named %q", name)
		}
		if all[at].Action.Type != "message" || !crewAgentTargetMatches(all[at].Action.Tab, h) {
			return nil, fmt.Errorf("%q does not send its work to this agent; it stays on this Mac", name)
		}
		ids = append(ids, all[at].ID)
	}
	return ids, nil
}

func (api *StreamingAPI) runCrewAgentOnline(ctx context.Context, claims *UserClaims, connection desktopConnection, roots crewroot.Roots, source, tab, name string, selection crewAgentPublishSelection, schedules []string) (string, error) {
	deadline := time.Now().Add(crewRunOnlineWait)
	for api.crewAgentTurnRunning(source) {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("its reply did not end within %s", crewRunOnlineWait)
		}
		time.Sleep(2 * time.Second)
	}
	ctx = context.WithValue(ctx, UserContextKey, claims)
	copyName := name + crewRunOnlineSuffix
	fork, err := api.forkCrewTab(ctx, crewAgentPublishKey(roots.ID, tab), copyName)
	if err != nil {
		return copyName, err
	}
	copyName = fork.Name
	// Point the chosen schedules at the copy: the publish moves them with it and switches them off here.
	var before []projectactions.Schedule
	if len(schedules) > 0 {
		all, e := readProjectSchedules(ctx, roots.Virtual)
		if e != nil {
			return copyName, e
		}
		before = slices.Clone(all)
		for i := range all {
			if slices.Contains(schedules, all[i].ID) {
				all[i].Action.Tab = fork.ChatID
			}
		}
		if e = writeProjectSchedules(ctx, roots.Virtual, all); e != nil {
			return copyName, e
		}
	}
	err = api.publishCrewRunOnlineCopy(ctx, claims, connection, roots.ID, selection, fork.ChatID)
	if err != nil && before != nil {
		if e := writeProjectSchedules(ctx, roots.Virtual, before); e != nil {
			log.Printf("[CREW_RUN_ONLINE] project=%s tab=%s schedules_restored=false reason=%q", roots.ID, tab, e.Error())
		}
	}
	if err != nil {
		return copyName, fmt.Errorf("%s (the tab %q stayed on this Mac; you can delete it)", err.Error(), copyName)
	}
	return copyName, nil
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
		return out, fmt.Errorf("the copy's chat could not be made")
	}
	return out, nil
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
