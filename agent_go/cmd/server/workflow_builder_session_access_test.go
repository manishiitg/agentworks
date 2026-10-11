package server

import "testing"

func TestBuilderConversationVisibleToIsPrivatePerUser(t *testing.T) {
	tests := []struct {
		name        string
		logUserID   string
		viewerID    string
		access      WorkflowAccessLevel
		wantVisible bool
	}{
		{"owner sees own log", "admin", "admin", WorkflowAccessOwner, true},
		{"reader sees own log", "yoav", "yoav", WorkflowAccessRead, true},
		{"reader cannot see owner log", "admin", "yoav", WorkflowAccessRead, false},
		{"owner may recover legacy log", "", "admin", WorkflowAccessOwner, true},
		{"reader cannot recover legacy log", "", "yoav", WorkflowAccessRead, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := builderConversationVisibleTo(tt.logUserID, tt.viewerID, tt.access); got != tt.wantVisible {
				t.Fatalf("builderConversationVisibleTo() = %v, want %v", got, tt.wantVisible)
			}
		})
	}
}
