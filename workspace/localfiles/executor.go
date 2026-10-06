package localfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Grant is local configuration, never accepted from the server.
type Grant struct {
	Resource
	Root  string
	State string
}
type Executor struct {
	Hello   Hello
	editors map[string]*wf.Editor
	grants  map[string]Grant
	roots   map[string]*os.Root
}

func Open(deviceID string, grants []Grant) (*Executor, error) {
	grants = append([]Grant(nil), grants...)
	for i := range grants {
		var err error
		grants[i].Root, err = filepath.Abs(grants[i].Root)
		if err != nil {
			return nil, err
		}
		if grants[i].Writable {
			grants[i].State, err = filepath.Abs(grants[i].State)
			if err != nil {
				return nil, err
			}
		}
	}
	e := &Executor{Hello: Hello{Version: Version, DeviceID: deviceID}, editors: map[string]*wf.Editor{}, grants: map[string]Grant{}, roots: map[string]*os.Root{}}
	for _, g := range grants {
		e.Hello.Resources = append(e.Hello.Resources, g.Resource)
	}
	if err := e.Hello.Validate(); err != nil {
		return nil, err
	}
	for _, g := range grants {
		// The root supplied by the local user is resolved once and held open.
		root, err := os.OpenRoot(g.Root)
		if err != nil {
			e.Close()
			return nil, err
		}
		e.roots[g.ID] = root
		var editor *wf.Editor
		if g.Writable {
			editor, err = wf.OpenEditor(g.Root, g.State)
		} else {
			editor, err = wf.OpenReadOnlyEditor(g.Root)
		}
		if err != nil {
			e.Close()
			return nil, err
		}
		e.editors[g.ID] = editor
		e.grants[g.ID] = g
	}
	// Neither audit receipts nor the shared lock database may be visible
	// through another alias in this same connection.
	for _, g := range grants {
		if !g.Writable {
			continue
		}
		state, _ := filepath.EvalSymlinks(g.State)
		lockState, err := wf.DefaultStateDir(g.Root)
		if err == nil {
			lockState, err = filepath.EvalSymlinks(lockState)
		}
		if err != nil {
			e.Close()
			return nil, err
		}
		for _, other := range grants {
			root, err := filepath.EvalSymlinks(other.Root)
			if err != nil {
				e.Close()
				return nil, err
			}
			root, err = filepath.Abs(root)
			if err != nil {
				e.Close()
				return nil, err
			}
			for _, private := range []string{state, lockState} {
				rel, err := filepath.Rel(root, private)
				if err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
					e.Close()
					return nil, fmt.Errorf("executor state must be outside every shared folder")
				}
			}
		}
	}
	return e, nil
}
func (e *Executor) Close() {
	for _, editor := range e.editors {
		editor.Close()
	}
	for _, root := range e.roots {
		root.Close()
	}
}
func (e *Executor) Execute(ctx context.Context, r Request) Response {
	result := Response{ID: r.ID, Status: 200}
	g, ok := e.grants[r.ResourceID]
	if !ok {
		result.Status = 404
		result.Error = "folder alias is not shared"
		return result
	}
	var err error
	switch r.Operation {
	case "read":
		var file wf.File
		file, err = e.editors[g.ID].Read(".", r.Path, &g.Guard)
		result.File = &file
	case "write":
		if !g.Writable {
			result.Status = 403
			result.Error = "folder is read-only"
			return result
		}
		var receipt wf.WriteReceipt
		receipt, err = e.editors[g.ID].Write(ctx, wf.WriteRequest{Root: ".", Path: r.Path, Content: r.Content, ExpectedRevision: r.ExpectedRevision, RequestID: r.RequestID, Actor: e.Hello.DeviceID + "/" + g.ID, Identity: r.Identity, Guard: &g.Guard})
		result.Receipt = &receipt
	case "list":
		result.Entries, err = e.list(ctx, g, r.Path)
	default:
		result.Status = 400
		result.Error = "unsupported operation"
		return result
	}
	if err != nil {
		result.Status = wf.StatusCode(err)
		result.Code, result.Error = wf.ErrorDetails(err)
		result.File = nil
		result.Receipt = nil
		result.Entries = nil
	}
	return result
}
func (e *Executor) list(ctx context.Context, g Grant, input string) ([]wf.Entry, error) {
	p, err := wf.CleanRelative(input)
	if err != nil {
		return nil, &wf.FileError{Status: 400, Message: "invalid path"}
	}
	if wf.Private(p) || !g.Guard.Allows(p, false) {
		return nil, &wf.FileError{Status: 403, Message: "path outside grants"}
	}
	dir, err := wf.OpenDirectory(e.roots[g.ID], p, false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// A bounded single-directory listing: callers descend explicitly.
	entries, err := f.ReadDir(201)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > 200 {
		return nil, &wf.FileError{Status: 413, Message: "directory exceeds 200 entries; narrow the shared folder"}
	}
	out := []wf.Entry{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		relative := filepath.ToSlash(filepath.Join(p, entry.Name()))
		if entry.Type()&fs.ModeSymlink != 0 || wf.Private(relative) || !g.Guard.Allows(relative, false) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		kind := "file"
		if info.IsDir() {
			kind = "folder"
		} else if !info.Mode().IsRegular() {
			continue
		}
		out = append(out, wf.Entry{Path: relative, Type: kind, Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
