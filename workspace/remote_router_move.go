package main

// Moving a workflow between this workspace and a remote workspace server.
// A workflow lives in exactly one place, so a move copies it, verifies the
// copy, flips placement, and retires the old copy to _system/moved-workflows
// (a recovery point, never read by anything).

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

// handleRemoteAPI serves /api/remote/*. Returns false when the path is not a
// remote-management route.
func (rr *remoteRouter) handleRemoteAPI(c *gin.Context) bool {
	switch c.Request.URL.Path {
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

func (rr *remoteRouter) moveToServer(c *gin.Context, workflow, serverID string) (moveResult, error) {
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
	if info, err := os.Stat(localDir); err != nil || !info.IsDir() {
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

	archive, files, err := zipFolder(localDir, rr.localRoot, serverRoot)
	if err != nil {
		return moveResult{}, fmt.Errorf("pack %s: %w", rel, err)
	}
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	part, _ := mw.CreateFormFile("file", filepath.Base(rel)+".zip")
	_, _ = part.Write(archive)
	_ = mw.WriteField("workspace_path", rel)
	// Safe: the server folder was just verified to hold no files (an empty
	// leftover from an interrupted move is the only thing replaced).
	_ = mw.WriteField("overwrite", "true")
	_ = mw.Close()
	header := http.Header{}
	header.Set("Content-Type", mw.FormDataContentType())
	header.Set("X-User-ID", c.GetHeader("X-User-ID"))
	resp, body, err := rr.callServer(c.Request.Context(), serverID, srv, http.MethodPost, "/api/workspace/import", header, form.Bytes())
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
		return moveResult{}, fmt.Errorf("server copy of %s has %d files, local has %d; placement not changed", rel, remoteFiles, files)
	}

	if err := rr.setPlacement(rel, serverID); err != nil {
		return moveResult{}, err
	}
	retired, err := rr.retireLocalCopy(localDir, rel)
	if err != nil {
		return moveResult{}, fmt.Errorf("%s is now served from %q, but the old local copy could not be retired: %w", rel, serverID, err)
	}
	return moveResult{Workflow: rel, Server: serverID, Files: files, Retired: retired}, nil
}

func (rr *remoteRouter) moveToLocal(c *gin.Context, workflow string) (moveResult, error) {
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
	serverRoot, err := rr.serverRoot(c.Request.Context(), serverID, srv)
	if err != nil {
		return moveResult{}, err
	}
	payload, _ := json.Marshal(map[string]string{"workspace_path": rel})
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("X-User-ID", c.GetHeader("X-User-ID"))
	resp, archive, err := rr.callServer(c.Request.Context(), serverID, srv, http.MethodPost, "/api/workspace/export", header, payload)
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
		return moveResult{}, err
	}
	// The server copy is retired by renaming it inside the server's own
	// workspace, which keeps it recoverable there.
	stamp := time.Now().UTC().Format("20060102T150405Z")
	retiredRel := movedWorkflowsRelPath + "/" + filepath.Base(rel) + "-" + stamp
	copyBody, _ := json.Marshal(map[string]string{"source_path": rel, "destination_path": retiredRel})
	if resp, body, err := rr.callServer(c.Request.Context(), serverID, srv, http.MethodPost, "/api/folders/copy", header, copyBody); err != nil || resp.StatusCode >= 300 {
		return moveResult{Workflow: rel, Files: files}, fmt.Errorf("%s is local again, but the server copy was not retired (%v %s); delete it on the server", rel, err, strings.TrimSpace(string(body)))
	}
	delPath := "/api/folders/" + (&url.URL{Path: rel}).EscapedPath() + "?confirm=true"
	if resp, body, err := rr.callServer(c.Request.Context(), serverID, srv, http.MethodDelete, delPath, header, nil); err != nil || resp.StatusCode >= 300 {
		return moveResult{Workflow: rel, Files: files}, fmt.Errorf("%s is local again and the server copy was archived, but not deleted (%v %s)", rel, err, strings.TrimSpace(string(body)))
	}
	return moveResult{Workflow: rel, Files: files, Retired: "server:" + retiredRel}, nil
}

func (rr *remoteRouter) countServerFiles(c *gin.Context, id string, srv remoteServerConfig, rel string) (int, error) {
	q := url.Values{}
	q.Set("pattern", "**/*")
	q.Set("folder", rel)
	header := http.Header{}
	header.Set("X-User-ID", c.GetHeader("X-User-ID"))
	resp, body, err := rr.callServer(c.Request.Context(), id, srv, http.MethodGet, "/api/glob?"+q.Encode(), header, nil)
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
	return len(parsed.Data), nil
}

func (rr *remoteRouter) setPlacement(rel, serverID string) error {
	cfgPath := filepath.Join(rr.localRoot, filepath.FromSlash(remotePlacementRelPath))
	var cfg remotePlacementConfig
	if raw, err := os.ReadFile(cfgPath); err == nil { // #nosec G304 -- fixed path under the docs root
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parse %s: %w", remotePlacementRelPath, err)
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
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return err
	}
	tmp := cfgPath + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, cfgPath); err != nil {
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

// zipFolder packs dir with entries relative to it, rewriting embedded docs
// roots in text files. Returns the archive and its file count.
func zipFolder(dir, fromRoot, toRoot string) ([]byte, int, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
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
		return nil, 0, err
	}
	if err := zw.Close(); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), files, nil
}

// unzipWorkflow extracts a server export (entries prefixed with the workflow
// folder name) into dest.
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
		if clean == "" || strings.HasSuffix(f.Name, "/") {
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
