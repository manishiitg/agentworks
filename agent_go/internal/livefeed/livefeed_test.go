package livefeed

import (
	"reflect"
	"testing"
)

func TestPublishCoalescesDuplicatesInOrder(t *testing.T) {
	b := NewBus()
	s := b.Subscribe()
	for i := 0; i < 20; i++ {
		b.Publish(Report, "Workflow/a")
	}
	b.Publish(HumanInputs, "Workflow/a")
	b.Publish(Report, "Workflow/b")

	select {
	case <-s.Wake:
	default:
		t.Fatal("subscriber was not woken")
	}
	got, resync := s.Drain()
	want := []Notice{{Report, "Workflow/a"}, {HumanInputs, "Workflow/a"}, {Report, "Workflow/b"}}
	if resync || !reflect.DeepEqual(got, want) {
		t.Fatalf("Drain = %v resync=%v, want %v", got, resync, want)
	}
	if again, _ := s.Drain(); len(again) != 0 {
		t.Fatalf("second Drain = %v, want empty", again)
	}
}

func TestOverflowTurnsIntoResync(t *testing.T) {
	b := NewBus()
	s := b.Subscribe()
	for i := 0; i <= maxPending; i++ {
		b.Publish(Report, "Workflow/"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	got, resync := s.Drain()
	if !resync || len(got) != 0 {
		t.Fatalf("Drain after overflow = %d notices resync=%v, want 0 and true", len(got), resync)
	}
}

func TestUnsubscribedStreamGetsNothing(t *testing.T) {
	b := NewBus()
	s := b.Subscribe()
	b.Unsubscribe(s)
	b.Publish(Sessions, "")
	if got, _ := s.Drain(); len(got) != 0 {
		t.Fatalf("unsubscribed Drain = %v, want empty", got)
	}
}

func TestIsPlanPathAcrossProducts(t *testing.T) {
	for path, want := range map[string]bool{
		"Workflow/relay/planning/plan.json":                               true,
		"Workflow/agentworks/planning/step_config.json":                   true,
		"Crew/team/workflow.json":                                         true,
		"Chats/Work/projects/team/planning/plan.json":                     true,
		"_users/alice/Chats/Work/projects/team/planning/step_config.json": true,
		"Chats/Code/projects/site/workflow.json":                          true,
		"Workflow/relay/planning/workflow_layout.json":                    false,
		"Workflow/relay/planning/changelog.json":                          false,
		"Workflow/relay/db/reports/index.html":                            false,
		"Chats/team/planning/plan.json":                                   false,
		"Workflow/relay/planning/plan.json.bak":                           false,
	} {
		if got := IsPlanPath(path); got != want {
			t.Errorf("IsPlanPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestPublishPlanPathKeepsLegacyProjectPathsPrivate(t *testing.T) {
	sub := Default.Subscribe()
	defer Default.Unsubscribe(sub)
	PublishPlanPath("Workflow/relay/planning/plan.json")
	PublishPlanPath("Crew/team/planning/plan.json")
	PublishPlanPath("_users/alice/Chats/Work/projects/team/planning/plan.json")
	got, _ := sub.Drain()
	want := []Notice{{Kind: Plan, Workflow: "Workflow/relay"}, {Kind: Plan, Workflow: "Crew/team"}, {Kind: Plan}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notices = %v, want %v", got, want)
	}
}
