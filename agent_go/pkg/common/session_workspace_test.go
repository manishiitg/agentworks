package common

import "testing"

func TestClassifySessionWorkspace(t *testing.T) {
	cases := []struct {
		name     string
		userID   string
		path     string
		wantKind SessionWorkspaceKind
		wantRoot string
	}{
		{"workflow root", "u1", "Workflow/trading", SessionWorkspaceWorkflow, "Workflow/trading"},
		{"workflow deep working dir collapses", "u1", "Workflow/trading/code", SessionWorkspaceWorkflow, "Workflow/trading"},
		{"workflow bare", "u1", "Workflow", SessionWorkspaceUnknown, ""},
		{"crew public", "u1", "Chats/Work/projects/customer-qa-480b6936", SessionWorkspaceCrewProject, "Chats/Work/projects/customer-qa-480b6936"},
		{"crew physical", "2a0aea4e", "_users/2a0aea4e/Chats/Work/projects/rooks-8a14b84a", SessionWorkspaceCrewProject, "Chats/Work/projects/rooks-8a14b84a"},
		{"crew deep collapses", "u1", "Chats/Work/projects/demo/db/reports", SessionWorkspaceCrewProject, "Chats/Work/projects/demo"},
		{"crew landing is not a project", "u1", "Chats/Work/projects/", SessionWorkspaceUnknown, ""},
		{"crew landing bare", "u1", "Chats/Work", SessionWorkspaceUnknown, ""},
		{"other owner's crew project still classifies", "u1", "_users/u2/Chats/Work/projects/demo", SessionWorkspaceCrewProject, "Chats/Work/projects/demo"},
		{"crew at the shared root", "u1", "Crew/demo-1a2b3c4d", SessionWorkspaceCrewProject, "Crew/demo-1a2b3c4d"},
		{"shared crew deep collapses", "u1", "Crew/demo-1a2b3c4d/db/reports", SessionWorkspaceCrewProject, "Crew/demo-1a2b3c4d"},
		{"shared crew, another viewer", "u2", "Crew/demo-1a2b3c4d", SessionWorkspaceCrewProject, "Crew/demo-1a2b3c4d"},
		{"the shared root itself is not a project", "u1", "Crew", SessionWorkspaceUnknown, ""},
		{"a hidden entry of the shared root is not a project", "u1", "Crew/.migrating/x", SessionWorkspaceUnknown, ""},
		{"traversal rejected", "u1", "../Workflow/x", SessionWorkspaceUnknown, ""},
		{"empty", "u1", "", SessionWorkspaceUnknown, ""},
		{"unrelated", "u1", "Downloads/x", SessionWorkspaceUnknown, ""},
		{"sloppy slashes", "u1", "/Workflow/trading/", SessionWorkspaceWorkflow, "Workflow/trading"},
	}
	for _, tc := range cases {
		kind, root := ClassifySessionWorkspace(tc.userID, tc.path)
		if kind != tc.wantKind || root != tc.wantRoot {
			t.Fatalf("%s: got (%q,%q) want (%q,%q)", tc.name, kind, root, tc.wantKind, tc.wantRoot)
		}
	}
}
