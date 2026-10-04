package utils

import (
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// IsValidFilePath validates that the file path is safe and within the docs directory
func IsValidFilePath(filePath, docsDir string) bool {
	cleanPath, err := filepath.Abs(filepath.Clean(filePath))
	if err != nil {
		return false
	}
	cleanDocsDir, err := filepath.Abs(filepath.Clean(docsDir))
	if err != nil || !pathWithinRoot(cleanPath, cleanDocsDir) {
		return false
	}

	resolvedRoot, err := filepath.EvalSymlinks(cleanDocsDir)
	if err != nil {
		// The server must establish its workspace root before serving paths. If the
		// root cannot be resolved, no containment guarantee can be made.
		return false
	}
	resolvedCandidate, err := resolveExistingPathPrefix(cleanPath, cleanDocsDir)
	if err != nil || !pathWithinRoot(resolvedCandidate, resolvedRoot) {
		return false
	}
	// A symlink never carries a path from one user's tree into another's.
	if realOwner, inUser := userTreeOwner(resolvedRoot, resolvedCandidate); inUser {
		lexOwner, lexIn := userTreeOwner(cleanDocsDir, cleanPath)
		if realOwner != "" {
			if !lexIn || lexOwner != realOwner {
				return false
			}
		} else if lexIn && lexOwner != "" {
			// The path resolved no further than the _users folder itself. That is fine while the path does
			// not exist yet (a user's first file: nothing below _users/<id> is there to follow), but a
			// link that exists and points at _users would list every account.
			if _, statErr := os.Lstat(cleanPath); statErr == nil {
				return false
			}
		}
	}
	return true
}

func resolveExistingPathPrefix(candidate, root string) (string, error) {
	current := candidate
	for {
		if _, err := os.Lstat(current); err == nil {
			return filepath.EvalSymlinks(current)
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if current == root {
			return filepath.EvalSymlinks(root)
		}
		parent := filepath.Dir(current)
		if parent == current || !pathWithinRoot(parent, root) {
			return "", fmt.Errorf("path %q escapes workspace root %q", candidate, root)
		}
		current = parent
	}
}

func pathWithinRoot(candidate, root string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// GetRelativePath converts a full internal path to a relative path for API responses
// This ensures that internal directory structure (like /app/workspace-docs) is never exposed
func GetRelativePath(fullPath, docsDir string) (string, error) {
	return filepath.Rel(docsDir, fullPath)
}

// SanitizeInputPath sanitizes input filepaths by stripping internal directory prefixes
// This ensures that users can pass either relative paths or full paths, and we always get clean relative paths
func SanitizeInputPath(inputPath, docsDir string) string {
	// Clean the input path
	cleanInput := filepath.Clean(inputPath)
	cleanDocsDir := filepath.Clean(docsDir)

	// If the input path starts with the docs directory, strip it
	if strings.HasPrefix(cleanInput, cleanDocsDir) {
		// Remove the docs directory prefix and any leading path separators
		relativePath := strings.TrimPrefix(cleanInput, cleanDocsDir)
		relativePath = strings.TrimPrefix(relativePath, string(filepath.Separator))
		return relativePath
	}

	// If it's already a relative path, return it as is
	return cleanInput
}

// --- User ID Utilities ---
// These utilities support user identification for auth/database purposes.
// Per-user folders (Chats/, Downloads/) are routed to _users/{userID}/ for isolation.

// UsersDirectory is kept for backwards compatibility (e.g. migration from old layout)
const UsersDirectory = workspaceref.UsersDir

// defaultUserID is the resolved default user ID (lazy-initialized from env)
var (
	defaultUserID     string
	defaultUserIDOnce sync.Once
)

// GetDefaultUserID returns the default user ID, reading from DEFAULT_USER_ID
// env var if set, otherwise falling back to "default".
func GetDefaultUserID() string {
	defaultUserIDOnce.Do(func() {
		if id := os.Getenv("DEFAULT_USER_ID"); id != "" {
			defaultUserID = id
		} else {
			defaultUserID = "default"
		}
	})
	return defaultUserID
}

// validUserIDRegex matches allowed user ID characters (alphanumeric, hyphens, underscores)
var validUserIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// IsValidUserID checks if a user ID contains only allowed characters
func IsValidUserID(userID string) bool {
	if userID == "" {
		return false
	}
	// Max length check
	if len(userID) > 128 {
		return false
	}
	return validUserIDRegex.MatchString(userID)
}

// SanitizeUserID returns a sanitized user ID or the default user ID if invalid
func SanitizeUserID(userID string) string {
	if userID == "" || !IsValidUserID(userID) {
		return GetDefaultUserID()
	}
	return userID
}

// perUserPrefixes are top-level folders that are per-user isolated.
// Requests to these folders are routed to _users/{userID}/{folder}.
var perUserPrefixes = []string{"Chats", "Downloads", "chat_history", "memories"}

// PerUserPrefixes returns the list of per-user folder prefixes.
func PerUserPrefixes() []string {
	return perUserPrefixes
}

// IsPerUserPath returns true if the given relative path falls under a per-user folder.
func IsPerUserPath(relPath string) bool {
	for _, prefix := range perUserPrefixes {
		if relPath == prefix || strings.HasPrefix(relPath, prefix+"/") {
			return true
		}
	}
	return false
}

// ResolveUserPath resolves a requested path to the actual filesystem path.
// Per-user folders (Chats/, Downloads/) are routed to _users/{userID}/.
// Shared folders (skills/, Workflow/, etc.) resolve to docsDir directly.
func ResolveUserPath(docsDir, requestedPath, userID string) (string, error) {
	cleanPath := SanitizeInputPath(requestedPath, docsDir)

	var resolved string
	if IsPerUserPath(cleanPath) {
		sanitizedUID := SanitizeUserID(userID)
		resolved = filepath.Join(docsDir, filepath.FromSlash(workspaceref.PhysicalPathOf(sanitizedUID, filepath.ToSlash(cleanPath))))
	} else {
		resolved = filepath.Join(docsDir, cleanPath)
	}

	// Containment: reject any request that escapes the workspace root — e.g.
	// "../agent_go/server.go" survives filepath.Clean with its ".." intact and
	// would otherwise resolve to a sibling of docsDir. filepath.Join cleans
	// the result, so a prefix check against the cleaned root is sufficient.
	// See docs/bugs/workspace_docs_path_inside_repo.md (path containment).
	cleanRoot := filepath.Clean(docsDir)
	if resolved != cleanRoot && !strings.HasPrefix(resolved, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace root", requestedPath)
	}
	if !IsValidFilePath(resolved, docsDir) {
		return "", fmt.Errorf("path %q resolves outside the workspace root", requestedPath)
	}

	// Who may read a given user's tree is decided above this API (the app server authorizes the caller
	// and stamps X-User-ID: Code collaborators, Crew owners and administrators legitimately read another
	// user's folder), so a path is not refused for naming another user. What is refused, in
	// IsValidFilePath, is a symlink carrying a path from one tree into another, which would let a link
	// planted in the caller's own folder read someone else's (docs/DECISIONS.md, 2026-10-01).
	return resolved, nil
}

// userTreeOwner returns the user id of the _users/<id>/ tree that path lies in, if it lies in one.
// A path directly at _users/ (no id) belongs to no user, and is reported with an empty owner.
func userTreeOwner(root, path string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return "", false
	}
	ref := workspaceref.MustParse(filepath.ToSlash(rel))
	if ref.IsUsersRoot() {
		return "", true
	}
	if ref.HasOwner() {
		return ref.Owner(), true
	}
	return "", false
}

// ConvertToUserRelativePath converts an absolute path back to a relative path
// for API responses. Strips _users/{userID}/ prefix so callers see clean paths.
func ConvertToUserRelativePath(fullPath, docsDir string) (string, error) {
	rel, err := filepath.Rel(docsDir, fullPath)
	if err != nil {
		return "", err
	}
	// Strip _users/{userID}/ prefix if present: _users/{userID}/Chats/... → Chats/...
	// (_users/{userID} with no sub-path shouldn't happen in normal use: "").
	if ref := workspaceref.MustParse(filepath.ToSlash(rel)); ref.HasOwner() {
		return ref.Logical(), nil
	}
	return rel, nil
}
