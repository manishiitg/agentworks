package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// SearchDocuments handles GET /api/search
func SearchDocuments(c *gin.Context) {
	var req models.SearchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid query parameters",
			Error:   err.Error(),
		})
		return
	}

	docsDir := viper.GetString("docs-dir")
	folder := req.Folder

	// Parse blocked paths from comma-separated string
	var blockedPaths []string
	if req.BlockedPaths != "" {
		blockedPaths = strings.Split(req.BlockedPaths, ",")
		for i, p := range blockedPaths {
			blockedPaths[i] = strings.TrimSpace(p)
		}
		fmt.Printf("[FOLDER GUARD] Search with blocked paths: %v\n", blockedPaths)
	}

	// Build search path
	var searchPath string
	if folder != "" {
		var err error
		searchPath, err = resolveUserPath(c, folder)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Validate that the folder exists and is within docs directory
		if !utils.IsValidFilePath(searchPath, docsDir) {
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{
				Success: false,
				Message: "Invalid folder path",
				Error:   "Folder path contains invalid characters or attempts directory traversal",
			})
			return
		}

		// Check if folder exists
		if _, err := os.Stat(searchPath); os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, models.APIResponse[any]{
				Success: false,
				Message: "Folder not found",
				Error:   "The specified folder does not exist: " + folder,
			})
			return
		}
	} else {
		searchPath = docsDir
	}
	if req.SearchType == "filename" {
		items, err := scanDocuments(searchPath, docsDir, documentScanOptions{maxDepth: -1, limit: req.Limit, query: req.Query, blocked: blockedPaths, visibility: req.FileTreeVisibility, userID: getUserID(c), ctx: c.Request.Context()})
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Could not search files", Error: err.Error()})
			return
		}
		results := make([]models.SearchResult, 0, len(items))
		for _, item := range items {
			path := strings.TrimPrefix(item.FilePath, "_users/"+getUserID(c)+"/")
			kind := item.Type
			if kind == "" {
				kind = "file"
			}
			results = append(results, models.SearchResult{ID: path, Filepath: path, Title: filepath.Base(path), Folder: filepath.ToSlash(filepath.Dir(path)), Type: kind})
		}
		c.JSON(http.StatusOK, models.APIResponse[models.SearchResponse]{Success: true, Message: "Files searched", Data: models.SearchResponse{Query: req.Query, Results: results, Total: len(results), Method: "filename"}})
		return
	}

	matches, err := wf.LineMatcher(req.Query, req.MatchCase, req.WholeWord, req.Regex)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: err.Error(), Error: err.Error()})
		return
	}
	results, truncated, err := searchFileContents(c.Request.Context(), searchPath, docsDir, req, blockedPaths, getUserID(c), matches)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Could not search inside files", Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, models.APIResponse[models.SearchResponse]{Success: true, Message: "Files searched", Data: models.SearchResponse{Query: req.Query, Results: results, Total: len(results), Method: "content", Truncated: truncated}})
}

// Content search limits: matches returned, bytes read per file, time spent.
const (
	contentSearchMaxMatches  = 200
	contentSearchMaxFileSize = wf.MaxFileBytes
	contentSearchBudget      = 5 * time.Second
	contentSearchLineRunes   = 300
)

var errContentSearchFull = errors.New("content search reached its match cap")

// searchFileContents searches the text inside the files the file browser
// shows under searchPath. It walks with the browser's own scan (visibility,
// skipped build folders, Crew confinement), never reads private paths
// (wf.Private, the rule the file search tools use) or _system, and opens
// every file through os.Root so a link cannot leave the searched folder.
// It skips binary files and files over 2 MB, and stops at 200 matches or
// after 5 seconds; truncated reports either stop.
func searchFileContents(ctx context.Context, searchPath, docsDir string, req models.SearchRequest, blocked []string, userID string, matches func(string) bool) ([]models.SearchResult, bool, error) {
	info, err := os.Stat(searchPath)
	if err != nil {
		return nil, false, err
	}
	rootDir := searchPath
	if !info.IsDir() {
		rootDir = filepath.Dir(searchPath)
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	limit := contentSearchMaxMatches
	if req.Limit > 0 && req.Limit < limit {
		limit = req.Limit
	}
	budget, cancel := context.WithTimeout(ctx, contentSearchBudget)
	defer cancel()
	results := make([]models.SearchResult, 0)
	_, err = scanDocuments(searchPath, docsDir, documentScanOptions{maxDepth: -1, blocked: blocked, visibility: req.FileTreeVisibility, userID: userID, ctx: budget,
		file: func(path, issued string, info os.FileInfo) error {
			rel, e := filepath.Rel(rootDir, path)
			if e != nil || !info.Mode().IsRegular() || info.Size() > contentSearchMaxFileSize || contentSearchPrivate(issued, filepath.ToSlash(rel)) {
				return nil
			}
			data, ok := readContentSearchFile(root, rel)
			if !ok {
				return nil
			}
			shown := issued
			if userID != "" {
				shown = strings.TrimPrefix(issued, "_users/"+userID+"/")
			}
			for number, line := range strings.Split(string(data), "\n") {
				if !matches(line) {
					continue
				}
				results = append(results, models.SearchResult{ID: fmt.Sprintf("%s:%d", shown, number+1), Type: "file", Title: filepath.Base(shown), Filepath: shown, Folder: filepath.ToSlash(filepath.Dir(shown)), LineNumber: number + 1, MatchedText: trimContentSearchLine(line)})
				if len(results) >= limit {
					return errContentSearchFull
				}
			}
			return nil
		}})
	switch {
	case err == nil:
		return results, false, nil
	case errors.Is(err, errContentSearchFull):
		return results, true, nil
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		return results, true, nil
	}
	return nil, false, err
}

// contentSearchPrivate keeps secrets, Builder transcripts, databases, hidden
// folders and server state out of content search.
func contentSearchPrivate(issued, rel string) bool {
	return wf.Private(rel) || issued == "_system" || strings.HasPrefix(issued, "_system/")
}

// readContentSearchFile reads a regular text file inside root, or reports
// false for a binary, oversized, unreadable or linked-out file.
func readContentSearchFile(root *os.Root, rel string) ([]byte, bool) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > contentSearchMaxFileSize {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, contentSearchMaxFileSize+1))
	if err != nil || len(data) > contentSearchMaxFileSize || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return nil, false
	}
	return data, true
}

func trimContentSearchLine(line string) string {
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) <= contentSearchLineRunes {
		return line
	}
	return string([]rune(line)[:contentSearchLineRunes]) + "…"
}
