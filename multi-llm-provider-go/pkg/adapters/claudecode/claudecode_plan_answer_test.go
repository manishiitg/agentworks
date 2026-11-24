package claudecode

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/nativeinput"
)

// #3711: changes typed while a plan waits go back to Claude as a revision request; only Approve lets it start.
func TestPlanAnswerDenial(t *testing.T) {
	if planAnswerDenial(nativeinput.Answer{ID: "plan", SelectedLabels: []string{"Approve"}}) != nil {
		t.Fatal("Approve must allow the plan")
	}
	changed := planAnswerDenial(nativeinput.Answer{ID: "plan", OtherText: "use 30 a minute"})
	if changed == nil || changed["behavior"] != "deny" || !strings.Contains(changed["message"].(string), "use 30 a minute") {
		t.Fatalf("typed changes must reach Claude: %#v", changed)
	}
	if rejected := planAnswerDenial(nativeinput.Answer{ID: "plan", SelectedLabels: []string{"Reject"}}); rejected == nil || rejected["behavior"] != "deny" {
		t.Fatalf("Reject must deny: %#v", rejected)
	}
}
