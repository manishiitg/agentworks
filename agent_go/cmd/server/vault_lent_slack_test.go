package server

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// A Slack connection lent by its owner through a Crew or Goal: someone else may send, never read or search; the owner
// may do both. The body the proxy forwards is left intact.
func TestLentVaultSlackIsSendOnlyForOthers(t *testing.T) {
	api := &StreamingAPI{}
	in := vaultDelegation{Person: "owner", Connector: "c-slack", Session: "s1"}
	ctx := context.WithValue(context.Background(), vaultInventoryKey{}, vaultInventory{person: "owner", servers: []vaultRuntimeServer{{ID: "c-slack", Provider: "slack"}}})
	call := func(caller, tool string) (string, string) {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"slack_c-slack__` + tool + `","arguments":{}}}`
		r := httptest.NewRequest("POST", "/internal/vault/mcp", strings.NewReader(body)).WithContext(ctx)
		refusal := api.lentVaultSlackRefusal(r, in, caller, "Crew/x")
		forwarded, _ := io.ReadAll(r.Body)
		if string(forwarded) != body {
			t.Fatalf("body not restored for the proxy: %q", forwarded)
		}
		return refusal, string(forwarded)
	}
	if refusal, _ := call("runner", "slack_read_channel"); refusal == "" {
		t.Fatal("someone else read the owner's Slack")
	}
	if refusal, _ := call("runner", "slack_send_message"); refusal != "" {
		t.Fatalf("send refused: %s", refusal)
	}
	if refusal, _ := call("owner", "slack_read_channel"); refusal != "" {
		t.Fatalf("owner refused: %s", refusal)
	}
	batch := `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"slack_c-slack__slack_send_message"}},{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"slack_c-slack__slack_search_public"}}]`
	r := httptest.NewRequest("POST", "/internal/vault/mcp", strings.NewReader(batch)).WithContext(ctx)
	if api.lentVaultSlackRefusal(r, in, "runner", "Crew/x") == "" {
		t.Fatal("a read hidden in a batch got through")
	}
}
