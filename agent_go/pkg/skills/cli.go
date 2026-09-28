package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsauth"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// CLILockEntry matches one entry in skills-lock.json
type CLILockEntry struct {
	Source       string `json:"source"`
	SourceType   string `json:"sourceType"`
	ComputedHash string `json:"computedHash"`
}

// CLILockFile matches the skills-lock.json schema
type CLILockFile struct {
	Version int                     `json:"version"`
	Skills  map[string]CLILockEntry `json:"skills"`
}

// CLIImportResult contains the results of a CLI import operation
type CLIImportResult struct {
	InstalledSkills []string                `json:"installed_skills"`
	LockEntries     map[string]CLILockEntry `json:"lock_entries,omitempty"`
	Errors          []string                `json:"errors,omitempty"`
}

// CLIUpdateInfo represents update availability for one skill
type CLIUpdateInfo struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	HasUpdate bool   `json:"has_update"`
}

// CLISearchResult represents a skill found via search
type CLISearchResult struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Skill    string `json:"skill"`
	URL      string `json:"url"`
	Installs string `json:"installs"`
}

// IsAvailable checks if the skills CLI is available in the workspace container
func IsAvailable() bool {
	// This is a quick check — we don't actually call the workspace API here
	// The real check happens via /api/skills/cli/available endpoint
	return true
}

// ImportToWorkspace installs skills via the workspace container's CLI.
// Calls POST /api/skills/cli/install on the workspace API.
func ImportToWorkspace(ctx context.Context, workspaceAPIURL, source string) (*CLIImportResult, error) {
	return ImportToWorkspaceDir(ctx, workspaceAPIURL, source, "")
}

// ImportToWorkspaceDir is ImportToWorkspace into targetDir, a project's
// skills folder (docs-relative, e.g. _users/<u>/Chats/Code/projects/<p>/skills),
// instead of the account-wide skills/ library. Empty means the library.
func ImportToWorkspaceDir(ctx context.Context, workspaceAPIURL, source, targetDir string) (*CLIImportResult, error) {
	payload := map[string]string{"source": source}
	if targetDir = strings.Trim(strings.TrimSpace(targetDir), "/"); targetDir != "" {
		payload["target_dir"] = targetDir
	}
	reqBody, _ := json.Marshal(payload)

	apiURL := fmt.Sprintf("%s/api/skills/cli/install", workspaceAPIURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("workspace API call failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("workspace install failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result CLIImportResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	log.Printf("[SKILLS CLI] Installed via workspace API: %v", result.InstalledSkills)
	return &result, nil
}

// DeleteProjectSkill removes one skill from a project's skills folder
// (targetDir, docs-relative) through the workspace service, which refuses any
// path through a link. A project folder is writable by its agent, so the
// generic file API, which follows links, must not be used for it.
func DeleteProjectSkill(ctx context.Context, workspaceAPIURL, targetDir, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return fmt.Errorf("invalid skill folder name %q", name)
	}
	reqBody, _ := json.Marshal(map[string]string{"target_dir": strings.Trim(strings.TrimSpace(targetDir), "/"), "name": name})
	httpReq, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/api/skills/project/delete", workspaceAPIURL), bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	wsauth.SetHeader(httpReq)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpReq)
	if err != nil {
		return fmt.Errorf("workspace API call failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// FindSkills searches for skills via the workspace container's CLI.
// Calls GET /api/skills/cli/search on the workspace API.
func FindSkills(ctx context.Context, query string) ([]CLISearchResult, error) {
	workspaceAPIURL := getWorkspaceAPIURLFromEnv()
	if workspaceAPIURL == "" {
		return nil, fmt.Errorf("workspace API URL not available")
	}

	apiURL := fmt.Sprintf("%s/api/skills/cli/search?q=%s", workspaceAPIURL, url.QueryEscape(query))
	httpReq, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("workspace API call failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("workspace search failed (status %d): %s", resp.StatusCode, string(body))
	}

	var results []CLISearchResult
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return results, nil
}

// ReadLockFile reads skills-lock.json from the workspace
func ReadLockFile(workspaceAPIURL string) (*CLILockFile, error) {
	client := NewWorkspaceAPIClient(workspaceAPIURL)
	content, err := client.ReadFile("skills-lock.json")
	if err != nil {
		return nil, fmt.Errorf("lock file not found: %w", err)
	}

	var lockFile CLILockFile
	if err := json.Unmarshal([]byte(content), &lockFile); err != nil {
		return nil, fmt.Errorf("failed to parse lock file: %w", err)
	}
	return &lockFile, nil
}

// CheckUpdates checks which installed skills have updates available.
func CheckUpdates(ctx context.Context, workspaceAPIURL string) ([]CLIUpdateInfo, error) {
	lockFile, err := ReadLockFile(workspaceAPIURL)
	if err != nil {
		return nil, fmt.Errorf("no lock file found: %w", err)
	}

	if len(lockFile.Skills) == 0 {
		return nil, nil
	}

	// Group by source, re-install to temp, compare hashes
	sourceSkills := make(map[string][]string)
	for name, entry := range lockFile.Skills {
		sourceSkills[entry.Source] = append(sourceSkills[entry.Source], name)
	}

	var results []CLIUpdateInfo
	for source, skillNames := range sourceSkills {
		freshResult, fetchErr := ImportToWorkspace(ctx, workspaceAPIURL, source)

		for _, name := range skillNames {
			info := CLIUpdateInfo{Name: name, Source: source}
			if fetchErr == nil {
				oldEntry := lockFile.Skills[name]
				if newEntry, ok := freshResult.LockEntries[name]; ok {
					info.HasUpdate = newEntry.ComputedHash != oldEntry.ComputedHash
				}
			}
			results = append(results, info)
		}
	}

	return results, nil
}

// UpdateAll re-installs all skills tracked in skills-lock.json from their sources.
func UpdateAll(ctx context.Context, workspaceAPIURL string) (*CLIImportResult, error) {
	lockFile, err := ReadLockFile(workspaceAPIURL)
	if err != nil {
		return nil, fmt.Errorf("no lock file found: %w", err)
	}

	sources := make(map[string]bool)
	for _, entry := range lockFile.Skills {
		sources[entry.Source] = true
	}

	combined := &CLIImportResult{
		LockEntries: make(map[string]CLILockEntry),
	}

	for source := range sources {
		result, importErr := ImportToWorkspace(ctx, workspaceAPIURL, source)
		if importErr != nil {
			combined.Errors = append(combined.Errors, fmt.Sprintf("failed to update from %s: %v", source, importErr))
			continue
		}
		combined.InstalledSkills = append(combined.InstalledSkills, result.InstalledSkills...)
		for k, v := range result.LockEntries {
			combined.LockEntries[k] = v
		}
	}

	return combined, nil
}

// RemoveFromLockFile removes a skill entry from skills-lock.json
func RemoveFromLockFile(workspaceAPIURL, skillName string) error {
	lockFile, err := ReadLockFile(workspaceAPIURL)
	if err != nil {
		return nil
	}

	if _, exists := lockFile.Skills[skillName]; !exists {
		return nil
	}

	delete(lockFile.Skills, skillName)

	client := NewWorkspaceAPIClient(workspaceAPIURL)
	data, err := json.MarshalIndent(lockFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lock file: %w", err)
	}
	return client.WriteFile("skills-lock.json", string(data))
}

// getWorkspaceAPIURLFromEnv returns the workspace API URL from environment
func getWorkspaceAPIURLFromEnv() string {
	if url := strings.TrimSpace(strings.TrimRight(getEnvOrDefault("WORKSPACE_API_URL", ""), "/")); url != "" {
		return url
	}
	return "http://localhost:8080"
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := strings.TrimSpace(strings.TrimRight(fmt.Sprintf("%s", getEnv(key)), "/")); val != "" {
		return val
	}
	return defaultVal
}

func getEnv(key string) string {
	val, _ := os.LookupEnv(key)
	return val
}
