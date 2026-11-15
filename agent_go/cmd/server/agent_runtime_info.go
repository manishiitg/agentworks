package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"log"
	"strings"
	"time"
)

// Enrich only rows already admitted by the directory. No model, effort, group
// or Crew name is accepted from a tool argument. Unknown remote records stay
// unknown; the connection's remote directory is responsible for its projection.
func (api *StreamingAPI) annotateAgentRuntimeInfo(ctx context.Context, caller string, rows []map[string]interface{}) {
	// Metadata is optional: an unavailable model catalog must not hide the
	// already-authorized directory or prevent people messaging an agent.
	if api.agentProfiles == nil && api.projectActions == nil {
		log.Printf("[AGENT_INFO] caller=%s action=skipped reason=model_catalog_unavailable", caller)
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(caller))
	if err != nil {
		log.Printf("[AGENT_INFO] caller=%s action=skipped reason=registry_unavailable", caller)
		return
	}
	byAddress := map[string]ProductConversationRecord{}
	for _, record := range doc.Entries {
		if record.ConversationID != "" {
			byAddress[desktopAppAgentName(desktopMachineID(), record.ConversationID)] = record
		}
	}
	groups, _ := updateMenuGroups(ctx, nil)
	reachable := api.reachableAgentRuntimeInfo(ctx, caller)
	count := 0
	var annotate func(map[string]interface{})
	annotate = func(row map[string]interface{}) {
		// This is the caller's registry, never a guess based on a display name
		// or a conversation key received from another company's network.
		yours, _ := row["yours"].(bool)
		if yours {
			name, _ := row["agent_name"].(string)
			record, found := byAddress[name]
			if key, _ := row["chat_id"].(string); !found && key != "" {
				record, found = doc.Entries[productConversationRegistryEntryKey(crewProfileID, key)]
			}
			if found && record.ResourceID != "" {
				identity := doc.Agents[productConversationRegistryEntryKey(record.ProfileID, record.ConversationKey)]
				info := api.runtimeInfoForRecord(ctx, caller, record, identity, groups)
				projectAgentRuntimeInfo(row, info, true)
				count++
			}
		}
		if id, _ := row["id"].(string); id != "" {
			if admitted, ok := reachable[id]; ok {
				row["online"] = admitted.Reach.Online
				projectAgentRuntimeInfo(row, admitted.Info, true)
				activity := "Idle"
				switch admitted.Reach.Activity {
				case "running":
					activity = "Working"
				case "waiting":
					activity = "Waiting for you"
				}
				projectAgentActivity(row, activity, admitted.Info.LastActiveAt)
				row["crew_name"] = admitted.Reach.TeamName
				count++
			}
		}
		if children, ok := row["chats"].([]map[string]interface{}); ok {
			for _, child := range children {
				annotate(child)
			}
		}
	}
	for _, row := range rows {
		annotate(row)
	}
	log.Printf("[AGENT_INFO] caller=%s action=done enriched=%d", caller, count)
}

// Runtime facts are public to callers who already see the agent. Project
// organization is a separate, private projection, never a directory grant.
type agentRuntimeInfo struct {
	TeamProject     string            `json:"team_project_id,omitempty"`
	TeamTab         string            `json:"team_tab,omitempty"`
	OwnerStatus     *agentOwnerStatus `json:"owner_status,omitempty"`
	Activity        string            `json:"activity,omitempty"`
	LastActiveAt    time.Time         `json:"last_active_at,omitempty"`
	CLI             string            `json:"cli,omitempty"`
	Engine          string            `json:"engine,omitempty"`
	ModelID         string            `json:"model_id,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	CrewID          string            `json:"crew_id,omitempty"`
	CrewName        string            `json:"crew_name,omitempty"`
	GroupNames      []string          `json:"group_names,omitempty"`
}

func projectAgentRuntimeInfo(row map[string]interface{}, info *agentRuntimeInfo, internal bool) {
	if info == nil {
		return
	}
	row["cli"], row["engine"], row["model_id"], row["reasoning_effort"] = info.CLI, info.Engine, info.ModelID, info.ReasoningEffort
	if internal {
		row["crew_name"], row["group_names"] = info.CrewName, info.GroupNames
	}
}

// This route is private app-backend registration, not the agent tool's register
// arguments. Keep the projection bounded and exclude all runtime options,
// account identifiers, file paths and credentials.
func decodeAppAgentRuntimeInfo(raw any) (*agentRuntimeInfo, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil || len(data) > 8192 {
		return nil, fmt.Errorf("agent runtime information is too large")
	}
	var info agentRuntimeInfo
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&info); err != nil {
		return nil, fmt.Errorf("agent runtime information is invalid")
	}
	for _, field := range []string{info.TeamProject, info.TeamTab, info.Activity, info.CLI, info.Engine, info.ModelID, info.ReasoningEffort, info.CrewID, info.CrewName} {
		if len(field) > 256 || strings.ContainsAny(field, "\r\n\x00") {
			return nil, fmt.Errorf("agent runtime information must contain short single-line values")
		}
	}
	if info.Activity != "" && info.Activity != "Working" && info.Activity != "Idle" && info.Activity != "Waiting for you" {
		return nil, fmt.Errorf("agent activity is invalid")
	}
	if info.LastActiveAt.After(time.Now().Add(time.Minute)) {
		return nil, fmt.Errorf("agent activity time is invalid")
	}
	if info.OwnerStatus != nil {
		if len(info.OwnerStatus.CurrentStep) > 320 || len(info.OwnerStatus.CurrentTask) > 320 || len(info.OwnerStatus.LastMessages) > 3 {
			return nil, fmt.Errorf("agent status is too large")
		}
		for _, line := range info.OwnerStatus.LastMessages {
			if len(line) > 320 {
				return nil, fmt.Errorf("agent status line is too large")
			}
		}
	}
	if len(info.GroupNames) > 32 {
		return nil, fmt.Errorf("too many agent groups")
	}
	for _, name := range info.GroupNames {
		if len(name) > 160 || strings.ContainsAny(name, "\r\n\x00") {
			return nil, fmt.Errorf("agent group name is invalid")
		}
	}
	return &info, nil
}

func (api *StreamingAPI) runtimeInfoForRecord(ctx context.Context, owner string, record ProductConversationRecord, identity ProductAgentIdentity, groups menuGroups) *agentRuntimeInfo {
	info := api.activityForRecord(record)
	info.TeamProject = record.ResourceID
	info.TeamTab = crewStandbyTabForKeyOf(record.ResourceID, owner, record.ConversationKey)
	if api.agentProfiles == nil && api.projectActions == nil {
		return info
	}
	profile, binding, manifest, _, err := api.crewMoveDispatcher().projectManifestAnyOwner(ctx, owner, record.ProfileID, record.ResourceID)
	if err != nil {
		return info
	}
	info.CrewID, info.CrewName, info.GroupNames = record.ResourceID, manifest.displayTitle(), groups.agentGroupNames(record.ResourceID, record.ConversationKey)
	model, err := effectiveProfileChatModel(profile, binding, record, identity)
	if err == nil {
		info.CLI, info.Engine, info.ModelID, info.ReasoningEffort = providerAccountProductName(model.Provider), model.Engine, model.ModelID, model.ReasoningEffort
	} else {
		log.Printf("[AGENT_INFO] owner=%s session=%s action=skipped reason=model_unavailable", owner, record.SessionID)
	}
	return info
}

// The same queue and pending human-input store used by the Team panel. Reading
// activity never starts a turn and never copies its question or event payload.
func (api *StreamingAPI) activityForRecord(record ProductConversationRecord) *agentRuntimeInfo {
	info := &agentRuntimeInfo{Activity: "Idle"}
	if api.sessionTurnRunning(record.SessionID) {
		info.Activity = "Working"
	}
	if len(externalPendingInputs(record.SessionID)) > 0 {
		info.Activity = "Waiting for you"
	}
	if api.eventStore != nil {
		// Read only one tail entry. Never hydrate an entire cold chat merely
		// to show a directory timestamp.
		last := api.eventStore.LastIndex(record.SessionID)
		tail := api.eventStore.GetForwardEventPage(record.SessionID, last-1, 1).Events
		if len(tail) == 0 {
			page, err := api.eventStore.ReadDurableChatPage(record.SessionID, storeevents.DurableEventPageOptions{Limit: 1})
			if err == nil {
				tail = page.Events
			} else {
				log.Printf("[AGENT_ACTIVITY] session=%s action=skipped reason=last_active_unavailable", record.SessionID)
			}
		}
		for _, event := range tail {
			info.LastActiveAt = event.Timestamp
		}
	}
	log.Printf("[AGENT_ACTIVITY] session=%s action=read state=%s last_active_known=%t", record.SessionID, strings.ReplaceAll(info.Activity, " ", "_"), !info.LastActiveAt.IsZero())
	return info
}

func projectAgentActivity(row map[string]interface{}, activity string, at time.Time) {
	if online, known := row["online"].(bool); known && !online {
		activity = "Offline"
	}
	if activity == "" {
		activity = "Idle"
	}
	row["activity"] = activity
	delete(row, "last_active_at")
	delete(row, "last_active_label")
	if !at.IsZero() {
		row["last_active_at"] = at
		minutes := int(time.Since(at).Minutes())
		if minutes < 1 {
			row["last_active_label"] = "last active just now"
		} else {
			row["last_active_label"] = fmt.Sprintf("last active %d min ago", minutes)
		}
	}
}
