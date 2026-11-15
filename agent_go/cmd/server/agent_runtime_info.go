package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// Enrich only rows already admitted by the directory. No model, effort, group
// or Crew name is accepted from a tool argument. Unknown remote records stay
// unknown; the connection's remote directory is responsible for its projection.
func (api *StreamingAPI) annotateAgentRuntimeInfo(ctx context.Context, caller string, rows []map[string]interface{}) {
	// Metadata is optional: an unavailable model catalog must not hide the
	// already-authorized directory or prevent people messaging an agent.
	if api.agentProfiles == nil && api.projectActions == nil {
		log.Printf("[AGENT_INFO] caller=%s action=skipped reason=model_catalog_unavailable", caller)
		return
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
	dispatcher := api.crewMoveDispatcher()
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
				profile, binding, manifest, _, resolveErr := dispatcher.projectManifestAnyOwner(ctx, caller, record.ProfileID, record.ResourceID)
				if resolveErr == nil {
					identity := doc.Agents[productConversationRegistryEntryKey(record.ProfileID, record.ConversationKey)]
					row["crew_name"] = manifest.displayTitle()
					model, modelErr := effectiveProfileChatModel(profile, binding, record, identity)
					if modelErr == nil {
						row["cli"], row["engine"], row["model_id"], row["reasoning_effort"] = providerAccountProductName(model.Provider), model.Engine, model.ModelID, model.ReasoningEffort
						count++
					} else {
						log.Printf("[AGENT_INFO] caller=%s session=%s action=skipped reason=model_unavailable", caller, record.SessionID)
					}
				}
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
	CLI             string   `json:"cli,omitempty"`
	Engine          string   `json:"engine,omitempty"`
	ModelID         string   `json:"model_id,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	CrewID          string   `json:"crew_id,omitempty"`
	CrewName        string   `json:"crew_name,omitempty"`
	GroupNames      []string `json:"group_names,omitempty"`
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
	for _, field := range []string{info.CLI, info.Engine, info.ModelID, info.ReasoningEffort, info.CrewID, info.CrewName} {
		if len(field) > 256 || strings.ContainsAny(field, "\r\n\x00") {
			return nil, fmt.Errorf("agent runtime information must contain short single-line values")
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
	if api.agentProfiles == nil && api.projectActions == nil {
		return nil
	}
	profile, binding, manifest, _, err := api.crewMoveDispatcher().projectManifestAnyOwner(ctx, owner, record.ProfileID, record.ResourceID)
	if err != nil {
		return nil
	}
	info := &agentRuntimeInfo{CrewID: record.ResourceID, CrewName: manifest.displayTitle(), GroupNames: groups.agentGroupNames(record.ResourceID, record.ConversationKey)}
	model, err := effectiveProfileChatModel(profile, binding, record, identity)
	if err == nil {
		info.CLI, info.Engine, info.ModelID, info.ReasoningEffort = providerAccountProductName(model.Provider), model.Engine, model.ModelID, model.ReasoningEffort
	} else {
		log.Printf("[AGENT_INFO] owner=%s session=%s action=skipped reason=model_unavailable", owner, record.SessionID)
	}
	return info
}
