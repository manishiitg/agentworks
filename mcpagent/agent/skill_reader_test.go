package mcpagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestAttachedSkillAutomaticallyRegistersTransportNeutralReader(t *testing.T) {
	agent := &Agent{}
	if err := agent.attachSkill(&llmtypes.Skill{
		Name:        "workflow-reference",
		Description: "workflow contracts",
		Content:     "main skill instructions",
		SupportingFiles: []llmtypes.SkillFile{
			{RelPath: "references/stores.md", Content: []byte("store contract")},
			{RelPath: "assets/pixel.bin", Content: []byte{0xff, 0x00, 0x81}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	view := agent.Definition()
	found := false
	for _, tool := range view.Tools {
		if tool.Name == readSkillToolName {
			found = true
			if tool.DisplayGroup != readSkillToolCategory {
				t.Fatalf("read_skill display group = %q", tool.DisplayGroup)
			}
		}
	}
	if !found {
		t.Fatalf("attached skill did not register %s: %#v", readSkillToolName, view.Tools)
	}
	if len(agent.additionalBridgeTools) != 1 || agent.additionalBridgeTools[0] != readSkillToolName {
		t.Fatalf("read_skill was not projected through the coding-agent MCP bridge: %v", agent.additionalBridgeTools)
	}

	executor := agent.getCustomToolExecutor(readSkillToolName)
	if executor == nil {
		t.Fatal("read_skill executor is missing")
	}
	read := func(request map[string]interface{}) attachedSkillReadResult {
		t.Helper()
		raw, err := executor(context.Background(), map[string]interface{}{
			"skills": []interface{}{request},
		})
		if err != nil {
			t.Fatal(err)
		}
		var got attachedSkillBatchReadResult
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("decode read_skill result: %v\n%s", err, raw)
		}
		if len(got.Results) != 1 || got.Results[0].Error != "" {
			t.Fatalf("single read result = %#v", got.Results)
		}
		return got.Results[0]
	}

	main := read(map[string]interface{}{"name": "workflow-reference"})
	if main.Path != "SKILL.md" || main.Content != "main skill instructions" || main.Encoding != "utf-8" {
		t.Fatalf("main skill result = %#v", main)
	}
	if strings.Join(main.AvailableFiles, ",") != "SKILL.md,assets/pixel.bin,references/stores.md" {
		t.Fatalf("available files = %v", main.AvailableFiles)
	}

	reference := read(map[string]interface{}{"name": "workflow-reference", "path": "references/stores.md"})
	if reference.Content != "store contract" || reference.Encoding != "utf-8" {
		t.Fatalf("reference result = %#v", reference)
	}

	binary := read(map[string]interface{}{"name": "workflow-reference", "path": "assets/pixel.bin"})
	if binary.Encoding != "base64" || binary.Content != base64.StdEncoding.EncodeToString([]byte{0xff, 0x00, 0x81}) {
		t.Fatalf("binary result = %#v", binary)
	}
}

func TestAttachedSkillReaderRejectsTraversalAndUnknownResources(t *testing.T) {
	agent := &Agent{}
	if err := agent.attachSkill(&llmtypes.Skill{Name: "safe", Content: "body"}); err != nil {
		t.Fatal(err)
	}
	executor := agent.getCustomToolExecutor(readSkillToolName)

	for _, request := range []map[string]interface{}{
		{"name": "safe", "path": "../secret"},
		{"name": "safe", "path": "/etc/passwd"},
		{"name": "safe", "path": "references/missing.md"},
		{"name": "missing"},
	} {
		raw, err := executor(context.Background(), map[string]interface{}{"skills": []interface{}{request}})
		if err != nil {
			t.Fatalf("item failure should not fail the batch: %v", err)
		}
		var got attachedSkillBatchReadResult
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Results) != 1 || got.Results[0].Error == "" {
			t.Fatalf("read_skill did not report item error: request=%#v result=%#v", request, got.Results)
		}
	}
}

// One read per call. Batching three reference docs returned 67,971 characters,
// which the coding CLI truncated against its result cap and spilled to a file
// outside every workspace root — leaving the agent ordered to read something the
// folder guard forbids. A missing skill must still be reported inside the result
// rather than as a whole-call error, so the agent can see which read failed.
func TestAttachedSkillReaderReadsOneSkillAndReportsMissingInResult(t *testing.T) {
	agent := &Agent{}
	for _, skill := range []*llmtypes.Skill{
		{Name: "first", Description: "first description", Content: "first body"},
		{Name: "second", Content: "second body", SupportingFiles: []llmtypes.SkillFile{
			{RelPath: "references/detail.md", Content: []byte("second detail")},
		}},
	} {
		if err := agent.attachSkill(skill); err != nil {
			t.Fatal(err)
		}
	}
	executor := agent.getCustomToolExecutor(readSkillToolName)

	raw, err := executor(context.Background(), map[string]interface{}{
		"skills": []interface{}{
			map[string]interface{}{"name": "second", "path": "references/detail.md"},
		},
	})
	if err != nil {
		t.Fatalf("single read returned a whole-call error: %v", err)
	}
	var got attachedSkillBatchReadResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode result: %v\n%s", err, raw)
	}
	if len(got.Results) != 1 {
		t.Fatalf("result count = %d, want 1: %#v", len(got.Results), got.Results)
	}
	if got.Results[0].SkillName != "second" || got.Results[0].Path != "references/detail.md" || got.Results[0].Content != "second detail" || got.Results[0].Error != "" {
		t.Fatalf("result = %#v", got.Results[0])
	}

	// A miss is data, not a tool failure.
	raw, err = executor(context.Background(), map[string]interface{}{
		"skills": []interface{}{map[string]interface{}{"name": "missing"}},
	})
	if err != nil {
		t.Fatalf("missing skill returned a whole-call error: %v", err)
	}
	got = attachedSkillBatchReadResult{}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode result: %v\n%s", err, raw)
	}
	if len(got.Results) != 1 || !strings.Contains(got.Results[0].Error, `attached skill "missing" not found`) {
		t.Fatalf("missing skill result = %#v", got.Results)
	}
}

// Asking for more than one must fail loudly, and the error must name the
// recovery — an agent told only "at most 1" may silently drop the files it
// still needs instead of asking for them in the next call.
func TestAttachedSkillReaderRejectsBatchedReads(t *testing.T) {
	agent := &Agent{}
	if err := agent.attachSkill(&llmtypes.Skill{Name: "first", Content: "first body"}); err != nil {
		t.Fatal(err)
	}
	executor := agent.getCustomToolExecutor(readSkillToolName)
	_, err := executor(context.Background(), map[string]interface{}{
		"skills": []interface{}{
			map[string]interface{}{"name": "first"},
			map[string]interface{}{"name": "first"},
		},
	})
	if err == nil {
		t.Fatal("batched read was accepted; one read per call is the bound that prevents an oversized result")
	}
	if !strings.Contains(err.Error(), "Call read_skill again") {
		t.Fatalf("error does not name the recovery: %v", err)
	}
}

func TestAttachedSkillReaderRejectsInvalidBatchArguments(t *testing.T) {
	agent := &Agent{}
	if err := agent.attachSkill(&llmtypes.Skill{Name: "safe", Content: "body"}); err != nil {
		t.Fatal(err)
	}
	executor := agent.getCustomToolExecutor(readSkillToolName)
	tooMany := make([]interface{}, maxReadSkillBatchSize+1)
	for i := range tooMany {
		tooMany[i] = map[string]interface{}{"name": "safe"}
	}
	for _, args := range []map[string]interface{}{
		{},
		{"skills": []interface{}{}},
		{"skills": []interface{}{"safe"}},
		{"skills": []interface{}{map[string]interface{}{"name": ""}}},
		{"skills": []interface{}{map[string]interface{}{"name": 42}}},
		{"skills": []interface{}{map[string]interface{}{"name": "safe", "path": 42}}},
		{"skills": []interface{}{map[string]interface{}{"name": "safe", "unknown": true}}},
		{"skills": tooMany},
		{"skill_name": "safe"},
	} {
		if _, err := executor(context.Background(), args); err == nil {
			t.Fatalf("read_skill accepted invalid batch arguments: %#v", args)
		}
	}
}

func TestAttachedSkillReaderDoesNotTruncateLargeBatchContent(t *testing.T) {
	agent := &Agent{}
	large := strings.Repeat("complete skill instructions\n", 20_000)
	if err := agent.attachSkill(&llmtypes.Skill{Name: "large", Content: large}); err != nil {
		t.Fatal(err)
	}
	executor := agent.getCustomToolExecutor(readSkillToolName)
	raw, err := executor(context.Background(), map[string]interface{}{
		"skills": []map[string]interface{}{{"name": "large"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got attachedSkillBatchReadResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode large batch result: %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Content != large {
		t.Fatalf("large skill content was altered or truncated: got %d bytes, want %d", len(got.Results[0].Content), len(large))
	}
}

func TestReadSkillIsReservedAndSurvivesToolFiltering(t *testing.T) {
	agent := &Agent{toolFilter: NewToolFilter(nil, nil, nil, nil, nil)}
	if err := agent.attachSkill(&llmtypes.Skill{Name: "identity", Content: "body"}); err != nil {
		t.Fatal(err)
	}
	if err := agent.registerCustomTool(readSkillToolName, "override", map[string]interface{}{}, definitionNoopTool, "skill_tools"); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("read_skill override error = %v", err)
	}

	agent.toolAllowList = map[string]bool{"some_other_tool": true}
	filtered := agent.applyToolAllowList(agent.tools)
	if len(filtered) != 1 || filtered[0].Function == nil || filtered[0].Function.Name != readSkillToolName {
		t.Fatalf("intrinsic read_skill was filtered out: %#v", filtered)
	}
	policy, err := normalizeToolPolicy(ToolPolicy{AllowedTools: []string{"some_other_tool"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), turnPolicyContextKey{}, policy)
	if !agent.isToolAllowedForContext(ctx, readSkillToolName) {
		t.Fatal("turn policy blocked intrinsic read_skill")
	}
}

func TestNoAttachedSkillsMeansNoReadSkillTool(t *testing.T) {
	agent := &Agent{toolRegistry: newCanonicalToolRegistry()}
	if _, ok := findDefinitionTool(agent, readSkillToolName); ok {
		t.Fatal("read_skill should not exist without attached skills")
	}
}
