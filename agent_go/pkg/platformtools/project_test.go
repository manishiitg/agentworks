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

// PLAT-825: a source serializing an empty required list as null failed the
// whole catalog on a server and the local app (slack_bot_credentials, ui.get).
func TestNormalizeRequiredTurnsNullIntoEmptyList(t *testing.T) {
	schema := map[string]any{"type": "object", "required": nil, "properties": map[string]any{
		"nested": map[string]any{"type": "object", "required": []string(nil)},
		"keep":   map[string]any{"type": "object", "required": []any{"a"}},
	}}
	out := normalizeRequired(schema)
	if _, err := compileSchema(out); err != nil {
		t.Fatalf("normalized schema still invalid: %v", err)
	}
	props := out["properties"].(map[string]any)
	if r := props["keep"].(map[string]any)["required"].([]any); len(r) != 1 {
		t.Fatalf("a real required list changed: %v", r)
	}
	if schema["required"] != nil {
		t.Fatal("the caller's schema was modified")
	}
}

// PLAT-856-397: update_variable needs its own action (add/update/delete), which the settings group used to drop, so
// no chat could change a workflow variable. Each settings variable action now reaches it with that action set.
func TestSettingsVariableActionsReachUpdateVariableWithItsAction(t *testing.T) {
	var got map[string]any
	schema := map[string]any{"type": "object", "additionalProperties": false, "required": []any{"action"}, "properties": map[string]any{
		"action": map[string]any{"type": "string", "enum": []any{"update", "add", "delete"}}, "name": map[string]any{"type": "string"},
		"value": map[string]any{"type": "string"}, "existing_variable_name": map[string]any{"type": "string"}}}
	jobs, err := Project([]mcpagent.ToolDefinition{{Name: "update_variable", InputSchema: schema,
		Execute: func(_ context.Context, args map[string]any) (string, error) { got = args; return "ok", nil }}})
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.Name != "settings" {
			continue
		}
		if _, err := job.Execute(t.Context(), map[string]any{"action": "variable_add", "name": "REGION", "value": "eu"}); err != nil {
			t.Fatal(err)
		}
		if got["action"] != "add" || got["name"] != "REGION" {
			t.Fatalf("update_variable got %v", got)
		}
		if _, err := job.Execute(t.Context(), map[string]any{"action": "variable_delete", "existing_variable_name": "REGION"}); err != nil || got["action"] != "delete" {
			t.Fatalf("delete reached update_variable as %v (%v)", got, err)
		}
		return
	}
	t.Fatal("no settings group")
}

// A Crew could not select its own saved skill: skills action=select reached
// update_project_skill_selection without its own select/deselect action and
// failed "schema missing action". The same collision hid mcp/vault selection
// and the Flow Run chat's read_relay; each public action now sets the native one.
func TestSelectorSourcesReceiveTheirOwnAction(t *testing.T) {
	for _, tc := range []struct {
		tool, family, field string
		native              []any
		public              map[string]string
	}{
		{"update_project_skill_selection", "skills", "skill", []any{"select", "deselect"}, map[string]string{"select": "select", "deselect": "deselect"}},
		{"update_project_mcp_server_selection", "mcp", "server", []any{"select", "deselect"}, map[string]string{"select": "select", "deselect": "deselect"}},
		{"update_project_global_secret_selection", "vault", "name", []any{"select", "deselect"}, map[string]string{"select": "select", "deselect": "deselect"}},
		{"read_relay", "relay", "version", []any{"source", "releases"}, map[string]string{"get": "source", "releases": "releases"}},
	} {
		var got map[string]any
		schema := map[string]any{"type": "object", "additionalProperties": false, "required": []any{"action"}, "properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": tc.native}, tc.field: map[string]any{"type": "string"}}}
		jobs, err := Project([]mcpagent.ToolDefinition{{Name: tc.tool, InputSchema: schema,
			Execute: func(_ context.Context, args map[string]any) (string, error) { got = args; return "ok", nil }}})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, job := range jobs {
			if job.Name != tc.family {
				continue
			}
			found = true
			for public, native := range tc.public {
				got = nil
				if _, err := job.Execute(t.Context(), map[string]any{"action": public, tc.field: "example"}); err != nil {
					t.Fatalf("%s action=%s: %v", tc.family, public, err)
				}
				if got["action"] != native || got[tc.field] != "example" {
					t.Fatalf("%s action=%s reached %s as %v", tc.family, public, tc.tool, got)
				}
			}
		}
		if !found {
			t.Fatalf("no %s group for %s", tc.family, tc.tool)
		}
	}
}
