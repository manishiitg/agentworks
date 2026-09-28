package skills

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// WorkspaceAPIClient provides methods to interact with the workspace API
type WorkspaceAPIClient struct {
	BaseURL string
	Client  *http.Client
}

// NewWorkspaceAPIClient creates a new workspace API client
func NewWorkspaceAPIClient(baseURL string) *WorkspaceAPIClient {
	return &WorkspaceAPIClient{
		BaseURL: baseURL,
		Client:  &http.Client{},
	}
}

// DocumentsResponse represents the response from listing documents
type DocumentsResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    []DocumentEntry `json:"data"`
}

// DocumentEntry represents a file or folder in the workspace
type DocumentEntry struct {
	Filepath     string          `json:"filepath"`
	Type         string          `json:"type"` // "file" or "folder"
	IsBinary     bool            `json:"is_binary,omitempty"`
	Size         int64           `json:"size,omitempty"`
	MimeType     string          `json:"mime_type,omitempty"`
	LastModified string          `json:"last_modified,omitempty"`
	Children     []DocumentEntry `json:"children,omitempty"`
}

// DocumentContentResponse represents the response from reading a document
type DocumentContentResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Content      string `json:"content"`
		Filepath     string `json:"filepath,omitempty"`
		IsBinary     bool   `json:"is_binary,omitempty"`
		Size         int64  `json:"size,omitempty"`
		MimeType     string `json:"mime_type,omitempty"`
		LastModified string `json:"last_modified"`
	} `json:"data"`
	Message string `json:"message"`
	// The workspace API reports a missing document as HTTP 200 with
	// success:true and the reason only in these fields. They were not decoded
	// at all, which is why a not-found was indistinguishable from an empty
	// file to every caller.
	Error string `json:"error,omitempty"`
}

// ListFiles lists files in a folder via workspace API
func (c *WorkspaceAPIClient) ListFiles(folderPath string) ([]DocumentEntry, error) {
	reqURL := fmt.Sprintf("%s/api/documents?folder=%s", c.BaseURL, url.QueryEscape(folderPath))

	resp, err := c.Client.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list files: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result DocumentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if !result.Success {
		return nil, fmt.Errorf("API returned error: %s", result.Message)
	}

	// The workspace API wraps the folder listing: data=[{filepath:"skills", type:"folder", children:[...]}]
	// It also repeats each of those children as its own top-level entry,
	// expanded one level deeper. Flattening both shapes returned every
	// subfolder twice, so a workspace holding one skill discovered two, and
	// the identically named pair was rejected when the agent definition was
	// finalized. The requested folder's own entry is the authoritative
	// listing of its direct contents; when it is present, read nothing else.
	for _, entry := range result.Data {
		if entry.Type == "folder" && entry.Filepath == folderPath {
			return entry.Children, nil
		}
	}

	// No self entry: the response is already this folder's contents.
	return result.Data, nil
}

// ReadFile reads a file's content via workspace API
func (c *WorkspaceAPIClient) ReadFile(filePath string) (string, error) {
	reqURL := fmt.Sprintf("%s/api/documents/%s", c.BaseURL, url.PathEscape(filePath))

	resp, err := c.Client.Get(reqURL)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to read file: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result DocumentContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if !result.Success {
		return "", fmt.Errorf("API returned error: %s", result.Message)
	}
	// The workspace API answers a missing document with HTTP 200 AND
	// success:true, carrying the failure only in Error/Message with an empty
	// filepath. Without this check the empty Content flows on and the first
	// parser to touch it reports the file as malformed — a missing skill was
	// surfacing as "SKILL.md must start with YAML frontmatter (---)".
	if strings.TrimSpace(result.Error) != "" && strings.TrimSpace(result.Data.Filepath) == "" {
		return "", fmt.Errorf("file not found: %s", filePath)
	}
	if result.Data.IsBinary {
		return "", fmt.Errorf("cannot read binary file as text: %s", filePath)
	}

	return result.Data.Content, nil
}

// WriteFile writes content to a file via workspace API
func (c *WorkspaceAPIClient) WriteFile(filePath, content string) error {
	reqURL := fmt.Sprintf("%s/api/documents/%s", c.BaseURL, url.PathEscape(filePath))

	body := map[string]string{
		"content": content,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPut, reqURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to write file: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// CreateFolder creates a folder via workspace API
func (c *WorkspaceAPIClient) CreateFolder(folderPath string) error {
	reqURL := fmt.Sprintf("%s/api/folders", c.BaseURL)

	body := map[string]string{
		"folder_path": folderPath,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, reqURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create folder: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create folder: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// DeleteFolder deletes a folder via workspace API
func (c *WorkspaceAPIClient) DeleteFolder(folderPath string) error {
	reqURL := fmt.Sprintf("%s/api/folders/%s?confirm=true", c.BaseURL, url.PathEscape(folderPath))

	req, err := http.NewRequest(http.MethodDelete, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete folder: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete folder: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// DiscoverSkills discovers all skills in the workspace, including those in skills/custom/
func DiscoverSkills(workspaceAPIURL string) ([]Skill, error) {
	client := NewWorkspaceAPIClient(workspaceAPIURL)

	// List all folders in the skills directory
	entries, err := client.ListFiles(SkillsBasePath)
	if err != nil {
		// If skills folder doesn't exist or workspace API is unreachable, return empty list
		return []Skill{}, nil
	}

	var skills []Skill

	// Helper to process a potential skill folder
	processSkillFolder := func(entry DocumentEntry, prefix string) {
		folderName := entry.Filepath
		if prefix != "" {
			// For nested skills, construct relative folder name
			// entry.Filepath is full path like "skills/custom/my-skill"
			// we want "custom/my-skill"
			parts := strings.Split(entry.Filepath, "/")
			if len(parts) >= 2 {
				// Take the last N parts based on prefix depth
				// But simpler: just strip "skills/" prefix
				relPath := strings.TrimPrefix(entry.Filepath, SkillsBasePath+"/")
				folderName = relPath
			}
		} else {
			folderName = path.Base(entry.Filepath)
		}

		// Try to read SKILL.md from this folder
		skillFilePath := path.Join(entry.Filepath, SkillFileName)
		content, err := client.ReadFile(skillFilePath)
		if err != nil {
			// Skip folders without SKILL.md
			return
		}

		// Parse the skill
		skill, err := ParseSkillFromContent(content, folderName, skillFilePath)
		if err != nil {
			// Log but skip invalid skills
			return
		}

		skills = append(skills, *skill)
	}

	// Process each entry in skills/
	for _, entry := range entries {
		if entry.Type != "folder" {
			continue
		}

		folderName := path.Base(entry.Filepath)

		// Check for "custom" folder
		if folderName == "custom" {
			// List contents of skills/custom
			customEntries, err := client.ListFiles(entry.Filepath)
			if err == nil {
				for _, customEntry := range customEntries {
					if customEntry.Type == "folder" {
						processSkillFolder(customEntry, "custom")
					}
				}
			}
			continue
		}

		// Process standard skill folder
		processSkillFolder(entry, "")
	}

	// Enrich with lock file info (source URL + version tracking)
	lockFile, lockErr := ReadLockFile(workspaceAPIURL)
	if lockErr == nil && lockFile != nil {
		for i, skill := range skills {
			if entry, ok := lockFile.Skills[skill.FolderName]; ok {
				skills[i].SourceURL = entry.Source
				skills[i].LockInfo = &entry
			}
		}
	}

	return skills, nil
}

// GetSkill retrieves a specific skill by folder name
func GetSkill(workspaceAPIURL, folderName string) (*Skill, error) {
	return GetSkillIn(workspaceAPIURL, "", folderName)
}

// GetSkillIn resolves a skill from a workspace's own skills/ folder, falling
// back to the user-level one.
//
// Products that install skills per project (Video Studio installs its managed
// HyperFrames set into <project>/skills/) were invisible to the unscoped
// lookup, which only ever read _users/<id>/skills/. Every attach of such a
// skill failed, including one the product explicitly declares as attach:.
// The fallback keeps ordinary workflows, whose skills are user-level, working
// exactly as before.
func GetSkillIn(workspaceAPIURL, workspacePath, folderName string) (*Skill, error) {
	validName, nameErr := ValidateSkillName(folderName)
	if nameErr != nil {
		return nil, nameErr
	}
	folderName = validName
	client := NewWorkspaceAPIClient(workspaceAPIURL)

	candidates := make([]string, 0, 2)
	if strings.TrimSpace(workspacePath) != "" {
		candidates = append(candidates, path.Join(workspacePath, SkillsBasePath, folderName, SkillFileName))
	}
	candidates = append(candidates, path.Join(SkillsBasePath, folderName, SkillFileName))

	// The fetch path may be workspace-scoped, but the path handed back must stay
	// workspace-relative: it is what the lazy-body excerpt tells the agent to
	// read, and the agent's folder guard is rooted at its own workspace.
	relativePath := path.Join(SkillsBasePath, folderName, SkillFileName)
	var content string
	var found bool
	var err error
	for _, candidate := range candidates {
		content, err = client.ReadFile(candidate)
		if err == nil {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("skill not found: %w", err)
	}
	skillFilePath := relativePath

	skill, err := ParseSkillFromContent(content, folderName, skillFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse skill: %w", err)
	}

	return skill, nil
}

// DeleteSkill deletes a skill folder
func DeleteSkill(workspaceAPIURL, folderName string) error {
	client := NewWorkspaceAPIClient(workspaceAPIURL)

	skillFolderPath := path.Join(SkillsBasePath, folderName)
	if err := client.DeleteFolder(skillFolderPath); err != nil {
		return fmt.Errorf("failed to delete skill: %w", err)
	}

	return nil
}

// UpdateSkill updates a skill's SKILL.md content
func UpdateSkill(workspaceAPIURL, folderName, content string) (*Skill, error) {
	// Validate the content first
	frontmatter, body, err := ValidateSkillContent(content)
	if err != nil {
		return nil, fmt.Errorf("invalid skill content: %w", err)
	}

	client := NewWorkspaceAPIClient(workspaceAPIURL)

	skillFilePath := path.Join(SkillsBasePath, folderName, SkillFileName)
	if err := client.WriteFile(skillFilePath, content); err != nil {
		return nil, fmt.Errorf("failed to write skill: %w", err)
	}

	return &Skill{
		Frontmatter: *frontmatter,
		Content:     body,
		FolderName:  folderName,
		FilePath:    skillFilePath,
	}, nil
}
