package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// Every merged tool is built from tools that exist, hides them from the menu
// (they stay callable), and refuses an action it does not have.
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
		case "suggest_change":
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
	if member, _, err := externalResolveMerged(suggest, map[string]any{"crew_id": "c"}); err != nil || member.Name != "suggest_crew_change" {
		t.Fatalf("one-action tool: %s %v", member.Name, err)
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
// the call used to be refused with "additional properties 'crew_id', 'wait_seconds' not allowed" (Citymall acceptance run, 2026-10-09).
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
	if err != nil || member.Name != "get_crew_function_call" {
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
}
