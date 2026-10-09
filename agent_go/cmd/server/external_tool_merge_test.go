package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
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
			member, ok := byName[pair[1]]
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
