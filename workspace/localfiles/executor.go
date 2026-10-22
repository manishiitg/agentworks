package localfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Grant is local configuration, never accepted from the server.
type Grant struct {
	Resource
	Root         string
	State        string
	PrivatePaths []string
}
type Executor struct {
	Hello         Hello
	settingsState string
	privatePaths  []string
	editors       map[string]*wf.Editor
	grants        map[string]Grant
	roots         map[string]*os.Root
}

func Open(deviceID string, grants []Grant) (*Executor, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return nil, fmt.Errorf("the local executor supports macOS, Linux and Windows")
	}
	grants = append([]Grant(nil), grants...)
	for i := range grants {
		// Local folders always support shell commands; writable controls edits. The advertised
		// field is protocol capability metadata, not a separate permission.
		grants[i].Shell = true
		grants[i].Publication = true
		grants[i].Patch = grants[i].Writable || grants[i].Downloads
		var err error
		grants[i].Root, err = filepath.Abs(grants[i].Root)
		if err != nil {
			return nil, err
		}
		canonicalRoot, err := filepath.EvalSymlinks(grants[i].Root)
		if err != nil {
			return nil, err
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			home = resolved
		}
		if containsPath(canonicalRoot, home) {
			return nil, fmt.Errorf("share a project folder, not your home directory or its parents")
		}
		for _, other := range grants {
			for _, private := range other.PrivatePaths {
				private, err = filepath.Abs(private)
				if err != nil {
					return nil, err
				}
				if resolved, err := filepath.EvalSymlinks(private); err == nil {
					private = resolved
				}
				if containsPath(canonicalRoot, private) {
					return nil, fmt.Errorf("executor credentials and private state must be outside every shared folder")
				}
			}
		}
		if grants[i].State == "" {
			grants[i].State, err = wf.DefaultStateDir(grants[i].Root)
			if err != nil {
				return nil, err
			}
		}
		{
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
		editor, err := wf.OpenEditor(g.Root, g.State)
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

func containsPath(root, file string) bool {
	// macOS volumes can resolve different casing to the same credential path.
	// This predicate denies grants; conservative alias matching only narrows them.
	if runtime.GOOS == "darwin" {
		root, file = strings.ToLower(root), strings.ToLower(file)
	}
	relative, err := filepath.Rel(root, file)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
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
	if e.Hello.SettingsManaged {
		return e.executeSettings(ctx, r)
	}
	result := Response{ID: r.ID, Status: 200}
	g, ok := e.grants[r.ResourceID]
	if !ok {
		result.Status = 404
		result.Error = "folder alias is not shared"
		return result
	}
	var err error
	switch r.Operation {
	case "publish_snapshot":
		result.Publication, err = e.publicationSnapshot(ctx, g, r)
	case "copy_read":
		result.Bytes, err = e.editors[g.ID].ReadBytes(".", r.Path, &g.Guard)
		if err == nil && result.Bytes == nil {
			err = &wf.FileError{Status: 404, Message: "File not found"}
		}
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
	case "patch":
		result.Patches, err = e.patch(ctx, g, r)
	case "shell":
		result.Shell, err = e.shell(ctx, g, r)
	case "list":
		result.Entries, result.Total, result.Truncated, err = e.list(ctx, g, r)
	default:
		result.Status = 400
		result.Error = "unsupported operation"
		return result
	}
	if err != nil {
		result.Status = wf.StatusCode(err)
		result.Code, result.Error = wf.ErrorDetails(err)
		result.Bytes = nil
		result.File = nil
		result.Receipt = nil
		result.Entries = nil
	}
	return result
}

const (
	defaultListLimit = 200
	maxListLimit     = 1000
	maxListedFolder  = 50000 // a folder with more entries than this is not read; name a subfolder
)

// list is one level of a folder, in name order, as a page: Limit entries (default 200, at most 1000) from Offset, optionally only
// the names containing Query. It returns how many entries matched and whether more follow, so a big folder such as Downloads is
// browsed or searched instead of refused.
func (e *Executor) list(ctx context.Context, g Grant, r Request) ([]wf.Entry, int, bool, error) {
	p, err := wf.CleanRelative(r.Path)
	if err != nil {
		return nil, 0, false, &wf.FileError{Status: 400, Message: "invalid path"}
	}
	if wf.Private(p) || !g.Guard.Allows(p, false) {
		return nil, 0, false, &wf.FileError{Status: 403, Message: "path outside grants"}
	}
	dir, err := wf.OpenDirectory(e.roots[g.ID], p, false)
	if err != nil {
		return nil, 0, false, err
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()
	entries, err := f.ReadDir(maxListedFolder + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, false, err
	}
	if len(entries) > maxListedFolder {
		return nil, 0, false, &wf.FileError{Status: 413, Message: "this folder has more than 50,000 entries; list one of its subfolders"}
	}
	limit := r.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	offset := int(r.Offset)
	if offset < 0 {
		offset = 0
	}
	query := strings.ToLower(strings.TrimSpace(r.Query))
	kept := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, 0, false, err
		}
		relative := filepath.ToSlash(filepath.Join(p, entry.Name()))
		if entry.Type()&fs.ModeSymlink != 0 || wf.Private(relative) || !g.Guard.Allows(relative, false) {
			continue
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(entry.Name()), query) {
			continue
		}
		kept = append(kept, entry)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Name() < kept[j].Name() })
	total := len(kept)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	out := make([]wf.Entry, 0, end-offset)
	for _, entry := range kept[offset:end] {
		kind, size := "file", int64(0)
		if entry.IsDir() {
			kind = "folder"
		}
		if info, err := entry.Info(); err == nil {
			size = info.Size()
		}
		out = append(out, wf.Entry{Path: filepath.ToSlash(filepath.Join(p, entry.Name())), Type: kind, Size: size})
	}
	return out, total, end < total, nil
}
