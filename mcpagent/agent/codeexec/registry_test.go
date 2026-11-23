package codeexec

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/mcpclient"
)

func resetRegistryForTest(t *testing.T) {
	t.Helper()

	registryMu.Lock()
	previous := globalRegistry
	globalRegistry = nil
	registryMu.Unlock()

	t.Cleanup(func() {
		registryMu.Lock()
		globalRegistry = previous
		registryMu.Unlock()
	})
}

func TestCallVirtualToolWithSessionFallsBackFromStaleEmptyDiscoveryScope(t *testing.T) {
	resetRegistryForTest(t)

	const (
		baseScope   = "builder-session"
		staleScope  = baseScope + ":vt:restored-terminal"
		latestScope = baseScope + ":vt:chat-agent"
	)
	staleCalls := 0
	latestCalls := 0

	InitRegistryVirtualToolsForSession(staleScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			staleCalls++
			return "", fmt.Errorf("server \"planner\" is not available. Available servers/categories: []. Use get_api_spec(server_name=\"<server>\", tool_name=\"<tool>\") with one of these server names")
		},
	}, nil)
	InitRegistryVirtualToolsForSession(latestScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			latestCalls++
			return "latest-spec", nil
		},
	}, nil)

	got, err := CallVirtualToolWithSession(context.Background(), staleScope, "get_api_spec", nil)
	if err != nil {
		t.Fatalf("CallVirtualToolWithSession() error = %v", err)
	}
	if got != "latest-spec" {
		t.Fatalf("CallVirtualToolWithSession() = %q, want latest-spec", got)
	}
	if staleCalls != 1 || latestCalls != 1 {
		t.Fatalf("calls stale=%d latest=%d, want 1/1", staleCalls, latestCalls)
	}
}

func TestCallVirtualToolWithSessionUsesLatestScopeForRawSession(t *testing.T) {
	resetRegistryForTest(t)

	const (
		baseScope   = "builder-session"
		latestScope = baseScope + ":vt:chat-agent"
	)

	InitRegistryVirtualToolsForSession(latestScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			return "latest-spec", nil
		},
	}, nil)

	got, err := CallVirtualToolWithSession(context.Background(), baseScope, "get_api_spec", nil)
	if err != nil {
		t.Fatalf("CallVirtualToolWithSession() error = %v", err)
	}
	if got != "latest-spec" {
		t.Fatalf("CallVirtualToolWithSession() = %q, want latest-spec", got)
	}
}

func TestCallVirtualToolWithSessionKeepsScopedNonEmptyDiscoveryErrors(t *testing.T) {
	resetRegistryForTest(t)

	const (
		baseScope   = "builder-session"
		staleScope  = baseScope + ":vt:child-agent"
		latestScope = baseScope + ":vt:chat-agent"
	)
	latestCalls := 0

	InitRegistryVirtualToolsForSession(staleScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			return "", fmt.Errorf("server \"planner\" is not available. Available servers/categories: [workspace_advanced]")
		},
	}, nil)
	InitRegistryVirtualToolsForSession(latestScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			latestCalls++
			return "latest-spec", nil
		},
	}, nil)

	_, err := CallVirtualToolWithSession(context.Background(), staleScope, "get_api_spec", nil)
	if err == nil {
		t.Fatal("CallVirtualToolWithSession() error = nil, want scoped error")
	}
	if latestCalls != 0 {
		t.Fatalf("latest scope calls = %d, want 0", latestCalls)
	}
}

// TestCallVirtualToolWithSessionFallsBackFromStaleScopeCurrentErrorFormat is
// the fail-before/pass-after regression test for PLAT-177's confirmed
// registry-scope-retry gap: get_api_spec's unavailableToolsError no longer
// produces the legacy "Available servers/categories: []" phrasing this
// retry was originally written against, so a genuinely stale/restored scope
// (the shape latestVirtualScopeBySession exists to recover from) silently
// never retried against the latest scope -- the model just saw the raw
// stale error. This reproduces get_api_spec's actual current wording
// (agent/code_execution_tools.go's unavailableToolsError, healthy-servers
// branch) and confirms the retry now engages.
func TestCallVirtualToolWithSessionFallsBackFromStaleScopeCurrentErrorFormat(t *testing.T) {
	resetRegistryForTest(t)

	const (
		baseScope   = "builder-session"
		staleScope  = baseScope + ":vt:restored-terminal"
		latestScope = baseScope + ":vt:chat-agent"
	)
	staleCalls := 0
	latestCalls := 0

	InitRegistryVirtualToolsForSession(staleScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			staleCalls++
			return "", fmt.Errorf("tools_unavailable: unknown=[delete_schedule]: these names are not registered by any currently connected server. Closest registered name(s): [delete_schedule]. Registered tools for this session: [add_group add_human_input_step validate_plan_change validate_report_html]. Call one of these exactly, or do the work with your own native tools if the capability you wanted is not here.")
		},
	}, nil)
	InitRegistryVirtualToolsForSession(latestScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			latestCalls++
			return "latest-spec", nil
		},
	}, nil)

	got, err := CallVirtualToolWithSession(context.Background(), staleScope, "get_api_spec", nil)
	if err != nil {
		t.Fatalf("CallVirtualToolWithSession() error = %v", err)
	}
	if got != "latest-spec" {
		t.Fatalf("CallVirtualToolWithSession() = %q, want latest-spec", got)
	}
	if staleCalls != 1 || latestCalls != 1 {
		t.Fatalf("calls stale=%d latest=%d, want 1/1", staleCalls, latestCalls)
	}
}

// TestCallVirtualToolWithSessionKeepsRealOutageErrorsInCurrentFormat mirrors
// TestCallVirtualToolWithSessionKeepsScopedNonEmptyDiscoveryErrors for the
// current error format: unavailableToolsError's server_unavailable branch
// explicitly states retrying with a different scope will not help (the
// server itself is down), so it must not trigger the scope retry even
// though it also starts with "tools_unavailable:" and a matching latest
// scope exists.
func TestCallVirtualToolWithSessionKeepsRealOutageErrorsInCurrentFormat(t *testing.T) {
	resetRegistryForTest(t)

	const (
		baseScope   = "builder-session"
		staleScope  = baseScope + ":vt:child-agent"
		latestScope = baseScope + ":vt:chat-agent"
	)
	latestCalls := 0

	InitRegistryVirtualToolsForSession(staleScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			return "", fmt.Errorf("tools_unavailable: server_unavailable=[google_sheets] requested=[batch_update]: MCP server(s) [google_sheets] are configured for this agent but currently have zero registered tools, so it failed to start. The requested tool names are most likely correct — retrying with different or guessed tool names will NOT help. Treat this as a server outage: report it and continue without those tools.")
		},
	}, nil)
	InitRegistryVirtualToolsForSession(latestScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			latestCalls++
			return "latest-spec", nil
		},
	}, nil)

	_, err := CallVirtualToolWithSession(context.Background(), staleScope, "get_api_spec", nil)
	if err == nil {
		t.Fatal("CallVirtualToolWithSession() error = nil, want scoped error")
	}
	if latestCalls != 0 {
		t.Fatalf("latest scope calls = %d, want 0 (a real server outage must not be masked by retrying a different scope)", latestCalls)
	}
}

func TestCallCustomToolWithSessionDoesNotBorrowGlobalExecutor(t *testing.T) {
	resetRegistryForTest(t)

	globalCalls := 0
	InitRegistry(nil, map[string]func(context.Context, map[string]interface{}) (string, error){
		"call_generic_agent": func(context.Context, map[string]interface{}) (string, error) {
			globalCalls++
			return "wrong-workflow", nil
		},
	}, nil, nil)
	InitRegistryForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) {
			return "ok", nil
		},
	}, nil)

	_, err := CallCustomToolWithSession(context.Background(), "workflow-a", "call_generic_agent", nil)
	if err == nil {
		t.Fatal("CallCustomToolWithSession() error = nil, want missing session tool error")
	}
	// PLAT-746-873: an unavailable bridge tool must explain discovery, not invite retries.
	if !strings.Contains(err.Error(), "search_tools -> get_api_spec -> call_tool") {
		t.Fatalf("missing session tool error has no discovery guidance: %v", err)
	}
	if globalCalls != 0 {
		t.Fatalf("global executor calls = %d, want 0", globalCalls)
	}
}

// A scripted workflow step calls tools through a session-group-* bridge child
// that has no registry of its own. It must reach its parent run's executor, not
// whichever run last wrote the global map (a concurrent schedule, 2026-09-23:
// "query_workflow_db caller does not own this tool session").
func TestCallCustomToolWithSessionUsesRegisteredParentRegistry(t *testing.T) {
	resetRegistryForTest(t)

	parent := "http-parent-" + t.Name()
	child := "session-group-default-" + t.Name()
	lookalike := "session-group-other-" + t.Name()
	sessions := mcpclient.GetSessionRegistry()
	sessions.RegisterHTTPSession(parent, child)
	t.Cleanup(func() { sessions.CloseHTTPSession(parent) })

	InitRegistry(nil, map[string]func(context.Context, map[string]interface{}) (string, error){
		"query_workflow_db": func(context.Context, map[string]interface{}) (string, error) {
			return "other-run", nil
		},
	}, nil, nil)
	InitRegistryForSession(parent, map[string]func(context.Context, map[string]interface{}) (string, error){
		"query_workflow_db": func(context.Context, map[string]interface{}) (string, error) {
			return "parent-run", nil
		},
	}, nil)

	got, err := CallCustomToolWithSession(context.Background(), child, "query_workflow_db", nil)
	if err != nil {
		t.Fatalf("child call error = %v", err)
	}
	if got != "parent-run" {
		t.Fatalf("child call = %q, want parent-run", got)
	}

	// An unregistered child keeps the legacy path; it is not adopted by name.
	got, err = CallCustomToolWithSession(context.Background(), lookalike, "query_workflow_db", nil)
	if err != nil {
		t.Fatalf("lookalike call error = %v", err)
	}
	if got != "other-run" {
		t.Fatalf("lookalike call = %q, want legacy other-run", got)
	}
}

// Resolving a child to its parent must never make a call fail that used to
// resolve: a tool the parent never registered keeps the legacy lookup, and the
// parent chat's per-turn allow list does not govern the step script.
func TestCallCustomToolWithSessionParentResolutionIsConservative(t *testing.T) {
	resetRegistryForTest(t)

	parent := "http-parent-" + t.Name()
	child := "session-group-default-" + t.Name()
	sessions := mcpclient.GetSessionRegistry()
	sessions.RegisterHTTPSession(parent, child)
	t.Cleanup(func() { sessions.CloseHTTPSession(parent) })

	InitRegistry(nil, map[string]func(context.Context, map[string]interface{}) (string, error){
		"record_goal_observations": func(context.Context, map[string]interface{}) (string, error) {
			return "legacy", nil
		},
	}, nil, nil)
	InitRegistryForSession(parent, map[string]func(context.Context, map[string]interface{}) (string, error){
		"query_workflow_db": func(context.Context, map[string]interface{}) (string, error) {
			return "parent-run", nil
		},
	}, nil)
	// The parent chat's turn policy allows nothing the step script calls.
	SetSessionToolAllowList(parent, map[string]bool{"read_skill": true})

	got, err := CallCustomToolWithSession(context.Background(), child, "record_goal_observations", nil)
	if err != nil || got != "legacy" {
		t.Fatalf("tool missing from parent = %q, %v; want legacy lookup", got, err)
	}
	got, err = CallCustomToolWithSession(context.Background(), child, "query_workflow_db", nil)
	if err != nil || got != "parent-run" {
		t.Fatalf("parent allow list applied to child = %q, %v; want parent-run", got, err)
	}
}

func TestCallCustomToolWithSessionKeepsLegacyFallbackWithoutSessionRegistry(t *testing.T) {
	resetRegistryForTest(t)

	InitRegistry(nil, map[string]func(context.Context, map[string]interface{}) (string, error){
		"legacy_tool": func(context.Context, map[string]interface{}) (string, error) {
			return "legacy-result", nil
		},
	}, nil, nil)

	got, err := CallCustomToolWithSession(context.Background(), "uninitialized-session", "legacy_tool", nil)
	if err != nil {
		t.Fatalf("CallCustomToolWithSession() error = %v", err)
	}
	if got != "legacy-result" {
		t.Fatalf("CallCustomToolWithSession() = %q, want legacy-result", got)
	}
}

// The session tool allow list is how a per-turn ToolPolicy reaches the HTTP
// bridge. Direct tool calls are gated in-process by isToolAllowedForContext,
// but a code-executing agent can also reach a tool over HTTP, and that path is
// gated only here. Without these tests the bridge could silently stop enforcing
// the policy while every other test kept passing.
func TestCallCustomToolWithSessionBlocksToolOutsideAllowList(t *testing.T) {
	resetRegistryForTest(t)

	calls := 0
	InitRegistryForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) {
			calls++
			return "should-not-run", nil
		},
	}, nil)
	SetSessionToolAllowList("workflow-a", map[string]bool{"read_skill": true})

	_, err := CallCustomToolWithSession(context.Background(), "workflow-a", "execute_shell_command", nil)
	if err == nil {
		t.Fatal("CallCustomToolWithSession() error = nil, want allow-list rejection")
	}
	if calls != 0 {
		t.Fatalf("blocked executor ran %d times, want 0", calls)
	}
}

func TestCallCustomToolWithSessionPermitsToolInsideAllowList(t *testing.T) {
	resetRegistryForTest(t)

	InitRegistryForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) {
			return "ran", nil
		},
	}, nil)
	SetSessionToolAllowList("workflow-a", map[string]bool{"execute_shell_command": true})

	got, err := CallCustomToolWithSession(context.Background(), "workflow-a", "execute_shell_command", nil)
	if err != nil {
		t.Fatalf("CallCustomToolWithSession() error = %v", err)
	}
	if got != "ran" {
		t.Fatalf("CallCustomToolWithSession() = %q, want ran", got)
	}
}

// A nil allow list means "no restriction", not "block everything". Turn.Run
// passes nil whenever ToolPolicy.AllowedTools is empty, so inverting this would
// break every unrestricted turn rather than fail closed on one.
func TestCallCustomToolWithSessionTreatsNilAllowListAsUnrestricted(t *testing.T) {
	resetRegistryForTest(t)

	InitRegistryForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) {
			return "ran", nil
		},
	}, nil)
	SetSessionToolAllowList("workflow-a", nil)

	got, err := CallCustomToolWithSession(context.Background(), "workflow-a", "execute_shell_command", nil)
	if err != nil {
		t.Fatalf("CallCustomToolWithSession() error = %v", err)
	}
	if got != "ran" {
		t.Fatalf("CallCustomToolWithSession() = %q, want ran", got)
	}
}

// One session's allow list must not gate another's. Concurrent workflows share
// one process-wide registry, so a leak here would let a restricted workshop
// turn silently constrain an unrelated run.
func TestSessionToolAllowListDoesNotLeakAcrossSessions(t *testing.T) {
	resetRegistryForTest(t)

	executor := map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) {
			return "ran", nil
		},
	}
	InitRegistryForSession("restricted", executor, nil)
	InitRegistryForSession("unrestricted", executor, nil)
	SetSessionToolAllowList("restricted", map[string]bool{"read_skill": true})

	if _, err := CallCustomToolWithSession(context.Background(), "restricted", "execute_shell_command", nil); err == nil {
		t.Fatal("restricted session: error = nil, want allow-list rejection")
	}
	got, err := CallCustomToolWithSession(context.Background(), "unrestricted", "execute_shell_command", nil)
	if err != nil {
		t.Fatalf("unrestricted session: error = %v", err)
	}
	if got != "ran" {
		t.Fatalf("unrestricted session = %q, want ran", got)
	}
}

// A reserved tool such as get_api_spec lives in the session's VIRTUAL partition.
// The model addresses every tool by one name and cannot know which endpoint owns
// it, so a bridge routing get_api_spec to the custom endpoint used to fail with
// "custom tool get_api_spec is not registered for session ..." even though that
// exact session had it registered.
func TestCallCustomToolWithSessionResolvesReservedVirtualTools(t *testing.T) {
	resetRegistryForTest(t)

	InitRegistryForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) { return "shell", nil },
	}, nil)
	InitRegistryVirtualToolsForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) { return "spec", nil },
	}, nil)

	got, err := CallCustomToolWithSession(context.Background(), "workflow-a", "get_api_spec", nil)
	if err != nil {
		t.Fatalf("CallCustomToolWithSession(get_api_spec) error = %v", err)
	}
	if got != "spec" {
		t.Fatalf("CallCustomToolWithSession(get_api_spec) = %q, want spec", got)
	}
}

// The virtual fallback is scoped to the same session. It must not become a route
// into another session's tools.
func TestVirtualFallbackDoesNotCrossSessions(t *testing.T) {
	resetRegistryForTest(t)

	InitRegistryForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) { return "shell", nil },
	}, nil)
	InitRegistryVirtualToolsForSession("workflow-b", map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) { return "other-session", nil },
	}, nil)

	if _, err := CallCustomToolWithSession(context.Background(), "workflow-a", "get_api_spec", nil); err == nil {
		t.Fatal("session workflow-a borrowed workflow-b's virtual tool")
	}
}

// A denial must not name a cause the registry cannot know. The old wording blamed
// "the current workshop mode" — a condition the model believes it can change — so
// a Pulse Fixer denied update_schedule concluded its session was in the wrong mode
// and abandoned the edit instead of proceeding without the tool.
func TestToolNotAllowedErrorDoesNotInventACause(t *testing.T) {
	err := toolNotAllowedError("update_schedule", map[string]bool{
		"query_workflow_db":  true,
		"update_step_config": true,
	}, true)
	msg := err.Error()

	for _, forbidden := range []string{"workshop mode", "workshop"} {
		if strings.Contains(strings.ToLower(msg), forbidden) {
			t.Errorf("denial names %q, a cause this registry cannot establish: %s", forbidden, msg)
		}
	}
	if !strings.Contains(msg, "update_schedule") {
		t.Errorf("denial does not name the tool: %s", msg)
	}
	if !strings.Contains(msg, "NOT") {
		t.Errorf("denial does not tell the agent retrying is futile: %s", msg)
	}
	// The allowed surface is what makes the correction actionable.
	if !strings.Contains(msg, "query_workflow_db") || !strings.Contains(msg, "update_step_config") {
		t.Errorf("denial does not name what is available instead: %s", msg)
	}
}

func TestToolNotAllowedErrorCapsTheAllowedList(t *testing.T) {
	allowed := make(map[string]bool, allowedToolNamesInError*2)
	for i := 0; i < allowedToolNamesInError*2; i++ {
		allowed[fmt.Sprintf("tool_%03d", i)] = true
	}
	msg := toolNotAllowedError("missing_tool", allowed, true).Error()
	if !strings.Contains(msg, fmt.Sprintf("%d of %d", allowedToolNamesInError, allowedToolNamesInError*2)) {
		t.Errorf("oversized allow list not summarised: %s", msg)
	}
	if strings.Count(msg, "tool_0") != allowedToolNamesInError {
		t.Errorf("listed %d names, want %d: %s", strings.Count(msg, "tool_0"), allowedToolNamesInError, msg)
	}
}

// An empty allow list is a distinct condition and must read as one.
func TestToolNotAllowedErrorHandlesAnEmptySurface(t *testing.T) {
	msg := toolNotAllowedError("anything", nil, true).Error()
	if !strings.Contains(msg, "no tools allowed at all") {
		t.Errorf("empty surface not reported: %s", msg)
	}
}

// A name that exists nowhere must not read as a permissions problem. After the
// Pulse surface was consolidated from eight tools to four on 2026-08-03, a Gate
// session invented six names derived from a removed tool and retried each one,
// because "not allowed" invites trying a variant.
func TestUnknownToolNameReadsAsGoneNotWithheld(t *testing.T) {
	msg := toolNotAllowedError("mark_pulse_final_command_result", map[string]bool{
		"record_pulse_result": true,
	}, false).Error()

	if !strings.HasPrefix(msg, "tool_not_found:") {
		t.Errorf("unknown name not distinguished from a denial: %s", msg)
	}
	if !strings.Contains(msg, "Guessing a variant of this name will NOT work") {
		t.Errorf("message does not close off the variant-guessing loop: %s", msg)
	}
	if !strings.Contains(msg, "record_pulse_result") {
		t.Errorf("message does not name the surface that does exist: %s", msg)
	}
}

// A registered-but-withheld tool keeps the permissions wording.
func TestRegisteredToolStillReadsAsADenial(t *testing.T) {
	msg := toolNotAllowedError("update_schedule", map[string]bool{"query_workflow_db": true}, true).Error()
	if !strings.HasPrefix(msg, "tool_not_allowed:") {
		t.Errorf("withheld tool lost its denial wording: %s", msg)
	}
	if strings.Contains(msg, "does not exist") {
		t.Errorf("withheld tool wrongly reported as missing: %s", msg)
	}
}

// PLAT-514: a webhook run's tools are registered by its workflow session, a sibling of the scripted step's bridge
// session, not by the HTTP run id. The step must reach that sibling, never the global executor another session (a Crew
// chat running at the same moment) registered last.
func TestCallCustomToolWithSessionReachesTheRunsSiblingNotTheGlobalExecutor(t *testing.T) {
	resetRegistryForTest(t)

	parent := "http-run-" + t.Name()
	step := "session-group-default-" + t.Name()
	workflow := "workflow-session-" + t.Name()
	sessions := mcpclient.GetSessionRegistry()
	sessions.RegisterHTTPSession(parent, step)
	sessions.RegisterHTTPSession(parent, workflow)
	t.Cleanup(func() { sessions.CloseHTTPSession(parent) })

	InitRegistry(nil, map[string]func(context.Context, map[string]interface{}) (string, error){
		"mutate_workflow_db": func(context.Context, map[string]interface{}) (string, error) { return "crew-chat", nil },
	}, nil, nil)
	InitRegistryForSession(workflow, map[string]func(context.Context, map[string]interface{}) (string, error){
		"mutate_workflow_db": func(context.Context, map[string]interface{}) (string, error) { return "this-run", nil },
	}, nil)

	got, err := CallCustomToolWithSession(context.Background(), step, "mutate_workflow_db", nil)
	if err != nil || got != "this-run" {
		t.Fatalf("step call = %q, %v; want this-run (the run's own session, not the global executor)", got, err)
	}

	// Two siblings with the tool is ambiguous: keep the legacy lookup instead of guessing.
	other := "workflow-session-b-" + t.Name()
	sessions.RegisterHTTPSession(parent, other)
	InitRegistryForSession(other, map[string]func(context.Context, map[string]interface{}) (string, error){
		"mutate_workflow_db": func(context.Context, map[string]interface{}) (string, error) { return "other", nil },
	}, nil)
	if got, _ := CallCustomToolWithSession(context.Background(), step, "mutate_workflow_db", nil); got != "crew-chat" {
		t.Fatalf("ambiguous siblings call = %q, want the legacy global lookup", got)
	}
}
