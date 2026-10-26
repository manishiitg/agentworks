package platformtools_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
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
		// Goal registration passes through Parameters before grouping (PLAT-825-142).
		var parameters llmtypes.Parameters
		if err := json.Unmarshal(raw, &parameters); err != nil {
			t.Fatal(err)
		}
		raw, err = json.Marshal(parameters)
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
		search, err := platformtools.MemberSchema(original, platformtools.Action{Source: tool.Name, SourceAction: "search"})
		if err != nil {
			t.Fatal(err)
		}
		const searchName = "https://example.com/grouped-brain-search.json"
		if err := compiler.AddResource(searchName, search); err != nil {
			t.Fatal(err)
		}
		searchValidator, err := compiler.Compile(searchName)
		if err != nil {
			t.Fatal(err)
		}
		if err := searchValidator.Validate(map[string]any{"query": "needle", "folder_path": "Shared"}); err != nil {
			t.Fatalf("valid scoped search refused: %v", err)
		}
		invalidSearch := map[string]any{"query": "needle", "path": "Shared/Guide.md"}
		if err := searchValidator.Validate(invalidSearch); err == nil {
			t.Fatal("search accepted a read locator")
		}
		invalidSearch["action"] = "search"
		if err := knowledgebase.ValidateToolArguments(tool.Name, invalidSearch); err == nil || !strings.Contains(err.Error(), "folder_path") {
			t.Fatalf("invalid search did not explain its existing folder scope: %v", err)
		}
		if err := validator.Validate(map[string]any{"path": "Shared/Guide.md", "end_line": float64(22)}); err == nil {
			t.Fatal("read lost its paired line-range rule")
		}
		if err := validator.Validate(map[string]any{"path": "Shared/Guide.md", "start_line": float64(1), "end_line": float64(22)}); err != nil {
			t.Fatalf("valid line range refused: %v", err)
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
