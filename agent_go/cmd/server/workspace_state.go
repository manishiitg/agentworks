package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	orchtypes "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/types"
)

// WorkspaceStateResponse is the consolidated response containing all workspace data
type WorkspaceStateResponse struct {
	Success bool            `json:"success"`
	Data    *WorkspaceState `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// WorkspaceState contains all workspace-related data in a single structure
type WorkspaceState struct {
	// Run folders and progress
	RunFolders       []RunFolderInfo `json:"run_folders"`
	SelectedProgress *StepProgress   `json:"selected_progress,omitempty"`

	// Variables manifest
	VariablesManifest *VariablesManifest `json:"variables_manifest,omitempty"`

	// Workflow phases
	Phases []orchtypes.WorkflowPhase `json:"phases"`

	// Currently running executions for this workspace (from in-memory registry)
	ActiveExecutions []ActiveWorkflowExecution `json:"active_executions,omitempty"`
}

// handleLoadWorkspaceState handles loading all workspace state in a single request
func (api *StreamingAPI) handleLoadWorkspaceState(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspacePath := r.URL.Query().Get("workspace_path")
	if workspacePath == "" {
		http.Error(w, "workspace_path parameter is required", http.StatusBadRequest)
		return
	}

	selectedFolder := r.URL.Query().Get("selected_folder") // Optional
	fmt.Printf("[DEBUG] handleLoadWorkspaceState: workspacePath=%s, selectedFolder=%s\n", workspacePath, selectedFolder)

	ctx := r.Context()
	state := &WorkspaceState{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []string

	// Load run folders
	wg.Add(1)
	go func() {
		defer wg.Done()
		folders, err := api.loadRunFoldersInternal(ctx, workspacePath)
		if err != nil {
			mu.Lock()
			errors = append(errors, fmt.Sprintf("run_folders: %v", err))
			mu.Unlock()
			return
		}
		mu.Lock()
		state.RunFolders = folders
		mu.Unlock()
	}()

	// Load variables manifest
	wg.Add(1)
	go func() {
		defer wg.Done()
		manifest, err := api.loadVariablesManifestInternal(ctx, workspacePath)
		if err != nil {
			mu.Lock()
			errors = append(errors, fmt.Sprintf("variables: %v", err))
			mu.Unlock()
			return
		}
		mu.Lock()
		state.VariablesManifest = manifest
		mu.Unlock()
	}()

	// Load phases (synchronous, fast)
	state.Phases = orchtypes.GetWorkflowConstants().Phases

	// Wait for all goroutines to complete
	wg.Wait()

	// Check if there were any errors
	if len(errors) > 0 {
		response := WorkspaceStateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to load some data: %v", errors),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPartialContent) // 206 - some data loaded successfully
		json.NewEncoder(w).Encode(response)
		return
	}

	// Populate active executions from the unified execution tracker.
	state.ActiveExecutions = api.listRunningWorkflowExecutionsForWorkspace(workspacePath)

	// Success - return all data
	response := WorkspaceStateResponse{
		Success: true,
		Data:    state,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// loadRunFoldersInternal loads run folders (extracted from handleGetRunFolders)
func (api *StreamingAPI) loadRunFoldersInternal(ctx context.Context, workspacePath string) ([]RunFolderInfo, error) {
	// This is the core logic from handleGetRunFolders
	// Returns just the folders array without HTTP handling
	folders, err := api.getRunFoldersFromWorkspace(ctx, workspacePath)
	if err != nil {
		// Propagate. This used to return an empty slice with a nil error, which
		// made "the workspace listing failed" indistinguishable from "this
		// workflow has no run folders". Both existing HTTP callers already had
		// `if err != nil` branches that could never fire, and the scheduler's
		// run-outcome reconciler silently treated the empty result as a real
		// baseline: with no folders recorded as pre-existing, every historical
		// run folder looked newly created by the current invocation, so any
		// old folder still carrying status "failed" was reported as this run's
		// failure. A healthy hetznerssh security audit was marked failed that
		// way on 2026-08-10, blamed on an iteration that had failed the day
		// before, moments after workspace folder calls were returning errors.
		return []RunFolderInfo{}, fmt.Errorf("load run folders for %s: %w", workspacePath, err)
	}
	return folders, nil
}

// loadVariablesManifestInternal loads variables manifest (extracted from handleGetVariableGroups)
func (api *StreamingAPI) loadVariablesManifestInternal(ctx context.Context, workspacePath string) (*VariablesManifest, error) {
	variablesPath := workspacePath + "/variables/variables.json"
	content, exists, err := readFileFromWorkspace(ctx, variablesPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil // No variables file - return nil, not error
	}

	var manifest VariablesManifest
	if err := json.Unmarshal([]byte(content), &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse variables.json: %w", err)
	}

	return &manifest, nil
}

// getRunFoldersFromWorkspace gets run folders by calling workspace API
// Extracted from handleGetRunFolders for reuse
func (api *StreamingAPI) getRunFoldersFromWorkspace(ctx context.Context, workspacePath string) ([]RunFolderInfo, error) {
	runsPath := workspacePath + "/runs"

	// List folders from workspace API
	apiURL := getWorkspaceAPIURL() + "/api/documents"
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add query parameters
	// Use max_depth=2 to list nested folders (iteration-X/group-Y)
	q := req.URL.Query()
	q.Add("folder", runsPath)
	q.Add("max_depth", "2")
	req.URL.RawQuery = q.Encode()

	resp, err := workspaceHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call workspace API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check if runs folder doesn't exist (404)
	if resp.StatusCode == http.StatusNotFound {
		return []RunFolderInfo{}, nil // Empty list, not error
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("workspace API returned status %d", resp.StatusCode)
	}

	// Parse workspace API response
	var apiResp virtualtools.WorkspaceAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	if !apiResp.Success {
		return []RunFolderInfo{}, nil // Empty list, not error
	}

	// Extract folder names from response data
	existingFolders := []string{} // Initialize as empty slice, not nil

	// Handle different response structures using typed structs
	// Case 1: Data is a WorkspaceFolderListing (array of folder items)
	var folderListing virtualtools.WorkspaceFolderListing
	if dataBytes, err := json.Marshal(apiResp.Data); err == nil {
		if err := json.Unmarshal(dataBytes, &folderListing); err == nil && len(folderListing) > 0 {
			// Extract iteration folders from the first item's children (the runs folder)
			if len(folderListing) > 0 && len(folderListing[0].Children) > 0 {
				existingFolders = extractIterationFoldersFromTypedChildren(folderListing[0].Children, existingFolders)
			}
		} else {
			// Fallback: try to parse as array of interface{} (backward compatibility)
			if dataArray, ok := apiResp.Data.([]interface{}); ok {
				existingFolders = extractIterationFoldersFromInterfaceArray(dataArray, existingFolders)
			} else if dataMap, ok := apiResp.Data.(map[string]interface{}); ok {
				if children, ok := dataMap["children"].([]interface{}); ok {
					existingFolders = extractIterationFoldersFromChildren(children, existingFolders)
				}
			}
		}
	}

	// Build folder info list
	folderInfos := make([]RunFolderInfo, 0, len(existingFolders))
	for _, folderName := range existingFolders {
		folderInfos = append(folderInfos, RunFolderInfo{Name: folderName})
	}

	// Sort by iteration number descending (highest first), then by group name ascending within the same iteration
	// Supports both formats: iteration-X and iteration-X/group-Y
	sort.Slice(folderInfos, func(i, j int) bool {
		iterI := extractIterationNumber(folderInfos[i].Name)
		iterJ := extractIterationNumber(folderInfos[j].Name)
		if iterI != iterJ {
			return iterI > iterJ
		}
		// Same iteration: sort group names ascending so alphabetically early names (e.g. "atul") aren't pushed to the end
		return folderInfos[i].Name < folderInfos[j].Name
	})

	// Limit to 10 most recent folders/groups
	const maxFolders = 10
	if len(folderInfos) > maxFolders {
		folderInfos = folderInfos[:maxFolders]
	}

	// Load metadata for all iterations/groups in parallel (bounded concurrency)
	{
		var wgMeta sync.WaitGroup
		sem := make(chan struct{}, 10) // max 10 concurrent reads
		for i := range folderInfos {
			wgMeta.Add(1)
			go func(idx int) {
				defer wgMeta.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				folderName := folderInfos[idx].Name
				metadataPath := workspacePath + "/runs/" + folderName + "/run_metadata.json"
				metadata, _ := readRunMetadata(ctx, metadataPath)
				if metadata != nil {
					folderInfos[idx].Metadata = metadata
				}
			}(i)
		}
		wgMeta.Wait()
	}

	// Re-sort by created_at (most recent first) when metadata is available
	sort.Slice(folderInfos, func(i, j int) bool {
		mi := folderInfos[i].Metadata
		mj := folderInfos[j].Metadata
		if mi != nil && mj != nil {
			return mi.CreatedAt.After(mj.CreatedAt)
		}
		// Folders with metadata come first
		if mi != nil {
			return true
		}
		if mj != nil {
			return false
		}
		// Fallback: iteration number descending
		return extractIterationNumber(folderInfos[i].Name) > extractIterationNumber(folderInfos[j].Name)
	})

	return folderInfos, nil
}

// extractIterationNumber extracts the iteration number from a folder name.
// Supports both "iteration-X" and "iteration-X/group-Y" formats.
func extractIterationNumber(name string) int {
	matches := iterationRe.FindStringSubmatch(name)
	if len(matches) > 1 {
		var num int
		if _, err := fmt.Sscanf(matches[1], "%d", &num); err == nil {
			return num
		}
	}
	return -1
}

// handleGetActiveExecutions returns currently running workflow executions
// GET /api/workflow/active-executions?workspace_path=... (optional filter)
func (api *StreamingAPI) handleGetActiveExecutions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	workspacePath := r.URL.Query().Get("workspace_path")

	executions := api.listRunningWorkflowExecutionsForWorkspace(workspacePath)
	// Unfiltered, the tracker lists every user's runs: keep only the
	// workflows this caller may read (a named workspace_path was already
	// checked by the route).
	if workspacePath == "" {
		executions = activeExecutionsReadableBy(r.Context(), GetUserFromContext(r.Context()), executions)
	}

	if executions == nil {
		executions = []ActiveWorkflowExecution{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"executions": executions,
	})
}

// WorkflowSummary is a lightweight per-workflow summary for dashboard pages.
type WorkflowSummary struct {
	WorkspacePath   string      `json:"workspace_path"`
	TotalRuns       int         `json:"total_runs"`
	LatestRun       *RunSummary `json:"latest_run,omitempty"`
	IsRunning       bool        `json:"is_running"`
	ActiveRunFolder string      `json:"active_run_folder,omitempty"`
}

type WorkflowOverviewRunFolderDetail struct {
	Folder         RunFolderInfo      `json:"folder"`
	TotalSteps     int                `json:"total_steps"`
	CompletedSteps int                `json:"completed_steps"`
	LastUpdated    *string            `json:"last_updated,omitempty"`
	CostUSD        *float64           `json:"cost_usd,omitempty"`
	StartedAt      *string            `json:"started_at,omitempty"`
	CompletedAt    *string            `json:"completed_at,omitempty"`
	TriggeredBy    *string            `json:"triggered_by,omitempty"`
	Status         string             `json:"status"`
	Models         *RunMetadataModels `json:"models,omitempty"`
}

type WorkflowOverview struct {
	WorkspacePath  string                            `json:"workspace_path"`
	RunFolders     []WorkflowOverviewRunFolderDetail `json:"run_folders"`
	LastUpdated    *string                           `json:"last_updated,omitempty"`
	TotalRunCount  int                               `json:"total_run_count"`
	ActiveRunPaths []string                          `json:"active_run_paths,omitempty"`
	Error          string                            `json:"error,omitempty"`
}

// RunSummary is the minimal metadata for the most recent run folder.
type RunSummary struct {
	Folder         string  `json:"folder"`
	Status         string  `json:"status"`
	CreatedAt      string  `json:"created_at,omitempty"`
	CompletedAt    *string `json:"completed_at,omitempty"`
	CompletedSteps int     `json:"completed_steps"`
	TotalSteps     int     `json:"total_steps"`
}

func selectWorkflowSummaryFolder(ctx context.Context, workspacePath string, folders []string, activeRunFolder string) (string, *RunMetadata) {
	if len(folders) == 0 {
		return "", nil
	}

	iterationZeroFolders := make([]string, 0, len(folders))
	for _, folder := range folders {
		if extractIterationNumber(folder) == 0 {
			iterationZeroFolders = append(iterationZeroFolders, folder)
		}
	}

	candidates := iterationZeroFolders
	if len(candidates) == 0 {
		candidates = folders
	}

	metadataByFolder := make(map[string]*RunMetadata, len(candidates))
	for _, folder := range candidates {
		metadataPath := workspacePath + "/runs/" + folder + "/run_metadata.json"
		metadata, _ := readRunMetadata(ctx, metadataPath)
		metadataByFolder[folder] = metadata
	}

	if activeRunFolder != "" {
		if _, ok := metadataByFolder[activeRunFolder]; ok {
			return activeRunFolder, metadataByFolder[activeRunFolder]
		}
	}

	bestFolder := candidates[0]
	bestMetadata := metadataByFolder[bestFolder]
	bestTime := runSummarySortTime(bestMetadata)

	for _, folder := range candidates[1:] {
		metadata := metadataByFolder[folder]
		candidateTime := runSummarySortTime(metadata)
		if candidateTime > bestTime || (candidateTime == bestTime && folder < bestFolder) {
			bestFolder = folder
			bestMetadata = metadata
			bestTime = candidateTime
		}
	}

	return bestFolder, bestMetadata
}

func runSummarySortTime(metadata *RunMetadata) int64 {
	if metadata == nil {
		return 0
	}
	if metadata.CompletedAt != nil && !metadata.CompletedAt.IsZero() {
		return metadata.CompletedAt.UnixNano()
	}
	if !metadata.CreatedAt.IsZero() {
		return metadata.CreatedAt.UnixNano()
	}
	return 0
}

// handleGetWorkflowsSummary returns lightweight summaries for multiple workflows in one call.
// GET /api/workflows/summary?workspace_paths=path1,path2,...
func (api *StreamingAPI) handleGetWorkflowsSummary(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	pathsParam := r.URL.Query().Get("workspace_paths")
	if pathsParam == "" {
		http.Error(w, "workspace_paths parameter is required (comma-separated)", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	workspacePaths := workspacePathsReadableBy(ctx, GetUserFromContext(ctx), strings.Split(pathsParam, ","))

	// Build active executions lookup from in-memory registry
	activeByWorkspace := map[string]string{} // workspace_path -> run_folder
	for _, exec := range api.listRunningWorkflowExecutions("") {
		activeByWorkspace[exec.WorkspacePath] = exec.RunFolder
	}

	// Fetch summaries in parallel with bounded concurrency
	summaries := make([]WorkflowSummary, len(workspacePaths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5) // max 5 workflows concurrently

	for i, wp := range workspacePaths {
		wg.Add(1)
		go func(idx int, workspacePath string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			summary := WorkflowSummary{WorkspacePath: workspacePath}

			// Check active executions (free — from memory)
			if runFolder, ok := activeByWorkspace[workspacePath]; ok {
				summary.IsRunning = true
				summary.ActiveRunFolder = runFolder
			}

			// List run folders (1 HTTP call to workspace API)
			folders, err := api.listRunFolderNames(ctx, workspacePath)
			if err != nil || len(folders) == 0 {
				summaries[idx] = summary
				return
			}
			summary.TotalRuns = len(folders)

			latestFolder, metadata := selectWorkflowSummaryFolder(ctx, workspacePath, folders, summary.ActiveRunFolder)
			if latestFolder == "" {
				summaries[idx] = summary
				return
			}

			run := &RunSummary{Folder: latestFolder}
			if metadata != nil {
				run.Status = metadata.Status
				if !metadata.CreatedAt.IsZero() {
					run.CreatedAt = metadata.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
				}
				if metadata.CompletedAt != nil {
					formatted := metadata.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
					run.CompletedAt = &formatted
				}
			}

			// Reconcile status with active executions
			if summary.IsRunning && summary.ActiveRunFolder == latestFolder {
				run.Status = "running"
			} else if run.Status == "running" {
				// Metadata says running but not in active executions — stale
				run.Status = "unknown"
			}

			// Read progress for the latest folder (1 HTTP call)
			stepsPath := workspacePath + "/runs/" + latestFolder + "/execution/steps_done.json"
			progress, _ := readProgressForFolder(ctx, stepsPath)
			if progress != nil {
				run.CompletedSteps = len(progress.CompletedStepIndices)
				run.TotalSteps = progress.TotalSteps
				// If no metadata, infer status from progress
				if run.Status == "" || run.Status == "unknown" {
					if run.TotalSteps > 0 && run.CompletedSteps >= run.TotalSteps {
						run.Status = "completed"
					}
				}
			}

			if run.Status == "" {
				run.Status = "unknown"
			}

			summary.LatestRun = run
			summaries[idx] = summary
		}(i, wp)
	}

	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"workflows": summaries,
	})
}

// handleGetWorkflowsOverview returns richer overview rows for multiple workflows in one call.
// GET /api/workflows/overview?workspace_paths=path1,path2,...
func (api *StreamingAPI) handleGetWorkflowsOverview(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	pathsParam := r.URL.Query().Get("workspace_paths")
	if pathsParam == "" {
		http.Error(w, "workspace_paths parameter is required (comma-separated)", http.StatusBadRequest)
		return
	}

	rawPaths := strings.Split(pathsParam, ",")
	workspacePaths := make([]string, 0, len(rawPaths))
	for _, raw := range rawPaths {
		workspacePath := strings.TrimSpace(raw)
		if workspacePath == "" || strings.Contains(workspacePath, "..") {
			continue
		}
		workspacePaths = append(workspacePaths, workspacePath)
	}
	workspacePaths = workspacePathsReadableBy(r.Context(), GetUserFromContext(r.Context()), workspacePaths)

	activeByWorkspace := map[string]map[string]struct{}{}
	for _, exec := range api.listRunningWorkflowExecutions("") {
		if exec.WorkspacePath == "" || exec.RunFolder == "" {
			continue
		}
		if activeByWorkspace[exec.WorkspacePath] == nil {
			activeByWorkspace[exec.WorkspacePath] = map[string]struct{}{}
		}
		activeByWorkspace[exec.WorkspacePath][exec.RunFolder] = struct{}{}
	}

	ctx := r.Context()
	overviews := make([]WorkflowOverview, len(workspacePaths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)

	for i, wp := range workspacePaths {
		wg.Add(1)
		go func(idx int, workspacePath string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			overview := WorkflowOverview{WorkspacePath: workspacePath}
			activeRunFolders := activeByWorkspace[workspacePath]
			for activeRunFolder := range activeRunFolders {
				overview.ActiveRunPaths = append(overview.ActiveRunPaths, activeRunFolder)
			}
			sort.Strings(overview.ActiveRunPaths)

			folders, err := api.loadRunFoldersInternal(ctx, workspacePath)
			if err != nil {
				overview.Error = "failed to load run folders"
				overviews[idx] = overview
				return
			}
			overview.TotalRunCount = len(folders)

			costResp := loadWorkflowCosts(ctx, workspacePath)
			costByFolder := make(map[string]workflowRunCostEntry, len(costResp.Runs))
			for _, entry := range costResp.Runs {
				// Multiple immutable executions may have begun in iteration-0.
				// Prefer the latest record for the run-folder overview; detailed
				// cost APIs retain every execution separately.
				folder := entry.RunFolder
				if entry.ArchivedRunFolder != "" {
					folder = entry.ArchivedRunFolder
				}
				if current, exists := costByFolder[folder]; !exists || workflowRunCostEntryTime(entry).After(workflowRunCostEntryTime(current)) {
					costByFolder[folder] = entry
				}
			}

			details := make([]WorkflowOverviewRunFolderDetail, 0, len(folders))
			for _, folder := range folders {
				detail := WorkflowOverviewRunFolderDetail{
					Folder: folder,
					Status: "unknown",
				}

				if metadata := folder.Metadata; metadata != nil {
					detail.Status = metadata.Status
					if metadata.TriggeredBy != "" {
						triggeredBy := metadata.TriggeredBy
						detail.TriggeredBy = &triggeredBy
					}
					if metadata.Models != nil {
						detail.Models = metadata.Models
					}
					if !metadata.CreatedAt.IsZero() {
						startedAt := metadata.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
						detail.StartedAt = &startedAt
						detail.LastUpdated = &startedAt
					}
					if metadata.CompletedAt != nil && !metadata.CompletedAt.IsZero() {
						completedAt := metadata.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
						detail.CompletedAt = &completedAt
						detail.LastUpdated = &completedAt
					}
				}
				if detail.Status == "running" {
					if _, ok := activeRunFolders[folder.Name]; !ok {
						detail.Status = "failed"
					}
				}

				if folder.Progress != nil {
					detail.CompletedSteps = len(folder.Progress.CompletedStepIndices)
					detail.TotalSteps = folder.Progress.TotalSteps
				}

				if runCosts, ok := costByFolder[folder.Name]; ok && runCosts.TokenUsage != nil {
					totalCost := orchestrator.TokenUsageTotalCostUSD(runCosts.TokenUsage)
					if totalCost > 0 {
						detail.CostUSD = &totalCost
					}
					if detail.StartedAt == nil && !runCosts.TokenUsage.CreatedAt.IsZero() {
						startedAt := runCosts.TokenUsage.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
						detail.StartedAt = &startedAt
						detail.LastUpdated = &startedAt
					}
					if detail.CompletedAt == nil && detail.Status == "completed" && !runCosts.TokenUsage.UpdatedAt.IsZero() {
						completedAt := runCosts.TokenUsage.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
						detail.CompletedAt = &completedAt
						detail.LastUpdated = &completedAt
					}
				}

				details = append(details, detail)
				if detail.LastUpdated != nil && (overview.LastUpdated == nil || *detail.LastUpdated > *overview.LastUpdated) {
					lastUpdated := *detail.LastUpdated
					overview.LastUpdated = &lastUpdated
				}
			}

			sort.Slice(details, func(i, j int) bool {
				a := ""
				b := ""
				if details[i].StartedAt != nil {
					a = *details[i].StartedAt
				} else if details[i].LastUpdated != nil {
					a = *details[i].LastUpdated
				}
				if details[j].StartedAt != nil {
					b = *details[j].StartedAt
				} else if details[j].LastUpdated != nil {
					b = *details[j].LastUpdated
				}
				if a == "" && b == "" {
					return details[i].Folder.Name < details[j].Folder.Name
				}
				if a == "" {
					return false
				}
				if b == "" {
					return true
				}
				return a > b
			})

			overview.RunFolders = details
			overviews[idx] = overview
		}(i, wp)
	}

	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"workflows": overviews,
	})
}

// listRunFolderNames returns just the folder names under /runs/ for a workspace.
// Much cheaper than getRunFoldersFromWorkspace — no metadata reads.
func (api *StreamingAPI) listRunFolderNames(ctx context.Context, workspacePath string) ([]string, error) {
	runsPath := workspacePath + "/runs"
	apiURL := getWorkspaceAPIURL() + "/api/documents"
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	q := req.URL.Query()
	q.Add("folder", runsPath)
	q.Add("max_depth", "2")
	req.URL.RawQuery = q.Encode()

	resp, err := workspaceHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("workspace API returned status %d", resp.StatusCode)
	}

	var apiResp virtualtools.WorkspaceAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}
	if !apiResp.Success {
		return nil, nil
	}

	// Extract folder names using the same logic as getRunFoldersFromWorkspace
	existingFolders := []string{}
	var folderListing virtualtools.WorkspaceFolderListing
	if dataBytes, err := json.Marshal(apiResp.Data); err == nil {
		if err := json.Unmarshal(dataBytes, &folderListing); err == nil && len(folderListing) > 0 {
			if len(folderListing[0].Children) > 0 {
				existingFolders = extractIterationFoldersFromTypedChildren(folderListing[0].Children, existingFolders)
			}
		} else {
			if dataArray, ok := apiResp.Data.([]interface{}); ok {
				existingFolders = extractIterationFoldersFromInterfaceArray(dataArray, existingFolders)
			} else if dataMap, ok := apiResp.Data.(map[string]interface{}); ok {
				if children, ok := dataMap["children"].([]interface{}); ok {
					existingFolders = extractIterationFoldersFromChildren(children, existingFolders)
				}
			}
		}
	}

	return existingFolders, nil
}
