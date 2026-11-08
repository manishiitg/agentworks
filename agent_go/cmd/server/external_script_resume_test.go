package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Extend the real MCP/Python fixture: a resume must execute the captured code
// with the old inputs, preserve output, and refuse Run users or changed proof.
func checkSavedScriptMCPResume(t *testing.T, api *StreamingAPI, binding crewExecutableBinding, docs string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	endpoint := serveExternalMCP(t, api, &UserClaims{UserID: "owner", Username: "owner"})
	cli := dialExternalMCP(t, ctx, endpoint.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		reply := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": name, "arguments": args})
		requireRemoteSuccess(t, reply, name)
		var result map[string]any
		if err := json.Unmarshal([]byte(marshalStructured(t, reply)), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	write := func(source string) {
		call("workspace", map[string]any{"action": "automation_write", "crew_id": binding.CrewID, "workspace_path": binding.Root, "file": "main.py", "content": source})
	}
	source := "import json, os, time\nfrom pathlib import Path\nfrom agentworks import inputs, run_folder\np=run_folder/'resume-counter.txt'\nn=int(p.read_text())+1 if p.exists() else 1\np.write_text(str(n))\nif n==1: raise RuntimeError('resume-fixture failed once')\nwhile not (run_folder/'resume-release.txt').exists(): time.sleep(0.02)\ndata=json.dumps({'count':inputs['count'],'attempts':n})\nfor name in ['resumed.json','result.json']: (Path(os.environ['STEP_OUTPUT_DIR'])/name).write_text(data)\n"
	write(source)
	layout, found, err := crewroot.LookupExecutable(docs, binding.Root)
	if err != nil || !found {
		t.Fatal(err)
	}
	readMeta := func(folder string) map[string]any {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(layout.Runtime, "runs", folder, "run_metadata.json"))
		if err != nil {
			return nil
		}
		var metadata map[string]any
		if err := json.Unmarshal(raw, &metadata); err != nil {
			t.Fatal(err)
		}
		return metadata
	}
	wait := func(session, folder, status, previous string) string {
		t.Helper()
		for {
			call("runs", map[string]any{"action": "status", "workflow_id": binding.ID, "session_id": session})
			entries, _ := os.ReadDir(filepath.Join(layout.Runtime, "runs"))
			for _, entry := range entries {
				if folder != "" && entry.Name() != folder {
					continue
				}
				metadata := readMeta(entry.Name())
				if metadata["status"] == status && metadata["execution_id"] != previous {
					if _, err := os.Stat(filepath.Join(layout.Runtime, "runs", entry.Name(), "resume-counter.txt")); err == nil {
						return entry.Name()
					}
				}
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	started := call("runs", map[string]any{"action": "start", "workflow_id": binding.ID, "inputs": map[string]any{"count": 9}})
	folder := wait(started["session_id"].(string), "", "failed", "")
	releasePath := filepath.Join(layout.Runtime, "runs", folder, "resume-release.txt")
	defer func() { _ = os.WriteFile(releasePath, []byte("finish"), 0600) }()
	metaPath := filepath.Join(layout.Runtime, "runs", folder, "run_metadata.json")
	inputPath := filepath.Join(layout.Runtime, "runs", folder, "inputs.json")
	metadata := readMeta(folder)
	expectedSource := metadata["source_snapshot_hash"]
	previous := metadata["execution_id"].(string)
	originalMeta, _ := os.ReadFile(metaPath)
	originalInputs, _ := os.ReadFile(inputPath)
	refuse := func(reason string) {
		t.Helper()
		reply := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "runs", "arguments": map[string]any{"action": "resume", "workflow_id": binding.ID, "run_folder": folder}})
		requireRemoteError(t, reply, reason, "resume_unavailable")
		if !strings.Contains(marshalContent(t, reply), "Start a new run") {
			t.Fatal("changed/missing proof did not offer a new run")
		}
	}
	// A known source hash plus saved typed input bytes is required; old or
	// tampered records never fall back to current authored source.
	metadata["source_snapshot_hash"] = strings.Repeat("0", 64)
	raw, _ := json.Marshal(metadata)
	if err := os.WriteFile(metaPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	refuse("changed source proof")
	if err := os.WriteFile(metaPath, originalMeta, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, []byte(`{"count":7}`), 0600); err != nil {
		t.Fatal(err)
	}
	refuse("changed input proof")
	if err := os.WriteFile(inputPath, originalInputs, 0600); err != nil {
		t.Fatal(err)
	}
	runnerEndpoint := serveExternalMCP(t, api, &UserClaims{UserID: "runner", Username: "runner"})
	runner := dialExternalMCP(t, ctx, runnerEndpoint.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, runner)
	denied := callRemoteTool(t, ctx, runner, "call_tool", map[string]any{"name": "runs", "arguments": map[string]any{"action": "resume", "workflow_id": binding.ID, "run_folder": folder}})
	requireRemoteError(t, denied, "Run access cannot continue an owned run", "forbidden")
	write("raise RuntimeError('current authored source must not run during resume')\n")
	resumed := call("runs", map[string]any{"action": "resume", "workflow_id": binding.ID, "run_folder": folder})
	if resumed["run_folder"] != folder || resumed["resume_of"] != previous || resumed["source_snapshot_hash"] != expectedSource {
		t.Fatalf("resume lost its trusted run binding: %v", resumed)
	}
	busy := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "runs", "arguments": map[string]any{"action": "resume", "workflow_id": binding.ID, "run_folder": folder}})
	requireRemoteError(t, busy, "active resume attempt", "run_already_running")
	if err := os.WriteFile(releasePath, []byte("finish"), 0600); err != nil {
		t.Fatal(err)
	}
	wait(resumed["session_id"].(string), folder, "completed", previous)
	current := readMeta(folder)
	var original map[string]any
	_ = json.Unmarshal(originalMeta, &original)
	if current["source_snapshot_hash"] != original["source_snapshot_hash"] || current["inputs_snapshot_hash"] != original["inputs_snapshot_hash"] || current["resume_of"] != previous {
		t.Fatal("resume changed captured source or inputs")
	}
	artifact, err := os.ReadFile(filepath.Join(layout.Runtime, "runs", folder, "execution", binding.ID, "resumed.json"))
	if err != nil || !strings.Contains(string(artifact), `"count": 9`) || !strings.Contains(string(artifact), `"attempts": 2`) {
		t.Fatalf("captured Python did not resume with old output/input: %s %v", artifact, err)
	}
	t.Logf("MCP resume same_run=%s previous=%s source_hash=%s real_python=true", folder, previous, current["source_snapshot_hash"])
}
