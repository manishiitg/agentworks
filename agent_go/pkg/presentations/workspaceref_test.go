package presentations

import "testing"

// PLAT-435: the database path is the same for the logical and the physical
// spelling and drops any owner prefix.
func TestWorkspaceDatabasePathBothSpellings(t *testing.T) {
	const want = "Chats/Work/projects/c/db/db.sqlite"
	for _, in := range []string{"Chats/Work/projects/c", "_users/alice/Chats/Work/projects/c", "_users/bob/Chats/Work/projects/c/"} {
		if got := workspaceDatabasePath(in); got != want {
			t.Errorf("%q -> %q", in, got)
		}
	}
}
