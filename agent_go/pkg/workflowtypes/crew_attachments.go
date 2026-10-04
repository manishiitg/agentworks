package workflowtypes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// CrewAttachment exposes one Crew project's workspace to a workflow under a
// stable alias, read-only. Attachments live in workflow.json next to the
// host-local folder_access grants, but unlike those grants they reference
// workspace paths (resolved once at attach time), never host directories.
// Downstream steps read crew files as "<alias>/<crew-relative path>".
type CrewAttachment struct {
	ID                string `json:"id"`
	Alias             string `json:"alias"`
	CrewProfileID     string `json:"crew_profile_id"`
	CrewProjectID     string `json:"crew_project_id"`
	CrewWorkspacePath string `json:"crew_workspace_path"`
	CreatedAt         string `json:"created_at,omitempty"`
}

// crewAttachmentAliasPattern keeps aliases usable as a path segment and as
// an environment suffix. Aliases are matched exactly (case-sensitive).
var crewAttachmentAliasPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-_]{0,63}$`)

// ReservedCrewAttachmentAliases are top-level workflow names an attachment
// alias must never shadow; reads under them always resolve inside the
// workflow. The read path uses the same list to skip the manifest lookup
// for ordinary paths.
var ReservedCrewAttachmentAliases = []string{
	"runs", "planning", "skills", "notes", "knowledgebase", "db",
	"variables", "logs", "workflow.json",
}

// ValidateCrewAttachmentAlias rejects empty, malformed, and shadowing aliases.
// The reserved list is checked before the charset pattern so reserved names
// always get the specific shadowing error, even ones like workflow.json
// that could never be a valid alias anyway.
func ValidateCrewAttachmentAlias(alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return errCrewAttachmentAlias("alias is required")
	}
	for _, reserved := range ReservedCrewAttachmentAliases {
		if alias == reserved {
			return errCrewAttachmentAlias("alias must not shadow the workflow path " + reserved)
		}
	}
	if !crewAttachmentAliasPattern.MatchString(alias) {
		return errCrewAttachmentAlias("alias must be 1-64 lowercase letters, digits, dashes, or underscores")
	}
	return nil
}

type crewAttachmentAliasError struct{ msg string }

func errCrewAttachmentAlias(msg string) error { return crewAttachmentAliasError{msg} }

func (e crewAttachmentAliasError) Error() string { return "invalid crew attachment alias: " + e.msg }

// ValidateCrewAttachmentBinding verifies one attachment's stored binding
// without touching the filesystem: the alias must be valid, a crew project
// and workspace path must be named, and the path must sit under a projects
// directory (so a hand-edited manifest cannot retarget the alias at an
// arbitrary path). It deliberately does not couple the path to the project
// ID: crew directories are named <slug>-<id-prefix>, so the last segment
// never equals a UUID project ID. Exact binding is enforced server-side by
// comparing the stored root against the freshly authorized project binding
// (run preflight and session grants); this shape check is the local,
// service-free companion for read paths.
func ValidateCrewAttachmentBinding(attachment CrewAttachment) error {
	if err := ValidateCrewAttachmentAlias(attachment.Alias); err != nil {
		return err
	}
	projectID := strings.TrimSpace(attachment.CrewProjectID)
	root := strings.Trim(strings.TrimSpace(attachment.CrewWorkspacePath), "/")
	if projectID == "" || root == "" {
		return errCrewAttachmentAlias("attachment must name a crew project and workspace path")
	}
	segments := strings.Split(filepath.ToSlash(foldCrewRoot(root)), "/")
	if len(segments) < 2 {
		return errCrewAttachmentAlias("attachment workspace path is not a crew workspace")
	}
	// A Crew at the shared root: exactly Crew/<folder> (PLAT-442 step 4).
	if len(segments) == 2 && segments[0] == "Crew" && segments[1] != "" && !strings.HasPrefix(segments[1], ".") {
		return nil
	}
	for _, segment := range segments[:len(segments)-1] {
		if segment == "projects" {
			return nil
		}
	}
	return errCrewAttachmentAlias("attachment workspace path is not a crew workspace")
}

// CanonicalCrewAttachmentRoot normalizes a stored or freshly resolved crew
// root so the two can be compared for exact equality: slash separators,
// trimmed whitespace, no leading or trailing slashes.
func CanonicalCrewAttachmentRoot(path string) string {
	return foldCrewRoot(strings.Trim(filepath.ToSlash(strings.TrimSpace(path)), "/"))
}

// ValidateCrewAttachmentRoot verifies one attachment before its root is
// granted or resolved: the binding must validate and the root must still
// exist under the docs root (a deleted crew fails closed). Callers re-run
// this on every read and every grant; attachments are only as trustworthy
// as their last validation.
func ValidateCrewAttachmentRoot(attachment CrewAttachment, docsRoot string) error {
	if err := ValidateCrewAttachmentBinding(attachment); err != nil {
		return err
	}
	root := foldCrewRoot(strings.Trim(strings.TrimSpace(attachment.CrewWorkspacePath), "/"))
	info, err := os.Stat(filepath.Join(docsRoot, filepath.FromSlash(root)))
	if err != nil || !info.IsDir() {
		return errCrewAttachmentAlias("attached crew workspace is unavailable")
	}
	return nil
}

// CrewAttachmentEnvKeys maps validated attachments to WORKFLOW_CREW_*
// session env keys tracking their granted roots. Keys derive from the
// alias (uppercased, dashes to underscores) with numeric disambiguation,
// sorted by alias so the mapping is deterministic.
func CrewAttachmentEnvKeys(attachments []CrewAttachment) map[string]string {
	ordered := append([]CrewAttachment(nil), attachments...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Alias < ordered[j].Alias })
	env := make(map[string]string, len(ordered))
	used := make(map[string]bool, len(ordered))
	for _, attachment := range ordered {
		base := "WORKFLOW_CREW_" + strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(attachment.Alias), "-", "_"))
		key := base
		for n := 2; used[key]; n++ {
			key = base + "_" + strconv.Itoa(n)
		}
		used[key] = true
		env[key] = foldCrewRoot(strings.Trim(strings.TrimSpace(attachment.CrewWorkspacePath), "/"))
	}
	return env
}

// ReadCrewAttachments loads the crew attachments declared in a workflow's
// manifest from the local docs root. It reports found=false when no
// manifest exists there; a manifest without attachments returns an empty
// list with found=true.
func ReadCrewAttachments(docsRoot, workspacePath string) ([]CrewAttachment, bool) {
	manifestPath := filepath.Join(docsRoot, filepath.FromSlash(strings.TrimSpace(workspacePath)), "workflow.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, false
	}
	var manifest struct {
		CrewAttachments []CrewAttachment `json:"crew_attachments"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return nil, false
	}
	return manifest.CrewAttachments, true
}

// ResolveCrewAttachmentPath rewrites an "<alias>/..." read path to the
// attached Crew workspace path. It returns ok=false when the first segment
// is not an attached alias, and refuses to resolve outside the attached
// root (fail closed on ".." escapes).
func ResolveCrewAttachmentPath(attachments []CrewAttachment, filePath string) (resolved string, ok bool) {
	trimmed := strings.Trim(strings.TrimSpace(filePath), "/")
	if trimmed == "" {
		return "", false
	}
	segment := trimmed
	if idx := strings.IndexByte(trimmed, '/'); idx >= 0 {
		segment = trimmed[:idx]
	}
	for _, attachment := range attachments {
		if attachment.Alias != segment {
			continue
		}
		root := foldCrewRoot(strings.Trim(strings.TrimSpace(attachment.CrewWorkspacePath), "/"))
		if root == "" {
			return "", false
		}
		rest := strings.TrimPrefix(trimmed, segment)
		rest = strings.Trim(rest, "/")
		if rest == "" {
			return root, true
		}
		candidate := filepath.ToSlash(filepath.Join(filepath.FromSlash(root), filepath.FromSlash(rest)))
		if candidate != root && !strings.HasPrefix(candidate, root+"/") {
			return "", false
		}
		return candidate, true
	}
	return "", false
}

// IsReservedCrewAttachmentAlias reports whether a first path segment can
// never be an attachment alias.
func IsReservedCrewAttachmentAlias(segment string) bool {
	for _, reserved := range ReservedCrewAttachmentAliases {
		if segment == reserved {
			return true
		}
	}
	return false
}
