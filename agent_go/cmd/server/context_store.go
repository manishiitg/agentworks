package server

import (
	"context"
	"fmt"
	"path"
	"strings"
)

func contextRulesPath(workspacePath string) string {
	return path.Join(strings.Trim(workspacePath, "/"), "knowledgebase", "context", "context.md")
}

func legacyContextRulesPath(workspacePath string) string {
	return path.Join(strings.Trim(workspacePath, "/"), "knowledgebase", "rules", "rules.md")
}

// ReadContextRules returns the contents of knowledgebase/context/context.md.
func ReadContextRules(ctx context.Context, workspacePath string) (string, bool, error) {
	contents, exists, err := readFileFromWorkspace(ctx, contextRulesPath(workspacePath))
	if err != nil || exists {
		return contents, exists, err
	}
	return readFileFromWorkspace(ctx, legacyContextRulesPath(workspacePath))
}

// AppendContextRule adds a new rule under the given (optional) section to
// knowledgebase/context/context.md. If the section does not exist it is created.
func AppendContextRule(ctx context.Context, workspacePath, section, ruleText string) error {
	ruleText = strings.TrimSpace(ruleText)
	if ruleText == "" {
		return fmt.Errorf("context text is empty")
	}

	existing, exists, err := ReadContextRules(ctx, workspacePath)
	if err != nil {
		return err
	}
	if !exists {
		existing = "# Workflow Context\n\n" +
			"This file accumulates runtime business context supplied by the user: rules, preferences, constraints, assumptions, and examples that future workflow steps must respect. " +
			"Context is persisted via the `capture_context` tool (the builder agent recognizes durable runtime context in conversation and offers to capture). " +
			"Each captured item lands as a bullet under a section heading. " +
			"Steps with `knowledgebase_access` set to `read` (or `read-write`) automatically see this file at runtime — " +
			"the context folder is a sub-section of the knowledgebase.\n\n" +
			"This file is **excluded** from `reorganize_knowledgebase` and `consolidate_knowledgebase` passes — " +
			"user-supplied content is never silently rewritten by the optimizer.\n\n"
	}

	body := existing
	section = strings.TrimSpace(section)
	if section == "" {
		section = "General"
	}
	heading := "## " + section
	bullet := "- " + ruleText + "\n"

	if strings.Contains(body, heading) {
		idx := strings.Index(body, heading)
		searchFrom := idx + len(heading)
		nextIdx := strings.Index(body[searchFrom:], "\n## ")
		insertAt := len(body)
		if nextIdx >= 0 {
			insertAt = searchFrom + nextIdx
		}
		body = body[:insertAt] + "\n" + bullet + body[insertAt:]
	} else {
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		body += "\n" + heading + "\n\n" + bullet
	}

	return writeFileToWorkspace(ctx, contextRulesPath(workspacePath), body)
}

// CaptureContext appends the user rule to the runtime context file and records
// a typed authoritative receipt in the workflow database.

type CaptureContextInput struct {
	Section     string `json:"section,omitempty"`
	ContextText string `json:"context_text,omitempty"`
	ExampleNote string `json:"example_note,omitempty"`
}

type CaptureContextOutput struct {
	DecisionID     string   `json:"decision_id,omitempty"`
	Status         string   `json:"status"`
	Section        string   `json:"section,omitempty"`
	AppliedChanges []string `json:"applied_changes,omitempty"`
}

func CaptureContextTool(ctx context.Context, workspacePath string, input CaptureContextInput) (*CaptureContextOutput, error) {
	section := strings.TrimSpace(input.Section)
	if section == "" {
		section = "General"
	}
	if strings.TrimSpace(input.ContextText) == "" {
		return nil, fmt.Errorf("capture_context requires context text")
	}
	if err := AppendContextRule(ctx, workspacePath, section, input.ContextText); err != nil {
		return nil, err
	}
	return &CaptureContextOutput{
		Status:         "captured",
		Section:        section,
		AppliedChanges: []string{"knowledgebase/context/context.md"},
	}, nil
}
