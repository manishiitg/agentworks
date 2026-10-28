package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// 2026-10-28 (PLAT-856-397): an in-app Goal agent asked for retired member
// names and an MCP-only help action, and the errors gave it nothing to move to.
func TestToolErrorsNameTheShapeThatWorksInChat(t *testing.T) {
	for _, tc := range []struct{ tool, err, want string }{
		{"get_api_spec", "tools_unavailable: unknown=[run_full_workflow]: these names are not registered by any currently connected server.", "run_full_workflow is now `runs action=start`."},
		{"get_api_spec", "tools_unavailable: unknown=[execute_step send_message]: these names are not registered", "execute_step is now `runs action=step`. send_message is now `messages action=send`."},
		{"help", `invalid arguments for tool "help": unknown field(s): path, topic; field "action" must be one of [command].`, "read_skill"},
	} {
		body, _ := json.Marshal(map[string]interface{}{"success": false, "error": tc.err})
		var out struct{ Error string }
		if err := json.Unmarshal(annotateToolNameHints(body, tc.tool), &out); err != nil || !strings.Contains(out.Error, tc.want) || !strings.HasPrefix(out.Error, tc.err) {
			t.Errorf("%s: got %q, want it to keep the error and add %q", tc.tool, out.Error, tc.want)
		}
	}
	// A name that never was a member, or a family name, gets no invented hint.
	body := []byte(`{"success":false,"error":"tools_unavailable: unknown=[runs made_up_tool]: x"}`)
	if got := string(annotateToolNameHints(body, "get_api_spec")); got != string(body) {
		t.Fatalf("unexpected hint: %s", got)
	}
}
