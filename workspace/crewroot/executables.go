package crewroot

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExecutableBinding is private routing state, never read from project files.
// Workflow/<id> is the execution identity; its authored files live with its Crew.
type ExecutableBinding struct {
	ID       string `json:"id"`
	CrewID   string `json:"crew_id"`
	CrewRoot string `json:"crew_root"`
	Root     string `json:"workspace_path"`
	Kind     string `json:"kind"`
}

type ExecutableLayout struct {
	Binding                    ExecutableBinding
	Crew                       Roots
	Definitions, Code, Runtime string
}

func (q *Resolver) executable(virtual string) (ExecutableLayout, bool, error) {
	parts := strings.Split(filepath.ToSlash(virtual), "/")
	if len(parts) < 2 || parts[0] != "Workflow" || parts[1] == "" || strings.HasPrefix(parts[1], ".") {
		return ExecutableLayout{}, false, nil
	}
	root := strings.Join(parts[:2], "/")
	state, err := StateRoot()
	if err != nil {
		return ExecutableLayout{}, true, err
	}
	raw, err := os.ReadFile(filepath.Join(state, "ownership", "crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte(root)))))
	if os.IsNotExist(err) {
		return ExecutableLayout{}, false, nil
	}
	if err != nil {
		return ExecutableLayout{}, true, err
	}
	var b ExecutableBinding
	if err := json.Unmarshal(raw, &b); err != nil {
		return ExecutableLayout{}, true, err
	}
	if b.Root != root || b.ID == "" || b.ID == "." || b.ID == ".." || strings.ContainsAny(b.ID, "/\\") || (b.Kind != "workflow" && b.Kind != "script" && b.Kind != "relay") {
		return ExecutableLayout{}, true, fmt.Errorf("invalid Crew executable binding")
	}
	// Relay storage is deliberately outside this refactor.
	if b.Kind == "relay" {
		return ExecutableLayout{}, false, nil
	}
	r, found, err := q.Lookup(b.CrewRoot)
	if err != nil {
		return ExecutableLayout{}, true, err
	}
	if !found || r.ID != b.CrewID || r.Virtual != b.CrewRoot {
		return ExecutableLayout{}, true, fmt.Errorf("executable's owning Crew is unavailable")
	}
	automation := filepath.Join(r.Files, ".agentworks")
	return ExecutableLayout{b, r, filepath.Join(automation, "workflows", b.ID), filepath.Join(automation, "code", b.ID), filepath.Join(q.docs, filepath.FromSlash(root))}, true, nil
}

// LookupExecutable is shared by file routes, native execution and discovery.
func LookupExecutable(docs, virtual string) (ExecutableLayout, bool, error) {
	q, err := NewResolver(docs)
	if err != nil {
		return ExecutableLayout{}, false, err
	}
	return q.executable(virtual)
}

func (l ExecutableLayout) source(rel string) (root, suffix string, mapped bool) {
	switch {
	case rel == "planning/changelog" || strings.HasPrefix(rel, "planning/changelog/") || rel == "planning/revisions" || strings.HasPrefix(rel, "planning/revisions/"):
		return "", "", false
	case rel == "code" || strings.HasPrefix(rel, "code/"):
		return l.Code, strings.TrimPrefix(strings.TrimPrefix(rel, "code"), "/"), true
	case rel == "workflow.json" || rel == "workflow.json.kb-lock" || rel == "planning" || strings.HasPrefix(rel, "planning/") || rel == "variables" || strings.HasPrefix(rel, "variables/"):
		if l.Binding.Kind == "workflow" {
			return l.Definitions, rel, true
		}
	}
	return "", "", false
}

func (q *Resolver) resolveExecutable(virtual string) (string, bool, error) {
	l, found, err := q.executable(virtual)
	if err != nil || !found {
		return "", found, err
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(virtual, l.Binding.Root), "/")
	root, suffix, mapped := l.source(rel)
	if !mapped {
		return "", false, nil
	}
	// Resolve against the existing project root so first writes can create the
	// automation directories; a planted symlink cannot escape the project.
	target := filepath.Join(root, filepath.FromSlash(suffix))
	projectRel, err := filepath.Rel(l.Crew.Files, target)
	if err != nil {
		return "", true, err
	}
	target, err = ResolveContained(l.Crew.Files, projectRel)
	return target, true, err
}

func executableGrants(docs string, grants []string) []string {
	out := append([]string(nil), grants...)
	for _, grant := range grants {
		logical := strings.TrimSuffix(grant, "/")
		if filepath.IsAbs(logical) && Within(docs, logical) {
			rel, _ := filepath.Rel(docs, logical)
			logical = filepath.ToSlash(rel)
		}
		if l, found, err := LookupExecutable(docs, logical); found && err == nil && logical == l.Binding.Root {
			out = append(out, l.Definitions, l.Code)
		}
	}
	return out
}

// ExecutableLayouts enumerates only private bindings, never project manifests.
func ExecutableLayouts(docs string) ([]ExecutableLayout, error) {
	state, err := StateRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(state, "ownership", "crew-executables"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	q, err := NewResolver(docs)
	if err != nil {
		return nil, err
	}
	var out []ExecutableLayout
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(state, "ownership", "crew-executables", entry.Name()))
		if err != nil {
			return nil, err
		}
		var b ExecutableBinding
		if err = json.Unmarshal(data, &b); err != nil {
			return nil, err
		}
		if entry.Name() != fmt.Sprintf("%x.json", sha256.Sum256([]byte(b.Root))) {
			return nil, fmt.Errorf("invalid executable registry filename")
		}
		l, found, err := q.executable(b.Root)
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, l)
		}
	}
	return out, nil
}
