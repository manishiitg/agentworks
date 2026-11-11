package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"path"
	"sort"
	"strings"
)

// PLAT-746-886: deletion is a server-owned plan, shared by the app and MCP.
// A preview never changes work. Every linked item needs an explicit choice.
type crewAgentRemovalDependency struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	CanReassign bool     `json:"can_reassign"`
	WhenRemoved []string `json:"when_removed,omitempty"`
}
type crewAgentRemovalDestination struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}
type crewAgentRemovalPreview struct {
	Confirmation string                        `json:"confirmation"`
	Dependencies []crewAgentRemovalDependency  `json:"dependencies"`
	Destinations []crewAgentRemovalDestination `json:"destinations"`
	Effects      []string                      `json:"effects"`
}
type crewAgentRemovalFile struct {
	path, raw, collection string
	document              map[string]any
	choices               map[int]string
	references            map[int][]map[string]any
	fields                map[int][]string
}
type crewAgentRemovalPlan struct {
	preview       crewAgentRemovalPreview
	files         []*crewAgentRemovalFile
	applyChannels []func(context.Context, map[string]string) error
}

func removalReferenceMatches(value, key, project string, main bool) bool {
	value = strings.TrimSpace(value)
	return value == key || value == strings.TrimPrefix(key, project+codeChatSideMarker) || main && (value == "" || value == "main" || value == project)
}

func agentRemovalRecordRefs(record map[string]any, collection, key, project string, main bool) ([]map[string]any, []string, bool) {
	var objects []map[string]any
	var fields []string
	add := func(object map[string]any, field string, implicit bool) {
		if object == nil {
			return
		}
		value, _ := object[field].(string)
		if value == "" && !implicit {
			return
		}
		if removalReferenceMatches(value, key, project, main && (implicit || value != "")) {
			objects = append(objects, object)
			fields = append(fields, field)
		}
	}
	action, _ := record["action"].(map[string]any)
	input, _ := record["input"].(map[string]any)
	condition, _ := input["condition"].(map[string]any)
	if condition != nil {
		target, _ := condition["target"].(map[string]any)
		github, _ := condition["github_pr"].(map[string]any)
		if github != nil {
			add(github, "chat_key", false)
			return objects, fields, false
		}
		if target["kind"] == "agent" {
			add(target, "chat_id", true)
		}
		return objects, fields, true
	}
	isolated := action["conversation"] == "isolated" || action["conversation"] == "new"
	// Function tabs are named independent destinations; an omitted function tab
	// is not the project Main. Script-only runs need no conversational receiver.
	if !isolated {
		add(action, "tab", collection != "functions" && action["type"] == "message")
	}
	return objects, fields, true
}

func (api *StreamingAPI) planCrewAgentRemoval(ctx context.Context, caller string, binding productConversationBinding, sessions []string) (crewAgentRemovalPlan, error) {
	plan := crewAgentRemovalPlan{preview: crewAgentRemovalPreview{Dependencies: []crewAgentRemovalDependency{}, Destinations: []crewAgentRemovalDestination{}, Effects: []string{"Delete this agent's saved chats and queued messages", "Remove this agent's settings and shared listing", "Keep project files and other agents"}}}
	project, key := binding.ResourceID, binding.ConversationKey
	hash := sha256.New()
	fmt.Fprintln(hash, caller, key, strings.Join(sessions, "|"))
	roots := []string{binding.WorkspacePath}
	executables, err := readCrewExecutables()
	if err != nil {
		return plan, err
	}
	for _, executable := range executables {
		if executable.CrewID == project && canonicalCrewWorkspaceRoot(executable.CrewRoot) == canonicalCrewWorkspaceRoot(binding.WorkspacePath) {
			roots = append(roots, executable.Root)
		}
	}
	sort.Strings(roots)
	seen := map[string]bool{}
	for _, root := range roots {
		if seen[root] {
			continue
		}
		seen[root] = true
		for _, collection := range []string{"schedules", "webhooks", "functions"} {
			file := path.Join(root, collection+".json")
			raw, found, err := readFileFromWorkspace(ctx, file)
			if err != nil {
				return plan, err
			}
			if !found {
				continue
			}
			fmt.Fprintln(hash, file, raw)
			state := &crewAgentRemovalFile{path: file, raw: raw, collection: collection, choices: map[int]string{}, references: map[int][]map[string]any{}, fields: map[int][]string{}}
			if err := json.Unmarshal([]byte(raw), &state.document); err != nil {
				return plan, fmt.Errorf("read linked %s: %w", collection, err)
			}
			records, _ := state.document[collection].([]any)
			for index, item := range records {
				record, ok := item.(map[string]any)
				if !ok {
					return plan, fmt.Errorf("invalid linked %s record", collection)
				}
				objects, fields, reassign := agentRemovalRecordRefs(record, collection, key, project, key == binding.ProjectMainChatKey)
				if len(objects) == 0 {
					continue
				}
				id, _ := record["id"].(string)
				if id == "" {
					return plan, fmt.Errorf("linked %s has no identity", collection)
				}
				stable := sha256.Sum256([]byte(file + "/" + id))
				dependency := hex.EncodeToString(stable[:])
				name, _ := record["name"].(string)
				if name == "" {
					name = collection
				}
				kind := collection
				if input, ok := record["input"].(map[string]any); ok && input["condition"] != nil {
					kind = "watcher"
				}
				plan.preview.Dependencies = append(plan.preview.Dependencies, crewAgentRemovalDependency{ID: dependency, Kind: kind, Name: name, CanReassign: reassign})
				state.choices[index] = dependency
				state.references[index] = objects
				state.fields[index] = fields
			}
			plan.files = append(plan.files, state)
		}
	}
	plan.addDependentSchedules()
	// Project-wide Slack/WhatsApp destinations keep their independent sender/thread
	// chats. Only a channel explicitly bound to this physical agent is a link.
	linkedRoutes := func(routes map[string]ChannelRoute, platform string) map[string]string {
		ids := map[string]string{}
		for channel, route := range routes {
			if route.ProfileID != crewProfileID || route.ConversationKey != key || (route.WorkspaceUserID != "" && route.WorkspaceUserID != caller) {
				continue
			}
			stable := sha256.Sum256([]byte(platform + "/" + channel))
			id := hex.EncodeToString(stable[:])
			ids[channel] = id
			plan.preview.Dependencies = append(plan.preview.Dependencies, crewAgentRemovalDependency{ID: id, Kind: "channel", Name: platform + " · " + channel, CanReassign: true})
		}
		return ids
	}
	if api.chatStore != nil {
		configs, err := api.chatStore.ListBotConnectorConfigs(ctx)
		if err != nil {
			return plan, err
		}
		for _, config := range configs {
			routes := map[string]ChannelRoute{}
			if json.Unmarshal([]byte(config.AllowedChannels), &routes) != nil {
				continue
			}
			ids := linkedRoutes(routes, config.ID)
			if len(ids) == 0 {
				continue
			}
			fmt.Fprintln(hash, config.ID, config.AllowedChannels)
			snapshot := config
			plan.applyChannels = append(plan.applyChannels, func(ctx context.Context, choices map[string]string) error {
				for channel, id := range ids {
					if choices[id] == "remove" {
						delete(routes, channel)
					} else {
						route := routes[channel]
						route.ConversationKey = choices[id]
						routes[channel] = route
					}
				}
				raw, err := json.Marshal(routes)
				if err != nil {
					return err
				}
				_, err = api.chatStore.UpsertBotConnectorConfig(ctx, &chathistory.CreateBotConnectorConfigRequest{ID: snapshot.ID, Enabled: snapshot.Enabled, BotMode: snapshot.BotMode, ConfigJSON: snapshot.ConfigJSON, DefaultPresetID: snapshot.DefaultPresetID, AutoConfirm: snapshot.AutoConfirm, AllowedChannels: string(raw)})
				return err
			})
		}
	}
	if api.whatsappManager != nil {
		routes, err := api.whatsappManager.RoutingForUser(ctx, caller)
		if err != nil {
			return plan, err
		}
		ids := linkedRoutes(map[string]ChannelRoute(routes), "WhatsApp")
		if len(ids) > 0 {
			raw, err := json.Marshal(routes)
			if err != nil {
				return plan, err
			}
			fmt.Fprintln(hash, string(raw))
			plan.applyChannels = append(plan.applyChannels, func(ctx context.Context, choices map[string]string) error {
				unlock := api.whatsappManager.LockAccountRouting(caller)
				defer unlock()
				current, err := api.whatsappManager.RoutingForUser(ctx, caller)
				if err != nil {
					return err
				}
				for channel, id := range ids {
					if choices[id] == "remove" {
						delete(current, channel)
					} else {
						route := current[channel]
						route.ConversationKey = choices[id]
						current[channel] = route
					}
				}
				return api.whatsappManager.SetRoutingForUser(ctx, caller, services.WhatsAppRouting(current))
			})
		}
	}
	// Only the Crew's owner can remove or reassign project-wide saved work.
	if len(plan.preview.Dependencies) > 0 && ownerOfCrewProjectID(project) != caller {
		return plan, fmt.Errorf("This agent has linked project work. Ask the Crew owner to reassign or remove those links first.")
	}
	tabs, err := defaultProductConversationRegistryStore().projectChats(ctx, caller, crewProfileID, project)
	if err != nil {
		return plan, err
	}
	identities, err := defaultProductConversationRegistryStore().agentIdentities(ctx, caller, crewProfileID, project)
	if err != nil {
		return plan, err
	}
	for _, tab := range tabs {
		if tab.ConversationKey != key && ordinaryProjectChatKey(project, tab.ConversationKey) && canonicalCrewWorkspaceRoot(tab.WorkspacePath) == canonicalCrewWorkspaceRoot(binding.WorkspacePath) {
			plan.preview.Destinations = append(plan.preview.Destinations, crewAgentRemovalDestination{tab.ConversationKey, firstNonEmptyTrimmed(identities[tab.ConversationKey].Name, tab.Title, "Agent")})
		}
	}
	sort.Slice(plan.preview.Destinations, func(i, j int) bool { return plan.preview.Destinations[i].Key < plan.preview.Destinations[j].Key })
	fmt.Fprintln(hash, binding.ProjectMainChatKey)
	plan.preview.Confirmation = hex.EncodeToString(hash.Sum(nil))
	return plan, nil
}

func (plan crewAgentRemovalPlan) apply(ctx context.Context, confirmation string, choices map[string]string, project string) error {
	if confirmation == "" || confirmation != plan.preview.Confirmation {
		return fmt.Errorf("Linked work changed. Review the removal preview again before confirming.")
	}
	valid := map[string]bool{}
	for _, target := range plan.preview.Destinations {
		valid[target.Key] = true
	}
	selected := map[string]string{}
	known := map[string]bool{}
	active := map[string]bool{}
	for _, dep := range plan.preview.Dependencies {
		known[dep.ID] = true
		if len(dep.WhenRemoved) == 0 {
			active[dep.ID] = true
		}
	}
	// Reachability is independent of file/preview order; an inactive stale choice
	// must not activate anything downstream, including a cycle.
	for changed := true; changed; {
		changed = false
		for _, dep := range plan.preview.Dependencies {
			if active[dep.ID] {
				continue
			}
			for _, parent := range dep.WhenRemoved {
				if active[parent] && choices[parent] == "remove" {
					active[dep.ID] = true
					changed = true
					break
				}
			}
		}
	}
	for _, dep := range plan.preview.Dependencies {
		if !active[dep.ID] {
			continue
		}
		choice := choices[dep.ID]
		if choice != "remove" && (!dep.CanReassign || !valid[choice]) {
			return fmt.Errorf("Choose Remove or an available agent for %s.", dep.Name)
		}
		selected[dep.ID] = choice
	}
	for id := range choices {
		if !known[id] {
			return fmt.Errorf("Linked work changed. Review the removal preview again.")
		}
	}
	choices = selected
	for _, apply := range plan.applyChannels {
		if err := apply(ctx, choices); err != nil {
			return err
		}
	}
	for _, file := range plan.files {
		if len(file.choices) == 0 {
			continue
		}
		before := file.document[file.collection].([]any)
		after := make([]any, 0, len(before))
		for i, item := range before {
			id := file.choices[i]
			choice := choices[id]
			if id != "" && choice == "remove" {
				continue
			}
			if id != "" && choice != "" {
				for j, object := range file.references[i] {
					field := file.fields[i][j]
					target := choice
					if field == "chat_id" {
						target = strings.TrimPrefix(choice, project+codeChatSideMarker)
					}
					object[field] = target
				}
			}
			after = append(after, item)
		}
		file.document[file.collection] = after
		encoded, err := json.MarshalIndent(file.document, "", "  ")
		if err != nil {
			return err
		}
		if err = writeFileToWorkspace(ctx, file.path, string(encoded)+"\n"); err != nil {
			return err
		}
	}
	return nil
}

// A schedule depending on a removed schedule cannot remain runnable. Show it
// only when its prerequisite is chosen for removal, never delete it implicitly.
func (plan *crewAgentRemovalPlan) addDependentSchedules() {
	for _, file := range plan.files {
		if file.collection != "schedules" {
			continue
		}
		records, _ := file.document[file.collection].([]any)
		known := map[string]string{}
		for i, item := range records {
			record, _ := item.(map[string]any)
			id, _ := record["id"].(string)
			if file.choices[i] != "" {
				known[id] = file.choices[i]
			}
		}
		for {
			added := false
			for i, item := range records {
				if file.choices[i] != "" {
					continue
				}
				record, _ := item.(map[string]any)
				when, _ := record["when"].(map[string]any)
				after, _ := when["after"].(map[string]any)
				dependencies, _ := after["schedules"].([]any)
				var parents []string
				for _, dependency := range dependencies {
					id, _ := dependency.(string)
					if parent := known[id]; parent != "" {
						parents = append(parents, parent)
					}
				}
				if len(parents) == 0 {
					continue
				}
				id, _ := record["id"].(string)
				name, _ := record["name"].(string)
				stable := sha256.Sum256([]byte(file.path + "/" + id))
				key := hex.EncodeToString(stable[:])
				plan.preview.Dependencies = append(plan.preview.Dependencies, crewAgentRemovalDependency{ID: key, Kind: "dependent schedule", Name: firstNonEmptyTrimmed(name, "Schedule"), WhenRemoved: parents})
				file.choices[i] = key
				known[id] = key
				added = true
			}
			if !added {
				break
			}
		}
		// Discovery can reach a second parent later than its child. Refresh
		// every conditional edge only after the complete reachable set exists.
		for i, item := range records {
			key := file.choices[i]
			if key == "" {
				continue
			}
			for j := range plan.preview.Dependencies {
				dep := &plan.preview.Dependencies[j]
				if dep.ID != key || dep.Kind != "dependent schedule" {
					continue
				}
				record, _ := item.(map[string]any)
				when, _ := record["when"].(map[string]any)
				after, _ := when["after"].(map[string]any)
				dependencies, _ := after["schedules"].([]any)
				dep.WhenRemoved = nil
				for _, dependency := range dependencies {
					id, _ := dependency.(string)
					if parent := known[id]; parent != "" {
						dep.WhenRemoved = append(dep.WhenRemoved, parent)
					}
				}
			}
		}
	}
}
