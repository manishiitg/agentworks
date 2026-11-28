package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filelock"
)

// PLAT-449: who owns a Crew or a Code is SERVER-CONTROLLED metadata, kept where neither a user nor an agent turn can
// write it. product.json is project data: a user (or a Crew's own agent, which runs as the app account with write
// access to the project) can edit it, so it can never decide whose Linux slot a launch uses or who may open a
// project. Its owner_id is informational only.
//
// The registry lives in the app's STATE area (<state root>/ownership/projects.json: app-owned, 0700 directory, 0600
// file), not under the docs root, so it is neither reachable by the workspace proxy nor by a CLI's folder guard.
// Resolution order (resolveProjectOwner):
//
//  1. the registry entry for the project (product + folder name), if there is one;
//  2. for a project that has no entry, the owner the PHYSICAL PATH names (_users/<owner>/...): a user can only
//     create folders in their own tree, so the path is as trustworthy as the account that wrote it;
//  3. for a project at the shared Crew/ root with no entry: nobody (fail closed).
//
// A registry entry is written when the server creates a project, when the owner first opens an unregistered one, by
// the startup scan, and by the Crew move for the Crews it moves. An entry is never overwritten with another owner:
// there is no ownership transfer, and none may be added without updating every authority together (this registry and
// the project's location).

const (
	projectOwnerRegistryDir  = "ownership"
	projectOwnerRegistryFile = "projects.json"
	projectOwnerSchema       = 1
)

// projectOwnerRecord is one project's server-controlled identity.
type projectOwnerRecord struct {
	Product string `json:"product"` // "work"
	Folder  string `json:"folder"`  // <slug>-<id8>, the project's folder name (kept when a Crew moves)
	OwnerID string `json:"owner_id"`
	// CanonicalRoot is server-owned routing metadata. Empty keeps the legacy
	// Shared/path interpretation; product.json never decides origin or location.
	CanonicalRoot string `json:"canonical_root,omitempty"`
	// BindingRoot pins path-keyed private stores and publication IDs when the
	// project moves. Only a trusted storage relocation can set it.
	BindingRoot string `json:"binding_root,omitempty"`
	// LocalRoot is an immutable create-time binding for a local Crew. It is
	// never read from product.json or accepted by settings updates.
	LocalRoot string `json:"local_root,omitempty"`
	// DatabaseRoot is set only by the explicit automation conversion.
	DatabaseRoot string `json:"database_root,omitempty"`
	// ProjectID is the manifest's id when the entry was written: informational (the manifest is user-writable).
	ProjectID string `json:"project_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	// Shared marks a Crew that lives at Crew/<folder> (moved there by the Crew move or created there).
	Shared bool `json:"shared,omitempty"`
	// Aliases are the old physical roots of a moved Crew (_users/<owner>/Chats/Work/projects/<folder>).
	Aliases []string `json:"aliases,omitempty"`
	// Private is the owner's restriction of a Crew to themselves; a Crew is shared with every user of the Crew product
	// otherwise (crewSharedWithOthers).
	Private bool `json:"private,omitempty"`
	// AdminInspection preserves the existing audited review grant during conversion.
	// It is server-owned and cannot be enabled by an editable project manifest.
	AdminInspection bool `json:"admin_inspection,omitempty"`
	// AudienceConfigured distinguishes an explicit new audience from legacy Code
	// records whose zero-value Private field was never an access grant.
	AudienceConfigured bool `json:"audience_configured,omitempty"`
	// RunPeople are the people added to the project's dashboard (general access Restricted). Like Private=false (anyone at
	// the company with the link), it opens the dashboard pages and their live data only, not the project (#4545).
	RunPeople []string `json:"run_people,omitempty"`
	// SharedAgents are the stable existing tabs exposed to the same Run audience.
	SharedAgents []string `json:"shared_agents,omitempty"`
	// SharedAgentSince is when each selected conversation became shared.
	SharedAgentSince map[string]string `json:"shared_agent_since,omitempty"`
	// Bots are this project's agents shared for others to use without joining it, by conversation key (crew_bots.go).
	Bots map[string]crewBot `json:"bots,omitempty"`
	// AddedBots are other projects' bots added to this project (by bot id): they show in its agent list and right
	// menu, and its agents can ask them. Each person's own access to a bot is still checked when they use it.
	AddedBots []string `json:"added_bots,omitempty"`
	// BotServers is, for a project on a Mac, the saved sign-in (connection id) each of its AddedBots comes from.
	BotServers map[string]string `json:"bot_servers,omitempty"`
	// CoOwners build a server Crew with its main owner (OwnerID, the creator, who never changes and whose server account
	// every turn runs in). Server-controlled like the rest of the record; only the main owner changes it (crew_owners.go).
	CoOwners []string `json:"co_owners,omitempty"`
	// Users work in a server Crew through its chat with the setup the owners made, and cannot change that setup (owner,
	// 25 Nov: "co-owners can always change everything... we should define a user role for this type of user who have
	// limited permission"; crew_setup_lock.go). Only the main owner adds or removes them.
	Users []string `json:"users,omitempty"`
	// UserLockedPaths are, per user, the extra project paths the owner locked besides the instructions, skills and settings.
	UserLockedPaths map[string][]string `json:"user_locked_paths,omitempty"`
	// LegacySetupLocked is the 24 Nov co-owner switch that Users replaced. load turns each entry into a User; remove the
	// field once every server has written its registry again.
	LegacySetupLocked map[string][]string `json:"setup_locked,omitempty"`
}

type projectOwnerFile struct {
	SchemaVersion int                           `json:"schema_version"`
	Projects      map[string]projectOwnerRecord `json:"projects"`
}

// errProjectOwnerConflict is returned when a registration names a different owner than the registered one.
var errProjectOwnerConflict = errors.New("project is registered to another owner")

func projectOwnerKey(product, folder string) string {
	return strings.ToLower(strings.TrimSpace(product)) + "/" + strings.TrimSpace(folder)
}

// projectOwnerRegistry is the registry file with a change-aware read cache. Writers take an exclusive file lock
// (the server and the migration command both write), readers re-read when the file's size or mtime moved, so the
// running server sees what the migration command wrote without a restart.
type projectOwnerRegistry struct {
	// dir is the registry directory; empty means "no state area": lookups find nothing, writes fail loudly.
	dir string

	mu        sync.Mutex
	cached    map[string]projectOwnerRecord
	stamp     fileStamp
	checkedAt time.Time // last time the file's stamp was compared
}

// projectOwnerStatLife is how long a cached registry is trusted without re-checking the file. This process's own
// writes drop the cache at once (modify); another process's write (the migration command) shows within this time.
// The per-call Lstat was ~16% of agent CPU on a server CPU profile (#2989). Tests edit the file directly and
// read it back at once, so they re-check on every read.
var projectOwnerStatLife = func() time.Duration {
	if testing.Testing() {
		return 0
	}
	return time.Second
}()

type fileStamp struct {
	size  int64
	mtime time.Time
	ok    bool
}

// defaultProjectOwners is the registry of this process's state root (re-resolved on each call so tests that move
// AGENTWORKS_STATE_ROOT get their own registry).
var defaultProjectOwnersOverride *projectOwnerRegistry

func defaultProjectOwners() *projectOwnerRegistry {
	if defaultProjectOwnersOverride != nil {
		return defaultProjectOwnersOverride
	}
	// A test binary never reads or writes the real state area: without an explicit AGENTWORKS_STATE_ROOT it has
	// no registry at all (owners fall back to the path), and a test that wants one sets the root.
	if testing.Testing() && strings.TrimSpace(os.Getenv("AGENTWORKS_STATE_ROOT")) == "" {
		return &projectOwnerRegistry{}
	}
	root, err := workflowCLIStateRoot()
	if err != nil {
		return &projectOwnerRegistry{}
	}
	return projectOwnersAt(root)
}

var registryByDir sync.Map // map[string]*projectOwnerRegistry

// projectOwnersAt is the registry under a state root (the migration command passes its own).
func projectOwnersAt(stateRoot string) *projectOwnerRegistry {
	dir := filepath.Join(filepath.Clean(stateRoot), projectOwnerRegistryDir)
	if existing, ok := registryByDir.Load(dir); ok {
		return existing.(*projectOwnerRegistry)
	}
	created, _ := registryByDir.LoadOrStore(dir, &projectOwnerRegistry{dir: dir})
	return created.(*projectOwnerRegistry)
}

func (r *projectOwnerRegistry) path() string { return filepath.Join(r.dir, projectOwnerRegistryFile) }

// load returns the registered projects. A missing file is an empty registry; an unreadable or corrupt one is
// reported (and treated as empty by Lookup, which then falls back to the path owner, never to a manifest).
func (r *projectOwnerRegistry) load() (map[string]projectOwnerRecord, error) {
	if r == nil || r.dir == "" {
		return map[string]projectOwnerRecord{}, nil
	}
	r.mu.Lock()
	if r.cached != nil && time.Since(r.checkedAt) < projectOwnerStatLife {
		cached := r.cached
		r.mu.Unlock()
		return cached, nil
	}
	r.mu.Unlock()
	info, err := os.Lstat(r.path())
	switch {
	case errors.Is(err, os.ErrNotExist):
		return map[string]projectOwnerRecord{}, nil
	case err != nil:
		return nil, err
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file", r.path())
	}
	stamp := fileStamp{size: info.Size(), mtime: info.ModTime(), ok: true}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cached != nil && r.stamp == stamp {
		r.checkedAt = time.Now()
		return r.cached, nil
	}
	raw, err := os.ReadFile(r.path())
	if err != nil {
		return nil, err
	}
	var file projectOwnerFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", r.path(), err)
	}
	if file.Projects == nil {
		file.Projects = map[string]projectOwnerRecord{}
	}
	for key, rec := range file.Projects {
		if len(rec.LegacySetupLocked) > 0 {
			file.Projects[key] = migrateSetupLockedToUsers(rec)
		}
	}
	r.cached, r.stamp, r.checkedAt = file.Projects, stamp, time.Now()
	return file.Projects, nil
}

// Lookup returns the registered record of a project.
func (r *projectOwnerRegistry) Lookup(product, folder string) (projectOwnerRecord, bool) {
	projects, err := r.load()
	if err != nil {
		log.Printf("[OWNER_REGISTRY] cannot read the project registry (%v); owners fall back to the path", err)
		return projectOwnerRecord{}, false
	}
	rec, ok := projects[projectOwnerKey(product, folder)]
	return rec, ok
}

// FindByProjectID returns the record of a product's project by its stable id, reading the shared cache without
// copying it. crewOwnerRecord ran per request through All, and the copies were ~35-45% of agent allocations (#2989).
// The cached map is replaced, never changed in place, so reading it after load returns is safe (as in Lookup).
func (r *projectOwnerRegistry) FindByProjectID(product, projectID string) (projectOwnerRecord, bool) {
	projects, err := r.load()
	if err != nil {
		return projectOwnerRecord{}, false
	}
	for _, record := range projects {
		if record.Product == product && record.ProjectID == projectID {
			return record, true
		}
	}
	return projectOwnerRecord{}, false
}

// All returns a copy of every registered project.
func (r *projectOwnerRegistry) All() (map[string]projectOwnerRecord, error) {
	projects, err := r.load()
	if err != nil {
		return nil, err
	}
	out := make(map[string]projectOwnerRecord, len(projects))
	for key, rec := range projects {
		out[key] = rec
	}
	return out, nil
}

// Register records a project's owner. It never changes an existing owner (errProjectOwnerConflict) and is
// idempotent for the same owner; fields it does not know yet (project id, created_at) are filled in, and aliases
// are merged. The write is atomic and serialized across processes.
func (r *projectOwnerRegistry) Register(rec projectOwnerRecord) error {
	rec.Product = strings.ToLower(strings.TrimSpace(rec.Product))
	rec.Folder = strings.TrimSpace(rec.Folder)
	rec.OwnerID = cleanManifestOwner(rec.OwnerID)
	if rec.Product == "" || rec.Folder == "" || rec.OwnerID == "" || strings.ContainsAny(rec.Folder, "/\\") || strings.HasPrefix(rec.Folder, ".") {
		return fmt.Errorf("invalid project registration %+v", rec)
	}
	if err := validateProjectStorageRecord(rec); err != nil {
		return err
	}
	return r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(rec.Product, rec.Folder)
		if err := checkProjectStorageClaim(projects, key, rec); err != nil {
			return false, err
		}
		current, exists := projects[key]
		if !exists {

			if rec.CreatedAt == "" {
				rec.CreatedAt = time.Now().UTC().Format(time.RFC3339)
			}
			projects[key] = rec
			return true, nil
		}
		if current.OwnerID != rec.OwnerID {
			return false, fmt.Errorf("%w: %s is registered to %q, not %q", errProjectOwnerConflict, key, current.OwnerID, rec.OwnerID)
		}
		changed := false
		if rec.LocalRoot != "" && rec.LocalRoot != current.LocalRoot {
			return false, fmt.Errorf("a project's local folder is chosen only at creation")
		}
		if rec.BindingRoot != "" && rec.BindingRoot != current.BindingRoot {
			return false, fmt.Errorf("private state identity can only be pinned by the storage migration")
		}
		if rec.CanonicalRoot != "" && current.CanonicalRoot != rec.CanonicalRoot {
			if projectStorageRoot(current) != rec.CanonicalRoot {
				return false, fmt.Errorf("project location changed; use the storage migration")
			}
			current.CanonicalRoot, changed = rec.CanonicalRoot, true
		}

		if current.ProjectID == "" && rec.ProjectID != "" {
			current.ProjectID, changed = rec.ProjectID, true
		}
		if rec.Shared && !current.Shared {
			current.Shared, changed = true, true
		}
		for _, alias := range rec.Aliases {
			if !registryListHas(current.Aliases, alias) {
				current.Aliases, changed = append(current.Aliases, alias), true
			}
		}
		if changed {
			projects[key] = current
		}
		return changed, nil
	})
}

// SetShared marks a registered Crew as living at Crew/<folder> (true) or back in its owner's tree (false, the
// rollback of a move), keeping the aliases. The project must be registered.
func (r *projectOwnerRegistry) SetShared(product, folder string, shared bool) error {
	return r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(product, folder)
		current, exists := projects[key]
		if !exists {
			return false, fmt.Errorf("%s is not registered", key)
		}
		if current.Shared == shared {
			return false, nil
		}
		current.Shared = shared
		projects[key] = current
		return true, nil
	})
}

// SetPrivate records the owner's choice to keep a registered Crew to themselves (true) or share it (false).
func (r *projectOwnerRegistry) SetPrivate(product, folder string, private bool) error {
	return r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(product, folder)
		current, exists := projects[key]
		if !exists {
			return false, fmt.Errorf("%s is not registered", key)
		}
		if current.Private == private && current.AudienceConfigured {
			return false, nil
		}
		current.Private, current.AudienceConfigured = private, true
		projects[key] = current
		return true, nil
	})
}

// SetRunPeople records the accounts a Crew is shared with by name; an empty list clears it.
func (r *projectOwnerRegistry) SetRunPeople(product, folder string, people []string) error {
	return r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(product, folder)
		current, exists := projects[key]
		if !exists {
			return false, fmt.Errorf("%s is not registered", key)
		}
		current.RunPeople = append([]string(nil), people...)
		projects[key] = current
		return true, nil
	})
}

// AddCoOwner records an account as a co-owner of a registered project; adding the main owner or an existing co-owner
// changes nothing. A co-owner is no longer one of the Crew's Run people. Returns whether the list changed.
func (r *projectOwnerRegistry) AddCoOwner(product, folder, userID string) (changed bool, err error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, errors.New("a co-owner needs an account id")
	}
	err = r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(product, folder)
		current, exists := projects[key]
		if !exists {
			return false, fmt.Errorf("%s is not registered", key)
		}
		if current.OwnerID == userID || slices.Contains(current.CoOwners, userID) {
			return false, nil
		}
		current.CoOwners = append(slices.Clone(current.CoOwners), userID)
		current.RunPeople = slices.DeleteFunc(slices.Clone(current.RunPeople), func(id string) bool { return id == userID })
		current = withoutProjectUser(current, userID)
		projects[key] = current
		changed = true
		return true, nil
	})
	return changed, err
}

// RemoveCoOwner drops a co-owner. The main owner is not in this list and cannot be removed. Returns whether it changed.
func (r *projectOwnerRegistry) RemoveCoOwner(product, folder, userID string) (changed bool, err error) {
	userID = strings.TrimSpace(userID)
	err = r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(product, folder)
		current, exists := projects[key]
		if !exists {
			return false, fmt.Errorf("%s is not registered", key)
		}
		kept := slices.DeleteFunc(slices.Clone(current.CoOwners), func(id string) bool { return id == userID })
		if len(kept) == len(current.CoOwners) {
			return false, nil
		}
		current.CoOwners = kept
		projects[key] = current
		changed = true
		return true, nil
	})
	return changed, err
}

// AddUser records an account as a User of a registered project (crew_setup_lock.go). A User is not also a co-owner
// or one of the Run people. Adding the main owner changes nothing. Returns whether the record changed.
func (r *projectOwnerRegistry) AddUser(product, folder, userID string) (changed bool, err error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, errors.New("a user needs an account id")
	}
	err = r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(product, folder)
		current, exists := projects[key]
		if !exists {
			return false, fmt.Errorf("%s is not registered", key)
		}
		if current.OwnerID == userID || slices.Contains(current.Users, userID) {
			return false, nil
		}
		current.Users = append(slices.Clone(current.Users), userID)
		current.CoOwners = slices.DeleteFunc(slices.Clone(current.CoOwners), func(id string) bool { return id == userID })
		current.RunPeople = slices.DeleteFunc(slices.Clone(current.RunPeople), func(id string) bool { return id == userID })
		projects[key] = current
		changed = true
		return true, nil
	})
	return changed, err
}

// RemoveUser drops a User and their locked paths. Returns whether the record changed.
func (r *projectOwnerRegistry) RemoveUser(product, folder, userID string) (changed bool, err error) {
	userID = strings.TrimSpace(userID)
	err = r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(product, folder)
		current, exists := projects[key]
		if !exists {
			return false, fmt.Errorf("%s is not registered", key)
		}
		if !slices.Contains(current.Users, userID) {
			return false, nil
		}
		projects[key] = withoutProjectUser(current, userID)
		changed = true
		return true, nil
	})
	return changed, err
}

// SetUserLockedPaths replaces the extra locked paths of one current User.
func (r *projectOwnerRegistry) SetUserLockedPaths(product, folder, userID string, paths []string) error {
	userID = strings.TrimSpace(userID)
	return r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(product, folder)
		current, exists := projects[key]
		if !exists {
			return false, fmt.Errorf("%s is not registered", key)
		}
		if !slices.Contains(current.Users, userID) {
			return false, errors.New("that person is not a user of this project")
		}
		next := map[string][]string{}
		for id, p := range current.UserLockedPaths {
			next[id] = p
		}
		if len(paths) > 0 {
			next[userID] = append([]string{}, paths...)
		} else {
			delete(next, userID)
		}
		if len(next) == 0 {
			next = nil
		}
		current.UserLockedPaths = next
		projects[key] = current
		return true, nil
	})
}

func withoutProjectUser(rec projectOwnerRecord, userID string) projectOwnerRecord {
	rec.Users = slices.DeleteFunc(slices.Clone(rec.Users), func(id string) bool { return id == userID })
	if len(rec.Users) == 0 {
		rec.Users = nil
	}
	if _, ok := rec.UserLockedPaths[userID]; ok {
		next := map[string][]string{}
		for id, p := range rec.UserLockedPaths {
			if id != userID {
				next[id] = p
			}
		}
		if len(next) == 0 {
			next = nil
		}
		rec.UserLockedPaths = next
	}
	return rec
}

// migrateSetupLockedToUsers turns each co-owner whose setup was locked (the 24 Nov switch) into a User with the same
// extra locked paths. It only moves ids between lists of the same record; the next registry write persists it.
func migrateSetupLockedToUsers(rec projectOwnerRecord) projectOwnerRecord {
	for id, paths := range rec.LegacySetupLocked {
		if id == "" || id == rec.OwnerID {
			continue
		}
		rec.CoOwners = slices.DeleteFunc(slices.Clone(rec.CoOwners), func(existing string) bool { return existing == id })
		if !slices.Contains(rec.Users, id) {
			rec.Users = append(slices.Clone(rec.Users), id)
		}
		if len(paths) > 0 {
			next := map[string][]string{}
			for k, v := range rec.UserLockedPaths {
				next[k] = v
			}
			next[id] = append([]string{}, paths...)
			rec.UserLockedPaths = next
		}
	}
	rec.LegacySetupLocked = nil
	return rec
}

// Remove drops a project's entry (the project was deleted).
func (r *projectOwnerRegistry) Remove(product, folder string) error {
	return r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(product, folder)
		if _, exists := projects[key]; !exists {
			return false, nil
		}
		delete(projects, key)
		return true, nil
	})
}

// modify runs fn on the registry under an exclusive lock and writes the result atomically when fn reports a change.
func (r *projectOwnerRegistry) modify(fn func(map[string]projectOwnerRecord) (bool, error)) error {
	if r == nil || r.dir == "" {
		return fmt.Errorf("no state area for the project registry")
	}
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return err
	}
	lock, err := openProjectOwnerLock(filepath.Join(r.dir, ".lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lockProjectFile(lock); err != nil {
		return err
	}
	defer filelock.Unlock(lock)

	// Read fresh under the lock (the cache may be behind another process's write).
	r.mu.Lock()
	r.cached = nil
	r.mu.Unlock()
	projects, err := r.load()
	if err != nil {
		return fmt.Errorf("refusing to rewrite an unreadable registry: %w", err)
	}
	work := make(map[string]projectOwnerRecord, len(projects)+1)
	for key, rec := range projects {
		work[key] = rec
	}
	changed, err := fn(work)
	if err != nil || !changed {
		return err
	}
	encoded, err := json.MarshalIndent(projectOwnerFile{SchemaVersion: projectOwnerSchema, Projects: work}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeRegistryFileAtomically(r.path(), append(encoded, '\n')); err != nil {
		return err
	}
	r.mu.Lock()
	r.cached = nil
	r.mu.Unlock()
	return nil
}

// writeRegistryFileAtomically writes content to path through a new file in the same directory and a rename. The
// rename replaces whatever stands at path (a symlink there is replaced, never written through), and the new file
// is created exclusively with 0600.
func writeRegistryFileAtomically(path string, content []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf(".projects-%d-%d.tmp", os.Getpid(), time.Now().UnixNano())), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func registryListHas(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// SetAudience commits the audience as one registry change; concurrent requests
// never observe the previous selected people combined with a new mode.
func (r *projectOwnerRegistry) SetAudience(product, folder string, private bool, people []string, agents ...[]string) error {
	return r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(product, folder)
		current, exists := projects[key]
		if !exists {
			return false, fmt.Errorf("%s is not registered", key)
		}
		current.Private, current.AudienceConfigured = private, true
		current.RunPeople = append([]string(nil), people...)
		if len(agents) > 0 {
			current.SharedAgents = append([]string(nil), agents[0]...)
		}
		since := map[string]string{}
		if !private || len(people) > 0 {
			now := time.Now().UTC().Format(time.RFC3339Nano)
			for _, agent := range current.SharedAgents {
				since[agent] = current.SharedAgentSince[agent]
				if since[agent] == "" {
					since[agent] = now
				}
			}
		}
		current.SharedAgentSince = since
		projects[key] = current
		return true, nil
	})
}
