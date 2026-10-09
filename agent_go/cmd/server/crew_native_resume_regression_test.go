package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// server A 2026-09-24 04:54: after a restart whose deploy changed the Work
// definition, the latency Crew (claude-code) got "Native coding-agent
// continuation unavailable" instead of resuming. A changed Crew definition
// must still find the Crew's own persisted native session (in the Crew
// private runtime, across the day-folder rollover) and resume it. Old shared
// project sessions cannot be adopted into the private mode directory.
func TestCrewResumesNativeSessionAfterDefinitionChangeAcrossDays(t *testing.T) {
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	project := filepath.Join(root, "_users", "user-1", "Chats", "Work", "projects", "latency")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	runtimeDir, err := crewCLIWorkingDir("_users/user-1/Chats/Work/projects/latency", "user-1", "work:project:crew-1", "claude-code", false)
	if err != nil {
		t.Fatal(err)
	}
	write := func(day, native string) {
		t.Helper()
		dir := filepath.Join(project, "builder", "conversation", day)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		handle := requireAgentHandle(t, testAgentWithHandle("work:project:crew-1", llmtypes.CodingProviderSessionHandle{Provider: "claude-code", WorkingDir: runtimeDir, NativeSessionID: native}))
		payload, err := json.Marshal(map[string]interface{}{"session_id": "work:project:crew-1", "runtime": &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: "claude-code", ResumeSupported: true, ExternalSessionID: native, AgentProfileKey: "profile-sha256:before-deploy", AgentSessionHandle: handle}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "session-work:project:crew-1-conversation.json"), payload, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("2026-09-23", "native-yesterday")

	for _, tc := range []struct{ name, want string }{
		{"only yesterday's folder exists", "native-yesterday"},
		{"today's folder exists", "native-today"},
	} {
		if tc.want == "native-today" {
			write("2026-09-24", "native-today")
		}
		api := &StreamingAPI{lastAgentProfileKeyBySession: map[string]string{"work:project:crew-1": "profile-sha256:after-deploy"}}
		ag := testAgentWithHandle("work:project:crew-1", llmtypes.CodingProviderSessionHandle{Provider: "claude-code", WorkingDir: runtimeDir})
		// handleQuery resolves the Crew's SelectedFolder (logical form from the
		// client) to its physical workspace for the native lookup.
		workspace := productConversationRuntimeWorkspace("user-1", "Chats/Work/projects/latency")
		if workspace != "_users/user-1/Chats/Work/projects/latency" {
			t.Fatalf("runtime workspace = %q", workspace)
		}
		seeded, runtime := api.seedCodingAgentRuntimeFromCurrentConversation("work:project:crew-1", "user-1", "claude-code", "", workspace, ag)
		if !seeded || runtime == nil {
			t.Fatalf("%s: changed definition must resume the Crew's native session", tc.name)
		}
		if handle := mcpagent.SnapshotAgentSession(ag); handle == nil || handle.Provider.NativeSessionID != tc.want {
			t.Fatalf("%s: native session = %+v, want %s", tc.name, handle, tc.want)
		}
	}
}

func TestProductConversationRuntimeWorkspaceKeepsOwnerPaths(t *testing.T) {
	if got := productConversationRuntimeWorkspace("reader", "_users/owner/Chats/Work/projects/x/"); got != "_users/owner/Chats/Work/projects/x" {
		t.Fatalf("owner-qualified path changed: %q", got)
	}
	if got := productConversationRuntimeWorkspace("u", ""); got != "" {
		t.Fatalf("empty folder = %q", got)
	}
}

// The query path used to skip every native-seed attempt whenever the product
// definition changed (productDefinitionRefreshed), so the resume rule in
// seedCodingAgentRuntimeFromRestoredConversation was never reached. Only a
// Builder <-> Run role change may bypass seeding.
func TestQueryPathDoesNotGateNativeResumeOnDefinitionRefresh(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"modeChangedThisTurn || productDefinitionRefreshed",
		"!modeChangedThisTurn && !productDefinitionRefreshed",
		"!modeChangedThisTurn && !productDefinitionRefreshed && restoredRuntime == nil",
	} {
		if strings.Contains(string(src), forbidden) {
			t.Fatalf("server.go gates native resume on a definition refresh again: %q", forbidden)
		}
	}
}
