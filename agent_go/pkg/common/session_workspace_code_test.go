package common

import "testing"

// A torn-down session drops its Code mark with the rest of its config.
func TestClearedSessionsForgetTheirCode(t *testing.T) {
	MarkCodeSession("code-mark-prune", "_users/alice/Chats/Code/projects/app-1")
	if CodeSessionRoot("code-mark-prune") == "" {
		t.Fatal("mark not recorded")
	}
	ClearSessionShellConfig("code-mark-prune")
	if root := CodeSessionRoot("code-mark-prune"); root != "" {
		t.Fatalf("cleared session still marked as Code %q", root)
	}
}
