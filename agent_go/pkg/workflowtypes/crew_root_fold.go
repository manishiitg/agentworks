package workflowtypes

import "sync/atomic"

// A Crew attached to a workflow is stored by the workspace path it had when it was attached. When the Crew moves to
// the shared root (Crew/<folder>, PLAT-442 step 4) that stored path becomes an old spelling of the same Crew. The
// server installs the alias lookup once; every read of an attachment's root goes through foldCrewRoot, so a stored
// attachment keeps resolving to the Crew's current folder, and a stored root and a freshly authorized binding root
// compare equal. Without the hook (tests, tools) roots are used as stored.

var crewRootFold atomic.Value // func(string) string

// SetCrewRootFold installs the lookup that maps any spelling of a moved Crew's root to its current root and leaves
// every other path unchanged.
func SetCrewRootFold(fold func(string) string) {
	crewRootFold.Store(fold)
}

func foldCrewRoot(root string) string {
	if fold, ok := crewRootFold.Load().(func(string) string); ok && fold != nil && root != "" {
		return fold(root)
	}
	return root
}
