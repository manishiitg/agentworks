package step_based_workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// RegisterLazyPythonRunTool uses the saved-Python start schema without initializing a
// controller, credentials or connections until a tool is actually called.
// The caller's registrar still decides which of the native tools are admitted.
func RegisterLazyPythonRunTool(target DefinitionRegistrar, resolve func(context.Context) (*WorkshopChatSession, error), logger loggerv2.Logger) {
	schemaSession := &WorkshopChatSession{
		controller: &StepBasedWorkflowOrchestrator{BaseOrchestrator: &orchestrator.BaseOrchestrator{}},
		config:     &WorkshopConfig{},
	}
	lazy := &lazyPythonRunRegistrar{target: target, resolve: resolve, logger: logger}
	RegisterRunFullWorkflowTool(lazy, schemaSession, logger)
}

type lazyPythonRunRegistrar struct {
	target  DefinitionRegistrar
	resolve func(context.Context) (*WorkshopChatSession, error)
	logger  loggerv2.Logger
}

func (r *lazyPythonRunRegistrar) AttachedSkills() []*llmtypes.Skill { return r.target.AttachedSkills() }
func (r *lazyPythonRunRegistrar) RegisterCustomTool(n, d string, schema map[string]interface{}, _ func(context.Context, map[string]interface{}) (string, error), group string) error {
	return r.RegisterCustomToolWithTimeout(n, d, schema, nil, 0, group)
}
func (r *lazyPythonRunRegistrar) RegisterCustomToolWithTimeout(n, d string, schema map[string]interface{}, _ func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, group string) error {
	return r.target.RegisterCustomToolWithTimeout(n, d, schema, func(ctx context.Context, args map[string]interface{}) (string, error) {
		session, err := r.resolve(ctx)
		if err != nil {
			return "", err
		}
		// Tool starts are short; the work itself runs in the existing background
		// registry. Serialize selection/start across replacement chat definitions.
		session.toolCallMu.Lock()
		defer session.toolCallMu.Unlock()
		if err := ctx.Err(); err != nil {
			return "", err
		}
		capture := &pythonRunToolCapture{name: n}
		RegisterRunFullWorkflowTool(capture, session, r.logger)
		if capture.execute == nil {
			return "", fmt.Errorf("this Python workflow is unavailable")
		}
		return capture.execute(ctx, args)
	}, timeout, group)
}

type pythonRunToolCapture struct {
	name    string
	execute func(context.Context, map[string]interface{}) (string, error)
}

func (*pythonRunToolCapture) AttachedSkills() []*llmtypes.Skill { return nil }
func (r *pythonRunToolCapture) RegisterCustomTool(n, _ string, _ map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), _ string) error {
	if n == r.name {
		r.execute = execute
	}
	return nil
}
func (r *pythonRunToolCapture) RegisterCustomToolWithTimeout(n, d string, schema map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), _ time.Duration, group string) error {
	return r.RegisterCustomTool(n, d, schema, execute, group)
}
