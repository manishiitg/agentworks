package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-746: parent Python reads live own-Crew knowledge through its command
// bridge without a raw grant; scope/private/symlink refusals use the real reader.
func TestPythonCrewFileReadIsFreshScopedAndKeepsRawSandboxClosed(t *testing.T) {
	docs, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("AGENTWORKS_SLOTS", "off")
	write := func(file, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const root = "Workflow/files"
	crew := filepath.Join(docs, "Crew/example")
	private := filepath.Join(docs, "_system/crew-data/crew")
	runtime := filepath.Join(private, "workflows/task")
	code, output := filepath.Join(runtime, "runs/run-one/source"), filepath.Join(runtime, "runs/run-one/execution/task")
	for _, dir := range []string{crew, output} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(state, "ownership/projects.json"), `{"projects":{"work/example":{"product":"work","folder":"example","project_id":"crew","owner_id":"owner"}}}`)
	write(filepath.Join(state, "ownership/crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte(root)))), `{"id":"task","crew_id":"crew","crew_root":"Crew/example","workspace_path":"Workflow/files","kind":"script","runtime_root":"workflows/task"}`)
	note := filepath.Join(private, "knowledgebase/notes/owner-handled.md")
	write(note, "first")
	write(filepath.Join(private, "knowledgebase/notes/big.md"), strings.Repeat("x", pythonCrewFileLimit+1))
	write(filepath.Join(private, "knowledgebase/notes/binary.md"), string([]byte{0xff}))
	write(filepath.Join(private, "workflow.json"), `{"private":true}`)
	outside := filepath.Join(docs, "Crew/other/private-note.md")
	write(outside, "must not be returned")
	if err := os.Symlink(outside, filepath.Join(private, "knowledgebase/notes/link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(private, "knowledgebase/linked")); err != nil {
		t.Fatal(err)
	}
	source := `from pathlib import Path
import os
from agentworks import files, AutomationError
from agentworks_db import _call, DBError
try:
    Path(os.environ['FIXTURE_NOTE']).read_text()
except OSError:
    pass
else:
    raise AssertionError('parent received a raw Crew knowledge grant')
assert files.read('knowledgebase/notes/owner-handled.md') == 'first'
assert files.read('knowledgebase/notes/owner-handled.md') == 'second', 'read reused a saved helper result'
for path in ('Crew/other/private-note.md', '../other/private-note.md', os.environ['FIXTURE_NOTE'], '.env', 'builder/conversation/chat.json', 'workflow.json', 'knowledgebase/notes/link.md', 'knowledgebase/linked/private-note.md', 'knowledgebase/notes/big.md', 'knowledgebase/notes/binary.md'):
    try:
        files.read(path)
    except AutomationError as error:
        assert 'must not be returned' not in str(error)
    else:
        raise AssertionError('file boundary allowed ' + path)
try:
    _call('crew_files_read', {'path':'knowledgebase/notes/owner-handled.md', 'root':'Crew/other'})
except DBError:
    pass
else:
    raise AssertionError('caller selected another Crew root')
print('fresh-own-Crew-read-and-refusals-ok')
`
	write(filepath.Join(code, "main.py"), source)
	c, err := NewStepBasedWorkflowOrchestrator(t.Context(), "", "", 0, "", nil, nil, false, "", &orchestrator.LLMConfig{}, 1, loggerv2.NewNoop(), nil, nil, nil, nil, nil, nil, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetWorkspacePath(root)
	c.selectedRunFolder = "run-one"
	handlers := executor.NewExecutorHandlers("", loggerv2.NewNoop())
	var reads atomic.Int32
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 5 || parts[0] != "s" || r.Header.Get("Authorization") != "Bearer fixture-files" {
			http.Error(w, "command session required", 403)
			return
		}
		r.Header.Set("X-Session-ID", parts[1])
		handlers.HandlePerToolCustomRequest(w, r, parts[4])
		if reads.Add(1) == 1 {
			write(note, "second")
		}
	}))
	defer bridge.Close()
	t.Setenv("MCP_API_URL", bridge.URL)
	env := map[string]string{"MCP_API_TOKEN": "fixture-files", "FIXTURE_NOTE": note}
	step := &ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "files", Title: "Files"}}
	guard := &workspace.FolderGuardConfig{ReadPaths: []string{code, output}, WritePaths: []string{output}}
	release := c.configureScriptBridgeSession(context.Background(), step, guard, DBAccessNone, env, code, root+"/runs/run-one/execution/task")
	defer release()
	iso := security.Isolator{BaseDir: docs, WorkDir: code, ReadPaths: guard.ReadPaths, WritePaths: guard.WritePaths, ExtraEnv: env}
	command, cleanup, err := iso.ExecuteIsolated(t.Context(), "python3", []string{filepath.Join(code, "main.py")})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	command.Env = security.MergeExtraEnv(command.Env, env)
	raw, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("parent SDK file path: %v\n%s", err, raw)
	}
	if !strings.Contains(string(raw), "fresh-own-Crew-read-and-refusals-ok") {
		t.Fatalf("missing actual Python completion: %s", raw)
	}
}
