package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsauth"
)

// CLILockEntry matches one entry in skills-lock.json
type CLILockEntry struct {
	Source       string `json:"source"`
	SourceType   string `json:"sourceType"`
	ComputedHash string `json:"computedHash"`
}

// CLIImportResult contains the results of a CLI import operation
type CLIImportResult struct {
	InstalledSkills []string                `json:"installed_skills"`
	LockEntries     map[string]CLILockEntry `json:"lock_entries,omitempty"`
	Errors          []string                `json:"errors,omitempty"`
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

// ImportToWorkspaceDir installs into targetDir, a workspace's
// skills folder (docs-relative, e.g. _users/<u>/Chats/Code/projects/<p>/skills),
// A target workspace is required; there is no account-wide install.
func ImportToWorkspaceDir(ctx context.Context, workspaceAPIURL, source, targetDir string) (*CLIImportResult, error) {
	if strings.TrimSpace(targetDir) == "" {
		return nil, fmt.Errorf("choose a workspace before installing skills")
	}
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
	wsauth.SetHeader(httpReq)

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
