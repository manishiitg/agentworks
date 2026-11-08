package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/runlog"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

func prepareShellLog(req models.ExecuteShellRequest, docsDir, workingDir string) (*runlog.Log, string) {
	destination := req.LogFile
	if destination == "" {
		return nil, "log destination is required"
	}
	resolved, err := security.ResolveShellLogPath(destination, docsDir, req.FolderGuard)
	if err != nil {
		return nil, err.Error()
	}
	// Runs live beneath docs. A bound local project may live elsewhere; only
	// its already-resolved cwd is a permitted anchor in that case.
	anchor, err := filepath.EvalSymlinks(docsDir)
	if err != nil {
		return nil, err.Error()
	}
	rel, err := filepath.Rel(anchor, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		anchor, err = filepath.EvalSymlinks(workingDir)
		if err != nil {
			return nil, err.Error()
		}
		rel, err = filepath.Rel(anchor, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, "log is outside the command's workspace"
		}
	}
	root, err := os.OpenRoot(anchor)
	if err != nil {
		return nil, err.Error()
	}
	defer root.Close()
	log, err := runlog.Create(root, rel)
	if err != nil {
		return nil, err.Error()
	}
	return log, ""
}

// No automatic retention is applied yet; spilled files live with their private
// chat/run records. Ordinary short commands never create a file.
func prepareShellCapture(req models.ExecuteShellRequest, docsDir, workingDir string) *runlog.Capture {
	limit := req.LogBufferBytes
	if limit <= 0 {
		limit = 48000
	}
	eager := req.LogFile != ""
	capture := runlog.NewCapture(limit, func() (*runlog.Log, error) {
		if !eager {
			if req.LogDirectory == "" {
				return nil, fmt.Errorf("full log unavailable: this session has no private log write grant")
			}
			req.LogFile = filepath.Join(req.LogDirectory, fmt.Sprintf("shell-%d.log", time.Now().UnixNano()))
		}
		log, reason := prepareShellLog(req, docsDir, workingDir)
		if reason != "" {
			return nil, fmt.Errorf("full log unavailable: %s", reason)
		}
		return log, nil
	}, eager)
	capture.Note("command starting")
	return capture
}
