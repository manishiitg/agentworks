package workflowfiles

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// WorkflowFS is the read surface for one admitted workflow. Directory reads
// include its authored mounts, so moving records does not hide code or plans.
// Authorization still belongs to the caller, before opening this filesystem.
type WorkflowFS struct{ Docs, Root string }

func (w WorkflowFS) Open(name string) (fs.File, error) {
	root, relative, err := OpenWorkflowDirectory(w.Docs, w.Root, name)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(relative)
}

func (w WorkflowFS) ReadDir(name string) ([]fs.DirEntry, error) {
	file, err := w.Open(name)
	entries := map[string]fs.DirEntry{}
	if err == nil {
		defer file.Close()
		list, e := file.(fs.ReadDirFile).ReadDir(-1)
		if e != nil {
			return nil, e
		}
		for _, entry := range list {
			entries[entry.Name()] = entry
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	mounts := map[string][]string{
		".":        {"workflow.json", "planning", "variables", "code"},
		"planning": {"changelog", "revisions"},
	}
	for _, child := range mounts[name] {
		path := child
		if name != "." {
			path = name + "/" + child
		}
		f, e := w.Open(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		info, e := f.Stat()
		f.Close()
		if e != nil {
			return nil, e
		}
		entries[child] = workflowMountEntry{fs.FileInfoToDirEntry(info), child}
	}
	if len(entries) == 0 && err != nil {
		return nil, err
	}
	result := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return strings.Compare(result[i].Name(), result[j].Name()) < 0 })
	return result, nil
}

type workflowMountEntry struct {
	fs.DirEntry
	name string
}

func (e workflowMountEntry) Name() string { return e.name }
