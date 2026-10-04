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

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
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
var productOwnerProducts = map[string]bool{"work": true, "code": true}

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

// projectIdentityOf parses a project root in any physical spelling (per-user Crew/Code, or Crew/<folder>).
func projectIdentityOf(root string) (projectIdentity, bool) {
	ref, ok := workspaceref.Parse(filepath.ToSlash(strings.TrimSpace(root)))
	if !ok {
		return projectIdentity{}, false
	}
	if folder, shared := ref.SharedProject(); shared {
		return projectIdentity{Product: "work", Folder: folder, Shared: true}, true
	}
	projectRoot, folder, isProject := ref.Project()
	if !isProject {
		return projectIdentity{}, false
	}
	id := projectIdentity{Folder: folder, PathOwner: ref.Owner()}
	switch projectRoot {
	case workspaceref.CrewProjectsRoot:
		id.Product = "work"
	case workspaceref.CodeProjectsRoot:
		id.Product = "code"
	default:
		return projectIdentity{}, false
	}
	return id, true
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

// stampProjectManifestSafely writes the owner as information into the project's manifest when it has none. The
// project is addressed by its owner's tree and folder; nothing outside that folder is ever touched.
func stampProjectManifestSafely(docs *os.Root, owner string, projectsRoot, folder string) (changed bool, err error) {
	project, err := openProjectDir(docs, owner, projectsRoot, folder)
	if err != nil {
		return false, err
	}
	defer project.Close()
	raw, perm, err := readProjectManifest(project)
	if err != nil {
		return false, err
	}
	stamped, changed, err := stampProductOwner(raw, owner)
	if err != nil || !changed {
		return false, err
	}
	return true, writeProjectManifest(project, stamped, perm)
}

// ensureProjectOwnerID records a project's owner when its owner opens it (callerID is the verified owner): in the
// registry (authoritative) and, best effort, as information in its manifest. A project that is not in the caller's
// own tree, or is already registered to someone else, is left alone.
func ensureProjectOwnerID(ctx context.Context, root, callerID string) {
	root = strings.TrimSuffix(strings.TrimSpace(root), "/")
	if root == "" || callerID == "" {
		return
	}
	// The caller's own tree only: a logical path maps under the caller, a physical one must already be theirs.
	ref := workspaceref.MustParse(filepath.ToSlash(root))
	if !ref.IsProject() || !ref.OwnedByOrUnowned(callerID) {
		return
	}
	id, ok := projectIdentityOf(ref.Physical(callerID))
	if !ok {
		return
	}
	owner := cleanManifestOwner(callerID)
	if registered, conflict := registeredOwnerConflict(id, owner); conflict {
		log.Printf("[OWNER_MISMATCH] %s/%s is registered to %q; not registering %q", id.Product, id.Folder, registered, owner)
		return
	}
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: id.Product, Folder: id.Folder, OwnerID: owner}); err != nil {
		log.Printf("[OWNER_REGISTRY] %s/%s: %v", id.Product, id.Folder, err)
		return
	}
	// Information only, written safely, and only when the docs folder is on this machine.
	docs, err := os.OpenRoot(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return
	}
	defer docs.Close()
	projectsRoot := workspaceref.CrewProjectsRoot
	if id.Product == "code" {
		projectsRoot = workspaceref.CodeProjectsRoot
	}
	if _, err := stampProjectManifestSafely(docs, owner, projectsRoot, id.Folder); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logUnsafeProjectPath(owner+"/"+projectsRoot+"/"+id.Folder, err)
	}
}

func logUnsafeProjectPath(where string, err error) {
	if errors.Is(err, errUnsafeProjectPath) {
		log.Printf("[UNSAFE_PATH] %s: %v; skipped", where, err)
		return
	}
	log.Printf("[OWNER_BACKFILL] %s: %v", where, err)
}

// productOwnerMigrationReport counts what one startup scan did.
type productOwnerMigrationReport struct {
	Scanned int
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
			if _, ok := registry.Lookup("work", folder); !ok {
				report.SharedOrphans++
				log.Printf("[CREW_ORPHAN] %s/%s has no owner in the registry: nobody can open it until the registry is restored or the owner is registered", workspaceref.SharedCrewRoot, folder)
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
		return report
	}
	defer docs.Close()
	users, err := listProjectDirNames(docs, workspaceref.UsersDir)
	if err != nil {
		return report
	}
	registry := defaultProjectOwners()
	for _, user := range users {
		if sanitizeUserIDForPath(user) != user {
			continue
		}
		for _, projectsRoot := range workspaceref.ProjectRoots {
			product := "work"
			if projectsRoot == workspaceref.CodeProjectsRoot {
				product = "code"
			}
			folders, err := listProjectNamesUnder(docs, user, projectsRoot)
			if err != nil {
				if errors.Is(err, errUnsafeProjectPath) {
					report.Unsafe++
					logUnsafeProjectPath(user+"/"+projectsRoot, err)
				}
				continue
			}
			for _, folder := range folders {
				report.Scanned++
				where := user + "/" + projectsRoot + "/" + folder
				project, err := openProjectDir(docs, user, projectsRoot, folder)
				if err != nil {
					if errors.Is(err, errUnsafeProjectPath) {
						report.Unsafe++
						logUnsafeProjectPath(where, err)
					} else {
						report.Skipped++
					}
					continue
				}
				raw, perm, err := readProjectManifest(project)
				if err != nil {
					_ = project.Close()
					if errors.Is(err, errUnsafeProjectPath) {
						report.Unsafe++
						logUnsafeProjectPath(where+"/product.json", err)
					} else {
						report.Skipped++
					}
					continue
				}
				existing, manifestProduct, ok := productManifestOwnerID(raw)
				if !ok || manifestProduct != product {
					_ = project.Close()
					report.Skipped++
					continue
				}
				var manifestID struct {
					ID string `json:"id"`
				}
				_ = json.Unmarshal([]byte(raw), &manifestID)
				id := projectIdentity{Product: product, Folder: folder, PathOwner: user}
				if registered, conflict := registeredOwnerConflict(id, user); conflict {
					report.Conflicts++
					log.Printf("[OWNER_MISMATCH] %s: the folder is registered to %q; this copy in %q's tree is not registered", where, registered, user)
					_ = project.Close()
					continue
				} else if registered != "" {
					report.Registry++
				} else if err := registry.Register(projectOwnerRecord{Product: product, Folder: folder, OwnerID: user, ProjectID: strings.TrimSpace(manifestID.ID)}); err != nil {
					report.Failures = append(report.Failures, fmt.Sprintf("%s: %v", where, err))
					_ = project.Close()
					continue
				} else {
					report.Registered++
				}
				if existing != "" {
					report.Current++
					if existing != user {
						report.Mismatched++
						log.Printf("[OWNER_MISMATCH] %s: manifest owner_id %q differs from the owner %q; the manifest is information only and is ignored", where, existing, user)
					}
					_ = project.Close()
					continue
				}
				stamped, changed, err := stampProductOwner(raw, user)
				if err != nil || !changed {
					report.Skipped++
					if err != nil {
						report.Failures = append(report.Failures, fmt.Sprintf("%s: %v", where, err))
					}
					_ = project.Close()
					continue
				}
				if err := writeProjectManifest(project, stamped, perm); err != nil {
					report.Failures = append(report.Failures, fmt.Sprintf("%s: %v", where, err))
				} else {
					report.Stamped++
				}
				_ = project.Close()
			}
		}
	}
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
