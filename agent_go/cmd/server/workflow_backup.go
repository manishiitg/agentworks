package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
)

const (
	workflowBackupStatusVersion = 1

	workflowBackupStateNotConfigured         = "not_configured"
	workflowBackupStateLocalOnly             = "local_only"
	workflowBackupStateConfiguredNotVerified = "configured_not_verified"
	workflowBackupStateRunning               = "running"
	workflowBackupStateHealthy               = "healthy"
	workflowBackupStatePartial               = "partial"
	workflowBackupStateFailed                = "failed"
)

type WorkflowBackupStatus struct {
	Version            int                               `json:"version"`
	State              string                            `json:"state"`
	LastAttemptAt      string                            `json:"last_attempt_at,omitempty"`
	LastSuccessAt      string                            `json:"last_success_at,omitempty"`
	LastAgentSessionID string                            `json:"last_agent_session_id,omitempty"`
	LastSourceHash     string                            `json:"last_source_hash,omitempty"`
	Summary            string                            `json:"summary,omitempty"`
	Destinations       []WorkflowBackupDestinationStatus `json:"destinations,omitempty"`
	LastError          string                            `json:"last_error,omitempty"`
	UpdatedAt          string                            `json:"updated_at,omitempty"`
}

type WorkflowBackupDestinationStatus struct {
	ID            string `json:"id"`
	Type          string `json:"type,omitempty"`
	Provider      string `json:"provider,omitempty"`
	State         string `json:"state"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	Commit        string `json:"commit,omitempty"`
	ObjectsSynced int    `json:"objects_synced,omitempty"`
	Summary       string `json:"summary,omitempty"`
	Error         string `json:"error,omitempty"`
}

type WorkflowBackupStrategyInfo struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	BestFor     []string `json:"best_for"`
}

type WorkflowBackupInfoResponse struct {
	Success           bool                         `json:"success"`
	Config            *WorkflowBackupConfig        `json:"config,omitempty"`
	Status            *WorkflowBackupStatus        `json:"status,omitempty"`
	EffectiveState    string                       `json:"effective_state"`
	CurrentSourceHash string                       `json:"current_source_hash,omitempty"`
	TrackedFilesCount int                          `json:"tracked_files_count,omitempty"`
	Supported         []WorkflowBackupStrategyInfo `json:"supported"`
	StatusPath        string                       `json:"status_path"`
}

func workflowBackupStatusPath(workspacePath string) string {
	return strings.TrimRight(workspacePath, "/") + "/backup/status.json"
}

func supportedWorkflowBackupStrategies() []WorkflowBackupStrategyInfo {
	return []WorkflowBackupStrategyInfo{
		{
			ID:          "git",
			Label:       "GitHub / remote Git (recommended)",
			Description: "Recommended for off-device protection of workflow config, planning, knowledgebase, learnings, scripts, and small JSON. A local Git repo alone is not a durable backup.",
			BestFor:     []string{"workflow", "planning", "knowledgebase", "learnings", "small-db"},
		},
		{
			ID:          "object_store",
			Label:       "R2 / S3 / B2",
			Description: "Best for run folders, generated media, large artifacts, and files that should not live in git.",
			BestFor:     []string{"runs", "large-artifacts", "media", "archives"},
		},
		{
			ID:          "huggingface",
			Label:       "HuggingFace Hub",
			Description: "Best for dataset/model-style backups, generated media, and revisioned ML artifacts.",
			BestFor:     []string{"datasets", "models", "media", "large-artifacts"},
		},
	}
}

func readWorkflowBackupStatus(ctx context.Context, workspacePath string) (*WorkflowBackupStatus, bool, error) {
	content, exists, err := readFileFromWorkspace(ctx, workflowBackupStatusPath(workspacePath))
	if err != nil || !exists {
		return nil, exists, err
	}
	var status WorkflowBackupStatus
	if err := json.Unmarshal([]byte(content), &status); err != nil {
		return nil, true, fmt.Errorf("failed to parse backup status: %w", err)
	}
	if status.Version == 0 {
		status.Version = workflowBackupStatusVersion
	}
	return &status, true, nil
}

// workflowBackupEffectiveState reports the backup's health only -- whether the
// last run succeeded, not whether the workflow's files have since changed. A
// prior version also flagged "stale" when a content hash no longer matched
// the last backup, which read as change detection rather than a health
// signal and confused users (2026-09-03).
func workflowBackupEffectiveState(config *WorkflowBackupConfig, status *WorkflowBackupStatus) string {
	if config == nil || !config.Enabled {
		return workflowBackupStateNotConfigured
	}
	if !workflowBackupHasRemoteDestination(config) {
		return workflowBackupStateLocalOnly
	}
	if status == nil || strings.TrimSpace(status.State) == "" {
		return workflowBackupStateConfiguredNotVerified
	}
	return strings.TrimSpace(status.State)
}

func workflowBackupHasRemoteDestination(config *WorkflowBackupConfig) bool {
	if config == nil {
		return false
	}
	for _, destination := range config.Destinations {
		provider := strings.ToLower(strings.TrimSpace(destination.Provider))
		destinationType := strings.NewReplacer("-", "_", " ", "_").Replace(
			strings.ToLower(strings.TrimSpace(destination.Type)),
		)
		repo := strings.TrimSpace(destination.Repo)
		bucket := strings.TrimSpace(destination.Bucket)
		if strings.HasPrefix(provider, "local") || provider == "filesystem" ||
			strings.HasPrefix(destinationType, "local") || destinationType == "git_bundle" {
			continue
		}
		if provider == "git" && repo == "" {
			continue
		}
		if provider != "" || repo != "" || bucket != "" {
			return true
		}
		if destinationType == "object_store" || destinationType == "huggingface" || destinationType == "remote_git" {
			return true
		}
	}
	return false
}

var backupHashFiles = []string{
	"workflow.json",
	"planning/plan.json",
	"planning/step_config.json",
	"planning/workflow_layout.json",
	"planning/step_override.json",
	"variables/variables.json",
	// The protected live db/db.sqlite is intentionally unreadable here. Its
	// managed WAL-aware restore image is the backup-owned database source, so a
	// DB-only change must still invalidate LastSourceHash.
	"backup/database/db.sqlite.sha256",
}

var backupHashFolders = []string{
	"db/reports",
	"knowledgebase",
	"learnings",
}

func computeWorkflowBackupSourceHash(ctx context.Context, workspacePath string) (string, int) {
	workspacePath = strings.TrimRight(workspacePath, "/")
	files := make(map[string]string)
	for _, relPath := range backupHashFiles {
		files[relPath] = workspacePath + "/" + relPath
	}

	for _, folder := range backupHashFolders {
		listing, exists, err := listWorkspaceFolder(ctx, workspacePath+"/"+folder, 100)
		if err != nil || !exists {
			continue
		}
		var fullPaths []string
		collectWorkspaceFilePaths(listing, &fullPaths)
		for _, fullPath := range fullPaths {
			relPath := strings.TrimPrefix(fullPath, workspacePath+"/")
			if relPath == fullPath || shouldSkipBackupHashFile(relPath) {
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
	tracked := 0
	for _, relPath := range relPaths {
		content, exists, err := readFileFromWorkspace(ctx, files[relPath])
		if err != nil || !exists {
			continue
		}
		tracked++
		hasher.Write([]byte(relPath))
		hasher.Write([]byte{0})
		hasher.Write([]byte(content))
		hasher.Write([]byte{0})
	}
	if tracked == 0 {
		return "", 0
	}
	return hex.EncodeToString(hasher.Sum(nil)), tracked
}

func shouldSkipBackupHashFile(relPath string) bool {
	lower := strings.ToLower(relPath)
	if strings.Contains(lower, "/.git/") || strings.HasPrefix(lower, ".git/") {
		return true
	}
	if strings.HasSuffix(lower, ".sqlite") || strings.HasSuffix(lower, ".db") {
		return true
	}
	if strings.Contains(lower, "/runs/") || strings.HasPrefix(lower, "runs/") {
		return true
	}
	// Installed packages (PLAT-284): hundreds of MB of numpy/pandas that any
	// run can recreate; never part of what a backup is for.
	if strings.Contains(lower, "/.sandbox-cache/") || strings.HasPrefix(lower, ".sandbox-cache/") {
		return true
	}
	return false
}

func (api *StreamingAPI) handleGetWorkflowBackup(w http.ResponseWriter, r *http.Request) {
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

	status, _, statusErr := readWorkflowBackupStatus(r.Context(), workspacePath)
	if statusErr != nil {
		log.Printf("[BACKUP] Failed to read backup status for %s: %v", workspacePath, statusErr)
	}
	sourceHash, trackedFiles := computeWorkflowBackupSourceHash(r.Context(), workspacePath)
	resp := WorkflowBackupInfoResponse{
		Success:           true,
		Config:            manifest.Backup,
		Status:            status,
		EffectiveState:    workflowBackupEffectiveState(manifest.Backup, status),
		CurrentSourceHash: sourceHash,
		TrackedFilesCount: trackedFiles,
		Supported:         supportedWorkflowBackupStrategies(),
		StatusPath:        workflowBackupStatusPath(workspacePath),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
