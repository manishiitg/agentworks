package subagents

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

// githubHTTPClient bounds GitHub fetches: a stalled connection would
// otherwise hang the install or import that started it forever.
var githubHTTPClient = &http.Client{Timeout: 60 * time.Second}

// GitHubURLInfo contains parsed information from a GitHub URL
type GitHubURLInfo struct {
	Owner  string
	Repo   string
	Branch string
	Path   string
	IsBlob bool   // True if it points to a single file (blob)
	Token  string // Optional PAT for private repos
}

// ParseGitHubURL parses a GitHub folder/file URL into its components
func ParseGitHubURL(rawURL string) (*GitHubURLInfo, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Host != "github.com" {
		return nil, fmt.Errorf("not a GitHub URL (host: %s)", parsed.Host)
	}

	// Parse path: /owner/repo/tree/branch/path/to/folder or /owner/repo/blob/branch/path/to/file
	pathParts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(pathParts) < 4 {
		return nil, fmt.Errorf("invalid GitHub URL format, expected: https://github.com/owner/repo/[tree|blob]/branch/path")
	}

	owner := pathParts[0]
	repo := pathParts[1]
	typePart := pathParts[2]
	branch := pathParts[3]

	if typePart != "tree" && typePart != "blob" {
		return nil, fmt.Errorf("invalid GitHub URL format, expected tree or blob, got: %s", typePart)
	}

	folderPath := ""
	if len(pathParts) > 4 {
		folderPath = strings.Join(pathParts[4:], "/")
	}

	return &GitHubURLInfo{
		Owner:  owner,
		Repo:   repo,
		Branch: branch,
		Path:   folderPath,
		IsBlob: typePart == "blob",
	}, nil
}

// FetchGitHubFileContent fetches the content of a single file from GitHub
func FetchGitHubFileContent(info *GitHubURLInfo) (string, error) {
	// Construct raw download URL
	apiURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
		info.Owner, info.Repo, info.Branch, url.PathEscape(info.Path))

	log.Printf("[GITHUB] Fetching raw file: %s", apiURL)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	if info.Token != "" {
		req.Header.Set("Authorization", "token "+info.Token)
	}

	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to fetch file: status %d, body: %s", resp.StatusCode, string(body))
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read file content: %w", err)
	}

	return string(content), nil
}

// ValidateGitHubSubAgent validates a subagent at a GitHub URL without importing it
func ValidateGitHubSubAgent(workspaceAPIURL, gitHubURL, token string) (*ValidateSubAgentResponse, error) {
	info, err := ParseGitHubURL(gitHubURL)
	if err != nil {
		return &ValidateSubAgentResponse{Valid: false, Error: err.Error()}, nil
	}
	info.Token = token

	if !info.IsBlob {
		return &ValidateSubAgentResponse{Valid: false, Error: "SubAgents must be imported from a single file (blob) URL, not a folder (tree)."}, nil
	}

	content, err := FetchGitHubFileContent(info)
	if err != nil {
		return &ValidateSubAgentResponse{Valid: false, Error: fmt.Sprintf("failed to fetch %s: %v", info.Path, err)}, nil
	}

	frontmatter, _, err := ParseSubAgentFile(content)
	if err != nil {
		return &ValidateSubAgentResponse{Valid: false, Error: fmt.Sprintf("invalid %s: %v", SubAgentFileName, err)}, nil
	}

	// Check if a subagent with this name already exists
	subAgentName := frontmatter.Name
	if subAgentName == "" {
		subAgentName = path.Base(info.Path)
		subAgentName = strings.TrimSuffix(subAgentName, ".md")
	}
	subAgentName = sanitizeFolderName(subAgentName)
	exists := false
	if _, err := GetSubAgent(workspaceAPIURL, subAgentName); err == nil {
		exists = true
	}

	return &ValidateSubAgentResponse{Valid: true, Frontmatter: frontmatter, Files: []string{SubAgentFileName}, Exists: exists}, nil
}

// ImportGitHubSubAgent imports a subagent from a GitHub URL to the workspace
func ImportGitHubSubAgent(workspaceAPIURL, gitHubURL, token string) (*ImportSubAgentResponse, error) {
	validation, err := ValidateGitHubSubAgent(workspaceAPIURL, gitHubURL, token)
	if err != nil {
		return &ImportSubAgentResponse{Success: false, Error: err.Error()}, nil
	}
	if !validation.Valid {
		return &ImportSubAgentResponse{Success: false, Error: validation.Error}, nil
	}

	info, err := ParseGitHubURL(gitHubURL)
	if err != nil {
		return &ImportSubAgentResponse{Success: false, Error: err.Error()}, nil
	}
	info.Token = token

	subAgentName := validation.Frontmatter.Name
	if subAgentName == "" {
		subAgentName = path.Base(info.Path)
		subAgentName = strings.TrimSuffix(subAgentName, ".md")
	}
	subAgentName = sanitizeFolderName(subAgentName)

	client := NewWorkspaceAPIClient(workspaceAPIURL)
	subAgentFolderPath := path.Join(SubAgentsBasePath, "custom", subAgentName)

	if err := client.CreateFolder(subAgentFolderPath); err != nil {
		if !strings.Contains(err.Error(), "exists") {
			return &ImportSubAgentResponse{Success: false, Error: fmt.Sprintf("failed to create folder: %v", err)}, nil
		}
	}

	content, err := FetchGitHubFileContent(info)
	if err != nil {
		return &ImportSubAgentResponse{Success: false, Error: fmt.Sprintf("failed to download: %v", err)}, nil
	}

	destFilePath := path.Join(subAgentFolderPath, SubAgentFileName)
	if err := client.WriteFile(destFilePath, content); err != nil {
		return &ImportSubAgentResponse{Success: false, Error: fmt.Sprintf("failed to write %s: %v", destFilePath, err)}, nil
	}

	return &ImportSubAgentResponse{Success: true, SubAgentName: subAgentName}, nil
}

func sanitizeFolderName(name string) string {
	name = strings.ReplaceAll(name, " ", "-")
	reg := regexp.MustCompile(`[^a-zA-Z0-9\-_]`)
	name = reg.ReplaceAllString(name, "")
	name = strings.ToLower(name)
	if name == "" {
		name = "subagent"
	}
	return name
}
