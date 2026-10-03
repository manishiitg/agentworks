package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Publish is the share-twin of Backup: it deploys the workflow's HTML artifacts
// (Pulse log + report dashboard) to a public URL on any static host. It mirrors
// workflow_backup.go — provider-agnostic, agent-driven, status in publish/status.json.

const (
	workflowPublishStatusVersion = 1

	workflowPublishStateNotConfigured         = "not_configured"
	workflowPublishStateConfiguredNotVerified = "configured_not_verified"
	workflowPublishStatePublishing            = "publishing"
	workflowPublishStatePublished             = "published"
	workflowPublishStateFailed                = "failed"
)

type WorkflowPublishStatus struct {
	Version            int                                `json:"version"`
	State              string                             `json:"state"`
	URL                string                             `json:"url,omitempty"`
	LastPublishedAt    string                             `json:"last_published_at,omitempty"`
	LastAttemptAt      string                             `json:"last_attempt_at,omitempty"`
	LastAgentSessionID string                             `json:"last_agent_session_id,omitempty"`
	LastSourceHash     string                             `json:"last_source_hash,omitempty"`
	Visibility         string                             `json:"visibility,omitempty"`
	SecretName         string                             `json:"secret_name,omitempty"`
	Targets            []json.RawMessage                  `json:"targets,omitempty"`
	Summary            string                             `json:"summary,omitempty"`
	Destinations       []WorkflowPublishDestinationStatus `json:"destinations,omitempty"`
	LastError          string                             `json:"last_error,omitempty"`
	UpdatedAt          string                             `json:"updated_at,omitempty"`
}

type WorkflowPublishDestinationStatus struct {
	ID            string `json:"id"`
	Provider      string `json:"provider,omitempty"`
	Method        string `json:"method,omitempty"`
	State         string `json:"state"`
	URL           string `json:"url,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	Summary       string `json:"summary,omitempty"`
	Error         string `json:"error,omitempty"`
}

type WorkflowPublishStrategyInfo struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Method      string `json:"method"`
	Description string `json:"description"`
}

type WorkflowPublishInfoResponse struct {
	Success           bool                          `json:"success"`
	Config            *WorkflowPublishConfig        `json:"config,omitempty"`
	Status            *WorkflowPublishStatus        `json:"status,omitempty"`
	EffectiveState    string                        `json:"effective_state"`
	URL               string                        `json:"url,omitempty"`
	CurrentSourceHash string                        `json:"current_source_hash,omitempty"`
	Supported         []WorkflowPublishStrategyInfo `json:"supported"`
	StatusPath        string                        `json:"status_path"`
}

type WorkflowPublishSecretResponse struct {
	Success bool   `json:"success"`
	Name    string `json:"name"`
	Value   string `json:"value"`
}

func workflowPublishStatusPath(workspacePath string) string {
	return strings.TrimRight(workspacePath, "/") + "/publish/status.json"
}

// supportedWorkflowPublishStrategies is an illustrative hint list for the UI —
// NOT an enum. Any static host works; the deploy logic lives in publish-strategy.md.
func supportedWorkflowPublishStrategies() []WorkflowPublishStrategyInfo {
	return []WorkflowPublishStrategyInfo{
		{ID: "netlify", Label: "Netlify", Method: "cli", Description: "netlify deploy --prod; default URL *.netlify.app."},
		{ID: "vercel", Label: "Vercel", Method: "cli", Description: "vercel deploy --prod; default URL *.vercel.app."},
		{ID: "cloudflare-pages", Label: "Cloudflare Pages", Method: "cli", Description: "wrangler pages deploy; default URL *.pages.dev."},
		{ID: "github-pages", Label: "GitHub Pages", Method: "git", Description: "Push static files to the gh-pages branch."},
		{ID: "s3", Label: "S3 / object store", Method: "sync", Description: "aws s3 sync / rclone to a static bucket (the any-host catch-all)."},
	}
}

func readWorkflowPublishStatus(ctx context.Context, workspacePath string) (*WorkflowPublishStatus, bool, error) {
	content, exists, err := readFileFromWorkspace(ctx, workflowPublishStatusPath(workspacePath))
	if err != nil || !exists {
		return nil, exists, err
	}
	var status WorkflowPublishStatus
	if err := json.Unmarshal([]byte(content), &status); err != nil {
		return nil, true, fmt.Errorf("failed to parse publish status: %w", err)
	}
	if status.Version == 0 {
		status.Version = workflowPublishStatusVersion
	}
	return &status, true, nil
}

// workflowPublishEffectiveState reports the publish's health only -- whether
// the last publish succeeded, not whether the workflow's files have since
// changed. A prior version also flagged "stale" when a content hash no
// longer matched the last publish, which read as change detection rather
// than a health signal and confused users (2026-09-03).
func workflowPublishEffectiveState(config *WorkflowPublishConfig, status *WorkflowPublishStatus) string {
	if config == nil || !config.Enabled {
		return workflowPublishStateNotConfigured
	}
	if status == nil || strings.TrimSpace(status.State) == "" {
		return workflowPublishStateConfiguredNotVerified
	}
	return strings.TrimSpace(status.State)
}

// The artifacts whose change should trigger a re-publish: report HTML and
// db.sqlite (the dashboard snapshot is baked from it). Pulse is in-app only.
var publishHashFiles = []string{
	"db/db.sqlite",
}

var publishHashFolders = []string{
	"reports",
}

func computeWorkflowPublishSourceHash(ctx context.Context, workspacePath string) string {
	workspacePath = strings.TrimRight(workspacePath, "/")
	files := make(map[string]string)
	for _, relPath := range publishHashFiles {
		files[relPath] = workspacePath + "/" + relPath
	}
	for _, folder := range publishHashFolders {
		listing, exists, err := listWorkspaceFolder(ctx, workspacePath+"/"+folder, 100)
		if err != nil || !exists {
			continue
		}
		var fullPaths []string
		collectWorkspaceFilePaths(listing, &fullPaths)
		for _, fullPath := range fullPaths {
			relPath := strings.TrimPrefix(fullPath, workspacePath+"/")
			if relPath == fullPath {
				continue
			}
			files[relPath] = fullPath
		}
	}

	relPaths := make([]string, 0, len(files))
	for relPath := range files {
		relPaths = append(relPaths, relPath)
	}
	sort.Strings(relPaths)

	hasher := sha256.New()
	for _, relPath := range relPaths {
		content, exists, err := readFileFromWorkspace(ctx, files[relPath])
		if err != nil || !exists {
			continue
		}
		hasher.Write([]byte(relPath))
		hasher.Write([]byte(content))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func (api *StreamingAPI) handleGetWorkflowPublish(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
	if workspacePath == "" {
		http.Error(w, "workspace_path parameter is required", http.StatusBadRequest)
		return
	}

	manifest, found, err := ReadWorkflowManifest(r.Context(), workspacePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}

	status, _, statusErr := readWorkflowPublishStatus(r.Context(), workspacePath)
	if statusErr != nil {
		log.Printf("[PUBLISH] Failed to read publish status for %s: %v", workspacePath, statusErr)
	}
	sourceHash := computeWorkflowPublishSourceHash(r.Context(), workspacePath)
	url := ""
	if status != nil {
		url = status.URL
	}
	resp := WorkflowPublishInfoResponse{
		Success:           true,
		Config:            manifest.Publish,
		Status:            status,
		EffectiveState:    workflowPublishEffectiveState(manifest.Publish, status),
		URL:               url,
		CurrentSourceHash: sourceHash,
		Supported:         supportedWorkflowPublishStrategies(),
		StatusPath:        workflowPublishStatusPath(workspacePath),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (api *StreamingAPI) handleGetWorkflowPublishSecret(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
	requestedSecretName := strings.TrimSpace(r.URL.Query().Get("secret_name"))
	if workspacePath == "" {
		http.Error(w, "workspace_path parameter is required", http.StatusBadRequest)
		return
	}

	manifest, found, err := ReadWorkflowManifest(r.Context(), workspacePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}
	status, _, statusErr := readWorkflowPublishStatus(r.Context(), workspacePath)
	if statusErr != nil {
		log.Printf("[PUBLISH] Failed to read publish status for %s: %v", workspacePath, statusErr)
	}
	api.writePublishSecretResponse(w, r, workspacePath, manifest.Publish, status, requestedSecretName)
}

func (api *StreamingAPI) writePublishSecretResponse(w http.ResponseWriter, r *http.Request, workflowPath string, config *WorkflowPublishConfig, status *WorkflowPublishStatus, requestedSecretName string) {
	secretName, ok := resolvePublishPasswordSecretName(requestedSecretName, config, status)
	if !ok {
		http.Error(w, "publish password secret is not configured", http.StatusNotFound)
		return
	}

	value, found := api.resolvePublishSecretValue(r.Context(), GetUserIDFromContext(r.Context()), workflowPath, secretName)
	if !found {
		http.Error(w, "publish password secret value was not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(WorkflowPublishSecretResponse{
		Success: true,
		Name:    secretName,
		Value:   value,
	})
}

func (api *StreamingAPI) resolvePublishSecretValue(ctx context.Context, userID, workflowPath, secretName string) (string, bool) {
	secretName = strings.TrimSpace(secretName)
	if secretName == "" {
		return "", false
	}
	selected := api.loadSelectedSecrets(ctx, userID, workflowPath, []string{secretName})
	selectedGlobals := []string{secretName}
	for _, secret := range api.mergeGlobalSecretsFor(ctx, userID, selected, &selectedGlobals) {
		if secret.Name == secretName && secret.Value != "" {
			return secret.Value, true
		}
	}
	return "", false
}

func resolvePublishPasswordSecretName(requested string, config *WorkflowPublishConfig, status *WorkflowPublishStatus) (string, bool) {
	allowed := collectPublishPasswordSecretNames(config, status)
	if len(allowed) == 0 {
		return "", false
	}
	if requested != "" {
		if canonical, ok := allowed[strings.ToUpper(strings.TrimSpace(requested))]; ok {
			return canonical, true
		}
		return "", false
	}
	if len(allowed) == 1 {
		for _, canonical := range allowed {
			return canonical, true
		}
	}
	return "", false
}

func collectPublishPasswordSecretNames(config *WorkflowPublishConfig, status *WorkflowPublishStatus) map[string]string {
	names := make(map[string]string)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || !publishSecretLooksLikePassword(name) {
			return
		}
		names[strings.ToUpper(name)] = name
	}

	if config != nil {
		add(extractPublishSecretNameFromText(config.Notes))
		for _, destination := range config.Destinations {
			add(destination.SecretName)
			add(extractPublishSecretNameFromText(destination.Notes))
		}
	}
	if status != nil {
		add(status.SecretName)
		add(extractPublishSecretNameFromText(status.Summary))
		for _, destination := range status.Destinations {
			add(extractPublishSecretNameFromText(destination.Summary))
		}
	}

	return names
}

func publishSecretLooksLikePassword(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	return strings.Contains(upper, "PASSWORD") ||
		strings.Contains(upper, "PASSPHRASE") ||
		strings.Contains(upper, "STATICRYPT") ||
		strings.HasSuffix(upper, "_PASS") ||
		upper == "PASS"
}

var publishSecretNamePattern = regexp.MustCompile(`(?i)(?:workflow\s+secret|secret)\s+([A-Z][A-Z0-9_]{2,})\b`)

func extractPublishSecretNameFromText(text string) string {
	match := publishSecretNamePattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}
