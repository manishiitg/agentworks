package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

// Reproduce the nested workspace shell -> MCP executor -> Slack diagnostic
// envelopes. Failed calls must retain their failure marker AND their details.
func TestShellBridgePreservesFailedToolDiagnostics(t *testing.T) {
	diagnostic := `{"success":false,"message":"Slack setup checks failed","checks":[{"name":"app_mentions:read","status":"missing","message":"Add this bot scope and reinstall the Slack app"}]}`
	apiBody, _ := json.Marshal(map[string]interface{}{"success": false, "error": "tool execution failed: success=false", "result": diagnostic})
	shellBody, _ := json.Marshal(map[string]interface{}{"data": map[string]interface{}{"stdout": string(apiBody), "stderr": "", "exit_code": 0}})
	got := parseShellResponse(shellBody)
	if !strings.HasPrefix(got.Stdout, "ERROR: tool execution failed:") {
		t.Fatalf("failure marker lost: %s", got.Stdout)
	}
	parts := strings.SplitN(got.Stdout, "\n\n", 2)
	if len(parts) != 2 || parts[1] != diagnostic {
		t.Fatalf("actionable diagnostic body was discarded: %s", got.Stdout)
	}
	var checks struct {
		Checks []struct{ Name, Status, Message string }
	}
	if err := json.Unmarshal([]byte(parts[1]), &checks); err != nil {
		t.Fatal(err)
	}
	if len(checks.Checks) != 1 || checks.Checks[0].Name != "app_mentions:read" || checks.Checks[0].Status != "missing" {
		t.Fatal("agent cannot identify the missing scope")
	}
}
