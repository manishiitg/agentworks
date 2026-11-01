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

type publicationFileReadKey struct{}

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
	parts := strings.Split(rootPath, "/")
	crewRoot := externalIsCrewRoot(rootPath)
	localRoots, localBound, localErr := crewStorageRoots(rootPath)
	if localErr != nil {
		return wf.Result{}, localErr
	}
	localFiles := localBound && localRoots.Local && rootPath == localRoots.FilesPath()
	crewRoot = crewRoot || localFiles
	// A Flow release folder is allowed only when it maps back to a live Flow;
	// callers pass it after checking access to that Flow.
	releaseRoot := len(parts) == 4 && parts[0] == "Workflow" && parts[1] == relayReleasesFolder
	if releaseRoot {
		if _, err := relayDraftWorkspaceForRelease(ctx, rootPath); err != nil {
			return wf.Result{}, &externalUpstreamError{400, "unknown Flow release"}
		}
	}
	if !crewRoot && !releaseRoot && (len(parts) != 2 || parts[0] != "Workflow" || parts[1] == ".") {
		return wf.Result{}, &externalUpstreamError{400, "root must identify one workflow or Crew"}
	}
	p, err := wf.CleanRelative(req.Path)
	if err != nil {
		return wf.Result{}, &externalUpstreamError{400, err.Error()}
	}
	guard := externalFileGuard(ctx, crewRoot)
	if req.Operation == "read" && !guard.Allows(p, false) || req.Operation != "read" && !guard.AllowsTraversal(p) {
		return wf.Result{}, &externalUpstreamError{403, "file is outside folder grants"}
	}
	if externalPathPrivate(crewRoot, p) {
		return wf.Result{}, &externalUpstreamError{403, "private path"}
	}
	if err := wf.ValidateGlob(req.Glob); err != nil {
		return wf.Result{}, &externalUpstreamError{400, err.Error()}
	}
	var filesPrefix string
	req.Root, p, _, guard, filesPrefix, err = wf.BoundCrewFileRequest(getWorkspaceDocsAbsPath(), rootPath, p, "", guard)
	if err != nil {
		return wf.Result{}, err
	}
	req.Path = p
	rootPath = req.Root
	localFiles = localBound && localRoots.Local && rootPath == localRoots.FilesPath()
	if filesPrefix != "" {
		ctx = context.WithValue(ctx, externalFileGuardKey{}, guard)
	}
	var sourcePublication *publishedProject
	skipPublication, _ := ctx.Value(publicationFileReadKey{}).(bool)
	if crewRoot && !skipPublication && !localRoots.Local {
		pub, e := readPublishedProject(ctx, rootPath)
		if e != nil {
			return wf.Result{}, e
		}
		if pub != nil {
			sourcePublication = pub
			if p == "." {
				rootPath = pub.sourceRoot()
			} else {
				actual := publishedReadPath(ctx, rootPath+"/"+p)
				if strings.HasPrefix(actual, pub.sourceRoot()+"/") {
					rootPath = pub.sourceRoot()
				}
				if pub.AgentID != "" && strings.HasPrefix(actual, pub.runtimeRoot()+"/") {
					rootPath = pub.runtimeRoot()
				}
				if pub.AgentID != "" && actual != rootPath+"/"+p {
					// A resource absent from this agent's import may come from
					// Main's published source, under the same admitted Crew.
					main, e := readPublishedProject(contextWithoutPublication(ctx), req.Root)
					if e != nil {
						return wf.Result{}, e
					}
					if main != nil && actual == main.sourceRoot()+"/"+p {
						rootPath, sourcePublication = main.sourceRoot(), main
					}
				}
			}
		}
	}

	// A published source may be outside the live Crew. Resolve the selected
	// root after publication routing, never the original live resource tree.
	if !localRoots.Local {
		localRoots, localBound, localErr = crewStorageRoots(rootPath)
		if localErr != nil {
			return wf.Result{}, localErr
		}
	}

	var root *os.Root
	if localBound {
		physical := localRoots.ResourcePathRoot(p)
		if localFiles {
			physical = localRoots.Files
		}
		root, err = os.OpenRoot(physical)
		if err != nil {
			return wf.Result{}, err
		}
	} else {
		base, err := os.OpenRoot(getWorkspaceDocsAbsPath())
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				result, err := externalRemoteFileRequest(ctx, req, p)
				return externalGuardReadResult(result, err, guard)
			}
			return wf.Result{}, err
		}
		defer base.Close()
		if err := externalNoSymlinks(base, rootPath); err != nil {
			return wf.Result{}, err
		}
		root, err = base.OpenRoot(rootPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				result, err := externalRemoteFileRequest(ctx, req, p)
				return externalGuardReadResult(result, err, guard)
			}
			return wf.Result{}, err
		}
	}
	defer root.Close()
	if err := externalNoSymlinks(root, p); err != nil {
		return wf.Result{}, err
	}
	shared := externalRootUsesSharedKnowledge(root)
	if localBound {
		private, openErr := os.OpenRoot(localRoots.Private)
		if openErr != nil {
			return wf.Result{}, openErr
		}
		shared = !localFiles && externalRootUsesSharedKnowledge(private)
		private.Close()
	}
	if shared && (p == "knowledgebase" || strings.HasPrefix(p, "knowledgebase/")) {
		return wf.Result{}, &externalUpstreamError{403, "local knowledge archive is unavailable; use shared Brain MCP"}
	}
	switch req.Operation {
	case "read":
		file, err := externalScopedFile(root, p)
		if filesPrefix != "" {
			file.Path = path.Join(filesPrefix, file.Path)
		}
		return wf.Result{File: file}, err
	case "list", "search":
		result, err := externalListFiles(ctx, root, p, req, crewRoot, localFiles, shared, localBound && localRoots.Local && !localFiles)
		if err == nil && sourcePublication != nil && rootPath == sourcePublication.sourceRoot() {
			runtimeReq := req
			// Agent runtime paths have their own namespace. An ordinary Crew's
			// shared read surface still exposes only its ordinary logical root.
			runtimeReq.Root = sourcePublication.runtimeRoot()
			if sourcePublication.AgentID != "" {
				runtimeReq.Root = req.Root
				runtimeReq.Path = path.Join("outputs/agents", sourcePublication.AgentID, p)
			}
			live, e := externalFileRequest(context.WithValue(ctx, publicationFileReadKey{}, true), runtimeReq)
			if e != nil {
				return wf.Result{}, e
			}
			sourceFiles := map[string]bool{}
			for _, file := range sourcePublication.SourcePaths {
				sourceFiles[file] = true
			}
			for _, entry := range live.Entries {
				if sourcePublication.AgentID != "" {
					entry.Path = strings.TrimPrefix(entry.Path, "outputs/agents/"+sourcePublication.AgentID+"/")
				}
				if publishedRuntimeRelative(entry.Path) && !sourceFiles[entry.Path] {
					result.Entries = append(result.Entries, entry)
				}
			}
			result.Truncated = result.Truncated || live.Truncated
		}
		result, err = externalGuardReadResult(result, err, guard)
		if filesPrefix != "" {
			result.File.Path = path.Join(filesPrefix, result.File.Path)
			for i := range result.Entries {
				result.Entries[i].Path = path.Join(filesPrefix, result.Entries[i].Path)
			}
		}
		return result, err
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
	if st.Size() > wf.MaxBinaryFileBytes {
		return result, &externalUpstreamError{413, fmt.Sprintf("file exceeds %d byte limit", wf.MaxBinaryFileBytes)}
	}
	data, err := io.ReadAll(io.LimitReader(f, wf.MaxBinaryFileBytes+1))
	if err != nil {
		return result, err
	}
	if len(data) > wf.MaxBinaryFileBytes {
		return result, &externalUpstreamError{413, "file exceeds limit"}
	}
	result.Exists = true
	result.Size = int64(len(data))
	result.Revision = wf.Revision(data)
	if len(data) > wf.MaxFileBytes {
		result.Encoding = "binary"
		return result, nil
	}
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
// "_users/<owner>/Chats/{Work,Code}/projects/<id>".
func externalIsCrewRoot(rootPath string) bool {
	parts := strings.Split(rootPath, "/")
	switch {
	case len(parts) == 2 && parts[0] == crewSharedRootName:
		return parts[1] != "" && parts[1] != "."
	}
	ref := workspaceref.MustParse(rootPath)
	root, _, ok := ref.ProjectRoot()
	return ok && (root == workspaceref.CrewProjectsRoot || root == workspaceref.CodeProjectsRoot) && ref.HasOwner() && ref.String() == rootPath
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

func externalListFiles(ctx context.Context, root *os.Root, p string, req wf.Request, crewRoot bool, localFiles bool, shared bool, includeFiles bool) (wf.Result, error) {
	guard := externalFileGuard(ctx, crewRoot)
	if req.Limit <= 0 {
		req.Limit = 100
	}
	if req.Limit > 200 || req.Offset < 0 || req.Offset > 10000 {
		return wf.Result{}, &externalUpstreamError{400, "invalid pagination"}
	}
	if req.Depth <= 0 {
		// A search covers the whole workflow unless the caller narrows it; a
		// listing stays shallow (MCP feedback, 2026-10-08: a plain search at
		// depth 4 skipped everything below and looked empty).
		req.Depth = 4
		if req.Operation == "search" {
			req.Depth = 8
		}
	}
	if req.Depth > 8 {
		return wf.Result{}, &externalUpstreamError{400, "depth exceeds 8"}
	}
	if req.Operation == "search" && req.Query == "" {
		return wf.Result{}, &externalUpstreamError{400, "query is required"}
	}
	var matches func(string) bool
	if req.Operation == "search" {
		var err error
		if matches, err = wf.LineMatcher(req.Query, req.MatchCase, req.WholeWord, req.Regex); err != nil {
			return wf.Result{}, &externalUpstreamError{400, err.Error()}
		}
	}
	if _, err := root.Lstat(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return wf.Result{}, &externalUpstreamError{404, "path does not exist: " + p}
		}
		return wf.Result{}, err
	}
	all := make([]wf.Entry, 0)
	if includeFiles && p == "." && req.Operation == "list" && guard.AllowsTraversal("_files") && wf.MatchGlob(req.Glob, "_files") {
		all = append(all, wf.Entry{Path: "_files", Type: "folder"})
	}
	visited := 0
	var scannedBytes int64
	truncated := false
	depthLimited := false
	searchedFiles := 0
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
		if localFiles && (strings.EqualFold(name, ".agentworks") || strings.HasPrefix(strings.ToLower(name), ".agentworks/")) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || externalPathPrivate(crewRoot, name) || shared && (name == "knowledgebase" || strings.HasPrefix(name, "knowledgebase/")) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !guard.Allows(name, false) && !(d.IsDir() && guard.AllowsTraversal(name)) {
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
				depthLimited = true
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
		} else if matches(rel) {
			// A search also finds files and folders by name, so "components"
			// finds src/components, not only text inside files.
			all = append(all, entry)
		}
		if req.Operation == "search" && st.Mode().IsRegular() && st.Size() <= wf.MaxFileBytes {
			searchedFiles++
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
					if matches(line) {
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
	result := wf.Result{File: wf.File{Path: p, Exists: true}, Entries: all[start:end], Truncated: truncated, DepthLimited: depthLimited, Searched: searchedFiles}
	if len(all) > end {
		result.NextOffset = end
	}
	if depthLimited && req.Operation == "search" {
		result.Note = fmt.Sprintf("Folders deeper than depth %d were not searched; raise depth (max 8) or search a subfolder with path.", req.Depth)
	}
	if len(result.Entries) == 0 && req.Operation == "search" {
		result.Note = strings.TrimSpace(fmt.Sprintf("No file name or text under %q contains %q (searched %d files at depth %d). %s", p, req.Query, searchedFiles, req.Depth, result.Note))
	}
	return result, nil
}

type externalFileGuardKey struct{}

// File guards narrow every selected project, including Crew. Product roles
// and Crew privacy checks remain independent and cannot override these caps.
func externalFileGuard(ctx context.Context, _ bool) *wf.FolderGuard {
	if guard, ok := ctx.Value(externalFileGuardKey{}).(*wf.FolderGuard); ok {
		return guard
	}
	if claims := GetUserFromContext(ctx); claims != nil && claims.AccessToken != nil {
		return claims.AccessToken.FileGuard
	}
	return nil
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

func externalGuardReadResult(result wf.Result, err error, guard *wf.FolderGuard) (wf.Result, error) {
	if err != nil || guard == nil {
		return result, err
	}
	visible := result.Entries[:0]
	for _, entry := range result.Entries {
		if guard.Allows(entry.Path, false) || entry.Type == "folder" && guard.AllowsTraversal(entry.Path) {
			visible = append(visible, entry)
		}
	}
	result.Entries = visible
	return result, nil
}

// crewCallerPrivatePath is a Crew area that belongs to the owner and co-owners: the inputs and results of other
// callers' function calls (triggers/deliveries) and the schedule run log. A guest's ask and any non-owner reader get
// "file not found or private"; a caller still sees their own calls through the caller-filtered run tools (856-65).
func crewCallerPrivatePath(p string) bool {
	p = strings.ToLower(strings.Trim(path.Clean("/"+strings.TrimSpace(p)), "/"))
	return p == "schedule-runs.json" || p == "triggers/deliveries" || strings.HasPrefix(p, "triggers/deliveries/")
}

// withoutCrewCallerPrivate removes those areas from a listing or search result.
func withoutCrewCallerPrivate(result wf.Result) wf.Result {
	kept := result.Entries[:0:0]
	for _, entry := range result.Entries {
		if !crewCallerPrivatePath(entry.Path) {
			kept = append(kept, entry)
		}
	}
	result.Entries = kept
	return result
}

// externalTextSearchSchema adds the Files panel's search options to a file
// search tool, so the screen and chat share one matcher (wf.LineMatcher).
func externalTextSearchSchema(p map[string]any) {
	p["match_case"] = map[string]any{"type": "boolean", "description": "Match upper and lower case exactly. Off by default."}
	p["whole_word"] = map[string]any{"type": "boolean", "description": "Match whole words only. Off by default."}
	p["regex"] = map[string]any{"type": "boolean", "description": "Treat query as a regular expression (RE2 syntax). Off by default."}
}

func externalTextSearchArgs(req *wf.Request, args map[string]any) {
	req.MatchCase, _ = args["match_case"].(bool)
	req.WholeWord, _ = args["whole_word"].(bool)
	req.Regex, _ = args["regex"].(bool)
}
