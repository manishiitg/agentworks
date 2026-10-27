package models

import "time"

// SearchResult represents a single search result
type SearchResult struct {
	Type           string    `json:"type,omitempty"`
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Filepath       string    `json:"filepath"`
	Folder         string    `json:"folder"`
	Matches        []string  `json:"matches"`
	Score          int       `json:"score"`
	LastModified   time.Time `json:"last_modified"`
	ContentPreview string    `json:"content_preview"`
	LineNumber     int       `json:"line_number"`
	MatchedText    string    `json:"matched_text"`
}

// SearchResponse represents the response from a search operation
type SearchResponse struct {
	Query   string         `json:"query"`
	Results []SearchResult `json:"results"`
	Total   int            `json:"total"`
	Method  string         `json:"method"`
}
