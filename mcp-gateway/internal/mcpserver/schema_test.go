package mcpserver

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func TestApprovedInputSchemaValidation(t *testing.T) {
	g := &Gateway{}
	snap := store.ToolSnapshot{
		Fingerprint: "schema-v1",
		InputSchema: []byte(`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"],"additionalProperties":false}`),
	}
	if err := g.validateArguments(snap, map[string]any{"count": float64(2)}); err != nil {
		t.Fatalf("valid arguments: %v", err)
	}
	for _, args := range []map[string]any{{}, {"count": "two"}, {"count": float64(2), "extra": true}} {
		if err := g.validateArguments(snap, args); err == nil {
			t.Fatalf("invalid arguments passed: %#v", args)
		}
	}
	snap.Fingerprint = "schema-v2"
	snap.InputSchema = []byte(`{"$ref":"https://example.com/schema.json"}`)
	if err := g.validateArguments(snap, map[string]any{}); err == nil || !strings.Contains(err.Error(), "invalid tool schema") {
		t.Fatalf("external schema reference was loaded: %v", err)
	}
}
