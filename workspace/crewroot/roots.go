// Package crewroot resolves server-owned Crew storage bindings. Virtual Crew
// identities stay stable; a user's folder never supplies routing or ownership.
package crewroot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const FilesSegment = "_files"
const PrivateDirectory = "_system/crew-data"

type Roots struct {
	ID, Owner, Virtual        string
	Files, Resources, Private string
	Local                     bool
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

// PrivateRoot is selected only from a server-owned registry record.
func PrivateRoot(docs, id string) (string, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		return "", fmt.Errorf("invalid Crew identity")
	}
	return filepath.Join(docs, filepath.FromSlash(PrivateDirectory), id), nil
}

func rootsForOperation(docs string, r Record, allowMissing bool) (Roots, error) {
	if r.Product != "work" || r.Owner == "" || r.Folder == "" || strings.ContainsAny(r.Folder, "/\\") || strings.HasPrefix(r.Folder, ".") {
		return Roots{}, fmt.Errorf("invalid Crew binding")
	}
	realDocs, err := filepath.EvalSymlinks(docs)
	if err != nil {
		return Roots{}, err
	}
	private, err := PrivateRoot(realDocs, r.ID)
	if err != nil {
		return Roots{}, err
	}
	files := filepath.Join(realDocs, "Crew", r.Folder)
	resources := files
	if r.LocalRoot != "" {
		if os.Getenv("LOCAL_MODE") != "true" || !filepath.IsAbs(r.LocalRoot) {
			return Roots{}, fmt.Errorf("local Crew binding is unavailable on this server")
		}
		files = filepath.Clean(r.LocalRoot)
		resources = filepath.Join(files, ".agentworks")
	}
	sync, err := ReadSync(r.ID)
	if err != nil {
		return Roots{}, err
	}
	if sync != nil && sync.Side == "server" && sync.SplitFiles {
		files = filepath.Join(files, FilesSegment)
	}
	for _, dir := range []string{files, resources, private} {
		if err := unchangedDirectory(dir, allowMissing); err != nil {
			return Roots{}, err
		}
	}
	return Roots{ID: r.ID, Owner: r.Owner, Virtual: "Crew/" + r.Folder, Files: files, Resources: resources, Private: private, Local: r.LocalRoot != ""}, nil
}

// Resolver keeps registry decoding and root bindings within one request. It is
// never shared across requests; each path still receives containment checks.
type Resolver struct {
	docs         string
	records      map[string]Record
	roots        map[string]Roots
	allowMissing bool
}

func NewResolver(docs string) (*Resolver, error) {
	all, err := records()
	if err != nil {
		return nil, err
	}
	return &Resolver{docs: docs, records: all, roots: make(map[string]Roots)}, nil
}

func (q *Resolver) root(rec Record) (Roots, error) {
	if r, ok := q.roots[rec.ID]; ok {
		return r, nil
	}
	r, err := rootsForOperation(q.docs, rec, q.allowMissing)
	if err == nil {
		q.roots[rec.ID] = r
	}
	return r, err
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
	q, err := NewResolver(docs)
	if err != nil {
		return Roots{}, false, err
	}
	q.allowMissing = allowMissing
	return q.Lookup(virtual)
}

func (q *Resolver) Lookup(virtual string) (Roots, bool, error) {
	parts := strings.Split(filepath.ToSlash(virtual), "/")
	if len(parts) < 2 || !strings.EqualFold(parts[0], "Crew") {
		return Roots{}, false, nil
	}
	all := q.records
	r, ok := all["work/"+parts[1]]
	if !ok {
		for _, candidate := range all {
			if candidate.Product == "work" && strings.EqualFold(candidate.Folder, parts[1]) {
				r, ok = candidate, true
				break
			}
		}
	}
	if !ok {
		return Roots{}, false, nil
	}
	if parts[0] != "Crew" || parts[1] != r.Folder {
		return Roots{}, true, fmt.Errorf("use the issued Crew path with its original capitalization")
	}
	out, err := q.root(r)
	return out, true, err
}

func (r Roots) FilesPath() string {
	if !r.SeparateFiles() {
		return r.Virtual
	}
	return r.Virtual + "/" + FilesSegment
}

// Platform records keep their managed namespace outside the working folder. All other resource paths resolve
// under the resource root, including skills, memory, code and outputs.
func privatePath(rel string) bool {
	rel = strings.ToLower(rel)
	if rel == "db/db.sqlite" || rel == "db/db.sqlite-wal" || rel == "db/db.sqlite-shm" || rel == "db/db.sqlite-journal" {
		return true
	}
	if rel == "triggers/deliveries" || strings.HasPrefix(rel, "triggers/deliveries/") {
		return true
	}
	first := strings.SplitN(rel, "/", 2)[0]
	switch first {
	case "product.json", "workflow.json", "workflow.json.kb-lock", "schedules.json", "webhooks.json", "product-schedules.json", "schedule-runs.json", "functions.json", "costs", "human_inputs", "runs", "planning", "knowledgebase", ".sandbox-cache", ".report-cache", "builder", "conversation", "chat_history":
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
	q, err := NewResolver(docs)
	if err != nil {
		return "", false, err
	}
	return q.Resolve(virtual)
}

func (q *Resolver) Resolve(virtual string) (string, bool, error) {
	if filepath.IsAbs(virtual) {
		return "", false, nil
	}
	for _, part := range strings.Split(filepath.ToSlash(virtual), "/") {
		if part == ".." {
			return "", false, fmt.Errorf("path leaves the Crew folder")
		}
	}
	r, bound, err := q.Lookup(virtual)
	if err != nil || !bound {
		return "", bound, err
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(filepath.ToSlash(virtual), r.Virtual), "/")
	root := r.Resources
	if first := strings.SplitN(rel, "/", 2)[0]; r.SeparateFiles() && strings.EqualFold(first, FilesSegment) && first != FilesSegment {
		return "", true, fmt.Errorf("use the issued _files path")
	}
	if r.SeparateFiles() && (rel == FilesSegment || strings.HasPrefix(rel, FilesSegment+"/")) {
		root = r.Files
		rel = strings.TrimPrefix(strings.TrimPrefix(rel, FilesSegment), "/")
		if first := strings.SplitN(strings.ToLower(rel), "/", 2)[0]; first == ".agentworks" {
			return "", true, fmt.Errorf("use the Crew resource paths for .agentworks files")
		}
	} else if privatePath(rel) {
		root = r.Private
	}
	if err := unchangedDirectory(root, false); err != nil {
		return "", true, err
	}
	path, err := ResolveContained(root, rel)
	if err == nil && r.Local && root == r.Files {
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
	q, err := NewResolver(docs)
	if err != nil {
		return "", Roots{}, false, err
	}
	return q.Reverse(physical)
}

func (q *Resolver) Reverse(physical string) (string, Roots, bool, error) {
	all := q.records
	docs := q.docs
	for _, rec := range all {
		if rec.Product != "work" {
			continue
		}
		private, err := PrivateRoot(docs, rec.ID)
		if err != nil {
			return "", Roots{}, true, err
		}
		managed := filepath.Join(docs, "Crew", rec.Folder)
		if !Within(managed, physical) && !Within(private, physical) && (rec.LocalRoot == "" || !Within(rec.LocalRoot, physical)) {
			continue
		}
		r, err := q.root(rec)
		if err != nil {
			return "", Roots{}, true, err
		}
		root, prefix := r.Files, r.FilesPath()
		if Within(r.Private, physical) {
			root, prefix = r.Private, r.Virtual
		}
		if Within(r.Resources, physical) && !(r.SeparateFiles() && Within(r.Files, physical)) {
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
		if rec.Product != "work" {
			continue
		}
		private, err := PrivateRoot(docs, rec.ID)
		if err != nil {
			return err
		}
		roots := []string{private, filepath.Join(docs, "Crew", rec.Folder)}
		if rec.LocalRoot != "" {
			roots = append(roots, rec.LocalRoot, filepath.Join(rec.LocalRoot, ".agentworks"))
		}
		for _, root := range roots {
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
	if r.SeparateFiles() && virtual == r.FilesPath() {
		return r.Files, true, nil
	}
	if virtual != r.Virtual {
		return "", true, fmt.Errorf("use the issued Crew root")
	}
	return r.ResourcePathRoot(rel), true, nil
}

// NativeGrants separates broad resource grants from platform state. The file
// service keeps its logical grants for typed operations; shell processes don't
// inherit writes to settings or conversation records. An explicitly granted
// run output directory remains writable.
func NativeGrants(docs string, read, write []string) ([]string, []string, []string) {
	var blocked []string
	reads := append([]string(nil), read...)
	for _, grant := range read {
		clean := strings.TrimSuffix(grant, "/")
		if r, found, err := Lookup(docs, clean); err == nil && found && clean == r.Virtual {
			reads = append(reads, r.Private)
			for _, name := range []string{"db.sqlite", "db.sqlite-wal", "db.sqlite-shm", "db.sqlite-journal"} {
				blocked = append(blocked, filepath.Join(r.Private, "db", name))
			}
		}
	}
	writes := make([]string, 0, len(write))
	caches := map[string]bool{}
	for _, grant := range write {
		clean := strings.TrimSuffix(grant, "/")
		r, found, err := Lookup(docs, clean)
		suffix := ""
		if found && err == nil {
			suffix = strings.TrimPrefix(strings.TrimPrefix(clean, r.Virtual), "/")
		}
		if filepath.IsAbs(clean) {
			logical, roots, match, e := Reverse(docs, clean)
			r, found, err = roots, match, e
			if match && e == nil {
				suffix = strings.TrimPrefix(strings.TrimPrefix(logical, r.Virtual), "/")
			}
		}
		if err != nil && found {
			continue
		}
		privateGrant := found && (r.ResourcePathRoot(suffix) == r.Private || filepath.IsAbs(clean) && Within(r.Private, clean))
		shellHome := suffix == ".sandbox-cache/home" || strings.HasPrefix(suffix, ".sandbox-cache/home/")
		if privateGrant && !strings.HasPrefix(suffix, "runs/") && !shellHome {
			continue
		}
		writes = append(writes, grant)
		if found && (clean == r.Virtual || clean == r.FilesPath() || clean == r.Files || clean == r.Resources) {
			caches[filepath.Join(r.Private, ".sandbox-cache")] = true
		}
	}
	// Full Builder resource access retains the existing tool/package home in
	// private storage. Narrow file and Run-output grants acquire no shared home.
	var cachePaths []string
	for cache := range caches {
		cachePaths = append(cachePaths, cache)
	}
	sort.Strings(cachePaths)
	writes = append(writes, cachePaths...)
	return reads, SyncWriteGrants(docs, writes), blocked
}
