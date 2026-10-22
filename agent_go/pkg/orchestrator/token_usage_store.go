package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type tokenUsageFileStore struct {
	workspacePath string
	readFile      func(context.Context, string) (string, error)
	listFiles     func(context.Context, string) ([]string, error)
	writeFile     func(context.Context, string, string) error
	deleteFile    func(context.Context, string) error
	warnf         func(string)
	now           func() time.Time
}

func newBaseOrchestratorTokenUsageStore(bo *BaseOrchestrator) *tokenUsageFileStore {
	return &tokenUsageFileStore{
		workspacePath: bo.GetWorkspacePath(),
		readFile:      bo.ReadWorkspaceFile,
		listFiles:     bo.ListWorkspaceFiles,
		writeFile:     bo.WriteWorkspaceFile,
		deleteFile:    bo.DeleteWorkspaceFile,
		warnf: func(msg string) {
			bo.GetLogger().Warn(msg)
		},
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func emptyTokenUsageFile() *TokenUsageFile {
	return &TokenUsageFile{
		ByModel:        make(map[string]*ModelTokenUsage),
		ByStepAndModel: make(map[string]map[string]*ModelTokenUsage),
	}
}

func (s *tokenUsageFileStore) parseDailyGroupTokenUsageFile(content string) (*DailyGroupTokenUsageFile, error) {
	var dailyFile DailyGroupTokenUsageFile
	if err := json.Unmarshal([]byte(content), &dailyFile); err != nil {
		return nil, err
	}
	if dailyFile.RunFolders == nil {
		dailyFile.RunFolders = make(map[string]*TokenUsageFile)
	}
	if dailyFile.Executions == nil {
		dailyFile.Executions = make(map[string]*ExecutionTokenUsage)
	}
	EnsureDailyGroupTokenUsageFilePricing(&dailyFile)
	return &dailyFile, nil
}

func (s *tokenUsageFileStore) parsePhaseTokenUsageFile(content string) (*PhaseTokenUsageFile, error) {
	var tokenFile PhaseTokenUsageFile
	if err := json.Unmarshal([]byte(content), &tokenFile); err != nil {
		return nil, err
	}
	if tokenFile.ByPhaseAndModel == nil {
		tokenFile.ByPhaseAndModel = make(map[string]map[string]*ModelTokenUsage)
	}
	if tokenFile.ByModel == nil {
		tokenFile.ByModel = make(map[string]*ModelTokenUsage)
	}
	EnsurePhaseTokenUsageFilePricing(&tokenFile)
	return &tokenFile, nil
}

func (s *tokenUsageFileStore) parseDailyPhaseTokenUsageFile(content string) (*DailyPhaseTokenUsageFile, error) {
	var dailyFile DailyPhaseTokenUsageFile
	if err := json.Unmarshal([]byte(content), &dailyFile); err != nil {
		return nil, err
	}
	if dailyFile.TokenUsage == nil {
		dailyFile.TokenUsage = &PhaseTokenUsageFile{
			ByPhaseAndModel: make(map[string]map[string]*ModelTokenUsage),
			ByModel:         make(map[string]*ModelTokenUsage),
		}
	}
	if dailyFile.TokenUsage.ByPhaseAndModel == nil {
		dailyFile.TokenUsage.ByPhaseAndModel = make(map[string]map[string]*ModelTokenUsage)
	}
	if dailyFile.TokenUsage.ByModel == nil {
		dailyFile.TokenUsage.ByModel = make(map[string]*ModelTokenUsage)
	}
	EnsureDailyPhaseTokenUsageFilePricing(&dailyFile)
	return &dailyFile, nil
}

func (s *tokenUsageFileStore) readRun(ctx context.Context, iterationFolder string) *TokenUsageFile {
	if iterationFolder == "" {
		return &TokenUsageFile{
			ByModel:        make(map[string]*ModelTokenUsage),
			ByStepAndModel: make(map[string]map[string]*ModelTokenUsage),
		}
	}

	scope, runFolder := NormalizeCostScopeAndRunFolder(iterationFolder)
	filePath := ResolveDailyGroupTokenUsagePath(s.workspacePath, scope, runFolder, s.now())
	content, err := s.readFile(ctx, filePath)
	if err != nil || content == "" {
		return &TokenUsageFile{
			ByModel:        make(map[string]*ModelTokenUsage),
			ByStepAndModel: make(map[string]map[string]*ModelTokenUsage),
		}
	}

	dailyFile, err := s.parseDailyGroupTokenUsageFile(content)
	if err != nil {
		return &TokenUsageFile{
			ByModel:        make(map[string]*ModelTokenUsage),
			ByStepAndModel: make(map[string]map[string]*ModelTokenUsage),
		}
	}

	if tokenFile := dailyTokenUsageForRunFolder(dailyFile, runFolder, false); tokenFile != nil {
		return tokenFile
	}

	return &TokenUsageFile{
		ByModel:        make(map[string]*ModelTokenUsage),
		ByStepAndModel: make(map[string]map[string]*ModelTokenUsage),
	}
}

// readRunAcrossDates resolves a run from the authoritative daily execution
// ledger. A run can cross midnight, so no single date shard is authoritative.
// Each shard owns a disjoint time interval; merging its run projection once
// preserves both aggregate and per-step views without adding those views
// together.
func (s *tokenUsageFileStore) readRunAcrossDates(ctx context.Context, iterationFolder string) *TokenUsageFile {
	if strings.TrimSpace(iterationFolder) == "" || s.listFiles == nil {
		return emptyTokenUsageFile()
	}

	scope, runFolder := NormalizeCostScopeAndRunFolder(iterationFolder)
	groupFolders := []string{ExtractGroupFolderFromRunFolder(runFolder)}
	matchGroupedChildren := !strings.Contains(strings.Trim(runFolder, "/"), "/")
	if matchGroupedChildren {
		// A scheduler normally knows only the iteration (for example
		// "iteration-0") while the authoritative ledgers are sharded by the
		// producing group (for example dev/ and production/). Looking only in
		// __ungrouped__ makes a perfectly valid scheduled run appear to have no
		// cost evidence. Discover every group shard and merge only child run keys
		// belonging to the requested iteration.
		root := filepath.Join(s.workspacePath, "costs", string(scope))
		if names, err := s.listFiles(ctx, root); err == nil {
			seen := map[string]bool{groupFolders[0]: true}
			for _, name := range names {
				name = filepath.Base(strings.TrimSpace(name))
				if name == "" || filepath.Ext(name) == ".json" || seen[name] {
					continue
				}
				seen[name] = true
				groupFolders = append(groupFolders, name)
			}
			sort.Strings(groupFolders)
		}
	}
	var merged *TokenUsageFile
	for _, groupFolder := range groupFolders {
		dirPath := filepath.Join(s.workspacePath, "costs", string(scope), groupFolder)
		names, err := s.listFiles(ctx, dirPath)
		if err != nil {
			continue
		}
		sort.Strings(names)
		for _, name := range names {
			name = filepath.Base(strings.TrimSpace(name))
			if filepath.Ext(name) != ".json" {
				continue
			}
			content, readErr := s.readFile(ctx, filepath.Join(dirPath, name))
			if readErr != nil || strings.TrimSpace(content) == "" {
				continue
			}
			daily, parseErr := s.parseDailyGroupTokenUsageFile(content)
			if parseErr != nil {
				s.warnf(fmt.Sprintf("⚠️ Failed to parse daily token usage file %s: %v", filepath.Join(dirPath, name), parseErr))
				continue
			}
			matchedCanonical := false
			for _, execution := range daily.Executions {
				if execution == nil || execution.TokenUsage == nil {
					continue
				}
				storedRunFolder := execution.EffectiveRunFolder()
				matches := storedRunFolder == runFolder
				if matchGroupedChildren && strings.HasPrefix(storedRunFolder, strings.TrimRight(runFolder, "/")+"/") {
					matches = true
				}
				if matches {
					matchedCanonical = true
					merged = MergeTokenUsageFiles(merged, execution.TokenUsage)
				}
			}
			// Use a shard's v1 projection only when it has no v2 record for this
			// requested run, avoiding double-counting while still preserving an
			// unrelated historical run in a mixed migration-day shard.
			if !matchedCanonical {
				for storedRunFolder, tokenFile := range daily.RunFolders {
					matches := storedRunFolder == runFolder
					if matchGroupedChildren && strings.HasPrefix(storedRunFolder, strings.TrimRight(runFolder, "/")+"/") {
						matches = true
					}
					if matches {
						merged = MergeTokenUsageFiles(merged, tokenFile)
					}
				}
			}
		}
	}
	if merged == nil {
		return emptyTokenUsageFile()
	}
	return merged
}

func dailyTokenUsageForRunFolder(daily *DailyGroupTokenUsageFile, runFolder string, matchGroupedChildren bool) *TokenUsageFile {
	if daily == nil {
		return nil
	}
	runFolder = strings.Trim(filepath.ToSlash(filepath.Clean(strings.TrimSpace(runFolder))), "/")
	var merged *TokenUsageFile
	for _, execution := range daily.Executions {
		if execution == nil || execution.TokenUsage == nil {
			continue
		}
		storedRunFolder := execution.EffectiveRunFolder()
		matches := storedRunFolder == runFolder
		if matchGroupedChildren && strings.HasPrefix(storedRunFolder, strings.TrimRight(runFolder, "/")+"/") {
			matches = true
		}
		if matches {
			merged = MergeTokenUsageFiles(merged, execution.TokenUsage)
		}
	}
	if merged != nil {
		return merged
	}
	return CloneTokenUsageFile(daily.RunFolders[runFolder])
}

// ArchiveRunCostPaths updates only the display path of execution-keyed cost
// records after the workflow rotates iteration-0 to an immutable backup
// folder. The execution ID and its token totals are never moved or merged.
// Legacy run_folders projections are intentionally left untouched.
func (bo *BaseOrchestrator) ArchiveRunCostPaths(ctx context.Context, fromRunFolder, toRunFolder string) error {
	tokenFileMutex.Lock()
	defer tokenFileMutex.Unlock()
	return newBaseOrchestratorTokenUsageStore(bo).archiveRunCostPaths(ctx, fromRunFolder, toRunFolder)
}

func (s *tokenUsageFileStore) archiveRunCostPaths(ctx context.Context, fromRunFolder, toRunFolder string) error {
	fromRunFolder = strings.Trim(filepath.ToSlash(filepath.Clean(strings.TrimSpace(fromRunFolder))), "/")
	toRunFolder = strings.Trim(filepath.ToSlash(filepath.Clean(strings.TrimSpace(toRunFolder))), "/")
	if fromRunFolder == "" || fromRunFolder == "." || toRunFolder == "" || toRunFolder == "." || fromRunFolder == toRunFolder {
		return nil
	}

	for _, scope := range []CostScope{CostScopeExecution} {
		root := filepath.Join(s.workspacePath, "costs", string(scope))
		groups, err := s.listFiles(ctx, root)
		if err != nil {
			continue // no ledger exists for this scope yet
		}
		for _, group := range groups {
			group = filepath.Base(strings.TrimSpace(group))
			if group == "" {
				continue
			}
			dir := filepath.Join(root, group)
			files, err := s.listFiles(ctx, dir)
			if err != nil {
				continue
			}
			for _, name := range files {
				name = filepath.Base(strings.TrimSpace(name))
				if filepath.Ext(name) != ".json" {
					continue
				}
				path := filepath.Join(dir, name)
				content, err := s.readFile(ctx, path)
				if err != nil || strings.TrimSpace(content) == "" {
					continue
				}
				daily, err := s.parseDailyGroupTokenUsageFile(content)
				if err != nil {
					continue
				}
				changed := false
				for _, execution := range daily.Executions {
					if execution == nil || strings.TrimSpace(execution.ArchivedRunFolder) != "" {
						continue
					}
					if archivedPathForRunFolder(execution.RunFolder, fromRunFolder, toRunFolder) != "" {
						execution.ArchivedRunFolder = archivedPathForRunFolder(execution.RunFolder, fromRunFolder, toRunFolder)
						changed = true
					}
				}
				if !changed {
					continue
				}
				encoded, err := json.MarshalIndent(daily, "", "  ")
				if err != nil {
					return fmt.Errorf("marshal archived cost ledger %s: %w", path, err)
				}
				if err := s.writeFile(ctx, path, string(encoded)); err != nil {
					return fmt.Errorf("update archived cost ledger %s: %w", path, err)
				}
			}
		}
	}
	return nil
}

func archivedPathForRunFolder(runFolder, fromRunFolder, toRunFolder string) string {
	runFolder = strings.Trim(filepath.ToSlash(filepath.Clean(strings.TrimSpace(runFolder))), "/")
	if runFolder == fromRunFolder {
		return toRunFolder
	}
	if strings.HasPrefix(runFolder, fromRunFolder+"/") {
		return toRunFolder + strings.TrimPrefix(runFolder, fromRunFolder)
	}
	return ""
}

func (s *tokenUsageFileStore) readPhase(ctx context.Context) *PhaseTokenUsageFile {

	filePath := ResolvePhaseTokenUsagePath(s.workspacePath)
	content, err := s.readFile(ctx, filePath)
	if err != nil || content == "" {
		return &PhaseTokenUsageFile{
			ByPhaseAndModel: make(map[string]map[string]*ModelTokenUsage),
			ByModel:         make(map[string]*ModelTokenUsage),
		}
	}

	tokenFile, err := s.parsePhaseTokenUsageFile(content)
	if err != nil {
		return &PhaseTokenUsageFile{
			ByPhaseAndModel: make(map[string]map[string]*ModelTokenUsage),
			ByModel:         make(map[string]*ModelTokenUsage),
		}
	}
	return tokenFile
}

func (s *tokenUsageFileStore) readPhaseDaily(ctx context.Context, ts time.Time) *DailyPhaseTokenUsageFile {
	filePath := ResolveDailyPhaseTokenUsagePath(s.workspacePath, ts)
	content, err := s.readFile(ctx, filePath)
	if err != nil || content == "" {
		return &DailyPhaseTokenUsageFile{
			Date:      CostDateKey(ts),
			UpdatedAt: s.now(),
			TokenUsage: &PhaseTokenUsageFile{
				ByPhaseAndModel: make(map[string]map[string]*ModelTokenUsage),
				ByModel:         make(map[string]*ModelTokenUsage),
			},
		}
	}

	dailyFile, err := s.parseDailyPhaseTokenUsageFile(content)
	if err != nil {
		return &DailyPhaseTokenUsageFile{
			Date:      CostDateKey(ts),
			UpdatedAt: s.now(),
			TokenUsage: &PhaseTokenUsageFile{
				ByPhaseAndModel: make(map[string]map[string]*ModelTokenUsage),
				ByModel:         make(map[string]*ModelTokenUsage),
			},
		}
	}
	return dailyFile
}
