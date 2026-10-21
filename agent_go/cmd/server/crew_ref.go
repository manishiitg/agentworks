package server

import (
	"context"
	"path"
	"strings"
	"sync"
	"time"
)

// Crew paths: one parser, one resolver, one access rule.
//
// A crew is moving from its owner's private tree to a shared root, the way
// workflows live at Workflow/<name> (docs/design/crew_shared_root.md):
//
//	Crew/<id>                               shared root; owner from the server's owner registry (PLAT-449)
//	Chats/Work/projects/<id>                legacy, user-relative: the caller's own crew
//	_users/<owner>/Chats/Work/projects/<id> legacy, physical: any owner's crew
//
// Every API edge that receives a crew path goes through resolveCrewPath, so a
// new endpoint cannot accept one spelling and reject another (the report-run
// owner 400 on 2026-09-26). After migration, the legacy spellings resolve to
// the crew's Crew/<id> root through the alias map written by the migration.

const (
	crewSharedRootName = "Crew"
	crewOwnerCacheTTL  = 30 * time.Second
)

// crewPathRef is one crew path split into the crew's root and the part below it.
type crewPathRef struct {
	// Root is the crew's root as addressed: "Crew/<id>" or
	// "_users/<owner>/Chats/Work/projects/<id>".
	Root string
	// Rest is the path below Root ("" for the root itself).
	Rest string
	// OwnerID is the owning user's path segment. Legacy paths carry it;
	// a shared root takes it from the server's owner registry (empty when unknown).
	OwnerID string
	// Shared reports a Crew/<id> root.
	Shared bool
}

// Path is the full workspace path the ref addresses.
func (r crewPathRef) Path() string {
	if r.Rest == "" {
		return r.Root
	}
	return r.Root + "/" + r.Rest
}

// parseCrewPath recognizes the three crew spellings without any I/O. A
// user-relative path resolves under the caller, as the workspace service does.
func parseCrewPath(callerID, raw string) (crewPathRef, bool) {
	clean := strings.Trim(path.Clean("/"+strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")), "/")
	if clean == "" || strings.Contains(strings.TrimSpace(raw), "..") {
		return crewPathRef{}, false
	}
	segments := strings.Split(clean, "/")
	rest := func(from int) string { return strings.Join(segments[from:], "/") }
	validID := func(id string) bool { return id != "" && !strings.HasPrefix(id, ".") }
	switch {
	case segments[0] == crewSharedRootName:
		if len(segments) < 2 || !validID(segments[1]) {
			return crewPathRef{}, false
		}
		return crewPathRef{Root: crewSharedRootName + "/" + segments[1], Rest: rest(2), Shared: true}, true

	}
	return crewPathRef{}, false
}

// resolveCrewPath is the one entry point for a crew path from a request or a
// stored reference: it parses any spelling, follows the migration alias to the
// crew's current root, and fills in the owner. ok=false means not a crew path.
func resolveCrewPath(ctx context.Context, callerID, raw string) (crewPathRef, bool) {
	ref, ok := parseCrewPath(callerID, raw)
	if !ok {
		return crewPathRef{}, false
	}
	if ref.Shared && ref.OwnerID == "" {
		ref.OwnerID = crewOwners.owner(ctx, ref.Root)
	} else if !ref.Shared {
		// PLAT-449: the owner is the server's registry entry, else the path owner; the manifest is never consulted
		// (users and agent turns can edit it).
		ref.OwnerID = resolveProjectOwner(ctx, ref.Root)
	}
	return ref, true
}

type crewAccessLevel int

const (
	crewAccessNone crewAccessLevel = iota
	crewAccessReader
	crewAccessOwner
)

// crewAccessFor is Crew Run mode: the owner has full access, any other user
// with the Crew product reads (while project sharing is on), everyone else has none. A crew whose owner
// cannot be established is nobody's.
func crewAccessFor(claims *UserClaims, ref crewPathRef) crewAccessLevel {
	if claims == nil || ref.OwnerID == "" {
		return crewAccessNone
	}
	if ref.OwnerID == sanitizeUserIDForPath(claims.UserID) {
		return crewAccessOwner
	}
	product, _, ok := projectProductForPath(ref.Root)
	if !ok || !userAllowedProduct(claims, product.ProfileID) {
		return crewAccessNone
	}

	// A co-owner builds with the same rights as the main owner (crew_co_owners.go); identity stays the main owner's.
	if crewIsCoOwner(ref.Root, claims.UserID) {
		return crewAccessOwner
	}
	// Another user's Crew is read while it is shared (the default, DECISIONS 2026-10-08); when sharing is off or the
	// owner made the Crew private, nobody else sees even that the Crew exists or is working.
	if crewSharedWith(ref.Root, claims.UserID) {
		return crewAccessReader
	}
	return crewAccessNone
}

// crewOwners caches Crew/<id> -> its registered owner. Ownership changes
// only through crew creation/transfer, so a short TTL is enough.
var crewOwners = &crewOwnerCache{entries: map[string]crewOwnerEntry{}}

type crewOwnerEntry struct {
	owner   string
	expires time.Time
}

type crewOwnerCache struct {
	mu      sync.Mutex
	entries map[string]crewOwnerEntry
	// read is replaced in tests.
	read func(ctx context.Context, root string) string
}

func (c *crewOwnerCache) owner(ctx context.Context, root string) string {
	now := time.Now()
	c.mu.Lock()
	if entry, ok := c.entries[root]; ok && now.Before(entry.expires) {
		c.mu.Unlock()
		return entry.owner
	}
	read := c.read
	c.mu.Unlock()
	if read == nil {
		read = readCrewManifestOwner
	}
	owner := read(ctx, root)
	// An unknown owner is not remembered: a Crew registered (created or migrated) a moment ago must be usable
	// at once, and a refusal that sticks for the cache lifetime would look like a lost Crew.
	if owner != "" {
		c.mu.Lock()
		c.entries[root] = crewOwnerEntry{owner: owner, expires: now.Add(crewOwnerCacheTTL)}
		c.mu.Unlock()
	}
	return owner
}

// readCrewManifestOwner is the owner of a Crew at the shared root: the server's registry entry, nothing else
// (PLAT-449: product.json is user-writable, so its owner_id is never read for this; the historical name stays for the
// tests that replace crewOwners.read). A Crew without an entry has no owner and belongs to nobody.
func readCrewManifestOwner(_ context.Context, root string) string {
	return resolveProjectOwner(context.Background(), root)
}

// sharedCrewIdentity is the Crew/<id>[/rest] path a crew path names once any alias is followed, for crews that
// live at the shared root; ok is false for any other path (including a crew still in its owner's tree).
func sharedCrewIdentity(callerID, raw string) (string, bool) {
	ref, ok := resolveCrewPath(context.Background(), callerID, raw)
	if !ok || !ref.Shared {
		return "", false
	}
	return ref.Path(), true
}

// crewTurnWorkspace is the workspace a crew turn runs in: the binding's verified root when the crew lives at the
// shared root (keeping any folder below the root that the client named), otherwise the client's path unchanged.
func crewTurnWorkspace(ctx context.Context, callerID, selected, bindingRoot string) string {
	binding, bound := resolveCrewPath(ctx, callerID, bindingRoot)
	if !bound {
		return selected
	}
	ref, ok := resolveCrewPath(ctx, callerID, selected)
	if !ok || ref.Root != binding.Root {
		return selected
	}
	original, _ := parseCrewPath(callerID, selected)
	if !ref.Shared && original.Root == ref.Root {
		return selected
	}
	return ref.Path()
}

// Canonical references are stored directly. Retired filesystem locations
// are accepted only by the one-off migration tool, never by live routing.
func foldCrewRootForStoredReference(root string) string {
	return strings.Trim(strings.ReplaceAll(strings.TrimSpace(root), "\\", "/"), "/")
}
