package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
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
	Product string `json:"product"` // "work" or "code"
	Folder  string `json:"folder"`  // <slug>-<id8>, the project's folder name (kept when a Crew moves)
	OwnerID string `json:"owner_id"`
	// ProjectID is the manifest's id when the entry was written: informational (the manifest is user-writable).
	ProjectID string `json:"project_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	// Shared marks a Crew that lives at Crew/<folder> (moved there by the Crew move or created there).
	Shared bool `json:"shared,omitempty"`
	// Aliases are the old physical roots of a moved Crew (_users/<owner>/Chats/Work/projects/<folder>).
	Aliases []string `json:"aliases,omitempty"`
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

	mu     sync.Mutex
	cached map[string]projectOwnerRecord
	stamp  fileStamp
}

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
	r.cached, r.stamp = file.Projects, stamp
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
	return r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(rec.Product, rec.Folder)
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
	lock, err := os.OpenFile(filepath.Join(r.dir, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

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
