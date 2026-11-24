package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The browser and Builder tools use the same caller-authorized directory.
func (api *StreamingAPI) handleAgentDirectory(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		externalError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to see the agents you can reach.")
		return
	}
	ctx := r.Context()
	if project := r.URL.Query().Get("crew"); IsLocalMode() && project != "" {
		root := crewProjectRootByID(project)
		if root == "" || !crewProjectOwnedByCaller(claims.UserID, root) {
			externalError(w, 404, "crew_unavailable", "This project is unavailable.")
			return
		}
		ctx = withDirectoryThisCrew(ctx, root)
	}
	if target := r.URL.Query().Get("agent_status"); target != "" {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		out, err := api.readAgentStatus(ctx, claims, target, false, limit)
		if err != nil {
			agentStatusHTTPError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		externalJSON(w, out)
		return
	}
	out, err := listAgentDirectory(ctx, claims, api)
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "directory_unavailable", "Could not load your agents. Try again.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	filterAgentDirectory(out, r.URL.Query().Get("query"))
	// "Message other agents" (owner, 3 Nov): your own agents, agents others published, and Crews you own or that are
	// shared with you (Everyone or you by name) with their shared agents. Not every Goal or Flow you can open.
	if r.URL.Query().Get("view") == "message" {
		if list, ok := out["agents"].([]map[string]interface{}); ok {
			kept := list[:0]
			for _, row := range list {
				if kind, _ := row["kind"].(string); kind != "goal" && kind != "relay" {
					kept = append(kept, row)
				}
			}
			out["agents"] = kept
		}
	}
	externalJSON(w, out)
}

// listAgentDirectory is the one list of agents a person can use (PLAT-861-297, owner 2026-10-20: "all should be a
// group of agents users can easily.. once shared"): every Crew and Relay they own or that is shared with them,
// and their agents connected through AgentBridge (local Claude Code and others). From inside a Crew, only that Crew, its Team and the Crews linked to it show (crew_reach.go).
// Rows come only from the existing access paths, and the list is built only when asked, never at message start (except a
// Crew agent's new network message on the Mac, checked against its own list).
// directoryThisCrewKey carries the folder of the Crew whose agent is asking, so the list can say which Crew is "this" one.
type directoryThisCrewKey struct{}

func withDirectoryThisCrew(ctx context.Context, crewPath string) context.Context {
	if strings.TrimSpace(crewPath) == "" {
		return ctx
	}
	return context.WithValue(ctx, directoryThisCrewKey{}, crewPath)
}

// directoryRelationRank orders the Crew rows: the Crew the agent works in, then the person's own, their Teams', then the rest.
var directoryRelationRank = map[string]int{"this_crew": 0, "yours": 1, "team": 2, "shared": 3}

func listAgentDirectory(ctx context.Context, claims *UserClaims, apis ...*StreamingAPI) (map[string]interface{}, error) {
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return map[string]interface{}{"agents": []map[string]interface{}{}}, nil
	}
	ctx, finishReads := startDirectoryRegistryReads(ctx)
	defer finishReads()
	agents := []map[string]interface{}{}
	// Reuse an existing canonical registration without writing during discovery.
	// A historical alias for this binding must not become a second row.
	hosted := map[hostedAgentKey]namedMessageAgent{}
	named := map[string]namedMessageAgent{}
	if snapshot, err := readAgentMessageSnapshot(ctx); err == nil {
		for _, a := range uniqueHostedAgents(snapshot.Agents) {
			if a.UserID == claims.UserID && a.Hosted != nil {
				hosted[hostedAgentKey{a.UserID, *a.Hosted}] = a
			}
		}
		for _, a := range snapshot.Agents {
			if a.UserID == claims.UserID {
				named[a.ID] = a
			}
		}
	}
	here, _ := ctx.Value(directoryThisCrewKey{}).(string)
	thisCrew := ""
	// The same rows on the website and over MCP: the person's live access decides, not connection scopes.
	crews, err := listAccessibleWorkspaceProjects(ctx, claims, "")
	if err != nil {
		return nil, err
	}
	// Asked from inside a Crew, only this Crew, its Team and the Crews linked to it are listed, with their agents
	// (owner, 7 and 19 Nov). Over MCP from outside a Crew there is no "here" and nothing changes; the person's
	// AgentWorks main agent reaches all their Crews, decided by its role on the server.
	reach, _ := crewReachFrom(ctx, here)
	for _, crew := range crews {
		id, _ := crew["id"].(string)
		profileID, _ := crew["profile_id"].(string)
		crewPath, _ := crew["workspace_path"].(string)
		name, _ := crew["name"].(string)
		if identity, ok := crew["identity"].(accessibleProjectIdentity); ok && strings.TrimSpace(identity.Name) != "" {
			name = identity.Name
		}
		owner := claims.UserID
		if crewOwner, ok := crewProjectOwnerID(crewPath); ok {
			owner = crewOwner
		}
		target := triggerTarget{Kind: triggerCallerCrew, Path: crewPath, Label: name, CrewID: id, CrewProfile: profileID, CrewOwner: owner}
		functions, _ := readCrewFunctions(ctx, target)
		kind := "crew"

		row := map[string]interface{}{
			"kind": kind, "type": "Crew", "presence": "Runs when called", "delivery_mode": "on_request", "id": "workspace:" + profileID + ":" + id, "crew_id": id, "name": name, "owner": crew["owner"],
			"your_access": crew["access"], "description": crew["description"], "functions": agentDirectoryFunctions(functions),
			"reach": crewDirectoryReach(false),
		}
		if role, _ := crew["role"].(string); role != "" {
			row["role"] = role
		}
		owned := crewProjectOwnedByCallerStrict(claims.UserID, crewPath)
		team, teamErr := readCrewTeam(id)
		inTeam := !IsLocalMode() && teamErr == nil && crewTeamAgentsVisible(team, claims.UserID) && team.OwnerID == owner
		if reach != nil && (owned || inTeam) && !reach.crew(crewPath, id) {
			continue
		}
		// How this Crew relates to the person (backend decides): the Crew the asking agent works in, one they own, one whose
		// Team they are in, or one shared with them (owner, 7 Nov).
		relation := "shared"
		if inTeam {
			relation = "team"
		}
		if owned {
			relation = "yours"
		}
		if here != "" && canonicalCrewWorkspaceRoot(crewPath) == canonicalCrewWorkspaceRoot(here) {
			relation, thisCrew = "this_crew", id
		}
		row["relation"] = relation
		// Team reach filters other people, never the owner's own ordinary tabs.
		// Otherwise an unregistered/Only-me tab disappears before lazy binding.
		if inTeam && !owned {
			chats := []map[string]interface{}{}
			for _, agent := range crewTeamReachableIn(ctx, team, claims.UserID) {
				chats = append(chats, map[string]interface{}{"chat_id": agent.ID, "name": agent.Name, "target": agent.Address, "owner_name": agent.Person, "place": agent.Place, "can_write": false})
			}
			row["chats"] = chats
			agents = append(agents, row)
			continue
		}
		if owned {
			tabs, err := crewOwnerChatRecords(ctx, claims.UserID, profileID, id, crewPath)
			if err != nil {
				return nil, err
			}
			chats := []map[string]interface{}{}
			hostedListed, hostedHidden := 0, 0
			for _, tab := range tabs {
				if tab.OwnerID == claims.UserID && profileID == crewProfileID && !hiddenFromTabStrip(tab.Record.ConversationKey) && !crewFunctionTabKey(tab.Record.ConversationKey) {
					record := tab.Record
					if hostedDirectoryTab(tab) {
						hostedListed++
						address := bridgeChatAddress(tab.OwnerID, profileID, id, record.ConversationKey)
						registered := hosted[hostedAgentKey{tab.OwnerID, hostedBindingForRecord(record)}]
						if registered.ID != "" {
							address = "agent:" + shareOwnerLabel(registered.UserID) + "/" + registered.Name
						}
						online := hostedChatViewed(tab.OwnerID, hostedBindingForRecord(record))
						presence, mode := "Runs when called", "on_request"
						if online {
							presence, mode = "Wakes now", "live"
						}
						agents = append(agents, map[string]interface{}{"kind": "connected_agent", "id": address, "target": address, "agent_id": registered.ID, "owner_id": tab.OwnerID, "chat_id": record.ConversationKey, "crew_id": record.ResourceID, "name": crewDirectoryChatName(tab), "purpose": tab.Identity.Purpose, "examples": tab.Identity.Examples, "yours": true, "online": online, "presence": presence, "delivery_mode": mode, "reach": "Reply in the returned inbox_id"})
					} else {
						hostedHidden++
					}
				}
				chats = append(chats, map[string]interface{}{"target": bridgeChatAddress(tab.OwnerID, profileID, id, tab.Record.ConversationKey), "chat_id": tab.Record.ConversationKey, "name": crewDirectoryChatName(tab), "purpose": tab.Identity.Purpose, "examples": tab.Identity.Examples, "owner_name": shareOwnerLabel(tab.OwnerID), "can_write": tab.OwnerID == claims.UserID, "is_main": tab.Record.IsMainChat, "shared_by": crewPersonShortName(tab.OwnerID), "yours": tab.OwnerID == claims.UserID, "function_agent": crewFunctionTabKey(tab.Record.ConversationKey)})
			}
			log.Printf("[AGENTBRIDGE] event=directory owner=%s project=%s hosted_listed=%d hosted_hidden=%d", claims.UserID, id, hostedListed, hostedHidden)
			row["chats"] = chats
		} else {
			selected, err := crewShareAgents(ctx, claims.UserID, crewPath, false)
			if err != nil {
				return nil, err
			}
			row["chats"] = selected
		}
		row["reach"] = crewDirectoryReach(true)
		agents = append(agents, row)
		if !owned {
			for _, agent := range row["chats"].([]sharedCrewAgent) {
				agents = append(agents, map[string]any{"kind": "crew_agent", "type": "Project agent", "id": "workspace:" + profileID + ":" + id + "/agent/" + agent.ChatID, "target": "", "crew_id": id, "chat_id": agent.ChatID, "name": agent.Name, "crew_name": name, "relation": relation, "shared_by": crewPersonShortName(agent.tab.OwnerID), "purpose": agent.Purpose, "examples": agent.Examples, "presence": "Runs when called", "your_access": "run", "reach": "Use the product Ask entry point, or discover this agent's AgentBridge address for a peer message."})
			}
		}
	}
	// Agents shared from Crews on their owners' Macs, for the people in each agent's audience (PLAT-856-509).
	if !IsLocalMode() {
		for _, row := range macSharedAgentRows(claims.UserID, false) {
			project, _ := row["crew_id"].(string)
			if t, err := readCrewTeam(project); err == nil && t.On && t.member(claims.UserID) {
				continue
			}
			agents = append(agents, row)
		}
	}
	agents = append(agents, crewTeamBridgeDirectoryItems(ctx, claims.UserID)...)
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return nil, err
	}
	for _, workflow := range filterWorkflowManifestsForUser(claims, discovered) {
		manifest := workflow.Manifest
		// Old workflow projects are no longer listed as Goals (owner, 19 Nov); Relays stay.
		if !manifestIsRelay(manifest) {
			continue
		}
		access := "run"
		if workflow.MyAccess == WorkflowAccessOwner {
			access = "owner"
		} else if workflow.MyAccess == WorkflowAccessWrite {
			access = "editor"
		}
		row := map[string]interface{}{
			"kind": "relay", "type": "Relay", "presence": "Runs when called", "delivery_mode": "on_request", "id": "workflow:" + manifest.ID, "workflow_id": manifest.ID, "name": firstNonEmptyTrimmed(manifest.Label, workflow.WorkspacePath),
			"your_access": access, "description": strings.TrimSpace(manifest.Description), "functions": agentDirectoryFunctions(workflowFunctions(manifest)),
			"reach": "functions action=call(target=<id>, function=<name>)",
		}
		agents = append(agents, row)
	}
	connected, err := listNamedMessageAgents(ctx, claims.UserID)
	if err == nil {
		list, _ := connected["agents"].([]map[string]interface{})
		for _, item := range list {
			row := map[string]interface{}{"kind": "connected_agent", "reach": "messages action=send(target=<id>)"}
			for key, value := range item {
				row[key] = value
			}
			row["id"] = item["target"]
			agents = append(agents, row)
		}
	}
	networkRows, networkError := desktopAgentNetworkRows(ctx, claims.UserID)
	agents = append(agents, networkRows...)
	agents = uniqueAgentDirectoryItems(agents)
	projectAgentLabels(agents)
	// Hiding is the caller's preference, not the agent owner's sharing policy.
	registryPath := productConversationRegistryPath(claims.UserID)
	registryMu := productConversationRegistryMutex(registryPath)
	registryMu.Lock()
	preferences, preferenceErr := defaultProductConversationRegistryStore().loadDocument(ctx, registryPath)
	registryMu.Unlock()
	if preferenceErr == nil {
		annotateDirectoryHiddenAgents(agents, preferences, desktopMachineID())
		annotateDirectoryAgentGroups(agents, preferences)
	}
	appCrews := map[string]string{}
	if len(apis) > 0 && apis[0] != nil {
		apis[0].annotateAgentRuntimeInfo(ctx, claims.UserID, agents)
		if IsLocalMode() {
			if source, err := apis[0].agentDirectorySource(ctx, claims.UserID); err == nil {
				agents = mergeOwnAppChatRows(agents, source.Document.Entries, desktopMachineID())
				projectAgentLabels(agents)
				for _, record := range source.Document.Entries {
					if record.ProfileID == crewProfileID && record.ConversationID != "" {
						appCrews[desktopAppAgentName(desktopMachineID(), record.ConversationID)] = record.ResourceID
					}
				}
			}
		}
	}
	if reach != nil {
		terminals, _ := terminalCrewMemberships()
		kept := agents[:0]
		for _, row := range agents {
			if reach.keepsRow(row, named, terminals, appCrews) {
				kept = append(kept, row)
			}
		}
		agents = kept
	}
	sort.SliceStable(agents, func(i, j int) bool {
		rank := func(row map[string]interface{}) int {
			relation, _ := row["relation"].(string)
			if r, ok := directoryRelationRank[relation]; ok {
				return r
			}
			return len(directoryRelationRank)
		}
		return rank(agents[i]) < rank(agents[j])
	})
	out := map[string]interface{}{
		"signed_in_as": firstNonEmptyTrimmed(claims.Email, claims.Username, claims.UserID),
		"agents":       agents,
		"note":         "Every agent you own or that is shared with you. hidden=true means hidden by you and used less often, not disabled or private. group is your own left-menu group holding the agent (agents in one group usually work together, so ask them first) and forked_from the agent it was forked from; neither is access. Prefer visible agents unless a hidden agent is named or specifically relevant; schedules and messages remain unchanged. Show name, type, cli, model_id, reasoning_effort, presence and last_seen_at when supplied; internal project names are projected only for their authorized audience; include last_delivered_at when supplied. Builder/Run describes access, not connection or delivery. Projects run when called and have no laptop heartbeat. Each project row says how it relates to you in relation: this_crew (the project you are working in), yours (you own it and linked it to this project), team (you are in its Team) or shared (shared with you); chats say who shared them (shared_by) and function_agent marks a function's own agent, which only its owner talks to. Last seen is connection activity, not proof that the model read a message. Missing timestamps are unknown, never zero activity. Use the id; a name works only when unique.",
	}
	if networkError != "" {
		out["network_error"] = networkError
	}
	if thisCrew != "" {
		out["this_crew"] = thisCrew
	}
	return out, nil
}

// crewDirectoryReach tells an agent how to use a Crew: its features are functions it calls, never the chat of a function's
// own agent (owner, 7 Nov). chats is whether the row also lists agents it may message.
func crewDirectoryReach(chats bool) string {
	call := "To use its features call a function: functions action=call(project=<crew_id>, function=<name>, input=<its asks_for>), then functions action=status(call_id) until it settles. A function runs in its own agent, which only the owner talks to."
	if chats {
		return "Message an agent through its exact AgentBridge address; product IDs are only for product Ask and functions. " + call
	}
	return call
}

// agentDirectoryFunctions are a Crew's functions as an agent reads them: the name, what it does and what it asks for.
func agentDirectoryFunctions(functions []crewFunction) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(functions))
	for _, fn := range functions {
		row := map[string]interface{}{"name": fn.Name, "description": fn.Description}
		if asks := crewFunctionAsksFor(fn.InputSchema); len(asks) > 0 {
			row["asks_for"] = asks
		}
		out = append(out, row)
	}
	return out
}

// crewFunctionAsksFor lists the inputs a function takes, in the order of its schema's required list first.
func crewFunctionAsksFor(schema map[string]interface{}) []map[string]interface{} {
	props, _ := schema["properties"].(map[string]interface{})
	required := map[string]bool{}
	switch list := schema["required"].(type) {
	case []interface{}:
		for _, name := range list {
			if text, ok := name.(string); ok {
				required[text] = true
			}
		}
	case []string:
		for _, name := range list {
			required[name] = true
		}
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if required[names[i]] != required[names[j]] {
			return required[names[i]]
		}
		return names[i] < names[j]
	})
	out := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		field, _ := props[name].(map[string]interface{})
		kind, _ := field["type"].(string)
		out = append(out, map[string]interface{}{"name": name, "type": kind, "required": required[name]})
	}
	return out
}

// Filter only the already-authorized projection; searching never broadens access.
func filterAgentDirectory(directory map[string]any, query string) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return
	}
	for _, key := range []string{"agents", "chats_inside_agentworks"} {
		rows, ok := directory[key].([]map[string]any)
		if !ok {
			continue
		}
		filtered := make([]map[string]any, 0)
		for _, row := range rows {
			aliasMatch := false
			for _, address := range agentDirectoryAddresses(row) {
				if strings.Contains(strings.ToLower(address), query) {
					aliasMatch = true
					break
				}
			}
			if aliasMatch {
				filtered = append(filtered, row)
				continue
			}
			for _, field := range []string{"agent_name", "name", "label", "purpose", "description", "display", "id", "target", "team_name", "crew_name", "owner"} {
				value, _ := row[field].(string)
				if field == "purpose" {
					if row["examples"] != nil {
						raw, _ := json.Marshal(row["examples"])
						value += " " + string(raw)
					}
				}
				if strings.Contains(strings.ToLower(value), query) {
					filtered = append(filtered, row)
					break
				}
			}
		}
		directory[key] = filtered
	}
}

// agentDirectoryHowToCall is the one line on how to use what the list holds, naming only the kinds it has (owner, 7 Nov: Goals and
// Relays are going away, so the line no longer teaches them to people who have none).
func agentDirectoryHowToCall(rows any) string {
	kinds := map[string]bool{}
	if list, ok := rows.([]map[string]interface{}); ok {
		for _, row := range list {
			kind, _ := row["kind"].(string)
			kinds[kind] = true
		}
	}
	parts := []string{}
	if kinds["crew"] {
		parts = append(parts, "Project: functions action=call(project=<crew_id>, function=<name>, input=<its asks_for>), then functions action=status(call_id) until it settles; or workspace action=ask. A function's own agent is not a chat to message")
	}
	if kinds["goal"] {
		parts = append(parts, "Goal: functions action=call with workflow_id and function")
	}
	if kinds["relay"] {
		parts = append(parts, "Relay: relay action=run with workflow_id")
	}
	if kinds["connected_agent"] {
		parts = append(parts, "Connected agent: messages action=send target=agent:<name>")
	}
	return strings.Join(parts, ". ") + "."
}

// crewDirectoryChatName is a chat's name as the header shows it: a person's Main is "Main", never its saved title
// "<person> · Builder".
func crewDirectoryChatName(tab crewOwnerChat) string {
	if tab.Record.IsMainChat {
		return "Main"
	}
	return firstNonEmptyTrimmed(tab.Identity.Name, tab.Record.Title, "Main")
}

// Only the viewing person's saved preferences are supplied here. Do not copy
// another owner's pin setting onto shared or remote agents.
func annotateDirectoryHiddenAgents(rows []map[string]interface{}, preferences productConversationRegistryDocument, device string) {
	hiddenChats := map[string]bool{}
	hiddenApps := map[string]bool{}
	for key, identity := range preferences.Agents {
		if !identity.Unpinned {
			continue
		}
		hiddenChats[strings.TrimPrefix(key, crewProfileID+"/")] = true
		if record, ok := preferences.Entries[key]; ok && device != "" && record.ConversationID != "" {
			hiddenApps[desktopAppAgentName(device, record.ConversationID)] = true
		}
	}
	mark := func(row map[string]interface{}) {
		row["hidden"] = true
		row["usage_note"] = "Hidden by you — used less often. Still available for messages and scheduled work."
	}
	for _, row := range rows {
		chat, _ := row["chat_id"].(string)
		name, _ := row["agent_name"].(string)
		yours, _ := row["yours"].(bool)
		if hiddenChats[chat] || (yours && hiddenApps[name]) {
			mark(row)
		}
		if chats, ok := row["chats"].([]map[string]interface{}); ok {
			for _, child := range chats {
				chat, _ := child["chat_id"].(string)
				if hiddenChats[chat] {
					mark(child)
				}
			}
		}
	}
}

// annotateDirectoryAgentGroups adds the caller's own left-menu group and, for a fork, the agent it came from (#3288,
// owner 19 Nov: agents are told about groups and forks). It only decorates rows already admitted to the caller.
func annotateDirectoryAgentGroups(rows []map[string]interface{}, preferences productConversationRegistryDocument) {
	groups := map[string]string{}
	for _, members := range preferences.AgentGroups {
		for key, group := range members {
			groups[key] = group
		}
	}
	parentName := func(parent string) string {
		if identity, ok := preferences.Agents[productConversationRegistryEntryKey(crewProfileID, parent)]; ok && strings.TrimSpace(identity.Name) != "" {
			return strings.TrimSpace(identity.Name)
		}
		if !strings.Contains(parent, codeChatSideMarker) {
			return "Main"
		}
		return parent[strings.LastIndex(parent, codeChatSideMarker)+len(codeChatSideMarker):]
	}
	annotate := func(row map[string]interface{}) {
		chat, _ := row["chat_id"].(string)
		if chat == "" {
			return
		}
		if group := groups[chat]; group != "" {
			row["group"] = group
		}
		if identity, ok := preferences.Agents[productConversationRegistryEntryKey(crewProfileID, chat)]; ok && identity.ForkedFrom != "" {
			row["forked_from"] = parentName(identity.ForkedFrom)
		}
	}
	for _, row := range rows {
		annotate(row)
		if chats, ok := row["chats"].([]map[string]interface{}); ok {
			for _, child := range chats {
				annotate(child)
			}
		}
	}
}

var teamAgentReachLogged sync.Map

// logTeamAgentReach logs how many Team agents a Crew chat's list offered, once per change (IDs and counts only).
func logTeamAgentReach(caller, workspace string, count int) {
	if previous, ok := teamAgentReachLogged.Swap(caller+"/"+workspace, count); ok && previous == count {
		return
	}
	log.Printf("[TEAM_AGENT_REACH] caller=%s workspace=%s event=discover team_agents=%d", caller, workspace, count)
}
