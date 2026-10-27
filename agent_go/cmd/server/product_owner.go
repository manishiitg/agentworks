package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// PLAT-442 step 1 / PLAT-449 / PLAT-450: who owns a Crew or a Code.
//
// The owner comes from the server-controlled registry (project_owner_registry.go), else from the physical path;
// NEVER from product.json, which users and agent turns can edit. product.json's `owner_id` is still written (so
// a person reading the folder can see it) but only as information: nothing trusts it, a disagreement is logged
// ([OWNER_MISMATCH]) and the registry or path wins.
//
// Every file this code reads or writes under the docs root is reached through anchored opens (os.Root): the
// docs root, then `_users`, then the user's directory, then each folder of the project's path, each checked with
// lstat to be a real directory and never a symlink, and the manifest itself checked to be a regular file. The
// final write goes to a new file in the project's directory and is renamed over product.json, so a symlink standing
// at product.json is replaced, never written through. A symlink anywhere on the way is logged ([UNSAFE_PATH]) and
// the project is skipped (PLAT-450: a bait symlink in one user's tree to another user's manifest used to make the
// backfill stamp the victim's manifest with the attacker's id).

// productOwnerProducts are the product ids whose product.json carries owner_id.
var productOwnerProducts = map[string]bool{"work": true}

// productOwnerFromPath is the owner a project's physical path names ("" for a logical or shared-root path).
func productOwnerFromPath(root string) string {
	ref, ok := workspaceref.Parse(filepath.ToSlash(strings.TrimSpace(root)))
	if !ok || !ref.HasOwner() || !ref.IsProject() {
		return ""
	}
	return ref.Owner()
}

// projectIdentity is what a project root names: the product, its folder name, who the path says owns it ("" at the
// shared root) and whether it is at the shared Crew root.
type projectIdentity struct {
	Product   string
	Folder    string
	PathOwner string
	Shared    bool
}

// projectIdentityOf recognizes the canonical Crew root.
func projectIdentityOf(root string) (projectIdentity, bool) {
	ref, ok := workspaceref.Parse(filepath.ToSlash(strings.TrimSpace(root)))
	if !ok {
		return projectIdentity{}, false
	}
	if folder, shared := ref.SharedProject(); shared {
		if rec, found := defaultProjectOwners().sharedRootRecord(folder); found {
			return projectIdentity{Product: rec.Product, Folder: folder, Shared: true}, true
		}
		return projectIdentity{Product: "work", Folder: folder, Shared: true}, true
	}
	return projectIdentity{}, false
}

// resolveProjectOwner returns the owner of the Crew or Code at root: the registry's, else the path's. It never reads
// the project's files. For a project at the shared Crew root with no registry entry the owner is "" (nobody's).
func resolveProjectOwner(_ context.Context, root string) string {
	id, ok := projectIdentityOf(root)
	if !ok {
		return ""
	}
	return resolveProjectOwnerOf(id)
}

func resolveProjectOwnerOf(id projectIdentity) string {
	if id.Shared {
		rec, found := defaultProjectOwners().sharedRootRecord(id.Folder)
		if !found || rec.Product != id.Product {
			return ""
		}
		return rec.OwnerID
	}
	rec, found := defaultProjectOwners().Lookup(id.Product, id.Folder)
	if !found {
		return id.PathOwner
	}
	if id.PathOwner != "" && id.PathOwner != rec.OwnerID {
		log.Printf("[OWNER_MISMATCH] %s/%s is registered to %q but this copy lives in %q's tree; the registry wins", id.Product, id.Folder, rec.OwnerID, id.PathOwner)
	}
	return rec.OwnerID
}

// registeredOwnerConflict reports a project whose registry entry names someone other than the owner the path
// admits: a copied or planted folder. An unregistered project has no conflict.
func registeredOwnerConflict(id projectIdentity, admittedOwner string) (registered string, conflict bool) {
	rec, found := defaultProjectOwners().Lookup(id.Product, id.Folder)
	if !found {
		return "", false
	}
	return rec.OwnerID, rec.OwnerID != cleanManifestOwner(admittedOwner)
}

// productManifestOwnerID reads owner_id from raw product.json content (informational); ok is false when the
// manifest is not a Crew or Code manifest.
func productManifestOwnerID(raw string) (owner string, product string, ok bool) {
	var manifest struct {
		Product string `json:"product"`
		OwnerID string `json:"owner_id"`
	}
	if json.Unmarshal([]byte(raw), &manifest) != nil {
		return "", "", false
	}
	product = strings.ToLower(strings.TrimSpace(manifest.Product))
	if !productOwnerProducts[product] {
		return "", product, false
	}
	return cleanManifestOwner(manifest.OwnerID), product, true
}

// cleanManifestOwner turns an owner id into a path-safe id; an absent one stays empty (sanitizeUserIDForPath alone
// would turn it into "default").
func cleanManifestOwner(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return sanitizeUserIDForPath(raw)
}

// stampProductOwner returns raw with owner_id set to owner when it has none. changed is false when the manifest
// already carries an owner_id (never overwritten), is not a Crew or Code manifest, or owner is empty.
func stampProductOwner(raw, owner string) (stamped string, changed bool, err error) {
	owner = cleanManifestOwner(owner)
	if owner == "" {
		return raw, false, nil
	}
	existing, _, ok := productManifestOwnerID(raw)
	if !ok || existing != "" {
		return raw, false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return raw, false, err
	}
	encodedOwner, _ := json.Marshal(owner)
	fields["owner_id"] = encodedOwner
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return raw, false, err
	}
	return string(out) + "\n", true, nil
}

// ---- anchored, symlink-refusing access to a project folder under the docs root ----

// errUnsafeProjectPath marks a path that has a symlink or a non-directory where a real directory must be.
var errUnsafeProjectPath = errors.New("unsafe path")

const maxProjectManifestBytes = 4 << 20

// openProjectDir opens <docsRoot>/_users/<user>/<rel...> as an os.Root, refusing a symlink or a non-directory at
// every step. The returned root confines every later operation to that directory (os.Root refuses a path that
// leaves it, whatever links lie on the way), so a link planted after the check cannot lead out of the project.
// A missing folder is fs.ErrNotExist.
func openProjectDir(docs *os.Root, user string, rel ...string) (*os.Root, error) {
	step := func(parent *os.Root, name string) (*os.Root, error) {
		info, err := parent.Lstat(name)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%w: %s is not a real directory", errUnsafeProjectPath, name)
		}
		return parent.OpenRoot(name)
	}
	users, err := step(docs, workspaceref.UsersDir)
	if err != nil {
		return nil, err
	}
	defer users.Close()
	current, err := step(users, user)
	if err != nil {
		return nil, err
	}
	for _, segment := range strings.Split(strings.Join(rel, "/"), "/") {
		if segment == "" {
			continue
		}
		next, err := step(current, segment)
		_ = current.Close()
		if err != nil {
			return nil, err
		}
		current = next
	}
	return current, nil
}

// readProjectManifest reads <project>/product.json when it is a regular file.
func readProjectManifest(project *os.Root) (string, fs.FileMode, error) {
	info, err := project.Lstat("product.json")
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%w: product.json is not a regular file", errUnsafeProjectPath)
	}
	if info.Size() > maxProjectManifestBytes {
		return "", 0, fmt.Errorf("product.json is larger than %d bytes", maxProjectManifestBytes)
	}
	file, err := project.Open("product.json")
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxProjectManifestBytes+1))
	if err != nil {
		return "", 0, err
	}
	return string(raw), info.Mode().Perm(), nil
}

// writeProjectManifest replaces <project>/product.json with content: a new file in the project directory,
// created exclusively, then renamed over the manifest. Whatever stands at product.json (a symlink included) is
// replaced and never written through.
func writeProjectManifest(project *os.Root, content string, perm fs.FileMode) error {
	tmp := fmt.Sprintf(".product.json.%d.%d.tmp", os.Getpid(), time.Now().UnixNano())
	file, err := project.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = project.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = project.Remove(tmp)
		return err
	}
	if err := project.Rename(tmp, "product.json"); err != nil {
		_ = project.Remove(tmp)
		return err
	}
	return nil
}

// productOwnerMigrationReport counts what one startup scan did.
type productOwnerMigrationReport struct {
	AudienceBaselineReady bool
	Scanned               int
	// Registered counts registry entries written by this scan; Registry counts projects already registered.
	Registered int
	Registry   int
	// Stamped counts manifests that got an informational owner_id; Current those that already had one.
	Stamped int
	Current int
	Skipped int
	// Mismatched counts manifests whose owner_id differs from the registry/path owner (ignored, logged).
	Mismatched int
	// Unsafe counts projects skipped because a symlink or non-directory stood on the way.
	Unsafe int
	// Conflicts counts projects whose folder name is registered to a different owner (a copy; not registered).
	Conflicts int
	// SharedOrphans counts folders at the shared Crew root that the registry does not know: nobody's, so nobody can open
	// them (restore the registry from a backup, or register the owner after confirming who it is).
	SharedOrphans int
	Failures      []string
}

// migrateProductOwners registers every Crew and Code under docsDir/_users/<id>/Chats/{Work,Code}/projects/<project>
// in the owner registry from the PHYSICAL PATH, and writes owner_id into manifests that lack one as information.
// Idempotent, never changes a registered owner or an existing owner_id, moves nothing, and never follows a
// symlink (see the file comment).
func migrateProductOwners(docsDir string) productOwnerMigrationReport {
	report := scanProductOwners(docsDir)
	// A Crew at the shared root that the registry does not know belongs to nobody (the registry is its only owner source).
	if docs, err := os.OpenRoot(docsDir); err == nil {
		defer docs.Close()
		registry := defaultProjectOwners()
		for _, folder := range dirNames(docs, workspaceref.SharedCrewRoot) {
			if _, ok := registry.sharedRootRecord(folder); !ok {
				report.SharedOrphans++
				log.Printf("[CREW_ORPHAN] %s/%s has no owner in the registry: nobody can open it until the registry is restored or the owner is registered", workspaceref.SharedCrewRoot, folder)
			}
		}
	}
	if report.AudienceBaselineReady {
		registry := defaultProjectOwners()
		if registry.dir != "" {
			if err := os.MkdirAll(registry.dir, 0o700); err == nil {
				file, createErr := os.OpenFile(filepath.Join(registry.dir, "audience-baseline-v1"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
				if createErr == nil {
					_ = file.Close()
				} else if !errors.Is(createErr, os.ErrExist) {
					report.Failures = append(report.Failures, createErr.Error())
				}
			} else {
				report.Failures = append(report.Failures, err.Error())
			}
		}
	}
	return report
}

// scanProductOwners is the scan of the per-user trees (see migrateProductOwners).
func scanProductOwners(docsDir string) productOwnerMigrationReport {
	report := productOwnerMigrationReport{}
	docs, err := os.OpenRoot(docsDir)
	if err != nil {
		report.Failures = append(report.Failures, err.Error())
		return report
	}
	defer docs.Close()
	_, err = listProjectDirNames(docs, workspaceref.UsersDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		report.Failures = append(report.Failures, err.Error())
		return report
	}
	report.AudienceBaselineReady = true

	return report
}

// listProjectDirNames lists the real (non-symlink) directories directly under docs/<name>.
func listProjectDirNames(docs *os.Root, name string) ([]string, error) {
	info, err := docs.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a real directory", errUnsafeProjectPath, name)
	}
	sub, err := docs.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	defer sub.Close()
	return listRealDirs(sub)
}

// listProjectNamesUnder lists the real project folders of docs/_users/<user>/<projectsRoot>. A missing projects
// root is fs.ErrNotExist; a symlink on the way is errUnsafeProjectPath.
func listProjectNamesUnder(docs *os.Root, user, projectsRoot string) ([]string, error) {
	dir, err := openProjectDir(docs, user, projectsRoot)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return listRealDirs(dir)
}

// listRealDirs lists the entries of root that are real directories, sorted. A symlink entry (to anything) is
// reported unsafe by name and skipped.
func listRealDirs(root *os.Root) ([]string, error) {
	handle, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	entries, err := handle.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		info, err := root.Lstat(entry.Name())
		if err != nil {
			continue
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			log.Printf("[UNSAFE_PATH] %s: a symlink where a project folder is expected; skipped", entry.Name())
		case info.IsDir():
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// dirNames lists the real directories directly below docs/rel (symlinks are not followed or listed).
func dirNames(docs *os.Root, rel string) []string {
	info, err := docs.Lstat(rel)
	if err != nil || !info.IsDir() {
		return nil
	}
	handle, err := docs.Open(rel)
	if err != nil {
		return nil
	}
	defer handle.Close()
	names, err := handle.Readdirnames(-1)
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range names {
		if info, err := docs.Lstat(rel + "/" + name); err == nil && info.IsDir() && !strings.HasPrefix(name, ".") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
