package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

// normalizeWorkspacePath cleans and normalizes a workspace path for comparison.
func normalizeWorkspacePath(path string) string {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(cleaned))
}

func workspaceCostPath(workspacePath string, parts ...string) string {
	all := []string{normalizeWorkspacePath(workspacePath)}
	all = append(all, parts...)
	return pathpkg.Clean(pathpkg.Join(all...))
}

func listWorkspaceFilesRecursive(ctx context.Context, folderPath string) ([]string, error) {
	if stat, err := os.Stat(folderPath); err == nil && stat.IsDir() {
		filePaths := make([]string, 0)
		err = filepath.WalkDir(folderPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			filePaths = append(filePaths, filepath.ToSlash(path))
			return nil
		})
		if err == nil {
			sort.Strings(filePaths)
			return filePaths, nil
		}
	}

	listing, _, err := listWorkspaceFolder(ctx, folderPath, 100)
	if err != nil {
		return nil, err
	}
	var filePaths []string
	collectWorkspaceFilePaths(listing, &filePaths)
	// Some workspace backends return both a nested tree and repeated flattened
	// children. Cost readers merge every returned file, so duplicate paths here
	// double every token and dollar amount in the run explorer.
	seen := make(map[string]struct{}, len(filePaths))
	unique := make([]string, 0, len(filePaths))
	for _, filePath := range filePaths {
		filePath = filepath.ToSlash(filePath)
		if _, exists := seen[filePath]; exists {
			continue
		}
		seen[filePath] = struct{}{}
		unique = append(unique, filePath)
	}
	sort.Strings(unique)
	return unique, nil
}

func readPhaseTokenUsageFromCosts(ctx context.Context, workspacePath string) (*orchestrator.PhaseTokenUsageFile, error) {
	phasePath := workspaceCostPath(workspacePath, "costs", "phase", "token_usage.json")
	content, exists, err := readFileFromWorkspace(ctx, phasePath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	var tokenFile orchestrator.PhaseTokenUsageFile
	if err := json.Unmarshal([]byte(content), &tokenFile); err != nil {
		return nil, err
	}
	orchestrator.EnsurePhaseTokenUsageFilePricing(&tokenFile)
	return &tokenFile, nil
}

type storedRunTokenUsage struct {
	ExecutionID       string
	RunFolder         string
	ArchivedRunFolder string
	TokenUsage        *orchestrator.TokenUsageFile
}

func (r *storedRunTokenUsage) effectiveRunFolder() string {
	if r == nil {
		return ""
	}
	if strings.TrimSpace(r.ArchivedRunFolder) != "" {
		return r.ArchivedRunFolder
	}
	return r.RunFolder
}

// readAllRunTokenUsageFromCosts returns one aggregate per immutable execution
// ID. Legacy files without executions remain visible with a legacy:<path> key;
// they are never merged into a new UUID-keyed execution.
func readAllRunTokenUsageFromCosts(ctx context.Context, workspacePath string, scope orchestrator.CostScope) (map[string]*storedRunTokenUsage, error) {
	root := workspaceCostPath(workspacePath, "costs", string(scope))

	result := make(map[string]*storedRunTokenUsage)
	filePaths, err := listWorkspaceFilesRecursive(ctx, root)
	if err != nil {
		return nil, err
	}
	for _, filePath := range filePaths {
		if !strings.HasSuffix(filePath, ".json") {
			continue
		}

		content, exists, err := readFileFromWorkspace(ctx, filePath)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}

		var dailyFile orchestrator.DailyGroupTokenUsageFile
		if err := json.Unmarshal([]byte(content), &dailyFile); err != nil {
			continue
		}
		orchestrator.EnsureDailyGroupTokenUsageFilePricing(&dailyFile)

		for executionID, execution := range dailyFile.Executions {
			if execution == nil || execution.TokenUsage == nil || strings.TrimSpace(executionID) == "" {
				continue
			}
			entry := result[executionID]
			if entry == nil {
				entry = &storedRunTokenUsage{ExecutionID: executionID, RunFolder: execution.RunFolder, ArchivedRunFolder: execution.ArchivedRunFolder}
				result[executionID] = entry
			}
			if entry.RunFolder == "" {
				entry.RunFolder = execution.RunFolder
			}
			if execution.ArchivedRunFolder != "" {
				entry.ArchivedRunFolder = execution.ArchivedRunFolder
			}
			entry.TokenUsage = orchestrator.MergeTokenUsageFiles(entry.TokenUsage, execution.TokenUsage)
		}

		// run_folders is the v1 projection. It may be historically merged, so
		// keep it separate instead of letting it contaminate a UUID record.
		for storedRunFolder, tokenFile := range dailyFile.RunFolders {
			legacyKey := "legacy:" + storedRunFolder
			entry := result[legacyKey]
			if entry == nil {
				entry = &storedRunTokenUsage{ExecutionID: legacyKey, RunFolder: storedRunFolder}
				result[legacyKey] = entry
			}
			entry.TokenUsage = orchestrator.MergeTokenUsageFiles(entry.TokenUsage, tokenFile)
		}
	}

	return result, nil
}

type workflowRunCostEntry struct {
	ExecutionID       string                       `json:"execution_id,omitempty"`
	RunFolder         string                       `json:"run_folder"`
	ArchivedRunFolder string                       `json:"archived_run_folder,omitempty"`
	TokenUsage        *orchestrator.TokenUsageFile `json:"token_usage,omitempty"`
}

type workflowRunDailyCostEntry struct {
	Date              string                       `json:"date"`
	Scope             orchestrator.CostScope       `json:"scope"`
	ExecutionID       string                       `json:"execution_id,omitempty"`
	GroupFolder       string                       `json:"group_folder"`
	RunFolder         string                       `json:"run_folder"`
	ArchivedRunFolder string                       `json:"archived_run_folder,omitempty"`
	TokenUsage        *orchestrator.TokenUsageFile `json:"token_usage,omitempty"`
}

type workflowPhaseDailyCostEntry struct {
	Date       string                            `json:"date"`
	TokenUsage *orchestrator.PhaseTokenUsageFile `json:"token_usage,omitempty"`
}

func buildWorkflowRunCostEntries(executionCosts map[string]*storedRunTokenUsage) []workflowRunCostEntry {
	entries := make([]workflowRunCostEntry, 0, len(executionCosts))
	for _, cost := range executionCosts {
		if cost == nil {
			continue
		}
		entries = append(entries, workflowRunCostEntry{ExecutionID: cost.ExecutionID, RunFolder: cost.RunFolder, ArchivedRunFolder: cost.ArchivedRunFolder, TokenUsage: cost.TokenUsage})
	}

	sort.Slice(entries, func(i, j int) bool {
		iTime := workflowRunCostEntryTime(entries[i])
		jTime := workflowRunCostEntryTime(entries[j])
		if !iTime.Equal(jTime) {
			return jTime.Before(iTime)
		}
		if entries[i].RunFolder != entries[j].RunFolder {
			return entries[j].RunFolder < entries[i].RunFolder
		}
		return entries[j].ExecutionID < entries[i].ExecutionID
	})

	return entries
}

func workflowRunCostEntryTime(entry workflowRunCostEntry) time.Time {
	if entry.TokenUsage != nil {
		if !entry.TokenUsage.UpdatedAt.IsZero() {
			return entry.TokenUsage.UpdatedAt
		}
		if !entry.TokenUsage.CreatedAt.IsZero() {
			return entry.TokenUsage.CreatedAt
		}
	}
	return time.Time{}
}

func readAllPhaseTokenUsageFromCosts(ctx context.Context, workspacePath string) ([]workflowPhaseDailyCostEntry, error) {
	root := workspaceCostPath(workspacePath, "costs", "phase", "daily")

	entries := make([]workflowPhaseDailyCostEntry, 0)
	filePaths, err := listWorkspaceFilesRecursive(ctx, root)
	if err != nil {
		return nil, err
	}
	for _, filePath := range filePaths {
		if !strings.HasSuffix(filePath, ".json") {
			continue
		}

		content, exists, err := readFileFromWorkspace(ctx, filePath)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}

		var dailyFile orchestrator.DailyPhaseTokenUsageFile
		if err := json.Unmarshal([]byte(content), &dailyFile); err != nil {
			continue
		}
		orchestrator.EnsureDailyPhaseTokenUsageFilePricing(&dailyFile)
		if dailyFile.TokenUsage == nil {
			continue
		}

		date := strings.TrimSuffix(pathpkg.Base(filePath), pathpkg.Ext(filePath))
		if strings.TrimSpace(dailyFile.Date) != "" {
			date = dailyFile.Date
		}

		entries = append(entries, workflowPhaseDailyCostEntry{
			Date:       date,
			TokenUsage: dailyFile.TokenUsage,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Date != entries[j].Date {
			return entries[i].Date > entries[j].Date
		}
		iUpdated := time.Time{}
		jUpdated := time.Time{}
		if entries[i].TokenUsage != nil {
			iUpdated = entries[i].TokenUsage.UpdatedAt
		}
		if entries[j].TokenUsage != nil {
			jUpdated = entries[j].TokenUsage.UpdatedAt
		}
		return jUpdated.Before(iUpdated)
	})

	return entries, nil
}

func readAllRunDailyTokenUsageFromCosts(ctx context.Context, workspacePath string, scope orchestrator.CostScope) ([]workflowRunDailyCostEntry, error) {
	root := workspaceCostPath(workspacePath, "costs", string(scope))

	entries := make([]workflowRunDailyCostEntry, 0)
	filePaths, err := listWorkspaceFilesRecursive(ctx, root)
	if err != nil {
		return nil, err
	}
	for _, filePath := range filePaths {
		if !strings.HasSuffix(filePath, ".json") {
			continue
		}

		content, exists, err := readFileFromWorkspace(ctx, filePath)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}

		var dailyFile orchestrator.DailyGroupTokenUsageFile
		if err := json.Unmarshal([]byte(content), &dailyFile); err != nil {
			continue
		}
		orchestrator.EnsureDailyGroupTokenUsageFilePricing(&dailyFile)
		if len(dailyFile.RunFolders) == 0 && len(dailyFile.Executions) == 0 {
			continue
		}

		date := strings.TrimSuffix(pathpkg.Base(filePath), pathpkg.Ext(filePath))
		if strings.TrimSpace(dailyFile.Date) != "" {
			date = dailyFile.Date
		}
		groupFolder := strings.TrimSpace(dailyFile.GroupFolder)
		if groupFolder == "" {
			groupFolder = pathpkg.Base(pathpkg.Dir(filePath))
		}

		for executionID, execution := range dailyFile.Executions {
			if execution == nil || execution.TokenUsage == nil {
				continue
			}
			entries = append(entries, workflowRunDailyCostEntry{
				Date:              date,
				Scope:             scope,
				ExecutionID:       executionID,
				GroupFolder:       groupFolder,
				RunFolder:         execution.RunFolder,
				ArchivedRunFolder: execution.ArchivedRunFolder,
				TokenUsage:        execution.TokenUsage,
			})
		}

		for runFolder, tokenFile := range dailyFile.RunFolders {
			if tokenFile == nil {
				continue
			}
			entries = append(entries, workflowRunDailyCostEntry{
				Date:        date,
				Scope:       scope,
				ExecutionID: "legacy:" + runFolder,
				GroupFolder: groupFolder,
				RunFolder:   runFolder,
				TokenUsage:  tokenFile,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Date != entries[j].Date {
			return entries[i].Date > entries[j].Date
		}
		if entries[i].GroupFolder != entries[j].GroupFolder {
			return entries[i].GroupFolder < entries[j].GroupFolder
		}
		if entries[i].RunFolder != entries[j].RunFolder {
			return entries[i].RunFolder < entries[j].RunFolder
		}
		if entries[i].Scope != entries[j].Scope {
			return entries[i].Scope < entries[j].Scope
		}
		return entries[i].ExecutionID < entries[j].ExecutionID
	})

	return entries, nil
}
