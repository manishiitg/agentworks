package skills

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// GitHubURLInfo contains parsed information from a GitHub URL
type GitHubURLInfo struct {
	Owner  string
	Repo   string
	Branch string
	Path   string
	Token  string // Optional PAT for private repos
}

// ParseGitHubURL parses a GitHub folder URL into its components
func ParseGitHubURL(rawURL string) (*GitHubURLInfo, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Host != "github.com" {
		return nil, fmt.Errorf("not a GitHub URL (host: %s)", parsed.Host)
	}

	// Parse path: /owner/repo/tree/branch/path/to/folder
	pathParts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(pathParts) < 4 || pathParts[2] != "tree" {
		return nil, fmt.Errorf("invalid GitHub URL format, expected: https://github.com/owner/repo/tree/branch/path")
	}

	owner := pathParts[0]
	repo := pathParts[1]
	branch := pathParts[3]
	folderPath := ""
	if len(pathParts) > 4 {
		folderPath = strings.Join(pathParts[4:], "/")
	}

	return &GitHubURLInfo{
		Owner:  owner,
		Repo:   repo,
		Branch: branch,
		Path:   folderPath,
	}, nil
}

// FetchGitHubFolderContents fetches the contents of a GitHub folder
func FetchGitHubFolderContents(info *GitHubURLInfo) ([]GitHubFileInfo, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
		info.Owner, info.Repo, url.PathEscape(info.Path), info.Branch)

	log.Printf("[GITHUB] Fetching: %s (token provided: %v)", apiURL, info.Token != "")

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if info.Token != "" {
		req.Header.Set("Authorization", "Bearer "+info.Token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch GitHub contents: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("[GITHUB] Response status: %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var files []GitHubFileInfo
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, fmt.Errorf("failed to decode GitHub response: %w", err)
	}

	return files, nil
}

// FetchGitHubFileContent fetches the content of a single file from GitHub
func FetchGitHubFileContent(downloadURL, token string) (string, error) {
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
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

// ValidateGitHubSkill validates a skill at a GitHub URL without importing it
func ValidateGitHubSkill(workspaceAPIURL, gitHubURL, token string) (*ValidateSkillResponse, error) {
	info, err := ParseGitHubURL(gitHubURL)
	if err != nil {
		return &ValidateSkillResponse{Valid: false, Error: err.Error()}, nil
	}
	info.Token = token

	files, err := FetchGitHubFolderContents(info)
	if err != nil {
		return &ValidateSkillResponse{Valid: false, Error: fmt.Sprintf("failed to fetch folder: %v", err)}, nil
	}

	var skillFile *GitHubFileInfo
	var fileNames []string
	for i := range files {
		fileNames = append(fileNames, files[i].Name)
		if files[i].Name == SkillFileName {
			skillFile = &files[i]
		}
	}

	if skillFile == nil {
		return &ValidateSkillResponse{Valid: false, Error: fmt.Sprintf("no %s found", SkillFileName), Files: fileNames}, nil
	}

	content, err := FetchGitHubFileContent(skillFile.DownloadURL, token)
	if err != nil {
		return &ValidateSkillResponse{Valid: false, Error: fmt.Sprintf("failed to fetch %s: %v", SkillFileName, err), Files: fileNames}, nil
	}

	frontmatter, _, err := ValidateSkillContent(content)
	if err != nil {
		return &ValidateSkillResponse{Valid: false, Error: fmt.Sprintf("invalid %s: %v", SkillFileName, err), Files: fileNames}, nil
	}

	// The authorized API checks for an existing install in its workspace.

	return &ValidateSkillResponse{Valid: true, Frontmatter: frontmatter, Files: fileNames, Exists: false}, nil
}

// ImportGitHubSkillInto is ImportGitHubSkill into basePath (a project's
// canonical skills folder), never an account-wide store.
func ImportGitHubSkillInto(workspaceAPIURL, gitHubURL, token, basePath string) (*ImportSkillResponse, error) {
	validation, err := ValidateGitHubSkill(workspaceAPIURL, gitHubURL, token)
	if err != nil {
		return &ImportSkillResponse{Success: false, Error: err.Error()}, nil
	}
	if !validation.Valid {
		return &ImportSkillResponse{Success: false, Error: validation.Error}, nil
	}

	info, err := ParseGitHubURL(gitHubURL)
	if err != nil {
		return &ImportSkillResponse{Success: false, Error: err.Error()}, nil
	}
	info.Token = token

	skillName := validation.Frontmatter.Name
	if skillName == "" {
		skillName = path.Base(info.Path)
	}
	skillName = sanitizeFolderName(skillName)

	files := map[string][]byte{}
	if err := collectGitHubSkillFiles(info, "", files); err != nil {
		return &ImportSkillResponse{Success: false, Error: err.Error()}, nil
	}
	workspacePath := strings.TrimSuffix(basePath, "/skills")
	if err := InstallSkillFilesIn(workspaceAPIURL, workspacePath, skillName, files); err != nil {
		return &ImportSkillResponse{Success: false, Error: err.Error()}, nil
	}

	return &ImportSkillResponse{Success: true, SkillName: skillName}, nil
}

func collectGitHubSkillFiles(info *GitHubURLInfo, relative string, out map[string][]byte) error {
	files, err := FetchGitHubFolderContents(info)
	if err != nil {
		return err
	}
	for _, file := range files {
		rel := path.Join(relative, file.Name)
		if file.Type == "dir" {
			sub := &GitHubURLInfo{Owner: info.Owner, Repo: info.Repo, Branch: info.Branch, Path: file.Path, Token: info.Token}
			if err := collectGitHubSkillFiles(sub, rel, out); err != nil {
				return err
			}
		} else if file.Type == "file" {
			content, err := FetchGitHubFileContent(file.DownloadURL, info.Token)
			if err != nil {
				return err
			}
			out[rel] = []byte(content)
		}
	}
	return nil
}

func sanitizeFolderName(name string) string {
	name = strings.ReplaceAll(name, " ", "-")
	reg := regexp.MustCompile(`[^a-zA-Z0-9\-_]`)
	name = strings.ToLower(reg.ReplaceAllString(name, ""))
	if name == "" {
		return "skill"
	}
	return name
}
