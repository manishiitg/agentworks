package services

import (
	"context"
	"sync"
)

// PLAT-442 step 4: a Crew may live at the shared root Crew/<folder>, whose path names no owner, and a migrated Crew
// keeps answering to its old spellings. The services package cannot see the server's owner registry or alias map, so
// the server installs these lookups once at startup. Without them (tests, tools) every function here falls back to
// what the path itself says, which is the pre-move behaviour.

// SharedCrewListing is a Crew at the shared root as a destination list shows it.
type SharedCrewListing struct {
	ID            string
	Title         string
	WorkspacePath string // Crew/<folder>
}

var (
	crewScopeMu          sync.RWMutex
	crewOwnerLookup      func(workspacePath string) string
	crewScopeFold        func(workspacePath string) string
	ownSharedCrewsLookup func(ctx context.Context, userID string) []SharedCrewListing
)

// SetCrewScopeHooks installs the server's crew lookups: owner returns the registered owner of a crew path (Crew/<f>,
// or any spelling of a migrated crew) and "" when unknown; fold maps any spelling of a migrated crew to its Crew/<f>
// path and leaves every other path alone; ownCrews lists the Crews at the shared root a user owns.
func SetCrewScopeHooks(owner func(workspacePath string) string, fold func(workspacePath string) string, ownCrews func(ctx context.Context, userID string) []SharedCrewListing) {
	crewScopeMu.Lock()
	defer crewScopeMu.Unlock()
	crewOwnerLookup, crewScopeFold, ownSharedCrewsLookup = owner, fold, ownCrews
}

func crewOwnerFor(workspacePath string) string {
	crewScopeMu.RLock()
	fn := crewOwnerLookup
	crewScopeMu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn(workspacePath)
}

func foldCrewScope(workspacePath string) string {
	crewScopeMu.RLock()
	fn := crewScopeFold
	crewScopeMu.RUnlock()
	if fn == nil {
		return workspacePath
	}
	return fn(workspacePath)
}

func listOwnSharedCrews(ctx context.Context, userID string) []SharedCrewListing {
	crewScopeMu.RLock()
	fn := ownSharedCrewsLookup
	crewScopeMu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, userID)
}
