package server

import (
	"context"
	"testing"
)

// PLAT-746-886: no channel/file changes happen until every dependency has an
// explicit valid choice and the confirmation belongs to the current preview.
func TestAgentRemovalRequiresCurrentPreviewAndEveryChoice(t *testing.T) {
	for _, tc := range []struct {
		name, confirmation string
		choices            map[string]string
		allowed            bool
	}{
		{"no confirmation", "", map[string]string{"schedule": "crew:chat:other"}, false},
		{"stale confirmation", "old", map[string]string{"schedule": "crew:chat:other"}, false},
		{"missing choice", "current", nil, false},
		{"foreign destination", "current", map[string]string{"schedule": "another:chat:private"}, false},
		{"remove", "current", map[string]string{"schedule": "remove"}, true},
		{"reassign", "current", map[string]string{"schedule": "crew:chat:other"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := false
			plan := crewAgentRemovalPlan{preview: crewAgentRemovalPreview{Confirmation: "current", Dependencies: []crewAgentRemovalDependency{{ID: "schedule", Name: "Daily check", CanReassign: true}}, Destinations: []crewAgentRemovalDestination{{Key: "crew:chat:other"}}}, applyChannels: []func(context.Context, map[string]string) error{func(context.Context, map[string]string) error { changed = true; return nil }}}
			err := plan.apply(context.Background(), tc.confirmation, tc.choices, "crew")
			if (err == nil) != tc.allowed || changed != tc.allowed {
				t.Fatalf("allowed=%v changed=%v err=%v", tc.allowed, changed, err)
			}
		})
	}
}
