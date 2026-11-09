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

// One automations tool replaces schedules, watchers and webhooks (owner, 4 Nov 2026): action and scope reach that
// kind's own member, so its checks still apply; an action several kinds share needs the scope.
func TestExternalAutomationsCallPicksMemberByScope(t *testing.T) {
	tool, ok := externalCatalogTool("automations")
	if !ok {
		t.Fatal("automations tool missing")
	}
	for args, want := range map[string]string{
		`{"action":"list","scope":"schedule","project":"p"}`: "project_schedules_list",
		`{"action":"list","scope":"webhook","project":"p"}`:  "project_webhooks_list",
		`{"action":"list","scope":"watcher","project":"p"}`:  "project_watchers_list",
		`{"action":"run_now","project":"p"}`:                 "project_schedules_run_now",
	} {
		var in map[string]any
		_ = json.Unmarshal([]byte(args), &in)
		member, rest, err := externalResolveMerged(tool, in)
		if err != nil || member.Name != want || rest["action"] != nil || rest["scope"] != nil {
			t.Fatalf("%s resolved to %s %v (%v), want %s", args, member.Name, rest, err, want)
		}
	}
	if _, _, err := externalResolveMerged(tool, map[string]any{"action": "list", "project": "p"}); err == nil || !strings.Contains(err.Error(), "needs scope") {
		t.Fatalf("list without scope: %v", err)
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
	costs, ok := externalCatalogTool("costs")
	if !ok {
		t.Fatal("costs unavailable")
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
			member, _ := externalCatalogTool(name)
			for key := range member.InputSchema["properties"].(map[string]any) {
				if props[key] == nil {
					t.Fatalf("%s lost parameter %s from the merged schema", name, key)
				}
			}
		}
	}
	// Functions are written with define (one top-level definition) and delete (a name); the project-settings
	// patch (update_crew with a nested functions list) is no longer a function action.
	if props["functions"] != nil || props["files"] != nil || props["definition"] == nil {
		t.Fatalf("function schema inherited project fields or lost its definition: %v", props)
	}
	if _, _, err := externalResolveMerged(tool, map[string]any{"action": "update", "crew_id": "c", "functions": map[string]any{"upsert": []any{map[string]any{"name": "summarize"}}}}); err == nil {
		t.Fatal("a nested functions patch must not resolve to any function action")
	}
	member, rest, err := externalResolveMerged(tool, map[string]any{"action": "define", "project": "c", "definition": map[string]any{"name": "summarize", "execution": "tab"}})
	if err != nil || member.Name != "project_functions_define" || rest["definition"] == nil {
		t.Fatalf("flat function dispatch: %s %v %v", member.Name, rest, err)
	}
}
