package handlers

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

type documentScanOptions struct {
	maxDepth, limit, offset int
	metadata                bool
	blocked                 []string
	visibility              models.FileTreeVisibility
	userID, query           string
	ctx                     context.Context
	// file, when set, receives each visible file (its physical path and
	// issued path) instead of the file being listed. Returning a non-nil
	// error stops the scan with that error.
	file func(path, issued string, info os.FileInfo) error
}

// Each listing gets one registry snapshot. Paths are still checked against
// the current filesystem before they can appear in either a list or a peek.
func scanDocuments(searchPath, docsDir string, opts documentScanOptions) ([]models.Document, error) {
	resolver, err := crewroot.NewResolver(docsDir)
	if err != nil {
		return nil, err
	}
	searchVirtual, roots, boundRoot, err := resolver.Reverse(searchPath)
	if err != nil {
		return nil, err
	}
	ignored := map[string]bool{".git": true, "node_modules": true, ".next": true, "dist": true, "build": true, "coverage": true, "__pycache__": true, ".venv": true, "venv": true}
	hidden := map[string]bool{}
	for _, name := range strings.Split(opts.visibility.HiddenEntries, ",") {
		hidden[name] = true
	}
	view := strings.Trim(opts.visibility.ViewRoot, "/")
	publicPath := func(path string) string {
		if opts.userID != "" {
			return strings.TrimPrefix(path, "_users/"+opts.userID+"/")
		}
		return path
	}
	visible := func(path string, info os.FileInfo) (string, bool) {
		if info.IsDir() && ignored[info.Name()] {
			return "", false
		}
		issued, _, bound, e := resolver.Reverse(path)
		if e != nil {
			return "", false
		}
		if boundRoot && (!bound || issued != searchVirtual && !strings.HasPrefix(issued, searchVirtual+"/")) {
			return "", false
		}
		if bound {
			resolved, _, e := resolver.Resolve(issued)
			if e != nil || filepath.Clean(resolved) != filepath.Clean(path) {
				return "", false
			}
		} else {
			issued, e = filepath.Rel(docsDir, path)
			if e != nil {
				return "", false
			}
			issued = filepath.ToSlash(issued)
		}
		shown := publicPath(issued)
		for _, blocked := range opts.blocked {
			if blocked = strings.TrimSuffix(strings.TrimSpace(blocked), "/"); blocked != "" && strings.HasPrefix(shown, blocked) {
				return "", false
			}
		}
		if view != "" && strings.HasPrefix(shown, view+"/") {
			first := strings.SplitN(strings.TrimPrefix(shown, view+"/"), "/", 2)[0]
			relative := strings.TrimPrefix(shown, view+"/")
			concealed := hidden[first]
			// The existing managed-files view keeps custom skills visible.
			if first == "skills" && concealed {
				concealed = relative != "skills" && relative != "skills/custom" && !strings.HasPrefix(relative, "skills/custom/")
				if relative == "skills" {
					custom, e := os.Stat(filepath.Join(path, "custom"))
					concealed = e != nil || !custom.IsDir()
				}
			}
			if concealed || opts.visibility.HideDotfiles && strings.HasPrefix(first, ".") {
				return "", false
			}
		}
		return issued, true
	}
	// Read in small batches and stop at the first showable entry. An unreadable
	// folder stays unknown; false means the same filters proved it empty.
	peek := func(path string) *bool {
		f, e := os.Open(path)
		if e != nil {
			return nil
		}
		defer f.Close()
		unknown := false
		for {
			entries, readErr := f.ReadDir(32)
			for _, entry := range entries {
				candidate := filepath.Join(path, entry.Name())
				info, statErr := os.Stat(candidate)
				if statErr != nil {
					unknown = true
					continue
				}
				if _, ok := visible(candidate, info); ok {
					answer := true
					return &answer
				}
			}
			if readErr == io.EOF {
				if unknown {
					return nil
				}
				answer := false
				return &answer
			}
			if readErr != nil {
				return nil
			}
		}
	}
	var documents []models.Document
	visited := map[string]bool{}
	count, skipped := 0, 0
	var walk func(string, int) error
	walk = func(path string, depth int) error {
		if opts.ctx != nil && opts.ctx.Err() != nil {
			return opts.ctx.Err()
		}
		if opts.maxDepth >= 0 && depth > opts.maxDepth || opts.query != "" && opts.limit > 0 && count >= opts.limit {
			return nil
		}
		info, e := os.Stat(path)
		if e != nil {
			if path == searchPath {
				return e
			}
			return nil
		}
		issued, ok := visible(path, info)
		if !ok {
			return nil
		}
		if info.IsDir() {
			real, e := filepath.EvalSymlinks(path)
			if e != nil || visited[real] {
				return nil
			}
			visited[real] = true
		}
		if opts.file != nil && !info.IsDir() {
			return opts.file(path, issued, info)
		}
		relative, _ := filepath.Rel(searchPath, path)
		matches := opts.query == "" || strings.Contains(strings.ToLower(filepath.ToSlash(relative)), strings.ToLower(opts.query))
		if matches && (path != searchPath || info.IsDir() && searchPath != docsDir) {
			if skipped < opts.offset {
				skipped++
			} else if info.IsDir() || opts.limit <= 0 || count < opts.limit {
				doc := models.Document{FilePath: issued}
				if info.IsDir() {
					doc.Type = "folder"
					if opts.maxDepth >= 0 {
						doc.HasChildren = peek(path)
					}
					if path == searchPath && boundRoot && roots.Local && searchVirtual == roots.FilesPath() {
						doc.DisplayName = filepath.Base(roots.Files)
					}
				} else if opts.metadata {
					doc.IsImage = isImageFile(info.Name())
					doc.Size = info.Size()
					doc.MimeType = detectFileMimeType(info.Name(), nil)
					doc.IsBinary = doc.IsImage || !isTextDocumentContent(info.Name(), doc.MimeType, nil)
				}
				if opts.metadata {
					doc.LastModified = info.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00")
				}
				documents = append(documents, doc)
				if !info.IsDir() || opts.query != "" {
					count++
				}
			}
		}
		if info.IsDir() && (opts.maxDepth < 0 || depth < opts.maxDepth) {
			entries, e := os.ReadDir(path)
			if e != nil {
				if path == searchPath {
					return e
				}
				return nil
			}
			for _, entry := range entries {
				if e := walk(filepath.Join(path, entry.Name()), depth+1); e != nil {
					return e
				}
			}
		}
		return nil
	}
	err = walk(searchPath, -1)
	return documents, err
}
