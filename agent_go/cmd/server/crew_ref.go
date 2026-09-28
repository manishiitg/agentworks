package server

import (
	"context"
	"encoding/json"
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
//	Crew/<id>                               shared root; owner from product.json owner_id
//	Chats/Work/projects/<id>                legacy, user-relative: the caller's own crew
//	_users/<owner>/Chats/Work/projects/<id> legacy, physical: any owner's crew
//
// Every API edge that receives a crew path goes through resolveCrewPath, so a
// new endpoint cannot accept one spelling and reject another (the report-run
// owner 400 on 2026-09-26). After migration, the legacy spellings resolve to
// the crew's Crew/<id> root through the alias map written by the migration.

const (
	crewSharedRootName = "Crew"
	crewPathAliasFile  = "_system/crew-path-aliases.json"
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
	// a shared root takes it from the manifest (empty when unknown).
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
	case len(segments) >= 4 && segments[0] == "Chats" && segments[1] == "Work" && segments[2] == "projects" && validID(segments[3]):
		if strings.TrimSpace(callerID) == "" {
			return crewPathRef{}, false
		}
		owner := sanitizeUserIDForPath(callerID)
		return crewPathRef{Root: legacyCrewRoot(owner, segments[3]), Rest: rest(4), OwnerID: owner}, true
	case len(segments) >= 6 && segments[0] == "_users" && segments[1] != "" && segments[2] == "Chats" && segments[3] == "Work" && segments[4] == "projects" && validID(segments[5]):
		return crewPathRef{Root: legacyCrewRoot(segments[1], segments[5]), Rest: rest(6), OwnerID: segments[1]}, true
	}
	return crewPathRef{}, false
}

func legacyCrewRoot(owner, id string) string {
	return "_users/" + owner + "/Chats/Work/projects/" + id
}

// resolveCrewPath is the one entry point for a crew path from a request or a
// stored reference: it parses any spelling, follows the migration alias to the
// crew's current root, and fills in the owner. ok=false means not a crew path.
func resolveCrewPath(ctx context.Context, callerID, raw string) (crewPathRef, bool) {
	ref, ok := parseCrewPath(callerID, raw)
	if !ok {
		return crewPathRef{}, false
	}
	if !ref.Shared {
		if moved := crewPathAliases.lookup(ctx, ref.Root); moved != "" {
			ref.Root, ref.Shared = moved, true
		}
	}
	if ref.Shared && ref.OwnerID == "" {
		ref.OwnerID = crewOwners.owner(ctx, ref.Root)
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
// with the Crew product reads, everyone else has none. A crew whose owner
// cannot be established is nobody's.
func crewAccessFor(claims *UserClaims, ref crewPathRef) crewAccessLevel {
	if claims == nil || ref.OwnerID == "" {
		return crewAccessNone
	}
	if ref.OwnerID == sanitizeUserIDForPath(claims.UserID) {
		return crewAccessOwner
	}
	if userAllowedProduct(claims, "work") {
		return crewAccessReader
	}
	return crewAccessNone
}

// crewOwners caches Crew/<id> -> product.json owner_id. Ownership changes
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
	c.mu.Lock()
	c.entries[root] = crewOwnerEntry{owner: owner, expires: now.Add(crewOwnerCacheTTL)}
	c.mu.Unlock()
	return owner
}

func readCrewManifestOwner(ctx context.Context, root string) string {
	raw, found, err := readFileFromWorkspace(ctx, root+"/product.json")
	if err != nil || !found {
		return ""
	}
	var manifest struct {
		Product string `json:"product"`
		OwnerID string `json:"owner_id"`
	}
	if json.Unmarshal([]byte(raw), &manifest) != nil || !strings.EqualFold(strings.TrimSpace(manifest.Product), "work") {
		return ""
	}
	return sanitizeUserIDForPath(strings.TrimSpace(manifest.OwnerID))
}

// crewPathAliases maps a migrated legacy crew root to its Crew/<id> root. The
// migration writes the file once; until then it is absent and nothing maps.
var crewPathAliases = &crewAliasCache{}

type crewAliasCache struct {
	mu      sync.Mutex
	loaded  time.Time
	aliases map[string]string
	// read is replaced in tests.
	read func(ctx context.Context) map[string]string
}

func (c *crewAliasCache) lookup(ctx context.Context, legacyRoot string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.aliases == nil || time.Since(c.loaded) > crewOwnerCacheTTL {
		read := c.read
		if read == nil {
			read = readCrewPathAliases
		}
		c.aliases, c.loaded = read(ctx), time.Now()
	}
	return c.aliases[legacyRoot]
}

func readCrewPathAliases(ctx context.Context) map[string]string {
	aliases := map[string]string{}
	raw, found, err := readFileFromWorkspace(ctx, crewPathAliasFile)
	if err != nil || !found {
		return aliases
	}
	var file struct {
		Aliases map[string]string `json:"aliases"`
	}
	if json.Unmarshal([]byte(raw), &file) == nil {
		for from, to := range file.Aliases {
			if ref, ok := parseCrewPath("", to); ok && ref.Shared && ref.Rest == "" {
				aliases[strings.Trim(from, "/")] = ref.Root
			}
		}
	}
	return aliases
}
