package server

import "log"

// Fold only already-admitted own rows proven to refer to a record on this
// device. A matching label, a remote chat key, or another person's app name is
// never identity evidence. Addresses remain aliases, not new delivery grants.
func mergeOwnAppChatRows(rows []map[string]any, entries map[string]ProductConversationRecord, device string) []map[string]any {
	if device == "" {
		return rows
	}
	byApp, byChat := map[string]string{}, map[string]string{}
	for _, record := range entries {
		if record.ConversationID == "" || record.ProfileID != crewProfileID {
			continue
		}
		byApp[desktopAppAgentName(device, record.ConversationID)] = record.ConversationID
		byChat[record.ResourceID+"\x00"+record.ConversationKey] = record.ConversationID
	}
	out := []map[string]any{}
	seen := map[string]int{}
	for _, row := range rows {
		key := ""
		if row["yours"] == true {
			if row["provider"] == "agentworks" {
				key = byApp[stringField(row["agent_name"])]
			}
			if key == "" {
				key = byChat[stringField(row["crew_id"])+"\x00"+stringField(row["chat_id"])]
			}
		}
		if key == "" {
			out = append(out, row)
			continue
		}
		address := firstNonEmptyTrimmed(stringField(row["target"]), stringField(row["id"]))
		if index, exists := seen[key]; exists {
			canonical := out[index]
			aliases, _ := canonical["addresses"].([]string)
			if address != "" {
				canonical["addresses"] = append(aliases, address)
			}
			log.Printf("[AGENTBRIDGE] event=directory_identity chat=%s action=merged", key)
			continue
		}
		seen[key] = len(out)
		row["addresses"] = []string{address}
		if name := stringField(row["name"]); name != "" {
			row["label"] = name
		}
		out = append(out, row)
	}
	return out
}

func agentDirectoryAddresses(row map[string]any) []string {
	addresses, _ := row["addresses"].([]string)
	return addresses
}
