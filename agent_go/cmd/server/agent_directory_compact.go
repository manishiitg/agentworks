package server

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

const agentDirectoryMaxBytes = 20_000
const agentDirectoryMaxRows = 20

// Tool presentation only. The full admitted directory remains the source for
// address resolution and the browser. Never clip an address into a new target.
// offset pages through the matches (#3364): an agent can list everyone it may
// message, 20 rows at a time, following next_offset.
func compactAgentDirectory(ctx context.Context, user string, directory map[string]any, query string, offset int) map[string]any {
	all, _ := directory["agents"].([]map[string]any)
	// #3666: projects (a Crew, Goal or Relay) are called through their functions or Ask, never messaged as a chat.
	// They get their own section so the agent count covers agents only.
	rows, projectRows := []map[string]any{}, []map[string]any{}
	for _, row := range all {
		if agentDirectoryProjectKind(stringField(row["kind"])) {
			projectRows = append(projectRows, row)
		} else {
			rows = append(rows, row)
		}
	}
	counts := map[string]int{}
	teams := map[string]int{}
	for _, row := range rows {
		kind := stringField(row["kind"])
		switch kind {
		case "team_agent", "connected_agent", "crew_agent", "mac_agent":
		default:
			kind = "other"
		}
		counts[kind]++
		if team := stringField(row["team_name"]); team != "" {
			teams[team]++
		}
	}
	total := len(rows)
	filtered := map[string]any{"agents": rows, "projects": projectRows}
	filterAgentDirectory(filtered, query)
	rows, _ = filtered["agents"].([]map[string]any)
	projectRows, _ = filtered["projects"].([]map[string]any)
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
	if offset < 0 {
		offset = 0
	}
	if offset > len(rows) {
		offset = len(rows)
	}
	rows = rows[offset:]
	out := map[string]any{
		"agents": []map[string]any{}, "total": total, "matched": matched,
		"counts_by_kind": counts, "teams": []map[string]any{},
		"hint": "More agents: call again with offset=next_offset, or use query=<agent name or Team>. Copy the exact target from a returned row; never shorten or guess it. Product IDs are not peer addresses.",
	}
	projects := []map[string]any{}
	for _, row := range projectRows {
		if len(projects) == agentDirectoryMaxRows {
			break
		}
		item := map[string]any{}
		for _, field := range []string{"id", "kind", "crew_id", "workflow_id", "name", "owner", "presence", "delivery_mode"} {
			if value := stringField(row[field]); value != "" {
				if field == "id" || field == "crew_id" || field == "workflow_id" {
					item[field] = value
				} else {
					item[field] = directoryText(value, 160)
				}
			}
		}
		if description := stringField(row["description"]); description != "" {
			item["description"] = directoryText(description, 240)
		}
		if functions := agentDirectoryFunctionNames(row["functions"]); len(functions) > 0 {
			item["functions"] = functions
		}
		projects = append(projects, item)
	}
	out["projects"], out["project_count"] = projects, len(projectRows)
	if len(projectRows) > 0 {
		out["projects_hint"] = "Projects are not agents: call their functions or Ask them; never message them as a chat."
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
	// #3666: your own agents carried no owner, so an agent read the owners it saw as the only people and missed
	// its own person's agents. Name them, and say which account "you" is.
	you := agentDirectoryYouLabel(user)
	out["you"] = you
	out["hint"] = stringField(out["hint"]) + " Rows with yours=true belong to your own person (" + you + "); every other owner is a separate person."
	kept := []map[string]any{}
	// consumed counts the rows this page used up, shown or not, so next_offset
	// never skips or repeats an agent.
	consumed := 0
	for _, row := range rows {
		if len(kept) == agentDirectoryMaxRows {
			break
		}
		item := map[string]any{}
		// No nested chat inventories, tools, transcripts or runtime metadata. A
		// focused status request supplies details without repeating the inventory.
		for _, field := range []string{"id", "target", "agent_id", "agent_name", "kind", "name", "label", "owner", "team_name", "team_project_id", "crew_name", "cli", "provider", "presence", "delivery_mode", "online", "hidden", "activity", "activity_source", "activity_reason", "last_active_label", "current_task", "yours", "published"} {
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
		if item["yours"] == true && strings.TrimSpace(stringField(item["owner"])) == "" {
			item["owner"] = you
		}
		if purpose := stringField(row["purpose"]); purpose != "" {
			item["purpose"] = directoryText(purpose, 240)
		}
		out["agents"] = append(kept, item)
		out["returned"], out["truncated"], out["next_offset"] = len(kept)+1, true, matched
		raw, _ := json.Marshal(out)
		if len(raw) >= agentDirectoryMaxBytes {
			if len(kept) > 0 {
				break // the page is full; this agent starts the next one
			}
			consumed++ // one oversized row alone: omitted intact, never clipped
			continue
		}
		kept = append(kept, item)
		consumed++
	}
	next := offset + consumed
	out["agents"], out["returned"], out["truncated"] = kept, len(kept), next < matched
	delete(out, "next_offset")
	if next < matched {
		out["next_offset"] = next
	}
	if offset > 0 {
		out["offset"] = offset
	}
	raw, _ := json.Marshal(out)
	log.Printf("[AGENTBRIDGE] event=directory user=%s total=%d matched=%d offset=%d returned=%d bytes=%d searched=%t", user, total, matched, offset, len(kept), len(raw), strings.TrimSpace(query) != "")
	return out
}

func agentDirectoryProjectKind(kind string) bool {
	return kind == "crew" || kind == "goal" || kind == "relay"
}

// agentDirectoryFunctionNames keeps only a project's function names (at most 10); call details come from the function.
func agentDirectoryFunctionNames(raw any) []string {
	names := []string{}
	add := func(name string) {
		if name = strings.TrimSpace(name); name != "" && len(names) < 10 {
			names = append(names, directoryText(name, 80))
		}
	}
	switch list := raw.(type) {
	case []map[string]any:
		for _, f := range list {
			add(stringField(f["name"]))
		}
	case []any:
		for _, f := range list {
			if m, ok := f.(map[string]any); ok {
				add(stringField(m["name"]))
			}
		}
	}
	return names
}

// agentDirectoryYouLabel is "you", with the account names this person is signed in as: on a Mac, each saved
// network sign-in (the name teammates see); on a server, the account's own username.
func agentDirectoryYouLabel(user string) string {
	names := []string{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name != "" && name != "unknown" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if IsLocalMode() {
		if connections, err := readDesktopConnections(user); err == nil {
			for _, connection := range connections {
				add(firstNonEmptyTrimmed(connection.User.Username, strings.SplitN(connection.User.Email, "@", 2)[0]))
			}
		}
	} else {
		add(shareOwnerLabel(user))
	}
	if len(names) == 0 {
		return "you"
	}
	return "you (" + strings.Join(names, ", ") + ")"
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
