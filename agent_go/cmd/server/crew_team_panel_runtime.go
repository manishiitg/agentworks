package server

import (
	"context"
	"sync"
	"time"
)

// The panel receives only runtime labels from the ordinary directory projection,
// never the helper's owner-status, organization, paths or account data.
type crewTeamPanelRuntime struct {
	CLI             string `json:"cli,omitempty"`
	Engine          string `json:"engine,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type crewTeamPanelRuntimeKey struct {
	api         *StreamingAPI
	owner, chat string
}
type crewTeamPanelRuntimeEntry struct {
	labels  crewTeamPanelRuntime
	expires time.Time
}

var crewTeamPanelRuntimeCache = struct {
	sync.Mutex
	entries map[crewTeamPanelRuntimeKey]crewTeamPanelRuntimeEntry
}{entries: map[crewTeamPanelRuntimeKey]crewTeamPanelRuntimeEntry{}}

func crewTeamPanelRuntimeFor(ctx context.Context, viewer string, out *crewTeamTabReply) {
	api := runningServerAPI
	if api == nil {
		return
	}
	rows := []map[string]interface{}{}
	targets := []*crewTeamPanelRuntime{}
	keys := []crewTeamPanelRuntimeKey{}
	now := time.Now()
	cache := &crewTeamPanelRuntimeCache
	cache.Lock()
	for key, entry := range cache.entries {
		if !now.Before(entry.expires) {
			delete(cache.entries, key)
		}
	}
	add := func(owner, chat string, row map[string]interface{}, target *crewTeamPanelRuntime) {
		key := crewTeamPanelRuntimeKey{api: api, owner: owner, chat: chat}
		if entry, ok := cache.entries[key]; ok {
			*target = entry.labels
			return
		}
		rows = append(rows, row)
		targets = append(targets, target)
		keys = append(keys, key)
	}
	// Audience is freshly resolved by /tab before this function. Cache only the
	// four display strings: a cached label never creates a row or admits a viewer.
	for i := range out.Reachable {
		row := &out.Reachable[i]
		owner, chat := row.PersonID, row.Address
		for _, agent := range out.Agents {
			if agent.ID == row.ID {
				owner, chat = agent.OwnerID, agent.Key
				break
			}
		}
		if row.Kind == "team_agent" {
			owner = "team-" + out.Team.ProjectID
		}
		add(owner, chat, map[string]interface{}{"id": row.Address}, &row.crewTeamPanelRuntime)
	}
	for i := range out.Agents {
		row := &out.Agents[i]
		if !row.Mine {
			continue
		}
		add(row.OwnerID, row.Key, map[string]interface{}{"id": row.Address, "chat_id": row.Key, "yours": true}, &row.crewTeamPanelRuntime)
	}
	cache.Unlock()
	if len(rows) == 0 {
		return
	}
	// The shared helper may read manifests/providers per row. Streaming /tab
	// refreshes reuse labels for 30 seconds, never repeating that work per chunk.
	api.annotateAgentRuntimeInfo(ctx, viewer, rows)
	expires := time.Now().Add(30 * time.Second)
	cache.Lock()
	defer cache.Unlock()
	for i, row := range rows {
		labels := crewTeamPanelRuntime{}
		labels.CLI, _ = row["cli"].(string)
		labels.Engine, _ = row["engine"].(string)
		labels.ModelID, _ = row["model_id"].(string)
		labels.ReasoningEffort, _ = row["reasoning_effort"].(string)
		*targets[i] = labels
		cache.entries[keys[i]] = crewTeamPanelRuntimeEntry{labels: labels, expires: expires}
	}
}
