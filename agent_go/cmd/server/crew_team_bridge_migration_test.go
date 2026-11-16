package server

import (
	"bytes"
	"encoding/json"
	"testing"
)

// #2762: moving the identity out of a transport must neither reassign it nor
// discard unrelated Team state. Conflicting identities require an operator.
func TestTeamBridgeNamedIDMigrationKeepsIdentityAndRejectsConflict(t *testing.T) {
	for _, current := range []string{"", "agent-one", "agent-other"} {
		raw := []byte(`{"project_id":"project","unknown":{"retained":9007199254740993},"agents":{"row":{"id":"row","named_agent_id":"` + current + `","terminal":{"named_agent_id":"agent-one","attachment_id":"lease"},"in_team":false}}}`)
		next, changed, err := convertTeamBridgeNamedID(raw)
		if current == "agent-other" {
			if err == nil {
				t.Fatal("conflicting identity was overwritten")
			}
			continue
		}
		if err != nil || !changed {
			t.Fatalf("conversion refused: %v", err)
		}
		var doc struct {
			Unknown json.RawMessage `json:"unknown"`
			Agents  map[string]struct {
				Named    string                     `json:"named_agent_id"`
				InTeam   bool                       `json:"in_team"`
				Terminal map[string]json.RawMessage `json:"terminal"`
			} `json:"agents"`
		}
		if err = json.Unmarshal(next, &doc); err != nil {
			t.Fatal(err)
		}
		row := doc.Agents["row"]
		if row.Named != "agent-one" || row.InTeam || row.Terminal["named_agent_id"] != nil || string(row.Terminal["attachment_id"]) != `"lease"` || !bytes.Contains(doc.Unknown, []byte("9007199254740993")) {
			t.Fatal("migration changed identity, sharing or unrelated state")
		}
		again, changed, err := convertTeamBridgeNamedID(next)
		if err != nil || changed || !bytes.Equal(again, next) {
			t.Fatal("already converted record was rewritten")
		}
	}
}
