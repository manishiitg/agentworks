package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-1008: the actual HTTP inventory and dispatch resolver share the
// current owned runtime, not a browser path, an old session or imported names.
func TestExplicitNativeSkillUsesCurrentOwnedRuntimeAtDispatch(t *testing.T) {
	state := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	f, _ := newExternalCrewCommandFixture(t)
	registry := map[string]any{}
	for _, sid := range []string{"current", "another"} {
		registry["work:"+sid] = map[string]any{"profile_id": "work", "session_id": sid, "workspace_path": externalCommandCrewRoot, "provider": "codex-cli"}
		dir, err := cliruntime.PrepareLinkedProject(state, f.docs, "owner", codingAgentWorkspaceWorkingDir(externalCommandCrewRoot), sid, "codex-cli", "builder")
		if err != nil {
			t.Fatal(err)
		}
		recognitionWrite(t, filepath.Join(dir, ".agents/skills/review/SKILL.md"), "---\nname: review\ndescription: Review changes\n---\nRead the diff.")
		recognitionWrite(t, filepath.Join(dir, ".agents/skills/internal/SKILL.md"), "---\nname: internal\ndescription: Internal\nuser-invocable: false\n---\nNot explicit.")
		runtime := ChatHistoryAgentRuntime{Provider: "codex-cli", WorkshopMode: "builder", WorkspacePath: externalCommandCrewRoot, AgentSessionHandle: &mcpagent.AgentSessionHandle{Provider: llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", Transport: "app-server", WorkingDir: dir, NativeSessionID: sid}}}
		body, _ := json.Marshal(map[string]any{"session_id": sid, "user_id": "owner", "runtime": runtime})
		file, ok := workProjectChatHistoryConversationPath("owner", externalCommandCrewRoot, sid, time.Now())
		if !ok {
			t.Fatal("fixture has no own history path")
		}
		f.write(t, file, string(body))
	}
	raw, _ := json.Marshal(map[string]any{"version": 1, "entries": registry})
	f.write(t, productConversationRegistryPath("owner"), string(raw))
	request := func(user, sid string) *http.Request {
		r := httptest.NewRequest("GET", "/api/skills/invocation?workspace_path="+url.QueryEscape(externalCommandCrewRoot)+"&session_id="+sid, nil)
		return r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
	}
	w := httptest.NewRecorder()
	f.api.ownNativeSkillRoute(false, f.api.nativeSkillCatalogHandler)(w, request("owner", "current"))
	var catalog nativeSkillCatalog
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &catalog) != nil || !catalog.Supported || len(catalog.Skills) != 1 || catalog.Skills[0].Name != "review" {
		t.Fatalf("usable inventory %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), state) || strings.Contains(w.Body.String(), "SKILL.md") {
		t.Fatal("inventory exposed host path")
	}
	id := catalog.Skills[0].ID
	req := QueryRequest{Query: "Inspect changes", SelectedFolder: externalCommandCrewRoot, RequestedSkillID: id}
	r, err := f.api.prepareRequestedSkill(request("owner", "current"), &req, "current", "codex-cli")
	selected, ok := llmtypes.NativeSkillForSession(r.Context(), "current")
	if err != nil || !ok || selected.Name != "review" || !filepath.IsAbs(selected.Path) {
		t.Fatalf("native dispatch: %v %+v", err, selected)
	}
	for _, tc := range []struct{ user, sid, provider string }{{"other", "current", "codex-cli"}, {"owner", "another", "codex-cli"}, {"owner", "old", "codex-cli"}, {"owner", "current", "claude-code"}} {
		copy := req
		if _, err := f.api.prepareRequestedSkill(request(tc.user, tc.sid), &copy, tc.sid, tc.provider); err == nil {
			t.Fatalf("stale/foreign selection accepted: %+v", tc)
		}
	}
	// Revocation in the real prepared file must be observed again at dispatch.
	recognitionWrite(t, selected.Path, "---\nname: review\ndescription: Review changes\nuser-invocable: false\n---\nNo longer explicit.")
	if _, err := f.api.prepareRequestedSkill(request("owner", "current"), &req, "current", "codex-cli"); err == nil {
		t.Fatal("dispatch trusted the earlier list")
	}
	w = httptest.NewRecorder()
	f.api.ownNativeSkillRoute(false, f.api.nativeSkillCatalogHandler)(w, request("other", "current"))
	if w.Code != 403 {
		t.Fatalf("foreign inventory: %d %s", w.Code, w.Body.String())
	}
}

// One selected skill never leaks into another row or an automatic continuation.
func TestExplicitSkillQueueIdentityAndContinuation(t *testing.T) {
	api := &StreamingAPI{}
	req := QueryRequest{Query: "Review", RequestedSkillID: "exact-selection", requestedSkillName: "review"}
	if api.queuedBatchEligible(queuedConversationTurn{Request: req}) {
		t.Fatal("explicit selection may be combined")
	}
	if next := restartContinueRequest(context.Background(), req); next.RequestedSkillID != "" || next.requestedSkillName != "" {
		t.Fatal("restart repeated skill")
	}
	other := req
	other.RequestedSkillID = "other-selection"
	if pendingAttachmentSubmissionQuery(req) == pendingAttachmentSubmissionQuery(other) {
		t.Fatal("submission identity lost explicit skill")
	}
	raw, _ := json.Marshal(sanitizedQueuedConversationRequest(context.Background(), req))
	if !strings.Contains(string(raw), `"requested_skill_id":"exact-selection"`) || strings.Contains(string(raw), `requestedSkillName`) {
		t.Fatal("queue lost selector or persisted a resolved capability")
	}
}
