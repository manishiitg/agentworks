package workflowfiles

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// LifecycleRequest is a trusted service request. Folder deletion is empty-only;
// moves never overwrite. Whole-tree checks forbid relocating protected files
// or bypassing child exclusions in a connection's guard.
type LifecycleRequest struct {
	Root string `json:"root"`
	Path string `json:"path"
"path/filepath"
"github.com/manishiitg/coding-agent-loop/workspace/crewroot"`
	Destination      string       `json:"destination,omitempty"`
	Action           string       `json:"action"`
	ExpectedRevision string       `json:"expected_revision,omitempty"`
	Guard            *FolderGuard `json:"guard,omitempty"`
}

func (e *Editor) Lifecycle(ctx context.Context, r LifecycleRequest) error {
	p, err := CleanRelative(r.Path)
	if err != nil || p == "." {
		return fileError(400, "invalid path")
	}
	scope, err := CleanRelative(r.Root)
	if err != nil || scope == "." {
		return fileError(400, "project root required")
	}
	if err = r.Guard.Validate(); err != nil {
		return fileError(400, "invalid file guard")
	}
	if ProtectedWrite(p) || !r.Guard.Allows(p, true) {
		return fileError(403, "path is protected or outside write grants")
	}
	destination := ""
	if r.Action == "move" {
		destination, err = CleanRelative(r.Destination)
		if err != nil || ProtectedWrite(destination) || !r.Guard.Allows(destination, true) {
			return fileError(403, "destination is protected or outside write grants")
		}
		if destination == p || strings.HasPrefix(destination, p+"/") {
			return fileError(400, "cannot move a path into itself")
		}
	}
	scoped, err := e.openScope(scope)
	if err != nil {
		return err
	}
	defer scoped.Close()
	if docs := os.Getenv("WORKSPACE_DOCS_PATH"); docs != "" {
		for _, rel := range []string{p, destination} {
			if rel != "" {
				if err := crewroot.CheckSyncWrite(docs, filepath.Join(scoped.Name(), rel)); err != nil {
					return fileError(409, err.Error())
				}
			}
		}
	}
	release, err := LockWorkspace(ctx, scoped.Name())
	if err != nil {
		return err
	}
	defer release()
	parent, err := OpenDirectory(scoped, path.Dir(p), false)
	if err != nil {
		return err
	}
	defer parent.Close()
	if r.Action == "mkdir" {
		if r.ExpectedRevision != MissingRevision {
			return fileError(400, "folder creation requires expected_revision=missing")
		}
		if _, err = parent.Lstat(path.Base(p)); !os.IsNotExist(err) {
			return fileError(409, "path already exists or is unavailable")
		}
	} else {
		info, statErr := parent.Lstat(path.Base(p))
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fileError(403, "symbolic links cannot be changed")
		}
		if info.IsDir() {
			if r.ExpectedRevision != "" {
				return fileError(400, "folder move/delete requires omitting expected_revision")
			}
			if r.Action == "delete" { // Remove is intentionally not recursive.
				dir, openErr := OpenDirectory(parent, path.Base(p), false)
				if openErr != nil {
					return openErr
				}
				f, openErr := dir.Open(".")
				if openErr != nil {
					dir.Close()
					return openErr
				}
				entries, readErr := f.ReadDir(1)
				f.Close()
				dir.Close()
				if readErr != nil && readErr != io.EOF {
					return readErr
				}
				if len(entries) > 0 {
					return fileError(409, "folder must be empty")
				}
			} else if r.Action == "move" {
				count := 0
				if err = validateLifecycleTree(scoped, p, destination, r.Guard, &count); err != nil {
					return err
				}
			} else {
				return fileError(400, "unsupported directory action")
			}
		} else {
			data, _, readErr := readRegular(parent, path.Base(p))
			if readErr != nil {
				return readErr
			}
			if r.ExpectedRevision == "" || revision(data) != r.ExpectedRevision {
				return fileError(409, "revision conflict; read the current file before changing it")
			}
		}
	}
	var destParent *os.Root
	if destination != "" {
		destParent, err = OpenDirectory(scoped, path.Dir(destination), false)
		if err != nil {
			return err
		}
		defer destParent.Close()
		if _, err = destParent.Lstat(path.Base(destination)); !os.IsNotExist(err) {
			return fileError(409, "destination already exists or is unavailable")
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Reopen parents through the anchored root immediately before mutation.
	parents := map[string]*os.Root{path.Dir(p): parent}
	if destParent != nil {
		parents[path.Dir(destination)] = destParent
	}
	for relative, opened := range parents {
		check, openErr := OpenDirectory(scoped, relative, false)
		if openErr != nil {
			return openErr
		}
		a, _ := opened.Stat(".")
		b, _ := check.Stat(".")
		check.Close()
		if a == nil || b == nil || !os.SameFile(a, b) {
			return fileError(409, "parent directory changed")
		}
	}
	check, err := e.openScope(scope)
	if err != nil {
		return err
	}
	a, _ := scoped.Stat(".")
	b, _ := check.Stat(".")
	check.Close()
	if a == nil || b == nil || !os.SameFile(a, b) {
		return fileError(409, "project root changed")
	}
	switch r.Action {
	case "mkdir":
		return parent.Mkdir(path.Base(p), 0755)
	case "delete":
		return parent.Remove(path.Base(p))
	case "move":
		return scoped.Rename(p, destination)
	default:
		return fileError(400, "unsupported lifecycle action")
	}
}
func validateLifecycleTree(root *os.Root, source, destination string, g *FolderGuard, count *int) error {
	*count++
	if *count > 10000 {
		return fileError(413, "folder contains too many entries to move")
	}
	if ProtectedWrite(source) || ProtectedWrite(destination) || !g.Allows(source, true) || !g.Allows(destination, true) {
		return fileError(403, "folder contains protected files or paths outside write grants")
	}
	info, err := root.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fileError(403, "folder contains a symbolic link")
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return fileError(403, "unsupported file type")
		}
		return nil
	}
	dir, err := OpenDirectory(root, source, false)
	if err != nil {
		return err
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err = validateLifecycleTree(root, path.Join(source, entry.Name()), path.Join(destination, entry.Name()), g, count); err != nil {
			return err
		}
	}
	return nil
}
