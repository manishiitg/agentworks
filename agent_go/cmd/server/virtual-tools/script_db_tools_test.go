package virtualtools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestScanWorkflowScriptDBUsageTool(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	const workspacePath = "Workflow/scan-tool"
	write := func(rel, body string) {
		path := filepath.Join(docs, filepath.FromSlash(workspacePath), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("code/step-a/main.py", "import sqlite3\nconn = sqlite3.connect('x')\nconn.execute('CREATE TABLE t (a)')\n")
	write("code/step-b/main.py", "from agentworks_db import query\nquery('SELECT 1')\n")
	write("code/step-a/test_main.py", "import sqlite3\n")

	sessionID := "script-db-scan-" + t.Name()
	common.SetSessionFolderGuard(sessionID, []string{workspacePath + "/code"}, nil)
	t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })
	registry := CreateScriptDBToolRegistry(sessionID)
	out, err := registry.Executors["scan_workflow_script_db_usage"](context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Scanned int  `json:"scripts_scanned"`
		Clean   bool `json:"clean"`
		Needs   []struct {
			File string   `json:"file"`
			Raw  []string `json:"raw_db"`
			DDL  []string `json:"ddl"`
		} `json:"needs_conversion"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Clean || result.Scanned != 2 || len(result.Needs) != 1 || result.Needs[0].File != "step-a/main.py" || len(result.Needs[0].DDL) != 1 {
		t.Fatalf("scan = %s", out)
	}
	// Converted: clean, with an empty (not null) list.
	write("code/step-a/main.py", "from agentworks_db import execute\nexecute('DELETE FROM t')\n")
	out, err = registry.Executors["scan_workflow_script_db_usage"](context.Background(), map[string]any{})
	if err != nil || !json.Valid([]byte(out)) {
		t.Fatalf("scan after converting: %v %s", err, out)
	}
	if want := `"needs_conversion":[]`; !strings.Contains(out, want) || !strings.Contains(out, `"clean":true`) {
		t.Fatalf("a clean workflow must report an empty list and clean=true: %s", out)
	}
}
