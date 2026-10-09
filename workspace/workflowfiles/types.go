// Package workflowfiles defines workflow-relative path and read-result types
// shared by the agent server and workspace service.
package workflowfiles

import (
	"crypto/sha256"
	"fmt"
	"path"
	"strings"
)

const MaxFileBytes = 2 << 20
const MissingRevision = "missing"

type Request struct {
	Root      string `json:"root"`
	Operation string `json:"operation"`
	Path      string `json:"path,omitempty"`
	Query     string `json:"query,omitempty"`
	Glob      string `json:"glob,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Depth     int    `json:"depth,omitempty"`
}
type File struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Content  string `json:"content,omitempty"`
	Encoding string `json:"encoding,omitempty"`
	Revision string `json:"revision"`
	Size     int64  `json:"size"`
}
type Entry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	Line int    `json:"line,omitempty"`
	Text string `json:"text,omitempty"`
}
type Result struct {
	File
	Entries    []Entry `json:"entries,omitempty"`
	NextOffset int     `json:"next_offset,omitempty"`
	// Truncated means the scan stopped at a size or count cap, so there may be
	// more results. DepthLimited means folders deeper than depth were skipped.
	Truncated    bool `json:"truncated,omitempty"`
	DepthLimited bool `json:"depth_limited,omitempty"`
	// Searched counts the files whose contents a search read.
	Searched int `json:"searched,omitempty"`
	// Note says what an empty or partial result means.
	Note string `json:"note,omitempty"`
}

func Revision(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func CleanRelative(p string) (string, error) {
	if p == "" {
		return ".", nil
	}
	if strings.ContainsAny(p, "\\\x00\r\n") || strings.HasPrefix(p, "/") || strings.Contains(p, ":") {
		return "", fmt.Errorf("path must be workflow-relative")
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", fmt.Errorf("parent traversal is not allowed")
		}
		if windowsRefusedPathPart(part) {
			return "", fmt.Errorf("path component is not allowed on Windows")
		}
	}
	return path.Clean(p), nil
}

// Private paths never appear in the public file API. Builder transcripts have
// stricter ownership than the shared workflow and use the chat API instead.
// Coding-agent infrastructure (prompt files, tool directories, and the skills
// beneath them) is managed through the skills and chat surfaces, not as
// ordinary workflow files; the list mirrors managedCodingAgentProjectionWritePaths.
func Private(p string) bool {
	for _, part := range strings.Split(strings.ToLower(p), "/") {
		switch path.Ext(part) {
		case ".pem", ".key", ".p12", ".pfx", ".kdbx":
			return true
		}
		// Hidden workspace folders may hold credentials, tool state, and
		// downloaded packages. Expose workflow-authored source through its
		// ordinary code/, learnings/, and knowledgebase/ paths instead.
		if strings.HasPrefix(part, ".") && part != "." {
			return true
		}
		if part == "builder" || part == "secrets" || part == "keys" || part == "db" || part == "credentials.json" || part == "token.json" || part == "tokens.json" || strings.HasSuffix(part, ".sqlite") || strings.HasSuffix(part, ".sqlite3") || strings.HasSuffix(part, ".db") || strings.HasSuffix(part, "-wal") || strings.HasSuffix(part, "-shm") {
			return true
		}
		if part == "agents.md" || part == "agent.md" || part == "claude.md" || part == "gemini.md" {
			return true
		}
		// Runtime caches and installed packages are not workflow source. Keep
		// them out of discovery and direct reads alike.
		switch part {
		case "__pycache__", "venv", "node_modules", "site-packages":
			return true
		}
	}
	return false
}

// ValidateGlob accepts workflow-relative path globs. ** must occupy a full
// path segment and matches zero or more directories.
func ValidateGlob(pattern string) error {
	if pattern == "" {
		return nil
	}
	if len(pattern) > 256 {
		return fmt.Errorf("glob exceeds 256 bytes")
	}
	clean, err := CleanRelative(pattern)
	if err != nil || clean != pattern || clean == "." {
		return fmt.Errorf("glob must be a clean relative path pattern")
	}
	for _, segment := range strings.Split(pattern, "/") {
		if strings.Contains(segment, "**") && segment != "**" {
			return fmt.Errorf("** must be a complete path segment")
		}
		if segment != "**" {
			if _, err := path.Match(segment, ""); err != nil {
				return fmt.Errorf("invalid glob: %w", err)
			}
		}
	}
	return nil
}

// MatchGlob matches a path relative to the directory selected by a file tool.
// Call ValidateGlob once before walking the directory.
func MatchGlob(pattern, relative string) bool {
	if pattern == "" {
		return true
	}
	parts := strings.Split(pattern, "/")
	pathParts := strings.Split(relative, "/")
	var match func(int, int) bool
	match = func(i, j int) bool {
		if i == len(parts) {
			return j == len(pathParts)
		}
		if parts[i] == "**" {
			return match(i+1, j) || (j < len(pathParts) && match(i, j+1))
		}
		if j >= len(pathParts) {
			return false
		}
		ok, _ := path.Match(parts[i], pathParts[j])
		return ok && match(i+1, j+1)
	}
	return match(0, 0)
}
