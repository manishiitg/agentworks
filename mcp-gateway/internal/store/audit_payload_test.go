package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuditPayloadBoundsAndDetachedSnapshot(t *testing.T) {
	input := map[string]any{"text": "hello", "nested": map[string]any{"count": 2}}
	raw, truncated := CaptureAuditPayload(input)
	if truncated || !json.Valid(raw) {
		t.Fatal("small payload truncated", string(raw))
	}
	input["text"] = "changed"
	st := NewMemoryStore()
	st.AppendAudit(AuditEvent{Input: raw})
	raw[0] = 'x'
	row := st.ListAudit()[0]
	if !json.Valid(row.Input) || !strings.Contains(string(row.Input), "hello") {
		t.Fatal("payload aliases caller data")
	}
	row.Input[0] = 'x'
	if !json.Valid(st.ListAudit()[0].Input) {
		t.Fatal("query mutated recorded payload")
	}
	for _, payload := range []any{strings.Repeat("界", 1<<20), strings.Repeat("\x00", 1<<20), make([]any, 100000)} {
		raw, truncated = CaptureAuditPayload(payload)
		if !truncated || len(raw) > AuditPayloadMaxBytes || !json.Valid(raw) {
			t.Fatal("payload not bounded valid JSON", len(raw))
		}
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	if raw, truncated = CaptureAuditPayload(cycle); !truncated || !json.Valid(raw) {
		t.Fatal("cyclic payload not bounded")
	}
}
