// Package crewroot resolves server-owned local Crew bindings. Virtual Crew
// identities stay stable; a user's folder never supplies routing or ownership.
package crewroot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const FilesSegment = "_files"

type Roots struct {
	ID, Owner, Virtual        string
	Files, Resources, Private string
}

type Record struct {
	Product   string `json:"product"`
	Folder    string `json:"folder"`
	Owner     string `json:"owner_id"`
	ID        string `json:"project_id"`
	LocalRoot string `json:"local_root,omitempty"`
}

// StateRoot matches the app's private-state selection, including desktop starts
// which do not set AGENTWORKS_STATE_ROOT explicitly.
func StateRoot() (string, error) {
	if p := strings.TrimSpace(os.Getenv("AGENTWORKS_STATE_ROOT")); p != "" {
		if !filepath.IsAbs(p) {
			return "", fmt.Errorf("AGENTWORKS_STATE_ROOT must be absolute")
		}
		return filepath.Clean(p), nil
	}
	if p := strings.TrimSpace(os.Getenv("RUNLOOP_USER_DATA_DIR")); p != "" {
		if !filepath.IsAbs(p) {
			return "", fmt.Errorf("RUNLOOP_USER_DATA_DIR must be absolute")
		}
		return filepath.Join(p, "state"), nil
	}
	p, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(p, "AgentWorks", "state"), nil
}

func records() (map[string]Record, error) {
	if os.Getenv("LOCAL_MODE") != "true" {
		return nil, nil
	}
	state, err := StateRoot()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(state, "ownership", "projects.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Projects map[string]Record `json:"projects"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc.Projects, nil
}

func roots(docs string, r Record) (Roots, error) { return rootsForOperation(docs, r, false) }

// Removal accepts absent directories after a partial cleanup, but an existing
// parent must still be the original directory, never a replacement symlink.
func unchangedDirectory(dir string, allowMissing bool) error {
	for current := dir; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			real, evalErr := filepath.EvalSymlinks(current)
			if evalErr != nil || real != current || !info.IsDir() {
				return fmt.Errorf("the Crew folder is unavailable or has changed; restore its original folder")
			}
			return nil
		}
		if !allowMissing || !os.IsNotExist(err) || filepath.Dir(current) == current {
			return fmt.Errorf("the Crew folder is unavailable or has changed; restore its original folder")
		}
	}
}

func rootsForOperation(docs string, r Record, allowMissing bool) (Roots, error) {
	if r.Product != "work" || r.ID == "" || r.Owner == "" || r.Folder == "" || strings.ContainsAny(r.Folder, "/\\") || strings.HasPrefix(r.Folder, ".") || !filepath.IsAbs(r.LocalRoot) {
		return Roots{}, fmt.Errorf("invalid local Crew binding")
	}
	canonical := filepath.Clean(r.LocalRoot)
	if err := unchangedDirectory(canonical, allowMissing); err != nil {
		return Roots{}, err
	}
	realDocs, err := filepath.EvalSymlinks(docs)
	if err != nil {
		return Roots{}, err
	}
	resource, private := filepath.Join(canonical, ".agentworks"), filepath.Join(realDocs, "Crew", r.Folder)
	for _, dir := range []string{resource, private} {
		if err := unchangedDirectory(dir, allowMissing); err != nil {
			return Roots{}, err
		}
	}

	return Roots{ID: r.ID, Owner: r.Owner, Virtual: "Crew/" + r.Folder, Files: canonical, Resources: resource, Private: private}, nil
}

// Lookup accepts the stable virtual project root, never an absolute input.
func Lookup(docs, virtual string) (Roots, bool, error) { return lookup(docs, virtual, false) }

// LookupForRemoval is deliberately separate from access resolution: an absent
// tree can be removed from the registry but must never grant file access.
func LookupForRemoval(docs, virtual string) (Roots, bool, error) { return lookup(docs, virtual, true) }

func lookup(docs, virtual string, allowMissing bool) (Roots, bool, error) {
	parts := strings.Split(filepath.ToSlash(virtual), "/")
	if len(parts) < 2 || !strings.EqualFold(parts[0], "Crew") {
		return Roots{}, false, nil
	}
	all, err := records()
	if err != nil {
		return Roots{}, false, err
	}
	r, ok := all["work/"+parts[1]]
	if !ok || r.LocalRoot == "" {
		for _, candidate := range all {
			if candidate.LocalRoot != "" && strings.EqualFold(candidate.Folder, parts[1]) {
				r, ok = candidate, true
				break
			}
		}
	}
	if !ok || r.LocalRoot == "" {
		return Roots{}, false, nil
	}
	if parts[0] != "Crew" || parts[1] != r.Folder {
		return Roots{}, true, fmt.Errorf("use the issued Crew path with its original capitalization")
	}
	out, err := rootsForOperation(docs, r, allowMissing)
	return out, true, err
}

func (r Roots) FilesPath() string { return r.Virtual + "/" + FilesSegment }

// Settings keep their managed namespace. All other resource paths resolve
// under .agentworks, including project skills, memory, functions and outputs.
func privatePath(rel string) bool {
	rel = strings.ToLower(rel)
	if rel == "triggers/deliveries" || strings.HasPrefix(rel, "triggers/deliveries/") {
		return true
	}
	first := strings.SplitN(rel, "/", 2)[0]
	switch first {
	case "product.json", "workflow.json", "workflow.json.kb-lock", "product-schedules.json", "schedule-runs.json", ".sandbox-cache", ".report-cache", "builder", "conversation", "chat_history":
		return true
	}
	return false
}

func (r Roots) ResourcePathRoot(rel string) string {
	if privatePath(rel) {
		return r.Private
	}
	return r.Resources
}

func Within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ResolveContained follows existing parents without allowing an outside link.
// The unresolved suffix is kept for creates. The issued root itself must exist.
func ResolveContained(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("use a path relative to this Crew")
	}
	for _, p := range strings.Split(filepath.ToSlash(rel), "/") {
		if p == ".." {
			return "", fmt.Errorf("path leaves the Crew folder")
		}
	}
	target := filepath.Join(root, filepath.FromSlash(rel))
	if !Within(root, target) {
		return "", fmt.Errorf("path leaves the Crew folder")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	current := target
	for {
		if _, err := os.Lstat(current); err == nil {
			real, err := filepath.EvalSymlinks(current)
			if err != nil || !Within(realRoot, real) {
				return "", fmt.Errorf("path links outside the Crew folder")
			}
			return target, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if current == root {
			return "", fmt.Errorf("Crew folder is unavailable")
		}
		current = filepath.Dir(current)
	}
}

// Resolve translates only a registered virtual path. A private manifest is
// never taken from a similarly named file in the person's selected folder.
func Resolve(docs, virtual string) (string, bool, error) {
	if filepath.IsAbs(virtual) {
		return "", false, nil
	}
	for _, part := range strings.Split(filepath.ToSlash(virtual), "/") {
		if part == ".." {
			return "", false, fmt.Errorf("path leaves the Crew folder")
		}
	}
	r, bound, err := Lookup(docs, virtual)
	if err != nil || !bound {
		return "", bound, err
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(filepath.ToSlash(virtual), r.Virtual), "/")
	root := r.Resources
	if first := strings.SplitN(rel, "/", 2)[0]; strings.EqualFold(first, FilesSegment) && first != FilesSegment {
		return "", true, fmt.Errorf("use the issued _files path")
	}
	if rel == FilesSegment || strings.HasPrefix(rel, FilesSegment+"/") {
		root = r.Files
		rel = strings.TrimPrefix(strings.TrimPrefix(rel, FilesSegment), "/")
		if first := strings.SplitN(strings.ToLower(rel), "/", 2)[0]; first == ".agentworks" {
			return "", true, fmt.Errorf("use the Crew resource paths for .agentworks files")
		}
	} else if privatePath(rel) {
		root = r.Private
	}
	path, err := ResolveContained(root, rel)
	if err == nil && root == r.Files {
		for existing := path; ; existing = filepath.Dir(existing) {
			if _, statErr := os.Lstat(existing); statErr == nil {
				real, evalErr := filepath.EvalSymlinks(existing)
				if evalErr != nil {
					return "", true, evalErr
				}
				if Within(r.Resources, real) {
					return "", true, fmt.Errorf("use the Crew resource paths for .agentworks files")
				}
				break
			} else if !os.IsNotExist(statErr) {
				return "", true, statErr
			}
			if existing == root {
				break
			}
		}
	}
	return path, true, err
}

// Reverse returns the issued virtual path for a physical file. It is used for
// listings and mutation locks, not to admit client-provided absolute paths.
func Reverse(docs, physical string) (string, Roots, bool, error) {
	all, err := records()
	if err != nil {
		return "", Roots{}, false, err
	}
	for _, rec := range all {
		if rec.LocalRoot == "" {
			continue
		}
		if !Within(rec.LocalRoot, physical) {
			continue
		}
		r, err := roots(docs, rec)
		if err != nil {
			return "", Roots{}, true, err
		}
		root, prefix := r.Files, r.FilesPath()
		if Within(r.Resources, physical) {
			root, prefix = r.Resources, r.Virtual
		}
		rel, _ := filepath.Rel(root, physical)
		if _, err := ResolveContained(root, rel); err != nil {
			return "", r, true, err
		}
		if rel == "." {
			return prefix, r, true, nil
		}
		return prefix + "/" + filepath.ToSlash(rel), r, true, nil
	}
	return "", Roots{}, false, nil
}

// CheckRootMutation keeps the chosen folder and its resource namespace owned
// by the Crew lifecycle. Ordinary files beneath them remain editable.
func CheckRootMutation(docs, physical string) error {
	all, err := records()
	if err != nil {
		return err
	}
	for _, rec := range all {
		if rec.LocalRoot == "" {
			continue
		}
		private := filepath.Join(docs, "Crew", rec.Folder)
		for _, root := range []string{rec.LocalRoot, filepath.Join(rec.LocalRoot, ".agentworks"), private} {
			if Within(strings.ToLower(physical), strings.ToLower(root)) {
				return fmt.Errorf("remove this Crew from its settings to keep your project folder and files")
			}
		}
	}
	return nil
}

// PrivateStateFor selects managed state for mutation journals and locks even
// when the desktop supplied RUNLOOP_USER_DATA_DIR rather than a state override.
func PrivateStateFor(physical string) (string, bool, error) {
	all, err := records()
	if err != nil {
		return "", false, err
	}
	for _, rec := range all {
		if rec.LocalRoot != "" && Within(rec.LocalRoot, physical) {
			state, err := StateRoot()
			return state, true, err
		}
	}
	return "", false, nil
}

// ScopeRoot chooses the physical editor root for a validated relative request.
// Resources and settings share a virtual identity, but never a physical root.
func ScopeRoot(docs, virtual, rel string) (string, bool, error) {
	r, bound, err := Lookup(docs, virtual)
	if err != nil || !bound {
		return "", bound, err
	}
	if virtual == r.FilesPath() {
		return r.Files, true, nil
	}
	if virtual != r.Virtual {
		return "", true, fmt.Errorf("use the issued Crew root")
	}
	return r.ResourcePathRoot(rel), true, nil
}
