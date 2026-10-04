// Package reftest holds the shared helper for path-sensitive tests (PLAT-435):
// run the same assertion under both spellings of a folder.
package reftest

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Spellings returns the logical spelling and the physical spelling of the
// logical path for user.
func Spellings(user, logical string) (logicalSpelling, physicalSpelling string) {
	ref := workspaceref.MustParse(logical)
	return ref.Logical(), ref.Physical(user)
}

// BothSpellings runs check once with the logical spelling and once with the
// physical spelling (for user) of the same folder, as subtests.
func BothSpellings(t *testing.T, user, logical string, check func(t *testing.T, spelling string)) {
	t.Helper()
	l, p := Spellings(user, logical)
	t.Run("logical", func(t *testing.T) { check(t, l) })
	t.Run("physical", func(t *testing.T) { check(t, p) })
}

// OtherUser returns the physical spelling of the folder under a different
// user, the path that must stay refused or unequal.
func OtherUser(user, logical string) string {
	other := "other-" + user
	return workspaceref.MustParse(logical).Physical(other)
}
