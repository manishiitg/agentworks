package server

import (
	"context"
	"log"
)

// Enrich only rows already admitted by the directory. No model, effort, group
// or Crew name is accepted from a tool argument. Unknown remote records stay
// unknown; the connection's remote directory is responsible for its projection.
func (api *StreamingAPI) annotateAgentRuntimeInfo(ctx context.Context, caller string, rows []map[string]interface{}) {
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
					model, modelErr := effectiveProfileChatModel(profile, binding, record, identity)
					if modelErr == nil {
						row["cli"], row["engine"], row["model_id"], row["reasoning_effort"] = providerAccountProductName(model.Provider), model.Engine, model.ModelID, model.ReasoningEffort
						row["crew_name"] = manifest.displayTitle()
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
