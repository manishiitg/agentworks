package platformtools

import (
	"context"
	"strings"
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// PLAT-825-113: the stored native patch selector must not grant image work
// or other file actions when its family becomes the advertised tool.
func TestPartialFileSelectionKeepsActionAndNamespace(t *testing.T) {
	for _, filter := range []bool{false, true} {
		calls := []string{}
		definitions := []mcpagent.ToolDefinition{}
		for _, name := range []string{"diff_patch_workspace_file", "read_image"} {
			definitions = append(definitions, mcpagent.ToolDefinition{Name: name, DisplayGroup: "workspace_advanced",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
				Execute:     func(context.Context, map[string]any) (string, error) { calls = append(calls, name); return name, nil }})
		}
		jobs, err := Project(definitions, Selection{Tools: []string{"workspace_advanced:files.patch"}, FilterCustom: filter})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, job := range jobs {
			if job.Name != "files" {
				continue
			}
			found = true
			if _, err := job.Execute(t.Context(), map[string]any{"action": "patch"}); err != nil {
				t.Fatalf("selected patch refused: %v", err)
			}
			if _, err := job.Execute(t.Context(), map[string]any{"action": "image"}); err == nil || !strings.Contains(err.Error(), "unavailable") {
				t.Fatalf("patch selection granted image work: %v", err)
			}
		}
		if !found || len(calls) != 1 || calls[0] != "diff_patch_workspace_file" {
			t.Fatalf("selected native source did not execute alone: found=%t calls=%v", found, calls)
		}
	}
}
