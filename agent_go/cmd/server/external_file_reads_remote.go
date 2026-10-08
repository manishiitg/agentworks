package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Some deployments run the workspace service on a separate volume. In that
// case the existing read-only shared-assets proxy is the filesystem surface;
// the retired revision/write endpoint is never used.
func externalRemoteFileRequest(ctx context.Context, req wf.Request, p string) (wf.Result, error) {
	switch req.Operation {
	case "read":
		file, err := externalRemoteReadFile(ctx, req.Root, p)
		return wf.Result{File: file}, err
	case "list", "search":
		return externalRemoteListFiles(ctx, req, p)
	default:
		return wf.Result{}, &externalUpstreamError{400, "unsupported file operation"}
	}
}

func externalRemoteAssetCall(ctx context.Context, root, p, operation string) (*http.Response, error) {
	data, err := json.Marshal(map[string]string{"root": root, "path": p, "operation": operation})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, getWorkspaceAPIURL()+"/api/shared-assets", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-Token", os.Getenv("WORKSPACE_API_TOKEN"))
	return workspaceHTTPClient.Do(request)
}

func externalRemoteReadFile(ctx context.Context, root, p string) (wf.File, error) {
	result := wf.File{Path: p, Revision: wf.MissingRevision}
	response, err := externalRemoteAssetCall(ctx, root, p, "read")
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return result, nil
	}
	if response.StatusCode != http.StatusOK {
		return result, &externalUpstreamError{response.StatusCode, fmt.Sprintf("workspace file read returned %d", response.StatusCode)}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, wf.MaxFileBytes+1))
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

func externalRemoteListFiles(ctx context.Context, req wf.Request, p string) (wf.Result, error) {
	guard := externalFileGuard(ctx, externalIsCrewRoot(req.Root))
	if err := wf.ValidateGlob(req.Glob); err != nil {
		return wf.Result{}, &externalUpstreamError{400, err.Error()}
	}
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
	response, err := externalRemoteAssetCall(ctx, req.Root, p, "list")
	if err != nil {
		return wf.Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusBadRequest {
			// The shared-assets list endpoint reports a missing directory as
			// 400. Confirm with stat so callers can distinguish an empty code
			// inventory from a malformed request.
			stat, statErr := externalRemoteAssetCall(ctx, req.Root, p, "stat")
			if statErr == nil {
				stat.Body.Close()
				if stat.StatusCode == http.StatusNotFound {
					return wf.Result{}, &externalUpstreamError{404, "path does not exist: " + p}
				}
			}
		}
		return wf.Result{}, &externalUpstreamError{response.StatusCode, fmt.Sprintf("workspace file list returned %d", response.StatusCode)}
	}
	var listing struct {
		Data []struct {
			Path string `json:"filepath"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&listing); err != nil {
		return wf.Result{}, err
	}
	all := make([]wf.Entry, 0)
	var scannedBytes int64
	truncated := false
	for _, item := range listing.Data {
		if err := ctx.Err(); err != nil {
			return wf.Result{}, err
		}
		if wf.Private(item.Path) {
			continue
		}
		if !guard.Allows(item.Path, false) && !(item.Type == "folder" && guard.AllowsTraversal(item.Path)) {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(item.Path, p), "/")
		if strings.Count(rel, "/") >= req.Depth {
			if item.Type == "folder" {
				truncated = true
			}
			continue
		}
		entry := wf.Entry{Path: item.Path, Type: item.Type, Size: item.Size}
		if !wf.MatchGlob(req.Glob, rel) {
			continue
		}
		if req.Operation == "list" {
			all = append(all, entry)
		} else if item.Type == "file" && item.Size <= wf.MaxFileBytes {
			scannedBytes += item.Size
			if scannedBytes > 64<<20 {
				truncated = true
				break
			}
			file, err := externalRemoteReadFile(ctx, req.Root, item.Path)
			if err != nil {
				return wf.Result{}, err
			}
			if file.Encoding == "utf-8" {
				for lineNumber, line := range strings.Split(file.Content, "\n") {
					if strings.Contains(strings.ToLower(line), strings.ToLower(req.Query)) {
						match := entry
						match.Line = lineNumber + 1
						if len(line) > 1000 {
							line = line[:1000]
						}
						match.Text = line
						all = append(all, match)
						if len(all) > req.Offset+req.Limit {
							truncated = true
							break
						}
					}
				}
			}
		}
		if len(all) > req.Offset+req.Limit {
			truncated = true
			break
		}
	}
	start := min(req.Offset, len(all))
	end := min(start+req.Limit, len(all))
	result := wf.Result{File: wf.File{Path: p, Exists: true}, Entries: all[start:end], Truncated: truncated}
	if len(all) > end {
		result.NextOffset = end
	}
	return result, nil
}
