package server

import "context"

// The panel receives only runtime labels from the ordinary directory projection,
// never the helper's owner-status, organization, paths or account data.
type crewTeamPanelRuntime struct {
	CLI             string `json:"cli,omitempty"`
	Engine          string `json:"engine,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

func crewTeamPanelRuntimeFor(ctx context.Context, viewer string, out *crewTeamTabReply) {
	api := runningServerAPI
	if api == nil {
		return
	}
	rows := []map[string]interface{}{}
	targets := []*crewTeamPanelRuntime{}
	for i := range out.Reachable {
		row := &out.Reachable[i]
		rows = append(rows, map[string]interface{}{"id": row.Address})
		targets = append(targets, &row.crewTeamPanelRuntime)
	}
	for i := range out.Agents {
		row := &out.Agents[i]
		if !row.Mine {
			continue
		}
		rows = append(rows, map[string]interface{}{"id": row.Address, "chat_id": row.Key, "yours": true})
		targets = append(targets, &row.crewTeamPanelRuntime)
	}
	// The shared directory helper owns runtime resolution and its current audience;
	// the browser neither re-queries the directory nor derives another model.
	api.annotateAgentRuntimeInfo(ctx, viewer, rows)
	for i, row := range rows {
		targets[i].CLI, _ = row["cli"].(string)
		targets[i].Engine, _ = row["engine"].(string)
		targets[i].ModelID, _ = row["model_id"].(string)
		targets[i].ReasoningEffort, _ = row["reasoning_effort"].(string)
	}
}
