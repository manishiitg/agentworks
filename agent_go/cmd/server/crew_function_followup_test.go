package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// PLAT-746-499: actual declared admission, durable run and current target tab;
// only the final query handler is replaced so no model or person is invoked.
func TestFunctionUpdateUsesAdmittedRunAndCurrentTarget(t *testing.T) {
	for _, kind := range []string{"own running call", "queued call", "different caller", "revoked audience", "finished call"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
			env := newCrewFunctionEnv(t)
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
			ownerCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
			target, err := resolveTriggerTarget(ownerCtx, &UserClaims{UserID: "other"}, "Gamma")
			if err != nil {
				t.Fatal(err)
			}
			fn := savedFunctionForTest(t, ownerCtx, target, crewFunction{Name: "update_task", Description: "Update task", Instructions: "Complete the task."})
			caller, err := crewTriggerLinkCaller(linkAlphaPath)(ctx)
			if err != nil {
				t.Fatal(err)
			}
			call, err := env.api.startCrewFunctionCall(ctx, "owner", caller, target, fn, nil, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			defer call.finish("completed", "fixture", "")
			if call.RunID == "" {
				t.Fatalf("declared call did not dispatch: %+v", call.snapshot())
			}
			profile, err := env.svc.registry.Resolve("work", 0, "other")
			if err != nil {
				t.Fatal(err)
			}
			key := "gamma" + codeChatSideMarker + crewFunctionTabPrefix + crewAgentChatID(fn.tabName())
			binding, err := resolveProductConversationBinding(ownerCtx, "other", profile, key)
			if err != nil {
				t.Fatal(err)
			}
			conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ownerCtx, "other", profile, binding, "")
			if err != nil {
				t.Fatal(err)
			}
			if kind != "queued call" {
				if err := UpdateScheduleRun(ctx, linkGammaPath, call.RunID, "running", "", nil, "", conversation.SessionID); err != nil {
					t.Fatal(err)
				}
			}
			delivered := 0
			env.api.internalQueryHandler = func(w http.ResponseWriter, r *http.Request) {
				delivered++
				var query QueryRequest
				if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
					t.Fatal(err)
				}
				if r.Header.Get("X-Session-ID") != conversation.SessionID || query.AgentProfileConversationKey != key || GetUserFromContext(r.Context()).UserID != "other" || !strings.Contains(query.Query, call.ID) {
					t.Errorf("update lost actual function tab/owner/call: session=%q key=%q", r.Header.Get("X-Session-ID"), query.AgentProfileConversationKey)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"accepted","delivery_status":"accepted"}`))
			}
			tools := env.alpha
			want := ""
			switch kind {
			case "queued call":
				want = "has not started yet"
			case "different caller":
				tools = env.beta
				want = "only the caller"
			case "revoked audience":
				if err := defaultProjectOwners().SetAudience("work", "gamma", true, nil); err != nil {
					t.Fatal(err)
				}
				want = "access denied"
			case "finished call":
				call.finish("completed", "done", "")
				want = "already finished"
			}
			_, err = tools["ask_function_update"].exec(ctx, map[string]interface{}{"call_id": call.ID, "question": "How is it going?"})
			if want == "" {
				if err != nil || delivered != 1 {
					t.Fatalf("own running update: delivered=%d err=%v", delivered, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), want) || delivered != 0 {
				t.Fatalf("%s: delivered=%d err=%v, want %q", kind, delivered, err, want)
			}
			if kind == "different caller" {
				foreign, err := crewTriggerLinkCaller(linkBetaPath)(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := env.api.sendToCrewTriggerRun(ctx, "owner", foreign, target, fn.TriggerID, call.RunID, "wrong caller"); err == nil || !strings.Contains(err.Error(), "admitted caller") || delivered != 0 {
					t.Fatalf("different caller borrowed run admission: delivered=%d err=%v", delivered, err)
				}
			}
		})
	}
}
