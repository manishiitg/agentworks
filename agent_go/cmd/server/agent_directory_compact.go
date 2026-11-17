package server

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"unicode/utf8"
)

const agentDirectoryMaxBytes = 20_000
const agentDirectoryMaxRows = 20

// Tool presentation only. The full admitted directory remains the source for
// address resolution and the browser. Never clip an address into a new target.
func compactAgentDirectory(ctx context.Context, user string, directory map[string]any, query string) map[string]any {
	rows, _ := directory["agents"].([]map[string]any)
	rows = append([]map[string]any(nil), rows...)
	counts := map[string]int{}
	teams := map[string]int{}
	for _, row := range rows {
		kind := stringField(row["kind"])
		switch kind {
		case "team_agent", "connected_agent", "crew_agent", "mac_agent", "crew", "goal", "relay":
		default:
			kind = "other"
		}
		counts[kind]++
		if team := stringField(row["team_name"]); team != "" {
			teams[team]++
		}
	}
	total := len(rows)
	filtered := map[string]any{"agents": rows}
	filterAgentDirectory(filtered, query)
	rows, _ = filtered["agents"].([]map[string]any)
	matched := len(rows)
	// Rank from the caller's existing threads only; never other people's traffic
	// or message text. Admission has already removed unavailable contacts.
	frequency := map[string]int{}
	if strings.TrimSpace(query) == "" {
		if store, err := readAgentMessageSnapshot(ctx); err == nil {
			frequency = agentDirectoryContactFrequency(store, user)
		}
		sort.SliceStable(rows, func(i, j int) bool {
			return frequency[stringField(rows[i]["agent_id"])] > frequency[stringField(rows[j]["agent_id"])]
		})
	}
	out := map[string]any{
		"agents": []map[string]any{}, "total": total, "matched": matched,
		"counts_by_kind": counts, "teams": []map[string]any{},
		"hint": "Use query=<agent name or Team> to find the rest; narrow the query if needed. Copy the exact target from a returned row; never shorten or guess it. Product IDs are not peer addresses.",
	}
	// A Team summary is bounded too, including hostile/oversized labels.
	names := make([]string, 0, len(teams))
	for name := range teams {
		names = append(names, name)
	}
	sort.Strings(names)
	groups := []map[string]any{}
	for _, name := range names {
		if len(groups) == agentDirectoryMaxRows {
			break
		}
		groups = append(groups, map[string]any{"name": directoryText(name, 80), "count": teams[name]})
	}
	out["teams"], out["team_count"] = groups, len(names)
	if directory["network_error"] != nil {
		out["network_error"] = "Some network agents could not be loaded. Try again."
	}
	kept := []map[string]any{}
	for _, row := range rows {
		if len(kept) == agentDirectoryMaxRows {
			break
		}
		item := map[string]any{}
		// No nested chat inventories, tools, transcripts or runtime metadata. A
		// focused status request supplies details without repeating the inventory.
		for _, field := range []string{"id", "target", "agent_id", "agent_name", "kind", "name", "label", "owner", "team_name", "team_project_id", "crew_name", "cli", "provider", "presence", "delivery_mode", "online", "hidden", "activity", "yours", "published"} {
			if value, ok := row[field]; ok {
				switch v := value.(type) {
				case string:
					if field == "id" || field == "target" || field == "agent_id" || field == "agent_name" {
						item[field] = v
					} else {
						item[field] = directoryText(v, 160)
					}
				case bool:
					item[field] = v
				}
			}
		}
		if purpose := stringField(row["purpose"]); purpose != "" {
			item["purpose"] = directoryText(purpose, 240)
		}
		out["agents"] = append(kept, item)
		out["returned"], out["truncated"] = len(kept)+1, len(kept)+1 < matched
		raw, _ := json.Marshal(out)
		if len(raw) >= agentDirectoryMaxBytes {
			continue
		}
		kept = append(kept, item)
	}
	out["agents"], out["returned"], out["truncated"] = kept, len(kept), len(kept) < matched
	raw, _ := json.Marshal(out)
	log.Printf("[AGENTBRIDGE] event=directory user=%s total=%d matched=%d returned=%d bytes=%d searched=%t", user, total, matched, len(kept), len(raw), strings.TrimSpace(query) != "")
	return out
}

func directoryText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}

func agentDirectoryContactFrequency(store agentMessageStore, user string) map[string]int {
	counts := map[string]int{}
	for _, conversation := range store.Conversations {
		for side, end := range conversation.Endpoints {
			if end.UserID != user {
				continue
			}
			peer := conversation.Endpoints[1-side].NamedAgentID
			if peer == "" {
				continue
			}
			for _, message := range conversation.Messages {
				if message.From == side && !serverAuthoredKind(message.Kind) {
					counts[peer]++
				}
			}
		}
	}
	return counts
}
