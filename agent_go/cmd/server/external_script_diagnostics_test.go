package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Stored helper reply paths are data, not permission to read another run or
// bypass a token's file exclusion. Real provider execution is tested elsewhere.
func TestExternalScriptAgentPathsAndLogTailsKeepRunBounds(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	owner := crewWriter("owner")
	crewCapabilityHTTP(t, f, owner, "workspace", map[string]any{"action": "automation_create", "kind": "script", "name": "Log fixture"}, 200)
	bindings, err := readCrewExecutables()
	if err != nil || len(bindings) != 1 {
		t.Fatalf("binding %v %v", bindings, err)
	}
	var script crewExecutableBinding
	for _, binding := range bindings {
		script = binding
	}
	const run = "run-diagnosis"
	put := func(name, content string) string {
		t.Helper()
		physical, _, err := crewroot.Resolve(f.docs, script.Root+"/"+name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(physical), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(physical, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return physical
	}
	logPath := "runs/" + run + "/execution/saved/script-output-1.log"
	physicalLog := put(logPath, strings.Repeat("x", 2*wf.MaxFileBytes)+"\nTraceback: failure-marker\n")
	answerPath := "runs/" + run + "/helpers/answers/one.txt"
	answer := put(answerPath, "Answer fixture")
	providerPath := "runs/" + run + "/logs/helpers/one-provider.log"
	provider := put(providerPath, "Provider fixture")
	one, _ := json.Marshal(map[string]any{"reply": map[string]any{"ok": true, "answer_file": answer, "log_file": provider, "log_kind": "provider_transcript", "conversation_id": "one"}})
	put("runs/"+run+"/helpers/scope/one.json", string(one))
	other, _, _ := crewroot.Resolve(f.docs, script.Root+"/runs/run-other/helpers/answers/other.txt")
	two, _ := json.Marshal(map[string]any{"reply": map[string]any{"answer_file": other, "log_file": "/other-project/provider.log", "log_kind": "provider_transcript", "conversation_id": "two"}})
	put("runs/"+run+"/helpers/scope/two.json", string(two))
	ctx := context.WithValue(t.Context(), UserContextKey, owner)
	log, err := externalScriptLogTail(ctx, script.Root, run, logPath, externalScriptLogTailBytes)
	if err != nil || !log.Truncated || len(log.Tail) > externalScriptLogTailBytes || !strings.Contains(log.Tail, "failure-marker") {
		t.Fatalf("bounded large log: %+v %v", log, err)
	}
	rows, err := externalScriptAgentResults(ctx, script.Root, run)
	if err != nil || len(rows) != 2 {
		t.Fatalf("agent projections %v %v", rows, err)
	}
	for _, row := range rows {
		if row["conversation_id"] == "one" && (row["answer_file"] != answerPath || row["provider_log"] != providerPath) {
			t.Fatalf("valid result lost paths: %v", row)
		}
		if row["conversation_id"] == "two" && (row["answer_file"] != nil || row["provider_log"] != nil) {
			t.Fatalf("another run/project path admitted: %v", row)
		}
	}
	owner.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"runs/" + run}, BlockedPaths: []string{answerPath, logPath}}
	if _, err := externalScriptLogTail(ctx, script.Root, run, logPath, externalScriptLogTailBytes); err == nil {
		t.Fatal("excluded log allowed")
	}
	rows, err = externalScriptAgentResults(ctx, script.Root, run)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row["answer_file"] != nil {
			t.Fatalf("excluded answer path exposed: %v", row)
		}
	}
	owner.AccessToken.FileGuard = nil
	if err := os.Remove(physicalLog); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(provider, physicalLog); err != nil {
		t.Fatal(err)
	}
	if _, err := externalScriptLogTail(ctx, script.Root, run, logPath, externalScriptLogTailBytes); err == nil {
		t.Fatal("symlink substituted for log was followed")
	}
}
