package admin

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/pii"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func TestOutputReviewRuleRejected(t *testing.T) {
	a := &Admin{Store: store.NewMemoryStore(), WorkspaceID: "w"}
	for _, direction := range []string{pii.Output, pii.Both} {
		_, err := a.SavePIIRule(pii.Rule{DataType: "credit_card", Action: pii.Review, Direction: direction})
		if err == nil || !strings.Contains(err.Error(), "input only") {
			t.Fatalf("%s review was accepted: %v", direction, err)
		}
	}
	if _, err := a.SavePIIRule(pii.Rule{DataType: "credit_card", Action: pii.Review, Direction: pii.Input}); err != nil {
		t.Fatalf("input review was rejected: %v", err)
	}
}
