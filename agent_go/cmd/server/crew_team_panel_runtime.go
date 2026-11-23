package server

import (
	"context"
	"sync"
	"time"
)

// The panel receives only runtime labels from the ordinary directory projection,
// never the helper's owner-status, organization, paths or account data.
type crewTeamPanelRuntime struct {
	ClientBuild     string `json:"client_build,omitempty"`
	UpdateNeeded    bool   `json:"update_needed,omitempty"`
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
	agentIDs := []string{}
	now := time.Now()
	cache := &crewTeamPanelRuntimeCache
	cache.Lock()
	for key, entry := range cache.entries {
		if !now.Before(entry.expires) {
			delete(cache.entries, key)
		}
	}
	add := func(owner, chat, agentID string, row map[string]interface{}, target *crewTeamPanelRuntime) {
		key := crewTeamPanelRuntimeKey{api: api, owner: owner, chat: chat}
		if entry, ok := cache.entries[key]; ok {
			*target = entry.labels
			return
		}
		rows = append(rows, row)
		targets = append(targets, target)
		keys = append(keys, key)
		agentIDs = append(agentIDs, agentID)
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
		add(owner, chat, row.ID, map[string]interface{}{"id": row.Address}, &row.crewTeamPanelRuntime)
	}
	for i := range out.Agents {
		row := &out.Agents[i]
		if !row.Mine {
			continue
		}
		add(row.OwnerID, row.Key, row.ID, map[string]interface{}{"id": row.Address, "chat_id": row.Key, "yours": true, "team_agent_id": row.ID}, &row.crewTeamPanelRuntime)
	}
	cache.Unlock()
	if len(rows) == 0 {
		return
	}
	// The shared helper may read manifests/providers per row. Streaming /tab
	// refreshes reuse labels for 30 seconds, never repeating that work per chunk.
	admitted := make([]crewTeamReachableAgent, 0, len(out.Reachable))
	for _, row := range out.Reachable {
		admitted = append(admitted, row.crewTeamReachableAgent)
	}
	api.annotateAgentRuntimeInfoFromReach(ctx, viewer, rows, admitted)
	// Own terminal rows are not in the other-members reach set. Match only the
	// already-admitted own registry row through the canonical current terminal binding (including a same-session reattach).
	team, teamErr := readCrewTeam(out.Team.ProjectID)
	if teamErr == nil {
		for _, row := range rows {
			id, _ := row["team_agent_id"].(string)
			registered, ok := team.Agents[id]
			if !ok || registered.OwnerID != viewer || registered.Terminal == nil {
				continue
			}
			if named, err := currentCrewTeamTerminal(ctx, team, registered); err == nil {
				projectAgentHelper(row, named)
			}
		}
	}

	expires := time.Now().Add(30 * time.Second)
	cache.Lock()
	defer cache.Unlock()
	for i, row := range rows {
		labels := crewTeamPanelRuntime{}
		labels.ClientBuild, _ = row["client_build"].(string)
		labels.UpdateNeeded, _ = row["update_needed"].(bool)
		labels.CLI, _ = row["cli"].(string)
		labels.Engine, _ = row["engine"].(string)
		labels.ModelID, _ = row["model_id"].(string)
		labels.ReasoningEffort, _ = row["reasoning_effort"].(string)
		// A teammate's terminal chat has no directory runtime row for this viewer;
		// its Team record still names the program (Claude Code or Codex), only that.
		if labels.CLI == "" && labels.Engine == "" && teamErr == nil {
			if registered, ok := team.Agents[agentIDs[i]]; ok && registered.Terminal != nil {
				labels.CLI = providerAccountProductName(registered.Terminal.Provider)
			}
		}
		*targets[i] = labels
		cache.entries[keys[i]] = crewTeamPanelRuntimeEntry{labels: labels, expires: expires}
	}
}
