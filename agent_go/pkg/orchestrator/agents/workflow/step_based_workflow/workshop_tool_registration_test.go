package step_based_workflow

import (
	"context"
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type workshopDefinitionDraft struct {
	tools  map[string]mcpagent.ToolDefinition
	skills []*llmtypes.Skill
}

func newWorkshopDefinitionDraft() *workshopDefinitionDraft {
	return &workshopDefinitionDraft{tools: make(map[string]mcpagent.ToolDefinition)}
}

func (d *workshopDefinitionDraft) RegisterCustomTool(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), displayGroup string) error {
	return d.RegisterCustomToolWithTimeout(name, description, parameters, execute, 0, displayGroup)
}

func (d *workshopDefinitionDraft) RegisterCustomToolWithTimeout(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, displayGroup string) error {
	d.tools[name] = mcpagent.ToolDefinition{Name: name, Description: description, InputSchema: parameters, Execute: execute, Timeout: timeout, DisplayGroup: displayGroup}
	return nil
}

func (d *workshopDefinitionDraft) AttachedSkills() []*llmtypes.Skill {
	return append([]*llmtypes.Skill(nil), d.skills...)
}

type workshopToolTestLogger struct{}

func (workshopToolTestLogger) Debug(string, ...loggerv2.Field)          {}
func (workshopToolTestLogger) Info(string, ...loggerv2.Field)           {}
func (workshopToolTestLogger) Warn(string, ...loggerv2.Field)           {}
func (workshopToolTestLogger) Error(string, error, ...loggerv2.Field)   {}
func (workshopToolTestLogger) Fatal(string, error, ...loggerv2.Field)   {}
func (l workshopToolTestLogger) With(...loggerv2.Field) loggerv2.Logger { return l }
func (workshopToolTestLogger) Close() error                             { return nil }

// An old full-mode schedule used to turn an owner's OFF back on after the
// Builder saved raw workflow.json. Drive the actual registered config tool.
