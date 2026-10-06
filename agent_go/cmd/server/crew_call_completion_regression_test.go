package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Exercise the caller tool, durable trigger/run record, runtime reader,
// supervisor and notification watcher together. Only the model's answer is
// supplied by the test; no paid model or live user conversation is invoked.
func TestCodeAskSharedCrewCompletesOrReportsRevocation(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "answer delivered", true: "binding revoked"}[revoke], func(t *testing.T) {
			env := newCrewFunctionEnv(t)
			t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
			resetCrewLocationCaches()
			t.Cleanup(resetCrewLocationCaches)
			const targetRoot = "Crew/beta"
			const sourceRoot = "_users/owner/Chats/Code/projects/source"
			if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "beta", OwnerID: "owner", ProjectID: "beta", Shared: true, Aliases: []string{linkBetaPath}}); err != nil {
				t.Fatal(err)
			}
			env.mock.mu.Lock()
			env.mock.files[targetRoot+"/product.json"] = env.mock.files[linkBetaPath+"/product.json"]
			delete(env.mock.files, linkBetaPath+"/product.json")
			env.mock.files[sourceRoot+"/product.json"] = `{"schema_version":1,"product":"code","id":"source","title":"Source","session_id":"code:project:source"}`
			env.mock.mu.Unlock()
			profile, err := env.svc.registry.Resolve("work", 0, "owner")
			if err != nil {
				t.Fatal(err)
			}
			profile.ID, profile.Product = "code", "code"
			profile.Runtime.Workspace.ProjectsRoot = workspaceref.CodeProjectsRoot
			if err := env.svc.registry.RegisterProfile(profile); err != nil {
				t.Fatal(err)
			}
			tools := env.functionTools(t, sourceRoot, "sess-caller", []string{targetRoot})
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
			out, err := tools["call_function"].exec(ctx, map[string]interface{}{"target": "Beta", "function": "ask", "args": map[string]interface{}{"message": "Summarize the bugs"}, "wait_seconds": 0})
			if err != nil {
				t.Fatal(err)
			}
			call := lookupCrewFunctionCall(decodeToolJSON(t, out)["call_id"].(string))
			if _, _, err := ReadWorkflowManifest(ctx, targetRoot); err != nil {
				t.Fatal(err)
			}
			env.svc.mu.Lock()
			for _, queue := range env.svc.queued {
				for _, delivery := range queue {
					if delivery.options.RunID == call.RunID && delivery.job.Schedule.Isolated {
						t.Error("same-owner Code call was isolated")
					}
				}
			}
			env.svc.mu.Unlock()
			if revoke {
				_, binding, manifest, err := env.svc.projectManifest(ctx, "owner", "work", "beta")
				if err != nil {
					t.Fatal(err)
				}
				manifest.Triggers = nil
				if err := env.svc.writeProjectManifest(ctx, binding, manifest); err != nil {
					t.Fatal(err)
				}
			} else if err := UpdateScheduleRunResult(ctx, targetRoot, call.RunID, ScheduleRunCompletion{Status: "success", SessionID: "sess-beta", FinalResponse: "Three open bugs."}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-call.done:
			case <-time.After(3 * time.Second):
				t.Fatal("function call never settled")
			}
			result := call.snapshot()
			if revoke {
				if result["status"] != "failed" || !strings.Contains(result["error"].(string), "trigger not found") {
					t.Fatalf("revoked call = %v", result)
				}
			} else if result["status"] != "completed" || result["result"].(map[string]interface{})["answer"] != "Three open bugs." {
				t.Fatalf("answer = %v", result)
			}
			// The caller's background footer reads this same registry.
			deadline := time.Now().Add(time.Second)
			for {
				watchers := env.api.bgAgentRegistry.GetAll("sess-caller")
				env.mock.mu.Lock()
				var persisted map[string]interface{}
				_ = json.Unmarshal([]byte(env.mock.files[call.recordPath()]), &persisted)
				env.mock.mu.Unlock()
				if persisted["status"] == result["status"] && len(watchers) == 1 && (watchers[0].GetSnapshot().Status == BGAgentCompleted || watchers[0].GetSnapshot().Status == BGAgentFailed) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("caller background watcher stayed running")
				}
				time.Sleep(10 * time.Millisecond)
			}
			env.mock.mu.Lock()
			var saved map[string]interface{}
			_ = json.Unmarshal([]byte(env.mock.files[call.recordPath()]), &saved)
			env.mock.mu.Unlock()
			if saved["status"] != result["status"] {
				t.Fatalf("saved call did not settle: %v", saved)
			}
		})
	}
}
