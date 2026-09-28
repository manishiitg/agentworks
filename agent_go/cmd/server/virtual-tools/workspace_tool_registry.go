package virtualtools

import (
	"context"
	"github.com/manishiitg/mcpagent/llm"
	"strings"
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// WorkspaceToolRegistryConfig controls creation of the LLM-visible workspace
// tools that live under the workspace_advanced category.
type WorkspaceToolRegistryConfig struct {
	WorkspaceAPIURL      string
	UserID               string
	SessionID            string
	ExtraEnvVars         map[string]string
	GenerateTextLLMTiers *WorkflowLLMTierConfig
	// APIKeys are the turn's provider keys, carrying the server's account
	// resolver for the turn's scope.
	APIKeys *llm.ProviderAPIKeys
}

// WorkspaceToolRegistry is the single assembly point for LLM-visible workspace
// tools: definitions, executors, categories, and shell/MCP environment.
type WorkspaceToolRegistry struct {
	Tools      []llmtypes.Tool
	Executors  map[string]func(ctx context.Context, args map[string]any) (string, error)
	Categories map[string]string
	Env        map[string]string
}

var workspaceToolNamesCache = struct {
	sync.Once
	byCategory map[string]map[string]bool
}{}

// CreateWorkspaceToolRegistry returns the active workspace tool bundle. Basic
// workspace and git tools are intentionally excluded. image_gen/image_edit run
// through codex-cli.
func CreateWorkspaceToolRegistry(cfg WorkspaceToolRegistryConfig) WorkspaceToolRegistry {
	workspaceURL := strings.TrimSpace(cfg.WorkspaceAPIURL)
	if workspaceURL == "" {
		workspaceURL = getWorkspaceAPIURL()
	}

	tools := append([]llmtypes.Tool{}, CreateWorkspaceAdvancedTools()...)
	tools = append(tools, CreateWorkspaceImageTools()...)

	advancedExecutors, env := createWorkspaceAdvancedExecutorsForRegistry(cfg, workspaceURL)
	if cfg.APIKeys != nil {
		SetGenerateTextWorkflowTierConfig(advancedExecutors, workspaceURL, cfg.GenerateTextLLMTiers, cfg.APIKeys)
	} else {
		SetGenerateTextWorkflowTierConfig(advancedExecutors, workspaceURL, cfg.GenerateTextLLMTiers)
	}
	executors := make(map[string]func(ctx context.Context, args map[string]any) (string, error), len(advancedExecutors)+8)
	for name, executor := range advancedExecutors {
		executors[name] = executor
	}
	mediaCfg := ImageGenExecutorConfig{
		WorkspaceAPIURL: workspaceURL,
		UserID:          cfg.UserID,
	}
	MergeImageToolExecutors(mediaCfg, executors, nil)
	visible := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if tool.Function != nil {
			visible[tool.Function.Name] = true
		}
	}
	for name := range executors {
		if !visible[name] {
			delete(executors, name)
		}
	}

	categories := make(map[string]string, len(tools))
	category := GetWorkspaceAdvancedToolCategory()
	for _, tool := range tools {
		if tool.Function != nil {
			categories[tool.Function.Name] = category
		}
	}

	return WorkspaceToolRegistry{
		Tools:      tools,
		Executors:  executors,
		Categories: categories,
		Env:        env,
	}
}

func createWorkspaceAdvancedExecutorsForRegistry(cfg WorkspaceToolRegistryConfig, workspaceURL string) (map[string]func(ctx context.Context, args map[string]any) (string, error), map[string]string) {
	switch {
	case workspaceURL != "" && workspaceURL != getWorkspaceAPIURL():
		return CreateWorkspaceAdvancedToolExecutorsWithURL(workspaceURL, cfg.UserID, cfg.SessionID)
	case len(cfg.ExtraEnvVars) > 0:
		return CreateWorkspaceAdvancedToolExecutorsWithSessionAndEnv(cfg.UserID, cfg.SessionID, cfg.ExtraEnvVars)
	case strings.TrimSpace(cfg.SessionID) != "":
		return CreateWorkspaceAdvancedToolExecutorsWithSession(cfg.UserID, cfg.SessionID)
	case strings.TrimSpace(cfg.UserID) != "":
		return CreateWorkspaceAdvancedToolExecutorsWithUserID(cfg.UserID), nil
	default:
		return CreateWorkspaceAdvancedToolExecutors(), nil
	}
}

// CreateWorkspaceToolRegistryUntyped mirrors CreateWorkspaceToolRegistry for
// call sites that store executors as map[string]interface{}.
func CreateWorkspaceToolRegistryUntyped(cfg WorkspaceToolRegistryConfig) ([]llmtypes.Tool, map[string]interface{}, map[string]string, map[string]string) {
	registry := CreateWorkspaceToolRegistry(cfg)
	executors := make(map[string]interface{}, len(registry.Executors))
	for name, executor := range registry.Executors {
		executors[name] = executor
	}
	return registry.Tools, executors, registry.Categories, registry.Env
}

// WorkspaceToolNamesByCategory returns tool names from the central registry for
// category expansion. workspace_tools also includes browser tools for legacy
// compatibility.
func WorkspaceToolNamesByCategory(category string) map[string]bool {
	workspaceToolNamesCache.Do(initWorkspaceToolNamesCache)
	if names, ok := workspaceToolNamesCache.byCategory[category]; ok {
		return cloneWorkspaceToolNameSet(names)
	}
	return map[string]bool{}
}

func initWorkspaceToolNamesCache() {
	workspaceRegistryToolNames := make(map[string]bool)
	for toolName := range CreateWorkspaceToolRegistry(WorkspaceToolRegistryConfig{}).Executors {
		workspaceRegistryToolNames[toolName] = true
	}

	workspaceTools := cloneWorkspaceToolNameSet(workspaceRegistryToolNames)
	for toolName := range CreateWorkspaceBrowserToolExecutors() {
		workspaceTools[toolName] = true
	}

	imageTools := make(map[string]bool)
	for toolName := range CreateWorkspaceImageToolExecutors(ImageGenExecutorConfig{}) {
		imageTools[toolName] = true
	}

	workspaceToolNamesCache.byCategory = map[string]map[string]bool{
		"workspace_tools":                  workspaceTools,
		GetWorkspaceAdvancedToolCategory(): workspaceRegistryToolNames,
		"workspace_image":                  imageTools,
	}
}

func cloneWorkspaceToolNameSet(names map[string]bool) map[string]bool {
	toolNames := make(map[string]bool, len(names))
	for name, enabled := range names {
		toolNames[name] = enabled
	}
	return toolNames
}
