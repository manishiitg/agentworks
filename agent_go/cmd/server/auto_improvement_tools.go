package server

import (
	"context"
	"encoding/json"
	"fmt"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// =====================================================================
// auto_improvement_tools.go — register auto-improvement tools with an
// mcpagent so the workflow optimizer can capture user-supplied runtime context.
//
// Caller wires these into the agent's tool registry alongside other custom
// tools like reorganize_knowledgebase / consolidate_knowledgebase. Each
// registration captures the workspacePath in a closure so the LLM does not
// need to supply it.
// =====================================================================

func RegisterCaptureContextTool(agent definitionToolRegistrar, workspacePath string, logger loggerv2.Logger) {
	desc := "Capture durable user-supplied runtime business context for this workflow. " +
		"Use only after the user confirms the item should be remembered across runs, and only when the Workflow Profile allows business-context accumulation. " +
		"Writes to knowledgebase/context/context.md. Keep the capture concise and authoritative. " +
		"Use for persistent rules, preferences, constraints, assumptions, examples, ICP filters, approval rules, brand voice, or domain context that workflow steps must respect. " +
		"Do not use for one-off instructions, general chat memory, workflow-discovered facts that belong in knowledgebase/notes, or execution recipes that belong in learnings."
	params := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"section": map[string]interface{}{
				"type":        "string",
				"description": "Markdown section heading in knowledgebase/context/context.md. Defaults to General when empty.",
			},
			"context_text": map[string]interface{}{
				"type":        "string",
				"description": "The durable user-supplied context to capture. Keep it concise and faithful to the user's wording.",
			},
			"example_note": map[string]interface{}{
				"type":        "string",
				"description": "Optional short note about the example or source context for this capture.",
			},
		},
		"required": []string{"context_text"},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (string, error) {
		raw, err := json.Marshal(args)
		if err != nil {
			return "", err
		}
		var input CaptureContextInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return fmt.Sprintf("invalid arguments for capture_context: %v", err), nil
		}
		out, err := CaptureContextTool(ctx, workspacePath, input)
		if err != nil {
			return fmt.Sprintf("capture_context failed: %v", err), nil
		}
		body, _ := json.MarshalIndent(out, "", "  ")
		return string(body), nil
	}

	if err := agent.RegisterCustomTool("capture_context", desc, params, handler, "auto_improvement"); err != nil {
		if logger != nil {
			logger.Warn(fmt.Sprintf("Failed to register capture_context: %v", err))
		}
	}
}
