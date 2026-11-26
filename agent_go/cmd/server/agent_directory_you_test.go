package server

import (
	"strings"
	"testing"
	"time"
)

// #3666: your own agents had no owner, so an agent counted only the named owners as people and missed its own
// person's agents. Your rows now say "you", the reply names who "you" is, and teammates' owners are unchanged.
func TestAgentDirectoryNamesYourOwnAgents(t *testing.T) {
	_ = newTriggerLinkEnv(t)
	rows := []map[string]any{
		{"kind": "connected_agent", "name": "Issue lead", "target": "agent:user/chat-1", "yours": true},
		{"kind": "team_agent", "name": "perf", "target": "network:c:agent:Owner/team-agent-1", "owner": "Owner P."},
		{"kind": "crew", "name": "ai-work", "id": "workspace:work:1", "owner": "you"},
	}
	out := compactAgentDirectory(t.Context(), "owner", map[string]any{"agents": rows}, "", 0)
	you, _ := out["you"].(string)
	if !strings.HasPrefix(you, "you") {
		t.Fatalf("you=%q", you)
	}
	if hint, _ := out["hint"].(string); !strings.Contains(hint, "yours=true belong to your own person") {
		t.Fatalf("hint=%q", hint)
	}
	owners := map[string]string{}
	for _, row := range out["agents"].([]map[string]any) {
		owners[stringField(row["name"])] = stringField(row["owner"])
	}
	if owners["Issue lead"] != you || owners["perf"] != "Owner P." {
		t.Fatalf("owners=%v", owners)
	}
	if _, listed := owners["ai-work"]; listed {
		t.Fatal("a project is listed among the agents")
	}
	if rows[0]["owner"] != nil {
		t.Fatal("compaction changed the resolution inventory")
	}
}

// #3666: a project is called through its functions or Ask, never messaged, so it has its own section and the agent
// counts cover agents only. Each agent row keeps when it was last active and what it is on now.
func TestAgentDirectoryListsProjectsApartWithAgentStatus(t *testing.T) {
	_ = newTriggerLinkEnv(t)
	rows := []map[string]any{
		{"kind": "team_agent", "name": "perf", "target": "network:c:agent:Owner/team-agent-1", "owner": "Owner P.", "activity": "Working", "last_active_label": "last active 3 min ago", "current_task": "Deploying #3666"},
		{"kind": "crew", "name": "ai-work", "id": "workspace:work:1", "crew_id": "1", "owner": "you", "description": "The project", "functions": []map[string]any{{"name": "triage", "description": "x"}}},
		{"kind": "goal", "name": "Daily goal", "id": "goal:1"},
	}
	out := compactAgentDirectory(t.Context(), "owner", map[string]any{"agents": rows}, "", 0)
	agents := out["agents"].([]map[string]any)
	if len(agents) != 1 || out["total"] != 1 || out["matched"] != 1 {
		t.Fatalf("agents=%v total=%v matched=%v", agents, out["total"], out["matched"])
	}
	counts := out["counts_by_kind"].(map[string]int)
	if counts["crew"] != 0 || counts["goal"] != 0 || counts["team_agent"] != 1 {
		t.Fatalf("counts=%v", counts)
	}
	if agents[0]["last_active_label"] != "last active 3 min ago" || agents[0]["current_task"] != "Deploying #3666" {
		t.Fatalf("agent status lost: %v", agents[0])
	}
	projects := out["projects"].([]map[string]any)
	if out["project_count"] != 2 || len(projects) != 2 || projects[0]["name"] != "ai-work" || projects[0]["crew_id"] != "1" {
		t.Fatalf("projects=%v count=%v", projects, out["project_count"])
	}
	if fns, _ := projects[0]["functions"].([]string); len(fns) != 1 || fns[0] != "triage" {
		t.Fatalf("functions=%v", projects[0]["functions"])
	}
	searched := compactAgentDirectory(t.Context(), "owner", map[string]any{"agents": rows}, "ai-work", 0)
	if len(searched["agents"].([]map[string]any)) != 0 || len(searched["projects"].([]map[string]any)) != 1 {
		t.Fatalf("search: %v", searched)
	}
	for ago, want := range map[time.Duration]string{30 * time.Second: "last active just now", 5 * time.Minute: "last active 5 min ago", 3 * time.Hour: "last active 3 h ago", 72 * time.Hour: "last active 3 days ago"} {
		if got := agentLastActiveLabel(ago); got != want {
			t.Fatalf("%v: %q", ago, got)
		}
	}
}

// #3666: each agent row says its program in one spelling, its model, and whether it is an app chat, an attached
// terminal or a server agent, and on whose Mac it runs. Unknown facts are left out.
func TestAgentDirectoryShowsProgramModelAndWhereItRuns(t *testing.T) {
	_ = newTriggerLinkEnv(t)
	rows := []map[string]any{
		{"kind": "team_agent", "name": "ops", "id": "network:c:agent:Owner/team-agent-1", "owner": "Owner P.", "cli": "Claude Code", "model_id": "claude-opus-5-5", "reasoning_effort": "medium", "place": "mac"},
		{"kind": "team_agent", "name": "ai-work", "id": "network:c:agent:Owner/ai-work", "owner": "Owner P.", "cli": "claude", "place": "mac", "terminal": true},
		{"kind": "connected_agent", "name": "computer use", "id": "network:c:agent:Owner/codex-1", "owner": "Owner P.", "cli": "codex", "provider": "codex"},
		{"kind": "team_agent", "name": "always on", "id": "network:c:agent:Owner/team-agent-2", "owner": "Owner P.", "cli": "Codex", "place": "server"},
		{"kind": "team_agent", "name": "unknown", "id": "network:c:agent:Owner/team-agent-3", "owner": "Owner P."},
	}
	out := compactAgentDirectory(t.Context(), "owner", map[string]any{"agents": rows}, "", 0)
	byName := map[string]map[string]any{}
	for _, row := range out["agents"].([]map[string]any) {
		byName[stringField(row["name"])] = row
	}
	check := func(name, field, want string) {
		t.Helper()
		if got := stringField(byName[name][field]); got != want {
			t.Fatalf("%s %s=%q want %q (%v)", name, field, got, want, byName[name])
		}
	}
	check("ops", "program", "Claude Code")
	check("ops", "model", "claude-opus-5-5 · medium effort")
	check("ops", "runs_as", "app chat")
	check("ops", "runs_on", "Owner P.'s Mac")
	check("ai-work", "program", "Claude Code")
	check("ai-work", "runs_as", "terminal (attached)")
	check("computer use", "program", "Codex")
	check("computer use", "runs_as", "terminal (attached)")
	check("computer use", "runs_on", "")
	check("always on", "runs_as", "server agent")
	check("always on", "runs_on", "server")
	for _, field := range []string{"program", "model", "runs_as", "runs_on", "cli", "provider"} {
		check("unknown", field, "")
	}
}
