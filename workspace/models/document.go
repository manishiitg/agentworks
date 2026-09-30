package models

import "time"

// Document represents a markdown document
type Document struct {
	FilePath     string     `json:"filepath"`
	Content      string     `json:"content,omitempty"`       // Omitted in list endpoints, populated for single file reads
	Type         string     `json:"type,omitempty"`          // "file" or "folder"
	Children     []Document `json:"children,omitempty"`      // For hierarchical structure
	IsImage      bool       `json:"is_image,omitempty"`      // Whether file is an image
	IsBinary     bool       `json:"is_binary,omitempty"`     // Whether the file bytes are binary and should be read via raw/download APIs
	Size         int64      `json:"size,omitempty"`          // File size in bytes
	MimeType     string     `json:"mime_type,omitempty"`     // Detected or extension-derived MIME type
	Encoding     string     `json:"encoding,omitempty"`      // Encoding for content when present (for example utf-8 or data-url)
	LastModified string     `json:"last_modified,omitempty"` // RFC3339 modification time
}

// CreateDocumentRequest represents the request to create a document
type CreateDocumentRequest struct {
	FilePath string `json:"filepath" binding:"required"`
	Content  string `json:"content" binding:"required"`
}

// UpdateDocumentRequest represents the request to update a document
type UpdateDocumentRequest struct {
	Content string `json:"content" binding:"required"`
}

// APIResponse represents a standard API response
type APIResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

// CreateDocumentResponse represents the response after creating a document
type CreateDocumentResponse struct {
	Document  Document          `json:"document"`
	Structure MarkdownStructure `json:"structure"`
}

// ListDocumentsRequest represents the request to list documents
type ListDocumentsRequest struct {
	Folder       string `form:"folder"`               // Base directory
	MaxDepth     int    `form:"max_depth,default=-1"` // Max directory depth (-1 = unlimited)
	Limit        int    `form:"limit,default=-1"`     // Max number of files to return (-1 = unlimited)
	Offset       int    `form:"offset,default=0"`     // Number of files to skip
	BlockedPaths string `form:"blocked_paths"`        // Comma-separated list of paths to exclude
}

// DeleteDocumentRequest represents the request to delete a document
type DeleteDocumentRequest struct {
	Confirm bool `form:"confirm"`
}

// MoveDocumentRequest represents the request to move a document
type MoveDocumentRequest struct {
	DestinationPath string `json:"destination_path" binding:"required"`
}

// PatchDocumentRequest represents the request to patch a document
type PatchDocumentRequest struct {
	TargetType     string `json:"target_type" binding:"required"`
	TargetSelector string `json:"target_selector" binding:"required"`
	Operation      string `json:"operation" binding:"required"`
	Content        string `json:"content" binding:"required"`
}

// DiffPatchRequest represents the request to apply a diff patch
type DiffPatchRequest struct {
	Diff string `json:"diff" binding:"required"`
	// DryRun applies the patch in memory and reports whether it would
	// apply, writing nothing (not even a new file's folder). Multi-file
	// patches check every file this way before writing any.
	DryRun bool `json:"dry_run,omitempty"`
}

// DiffPatchResponse represents the response after applying a diff patch
type DiffPatchResponse struct {
	Applied      bool                   `json:"applied"`
	DryRun       bool                   `json:"dry_run,omitempty"`
	WouldCreate  bool                   `json:"would_create,omitempty"`
	Suggestions  []string               `json:"suggestions,omitempty"`
	ErrorDetails map[string]interface{} `json:"error_details,omitempty"`
}

// FileVersion represents a version of a file
type FileVersion struct {
	CommitHash    string    `json:"commit_hash"`
	CommitMessage string    `json:"commit_message"`
	Author        string    `json:"author"`
	Date          time.Time `json:"date"`
	Content       string    `json:"content,omitempty"`
	Diff          string    `json:"diff,omitempty"`
}

// FileVersionHistoryRequest represents the request to get file version history
type FileVersionHistoryRequest struct {
	Limit int `form:"limit,default=10"`
}

// RestoreVersionRequest represents the request to restore a file version
type RestoreVersionRequest struct {
	CommitHash string `json:"commit_hash" binding:"required"`
}

// FileUploadRequest represents the request to upload a file
type FileUploadRequest struct {
	FolderPath string `form:"folder_path" binding:"required"`
}

// FileUploadResponse represents the response after file upload
type FileUploadResponse struct {
	AbsolutePath string `json:"absolute_path"`
	FilePath     string `json:"filepath"`
	FileName     string `json:"filename"`
	FileSize     int64  `json:"file_size"`
	ContentType  string `json:"content_type"`
	Folder       string `json:"folder"`
}

// CreateFolderRequest represents the request to create a folder
type CreateFolderRequest struct {
	FolderPath string `json:"folder_path" binding:"required"`
}

// CreateFolderResponse represents the response after folder creation
type CreateFolderResponse struct {
	FolderPath string `json:"folder_path"`
	Created    bool   `json:"created"`
}

// CopyFolderRequest represents the request to copy a folder
type CopyFolderRequest struct {
	SourcePath      string `json:"source_path" binding:"required"`
	DestinationPath string `json:"destination_path" binding:"required"`
}

// CopyFolderResponse represents the response after copying a folder
type CopyFolderResponse struct {
	SourcePath      string `json:"source_path"`
	DestinationPath string `json:"destination_path"`
	FilesCopied     int    `json:"files_copied"`
	DirsCreated     int    `json:"dirs_created"`
}

// GlobDocumentsRequest represents the request to discover files using glob patterns
type GlobDocumentsRequest struct {
	Pattern      string `form:"pattern" binding:"required"` // Glob pattern (e.g., "*.go", "**/*.md")
	Folder       string `form:"folder"`                     // Base directory to search within (default: root)
	MaxDepth     int    `form:"max_depth,default=-1"`       // Max directory depth (-1 = unlimited)
	IncludeDirs  bool   `form:"include_dirs,default=false"` // Include directories in results
	BlockedPaths string `form:"blocked_paths"`              // Comma-separated list of paths to exclude
}
