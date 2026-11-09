package workproduct

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// FolderGrant is an owner-approved external folder attached to a Crew
// conversation. It reuses the shared grant shape; only the storage owner
// differs (per-user Crew file, not a workflow.json).
type FolderGrant = workflowtypes.WorkflowFolderGrant

// Access levels reuse the shared folder-access vocabulary.
const (
	AccessReadOnly  = workflowtypes.FolderAccessReadOnly
	AccessReadWrite = workflowtypes.FolderAccessReadWrite
)

// AliasEnvKey converts a folder alias to the WORK_FOLDER_<KEY>
// environment suffix.
func AliasEnvKey(alias string) string {
	return workflowtypes.FolderAliasEnvKey(alias)
}

// ValidateRoot checks one administrator-configured allowed root: a
// host-absolute existing directory that is not the filesystem root.
func ValidateRoot(root string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(root))
	if !filepath.IsAbs(clean) || clean == filepath.VolumeName(clean)+string(filepath.Separator) {
		return "", fmt.Errorf("root must be an absolute directory path")
	}
	return workflowtypes.CanonicalizeFolderPath(clean)
}

// ValidateGrant checks a user-supplied external folder before it is
// stored. Beyond the shared canonicalization it requires an absolute
// path (never CWD-relative), a usable alias, and a known access level.
func ValidateGrant(path, alias, access string) (string, error) {
	cleanAlias := strings.TrimSpace(alias)
	if cleanAlias == "" {
		return "", fmt.Errorf("folder alias is required")
	}
	if AliasEnvKey(cleanAlias) == "" {
		return "", fmt.Errorf("folder alias %q has no usable characters", cleanAlias)
	}
	trimmedAccess := strings.TrimSpace(access)
	if trimmedAccess != AccessReadOnly && trimmedAccess != AccessReadWrite {
		return "", fmt.Errorf("unknown folder access %q", access)
	}
	clean := filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(clean) || clean == filepath.VolumeName(clean)+string(filepath.Separator) {
		return "", fmt.Errorf("folder path must be an absolute directory path")
	}
	return workflowtypes.CanonicalizeFolderPath(clean)
}

// PathWithinRoots reports whether a canonical absolute path falls inside
// one of the canonical allowed roots. Matching is separator-boundary
// safe: /data/proj2 is not inside /data/proj.
func PathWithinRoots(canonical string, roots []string) bool {
	clean := filepath.Clean(strings.TrimSpace(canonical))
	if !filepath.IsAbs(clean) {
		return false
	}
	for _, root := range roots {
		r := filepath.Clean(strings.TrimSpace(root))
		if r == "" || !filepath.IsAbs(r) {
			continue
		}
		if clean == r || strings.HasPrefix(clean, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// NormalizeRoots canonicalizes administrator-configured allowed roots,
// dropping anything unusable. A root that vanished must fail closed,
// never open.
func NormalizeRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		canonical, err := ValidateRoot(root)
		if err != nil {
			continue
		}
		out = append(out, canonical)
	}
	sort.Strings(out)
	return out
}

// ValidateGrantForUser is the attach authorization boundary: the path is
// validated and canonicalized, then -- unless the caller is an
// administrator -- required to fall inside the user's server-side allowed
// roots. The browser-supplied path is never authority; only canonical
// server-side state decides.
func ValidateGrantForUser(path, alias, access string, roots []string, isAdmin bool) (string, error) {
	canonical, err := ValidateGrant(path, alias, access)
	if err != nil {
		return "", err
	}
	if isAdmin {
		return canonical, nil
	}
	allowed := NormalizeRoots(roots)
	if len(allowed) == 0 {
		return "", fmt.Errorf("no folders are assigned to this user -- ask an administrator for access")
	}
	if !PathWithinRoots(canonical, allowed) {
		return "", fmt.Errorf("folder is outside the assigned workspace roots")
	}
	return canonical, nil
}

// ResolveGrants turns stored grants into guard inputs plus
// WORK_FOLDER_<ALIAS> session env. Stored paths are trusted
// (canonicalized at write time); use FolderGrantAvailable to report
// liveness separately.
func ResolveGrants(grants []FolderGrant) (read, write, readOnly []string, env map[string]string) {
	read, write, readOnly, env = workflowtypes.ResolveFolderGrants(grants, "WORK_FOLDER_")
	return read, write, readOnly, env
}

// FolderGrantAvailable reports whether a stored grant path still resolves
// to a live directory, for UI availability display.
func FolderGrantAvailable(path string) bool {
	return workflowtypes.FolderGrantAvailable(path)
}

// BuildAttachedFoldersPrompt renders the "### Attached Folders" system
// prompt section listing the resolved grants.
func BuildAttachedFoldersPrompt(grants []FolderGrant) string {
	return workflowtypes.FolderGrantsPrompt(grants, "WORK_FOLDER_", "No folders are attached. When the person asks for a folder on their computer, attach it with files action=folder_attach; it applies from their next message.\n")
}
