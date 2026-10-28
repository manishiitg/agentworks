package models

// MarkdownStructure represents the analyzed structure of a markdown document
type MarkdownStructure struct {
	Headings   []Heading `json:"headings"`
	Tables     []Table   `json:"tables"`
	Lists      []List    `json:"lists"`
	CodeBlocks int       `json:"code_blocks"`
	Links      int       `json:"links"`
	Images     int       `json:"images"`
	Paragraphs int       `json:"paragraphs"`
}

// Heading represents a markdown heading
type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	Line  int    `json:"line"`
}

// Table represents a markdown table
type Table struct {
	Index     int        `json:"index"`
	Headers   []string   `json:"headers"`
	Rows      int        `json:"rows"`
	Columns   int        `json:"columns"`
	LineStart int        `json:"line_start"`
	Data      [][]string `json:"data,omitempty"`
}

// List represents a markdown list
type List struct {
	Type      string   `json:"type"` // "unordered" or "ordered"
	Items     int      `json:"items"`
	LineStart int      `json:"line_start"`
	Content   []string `json:"content,omitempty"`
}

// PatchRequest represents the request to patch a document
type PatchRequest struct {
	TargetType     string `json:"target_type" binding:"required"`     // 'heading', 'table', 'list', 'paragraph', 'code_block'
	TargetSelector string `json:"target_selector" binding:"required"` // Selector (heading text, table index, list index, etc.)
	Operation      string `json:"operation" binding:"required"`       // 'append', 'prepend', 'replace', 'insert_after', 'insert_before'
	Content        string `json:"content" binding:"required"`         // New content to patch
}

// SearchRequest represents the request to search documents
type SearchRequest struct {
	FileTreeVisibility
	SearchType   string `form:"search_type"` // "filename" or "content" (text inside files)
	MatchCase    bool   `form:"match_case"`
	WholeWord    bool   `form:"whole_word"`
	Regex        bool   `form:"regex"`
	Query        string `form:"query" binding:"required"`
	Folder       string `form:"folder"` // Optional folder to search in
	Limit        int    `form:"limit,default=50"`
	BlockedPaths string `form:"blocked_paths"` // Comma-separated list of paths to exclude from search
}

// NestedContentRequest represents the request to get nested content
type NestedContentRequest struct {
	Path string `form:"path"` // Path like "Introduction -> Getting Started" (optional, defaults to level 1 headings)
}
