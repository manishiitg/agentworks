package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// External file tools read the shared workspace directly. The agent has the
// same docs mount as the workspace service, so reads need no HTTP proxy or
// workspace-wide write lock. The caller has already passed workflow access.
func externalFileRequest(ctx context.Context, req wf.Request) (wf.Result, error) {
	if err := ctx.Err(); err != nil {
		return wf.Result{}, err
	}
	rootPath, err := wf.CleanRelative(req.Root)
	if err != nil {
		return wf.Result{}, &externalUpstreamError{400, "invalid workflow root"}
	}
	// A migrated crew's old root keeps working (PLAT-442 step 4).
	if workspaceref.MustParse(rootPath).HasOwner() {
		rootPath = followCrewAlias("", rootPath)
	}
	parts := strings.Split(rootPath, "/")
	crewRoot := externalIsCrewRoot(rootPath)
	if !crewRoot && (len(parts) != 2 || parts[0] != "Workflow" || parts[1] == ".") {
		return wf.Result{}, &externalUpstreamError{400, "root must identify one workflow or Crew"}
	}
	p, err := wf.CleanRelative(req.Path)
	if err != nil {
		return wf.Result{}, &externalUpstreamError{400, err.Error()}
	}
	if externalPathPrivate(crewRoot, p) {
		return wf.Result{}, &externalUpstreamError{403, "private path"}
	}
	if err := wf.ValidateGlob(req.Glob); err != nil {
		return wf.Result{}, &externalUpstreamError{400, err.Error()}
	}
	base, err := os.OpenRoot(getWorkspaceDocsAbsPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return externalRemoteFileRequest(ctx, req, p)
		}
		return wf.Result{}, err
	}
	defer base.Close()
	if err := externalNoSymlinks(base, rootPath); err != nil {
		return wf.Result{}, err
	}
	root, err := base.OpenRoot(rootPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return externalRemoteFileRequest(ctx, req, p)
		}
		return wf.Result{}, err
	}
	defer root.Close()
	if err := externalNoSymlinks(root, p); err != nil {
		return wf.Result{}, err
	}
	shared := externalRootUsesSharedKnowledge(root)
	if shared && (p == "knowledgebase" || strings.HasPrefix(p, "knowledgebase/")) {
		return wf.Result{}, &externalUpstreamError{403, "local knowledge archive is unavailable; use shared Knowledge Base MCP"}
	}
	switch req.Operation {
	case "read":
		file, err := externalScopedFile(root, p)
		return wf.Result{File: file}, err
	case "list", "search":
		return externalListFiles(ctx, root, p, req, crewRoot)
	default:
		return wf.Result{}, &externalUpstreamError{400, "unsupported file operation"}
	}
}

func externalNoSymlinks(root *os.Root, p string) error {
	prefix := ""
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == "" {
			continue
		}
		prefix = path.Join(prefix, part)
		st, err := root.Lstat(prefix)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return &externalUpstreamError{403, "symbolic links are not exposed by workflow file tools"}
		}
	}
	return nil
}

func externalScopedFile(root *os.Root, p string) (wf.File, error) {
	result := wf.File{Path: p, Revision: wf.MissingRevision}
	if err := externalNoSymlinks(root, p); err != nil {
		return result, err
	}
	f, err := root.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return result, err
	}
	if !st.Mode().IsRegular() {
		return result, &externalUpstreamError{400, "path is not a regular file"}
	}
	if st.Size() > wf.MaxFileBytes {
		return result, &externalUpstreamError{413, fmt.Sprintf("file exceeds %d byte limit", wf.MaxFileBytes)}
	}
	data, err := io.ReadAll(io.LimitReader(f, wf.MaxFileBytes+1))
	if err != nil {
		return result, err
	}
	if len(data) > wf.MaxFileBytes {
		return result, &externalUpstreamError{413, "file exceeds limit"}
	}
	result.Exists = true
	result.Size = int64(len(data))
	result.Revision = wf.Revision(data)
	result.Encoding = "utf-8"
	if utf8.Valid(data) && !strings.ContainsRune(string(data), 0) {
		result.Content = string(data)
	} else {
		result.Encoding = "base64"
		result.Content = base64.StdEncoding.EncodeToString(data)
	}
	return result, nil
}

// externalIsCrewRoot reports a Crew project root: "Crew/<id>" or
// "_users/<owner>/Chats/Work/projects/<id>".
func externalIsCrewRoot(rootPath string) bool {
	parts := strings.Split(rootPath, "/")
	switch {
	case len(parts) == 2 && parts[0] == crewSharedRootName:
		return parts[1] != "" && parts[1] != "."
	}
	ref := workspaceref.MustParse(rootPath)
	root, _, ok := ref.ProjectRoot()
	return ok && root == workspaceref.CrewProjectsRoot && ref.HasOwner() && ref.String() == rootPath
}

// externalPathPrivate is the workflow file privacy rule, plus a Crew's own
// private areas (its database and manifests, as the shared Crew reader view
// hides them) when the root is a Crew.
func externalPathPrivate(crewRoot bool, p string) bool {
	if wf.Private(p) {
		return true
	}
	if !crewRoot {
		return false
	}
	_, private := confineSharedProjectPath("x", p)
	return !private && p != "." && p != ""
}

func externalListFiles(ctx context.Context, root *os.Root, p string, req wf.Request, crewRoot bool) (wf.Result, error) {
	if req.Limit <= 0 {
		req.Limit = 100
	}
	if req.Limit > 200 || req.Offset < 0 || req.Offset > 10000 {
		return wf.Result{}, &externalUpstreamError{400, "invalid pagination"}
	}
	if req.Depth <= 0 {
		req.Depth = 4
	}
	if req.Depth > 8 {
		return wf.Result{}, &externalUpstreamError{400, "depth exceeds 8"}
	}
	if req.Operation == "search" && req.Query == "" {
		return wf.Result{}, &externalUpstreamError{400, "query is required"}
	}
	if _, err := root.Lstat(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return wf.Result{}, &externalUpstreamError{404, "path does not exist: " + p}
		}
		return wf.Result{}, err
	}
	shared := externalRootUsesSharedKnowledge(root)
	all := make([]wf.Entry, 0)
	visited := 0
	var scannedBytes int64
	truncated := false
	err := fs.WalkDir(root.FS(), p, func(name string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > 10000 {
			truncated = true
			return fs.SkipAll
		}
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 || externalPathPrivate(crewRoot, name) || shared && (name == "knowledgebase" || strings.HasPrefix(name, "knowledgebase/")) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(name, p), "/")
		if name == p && d.IsDir() {
			return nil
		}
		if strings.Count(rel, "/") >= req.Depth {
			if d.IsDir() {
				truncated = true
				return fs.SkipDir
			}
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		entry := wf.Entry{Path: name, Type: "file", Size: st.Size()}
		if d.IsDir() {
			entry.Type = "folder"
		}
		if !wf.MatchGlob(req.Glob, rel) {
			return nil
		}
		if req.Operation == "list" {
			all = append(all, entry)
		} else if st.Mode().IsRegular() && st.Size() <= wf.MaxFileBytes {
			scannedBytes += st.Size()
			if scannedBytes > 64<<20 {
				truncated = true
				return fs.SkipAll
			}
			f, err := externalScopedFile(root, name)
			if err != nil {
				return err
			}
			if f.Encoding == "utf-8" {
				for lineNumber, line := range strings.Split(f.Content, "\n") {
					if strings.Contains(strings.ToLower(line), strings.ToLower(req.Query)) {
						match := entry
						match.Line = lineNumber + 1
						if len(line) > 1000 {
							line = line[:1000]
						}
						match.Text = line
						all = append(all, match)
						if len(all) > req.Offset+req.Limit {
							break
						}
					}
				}
			}
		}
		if len(all) > req.Offset+req.Limit {
			truncated = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return wf.Result{}, err
	}
	start := min(req.Offset, len(all))
	end := min(start+req.Limit, len(all))
	result := wf.Result{File: wf.File{Path: p, Exists: true}, Entries: all[start:end], Truncated: truncated}
	if len(all) > end {
		result.NextOffset = end
	}
	return result, nil
}

func externalRootUsesSharedKnowledge(root *os.Root) bool {
	data, err := root.ReadFile("workflow.json")
	if err != nil {
		return true
	}
	var m struct {
		Mode string `json:"knowledgebase_mode"`
	}
	return json.Unmarshal(data, &m) != nil || strings.TrimSpace(string(data)) == "null" || m.Mode != ""
}
