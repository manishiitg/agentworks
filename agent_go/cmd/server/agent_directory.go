package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
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
			externalError(w, 404, "crew_unavailable", "This Crew is unavailable.")
			return
		}
		ctx = withDirectoryThisCrew(ctx, root)
	}
	out, err := listAgentDirectory(ctx, claims)
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
// group of agents users can easily.. once shared"): every Crew, Goal and Relay they own or that is shared with them,
// and their agents connected through AgentBridge (local Claude Code and others). From inside a Crew, the person's other Crews show only when linked to it.
// Rows come only from the existing access paths, and the list is built only when asked, never at message start.
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

func listAgentDirectory(ctx context.Context, claims *UserClaims) (map[string]interface{}, error) {
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return map[string]interface{}{"agents": []map[string]interface{}{}}, nil
	}
	agents := []map[string]interface{}{}
	here, _ := ctx.Value(directoryThisCrewKey{}).(string)
	thisCrew := ""
	// The same rows on the website and over MCP: the person's live access decides, not connection scopes.
	crews, err := listAccessibleWorkspaceProjects(ctx, claims, "")
	if err != nil {
		return nil, err
	}
	// Asked from inside a Crew, the person's other Crews are listed only when linked to this one (owner, 7 Nov: not all of
	// a person's Crews talk to each other). Over MCP from outside a Crew there is no "here" and every owned Crew shows.
	// The person's AgentWorks main agent reaches all their Crews (owner, 7 Nov: "can access other agents.. thats it");
	// the server decides by its role.
	linked := map[string]bool{}
	onlyLinked := here != "" && !isAgentWorksCrew(ctx, here)
	if onlyLinked {
		refs, _ := readWorkWorkflowReferences(ctx, here)
		for _, ref := range refs {
			linked[canonicalCrewWorkspaceRoot(ref)] = true
		}
	}
	for _, crew := range crews {
		id, _ := crew["id"].(string)
		profileID, _ := crew["profile_id"].(string)
		crewPath, _ := crew["workspace_path"].(string)
		if onlyLinked && crewProjectOwnedByCaller(claims.UserID, crewPath) {
			root := canonicalCrewWorkspaceRoot(crewPath)
			if root != canonicalCrewWorkspaceRoot(here) && !linked[root] {
				continue
			}
		}
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
		owned := crewProjectOwnedByCaller(claims.UserID, crewPath)
		// How this Crew relates to the person (backend decides): the Crew the asking agent works in, one they own, one whose
		// Team they are in, or one shared with them (owner, 7 Nov).
		relation := "shared"
		if t, err := readCrewTeam(id); err == nil && t.On && t.member(claims.UserID) {
			relation = "team"
		}
		if owned {
			relation = "yours"
		}
		if here != "" && canonicalCrewWorkspaceRoot(crewPath) == canonicalCrewWorkspaceRoot(here) {
			relation, thisCrew = "this_crew", id
		}
		row["relation"] = relation
		if owned {
			tabs, err := crewOwnerChatRecords(ctx, claims.UserID, profileID, id, crewPath)
			if err != nil {
				return nil, err
			}
			chats := []map[string]interface{}{}
			for _, tab := range tabs {
				chats = append(chats, map[string]interface{}{"chat_id": tab.Record.ConversationKey, "name": firstNonEmptyTrimmed(tab.Identity.Name, tab.Record.Title, "Builder"), "purpose": tab.Identity.Purpose, "owner_name": crewOwnerDisplayName(tab.OwnerID), "can_write": tab.OwnerID == claims.UserID, "is_main": tab.Record.IsMainChat, "shared_by": crewPersonShortName(tab.OwnerID), "yours": tab.OwnerID == claims.UserID, "function_agent": crewFunctionTabKey(tab.Record.ConversationKey)})
			}
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
				agents = append(agents, map[string]any{"kind": "crew_agent", "type": "Crew agent", "id": "workspace:" + profileID + ":" + id + "/agent/" + agent.ChatID, "target": "workspace:" + profileID + ":" + id, "crew_id": id, "chat_id": agent.ChatID, "name": agent.Name, "crew_name": name, "relation": relation, "shared_by": crewPersonShortName(agent.tab.OwnerID), "purpose": agent.Purpose, "presence": "Runs when called", "your_access": "run", "reach": "messages action=send(target=<target>, chat_id=<chat_id>); same existing tab on the website and MCP"})
			}
		}
	}
	// Agents shared from Crews on their owners' Macs, for the people in each agent's audience (PLAT-856-509).
	if !IsLocalMode() {
		agents = append(agents, macSharedAgentRows(claims.UserID, false)...)
	}
	// On this Mac: the Team members' own agents of each Crew whose Team is on, from its online copy (owner, 5 Nov).
	if IsLocalMode() {
		agents = append(agents, teamMemberAgentRows(ctx, claims.UserID, crews)...)
	}
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return nil, err
	}
	for _, workflow := range filterWorkflowManifestsForUser(claims, discovered) {
		manifest := workflow.Manifest
		if manifest == nil {
			continue
		}
		kind := "goal"
		if manifestIsRelay(manifest) {
			kind = "relay"
		}
		access := "run"
		if workflow.MyAccess == WorkflowAccessOwner {
			access = "owner"
		} else if workflow.MyAccess == WorkflowAccessWrite {
			access = "editor"
		}
		row := map[string]interface{}{
			"kind": kind, "type": map[string]string{"goal": "Goal", "relay": "Relay"}[kind], "presence": "Runs when called", "delivery_mode": "on_request", "id": "workflow:" + manifest.ID, "workflow_id": manifest.ID, "name": firstNonEmptyTrimmed(manifest.Label, workflow.WorkspacePath),
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
	networkRows, networkError := crewNetworkAgentRows(ctx, claims.UserID, here)
	agents = append(agents, networkRows...)
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
		"note":         "Every agent you own or that is shared with you. Show name, type, provider, presence and last_seen_at; include last_delivered_at when supplied. Builder/Run describes access, not connection or delivery. Crews run when called and have no laptop heartbeat. Each Crew row says how it relates to you in relation: this_crew (the Crew you are working in), yours (you own it and linked it to this Crew), team (you are in its Team) or shared (shared with you); chats say who shared them (shared_by) and function_agent marks a function's own agent, which only its owner talks to. Last seen is connection activity, not proof that the model read a message. Missing timestamps are unknown, never zero activity. Use the id; a name works only when unique.",
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
		return "messages action=send(target=<id>, chat_id=<chat_id from chats>) messages one of its agents; omit chat_id for the Crew ask. " + call
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
			for _, field := range []string{"agent_name", "name", "label", "purpose", "description", "display"} {
				value, _ := row[field].(string)
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
		parts = append(parts, "Crew: functions action=call(project=<crew_id>, function=<name>, input=<its asks_for>), then functions action=status(call_id) until it settles; or workspace action=ask. A function's own agent is not a chat to message")
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
