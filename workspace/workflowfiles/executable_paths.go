package workflowfiles

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// OpenWorkflowDirectory routes an already-authorized file to its owning
// storage. Walk beneath the trusted Crew root, rejecting planted symlinks;
// resolving a filename and opening that absolute directory alone is not enough.
// The returned name is relative to the opened directory.
func OpenWorkflowDirectory(docs, virtual, relative string) (*os.Root, string, error) {
	clean, err := CleanRelative(relative)
	if err != nil {
		return nil, "", err
	}
	anchor, folder, name := docs, virtual, clean
	if layout, found, e := crewroot.LookupExecutable(docs, virtual); found || e != nil {
		if e != nil {
			return nil, "", e
		}
		scope, _, e := crewroot.ScopeRoot(docs, virtual, clean)
		if e != nil {
			return nil, "", e
		}
		target, _, e := crewroot.Resolve(docs, filepath.ToSlash(filepath.Join(virtual, clean)))
		if e != nil {
			return nil, "", e
		}
		anchor = layout.Crew.Files
		if crewroot.Within(layout.Crew.Private, scope) {
			anchor = layout.Crew.Private
		}
		if !crewroot.Within(anchor, scope) || !crewroot.Within(scope, target) {
			return nil, "", fmt.Errorf("workflow file leaves its owning storage")
		}
		folder, err = filepath.Rel(anchor, scope)
		if err != nil {
			return nil, "", err
		}
		name, err = filepath.Rel(scope, target)
		if err != nil {
			return nil, "", err
		}
	}
	base, err := os.OpenRoot(anchor)
	if err != nil {
		return nil, "", err
	}
	defer base.Close()
	root, err := OpenDirectory(base, filepath.ToSlash(folder), false)
	return root, filepath.ToSlash(name), err
}
