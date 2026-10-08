package server

import (
	"context"
	"log"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsalias"
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
	ownedRef := workspaceref.MustParse(clean)
	ownedRoot, ownedProject, ownedOK := ownedRef.Project()
	ownedOK = ownedOK && ownedRef.HasOwner() && ownedRoot == workspaceref.CrewProjectsRoot && validID(ownedProject)
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
	case ownedOK:
		below := strings.Trim(strings.TrimPrefix(ownedRef.Logical(), ownedRoot+"/"+ownedProject), "/")
		return crewPathRef{Root: legacyCrewRoot(ownedRef.Owner(), ownedProject), Rest: below, OwnerID: ownedRef.Owner()}, true
	}
	return crewPathRef{}, false
}

func legacyCrewRoot(owner, id string) string {
	return workspaceref.PhysicalPathOf(owner, workspaceref.CrewProjectsRoot, id)
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
			warnCrewAliasUsed(ref.Root, moved)
			ref.Root, ref.Shared = moved, true
		}
	}
	if ref.Shared && ref.OwnerID == "" {
		ref.OwnerID = crewOwners.owner(ctx, ref.Root)
	} else if !ref.Shared && ref.OwnerID != "" {
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
	// Another user's Crew is read while it is shared (the default, DECISIONS 2026-10-08); when sharing is off or the
	// owner made the Crew private, nobody else sees even that the Crew exists or is working.
	if userAllowedProduct(claims, "work") && crewSharedWithOthers(ref.Root) {
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

// crewPathAliases maps a migrated legacy crew root to its Crew/<id> root, from the server-controlled owner registry
// (the Crew move records an old path as an alias of the Crew it moved); until a Crew is moved nothing maps.
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

func readCrewPathAliases(_ context.Context) map[string]string {
	return crewAliasesFromRegistry(defaultProjectOwners())
}

// crewAliasesFromRegistry maps the old physical root of every migrated Crew to its Crew/<folder> root. The aliases
// are the server-controlled registry's (PLAT-449): a user or agent turn cannot add one, so no one can make an old path
// resolve to someone else's Crew.
func crewAliasesFromRegistry(registry *projectOwnerRegistry) map[string]string {
	aliases := map[string]string{}
	all, err := registry.All()
	if err != nil {
		log.Printf("[OWNER_REGISTRY] cannot read the project registry for crew aliases (%v); old crew paths do not resolve", err)
		return aliases
	}
	for _, rec := range all {
		if rec.Product != "work" || !rec.Shared {
			continue
		}
		for _, alias := range rec.Aliases {
			ref, ok := parseCrewPath("", alias)
			if !ok || ref.Shared || ref.Rest != "" {
				continue
			}
			aliases[ref.Root] = workspaceref.SharedProjectPath(rec.Folder)
		}
	}
	return aliases
}

// lookupFolder returns the Crew/<folder> root of a migrated crew by the folder name it kept, whoever owned it
// ("" when no migrated crew has that folder). A migrated crew keeps its folder name, and folder names are
// unique (<slug>-<id8>), so a stored reference that does not say who owned the crew ("Chats/Work/projects/<f>"
// in a chat history file) still maps to exactly one crew.
func (c *crewAliasCache) lookupFolder(ctx context.Context, folder string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.aliases == nil || time.Since(c.loaded) > crewOwnerCacheTTL {
		read := c.read
		if read == nil {
			read = readCrewPathAliases
		}
		c.aliases, c.loaded = read(ctx), time.Now()
	}
	want := workspaceref.SharedProjectPath(folder)
	for _, moved := range c.aliases {
		if moved == want {
			return moved
		}
	}
	return ""
}

var crewAliasWarned sync.Map

// warnCrewAliasUsed logs, once per old path per process, that a stored or typed reference to a migrated crew's
// old location was followed to its new one. Old spellings keep working indefinitely; the log is how a stale
// reference is found and rewritten at the source.
func warnCrewAliasUsed(legacyRoot, moved string) {
	if _, seen := crewAliasWarned.LoadOrStore(legacyRoot, true); !seen {
		log.Printf("[CREW_ALIAS] old crew path %q resolved to %q (migrated crew; the old spelling keeps working)", legacyRoot, moved)
	}
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
	if !workspaceref.MustParse(bindingRoot).IsShared() {
		return selected
	}
	ref, ok := resolveCrewPath(ctx, callerID, selected)
	if !ok || !ref.Shared || ref.Root != strings.Trim(bindingRoot, "/") {
		return selected
	}
	return ref.Path()
}

// followCrewAlias returns raw with an old spelling of a migrated crew replaced by its Crew/<id> path; any other
// path, and a crew that has not moved, comes back unchanged. This is the one function every reader of a stored or
// typed crew path that cannot carry a crewPathRef uses.
func followCrewAlias(callerID, raw string) string {
	ref, ok := resolveCrewPath(context.Background(), callerID, raw)
	if !ok || !ref.Shared {
		return raw
	}
	return ref.Path()
}

// foldCrewRootForStoredReference maps a stored crew root (a workflow's attached Crew, a place connection's root) to
// the Crew's current root: an old physical path follows the exact alias of its owner's folder; a logical path, which
// names no owner, follows the folder name (unique, and kept by the move). Anything else, and a Crew that has not
// moved, comes back unchanged.
func foldCrewRootForStoredReference(root string) string {
	ref := workspaceref.MustParse(root)
	if ref.HasOwner() {
		return followCrewAlias("", root)
	}
	return foldCrewScopePath(root)
}

func init() {
	// A Crew attached to a workflow keeps resolving after the Crew moves (PLAT-442 step 4).
	workflowtypes.SetCrewRootFold(foldCrewRootForStoredReference)
}

// crewAliasForWorkspaceIO is the resolver of the workspace transport (pkg/wsalias): a path argument that names an old
// spelling of a migrated Crew is rewritten to the Crew's Crew/<folder> path before the request leaves the server, so a
// typed, stored or browser-held old path reads the Crew's files and never creates a folder at the old place.
func crewAliasForWorkspaceIO(userID, p string) (string, bool) {
	// Only a per-user Crew path can be an old spelling: cheap check before parsing.
	if !strings.Contains(p, "Work/projects/") {
		return "", false
	}
	clean := workspaceProxyCleanPath(p)
	ref := workspaceref.MustParse(clean)
	folder, shared, ok := ref.AnyCrewProject()
	if !ok || shared {
		return "", false
	}
	owner := ref.Owner()
	if owner == "" {
		owner = strings.TrimSpace(userID)
		if owner == "" {
			owner = GetDefaultUserID()
		}
		owner = sanitizeUserIDForPath(owner)
	}
	moved := crewPathAliases.lookup(context.Background(), legacyCrewRoot(owner, folder))
	if moved == "" {
		return "", false
	}
	warnCrewAliasUsed(legacyCrewRoot(owner, folder), moved)
	rest := strings.TrimPrefix(ref.Logical(), workspaceref.CrewProjectsRoot+"/"+folder)
	return moved + rest, true
}

func init() {
	wsalias.SetResolver(crewAliasForWorkspaceIO)
}
