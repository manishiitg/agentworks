package mcpagent

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/mcpagent/mcpclient"
)

// On 2026-08-01 a run asked google_sheets for five tool names that were all
// spelled correctly while that server was failing to connect (transport closed,
// zero tools registered). It was told "unknown=[batch_update
// get_sheet_formulas get_sheet_values get_spreadsheet_info
// update_sheet_values]", read that as a naming error, and came back ninety
// seconds later with invented names (list_sheets, get_sheet_data). No name
// could have resolved, so the error must not describe the names at all.
func TestGetAPISpecBlamesTheServerWhenItRegisteredNoTools(t *testing.T) {
	agent := &Agent{
		serverName: "google_sheets",
		toolFilter: NewToolFilter(nil, nil, nil, nil, nil),
	}

	_, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"server_name": "google_sheets",
		"tool_name": []interface{}{
			"get_spreadsheet_info", "get_sheet_values", "get_sheet_formulas",
			"batch_update", "update_sheet_values",
		},
	})
	if err == nil {
		t.Fatal("a request against a server with no registered tools succeeded")
	}

	message := err.Error()
	if strings.Contains(message, "unknown=") {
		t.Fatalf("error still calls the correct names unknown, which is what caused the guessing loop:\n%s", message)
	}
	for _, want := range []string{"server_unavailable=", "google_sheets", "will NOT help"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error is missing %q:\n%s", want, message)
		}
	}
	if !strings.Contains(message, "failed to start or failed to connect") {
		t.Fatalf("error does not attribute the failure to the server:\n%s", message)
	}
}

// The same outage with no server_name supplied is still unambiguous when not a
// single MCP server registered a tool: there is no MCP name that could resolve,
// so the outage can be asserted rather than merely mentioned.
func TestGetAPISpecInfersTheOutageWhenNoMCPServerRegisteredAnything(t *testing.T) {
	agent := &Agent{
		selectedServers: []string{"google-sheets"},
		toolFilter:      NewToolFilter(nil, nil, nil, nil, nil),
	}

	_, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"tool_name": "get_sheet_values",
	})
	if err == nil {
		t.Fatal("a request with every MCP server down succeeded")
	}
	message := err.Error()
	if !strings.Contains(message, "server_unavailable=") || !strings.Contains(message, "google-sheets") {
		t.Fatalf("an outage with no surviving MCP server was not attributed to the server:\n%s", message)
	}
}

// A misspelling against a healthy server must stay a misspelling. Blaming a
// server here would teach the model to retry an unfixable call forever, which
// is the mirror image of the original bug.
func TestGetAPISpecKeepsUnknownWhenTheServersAreHealthy(t *testing.T) {
	agent := &Agent{
		serverName:   "github-server",
		tools:        []llmtypes.Tool{toolFixture("search_issues")},
		toolToServer: map[string]string{"search_issues": "github-server"},
		toolFilter:   NewToolFilter(nil, nil, nil, nil, nil),
	}

	_, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"server_name": "github-server",
		"tool_name":   "serch_issues",
	})
	if err == nil {
		t.Fatal("a misspelled tool name against a healthy server succeeded")
	}

	message := err.Error()
	if !strings.Contains(message, "unknown=[serch_issues]") {
		t.Fatalf("a genuinely unknown name lost its unknown= attribution:\n%s", message)
	}
	if strings.Contains(message, "server_unavailable=") {
		t.Fatalf("a healthy server was blamed for a misspelled name:\n%s", message)
	}
	// Every server is healthy, so the name is simply wrong — and the reply names
	// the alternatives instead of telling the model to go re-read its system
	// prompt. It used to say "use the exact tool names from the tool index",
	// which cost a turn to act on when the answer was already in hand here.
	if !strings.Contains(message, "search_issues") {
		t.Fatalf("error does not name the registered tools the model could call instead:\n%s", message)
	}
}

// One healthy server and one dead one is the case that cannot be decided: the
// name may be invented or it may belong to the server that never came up. The
// error has to carry both readings, because asserting either one alone sends
// the model down a loop it cannot exit.
func TestGetAPISpecMentionsTheDeadServerAlongsideAnUnknownName(t *testing.T) {
	agent := &Agent{
		serverName:   "github-server,google_sheets",
		tools:        []llmtypes.Tool{toolFixture("search_issues")},
		toolToServer: map[string]string{"search_issues": "github-server"},
		toolFilter:   NewToolFilter(nil, nil, nil, nil, nil),
	}

	_, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"tool_name": []interface{}{"search_issues", "get_sheet_values"},
	})
	if err == nil {
		t.Fatal("a mixed request resolved despite an unresolvable name")
	}

	message := err.Error()
	if !strings.Contains(message, "unknown=[get_sheet_values]") {
		t.Fatalf("the unresolvable name is not reported:\n%s", message)
	}
	if strings.Contains(message, "search_issues") {
		t.Fatalf("a resolvable name was reported as a failure:\n%s", message)
	}
	if !strings.Contains(message, "google_sheets") || !strings.Contains(message, "no tool name will work") {
		t.Fatalf("the configured-but-empty server is not offered as an explanation:\n%s", message)
	}
}

// Permission denials were already attributed correctly. Their wording is a
// separate contract and must survive this change byte for byte.
func TestGetAPISpecLeavesPermissionDenialsUnchanged(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(
			directToolFixture("get_pulse_module_state", "workflow"),
			directToolFixture("mark_pulse_final_command_result", "workflow"),
		),
		toolFilter:    NewToolFilter(nil, nil, nil, []string{"workflow"}, nil),
		toolAllowList: map[string]bool{"get_pulse_module_state": true},
	}

	_, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"tool_name": "mark_pulse_final_command_result",
	})
	if err == nil {
		t.Fatal("a denied tool returned a spec")
	}
	const want = "tools_unavailable: unknown=[] not_allowed=[mark_pulse_final_command_result]"
	if err.Error() != want {
		t.Fatalf("permission denial wording changed:\ngot  %s\nwant %s", err.Error(), want)
	}
}

// A server that connects and advertises nothing is still a server problem, but
// telling an operator it "failed to start" points debugging at the wrong place.
func TestGetAPISpecDistinguishesConnectedButEmptyFromNeverConnected(t *testing.T) {
	agent := &Agent{
		serverName: "google_sheets",
		clients:    map[string]mcpclient.ClientInterface{"google_sheets": nil},
		toolFilter: NewToolFilter(nil, nil, nil, nil, nil),
	}

	_, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"server_name": "google_sheets",
		"tool_name":   "get_sheet_values",
	})
	if err == nil {
		t.Fatal("a connected server with no tools returned a spec")
	}
	if !strings.Contains(err.Error(), "connected but advertised no tools") {
		t.Fatalf("a live-but-empty server was described as a startup failure:\n%s", err.Error())
	}
}

// A coding CLI may serialize the array before sending it, so tool_name arrives
// as the literal string `["a","b"]`. Treating that as a single tool name
// produced this in production:
//
//	unknown=[["query_workflow_db", "mutate_workflow_db"]]
//
// a name no registry could ever hold, which then read as "your names are wrong".
func TestGetAPISpecAcceptsAStringifiedToolNameArray(t *testing.T) {
	for _, encoded := range []string{
		`["query_workflow_db", "mutate_workflow_db"]`,
		`["query_workflow_db"]`,
		`  ["query_workflow_db","mutate_workflow_db"]  `,
	} {
		names, ok := decodeJSONStringArray(encoded)
		if !ok {
			t.Fatalf("decodeJSONStringArray(%q) = not an array, want decoded", encoded)
		}
		if len(names) == 0 || names[0] != "query_workflow_db" {
			t.Fatalf("decodeJSONStringArray(%q) = %v", encoded, names)
		}
	}
}

// A plain tool name must stay a plain tool name, including odd-looking ones.
func TestGetAPISpecLeavesPlainToolNamesAlone(t *testing.T) {
	for _, plain := range []string{
		"query_workflow_db", "", "[not json", "[]", `["", "  "]`, `[1,2]`, "search_issues[0]",
	} {
		if names, ok := decodeJSONStringArray(plain); ok {
			t.Fatalf("decodeJSONStringArray(%q) = %v, want treated as a plain name", plain, names)
		}
	}
}
