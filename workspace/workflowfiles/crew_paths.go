package workflowfiles

import (
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// BoundCrewFileRequest retains the resource namespace and translates the
// explicitly issued _files subtree only after intersecting the caller's guard.
// The returned prefix restores logical paths in response listings/receipts.
func BoundCrewFileRequest(docs, root, p, destination string, guard *FolderGuard) (string, string, string, *FolderGuard, string, error) {
	if err := guard.Validate(); err != nil {
		return "", "", "", nil, "", err
	}
	clean, err := CleanRelative(p)
	if err != nil {
		return "", "", "", nil, "", err
	}
	p = clean
	if destination != "" {
		destination, err = CleanRelative(destination)
		if err != nil {
			return "", "", "", nil, "", err
		}
	}
	if l, found, err := crewroot.LookupExecutable(docs, root); found || err != nil {
		if err != nil {
			return "", "", "", nil, "", err
		}
		if root != l.Binding.Root {
			return "", "", "", nil, "", fmt.Errorf("use the issued workflow root")
		}
		if destination != "" {
			sourceRoot, _, e := crewroot.ScopeRoot(docs, root, p)
			if e != nil {
				return "", "", "", nil, "", e
			}
			targetRoot, _, e := crewroot.ScopeRoot(docs, root, destination)
			if e != nil {
				return "", "", "", nil, "", e
			}
			if sourceRoot != targetRoot {
				return "", "", "", nil, "", fmt.Errorf("move files within the same definition, code or runtime folder")
			}
		}
		code := func(p string) bool { return p == "code" || strings.HasPrefix(p, "code/") }
		if destination != "" && code(p) != code(destination) {
			return "", "", "", nil, "", fmt.Errorf("move files within the same definition or code folder")
		}
		if code(p) {
			p = strings.TrimPrefix(strings.TrimPrefix(p, "code"), "/")
			if p == "" {
				p = "."
			}
			return root + "/code", p, strings.TrimPrefix(destination, "code/"), guard.Subtree("code"), "code", nil
		}
		return root, p, destination, guard, "", nil
	}
	roots, bound, err := crewroot.Lookup(docs, root)
	if err != nil || !bound {
		return root, p, destination, guard, "", err
	}
	if root != roots.Virtual {
		return "", "", "", nil, "", fmt.Errorf("use the issued Crew root")
	}
	if !roots.Local {
		if destination != "" && roots.ResourcePathRoot(p) != roots.ResourcePathRoot(destination) {
			return "", "", "", nil, "", fmt.Errorf("move files within the same Crew folder")
		}
		return root, p, destination, guard, "", nil
	}
	for _, input := range []string{p, destination} {
		if input != "" {
			if _, _, err := crewroot.Resolve(docs, root+"/"+input); err != nil {
				return "", "", "", nil, "", err
			}
		}
	}
	isFiles := func(s string) bool {
		return s == crewroot.FilesSegment || strings.HasPrefix(s, crewroot.FilesSegment+"/")
	}
	if destination != "" && (isFiles(p) != isFiles(destination) || (!isFiles(p) && roots.ResourcePathRoot(p) != roots.ResourcePathRoot(destination))) {
		return "", "", "", nil, "", fmt.Errorf("move files within the same Crew folder")
	}
	prefix := ""
	if root == roots.Virtual && (p == crewroot.FilesSegment || strings.HasPrefix(p, crewroot.FilesSegment+"/")) {
		prefix = crewroot.FilesSegment
		if destination != "" && !strings.HasPrefix(destination, prefix+"/") {
			return "", "", "", nil, "", fmt.Errorf("move files within the same Crew folder")
		}
		guard = guard.Subtree(prefix)
		p = strings.TrimPrefix(strings.TrimPrefix(p, prefix), "/")
		if p == "" {
			p = "."
		}
		destination = strings.TrimPrefix(strings.TrimPrefix(destination, prefix), "/")
		root = roots.FilesPath()
	}

	return root, p, destination, guard, prefix, nil
}

// Subtree intersects every allow/exclusion list with the selected subtree;
// stripping a namespace must never broaden a connection's existing grants.
func (g *FolderGuard) Subtree(prefix string) *FolderGuard {
	if g == nil {
		return nil
	}
	trim := func(paths []string, fold bool) []string {
		out := []string{}
		for _, p := range paths {
			check, base := p, prefix
			if fold {
				check, base = strings.ToLower(check), strings.ToLower(base)
			}
			if check == "." || check == base || strings.HasPrefix(base, check+"/") {
				return []string{"."}
			}
			if strings.HasPrefix(check, base+"/") {
				out = append(out, p[len(prefix)+1:])
			}
		}
		return out
	}
	return &FolderGuard{ReadPaths: trim(g.ReadPaths, false), WritePaths: trim(g.WritePaths, false), ReadOnlyPaths: trim(g.ReadOnlyPaths, true), BlockedPaths: trim(g.BlockedPaths, true), BlockedWritePaths: trim(g.BlockedWritePaths, true)}
}
