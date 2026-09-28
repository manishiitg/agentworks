package virtualtools

import (
	"context"
	"encoding/json"
	"log"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

type toolCostScope string

const (
	toolCostScopeExecution toolCostScope = "execution"
)

type pricedToolCost struct {
	ToolName    string
	Capability  string
	Provider    string
	ModelID     string
	Unit        string
	Quantity    float64
	Count       int
	TotalCost   float64
	Estimated   bool
	OutputPaths []string
	Metadata    map[string]interface{}
}

type workflowCostTarget struct {
	WorkspacePath string
	Scope         toolCostScope
	RunFolder     string
	StepKey       string
}

type toolCostDailyGroupFile struct {
	Date        string                              `json:"date"`
	GroupFolder string                              `json:"group_folder"`
	UpdatedAt   time.Time                           `json:"updated_at"`
	Executions  map[string]*toolCostExecutionRecord `json:"executions,omitempty"`
	RunFolders  map[string]*toolCostTokenUsageFile  `json:"run_folders,omitempty"`
}

// toolCostExecutionRecord mirrors the workflow token-cost ledger's v2 shape
// without importing orchestrator (which would create an import cycle).
type toolCostExecutionRecord struct {
	RunFolder         string                  `json:"run_folder"`
	ArchivedRunFolder string                  `json:"archived_run_folder,omitempty"`
	TokenUsage        *toolCostTokenUsageFile `json:"token_usage"`
}

type toolCostTokenUsageFile struct {
	CreatedAt      time.Time                                     `json:"created_at"`
	UpdatedAt      time.Time                                     `json:"updated_at"`
	ByModel        map[string]json.RawMessage                    `json:"by_model"`
	ByStepAndModel map[string]map[string]json.RawMessage         `json:"by_step_and_model,omitempty"`
	ByTool         map[string]*toolCostUsageFileEntry            `json:"by_tool,omitempty"`
	ByStepAndTool  map[string]map[string]*toolCostUsageFileEntry `json:"by_step_and_tool,omitempty"`
}

type toolCostUsageFileEntry struct {
	ToolName    string                 `json:"tool_name"`
	Capability  string                 `json:"capability,omitempty"`
	Provider    string                 `json:"provider,omitempty"`
	ModelID     string                 `json:"model_id,omitempty"`
	Unit        string                 `json:"unit,omitempty"`
	Quantity    float64                `json:"quantity,omitempty"`
	Count       int                    `json:"count,omitempty"`
	TotalCost   float64                `json:"total_cost_usd,omitempty"`
	Estimated   bool                   `json:"estimated,omitempty"`
	OutputPaths []string               `json:"output_paths,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt   time.Time              `json:"created_at,omitempty"`
	UpdatedAt   time.Time              `json:"updated_at,omitempty"`
}

func recordPricedToolCost(ctx context.Context, workspaceAPIURL, userID string, cost pricedToolCost) {
	if cost.TotalCost <= 0 {
		return
	}

	now := time.Now().UTC()
	target := workflowCostTarget{}
	hasTarget := false
	for _, outputPath := range cost.OutputPaths {
		if candidate, ok := inferWorkflowCostTarget(ctx, outputPath); ok {
			target = candidate
			hasTarget = true
			break
		}
	}
	scope := "tool"
	if hasTarget {
		scope = "workflow_execution"
	}
	sessionID := ""
	executionID := ""
	if ctx != nil {
		sessionID, _ = ctx.Value(common.WorkflowSessionIDKey).(string)
		if strings.TrimSpace(sessionID) == "" {
			sessionID, _ = ctx.Value(common.ChatSessionIDKey).(string)
		}
		executionID, _ = ctx.Value(orchestratorevents.ParentExecutionIDKey).(string)
	}
	billingBasis := "provider_actual"
	if cost.Estimated {
		billingBasis = "token_estimate"
	}
	ledger := costledger.DefaultLedger()
	closeLedger := false
	if ledger == nil {
		var err error
		ledger, err = costledger.NewSQLiteLedger(filepath.Join(fsutil.WorkspaceDocsRoot(), "_system", "costs.sqlite"))
		if err != nil {
			log.Printf("[TOOL_COST] failed to open cost event database for %s: %v", cost.ToolName, err)
			return
		}
		closeLedger = true
	}
	if closeLedger {
		defer ledger.Close()
	}
	if err := ledger.Append(costledger.Entry{
		Timestamp:         now,
		UserID:            userID,
		WorkflowID:        target.WorkspacePath,
		RunID:             target.RunFolder,
		ExecutionID:       executionID,
		SessionID:         sessionID,
		SourcePlatform:    costobserver.SourcePlatformFromContext(ctx),
		Scope:             scope,
		Component:         "tool:" + cost.ToolName,
		Provider:          cost.Provider,
		ModelID:           cost.ModelID,
		TotalCostUSD:      cost.TotalCost,
		BillingBasis:      billingBasis,
		PricingSource:     "tool_price_registry",
		ToolName:          cost.ToolName,
		OperationMetadata: cost.Metadata,
	}); err != nil {
		log.Printf("[TOOL_COST] failed to append global cost ledger entry for %s: %v", cost.ToolName, err)
	}

	if strings.TrimSpace(workspaceAPIURL) == "" || !hasTarget {
		return
	}

	wsClient := workspace.NewClient(workspaceAPIURL, workspace.WithUserID(userID))
	filePath := resolveToolCostDailyGroupPath(target.WorkspacePath, target.Scope, target.RunFolder, now)
	dailyFile := &toolCostDailyGroupFile{
		Date:        toolCostDateKey(now),
		GroupFolder: extractToolCostGroupFolder(target.RunFolder),
		UpdatedAt:   now,
		Executions:  make(map[string]*toolCostExecutionRecord),
		RunFolders:  make(map[string]*toolCostTokenUsageFile),
	}
	if existing, err := wsClient.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: filePath}); err == nil && strings.TrimSpace(existing.Content) != "" {
		if err := json.Unmarshal([]byte(existing.Content), dailyFile); err == nil {
			if dailyFile.RunFolders == nil {
				dailyFile.RunFolders = make(map[string]*toolCostTokenUsageFile)
			}
			if dailyFile.Executions == nil {
				dailyFile.Executions = make(map[string]*toolCostExecutionRecord)
			}
		}
	}
	if dailyFile.RunFolders == nil {
		dailyFile.RunFolders = make(map[string]*toolCostTokenUsageFile)
	}
	if dailyFile.Executions == nil {
		dailyFile.Executions = make(map[string]*toolCostExecutionRecord)
	}

	var executionRecord *toolCostExecutionRecord
	var tokenFile *toolCostTokenUsageFile
	if strings.TrimSpace(executionID) != "" {
		executionRecord = cloneToolCostExecutionRecord(dailyFile.Executions[executionID])
		if executionRecord == nil {
			executionRecord = &toolCostExecutionRecord{RunFolder: target.RunFolder}
		}
		if executionRecord.RunFolder == "" {
			executionRecord.RunFolder = target.RunFolder
		}
		tokenFile = cloneToolCostTokenUsageFile(executionRecord.TokenUsage)
	} else {
		tokenFile = cloneToolCostTokenUsageFile(dailyFile.RunFolders[target.RunFolder])
	}
	if tokenFile == nil {
		tokenFile = &toolCostTokenUsageFile{}
	}
	toolUsage := &toolCostUsageFileEntry{
		ToolName:    cost.ToolName,
		Capability:  cost.Capability,
		Provider:    cost.Provider,
		ModelID:     cost.ModelID,
		Unit:        cost.Unit,
		Quantity:    cost.Quantity,
		Count:       cost.Count,
		TotalCost:   cost.TotalCost,
		Estimated:   cost.Estimated,
		OutputPaths: append([]string(nil), cost.OutputPaths...),
		Metadata:    cost.Metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	aggregateKey := toolCostAggregateKey(cost.ToolName, cost.Provider, cost.ModelID)
	applyToolCostUsage(tokenFile, target.StepKey, aggregateKey, toolUsage, now)
	if strings.TrimSpace(executionID) != "" {
		executionRecord.TokenUsage = tokenFile
		dailyFile.Executions[executionID] = executionRecord
	} else {
		dailyFile.RunFolders[target.RunFolder] = tokenFile
	}
	dailyFile.Date = toolCostDateKey(now)
	dailyFile.GroupFolder = extractToolCostGroupFolder(target.RunFolder)
	dailyFile.UpdatedAt = now

	encoded, err := json.MarshalIndent(dailyFile, "", "  ")
	if err != nil {
		log.Printf("[TOOL_COST] failed to encode workflow cost file %s: %v", filePath, err)
		return
	}
	if _, err := wsClient.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: filePath, Content: string(encoded)}); err != nil {
		log.Printf("[TOOL_COST] failed to write workflow cost file %s: %v", filePath, err)
	}
}

func toolCostAggregateKey(toolName, provider, modelID string) string {
	parts := []string{strings.TrimSpace(toolName), strings.TrimSpace(provider), strings.TrimSpace(modelID)}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, ":")
}

func toolCostDateKey(ts time.Time) string {
	return ts.UTC().Format("2006-01-02")
}

func extractToolCostGroupFolder(runFolder string) string {
	cleaned := strings.Trim(path.Clean(strings.TrimSpace(runFolder)), "/")
	if cleaned == "" || cleaned == "." {
		return "__ungrouped__"
	}
	parts := strings.Split(cleaned, "/")
	if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
		return strings.TrimSpace(parts[1])
	}
	return "__ungrouped__"
}

func resolveToolCostDailyGroupPath(workspacePath string, scope toolCostScope, runFolder string, ts time.Time) string {
	return path.Join(workspacePath, "costs", string(scope), extractToolCostGroupFolder(runFolder), toolCostDateKey(ts)+".json")
}

func cloneToolCostTokenUsageFile(src *toolCostTokenUsageFile) *toolCostTokenUsageFile {
	if src == nil {
		return nil
	}
	clone := &toolCostTokenUsageFile{
		CreatedAt:      src.CreatedAt,
		UpdatedAt:      src.UpdatedAt,
		ByModel:        cloneRawMessageMap(src.ByModel),
		ByStepAndModel: cloneNestedRawMessageMap(src.ByStepAndModel),
		ByTool:         make(map[string]*toolCostUsageFileEntry),
		ByStepAndTool:  make(map[string]map[string]*toolCostUsageFileEntry),
	}
	for key, usage := range src.ByTool {
		clone.ByTool[key] = cloneToolCostUsageFileEntry(usage)
	}
	for stepKey, toolMap := range src.ByStepAndTool {
		clone.ByStepAndTool[stepKey] = make(map[string]*toolCostUsageFileEntry)
		for key, usage := range toolMap {
			clone.ByStepAndTool[stepKey][key] = cloneToolCostUsageFileEntry(usage)
		}
	}
	return clone
}

func cloneToolCostExecutionRecord(src *toolCostExecutionRecord) *toolCostExecutionRecord {
	if src == nil {
		return nil
	}
	return &toolCostExecutionRecord{
		RunFolder:         src.RunFolder,
		ArchivedRunFolder: src.ArchivedRunFolder,
		TokenUsage:        cloneToolCostTokenUsageFile(src.TokenUsage),
	}
}

func cloneRawMessageMap(src map[string]json.RawMessage) map[string]json.RawMessage {
	if src == nil {
		return nil
	}
	clone := make(map[string]json.RawMessage, len(src))
	for key, value := range src {
		clone[key] = append(json.RawMessage(nil), value...)
	}
	return clone
}

func cloneNestedRawMessageMap(src map[string]map[string]json.RawMessage) map[string]map[string]json.RawMessage {
	if src == nil {
		return nil
	}
	clone := make(map[string]map[string]json.RawMessage, len(src))
	for key, nested := range src {
		clone[key] = cloneRawMessageMap(nested)
	}
	return clone
}

func cloneToolCostUsageFileEntry(src *toolCostUsageFileEntry) *toolCostUsageFileEntry {
	if src == nil {
		return nil
	}
	clone := *src
	clone.OutputPaths = append([]string(nil), src.OutputPaths...)
	if src.Metadata != nil {
		clone.Metadata = make(map[string]interface{}, len(src.Metadata))
		for key, value := range src.Metadata {
			clone.Metadata[key] = value
		}
	}
	return &clone
}

func applyToolCostUsage(tokenFile *toolCostTokenUsageFile, stepKey, aggregateKey string, usage *toolCostUsageFileEntry, now time.Time) {
	if tokenFile == nil || usage == nil || usage.TotalCost <= 0 {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if tokenFile.CreatedAt.IsZero() {
		tokenFile.CreatedAt = now
	}
	tokenFile.UpdatedAt = now
	if tokenFile.ByModel == nil {
		tokenFile.ByModel = make(map[string]json.RawMessage)
	}
	if usage.CreatedAt.IsZero() {
		usage.CreatedAt = now
	}
	if usage.UpdatedAt.IsZero() {
		usage.UpdatedAt = now
	}
	if tokenFile.ByTool == nil {
		tokenFile.ByTool = make(map[string]*toolCostUsageFileEntry)
	}
	if aggregateKey == "" {
		aggregateKey = toolCostAggregateKey(usage.ToolName, usage.Provider, usage.ModelID)
	}
	tokenFile.ByTool[aggregateKey] = mergeToolCostUsageFileEntry(tokenFile.ByTool[aggregateKey], usage)
	if stepKey == "" {
		return
	}
	if tokenFile.ByStepAndTool == nil {
		tokenFile.ByStepAndTool = make(map[string]map[string]*toolCostUsageFileEntry)
	}
	if tokenFile.ByStepAndTool[stepKey] == nil {
		tokenFile.ByStepAndTool[stepKey] = make(map[string]*toolCostUsageFileEntry)
	}
	tokenFile.ByStepAndTool[stepKey][aggregateKey] = mergeToolCostUsageFileEntry(tokenFile.ByStepAndTool[stepKey][aggregateKey], usage)
}

func mergeToolCostUsageFileEntry(dst, src *toolCostUsageFileEntry) *toolCostUsageFileEntry {
	if src == nil {
		return dst
	}
	if dst == nil {
		return cloneToolCostUsageFileEntry(src)
	}
	dst.Quantity += src.Quantity
	dst.Count += src.Count
	dst.TotalCost += src.TotalCost
	dst.OutputPaths = append(dst.OutputPaths, src.OutputPaths...)
	dst.Estimated = dst.Estimated || src.Estimated
	if src.ToolName != "" {
		dst.ToolName = src.ToolName
	}
	if src.Capability != "" {
		dst.Capability = src.Capability
	}
	if src.Provider != "" {
		dst.Provider = src.Provider
	}
	if src.ModelID != "" {
		dst.ModelID = src.ModelID
	}
	if src.Unit != "" {
		dst.Unit = src.Unit
	}
	if dst.CreatedAt.IsZero() || (!src.CreatedAt.IsZero() && src.CreatedAt.Before(dst.CreatedAt)) {
		dst.CreatedAt = src.CreatedAt
	}
	if src.UpdatedAt.After(dst.UpdatedAt) {
		dst.UpdatedAt = src.UpdatedAt
	}
	if src.Metadata != nil {
		if dst.Metadata == nil {
			dst.Metadata = make(map[string]interface{}, len(src.Metadata))
		}
		for key, value := range src.Metadata {
			dst.Metadata[key] = value
		}
	}
	return dst
}

func inferWorkflowCostTarget(ctx context.Context, outputPath string) (workflowCostTarget, bool) {
	cleaned := strings.Trim(path.Clean(strings.TrimSpace(outputPath)), "/")
	if cleaned == "" || cleaned == "." {
		return workflowCostTarget{}, false
	}
	parts := strings.Split(cleaned, "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] != "Workflow" {
			continue
		}
		workspacePath := strings.Join(parts[:i+2], "/")
		remainder := parts[i+2:]
		if len(remainder) >= 2 && remainder[0] == "runs" {
			runFolder, stepKey := inferRunFolderAndStepKey(ctx, remainder[1:])
			if runFolder != "" {
				return workflowCostTarget{
					WorkspacePath: workspacePath,
					Scope:         toolCostScopeExecution,
					RunFolder:     runFolder,
					StepKey:       stepKey,
				}, true
			}
		}

	}
	return workflowCostTarget{}, false
}

func inferRunFolderAndStepKey(ctx context.Context, parts []string) (string, string) {
	if len(parts) == 0 {
		return "", ""
	}
	runLen := 1
	if len(parts) >= 2 && (strings.HasPrefix(parts[0], "iteration-") || strings.HasPrefix(parts[0], "run-")) {
		runLen = 2
	}
	if len(parts) < runLen {
		runLen = len(parts)
	}
	runFolder := strings.Join(parts[:runLen], "/")
	stepKey := inferStepKeyFromContext(ctx)
	if stepKey == "" {
		stepKey = inferStepKeyFromPath(parts[runLen:])
	}
	return runFolder, stepKey
}

func inferStepKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if raw, ok := ctx.Value("step_execution_path").(string); ok && strings.TrimSpace(raw) != "" {
		return inferStepKeyFromPath(strings.Split(strings.Trim(path.Clean(raw), "/"), "/"))
	}
	return ""
}

func inferStepKeyFromPath(parts []string) string {
	for i, part := range parts {
		lower := strings.ToLower(strings.TrimSpace(part))
		if lower == "" {
			continue
		}
		if strings.HasPrefix(lower, "step-") || strings.HasPrefix(lower, "step_") {
			return "execution:" + part
		}
		if lower == "steps" && i+1 < len(parts) && strings.TrimSpace(parts[i+1]) != "" {
			return "execution:" + parts[i+1]
		}
	}
	return ""
}
