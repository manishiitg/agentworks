package server

import "testing"

func TestWorkflowSchedulesAllPaused(t *testing.T) {
	enabled := WorkflowSchedule{ID: "a", Enabled: true}
	paused := WorkflowSchedule{ID: "b", Enabled: false}
	cases := []struct {
		name     string
		manifest *WorkflowManifest
		want     bool
	}{
		{"nil manifest", nil, false},
		{"no schedules at all: runs only on request, Pulse still looks after it", &WorkflowManifest{}, false},
		{"every schedule paused", &WorkflowManifest{Schedules: []WorkflowSchedule{paused, paused}}, true},
		{"partly paused is still active", &WorkflowManifest{Schedules: []WorkflowSchedule{paused, enabled}}, false},
		{"all enabled", &WorkflowManifest{Schedules: []WorkflowSchedule{enabled}}, false},
	}
	for _, tc := range cases {
		if got := workflowSchedulesAllPaused(tc.manifest); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
