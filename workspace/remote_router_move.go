package main

// Moving a workflow between this workspace and a remote workspace server.
// A workflow lives in exactly one place, so a move copies it, verifies the
// copy, flips placement, and retires the old copy (a recovery point, never
// read by anything). Moves are serialized; a failed move to a server removes
// the partial server copy so it can be retried.
//
// Known limit: a move does not check for a run in progress on this machine.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

const movedWorkflowsRelPath = "_system/moved-workflows"

// excludedFromMove are workflow-local folders that must not travel: the
// sandbox cache holds this machine's HOME (git credentials, CLI config) and
// platform-specific pip/npm builds.
var excludedFromMove = map[string]bool{".sandbox-cache": true}

func excludedMoveEntry(rel string) bool {
	first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	return excludedFromMove[first]
}

// handleRemoteAPI serves /api/remote/*. Returns false when the path is not a
// remote-management route. Callers are already token-checked.
func (rr *remoteRouter) handleRemoteAPI(c *gin.Context) bool {
	switch c.Request.URL.Path {
	case "/api/remote/whoami":
		// In server mode X-User-ID was pinned from the caller's token.
		c.JSON(http.StatusOK, gin.H{"success": true, "user": strings.TrimSpace(c.GetHeader("X-User-ID")), "docs_dir": rr.localRoot})
	case "/api/remote/placements":
		cfg := rr.config()
		servers := map[string]string{}
		for id, srv := range cfg.Servers {
			servers[id] = srv.URL
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "servers": servers, "workflows": cfg.Workflows})
	case "/api/remote/move-to-server":
		var req struct {
			Workflow string `json:"workflow"`
			Server   string `json:"server"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
			break
		}
		result, err := rr.moveToServer(c, req.Workflow, req.Server)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
			break
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "result": result})
	case "/api/remote/move-to-local":
		var req struct {
			Workflow string `json:"workflow"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
			break
		}
		result, err := rr.moveToLocal(c, req.Workflow)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
			break
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "result": result})
	default:
		return false
	}
	c.Abort()
	return true
}

func validateWorkflowRel(workflow string) (string, error) {
	rel := cleanRelPath(workflow)
	parts := strings.Split(rel, "/")
	if len(parts) != 2 || parts[0] != "Workflow" || parts[1] == "" || strings.HasPrefix(parts[1], ".") {
		return "", fmt.Errorf("workflow must be Workflow/<name>, got %q", workflow)
	}
	return rel, nil
}

type moveResult struct {
	Workflow string `json:"workflow"`
	Server   string `json:"server,omitempty"`
	Files    int    `json:"files"`
	Retired  string `json:"retired_copy"`
}

func escapedFolderPath(rel string) string {
	return (&url.URL{Path: rel}).EscapedPath()
}

// deleteServerFolder removes a workflow folder on the server (rollback or
// retiring the server copy).
func (rr *remoteRouter) deleteServerFolder(c *gin.Context, id string, srv remoteServerConfig, rel string) error {
	resp, body, err := rr.callServer(c.Request.Context(), id, srv, http.MethodDelete, "/api/folders/"+escapedFolderPath(rel)+"?confirm=true", http.Header{}, nil, false)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (rr *remoteRouter) moveToServer(c *gin.Context, workflow, serverID string) (result moveResult, err error) {
	rr.moveMu.Lock()
	defer rr.moveMu.Unlock()

	rel, err := validateWorkflowRel(workflow)
	if err != nil {
		return moveResult{}, err
	}
	cfg := rr.config()
	srv, ok := cfg.Servers[serverID]
	if !ok {
		return moveResult{}, fmt.Errorf("unknown workspace server %q", serverID)
	}
	if _, id := rr.placement(cfg, rel); id != "" {
		return moveResult{}, fmt.Errorf("%s is already on server %q", rel, id)
	}
	localDir := filepath.Join(rr.localRoot, filepath.FromSlash(rel))
	if info, statErr := os.Stat(localDir); statErr != nil || !info.IsDir() {
		return moveResult{}, fmt.Errorf("%s does not exist locally", rel)
	}
	serverRoot, err := rr.serverRoot(c.Request.Context(), serverID, srv)
	if err != nil {
		return moveResult{}, err
	}
	existing, err := rr.countServerFiles(c, serverID, srv, rel)
	if err != nil {
		return moveResult{}, err
	}
	if existing > 0 {
		return moveResult{}, fmt.Errorf("%s already has %d files on server %q; refusing to overwrite", rel, existing, serverID)
	}

	archive, files, err := zipFolderToTemp(localDir, rr.localRoot, serverRoot)
	if err != nil {
		return moveResult{}, fmt.Errorf("pack %s: %w", rel, err)
	}
	defer os.Remove(archive)

	// From here on, any failure removes the partial server copy so the move
	// can simply be retried.
	imported := false
	defer func() {
		if err != nil && imported {
			if delErr := rr.deleteServerFolder(c, serverID, srv, rel); delErr != nil {
				err = fmt.Errorf("%w (and the partial server copy could not be removed: %v)", err, delErr)
			}
		}
	}()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, perr := mw.CreateFormFile("file", filepath.Base(rel)+".zip")
		if perr == nil {
			var f *os.File
			if f, perr = os.Open(archive); perr == nil { // #nosec G304 -- temp file created above
				_, perr = io.Copy(part, f)
				_ = f.Close()
			}
		}
		if perr == nil {
			perr = mw.WriteField("workspace_path", rel)
		}
		if perr == nil {
			// Safe: the server folder was just verified to hold no files.
			perr = mw.WriteField("overwrite", "true")
		}
		if perr == nil {
			perr = mw.Close()
		}
		_ = pw.CloseWithError(perr)
	}()
	header := http.Header{}
	header.Set("Content-Type", mw.FormDataContentType())
	imported = true // a failed upload can still leave files behind
	resp, body, err := rr.callServer(c.Request.Context(), serverID, srv, http.MethodPost, "/api/workspace/import", header, pr, false)
	if err != nil {
		return moveResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return moveResult{}, fmt.Errorf("server import failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	remoteFiles, err := rr.countServerFiles(c, serverID, srv, rel)
	if err != nil {
		return moveResult{}, err
	}
	if remoteFiles != files {
		return moveResult{}, fmt.Errorf("server copy of %s has %d files, local has %d; move rolled back", rel, remoteFiles, files)
	}
	if err = rr.setPlacement(rel, serverID); err != nil {
		return moveResult{}, err
	}
	imported = false // placement flipped: the server copy is now the copy
	retired, retireErr := rr.retireLocalCopy(localDir, rel)
	if retireErr != nil {
		return moveResult{}, fmt.Errorf("%s is now served from %q, but the old local copy could not be retired: %w", rel, serverID, retireErr)
	}
	return moveResult{Workflow: rel, Server: serverID, Files: files, Retired: retired}, nil
}

func (rr *remoteRouter) moveToLocal(c *gin.Context, workflow string) (moveResult, error) {
	rr.moveMu.Lock()
	defer rr.moveMu.Unlock()

	rel, err := validateWorkflowRel(workflow)
	if err != nil {
		return moveResult{}, err
	}
	cfg := rr.config()
	_, serverID := rr.placement(cfg, rel)
	if serverID == "" {
		return moveResult{}, fmt.Errorf("%s is not placed on a server", rel)
	}
	srv := cfg.Servers[serverID]
	localDir := filepath.Join(rr.localRoot, filepath.FromSlash(rel))
	if _, err := os.Stat(localDir); err == nil {
		return moveResult{}, fmt.Errorf("a local folder already exists at %s; remove it first", rel)
	}
	user, serverRoot, err := rr.whoami(c.Request.Context(), serverID, srv)
	if err != nil {
		return moveResult{}, err
	}
	payload, _ := json.Marshal(map[string]string{"workspace_path": rel})
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	resp, archive, err := rr.callServer(c.Request.Context(), serverID, srv, http.MethodPost, "/api/workspace/export", header, bytes.NewReader(payload), false)
	if err != nil {
		return moveResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return moveResult{}, fmt.Errorf("server export failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(archive)))
	}
	files, err := unzipWorkflow(archive, localDir, serverRoot, rr.localRoot)
	if err != nil {
		_ = os.RemoveAll(localDir)
		return moveResult{}, fmt.Errorf("unpack %s: %w", rel, err)
	}
	if err := rr.setPlacement(rel, ""); err != nil {
		_ = os.RemoveAll(localDir)
		return moveResult{}, err
	}
	// Retire the server copy into the moving user's own server area, which
	// keeps it recoverable there and is a path that user may write.
	stamp := time.Now().UTC().Format("20060102T150405Z")
	retiredRel := "_users/" + user + "/moved-workflows/" + filepath.Base(rel) + "-" + stamp
	copyBody, _ := json.Marshal(map[string]string{"source_path": rel, "destination_path": retiredRel})
	if resp, body, err := rr.callServer(c.Request.Context(), serverID, srv, http.MethodPost, "/api/folders/copy", header, bytes.NewReader(copyBody), false); err != nil || resp.StatusCode >= 300 {
		return moveResult{Workflow: rel, Files: files}, fmt.Errorf("%s is local again, but the server copy was not archived (%v %s); delete it on the server", rel, err, strings.TrimSpace(string(body)))
	}
	if err := rr.deleteServerFolder(c, serverID, srv, rel); err != nil {
		return moveResult{Workflow: rel, Files: files}, fmt.Errorf("%s is local again and the server copy was archived, but not deleted: %v", rel, err)
	}
	return moveResult{Workflow: rel, Files: files, Retired: "server:" + retiredRel}, nil
}

// countServerFiles counts a workflow's files on the server, ignoring folders
// that never travel with a move.
func (rr *remoteRouter) countServerFiles(c *gin.Context, id string, srv remoteServerConfig, rel string) (int, error) {
	q := url.Values{}
	q.Set("pattern", "**/*")
	q.Set("folder", rel)
	resp, body, err := rr.callServer(c.Request.Context(), id, srv, http.MethodGet, "/api/glob?"+q.Encode(), http.Header{}, nil, false)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return 0, nil
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("verify server copy (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Data []struct {
			Filepath string `json:"filepath"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("verify server copy: %w", err)
	}
	count := 0
	for _, f := range parsed.Data {
		inner := strings.TrimPrefix(cleanRelPath(f.Filepath), rel+"/")
		if !excludedMoveEntry(inner) {
			count++
		}
	}
	return count, nil
}

// setPlacement updates the placement file. Callers hold rr.moveMu.
func (rr *remoteRouter) setPlacement(rel, serverID string) error {
	var cfg remotePlacementConfig
	if raw, err := os.ReadFile(rr.cfgPath); err == nil { // #nosec G304 -- operator-owned path outside the docs root
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parse %s: %w", rr.cfgPath, err)
		}
	}
	if cfg.Workflows == nil {
		cfg.Workflows = map[string]string{}
	}
	if serverID == "" {
		delete(cfg.Workflows, rel)
	} else {
		cfg.Workflows[rel] = serverID
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(rr.cfgPath), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(rr.cfgPath), "."+filepath.Base(rr.cfgPath)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, rr.cfgPath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	rr.mu.Lock()
	rr.cfgCheck = time.Time{}
	rr.mu.Unlock()
	return nil
}

func (rr *remoteRouter) retireLocalCopy(localDir, rel string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	retiredRel := movedWorkflowsRelPath + "/" + filepath.Base(rel) + "-" + stamp
	dest := filepath.Join(rr.localRoot, filepath.FromSlash(retiredRel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(localDir, dest); err != nil {
		return "", err
	}
	return retiredRel, nil
}

// zipFolderToTemp packs dir into a temp zip (entries relative to dir),
// skipping excluded folders and rewriting embedded docs roots in text files.
// Returns the archive path and its file count.
func zipFolderToTemp(dir, fromRoot, toRoot string) (string, int, error) {
	out, err := os.CreateTemp("", "workflow-move-*.zip")
	if err != nil {
		return "", 0, err
	}
	archive := out.Name()
	fail := func(err error) (string, int, error) {
		_ = out.Close()
		_ = os.Remove(archive)
		return "", 0, err
	}
	zw := zip.NewWriter(out)
	files := 0
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel != "." && excludedMoveEntry(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		data, err := os.ReadFile(p) // #nosec G304 -- walking the workflow folder being moved
		if err != nil {
			return err
		}
		if utf8.Valid(data) {
			data = rewriteRoots(data, fromRoot, toRoot)
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		files++
		return nil
	})
	if err != nil {
		return fail(err)
	}
	if err := zw.Close(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(archive)
		return "", 0, err
	}
	return archive, files, nil
}

// unzipWorkflow extracts a server export (entries prefixed with the workflow
// folder name) into dest, skipping folders that never travel.
func unzipWorkflow(archive []byte, dest, fromRoot, toRoot string) (int, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return 0, err
	}
	base := filepath.Base(dest)
	files := 0
	for _, f := range zr.File {
		name := filepath.ToSlash(f.Name)
		name = strings.TrimPrefix(name, base+"/")
		clean := cleanRelPath(name)
		if clean == "" || strings.HasSuffix(f.Name, "/") || excludedMoveEntry(clean) {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(clean))
		if !strings.HasPrefix(target, dest+string(os.PathSeparator)) {
			return files, fmt.Errorf("unsafe entry %q", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return files, err
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return files, err
		}
		if utf8.Valid(data) {
			data = rewriteRoots(data, fromRoot, toRoot)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return files, err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil { // #nosec G306 -- workspace documents are 0644 like every other workspace write
			return files, err
		}
		files++
	}
	return files, nil
}
