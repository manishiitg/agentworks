package handlers

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/parsers"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"
	"github.com/spf13/viper"
)

// getUserID extracts the user ID from the request header
// Returns the default user ID if not provided
func getUserID(c *gin.Context) string {
	userID := c.GetHeader("X-User-ID")
	return utils.SanitizeUserID(userID)
}

// resolveUserPath resolves a requested path with user isolation
func resolveUserPath(c *gin.Context, requestedPath string) (string, error) {
	docsDir := viper.GetString("docs-dir")
	userID := getUserID(c)
	return utils.ResolveUserPath(docsDir, requestedPath, userID)
}

// convertToUserRelativePath converts an absolute path to user-relative for API responses
func convertToUserRelativePath(fullPath string) (string, error) {
	docsDir := viper.GetString("docs-dir")
	return utils.ConvertToUserRelativePath(fullPath, docsDir)
}

// isImageFile checks if the file is an image
func isImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	imageExts := []string{".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".svg", ".ico"}
	for _, imgExt := range imageExts {
		if ext == imgExt {
			return true
		}
	}
	return false
}

// getImageMimeType returns the MIME type for image files
func getImageMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	default:
		return "image/png"
	}
}

// formatImageContent returns base64 encoded image data
func formatImageContent(filename string, content []byte) string {
	mimeType := getImageMimeType(filename)
	base64Data := base64.StdEncoding.EncodeToString(content)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data)
}

// CreateDocument handles POST /api/documents
func CreateDocument(c *gin.Context) {
	var req models.CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid request body",
			Error:   err.Error(),
		})
		return
	}

	// Sanitize input filepath to ensure it's relative
	docsDir := viper.GetString("docs-dir")
	req.FilePath = utils.SanitizeInputPath(req.FilePath, docsDir)

	// Validate filepath
	if err := validateFilepath(req.FilePath); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid filepath",
			Error:   err.Error(),
		})
		return
	}

	// Resolve path with user isolation
	fullPath, err := resolveUserPath(c, req.FilePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve path",
			Error:   err.Error(),
		})
		return
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to create directory",
			Error:   err.Error(),
		})
		return
	}

	// Check if file already exists
	if _, err := os.Stat(fullPath); err == nil {
		c.JSON(http.StatusConflict, models.APIResponse[any]{
			Success: false,
			Message: "Document already exists",
			Error:   "File already exists: " + req.FilePath,
		})
		return
	}

	// Write file
	if err := os.WriteFile(fullPath, []byte(req.Content), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to create document",
			Error:   err.Error(),
		})
		return
	}

	// Parse markdown structure
	structure := parsers.ParseMarkdown(req.Content)

	// Convert full path to relative path for API response
	relativePath, err := utils.GetRelativePath(fullPath, docsDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to convert file path",
			Error:   err.Error(),
		})
		return
	}

	// Create response
	doc := models.Document{
		FilePath: relativePath,
	}

	responseData := models.CreateDocumentResponse{
		Document:  doc,
		Structure: *structure,
	}

	c.JSON(http.StatusCreated, models.APIResponse[models.CreateDocumentResponse]{
		Success: true,
		Message: "Document created successfully",
		Data:    responseData,
	})
}

// getAllDocumentsRecursively recursively reads all files and folders from a directory.
// Follows symbolic links (directories are reported under their symlink path) and uses
// resolved-path cycle detection so the same underlying directory is never walked twice.
func getAllDocumentsRecursively(searchPath, docsDir string, maxDepth int, limit, offset int, includeMetadata ...bool) ([]models.Document, error) {
	metadata := true
	if len(includeMetadata) > 0 {
		metadata = includeMetadata[0]
	}
	var documents []models.Document
	count := 0
	skipped := 0
	visited := map[string]bool{}

	ignoredDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		".next":        true,
		"dist":         true,
		"build":        true,
		"coverage":     true,
		"__pycache__":  true,
		".venv":        true,
		"venv":         true,
	}

	var walk func(path string) error
	walk = func(path string) error {
		// os.Stat follows symlinks, so symlinked directories are recognised as directories.
		info, err := os.Stat(path)
		if err != nil {
			return nil
		}

		name := filepath.Base(path)
		if info.IsDir() && ignoredDirs[name] {
			return nil
		}

		isRoot := path == searchPath

		// Cycle protection: if we've already walked this underlying directory
		// (reached via a different symlink path), skip it entirely.
		if info.IsDir() {
			if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
				if visited[resolved] {
					return nil
				}
				visited[resolved] = true
			}
		}

		if !isRoot {
			relFromSearch, err := filepath.Rel(searchPath, path)
			if err != nil {
				return nil
			}
			currentDepth := 0
			if relFromSearch != "." {
				currentDepth = strings.Count(relFromSearch, string(filepath.Separator))
			}
			if maxDepth >= 0 && currentDepth > maxDepth {
				return nil
			}
		}

		relPathFromDocs, err := filepath.Rel(docsDir, path)
		if err != nil {
			return nil
		}

		// Add entry — the root searchPath is only added when it's a specific subfolder query.
		addEntry := true
		if isRoot && !(info.IsDir() && searchPath != docsDir) {
			addEntry = false
		}
		if addEntry {
			if offset > 0 && skipped < offset {
				skipped++
			} else if info.IsDir() {
				doc := models.Document{
					FilePath: relPathFromDocs,
					Type:     "folder",
				}
				if metadata {
					doc.LastModified = info.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00")
				}
				documents = append(documents, doc)
			} else if limit <= 0 || count < limit {
				doc := models.Document{
					FilePath: relPathFromDocs,
				}
				if metadata {
					doc.IsImage = isImageFile(name)
					doc.Size = info.Size()
					doc.MimeType = detectFileMimeType(name, nil)
					doc.IsBinary = doc.IsImage || !isTextDocumentContent(name, doc.MimeType, nil)
					doc.LastModified = info.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00")
				}
				documents = append(documents, doc)
				count++
			}
		}

		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				return nil
			}
			for _, e := range entries {
				if err := walk(filepath.Join(path, e.Name())); err != nil {
					return err
				}
			}
		}
		return nil
	}

	err := walk(searchPath)
	return documents, err
}

// buildHierarchicalStructure builds a tree structure from flat document list
func buildHierarchicalStructure(documents []models.Document, queryFolder string) []models.Document {
	// Create a map to store folders by their path - using pointers
	folderMap := make(map[string]*models.Document)
	var rootItems []*models.Document // Use pointers for root items too

	// First pass: create all folder nodes and store pointers
	for i := range documents {
		doc := &documents[i]
		if doc.Type == "folder" {
			folderMap[doc.FilePath] = doc
		}
	}

	// Helper to get parent directory ("." maps to "" for root-level items)
	parentDir := func(filePath string) string {
		dir := filepath.Dir(filePath)
		if dir == "." {
			return ""
		}
		return dir
	}

	// Second pass: organize files and folders into hierarchy
	for i := range documents {
		doc := &documents[i]
		docParent := parentDir(doc.FilePath)

		if doc.Type == "folder" {
			// This is a folder - add to root if it's a top-level folder, the queried folder itself, or a direct child of the queried folder
			if docParent == "" || (queryFolder != "" && (doc.FilePath == queryFolder || docParent == queryFolder)) {
				// Add the folder pointer to root items
				rootItems = append(rootItems, folderMap[doc.FilePath])
			}
			// Note: We don't add subfolders to their parents here because
			// we need to process all files first to populate the folders
		} else {
			// This is a file - add to its parent folder
			if docParent == "" || (queryFolder != "" && docParent == queryFolder) {
				// File is in root or in the queried folder - create a copy for root items
				fileCopy := *doc
				rootItems = append(rootItems, &fileCopy)
			} else {
				// File is in a folder - add to parent folder
				if parent, exists := folderMap[docParent]; exists {
					if parent.Children == nil {
						parent.Children = []models.Document{}
					}
					parent.Children = append(parent.Children, *doc)
				}
			}
		}
	}

	// Third pass: add subfolders to their parents (process in reverse order for deep nesting)
	for i := len(documents) - 1; i >= 0; i-- {
		doc := &documents[i]
		docParent := parentDir(doc.FilePath)
		if doc.Type == "folder" && docParent != "" {
			// This is a subfolder - add to its parent
			if parent, exists := folderMap[docParent]; exists {
				if parent.Children == nil {
					parent.Children = []models.Document{}
				}
				// Add a reference to the folder from folderMap
				parent.Children = append(parent.Children, *folderMap[doc.FilePath])
			}
		}
	}

	// Convert root items from pointers to values for return
	var result []models.Document
	for _, item := range rootItems {
		result = append(result, *item)
	}

	// Sort the root items (folders first, then files)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type == result[j].Type {
			return result[i].FilePath < result[j].FilePath
		}
		return result[i].Type == "folder"
	})

	return result
}

// normalizeFolderPath removes trailing slashes from folder path (except for root "/")
func normalizeFolderPath(folder string) string {
	if folder == "" || folder == "/" {
		return folder
	}
	return strings.TrimRight(folder, "/")
}

// ListDocuments handles GET /api/documents
func ListDocuments(c *gin.Context) {
	var req models.ListDocumentsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid query parameters",
			Error:   err.Error(),
		})
		return
	}

	docsDir := viper.GetString("docs-dir")
	userID := getUserID(c)
	workspaceDebugLogf("[USER_ID_DEBUGGING] ListDocuments: X-User-ID=%q, resolved=%q, folder=%q",
		c.GetHeader("X-User-ID"), userID, req.Folder)

	// Normalize folder path by removing trailing slashes
	// This fixes issues where trailing slashes cause comparison failures in buildHierarchicalStructure
	normalizedFolder := normalizeFolderPath(req.Folder)

	// Build search path with user isolation
	var searchPath string
	var err error
	isRootListing := normalizedFolder == "" || normalizedFolder == "."
	if !isRootListing {
		// Resolve path (per-user folders route to _users/{userID}/)
		searchPath, err = resolveUserPath(c, normalizedFolder)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
				Success: false,
				Message: "Failed to resolve path",
				Error:   err.Error(),
			})
			return
		}
	} else {
		searchPath = docsDir
	}

	// Check if the folder exists before attempting to read it
	if _, err := os.Stat(searchPath); os.IsNotExist(err) {
		// Folder doesn't exist - return 200 with empty list and message
		c.JSON(http.StatusOK, models.APIResponse[[]models.Document]{
			Success: true,
			Message: "Folder does not exist",
			Data:    []models.Document{},
			Error:   fmt.Sprintf("Folder not found: %s", normalizedFolder),
		})
		return
	}

	// Parse blocked paths from comma-separated string
	var blockedPaths []string
	if req.BlockedPaths != "" {
		blockedPaths = strings.Split(req.BlockedPaths, ",")
		for i, p := range blockedPaths {
			blockedPaths[i] = strings.TrimSpace(p)
		}
		fmt.Printf("[FOLDER GUARD] List with blocked paths: %v\n", blockedPaths)
	}

	// Use recursive function to get all documents with max depth
	// Force unlimited files (limit=-1) and no offset (offset=0) to ensure full tree structure is returned for the UI
	// Skip metadata (last_modified, is_image) for root listings to reduce response size
	documents, err := getAllDocumentsRecursively(searchPath, docsDir, req.MaxDepth, -1, 0, !isRootListing)
	if err != nil {
		// Check if error is due to directory not existing
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, models.APIResponse[[]models.Document]{
				Success: true,
				Message: "Folder does not exist",
				Data:    []models.Document{},
				Error:   fmt.Sprintf("Folder not found: %s", normalizedFolder),
			})
			return
		}
		// For other errors, return 500
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to read documents directory",
			Error:   err.Error(),
		})
		return
	}

	// For non-root per-user folder listings, rewrite paths to strip _users/{userID}/ prefix
	// e.g. _users/default/Chats/session.json → Chats/session.json
	if !isRootListing && utils.IsPerUserPath(normalizedFolder) {
		for i := range documents {
			if cleanPath, convErr := utils.ConvertToUserRelativePath(
				filepath.Join(docsDir, documents[i].FilePath), docsDir,
			); convErr == nil {
				documents[i].FilePath = cleanPath
			}
		}
	}

	// For root listing, replace per-user folders with contents from _users/{userID}/
	// and filter out the _users/ directory itself
	if isRootListing {
		// Remove per-user folder entries and _users/ entries from root scan
		var sharedDocuments []models.Document
		for _, doc := range documents {
			// Skip _users/ directory and its contents
			if docRef := workspaceref.MustParse(doc.FilePath); docRef.IsUsersRoot() || docRef.HasOwner() {
				continue
			}
			// Skip root-level per-user folders (they'll be replaced with user-scoped ones)
			if utils.IsPerUserPath(doc.FilePath) {
				continue
			}
			sharedDocuments = append(sharedDocuments, doc)
		}

		// Scan per-user folders from _users/{userID}/ and add with clean paths
		for _, prefix := range utils.PerUserPrefixes() {
			userFolderPath, _ := resolveUserPath(c, prefix)
			if _, statErr := os.Stat(userFolderPath); statErr != nil {
				continue // user folder doesn't exist yet
			}
			userDocs, scanErr := getAllDocumentsRecursively(userFolderPath, docsDir, req.MaxDepth, -1, 0, false)
			if scanErr != nil {
				continue
			}
			// Rewrite paths: _users/{userID}/Chats/... → Chats/...
			for i := range userDocs {
				if cleanPath, convErr := utils.ConvertToUserRelativePath(
					filepath.Join(docsDir, userDocs[i].FilePath), docsDir,
				); convErr == nil {
					userDocs[i].FilePath = cleanPath
				}
			}
			sharedDocuments = append(sharedDocuments, userDocs...)
		}
		documents = sharedDocuments
	}

	// Filter out blocked paths before building hierarchy
	if len(blockedPaths) > 0 {
		var filteredDocuments []models.Document
		for _, doc := range documents {
			isBlocked := false
			for _, blocked := range blockedPaths {
				blocked = strings.TrimSuffix(blocked, "/")
				if blocked != "" && (strings.HasPrefix(doc.FilePath, blocked+"/") || doc.FilePath == blocked ||
					strings.HasPrefix(doc.FilePath, blocked)) {
					isBlocked = true
					fmt.Printf("[FOLDER GUARD] Filtered out: %s\n", doc.FilePath)
					break
				}
			}
			if !isBlocked {
				filteredDocuments = append(filteredDocuments, doc)
			}
		}
		documents = filteredDocuments
	}

	// Build hierarchical structure from flat list using normalized folder path
	hierarchicalDocuments := buildHierarchicalStructure(documents, normalizedFolder)

	c.JSON(http.StatusOK, models.APIResponse[[]models.Document]{
		Success: true,
		Message: "Documents retrieved successfully",
		Data:    hierarchicalDocuments,
	})
}

// GlobDocuments handles GET /api/documents/glob
// Discovers files matching a glob pattern (supports ** for recursive matching)
func GlobDocuments(c *gin.Context) {
	var req models.GlobDocumentsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid query parameters",
			Error:   err.Error(),
		})
		return
	}

	docsDir := viper.GetString("docs-dir")

	// Normalize folder path
	normalizedFolder := normalizeFolderPath(req.Folder)

	// Resolve user path
	var searchPath string
	var err error
	if normalizedFolder != "" {
		searchPath, err = resolveUserPath(c, normalizedFolder)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
				Success: false,
				Message: "Failed to resolve folder path",
				Error:   err.Error(),
			})
			return
		}
	} else {
		searchPath = docsDir
	}

	// Check if the folder exists
	if _, err := os.Stat(searchPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, models.APIResponse[[]models.Document]{
			Success: false,
			Message: "Folder not found",
			Error:   fmt.Sprintf("Folder does not exist: %s", normalizedFolder),
			Data:    []models.Document{},
		})
		return
	}

	// Set max depth (default: unlimited if -1)
	maxDepth := req.MaxDepth
	if maxDepth < 0 {
		maxDepth = -1 // Unlimited
	}

	// Parse blocked paths from comma-separated string
	var blockedPaths []string
	if req.BlockedPaths != "" {
		blockedPaths = strings.Split(req.BlockedPaths, ",")
		for i, p := range blockedPaths {
			blockedPaths[i] = strings.TrimSpace(p)
		}
		fmt.Printf("[FOLDER GUARD] Glob with blocked paths: %v\n", blockedPaths)
	}

	// Get all documents recursively
	allDocuments, err := getAllDocumentsRecursively(searchPath, docsDir, maxDepth, -1, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to read documents directory",
			Error:   err.Error(),
		})
		return
	}

	// Filter out blocked paths before matching
	if len(blockedPaths) > 0 {
		var filteredDocuments []models.Document
		for _, doc := range allDocuments {
			isBlocked := false
			for _, blocked := range blockedPaths {
				blocked = strings.TrimSuffix(blocked, "/")
				if blocked != "" && (strings.HasPrefix(doc.FilePath, blocked+"/") || doc.FilePath == blocked ||
					strings.HasPrefix(doc.FilePath, blocked)) {
					isBlocked = true
					fmt.Printf("[FOLDER GUARD] Filtered out: %s\n", doc.FilePath)
					break
				}
			}
			if !isBlocked {
				filteredDocuments = append(filteredDocuments, doc)
			}
		}
		allDocuments = filteredDocuments
	}

	// Match files against glob pattern
	matchedDocuments := matchGlobPattern(allDocuments, req.Pattern, req.IncludeDirs, docsDir)

	c.JSON(http.StatusOK, models.APIResponse[[]models.Document]{
		Success: true,
		Message: fmt.Sprintf("Found %d files matching pattern '%s'", len(matchedDocuments), req.Pattern),
		Data:    matchedDocuments,
	})
}

// matchGlobPattern matches documents against a glob pattern
// Supports standard glob patterns: *, ?, [chars], and ** for recursive matching
func matchGlobPattern(documents []models.Document, pattern string, includeDirs bool, docsDir string) []models.Document {
	var matched []models.Document

	// Check if pattern contains ** (recursive matching)
	hasDoubleStar := strings.Contains(pattern, "**")

	for _, doc := range documents {
		// Skip directories if not included
		if doc.Type == "folder" && !includeDirs {
			continue
		}

		// Get relative path from docs directory for matching
		relPath := doc.FilePath

		// Handle recursive patterns with **
		if hasDoubleStar {
			if matchRecursiveGlob(relPath, pattern) {
				matched = append(matched, doc)
			}
		} else {
			// Use standard filepath.Match for non-recursive patterns
			// For patterns like "*.go", match against filename only
			// For patterns like "docs/*.md", match against full relative path
			matchedPath := relPath
			if !strings.Contains(pattern, "/") && !strings.Contains(pattern, string(filepath.Separator)) {
				// Pattern doesn't contain path separators - match against filename only
				matchedPath = filepath.Base(relPath)
			}

			if isMatched, err := filepath.Match(pattern, matchedPath); err == nil && isMatched {
				matched = append(matched, doc)
			}
		}
	}

	return matched
}

// matchRecursiveGlob matches a path against a glob pattern with ** support
// ** matches zero or more directories
func matchRecursiveGlob(path, pattern string) bool {
	// Convert path separators to forward slashes for consistent matching
	normalizedPath := strings.ReplaceAll(path, string(filepath.Separator), "/")
	normalizedPattern := strings.ReplaceAll(pattern, string(filepath.Separator), "/")

	// If pattern doesn't contain **, use standard matching
	if !strings.Contains(normalizedPattern, "**") {
		matched, _ := filepath.Match(pattern, path)
		return matched
	}

	// Split pattern by ** to handle recursive matching
	// Example: "docs/**/*.md" -> ["docs/", "/*.md"]
	// Example: "**/*.go" -> ["", "/*.go"]
	patternParts := strings.Split(normalizedPattern, "**")
	if len(patternParts) < 2 {
		// Invalid pattern or no ** found
		matched, _ := filepath.Match(pattern, path)
		return matched
	}

	// Start pattern (before first **)
	startPattern := patternParts[0]
	// End pattern (after last **)
	endPattern := patternParts[len(patternParts)-1]

	// Check if path starts with start pattern
	if startPattern != "" {
		if !strings.HasPrefix(normalizedPath, startPattern) {
			return false
		}
		normalizedPath = normalizedPath[len(startPattern):]
	}

	// Check if path ends with end pattern
	if endPattern != "" {
		// For end patterns like "/*.md" or "*.md", we need to match the suffix
		if strings.HasPrefix(endPattern, "/") {
			// Pattern like "/*.md" - match from any directory boundary
			endPattern = endPattern[1:] // Remove leading /
			// Try matching from each directory boundary
			pathParts := strings.Split(normalizedPath, "/")
			for i := 0; i < len(pathParts); i++ {
				suffix := strings.Join(pathParts[i:], "/")
				if matched, _ := filepath.Match(endPattern, suffix); matched {
					return true
				}
			}
			return false
		} else {
			// Pattern like "*.md" - match filename only
			// Extract filename from path
			lastSlash := strings.LastIndex(normalizedPath, "/")
			if lastSlash >= 0 {
				filename := normalizedPath[lastSlash+1:]
				matched, _ := filepath.Match(endPattern, filename)
				return matched
			} else {
				// No directory separator, match entire path
				matched, _ := filepath.Match(endPattern, normalizedPath)
				return matched
			}
		}
	}

	// If no end pattern, pattern matches if start pattern matched
	return true
}

// GetDocument handles GET /api/documents/*filepath
func GetDocument(c *gin.Context) {
	filePathParam := c.Param("filepath")
	docsDir := viper.GetString("docs-dir")

	// Sanitize input path to ensure it's relative
	filePathParam = utils.SanitizeInputPath(filePathParam, docsDir)

	// Resolve path with user isolation
	filePath, err := resolveUserPath(c, filePathParam)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve path",
			Error:   err.Error(),
		})
		return
	}

	// Validate file path for security
	if !utils.IsValidFilePath(filePath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid file path",
			Error:   "File path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Check if file exists
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		// File doesn't exist - return 200 with message indicating file doesn't exist
		c.JSON(http.StatusOK, models.APIResponse[models.Document]{
			Success: true,
			Message: "File does not exist",
			Data:    models.Document{},
			Error:   fmt.Sprintf("File not found: %s", filePathParam),
		})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to get file info",
			Error:   err.Error(),
		})
		return
	}

	// Read file. A chat conversation is assembled from its header and history
	// log; ?tail=N returns only its last N messages (never to be saved back).
	var content []byte
	if chatlog.IsConversationPath(filePath) {
		if tail := c.Query("tail"); tail != "" {
			n := 0
			_, _ = fmt.Sscanf(tail, "%d", &n)
			content, err = chatlog.LoadTail(filePath, n)
		} else {
			content, err = chatlog.Load(filePath)
		}
	} else {
		content, err = os.ReadFile(filePath)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to read document",
			Error:   err.Error(),
		})
		return
	}

	// Return the original user-relative path in the response (not the internal path)
	relativePath := filePathParam

	// Check if file is an image
	var contentStr string
	baseName := filepath.Base(filePath)
	isImage := isImageFile(baseName)
	mimeType := detectFileMimeType(baseName, content)
	isText := isTextDocumentContent(baseName, mimeType, content)
	isBinary := isImage || !isText
	encoding := ""

	if isImage {
		// Image file - return base64 encoded data
		contentStr = formatImageContent(baseName, content)
		encoding = "data-url"
	} else if isText {
		// Text file - include content
		contentStr = string(content)
		encoding = "utf-8"
	} else {
		// Binary file - expose metadata only. Raw bytes are available via
		// download=true or the /raw endpoint.
		contentStr = ""
	}

	doc := models.Document{
		FilePath: relativePath,
		Content:  contentStr, // Include content for read_workspace_file tool
		IsImage:  isImage,
		IsBinary: isBinary,
		Size:     int64(len(content)),
		MimeType: mimeType,
		Encoding: encoding,
	}

	// Opt-in last-modified: only set (and thus serialize, given omitempty) when
	// the caller explicitly asks. Keeps the default single-file read response
	// shape unchanged; used by the toolbar's "unseen builder docs" freshness dot.
	if c.Query("need_last_modified") == "true" {
		doc.LastModified = info.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00")
	}

	// Check if this is a download request (query parameter or Accept header)
	downloadParam := c.Query("download")
	acceptHeader := c.GetHeader("Accept")
	downloadRequested := downloadParam == "true" || acceptHeader == "application/octet-stream"

	// Debug logging
	if isBinary && !isImage {
		log.Printf("[GetDocument] Binary file detected: %s, download param: %s, accept header: %s, downloadRequested: %v",
			baseName, downloadParam, acceptHeader, downloadRequested)
	}

	// For binary files when download is requested, return raw file
	if downloadRequested && isBinary {
		// Return raw binary file
		c.Header("Content-Type", mimeType)
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", baseName))
		c.Data(http.StatusOK, mimeType, content)
		return
	}

	c.JSON(http.StatusOK, models.APIResponse[models.Document]{
		Success: true,
		Message: "Document retrieved successfully",
		Data:    doc,
	})
}

// GetRawDocument handles GET /api/documents/*filepath/raw
// Returns the raw file content as binary data (no JSON wrapping)
func GetRawDocument(c *gin.Context) {
	filePathParam := c.Param("filepath")
	docsDir := viper.GetString("docs-dir")

	// Sanitize input path to ensure it's relative
	filePathParam = utils.SanitizeInputPath(filePathParam, docsDir)

	// Resolve path with user isolation
	filePath, err := resolveUserPath(c, filePathParam)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve path",
			Error:   err.Error(),
		})
		return
	}
	if c.Query("report_media") == "true" {
		marker := "/db/assets/"
		idx := strings.Index(filepath.ToSlash(filePath), marker)
		if idx < 0 {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		assetsRoot := filePath[:idx] + "/db/assets"
		realRoot, rootErr := filepath.EvalSymlinks(assetsRoot)
		realFile, fileErr := filepath.EvalSymlinks(filePath)
		if rootErr != nil || fileErr != nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		// The durable assets root itself cannot redirect outside this workflow.
		if realRoot != filepath.Clean(assetsRoot) || !strings.HasPrefix(realFile, realRoot+string(os.PathSeparator)) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		filePath = realFile
	}

	// Validate file path for security
	if !utils.IsValidFilePath(filePath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid file path",
			Error:   "File path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Check if file exists
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, models.APIResponse[any]{
			Success: false,
			Message: "File not found",
			Error:   fmt.Sprintf("File not found: %s", filePathParam),
		})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to get file info",
			Error:   err.Error(),
		})
		return
	}
	if info.IsDir() {
		c.Status(http.StatusBadRequest)
		return
	}

	if chatlog.IsConversationPath(filePath) {
		content, err := chatlog.Load(filePath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
				Success: false,
				Message: "Failed to read file",
				Error:   err.Error(),
			})
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", filepath.Base(filePath)))
		c.Data(http.StatusOK, "application/json", content)
		return
	}

	file, err := os.Open(filePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to read file",
			Error:   err.Error(),
		})
		return
	}
	defer file.Close()

	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filePath)))
	if contentType == "" {
		buffer := make([]byte, 512)
		read, _ := file.Read(buffer)
		contentType = http.DetectContentType(buffer[:read])
		_, _ = file.Seek(0, io.SeekStart)
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", filepath.Base(filePath)))
	c.Header("Accept-Ranges", "bytes")
	http.ServeContent(c.Writer, c.Request, filepath.Base(filePath), info.ModTime(), file)
}

// UpdateDocument handles PUT /api/documents/*filepath
func UpdateDocument(c *gin.Context) {
	filePathParam := c.Param("filepath")
	var req models.UpdateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid request body",
			Error:   err.Error(),
		})
		return
	}

	docsDir := viper.GetString("docs-dir")

	// Sanitize input path to ensure it's relative
	filePathParam = utils.SanitizeInputPath(filePathParam, docsDir)

	// Resolve path with user isolation
	filePath, err := resolveUserPath(c, filePathParam)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve path",
			Error:   err.Error(),
		})
		return
	}

	// Validate file path for security
	if !utils.IsValidFilePath(filePath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid file path",
			Error:   "File path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Check if file exists and create directory if needed
	fileExists := true
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		fileExists = false
		// Create directory if it doesn't exist
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
				Success: false,
				Message: "Failed to create directory",
				Error:   err.Error(),
			})
			return
		}
	}

	// Write content (create or update). A chat conversation goes to its
	// header and history log, appending when only new messages were added.
	var writeErr error
	if chatlog.IsConversationPath(filePath) {
		writeErr = chatlog.Save(filePath, []byte(req.Content))
		if errors.Is(writeErr, chatlog.ErrPartialRecord) {
			c.JSON(http.StatusConflict, models.APIResponse[any]{
				Success: false,
				Message: "Refusing to save a partial conversation",
				Error:   writeErr.Error(),
			})
			return
		}
	} else {
		writeErr = os.WriteFile(filePath, []byte(req.Content), 0644)
	}
	if err := writeErr; err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to write document",
			Error:   err.Error(),
		})
		return
	}

	// Return user-relative path in the response
	relativePath := filePathParam

	doc := models.Document{
		FilePath: relativePath,
	}

	// Determine appropriate message based on whether file was created or updated
	message := "Document updated successfully"
	if !fileExists {
		message = "Document created successfully"
	}

	c.JSON(http.StatusOK, models.APIResponse[models.Document]{
		Success: true,
		Message: message,
		Data:    doc,
	})
}

// DeleteDocument handles DELETE /api/documents/*filepath
func DeleteDocument(c *gin.Context) {
	filePathParam := c.Param("filepath")
	confirm := c.Query("confirm")

	if confirm != "true" {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Deletion requires confirmation",
			Error:   "Add ?confirm=true to confirm deletion",
		})
		return
	}

	docsDir := viper.GetString("docs-dir")

	// Sanitize input path to ensure it's relative
	filePathParam = utils.SanitizeInputPath(filePathParam, docsDir)

	// Resolve path with user isolation
	filePath, err := resolveUserPath(c, filePathParam)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve path",
			Error:   err.Error(),
		})
		return
	}

	// Validate file path for security
	if !utils.IsValidFilePath(filePath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid file path",
			Error:   "File path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Check if path exists
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, models.APIResponse[any]{
			Success: false,
			Message: "Document not found",
			Error:   "Document not found: " + filePathParam,
		})
		return
	}

	// Use RemoveAll for directories so non-empty dirs are deleted cleanly,
	// and plain Remove for files (faster, no risk of accidentally nuking a tree).
	if info.IsDir() {
		err = os.RemoveAll(filePath)
	} else if chatlog.IsConversationPath(filePath) {
		err = chatlog.Remove(filePath)
	} else {
		err = os.Remove(filePath)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to delete document",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse[any]{
		Success: true,
		Message: "Document deleted successfully",
	})
}

// MoveDocument handles POST /api/documents/*filepath/move
func MoveDocument(c *gin.Context) {
	filePathParam := c.Param("filepath")
	var req models.MoveDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid request body",
			Error:   err.Error(),
		})
		return
	}

	docsDir := viper.GetString("docs-dir")

	// Sanitize input paths to ensure they're relative
	sourcePath := utils.SanitizeInputPath(filePathParam, docsDir)
	destinationPath := utils.SanitizeInputPath(req.DestinationPath, docsDir)

	// Validate source filepath (use lenient validation for move - supports all file types)
	if err := validateFilePathLenient(sourcePath); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid source filepath",
			Error:   err.Error(),
		})
		return
	}

	// Validate destination filepath (use lenient validation for move - supports all file types)
	if err := validateFilePathLenient(destinationPath); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid destination filepath",
			Error:   err.Error(),
		})
		return
	}

	// Check if source and destination are the same
	if sourcePath == destinationPath {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Source and destination paths are the same",
			Error:   "Cannot move file to the same location",
		})
		return
	}

	// Resolve user paths
	sourceFilePath, err := resolveUserPath(c, sourcePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve source path",
			Error:   err.Error(),
		})
		return
	}

	destinationFilePath, err := resolveUserPath(c, destinationPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve destination path",
			Error:   err.Error(),
		})
		return
	}

	// Validate file paths for security
	if !utils.IsValidFilePath(sourceFilePath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid source file path",
			Error:   "Source file path contains invalid characters or attempts directory traversal",
		})
		return
	}

	if !utils.IsValidFilePath(destinationFilePath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid destination file path",
			Error:   "Destination file path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Check if source file exists
	if _, err := os.Stat(sourceFilePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, models.APIResponse[any]{
			Success: false,
			Message: "Source file not found",
			Error:   "The file to move does not exist",
		})
		return
	}

	// Check if destination file already exists
	// Note: We check this before creating the directory, so if the directory
	// doesn't exist, os.Stat will fail and we'll continue to create it.
	if _, err := os.Stat(destinationFilePath); err == nil {
		c.JSON(http.StatusConflict, models.APIResponse[any]{
			Success: false,
			Message: "Destination file already exists",
			Error:   "File already exists: " + req.DestinationPath,
		})
		return
	}

	// Create destination directory if it doesn't exist
	destDir := filepath.Dir(destinationFilePath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to create destination directory",
			Error:   err.Error(),
		})
		return
	}

	// Move file (a chat conversation's history log moves with it)
	moveFile := os.Rename
	if chatlog.IsConversationPath(sourceFilePath) {
		moveFile = chatlog.Move
	}
	if err := moveFile(sourceFilePath, destinationFilePath); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to move document",
			Error:   err.Error(),
		})
		return
	}

	// Create response data
	relativePath, err := utils.GetRelativePath(destinationFilePath, docsDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to get relative path",
			Error:   err.Error(),
		})
		return
	}

	responseData := models.Document{
		FilePath: relativePath,
	}

	c.JSON(http.StatusOK, models.APIResponse[models.Document]{
		Success: true,
		Message: "Document moved successfully",
		Data:    responseData,
	})
}

// GetFileVersionHistory handles GET /api/documents/*filepath/versions
func GetFileVersionHistory(c *gin.Context) {
	filePathParam := c.Param("filepath")
	var req models.FileVersionHistoryRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid query parameters",
			Error:   err.Error(),
		})
		return
	}

	docsDir := viper.GetString("docs-dir")

	// Resolve user path
	filePath, err := resolveUserPath(c, filePathParam)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve file path",
			Error:   err.Error(),
		})
		return
	}

	// Validate file path for security
	if !utils.IsValidFilePath(filePath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid file path",
			Error:   "File path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, models.APIResponse[any]{
			Success: false,
			Message: "Document not found",
			Error:   "Document not found: " + filePathParam,
		})
		return
	}

	// Get version history
	versionManager := utils.NewGitVersionManager(docsDir)
	versions, err := versionManager.GetFileVersionHistory(filePath, req.Limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to get version history",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse[any]{
		Success: true,
		Message: "Version history retrieved successfully",
		Data:    versions,
	})
}

// RestoreFileVersion handles POST /api/documents/*filepath/restore
func RestoreFileVersion(c *gin.Context) {
	filePathParam := c.Param("filepath")
	var req models.RestoreVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid request body",
			Error:   err.Error(),
		})
		return
	}

	docsDir := viper.GetString("docs-dir")

	// Resolve user path
	filePath, err := resolveUserPath(c, filePathParam)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve file path",
			Error:   err.Error(),
		})
		return
	}

	// Validate file path for security
	if !utils.IsValidFilePath(filePath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid file path",
			Error:   "File path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Restore the version
	versionManager := utils.NewGitVersionManager(docsDir)
	if err := versionManager.RestoreFileVersion(filePath, req.CommitHash); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to restore version",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse[any]{
		Success: true,
		Message: "File version restored successfully",
		Data: map[string]interface{}{
			"commit_hash": req.CommitHash,
			"filepath":    filePathParam,
		},
	})
}

// CreateFolder handles POST /api/folders
func CreateFolder(c *gin.Context) {
	var req models.CreateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid request body",
			Error:   err.Error(),
		})
		return
	}

	// Sanitize input folder path to ensure it's relative
	docsDir := viper.GetString("docs-dir")
	req.FolderPath = utils.SanitizeInputPath(req.FolderPath, docsDir)

	// Validate folder path
	if err := validateFolderPath(req.FolderPath); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid folder path",
			Error:   err.Error(),
		})
		return
	}

	// Resolve path with user isolation
	folderPath, err := resolveUserPath(c, req.FolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve path",
			Error:   err.Error(),
		})
		return
	}

	// Validate folder path for security
	if !utils.IsValidFilePath(folderPath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid folder path",
			Error:   "Folder path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Check if folder already exists or if a file exists at that path
	if info, err := os.Stat(folderPath); err == nil {
		if info.IsDir() {
			c.JSON(http.StatusConflict, models.APIResponse[any]{
				Success: false,
				Message: "Folder already exists",
				Error:   "Folder already exists: " + req.FolderPath,
			})
		} else {
			c.JSON(http.StatusConflict, models.APIResponse[any]{
				Success: false,
				Message: "File exists at folder path",
				Error:   "A file already exists at this path. Cannot create folder: " + req.FolderPath,
			})
		}
		return
	}

	// Create the folder
	if err := os.MkdirAll(folderPath, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to create folder",
			Error:   err.Error(),
		})
		return
	}

	response := models.CreateFolderResponse{
		FolderPath: req.FolderPath,
		Created:    true,
	}

	c.JSON(http.StatusCreated, models.APIResponse[any]{
		Success: true,
		Message: "Folder created successfully",
		Data:    response,
	})
}

// CopyFolder handles POST /api/folders/copy
func CopyFolder(c *gin.Context) {
	var req models.CopyFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid request body",
			Error:   err.Error(),
		})
		return
	}

	docsDir := viper.GetString("docs-dir")

	// Sanitize input paths to ensure they're relative
	req.SourcePath = utils.SanitizeInputPath(req.SourcePath, docsDir)
	req.DestinationPath = utils.SanitizeInputPath(req.DestinationPath, docsDir)

	// Validate folder paths
	if err := validateFolderPath(req.SourcePath); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid source folder path",
			Error:   err.Error(),
		})
		return
	}

	if err := validateFolderPath(req.DestinationPath); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid destination folder path",
			Error:   err.Error(),
		})
		return
	}

	// Resolve user paths
	sourcePath, err := resolveUserPath(c, req.SourcePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve source path",
			Error:   err.Error(),
		})
		return
	}

	destinationPath, err := resolveUserPath(c, req.DestinationPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve destination path",
			Error:   err.Error(),
		})
		return
	}

	// Validate paths for security
	if !utils.IsValidFilePath(sourcePath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid source folder path",
			Error:   "Source path contains invalid characters or attempts directory traversal",
		})
		return
	}

	if !utils.IsValidFilePath(destinationPath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid destination folder path",
			Error:   "Destination path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Check if source folder exists
	sourceInfo, err := os.Stat(sourcePath)
	if os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, models.APIResponse[any]{
			Success: false,
			Message: "Source folder not found",
			Error:   "Source folder does not exist: " + req.SourcePath,
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to check source folder",
			Error:   err.Error(),
		})
		return
	}

	// Check if source is actually a directory
	if !sourceInfo.IsDir() {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Source path is not a folder",
			Error:   "Source path is not a directory: " + req.SourcePath,
		})
		return
	}

	// Check if destination already exists
	if _, err := os.Stat(destinationPath); err == nil {
		c.JSON(http.StatusConflict, models.APIResponse[any]{
			Success: false,
			Message: "Destination folder already exists",
			Error:   "Destination folder already exists: " + req.DestinationPath,
		})
		return
	}

	// Counters for response
	var filesCopied int
	var dirsCreated int

	// Use filepath.Walk to recursively copy all files and directories
	err = filepath.Walk(sourcePath, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip .git directory entirely
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}

		// Calculate relative path from source
		relPath, err := filepath.Rel(sourcePath, srcPath)
		if err != nil {
			return err
		}

		// Build destination path
		dstPath := filepath.Join(destinationPath, relPath)

		if info.IsDir() {
			// Create directory
			if err := os.MkdirAll(dstPath, info.Mode()); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dstPath, err)
			}
			dirsCreated++
		} else {
			// Create parent directory if it doesn't exist
			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
				return fmt.Errorf("failed to create parent directory for %s: %w", dstPath, err)
			}

			// Copy file
			srcFile, err := os.Open(srcPath)
			if err != nil {
				return fmt.Errorf("failed to open source file %s: %w", srcPath, err)
			}
			defer srcFile.Close()

			dstFile, err := os.Create(dstPath)
			if err != nil {
				return fmt.Errorf("failed to create destination file %s: %w", dstPath, err)
			}
			defer dstFile.Close()

			// Copy file contents
			_, err = io.Copy(dstFile, srcFile)
			if err != nil {
				return fmt.Errorf("failed to copy file from %s to %s: %w", srcPath, dstPath, err)
			}

			// Preserve file permissions
			if err := os.Chmod(dstPath, info.Mode()); err != nil {
				return fmt.Errorf("failed to set permissions for %s: %w", dstPath, err)
			}

			filesCopied++
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to copy folder",
			Error:   err.Error(),
		})
		return
	}

	response := models.CopyFolderResponse{
		SourcePath:      req.SourcePath,
		DestinationPath: req.DestinationPath,
		FilesCopied:     filesCopied,
		DirsCreated:     dirsCreated,
	}

	c.JSON(http.StatusOK, models.APIResponse[any]{
		Success: true,
		Message: fmt.Sprintf("Folder copied successfully: %d files, %d directories", filesCopied, dirsCreated),
		Data:    response,
	})
}

// DeleteFolder handles DELETE /api/folders/*folderpath
func DeleteFolder(c *gin.Context) {
	// Gin's wildcard route capture always includes the leading slash
	// ("/Chats/SparkQuill/..." for a request to /api/folders/Chats/...) —
	// HandleDocumentRequest strips this same leading slash from its own
	// *filepath wildcard param before resolving a path. Without it here,
	// SanitizeInputPath sees an absolute-looking path, IsPerUserPath's
	// prefix match ("Chats", not "/Chats") never fires, and resolution
	// silently skips the _users/<id>/ prefix — landing on a path that was
	// never going to exist and reporting "Folder not found" for a folder
	// that is actually there.
	folderPathParam := strings.TrimPrefix(c.Param("folderpath"), "/")
	confirm := c.Query("confirm") == "true"

	// Check if this is a request to delete all files in folder
	if strings.HasSuffix(c.Request.URL.Path, "/files") {
		// Remove "/files" from the folderpath
		folderPathParam = strings.TrimSuffix(folderPathParam, "/files")
		DeleteAllFilesInFolder(c, folderPathParam, confirm)
		return
	}

	if !confirm {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Folder deletion requires confirmation",
			Error:   "Add ?confirm=true to confirm folder deletion",
		})
		return
	}

	docsDir := viper.GetString("docs-dir")

	// Resolve user path
	folderPath, err := resolveUserPath(c, folderPathParam)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve folder path",
			Error:   err.Error(),
		})
		return
	}

	// Validate folder path for security
	if !utils.IsValidFilePath(folderPath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid folder path",
			Error:   "Folder path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Check if folder exists
	if _, err := os.Stat(folderPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, models.APIResponse[any]{
			Success: false,
			Message: "Folder not found",
			Error:   "Folder not found: " + folderPathParam,
		})
		return
	}

	// Check if it's actually a directory
	if info, err := os.Stat(folderPath); err == nil && !info.IsDir() {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Path is not a folder",
			Error:   "Path is not a directory: " + folderPathParam,
		})
		return
	}

	// Remove the folder and all its contents
	if err := os.RemoveAll(folderPath); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to delete folder",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse[any]{
		Success: true,
		Message: "Folder deleted successfully",
		Data: map[string]interface{}{
			"folder_path": folderPathParam,
		},
	})
}

// DeleteAllFilesInFolder handles DELETE /api/folders/*folderpath/files
func DeleteAllFilesInFolder(c *gin.Context, folderPathParam string, confirm bool) {
	if !confirm {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "File deletion requires confirmation",
			Error:   "Add ?confirm=true to confirm deletion of all files in folder",
		})
		return
	}

	docsDir := viper.GetString("docs-dir")

	// Resolve user path
	folderPath, err := resolveUserPath(c, folderPathParam)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve folder path",
			Error:   err.Error(),
		})
		return
	}

	// Validate folder path for security
	if !utils.IsValidFilePath(folderPath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid folder path",
			Error:   "Folder path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Check if folder exists
	if _, err := os.Stat(folderPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, models.APIResponse[any]{
			Success: false,
			Message: "Folder not found",
			Error:   "Folder not found: " + folderPathParam,
		})
		return
	}

	// Check if it's actually a directory
	if info, err := os.Stat(folderPath); err == nil && !info.IsDir() {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Path is not a folder",
			Error:   "Path is not a directory: " + folderPathParam,
		})
		return
	}

	// Count and collect all files and folders to be deleted
	var itemsToDelete []string
	var deletedCount int

	err = filepath.Walk(folderPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the root folder itself
		if path == folderPath {
			return nil
		}

		// Get relative path for processing
		relPath, err := filepath.Rel(docsDir, path)
		if err != nil {
			return err
		}
		itemsToDelete = append(itemsToDelete, relPath)

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to scan folder contents",
			Error:   err.Error(),
		})
		return
	}

	// Delete all files and folders
	for _, itemPath := range itemsToDelete {
		fullPath := filepath.Join(docsDir, itemPath)

		// Check if it's a file or directory
		info, err := os.Stat(fullPath)
		if err != nil {
			fmt.Printf("Warning: Could not stat item %s: %v\n", itemPath, err)
			continue
		}

		// Delete the item (file or directory)
		if info.IsDir() {
			if err := os.RemoveAll(fullPath); err != nil {
				fmt.Printf("Warning: Could not delete directory %s: %v\n", itemPath, err)
				continue
			}
		} else {
			if err := os.Remove(fullPath); err != nil {
				fmt.Printf("Warning: Could not delete file %s: %v\n", itemPath, err)
				continue
			}

		}

		deletedCount++
	}

	c.JSON(http.StatusOK, models.APIResponse[any]{
		Success: true,
		Message: fmt.Sprintf("Successfully deleted %d items (files and folders) from folder", deletedCount),
		Data: map[string]interface{}{
			"folder_path":   folderPathParam,
			"items_deleted": deletedCount,
			"total_found":   len(itemsToDelete),
		},
	})
}

// UploadFile handles POST /api/upload
func UploadFile(c *gin.Context) {
	var req models.FileUploadRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid request parameters",
			Error:   err.Error(),
		})
		return
	}

	// Get the uploaded file
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "No file uploaded",
			Error:   "Please provide a file to upload",
		})
		return
	}
	defer file.Close()

	// Validate file size (max 10MB)
	const maxFileSize = 10 * 1024 * 1024 // 10MB
	if header.Size > maxFileSize {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "File too large",
			Error:   fmt.Sprintf("File size exceeds maximum allowed size of %d bytes", maxFileSize),
		})
		return
	}

	// Validate file type - only allow text-based files and common documents/images
	if !isAllowedFile(header.Filename, header.Header.Get("Content-Type")) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid file type",
			Error:   "File type not allowed. Executables and system files are blocked for security.",
		})
		return
	}

	// Validate file name
	if header.Filename == "" {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid file name",
			Error:   "File name cannot be empty",
		})
		return
	}

	// Sanitize file name
	fileName := sanitizeFilename(header.Filename)
	if fileName == "" {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid file name",
			Error:   "File name contains only invalid characters",
		})
		return
	}

	docsDir := viper.GetString("docs-dir")

	// Resolve user path
	folderPath, err := resolveUserPath(c, req.FolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to resolve folder path",
			Error:   err.Error(),
		})
		return
	}

	// Create folder path
	if err := os.MkdirAll(folderPath, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to create folder",
			Error:   err.Error(),
		})
		return
	}

	// Validate folder path for security
	if !utils.IsValidFilePath(folderPath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid folder path",
			Error:   "Folder path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Create full file path
	fullFilePath := filepath.Join(folderPath, fileName)

	// Validate full file path for security
	if !utils.IsValidFilePath(fullFilePath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid file path",
			Error:   "File path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// Create the file
	dst, err := os.Create(fullFilePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to create file",
			Error:   err.Error(),
		})
		return
	}
	defer dst.Close()

	// Copy file content
	fileSize, err := io.Copy(dst, file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to save file",
			Error:   err.Error(),
		})
		return
	}

	// File uploaded successfully

	// Get content type
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Convert full path to user-relative path for API response
	relativePath, err := convertToUserRelativePath(fullFilePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to convert file path",
			Error:   err.Error(),
		})
		return
	}

	// Get folder path relative to user
	folderRelPath := filepath.Dir(relativePath)

	// Native terminal attachments need an unambiguous path even when the CLI
	// runs in a project subfolder rather than the workspace root.
	absolutePath, err := filepath.Abs(fullFilePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to resolve uploaded file path", Error: err.Error()})
		return
	}

	// Prepare response
	response := models.FileUploadResponse{
		AbsolutePath: absolutePath,
		FilePath:     relativePath,
		FileName:     fileName,
		FileSize:     fileSize,
		ContentType:  contentType,
		Folder:       folderRelPath,
	}

	c.JSON(http.StatusOK, models.APIResponse[any]{
		Success: true,
		Message: "File uploaded successfully",
		Data:    response,
	})
}

// sanitizeFilename keeps the original casing/readability while replacing unsafe path characters.
func sanitizeFilename(title string) string {
	filename := strings.TrimSpace(title)

	// Replace path/invalid filesystem characters with '-'
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"*", "-",
		"?", "-",
		"\"", "-",
		"<", "-",
		">", "-",
		"|", "-",
	)
	filename = replacer.Replace(filename)

	// Remove ASCII control characters
	filename = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, filename)

	// Collapse repeated dashes introduced by sanitization
	for strings.Contains(filename, "--") {
		filename = strings.ReplaceAll(filename, "--", "-")
	}

	// Keep valid dotfiles such as .env, but remove trailing dots and unsafe
	// surrounding spaces/hyphens. The special names . and .. still collapse.
	filename = strings.Trim(filename, " -")
	filename = strings.TrimRight(filename, ".")

	// Avoid relative path special names
	if filename == "" || filename == "." || filename == ".." {
		filename = "untitled"
	}

	return filename
}

// HandleDocumentRequest routes document requests to appropriate handlers based on method and path
func HandleDocumentRequest(c *gin.Context) {
	filePathParam := c.Param("filepath")
	method := c.Request.Method
	path := c.Request.URL.Path

	// Remove leading slash from filepath
	filePathParam = strings.TrimPrefix(filePathParam, "/")

	// Route based on HTTP method and path
	switch method {
	case "GET":
		if strings.HasSuffix(path, "/raw") {
			// Remove /raw from filepath for raw content serving
			filePathParam = strings.TrimSuffix(filePathParam, "/raw")
			c.Params = []gin.Param{{Key: "filepath", Value: filePathParam}}
			GetRawDocument(c)
		} else {
			// Basic document retrieval
			c.Params = []gin.Param{{Key: "filepath", Value: filePathParam}}
			GetDocument(c)
		}
	case "PUT":
		// Basic document update
		c.Params = []gin.Param{{Key: "filepath", Value: filePathParam}}
		UpdateDocument(c)
	case "PATCH":
		if strings.HasSuffix(path, "/diff") {
			// Remove /diff from filepath for diff patch operations
			filePathParam = strings.TrimSuffix(filePathParam, "/diff")
			c.Params = []gin.Param{{Key: "filepath", Value: filePathParam}}
			DiffPatchDocument(c)
		} else {
			c.JSON(http.StatusMethodNotAllowed, models.APIResponse[any]{
				Success: false,
				Message: "Method not allowed",
				Error:   "PATCH method only supports /diff endpoint",
			})
		}
	case "POST":
		if strings.HasSuffix(path, "/move") {
			// Remove /move from filepath
			filePathParam = strings.TrimSuffix(filePathParam, "/move")
			c.Params = []gin.Param{{Key: "filepath", Value: filePathParam}}
			MoveDocument(c)
		} else if strings.HasSuffix(path, "/append-history") {
			filePathParam = strings.TrimSuffix(filePathParam, "/append-history")
			c.Params = []gin.Param{{Key: "filepath", Value: filePathParam}}
			AppendConversationHistory(c)
		} else {
			c.JSON(http.StatusMethodNotAllowed, models.APIResponse[any]{
				Success: false,
				Message: "Method not allowed",
				Error:   "POST method not supported for this endpoint",
			})
		}
	case "DELETE":
		// Basic document deletion
		c.Params = []gin.Param{{Key: "filepath", Value: filePathParam}}
		DeleteDocument(c)
	default:
		c.JSON(http.StatusMethodNotAllowed, models.APIResponse[any]{
			Success: false,
			Message: "Method not allowed",
			Error:   "Unsupported HTTP method: " + method,
		})
	}
}

// validateFilepath validates the filepath for security and format
func validateFilepath(filepath string) error {
	// Check if filepath is empty
	if filepath == "" {
		return fmt.Errorf("filepath cannot be empty")
	}

	// Check for directory traversal attacks
	if strings.Contains(filepath, "..") || strings.HasPrefix(filepath, "/") {
		return fmt.Errorf("filepath contains invalid characters or path traversal")
	}

	// Check if it's a markdown file
	if !strings.HasSuffix(strings.ToLower(filepath), ".md") {
		return fmt.Errorf("filepath must end with .md extension")
	}

	// Check for invalid characters
	invalidChars := []string{"<", ">", ":", "\"", "|", "?", "*"}
	for _, char := range invalidChars {
		if strings.Contains(filepath, char) {
			return fmt.Errorf("filepath contains invalid character: %s", char)
		}
	}

	return nil
}

// validateFilePathLenient validates filepath for move operations - supports all file types, not just .md
func validateFilePathLenient(filepath string) error {
	// Check if filepath is empty
	if filepath == "" {
		return fmt.Errorf("filepath cannot be empty")
	}

	// Check for directory traversal attacks
	if strings.Contains(filepath, "..") || strings.HasPrefix(filepath, "/") {
		return fmt.Errorf("filepath contains invalid characters or path traversal")
	}

	// Check for invalid characters
	invalidChars := []string{"<", ">", ":", "\"", "|", "?", "*"}
	for _, char := range invalidChars {
		if strings.Contains(filepath, char) {
			return fmt.Errorf("filepath contains invalid character: %s", char)
		}
	}

	return nil
}

// validateFolderPath validates the folder path for security and format
func validateFolderPath(folderPath string) error {
	// Check if folder path is empty
	if folderPath == "" {
		return fmt.Errorf("folder path cannot be empty")
	}

	// Check for directory traversal attacks
	if strings.Contains(folderPath, "..") || strings.HasPrefix(folderPath, "/") {
		return fmt.Errorf("folder path contains invalid characters or path traversal")
	}

	// Check for invalid characters
	invalidChars := []string{"<", ">", ":", "\"", "|", "?", "*"}
	for _, char := range invalidChars {
		if strings.Contains(folderPath, char) {
			return fmt.Errorf("folder path contains invalid character: %s", char)
		}
	}

	// Check for reserved names
	reservedNames := []string{"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}
	upperFolderPath := strings.ToUpper(folderPath)
	for _, reserved := range reservedNames {
		if upperFolderPath == reserved || strings.HasPrefix(upperFolderPath, reserved+".") {
			return fmt.Errorf("folder path uses reserved name: %s", reserved)
		}
	}

	return nil
}

// isAllowedFile checks if a file is allowed to be uploaded (allows almost everything except executables)
func isAllowedFile(filename, contentType string) bool {
	// Get file extension
	ext := strings.ToLower(filepath.Ext(filename))
	if ext != "" {
		ext = ext[1:] // Remove the dot
	}

	// Blocked extensions (executables, system files)
	blockedExtensions := map[string]bool{
		"exe": true, "dll": true, "so": true, "dylib": true, "bin": true,
		"msi": true, "dmg": true, "iso": true, "jar": true, "bat": true,
		"cmd": true, "com": true, "scr": true,
	}

	if blockedExtensions[ext] {
		return false
	}

	return true
}

// isTextBasedFile checks if a file is text-based based on extension and MIME type
func isTextBasedFile(filename, contentType string) bool {
	// Get file extension
	ext := strings.ToLower(filepath.Ext(filename))
	if ext != "" {
		ext = ext[1:] // Remove the dot
	}

	// Allowed text-based file extensions
	allowedExtensions := map[string]bool{
		"txt":        true,
		"md":         true,
		"markdown":   true,
		"json":       true,
		"jsonl":      true,
		"ndjson":     true,
		"csv":        true,
		"yaml":       true,
		"yml":        true,
		"xml":        true,
		"html":       true,
		"htm":        true,
		"css":        true,
		"js":         true,
		"jsx":        true,
		"ts":         true,
		"tsx":        true,
		"py":         true,
		"go":         true,
		"java":       true,
		"cpp":        true,
		"c":          true,
		"h":          true,
		"hpp":        true,
		"php":        true,
		"rb":         true,
		"sh":         true,
		"bash":       true,
		"zsh":        true,
		"fish":       true,
		"sql":        true,
		"log":        true,
		"conf":       true,
		"config":     true,
		"ini":        true,
		"toml":       true,
		"env":        true,
		"gitignore":  true,
		"dockerfile": true,
		"makefile":   true,
		"cmake":      true,
		"gradle":     true,
		"maven":      true,
		"pom":        true,
		"sbt":        true,
		"scala":      true,
		"kt":         true,
		"swift":      true,
		"rs":         true,
		"dart":       true,
		"r":          true,
		"m":          true,
		"pl":         true,
		"lua":        true,
		"vim":        true,
		"emacs":      true,
		"tex":        true,
		"latex":      true,
		"rst":        true,
		"adoc":       true,
		"asciidoc":   true,
		"org":        true,
		"wiki":       true,
		"diff":       true,
		"patch":      true,
		"svg":        true,  // SVG is text-based
		"pdf":        false, // PDF is binary
		"doc":        false, // Word docs are binary
		"docx":       false,
		"xls":        false, // Excel files are binary
		"xlsx":       false,
		"ppt":        false, // PowerPoint files are binary
		"pptx":       false,
		"zip":        false, // Archives are binary
		"rar":        false,
		"7z":         false,
		"tar":        false,
		"gz":         false,
		"bz2":        false,
		"xz":         false,
		"jpg":        false, // Images are binary
		"jpeg":       false,
		"png":        false,
		"gif":        false,
		"bmp":        false,
		"tiff":       false,
		"webp":       false,
		"ico":        false,
		"mp4":        false, // Videos are binary
		"avi":        false,
		"mov":        false,
		"wmv":        false,
		"flv":        false,
		"webm":       false,
		"mp3":        false, // Audio files are binary
		"wav":        false,
		"flac":       false,
		"aac":        false,
		"ogg":        false,
		"exe":        false, // Executables are binary
		"dll":        false,
		"so":         false,
		"dylib":      false,
		"bin":        false,
		"app":        false,
		"deb":        false,
		"rpm":        false,
		"msi":        false,
		"dmg":        false,
		"iso":        false,
	}

	// Check extension first
	if allowed, exists := allowedExtensions[ext]; exists {
		return allowed
	}

	// If extension not found, check MIME type
	allowedMimeTypes := map[string]bool{
		"text/plain":                true,
		"text/markdown":             true,
		"text/html":                 true,
		"text/css":                  true,
		"text/javascript":           true,
		"text/x-javascript":         true,
		"text/typescript":           true,
		"application/json":          true,
		"application/xml":           true,
		"text/xml":                  true,
		"text/csv":                  true,
		"application/csv":           true,
		"text/yaml":                 true,
		"application/x-yaml":        true,
		"text/x-yaml":               true,
		"application/x-python-code": true,
		"text/x-python":             true,
		"text/x-go":                 true,
		"text/x-java":               true,
		"text/x-c":                  true,
		"text/x-c++":                true,
		"text/x-php":                true,
		"text/x-ruby":               true,
		"text/x-shellscript":        true,
		"text/x-sql":                true,
		"text/x-log":                true,
		"text/x-ini":                true,
		"text/x-toml":               true,
		"text/x-dockerfile":         true,
		"text/x-makefile":           true,
		"text/x-cmake":              true,
		"text/x-gradle":             true,
		"text/x-maven":              true,
		"text/x-sbt":                true,
		"text/x-scala":              true,
		"text/x-kotlin":             true,
		"text/x-swift":              true,
		"text/x-rust":               true,
		"text/x-dart":               true,
		"text/x-r":                  true,
		"text/x-matlab":             true,
		"text/x-perl":               true,
		"text/x-lua":                true,
		"text/x-vim":                true,
		"text/x-emacs":              true,
		"text/x-tex":                true,
		"text/x-latex":              true,
		"text/x-rst":                true,
		"text/x-adoc":               true,
		"text/x-asciidoc":           true,
		"text/x-org":                true,
	} // Check MIME type
	if allowed, exists := allowedMimeTypes[contentType]; exists {
		return allowed
	}

	// If neither extension nor MIME type is recognized, default to false (reject)
	return false
}

func detectFileMimeType(filename string, content []byte) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".json", ".jsonl", ".ndjson":
		return "application/json"
	case ".md", ".markdown":
		return "text/markdown"
	case ".txt", ".log":
		return "text/plain"
	case ".csv":
		return "text/csv"
	case ".yaml", ".yml":
		return "text/yaml"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js", ".jsx":
		return "text/javascript"
	case ".ts", ".tsx":
		return "text/typescript"
	}

	if len(content) > 0 {
		sampleLen := len(content)
		if sampleLen > 512 {
			sampleLen = 512
		}
		if detected := http.DetectContentType(content[:sampleLen]); detected != "" {
			return detected
		}
	}

	return "application/octet-stream"
}

func isKnownBinaryFile(filename string) bool {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz",
		".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tiff", ".webp", ".ico",
		".mp4", ".avi", ".mov", ".wmv", ".flv", ".webm",
		".mp3", ".wav", ".flac", ".aac", ".ogg",
		".exe", ".dll", ".so", ".dylib", ".bin", ".app", ".deb", ".rpm", ".msi", ".dmg", ".iso":
		return true
	default:
		return false
	}
}

func isLikelyTextContent(content []byte) bool {
	if len(content) == 0 {
		return true
	}
	for _, b := range content {
		if b == 0 {
			return false
		}
	}
	return utf8.Valid(content)
}

func isTextDocumentContent(filename, contentType string, content []byte) bool {
	if isKnownBinaryFile(filename) {
		return false
	}
	if isTextBasedFile(filename, contentType) {
		return isLikelyTextContent(content)
	}
	return isLikelyTextContent(content)
}
