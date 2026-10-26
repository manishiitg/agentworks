package platformtools_test

import (
	"encoding/json"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// PLAT-825-113: dropping only the root action left Brain's oneOf branches
// requiring the removed discriminator and rejected every valid read payload.
func TestBrainReadProjectionKeepsItsSelectedPayloadRules(t *testing.T) {
	for _, tool := range knowledgebase.ToolDefinitions() {
		if tool.Name != knowledgebase.ToolRead {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var original map[string]any
		if err := json.Unmarshal(raw, &original); err != nil {
			t.Fatal(err)
		}
		schema, err := platformtools.MemberSchema(original, platformtools.Action{Source: tool.Name, SourceAction: "read"})
		if err != nil {
			t.Fatal(err)
		}
		compiler := jsonschema.NewCompiler()
		const name = "https://example.com/grouped-brain-read.json"
		if err := compiler.AddResource(name, schema); err != nil {
			t.Fatal(err)
		}
		validator, err := compiler.Compile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := validator.Validate(map[string]any{"path": "Shared/Guide.md"}); err != nil {
			t.Fatalf("valid read payload refused: %v", err)
		}
		if err := validator.Validate(map[string]any{"query": "a search payload"}); err == nil {
			t.Fatal("read accepted another action's payload")
		}
		// The source still sees and validates its original fixed action.
		if err := knowledgebase.ValidateToolArguments(tool.Name, map[string]any{"action": "read", "path": "Shared/Guide.md"}); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("Brain read schema unavailable")
}

// PLAT-825-113: Brain access list has no ACL-version requirement, while a
// grant must keep that requirement after its source action is fixed.
func TestBrainAccessProjectionSpecializesConditionalRequirements(t *testing.T) {
	for _, tool := range knowledgebase.ToolDefinitions() {
		if tool.Name != knowledgebase.ToolAccess {
			continue
		}
		raw, _ := json.Marshal(tool.InputSchema)
		var original map[string]any
		if err := json.Unmarshal(raw, &original); err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"list", "grant"} {
			schema, err := platformtools.MemberSchema(original, platformtools.Action{Source: tool.Name, SourceAction: action})
			if err != nil {
				t.Fatal(err)
			}
			compiler := jsonschema.NewCompiler()
			const location = "https://example.com/brain-access.json"
			if err := compiler.AddResource(location, schema); err != nil {
				t.Fatal(err)
			}
			validator, err := compiler.Compile(location)
			if err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"folder_path": "Shared"}
			if action == "grant" {
				input["identity_id"] = "colleague"
				input["role"] = "Reader"
				input["request_id"] = "grant-one"
			}
			err = validator.Validate(input)
			if action == "list" && err != nil {
				t.Fatalf("list inherited grant-only requirements: %v", err)
			}
			if action == "grant" {
				if err == nil {
					t.Fatal("grant lost its ACL-version rule")
				}
				input["expected_acl_version"] = "opaque-current-version"
				if err := validator.Validate(input); err != nil {
					t.Fatal(err)
				}
			}
		}
		return
	}
	t.Fatal("actual Brain access schema missing")
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
