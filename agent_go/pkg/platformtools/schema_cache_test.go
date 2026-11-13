package platformtools

import (
	"context"
	"fmt"
	"sync"
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// PLAT-746-876: rebuilding agents must share validator graphs, not executors
// or selected permissions. Exercise projection and concurrent real validation.
func TestAgentProjectionsShareSchemasNotExecutors(t *testing.T) {
	schema := map[string]any{"type": "object", "$comment": t.Name(), "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}
	project := func(owner string) error {
		jobs, err := Project([]mcpagent.ToolDefinition{{Name: "diff_patch_workspace_file", DisplayGroup: "workspace_advanced", InputSchema: schema, Execute: func(context.Context, map[string]any) (string, error) { return owner, nil }}}, Selection{Tools: []string{"workspace_advanced:files.patch"}, FilterCustom: true})
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if job.Name != "files" {
				continue
			}
			got, err := job.Execute(context.Background(), map[string]any{"action": "patch", "path": "example.txt"})
			if err != nil || got != owner {
				return fmt.Errorf("executor crossed agent ownership: got=%q err=%v", got, err)
			}
			if _, err := job.Execute(context.Background(), map[string]any{"action": "patch", "path": 42}); err == nil {
				return fmt.Errorf("cached schema stopped validation")
			}
			if _, err := job.Execute(context.Background(), map[string]any{"action": "link", "path": "example.txt"}); err == nil {
				return fmt.Errorf("projection granted unselected action")
			}
			return nil
		}
		return fmt.Errorf("no projected files tool")
	}
	if err := project("first"); err != nil {
		t.Fatal(err)
	}
	compiledSchemas.Lock()
	before := len(compiledSchemas.byHash)
	compiledSchemas.Unlock()
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(owner string) { defer wg.Done(); errs <- project(owner) }(fmt.Sprintf("agent-%d", i))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	compiledSchemas.Lock()
	after := len(compiledSchemas.byHash)
	compiledSchemas.Unlock()
	if after != before {
		t.Fatalf("repeated agent builds retained %d new validator graphs", after-before)
	}
	// Changing a schema changes its hash; caller mutation cannot edit the old graph.
	first, err := compileSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	schema["properties"].(map[string]any)["path"] = map[string]any{"type": "number"}
	second, err := compileSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different schema reused the old graph")
	}
	if err := first.Validate(map[string]any{"path": "example.txt"}); err != nil {
		t.Fatal("caller mutation changed published schema")
	}
	if err := second.Validate(map[string]any{"path": "example.txt"}); err == nil {
		t.Fatal("new schema ignored")
	}
}
