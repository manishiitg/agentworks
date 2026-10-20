package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

type workflowCostsResponse struct {
	Success         bool                              `json:"success"`
	ScopedCosts     *costledger.Summary               `json:"scoped_costs,omitempty"`
	PhaseTokenUsage *orchestrator.PhaseTokenUsageFile `json:"phase_token_usage,omitempty"`
	PhaseDailyCosts []workflowPhaseDailyCostEntry     `json:"phase_daily_costs"`
	RunDailyCosts   []workflowRunDailyCostEntry       `json:"run_daily_costs"`
	Runs            []workflowRunCostEntry            `json:"runs"`
	ActivityTiming  workflowActivityTimingSummary     `json:"activity_timing"`
	History         *workflowCostHistoryPage          `json:"history,omitempty"`
}

type workflowCostHistoryPage struct {
	Days       int    `json:"days"`
	WindowFrom string `json:"window_from"`
	WindowTo   string `json:"window_to"`
	HasMore    bool   `json:"has_more"`
	NextBefore string `json:"next_before,omitempty"`
}

// workflowActivityTimingAggregate deliberately calls this agent time rather
// than wall time. It is the sum of elapsed time for the agents in a category;
// agents may overlap when a workflow runs work in parallel.
type workflowActivityTimingAggregate struct {
	DurationMS     int64                                       `json:"duration_ms"`
	LLMDurationMS  int64                                       `json:"llm_duration_ms"`
	ToolDurationMS int64                                       `json:"tool_duration_ms"`
	ByExecution    map[string]*workflowActivityTimingAggregate `json:"by_execution,omitempty"`
}

type workflowActivityTimingDate struct {
	ByScope map[string]*workflowActivityTimingAggregate `json:"by_scope"`
}

type workflowActivityTimingSummary struct {
	ByScope map[string]*workflowActivityTimingAggregate `json:"by_scope"`
	ByDate  map[string]*workflowActivityTimingDate      `json:"by_date"`
}

type persistedWorkflowTiming struct {
	StepID string `json:"step_id"`
	Agent  struct {
		StartedAt  string `json:"started_at"`
		DurationMS int64  `json:"duration_ms"`
	} `json:"agent"`
	Breakdown struct {
		WallDurationMS int64 `json:"wall_duration_ms"`
		LLMDurationMS  int64 `json:"llm_duration_ms"`
		ToolDurationMS int64 `json:"tool_duration_ms"`
	} `json:"breakdown"`
}

type StepOutputContent struct {
	FilePath string      `json:"file_path"`
	Content  interface{} `json:"content"`
	IsJSON   bool        `json:"is_json"`
}

type workflowReviewDataResponse struct {
	Success bool                  `json:"success"`
	Costs   workflowCostsResponse `json:"costs"`
}

func loadWorkflowCosts(ctx context.Context, workspacePath string) workflowCostsResponse {
	var scopedCosts *costledger.Summary
	if ledger := costledger.DefaultLedger(); ledger != nil {
		if summary, err := ledger.SummarizeWorkflow(workspacePath); err == nil {
			scopedCosts = summary
		}
	}
	var phaseTokenUsage *orchestrator.PhaseTokenUsageFile
	if phaseUsage, err := readPhaseTokenUsageFromCosts(ctx, workspacePath); err == nil {
		phaseTokenUsage = phaseUsage
	}

	phaseDailyCosts, err := readAllPhaseTokenUsageFromCosts(ctx, workspacePath)
	if err != nil {
		phaseDailyCosts = []workflowPhaseDailyCostEntry{}
	}

	executionCosts, err := readAllRunTokenUsageFromCosts(ctx, workspacePath, orchestrator.CostScopeExecution)
	if err != nil {
		executionCosts = map[string]*storedRunTokenUsage{}
	}

	runDailyCosts := readWorkflowRunDailyCosts(ctx, workspacePath)

	return workflowCostsResponse{
		Success:         true,
		ScopedCosts:     scopedCosts,
		PhaseTokenUsage: phaseTokenUsage,
		PhaseDailyCosts: phaseDailyCosts,
		RunDailyCosts:   runDailyCosts,
		Runs:            buildWorkflowRunCostEntries(executionCosts),
		ActivityTiming:  loadWorkflowActivityTiming(ctx, workspacePath),
	}
}

func loadWorkflowCostSummary(workspacePath string, days int, before string, now time.Time) (workflowCostsResponse, error) {
	if days <= 0 {
		days = 30
	}
	if days > 90 {
		days = 90
	}

	endDay := now.UTC()
	endDay = time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 0, 0, 0, 0, time.UTC)
	if strings.TrimSpace(before) != "" {
		parsed, err := time.Parse("2006-01-02", strings.TrimSpace(before))
		if err != nil {
			return workflowCostsResponse{}, fmt.Errorf("invalid before date %q (expected YYYY-MM-DD)", before)
		}
		endDay = parsed.UTC().AddDate(0, 0, -1)
	}
	startDay := endDay.AddDate(0, 0, -(days - 1))
	windowFrom := startDay.Format("2006-01-02")
	windowTo := endDay.Format("2006-01-02")

	response := workflowCostsResponse{
		Success:         true,
		PhaseDailyCosts: []workflowPhaseDailyCostEntry{},
		RunDailyCosts:   []workflowRunDailyCostEntry{},
		Runs:            []workflowRunCostEntry{},
		ActivityTiming:  newWorkflowActivityTimingSummary(),
		History: &workflowCostHistoryPage{
			Days:       days,
			WindowFrom: windowFrom,
			WindowTo:   windowTo,
		},
	}
	if ledger := costledger.DefaultLedger(); ledger != nil {
		summary, hasMore, err := ledger.SummarizeWorkflowOverview(workspacePath, windowFrom, windowTo)
		if err != nil {
			return workflowCostsResponse{}, err
		}
		response.ScopedCosts = summary
		response.History.HasMore = hasMore
		if hasMore {
			response.History.NextBefore = windowFrom
		}
	}
	return response, nil
}

func newWorkflowActivityTimingSummary() workflowActivityTimingSummary {
	return workflowActivityTimingSummary{
		ByScope: map[string]*workflowActivityTimingAggregate{},
		ByDate:  map[string]*workflowActivityTimingDate{},
	}
}

func addWorkflowActivityTiming(summary *workflowActivityTimingSummary, date, scope, executionID string, durationMS, llmDurationMS, toolDurationMS int64) {
	if summary == nil || strings.TrimSpace(scope) == "" || durationMS <= 0 {
		return
	}
	add := func(scopes map[string]*workflowActivityTimingAggregate) {
		aggregate := scopes[scope]
		if aggregate == nil {
			aggregate = &workflowActivityTimingAggregate{ByExecution: map[string]*workflowActivityTimingAggregate{}}
			scopes[scope] = aggregate
		}
		aggregate.DurationMS += durationMS
		aggregate.LLMDurationMS += llmDurationMS
		aggregate.ToolDurationMS += toolDurationMS
		if strings.TrimSpace(executionID) != "" {
			execution := aggregate.ByExecution[executionID]
			if execution == nil {
				execution = &workflowActivityTimingAggregate{}
				aggregate.ByExecution[executionID] = execution
			}
			execution.DurationMS += durationMS
			execution.LLMDurationMS += llmDurationMS
			execution.ToolDurationMS += toolDurationMS
		}
	}
	add(summary.ByScope)
	if date != "" {
		day := summary.ByDate[date]
		if day == nil {
			day = &workflowActivityTimingDate{ByScope: map[string]*workflowActivityTimingAggregate{}}
			summary.ByDate[date] = day
		}
		add(day.ByScope)
	}
}

func timingDate(value string) string {
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value)); err == nil {
		return parsed.UTC().Format("2006-01-02")
	}
	if len(value) >= 10 {
		return value[:10]
	}
	return ""
}

func loadWorkflowActivityTiming(ctx context.Context, workspacePath string) workflowActivityTimingSummary {
	summary := newWorkflowActivityTimingSummary()

	workspaceRoot := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workspacePath))
	loadWorkflowTimingFiles(&summary, filepath.Join(workspaceRoot, "runs"), "workflow_execution", "step:")
	return summary
}

func loadWorkflowTimingFiles(summary *workflowActivityTimingSummary, root, scope, executionPrefix string) {
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry == nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), "-timing.json") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var timing persistedWorkflowTiming
		if err := json.Unmarshal(content, &timing); err != nil || strings.TrimSpace(timing.StepID) == "" {
			return nil
		}
		durationMS := timing.Breakdown.WallDurationMS
		if durationMS <= 0 {
			durationMS = timing.Agent.DurationMS
		}
		date := timingDate(timing.Agent.StartedAt)
		if date == "" {
			if info, err := entry.Info(); err == nil {
				date = info.ModTime().UTC().Format("2006-01-02")
			}
		}
		addWorkflowActivityTiming(summary, date, scope, executionPrefix+timing.StepID, durationMS, timing.Breakdown.LLMDurationMS, timing.Breakdown.ToolDurationMS)
		return nil
	})
}

func readWorkflowRunDailyCosts(ctx context.Context, workspacePath string) []workflowRunDailyCostEntry {
	entries := make([]workflowRunDailyCostEntry, 0)
	if executionDailyCosts, err := readAllRunDailyTokenUsageFromCosts(ctx, workspacePath, orchestrator.CostScopeExecution); err == nil {
		entries = append(entries, executionDailyCosts...)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Date != entries[j].Date {
			return entries[i].Date > entries[j].Date
		}
		if entries[i].RunFolder != entries[j].RunFolder {
			return entries[i].RunFolder < entries[j].RunFolder
		}
		return entries[i].Scope < entries[j].Scope
	})
	return entries
}

func (api *StreamingAPI) handleGetWorkflowReviewData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspacePath := r.URL.Query().Get("workspace_path")
	if workspacePath == "" {
		http.Error(w, "workspace_path parameter is required", http.StatusBadRequest)
		return
	}

	cleanedWorkspacePath := filepath.Clean(workspacePath)
	if strings.Contains(cleanedWorkspacePath, "..") {
		http.Error(w, "Invalid workspace path", http.StatusBadRequest)
		return
	}

	response := workflowReviewDataResponse{
		Success: true,
		Costs:   loadWorkflowCosts(r.Context(), cleanedWorkspacePath),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
