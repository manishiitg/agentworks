package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/guidancesessions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/pkoukk/tiktoken-go"
)

func guidanceTestHandle(t *testing.T, body map[string]any) string {
	t.Helper()
	receipt, _ := body["guidance"].(map[string]any)
	handle, _ := receipt["guidance_session_id"].(string)
	if handle == "" {
		t.Fatal("full skill delivery needs a guidance session", body)
	}
	return handle
}

func guidanceTestCall(t *testing.T, api *StreamingAPI, claims *UserClaims, name string, args map[string]any, handle string, status int) map[string]any {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"name": name, "arguments": args, "guidance_session_id": handle})
	req := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", strings.NewReader(string(data)))
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
	w := httptest.NewRecorder()
	api.handleExternalCall(w, req)
	return externalTestBody(t, w, status)
}

// Real selected-schema preparation, mutation/file reads and persistent receipts.
// The existing dashboard native test covers CLI, stdio and HTTP MCP adapters.
func TestExternalGuidanceCatalogAndWorkflowSkills(t *testing.T) {
	f := newExternalToolsFixture(t)
	withProjectOwnerRegistry(t)
	t.Setenv("AUTH_SECRET", "catalog-guidance-test-secret")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
	claims := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "catalog-guidance", Scopes: []string{"workflows:read", "files:read", "runs:execute", "builder:chat"}, WorkflowIDs: []string{"invoices"}}}
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		t.Fatal(err)
	}
	for _, topic := range externalGuidanceTopics {
		for _, status := range []string{"delivered_to_client", "missing", "stale", "unavailable"} {
			hint := externalCompactGuidance(guidancesessions.Receipt{Topics: []guidancesessions.Topic{{Name: topic.Name, Version: externalTopicVersionFor(claims, topic), Status: status}}})
			raw, _ := json.Marshal(map[string]any{"guidance": hint})
			if count := len(encoder.Encode(string(raw), nil, nil)) - 1; count > 25 {
				t.Fatalf("%s/%s reminder costs %d reference tokens, want <=25", topic.Name, status, count)
			}
		}
	}
	for _, tool := range catalog {
		if !externalTokenAllows(claims, tool) {
			continue
		}
		if len(externalSchemaGuidance(claims, tool.Name)) == 0 {
			t.Fatalf("no relevant canonical/common guidance for %s", tool.Name)
		}
	}
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/external/v1/tools?names=list_workflows", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		f.api.handleExternalTools(w, req)
		body := externalTestBody(t, w, 200)
		if body["guidance_skills"] != nil || body["guidance_requirements"] == nil {
			t.Fatal("no-handle fallback preparation repeated a common skill package")
		}
	}
	prepare := func(handle string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/external/v1/tools?names=files&names=plan&names=settings&names=list_workflows&guidance_session_id="+handle, nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		f.api.handleExternalTools(w, req)
		return externalTestBody(t, w, 200)
	}
	first := prepare("")
	handle := guidanceTestHandle(t, first)
	if first["guidance_requirements"] == nil || first["guidance_skills"] == nil {
		t.Fatal("selected tools did not prepare canonical guidance")
	}
	// A cold preparation withholds the common fallback until the client returns
	// its handle. That first reuse delivers it exactly once; later calls reuse it.
	for _, raw := range first["guidance_skills"].([]any) {
		if raw.(map[string]any)["name"] == "agentworks-mcp" {
			t.Fatal("cold preparation did not withhold the common fallback")
		}
	}
	skills, ok := prepare(handle)["guidance_skills"].([]any)
	if !ok || len(skills) != 1 || skills[0].(map[string]any)["name"] != "agentworks-mcp" {
		t.Fatal("first reuse must deliver only the common fallback exactly once")
	}
	if prepare(handle)["guidance_skills"] != nil || guidanceTestHandle(t, prepare("")) == handle {
		t.Fatal("skill bodies repeated or independent conversations shared a handle")
	}
	write := guidanceTestCall(t, f.api, claims, "files", map[string]any{"action": "write", "workflow_id": "invoices", "path": "docs/guidance.txt", "content": "changed", "expected_revision": "missing", "request_id": "guided-file-write"}, handle, 200)
	if !strings.HasSuffix(write["guidance"].(string), " current") || f.read(t, "Workflow/invoices/docs/guidance.txt") != "changed" {
		t.Fatal("prepared file edit lost guidance or native write")
	}
	read := guidanceTestCall(t, f.api, claims, "files", map[string]any{"action": "read", "workflow_id": "invoices", "path": "docs/guidance.txt"}, handle, 200)
	if read["guidance"] != nil {
		t.Fatal("ordinary read gained a reminder")
	}
	f.write(t, "Workflow/invoices/skills/custom/SKILL.md", "---\nname: custom\n---\nUse invoice facts.\n")
	args := map[string]any{"action": "read", "workflow_id": "invoices", "path": "skills/custom/SKILL.md"}
	skill := guidanceTestCall(t, f.api, claims, "files", args, handle, 200)
	resource := skill["guidance_resources"].([]any)[0].(map[string]any)
	topic, version := resource["topic"].(string), resource["version"].(string)
	if resource["coverage"] != "SKILL.md" || guidanceTestHandle(t, skill) != handle {
		t.Fatal("project skill was not recorded in the same context")
	}
	f.write(t, "Workflow/invoices/skills/custom/SKILL.md", "---\nname: custom\n---\nUse revised invoice facts.\n")
	changed := guidanceTestCall(t, f.api, claims, "files", args, handle, 200)["guidance_resources"].([]any)[0].(map[string]any)
	if changed["topic"] != topic || changed["version"] == version {
		t.Fatal("project skill identity/version did not follow its content")
	}
	denied := guidanceTestCall(t, f.api, &UserClaims{UserID: "outsider", Username: "outsider"}, "files", args, handle, 404)
	if denied["guidance_resources"] != nil {
		t.Fatal("receipt granted project access")
	}
	bootstrap := guidanceTestCall(t, f.api, claims, "help", map[string]any{"action": "skill"}, handle, 200)
	entry := bootstrap["guidance_resources"].([]any)[0].(map[string]any)
	if entry["source"] != "server" || entry["coverage"] != "SKILL.md" || guidanceTestHandle(t, bootstrap) != handle {
		t.Fatal("full bootstrap SKILL.md fetch did not record its own delivery")
	}
	t.Cleanup(f.api.closeGuidanceSessions)
}

func TestExternalGuidanceCrewAndBrainSkillDelivery(t *testing.T) {
	t.Run("Crew", func(t *testing.T) {
		t.Setenv("AUTH_SECRET", "crew-guidance-test-secret")
		t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
		f, _ := newExternalCrewCommandFixture(t)
		docs := f.docs
		location := filepath.Join(docs, filepath.FromSlash(externalCommandCrewRoot), "skills", "custom", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(location), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(location, []byte("# Custom Crew instructions\n"), 0600); err != nil {
			t.Fatal(err)
		}
		claims := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "crew-guidance", Scopes: []string{"crews:read"}, CrewIDs: []string{"team"}}}
		args := map[string]any{"action": "read", "crew_id": "team", "path": "skills/custom/SKILL.md"}
		body := guidanceTestCall(t, f.api, claims, "files", args, "", 200)
		handle := guidanceTestHandle(t, body)
		if body["guidance_resources"].([]any)[0].(map[string]any)["source"] != "crew" {
			t.Fatal("Crew skill source missing")
		}
		copy := *claims
		token := *claims.AccessToken
		token.CrewIDs = []string{"alpha"}
		copy.AccessToken = &token
		if denied := guidanceTestCall(t, f.api, &copy, "files", args, handle, 404); denied["guidance"] != nil {
			t.Fatal("receipt bypassed current Crew bounds")
		}
		t.Cleanup(f.api.closeGuidanceSessions)
	})
	t.Run("Brain", func(t *testing.T) {
		api, service := knowledgebaseServerTest(t)
		// Another runtime's empty-key configuration must not bind a direct MCP
		// request to that unrelated project. Named sessions keep their policy.
		common.SetSessionWorkingDir("", t.TempDir())
		t.Cleanup(func() { common.ClearSessionShellConfig("") })
		t.Setenv("AUTH_SECRET", "brain-guidance-test-secret")
		t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
		principal := knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}
		for _, folder := range []map[string]any{
			{"folder_path": "", "name": "Company", "request_id": "guidance-company"},
			{"folder_path": "Company", "name": "Skills", "request_id": "guidance-skills"},
		} {
			if _, err := service.Call(t.Context(), principal, "create_knowledgebase_folder", folder); err != nil {
				t.Fatal(err)
			}
		}
		files := []any{map[string]any{"path": "SKILL.md", "content": "---\nname: custom\n---\nUse the references.\n"}, map[string]any{"path": "references/notes.md", "content": "original"}}
		if _, err := service.CallTool(t.Context(), principal, "brain_skills", map[string]any{"action": "publish", "folder_path": "Company/Skills/custom", "files": files, "request_id": "guidance-publish"}); err != nil {
			t.Fatal(err)
		}
		claims := &UserClaims{UserID: "admin", Username: "admin"}
		args := map[string]any{"action": "get", "folder_path": "Company/Skills/custom"}
		body := guidanceTestCall(t, api, claims, "brain_skills", args, "", 200)
		handle := guidanceTestHandle(t, body)
		resource := body["guidance_resources"].([]any)[0].(map[string]any)
		if resource["coverage"] != "package" || resource["source"] != "brain" {
			t.Fatal("Brain package receipt is incomplete")
		}
		metadata := guidanceTestCall(t, api, claims, "brain_skills", map[string]any{"action": "list"}, handle, 200)
		if metadata["guidance"] != nil || metadata["guidance_resources"] != nil {
			t.Fatal("Brain skill metadata counted as delivery")
		}
		fresh := guidanceTestCall(t, api, claims, "brain_skills", args, "", 200)
		if guidanceTestHandle(t, fresh) == handle {
			t.Fatal("independent Brain conversations shared receipts")
		}
		// Every explicit fetch remains a full reload, even in the same context.
		if reload := guidanceTestCall(t, api, claims, "brain_skills", args, handle, 200); len(reload["result"].(map[string]any)["files"].([]any)) != 2 {
			t.Fatal("receipt suppressed an explicit package reload")
		}
		files[1].(map[string]any)["content"] = "revised references"
		if _, err := service.CallTool(t.Context(), principal, "brain_skills", map[string]any{"action": "publish", "folder_path": "Company/Skills/custom", "files": files, "request_id": "guidance-republish"}); err != nil {
			t.Fatal(err)
		}
		updated := guidanceTestCall(t, api, claims, "brain_skills", args, handle, 200)["guidance_resources"].([]any)[0].(map[string]any)
		if updated["topic"] != resource["topic"] || updated["version"] == resource["version"] {
			t.Fatal("unchanged SKILL.md hid a package reference version change")
		}
		t.Cleanup(api.closeGuidanceSessions)
	})
}
