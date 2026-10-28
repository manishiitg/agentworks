package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// Every merged tool is built from tools that exist, hides them from the menu
// (retired names are dispatcher-only), and refuses an action it does not have.
func TestExternalToolMergesAreWellFormed(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]externalTool{}
	for _, tool := range catalog {
		byName[tool.Name] = tool
	}
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	listed := map[string]bool{}
	for _, tool := range externalListedTools(claims, catalog) {
		listed[tool.Name] = true
	}
	for _, merge := range externalToolMerges {
		merged, ok := byName[merge.name]
		if !ok {
			continue // none of its members is admitted on this server
		}
		for _, pair := range merge.actions {
			for _, name := range externalMemberNames(pair[1]) {
				member, ok := byName[name]
				if !ok {
					continue
				}
				if !member.hidden || listed[member.Name] {
					t.Fatalf("%s: member %s is still in the menu", merge.name, member.Name)
				}
				if _, has := member.InputSchema["properties"].(map[string]any)["action"]; has {
					t.Fatalf("%s: member %s has its own action field", merge.name, member.Name)
				}
			}
		}
		if merged.hidden {
			t.Fatalf("%s is hidden", merge.name)
		}
	}
	api := &StreamingAPI{}
	body, _ := json.Marshal(map[string]any{"name": "dashboard", "arguments": map[string]any{"action": "nope"}})
	w := httptest.NewRecorder()
	api.handleExternalCall(w, adminRequest("POST", "/api/external/v1/call", string(body), claims, nil))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "needs action") {
		t.Fatalf("unknown action: %d %s", w.Code, w.Body)
	}
}

// A workflow-or-Crew action runs the Crew's tool when the call names a crew_id,
// and a one-action tool needs no action argument.
func TestExternalMergedToolPicksWorkflowOrCrewMember(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	var files, suggest externalTool
	for _, tool := range catalog {
		switch tool.Name {
		case "files":
			files = tool
		case "needs_you":
			suggest = tool
		}
	}
	for args, want := range map[string]string{`{"action":"list","workflow_id":"w"}`: "list_files", `{"action":"list","crew_id":"c"}`: "list_crew_files", `{"action":"read","crew_id":"c","path":"x"}`: "read_crew_file", `{"action":"write","workflow_id":"w"}`: "write_file"} {
		var in map[string]any
		_ = json.Unmarshal([]byte(args), &in)
		member, rest, err := externalResolveMerged(files, in)
		if err != nil || member.Name != want || rest["action"] != nil {
			t.Fatalf("%s resolved to %s (%v), want %s", args, member.Name, err, want)
		}
	}
	if member, _, err := externalResolveMerged(suggest, map[string]any{"action": "suggest", "crew_id": "c"}); err != nil || member.Name != "suggest_crew_change" {
		t.Fatalf("suggest action: %s %v", member.Name, err)
	}
}

// A read-only connection is shown files without its write action.
func TestExternalMergedFilesAreNarrowedForReaders(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	reader := &UserClaims{UserID: "r", AccessToken: &accesstokens.Token{Scopes: []string{"workflows:read", "files:read"}, AllWorkflows: true}}
	for _, tool := range externalListedTools(reader, catalog) {
		if tool.Name != "relay" {
			continue
		}
		required, _ := tool.InputSchema["required"].([]any)
		found := false
		for _, key := range required {
			if key == "action" {
				found = true
			}
		}
		if !found {
			t.Fatalf("narrowed Relay permits omitted action: %+v", tool.InputSchema)
		}
	}
	for _, tool := range externalListedTools(reader, catalog) {
		if tool.Name != "files" {
			continue
		}
		actions, _ := tool.InputSchema["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]any)
		for _, action := range actions {
			if action == "write" {
				t.Fatalf("a files:read connection is offered write: %v", actions)
			}
		}
		if len(actions) == 0 {
			t.Fatal("no files actions for a files:read connection")
		}
		return
	}
	t.Fatal("files tool not offered to a files:read connection")
}

// functions status for a Crew passes crew_id (to pick the crew member) and wait_seconds, which the crew status tool does not declare;
// the call used to be refused with "additional properties 'crew_id', 'wait_seconds' not allowed" (acceptance run, 2026-10-09).
func TestExternalMergedCallDropsFieldsOnlyAnotherMemberTakes(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	var functions externalTool
	for _, tool := range catalog {
		if tool.Name == "functions" {
			functions = tool
		}
	}
	member, rest, err := externalResolveMerged(functions, map[string]any{"action": "status", "crew_id": "c", "call_id": "fn-1", "wait_seconds": 5})
	if err != nil || member.Name != "project_functions_get_call" {
		t.Fatalf("resolved to %s (%v)", member.Name, err)
	}
	if _, has := rest["crew_id"]; has {
		t.Fatalf("crew_id reached a tool that does not take it: %v", rest)
	}
	if rest["call_id"] != "fn-1" {
		t.Fatalf("call_id lost: %v", rest)
	}
	// A field no member knows is still passed on, to be refused by the member's own validation.
	_, rest, _ = externalResolveMerged(functions, map[string]any{"action": "status", "crew_id": "c", "call_id": "fn-1", "bogus": 1})
	if rest["bogus"] == nil {
		t.Fatalf("an unknown field was silently dropped: %v", rest)
	}
	// PLAT-825-113: a generic Run proxy admits operation parameters such as
	// run_folder. Its aggregate must advertise that pass-through without relaxing a
	// different selected member's strict schema.
	costs, ok := externalCatalogTool("costs")
	if !ok {
		t.Fatal("costs unavailable")
	}
	if err := costs.validator.Validate(map[string]any{"action": "summary", "workflow_id": "w", "run_folder": "runs/iteration-1"}); err != nil {
		t.Fatalf("aggregate hid the admitted proxy's run parameter: %v", err)
	}
	strict, args, err := externalResolveMerged(costs, map[string]any{"action": "breakdown", "bogus": true})
	if err != nil || strict.validator.Validate(args) == nil {
		t.Fatalf("strict selected member accepted an unknown argument: %v", err)
	}
}

// The advertised shape used to include a nested functions patch that dispatch
// silently replaced with an empty definition (PLAT-856-200).
func TestExternalFunctionAuthoringHasOneFlatShape(t *testing.T) {
	tool, ok := externalCatalogTool("functions")
	if !ok {
		t.Fatal("functions missing")
	}
	listed, ok := externalMergedForClaims(&UserClaims{UserID: "owner"}, tool)
	if !ok {
		t.Fatal("functions not listed")
	}
	props := listed.InputSchema["properties"].(map[string]any)
	for _, spec := range tool.actions {
		for _, name := range externalMemberNames(spec) {
			if name == "update_crew" {
				continue
			}
			member, _ := externalCatalogTool(name)
			for key := range member.InputSchema["properties"].(map[string]any) {
				if props[key] == nil {
					t.Fatalf("%s lost parameter %s from the merged schema", name, key)
				}
			}
		}
	}
	if props["functions"] != nil || props["files"] != nil || props["execution"] == nil {
		t.Fatalf("function schema inherited project fields or lost execution: %v", props)
	}
	_, _, err := externalResolveMerged(tool, map[string]any{"action": "update", "crew_id": "c", "functions": map[string]any{"upsert": []any{map[string]any{"name": "summarize"}}}})
	if err == nil || !strings.Contains(err.Error(), "top-level") {
		t.Fatalf("nested patch should get an actionable refusal: %v", err)
	}
	member, rest, err := externalResolveMerged(tool, map[string]any{"action": "update", "crew_id": "c", "name": "summarize", "description": "Summarize input", "instructions": "Summarize the request", "execution": "tab", "agent": "summaries"})
	if err != nil || member.Name != "update_crew" {
		t.Fatalf("flat function dispatch: %s %v", member.Name, err)
	}
	fn := rest["functions"].(map[string]any)["upsert"].([]any)[0].(map[string]any)
	if fn["name"] != "summarize" || fn["execution"] != "tab" || fn["agent"] != "summaries" || rest["name"] != nil || rest["agent"] != nil {
		t.Fatalf("function fields lost or treated as project fields: %v", rest)
	}
}

// Many MCP clients pass tool schemas straight to their model API, which rejects top-level allOf/anyOf/oneOf and
// does not support if/then: one such schema can make a client drop or refuse the whole tool list. Every listed
// external tool, as the full catalog and as an owner's narrowed view, keeps a flat object schema.
func TestListedExternalToolSchemasAreFlat(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	check := func(tool externalTool) {
		for _, key := range []string{"allOf", "anyOf", "oneOf", "if", "then", "else", "not"} {
			if _, found := tool.InputSchema[key]; found {
				t.Errorf("listed tool %s has a top-level %s", tool.Name, key)
			}
		}
	}
	needs := map[string]string{}
	for _, tool := range catalog {
		if tool.hidden {
			continue
		}
		check(tool)
		if action, _ := tool.InputSchema["properties"].(map[string]any)["action"].(map[string]any); action != nil {
			needs[tool.Name], _ = action["description"].(string)
		}
		if tool.actions != nil {
			if narrowed, ok := externalMergedForClaims(owner, tool); ok {
				check(narrowed)
			}
		}
	}
	// The required fields still reach clients, in the action description.
	for tool, want := range map[string]string{"workspace": "create: idempotency_key, kind, spec", "schedules": "run_now: "} {
		if !strings.Contains(needs[tool], want) {
			t.Errorf("%s action description = %q, want %q", tool, needs[tool], want)
		}
	}
}
