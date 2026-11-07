package step_based_workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// RegisterLazyWorkshopTools uses the native schemas without initializing a
// controller, credentials or connections until a tool is actually called.
// The caller's registrar still decides which of the native tools are admitted.
func RegisterLazyWorkshopTools(target DefinitionRegistrar, resolve func(context.Context) (*WorkshopChatSession, error), logger loggerv2.Logger) {
	schemaSession := &WorkshopChatSession{
		controller: &StepBasedWorkflowOrchestrator{BaseOrchestrator: &orchestrator.BaseOrchestrator{}},
		config:     &WorkshopConfig{},
	}
	lazy := &lazyWorkshopRegistrar{target: target, resolve: resolve, logger: logger}
	RegisterWorkshopChatTools(lazy, schemaSession, logger)
	RegisterRunFullWorkflowTool(lazy, schemaSession, logger)
}

type lazyWorkshopRegistrar struct {
	target  DefinitionRegistrar
	resolve func(context.Context) (*WorkshopChatSession, error)
	logger  loggerv2.Logger
}

func (r *lazyWorkshopRegistrar) AttachedSkills() []*llmtypes.Skill { return r.target.AttachedSkills() }
func (r *lazyWorkshopRegistrar) RegisterCustomTool(n, d string, schema map[string]interface{}, _ func(context.Context, map[string]interface{}) (string, error), group string) error {
	return r.RegisterCustomToolWithTimeout(n, d, schema, nil, 0, group)
}
func (r *lazyWorkshopRegistrar) RegisterCustomToolWithTimeout(n, d string, schema map[string]interface{}, _ func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, group string) error {
	return r.target.RegisterCustomToolWithTimeout(n, d, schema, func(ctx context.Context, args map[string]interface{}) (string, error) {
		session, err := r.resolve(ctx)
		if err != nil {
			return "", err
		}
		// Tool starts are short; the work itself runs in the existing background
		// registry. Serialize selection/start across replacement chat definitions.
		session.toolCallMu.Lock()
		defer session.toolCallMu.Unlock()
		capture := &workshopToolCapture{name: n}
		RegisterWorkshopChatTools(capture, session, r.logger)
		RegisterRunFullWorkflowTool(capture, session, r.logger)
		if capture.execute == nil {
			return "", fmt.Errorf("this workflow tool is unavailable")
		}
		return capture.execute(ctx, args)
	}, timeout, group)
}

type workshopToolCapture struct {
	name    string
	execute func(context.Context, map[string]interface{}) (string, error)
}

func (*workshopToolCapture) AttachedSkills() []*llmtypes.Skill { return nil }
func (r *workshopToolCapture) RegisterCustomTool(n, _ string, _ map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), _ string) error {
	if n == r.name {
		r.execute = execute
	}
	return nil
}
func (r *workshopToolCapture) RegisterCustomToolWithTimeout(n, d string, schema map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), _ time.Duration, group string) error {
	return r.RegisterCustomTool(n, d, schema, execute, group)
}
