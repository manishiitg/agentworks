package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// PLAT-442 step 1: a Crew's or Code's owner is written in its product.json
// (`owner_id`), not only implied by the folder it lives in. Today the folder is
// `_users/<owner>/Chats/{Work,Code}/projects/<id>`, so the two agree; once Crews
// move to `Crew/<id>` the path names no owner and the manifest is all there is.
//
// Reads prefer the manifest and fall back to the path; a disagreement is logged
// ([OWNER_MISMATCH]) and never fails a request. Existing manifests are backfilled
// from the physical path, lazily when the owner opens the project and by a startup
// scan. A backfill never overwrites an existing owner_id and never moves data.

// productOwnerProducts are the product ids whose product.json carries owner_id.
var productOwnerProducts = map[string]bool{"work": true, "code": true}

// productOwnerFromPath is the owner a project's physical path names ("" for a
// logical or shared-root path). It is the path rule the manifest replaces.
func productOwnerFromPath(root string) string {
	ref, ok := workspaceref.Parse(filepath.ToSlash(strings.TrimSpace(root)))
	if !ok || !ref.HasOwner() || !ref.IsProject() {
		return ""
	}
	return ref.Owner()
}

// productManifestOwnerID reads owner_id from raw product.json content; ok is false
// when the manifest is not a Crew or Code manifest.
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

// cleanManifestOwner turns a manifest's owner_id into a path-safe id; an absent one stays
// empty (sanitizeUserIDForPath alone would turn it into "default").
func cleanManifestOwner(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return sanitizeUserIDForPath(raw)
}

// pickProjectOwner is the one rule: the manifest's owner when it has one, else the
// path's. mismatch is true when both exist and differ.
func pickProjectOwner(manifestOwner, pathOwner string) (owner string, mismatch bool) {
	if manifestOwner != "" {
		return manifestOwner, pathOwner != "" && pathOwner != manifestOwner
	}
	return pathOwner, false
}

// stampProductOwner returns raw with owner_id set to owner when it has none. changed
// is false when the manifest already carries an owner_id (never overwritten), is not a
// Crew or Code manifest, or owner is empty.
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

// ensureProjectOwnerID backfills owner_id lazily when the owner opens a project
// (callerID is the verified owner). Best effort: a failure is logged, never returned.
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
	root = ref.Physical(callerID)
	manifestPath := root + "/product.json"
	raw, found, err := readFileFromWorkspace(ctx, manifestPath)
	if err != nil || !found {
		return
	}
	stamped, changed, err := stampProductOwner(raw, callerID)
	if err != nil || !changed {
		return
	}
	if err := writeFileToWorkspace(ctx, manifestPath, stamped); err != nil {
		log.Printf("[OWNER_BACKFILL] %s: could not write owner_id: %v", manifestPath, err)
		return
	}
	log.Printf("[OWNER_BACKFILL] %s: owner_id set from the owner opening the project", manifestPath)
}

// productOwnerMigrationReport counts what one startup scan did.
type productOwnerMigrationReport struct {
	Scanned    int
	Stamped    int
	Current    int
	Skipped    int
	Mismatched int
	Failures   []string
}

// migrateProductOwners stamps owner_id on every Crew and Code manifest under
// docsDir/_users/<id>/Chats/{Work,Code}/projects/<project>/product.json from the
// physical path. Idempotent, never overwrites an owner_id, moves nothing. A manifest
// whose owner_id disagrees with its path is counted and logged, not changed.
func migrateProductOwners(docsDir string) productOwnerMigrationReport {
	report := productOwnerMigrationReport{}
	usersRoot := filepath.Join(docsDir, workspaceref.UsersDir)
	users, err := os.ReadDir(usersRoot)
	if err != nil {
		return report
	}
	for _, user := range users {
		if !user.IsDir() || sanitizeUserIDForPath(user.Name()) != user.Name() {
			continue
		}
		for _, projectsRoot := range workspaceref.ProjectRoots {
			projects, err := os.ReadDir(filepath.Join(usersRoot, user.Name(), filepath.FromSlash(projectsRoot)))
			if err != nil {
				continue
			}
			for _, project := range projects {
				if !project.IsDir() {
					continue
				}
				manifestPath := filepath.Join(usersRoot, user.Name(), filepath.FromSlash(projectsRoot), project.Name(), "product.json")
				report.Scanned++
				raw, err := os.ReadFile(manifestPath)
				if err != nil {
					report.Skipped++
					continue
				}
				existing, _, ok := productManifestOwnerID(string(raw))
				if !ok {
					report.Skipped++
					continue
				}
				if existing != "" {
					report.Current++
					if existing != user.Name() {
						report.Mismatched++
						log.Printf("[OWNER_MISMATCH] %s: owner_id %q differs from the path owner %q", manifestPath, existing, user.Name())
					}
					continue
				}
				stamped, changed, err := stampProductOwner(string(raw), user.Name())
				if err != nil || !changed {
					report.Skipped++
					if err != nil {
						report.Failures = append(report.Failures, fmt.Sprintf("%s: %v", manifestPath, err))
					}
					continue
				}
				if err := os.WriteFile(manifestPath, []byte(stamped), 0o644); err != nil {
					report.Failures = append(report.Failures, fmt.Sprintf("%s: %v", manifestPath, err))
					continue
				}
				report.Stamped++
			}
		}
	}
	return report
}

// resolveProjectOwner returns the owner of the Crew or Code at root (a project
// root in any spelling the caller knows is physical): manifest first, path second.
func resolveProjectOwner(ctx context.Context, root string) string {
	pathOwner := productOwnerFromPath(root)
	manifestOwner := ""
	if raw, found, err := readFileFromWorkspace(ctx, strings.TrimSuffix(root, "/")+"/product.json"); err == nil && found {
		manifestOwner, _, _ = productManifestOwnerID(raw)
	}
	owner, mismatch := pickProjectOwner(manifestOwner, pathOwner)
	if mismatch {
		log.Printf("[OWNER_MISMATCH] %s: product.json owner_id %q differs from the path owner %q; using the manifest", root, manifestOwner, pathOwner)
	}
	return owner
}
