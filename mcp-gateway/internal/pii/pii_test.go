package pii

import (
	"strings"
	"testing"
)

func TestDeterministicPII(t *testing.T) {
	scope := Scope{WorkspaceID: "w1", ConnectorID: "c1", PublicName: "notes__send", Direction: Input}
	value := map[string]any{"message": "Reach alice@example.com or 415-555-1212", "nested": []any{"4111 1111 1111 1111"}}
	_, decision, err := ScanJSON(value, scope, nil)
	if err != nil || decision.Action != Block || decision.MatchCount != 3 {
		t.Fatalf("default decision = %+v, %v", decision, err)
	}

	transformed, decision, err := ScanJSON(value, scope, []Rule{{WorkspaceID: "w1", PublicName: "notes__send", DataType: "credit_card", Action: Allow}})
	if err != nil || decision.Action != Mask {
		t.Fatalf("allowlisted card decision = %+v, %v", decision, err)
	}
	message := transformed.(map[string]any)["message"].(string)
	if strings.Contains(message, "alice@example.com") || strings.Contains(message, "415-555-1212") || !strings.Contains(message, "[REDACTED:email]") {
		t.Fatalf("unmasked message: %q", message)
	}
}

func TestChecksumAndScope(t *testing.T) {
	scope := Scope{WorkspaceID: "w1", Direction: Output}
	for _, sample := range []string{"4111 1111 1111 1112", "000-00-0000", "123-45-0000"} {
		_, decision, err := ScanText(sample, scope, nil)
		if err != nil || decision.Action != Allow {
			t.Fatalf("false positive %q: %+v, %v", sample, decision, err)
		}
	}
	_, decision, _ := ScanText("123-45-6789", scope, nil)
	if decision.Action != Block {
		t.Fatalf("valid SSN: %+v", decision)
	}
	rules := []Rule{{WorkspaceID: "w1", GroupID: "g1", DataType: "ssn", Direction: Output, Action: Review}}
	scope.GroupIDs = []string{"g1"}
	_, decision, _ = ScanText("123-45-6789", scope, rules)
	if decision.Action != Review {
		t.Fatalf("group rule: %+v", decision)
	}
	scope.GroupIDs = []string{"g2"}
	_, decision, _ = ScanText("123-45-6789", scope, rules)
	if decision.Action != Block {
		t.Fatalf("other group inherited rule: %+v", decision)
	}
}

func TestPayloadBound(t *testing.T) {
	_, _, err := ScanText(strings.Repeat("a", MaxPayloadBytes+1), Scope{}, nil)
	if err != ErrPayloadTooLarge {
		t.Fatalf("large payload: %v", err)
	}
}
