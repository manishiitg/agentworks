package server

import (
	"context"

	"time"
)

type llmToolPolicyRecordingRegistrar struct {
	names map[string]bool
}

func (r *llmToolPolicyRecordingRegistrar) RegisterCustomTool(name, _ string, _ map[string]interface{}, _ func(context.Context, map[string]interface{}) (string, error), _ string) error {
	r.names[name] = true
	return nil
}

func (r *llmToolPolicyRecordingRegistrar) RegisterCustomToolWithTimeout(name, description string, params map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), _ time.Duration, category string) error {
	return r.RegisterCustomTool(name, description, params, execute, category)
}
