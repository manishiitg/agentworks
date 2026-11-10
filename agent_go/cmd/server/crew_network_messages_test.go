package server

import (
	"encoding/json"
	"testing"
)

// PLAT-746: a read's advertised reply address must round-trip through the
// network routing branch, not fall through to a nonexistent local mailbox.
func TestCrewNetworkReadQualifiesNestedReplyAddresses(t *testing.T) {
	var out map[string]any
	if err := json.Unmarshal([]byte(`{"inbox_id":"inbox-top","messages":[{"inbox_id":"inbox-one","reply_address":"inbox-one","conversation_id":"inbox-one","message":"reply to inbox-one"},{"inbox_id":"network:other:inbox-two","reply_address":"network:other:inbox-two"}]}`), &out); err != nil {
		t.Fatal(err)
	}
	qualifyCrewNetworkReplyAddresses(out, "crew")
	qualifyCrewNetworkReplyAddresses(out, "crew")
	if out["inbox_id"] != "network:crew:inbox-top" {
		t.Fatalf("top inbox = %v", out["inbox_id"])
	}
	rows := out["messages"].([]any)
	row := rows[0].(map[string]any)
	for _, key := range []string{"inbox_id", "reply_address"} {
		project, inbox, ok := splitCrewNetworkAddress(row[key].(string))
		if !ok || project != "crew" || inbox != "inbox-one" {
			t.Fatalf("%s does not route back to the same network: %v", key, row[key])
		}
	}
	if row["conversation_id"] != "inbox-one" || row["message"] != "reply to inbox-one" {
		t.Fatalf("non-address data changed: %v", row)
	}
	qualified := rows[1].(map[string]any)
	if qualified["inbox_id"] != "network:other:inbox-two" || qualified["reply_address"] != "network:other:inbox-two" {
		t.Fatalf("already qualified address changed: %v", qualified)
	}
}
