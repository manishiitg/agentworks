package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func patClaims(user string, scopes []string) *UserClaims {
	return &UserClaims{UserID: user, Username: user, AccessToken: &accesstokens.Token{ID: "test-token", Scopes: scopes, AllWorkflows: true}}
}

func TestExternalKnowledgeInventoryPagination(t *testing.T) {
	f := newExternalToolsFixture(t)
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md"} {
		f.write(t, "Workflow/invoices/knowledgebase/"+name, "# "+name)
	}
	first := externalTestBody(t, f.call(t, "owner", "list_workflow_knowledge", map[string]any{"workflow_id": "invoices", "limit": 2}), 200)
	if len(first["knowledgebase"].([]any)) != 2 || first["has_more"] != true || first["next_offset"] != float64(2) || first["truncated"] != true {
		t.Fatalf("first knowledge page hides more files: %v", first)
	}
	second := externalTestBody(t, f.call(t, "owner", "list_workflow_knowledge", map[string]any{"workflow_id": "invoices", "limit": 2, "offset": 2}), 200)
	if len(second["knowledgebase"].([]any)) != 2 || second["has_more"] != false {
		t.Fatalf("second knowledge page is incomplete: %v", second)
	}
}

func TestExternalGuidanceTopicScopeSplit(t *testing.T) {
	api := &StreamingAPI{}
	// A read-only token sees canonical guidance but not workflow knowledge.
	claims := patClaims("reader", []string{"workflows:read"})
	w := httptest.NewRecorder()
	api.handleExternalTools(w, adminRequest(http.MethodGet, "/api/external/tools", "", claims, nil))
	body := externalTestBody(t, w, 200)
	names := map[string]bool{}
	for _, raw := range body["tools"].([]any) {
		names[raw.(map[string]any)["name"].(string)] = true
	}
	for _, name := range []string{"help"} {
		if !names[name] {
			t.Fatalf("workflows:read token missing %s", name)
		}
	}
	for _, name := range []string{"list_workflow_knowledge", "read_workflow_knowledge", "read_file", "get_file_link"} {
		if names[name] {
			t.Fatalf("workflows:read token must not see %s", name)
		}
	}
}

func TestExternalAgentContextGlobalAndScoped(t *testing.T) {
	f := newExternalToolsFixture(t)
	// No workflow required.
	body := externalTestBody(t, f.call(t, "owner", "get_agent_context", map[string]any{}), 200)
	if body["guidance_version"] != externalGuidanceVersionFor(&UserClaims{UserID: "owner", Username: "owner"}) {
		t.Fatalf("missing guidance version: %v", body)
	}
	prep, _ := body["preparation"].([]any)
	if len(prep) == 0 {
		t.Fatalf("missing preparation: %v", body)
	}
	// Scoped role reporting.
	body = externalTestBody(t, f.call(t, "owner", "get_agent_context", map[string]any{"workflow_id": "invoices"}), 200)
	if body["role"] != string(WorkflowAccessOwner) {
		t.Fatalf("wrong role: %v", body)
	}
	// A reader sees the same token-level tools; with no mutations exposed,
	// the effective list equals the available list.
	body = externalTestBody(t, f.call(t, "reader", "get_agent_context", map[string]any{"workflow_id": "invoices"}), 200)
	if body["role"] != string(WorkflowAccessRead) {
		t.Fatalf("wrong role: %v", body)
	}
	effective := map[string]bool{}
	for _, raw := range body["effective_tools"].([]any) {
		effective[raw.(string)] = true
	}
	if !effective["workflow"] || !effective["files"] {
		t.Fatalf("wrong effective tools: %v", effective)
	}
	unknown := f.call(t, "owner", "get_agent_context", map[string]any{"workflow_id": "nope"})
	externalTestBody(t, unknown, 404)
}

func TestExternalGuidanceTopicsRoundTrip(t *testing.T) {
	f := newExternalToolsFixture(t)
	body := externalTestBody(t, f.call(t, "owner", "help", map[string]any{"action": "topics"}), 200)
	topics, _ := body["topics"].([]any)
	if len(topics) < 9 {
		t.Fatalf("got %d topics, want at least the 9 original ones", len(topics))
	}
	original := map[string]bool{}
	for _, raw := range topics {
		original[raw.(map[string]any)["name"].(string)] = true
	}
	for _, name := range []string{"dashboard-authoring", "motion-guide", "schedules", "skill-management", "file-layout", "secure-share-links"} {
		if !original[name] {
			t.Fatalf("original reference lost during source migration: %s", name)
		}
	}
	for _, raw := range topics {
		topic, _ := raw.(map[string]any)
		if topic["version"] == "" || topic["version"] == nil {
			t.Fatalf("topic without a version: %v", topic)
		}
	}
	body = externalTestBody(t, f.call(t, "owner", "help", map[string]any{"action": "topic", "topic": "schedules"}), 200)
	content, _ := body["content"].(string)
	if !strings.Contains(content, "cron") {
		t.Fatalf("topic content not rendered from canonical reference: %.120s", content)
	}
	if _, ok := body["external_note"]; !ok {
		t.Fatal("missing external mapping note")
	}
	// A client holding the current version is told so without the text.
	version, _ := body["version"].(string)
	if version == "" {
		t.Fatal("topic has no version")
	}
	same := externalTestBody(t, f.call(t, "owner", "help", map[string]any{"action": "topic", "topic": "schedules", "known_version": version}), 200)
	if same["unchanged"] != true || same["content"] != nil {
		t.Fatalf("unchanged topic was resent: %v", same)
	}
	// PLAT-825-113: an area router fetch must not require loading every procedure.
	area := externalTestBody(t, f.call(t, "owner", "help", map[string]any{"action": "topic", "topic": "panel-dashboard"}), 200)
	path := "references/dashboard/SKILL.md"
	ref := externalTestBody(t, f.call(t, "owner", "help", map[string]any{"action": "topic", "topic": "panel-dashboard", "path": path, "known_version": area["version"]}), 200)
	if ref["path"] != path || ref["content"] == "" || ref["content"] == area["content"] || ref["unchanged"] == true {
		t.Fatalf("reference was not independently readable: %v", ref)
	}
	externalTestBody(t, f.call(t, "owner", "help", map[string]any{"action": "topic", "topic": "panel-dashboard", "path": "../workflow.json"}), 404)
	// A topic outside the external profile is rejected even though it exists
	// in the builder reference.
	unknown := f.call(t, "owner", "help", map[string]any{"action": "topic", "topic": "stores"})
	denied := externalTestBody(t, unknown, 404)
	if denied["error"].(map[string]any)["code"] != "unknown_topic" {
		t.Fatalf("wrong error: %v", denied)
	}
}

func TestExternalWorkflowKnowledgeListsAndReads(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoice processing","created_by":"owner","access":{"owners":["owner","readonly-owner"],"readers":["reader"]},"capabilities":{"selected_servers":["accounting"],"selected_skills":["invoice-skill","ghost-skill"]}}`)
	f.write(t, "Workflow/invoices/planning/step_config.json", `{"steps":[{"id":"fetch-invoices","agent_configs":{"enabled_skills":["step-skill"]}}]}`)
	f.write(t, "Workflow/invoices/learnings/_global/SKILL.md", "# Invoice know-how\n")
	f.write(t, "Workflow/invoices/knowledgebase/notes/pricing.md", "# Pricing\n")
	f.write(t, "skills/invoice-skill/SKILL.md", "# Invoice skill\n")
	f.write(t, "skills/step-skill/SKILL.md", "# Step skill\n")
	f.write(t, "skills/other-skill/SKILL.md", "# Unrelated skill\n")
	body := externalTestBody(t, f.call(t, "owner", "list_workflow_knowledge", map[string]any{"workflow_id": "invoices"}), 200)
	for _, key := range []string{"learnings", "knowledgebase", "selected_skills", "step_skills", "workspace_skills"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("missing key %s: %v", key, body)
		}
	}
	// Only the workflow's selected and step-enabled skills are visible, and a
	// populated catalog produces no warnings.
	rawSkills, _ := json.Marshal(body["workspace_skills"])
	if string(rawSkills) != `["invoice-skill","step-skill"]` {
		t.Fatalf("wrong visible skills %s", rawSkills)
	}
	if _, warned := body["warnings"]; warned {
		t.Fatalf("unexpected warnings: %v", body)
	}
	read := externalTestBody(t, f.call(t, "owner", "read_workflow_knowledge", map[string]any{"workflow_id": "invoices", "path": "learnings/_global/SKILL.md"}), 200)
	if content, _ := read["content"].(string); !strings.Contains(content, "Invoice know-how") {
		t.Fatalf("wrong knowledge content: %v", read)
	}
	skill := externalTestBody(t, f.call(t, "owner", "read_workflow_knowledge", map[string]any{"workflow_id": "invoices", "path": "skills/invoice-skill/SKILL.md"}), 200)
	if content, _ := skill["content"].(string); !strings.Contains(content, "Invoice skill") {
		t.Fatalf("wrong skill content: %v", skill)
	}
	// A step-only skill (production {"steps": [...]} shape) reads too.
	stepSkill := externalTestBody(t, f.call(t, "owner", "read_workflow_knowledge", map[string]any{"workflow_id": "invoices", "path": "skills/step-skill/SKILL.md"}), 200)
	if content, _ := stepSkill["content"].(string); !strings.Contains(content, "Step skill") {
		t.Fatalf("wrong step skill content: %v", stepSkill)
	}
	// A skill the workflow does not use is denied even though it exists.
	unrelated := f.call(t, "owner", "read_workflow_knowledge", map[string]any{"workflow_id": "invoices", "path": "skills/other-skill/SKILL.md"})
	if code := externalTestBody(t, unrelated, 403)["error"].(map[string]any)["code"]; code != "forbidden" {
		t.Fatalf("wrong code %v", code)
	}
	// A selected skill with no files is a clean 404, not a server error.
	missing := f.call(t, "owner", "read_workflow_knowledge", map[string]any{"workflow_id": "invoices", "path": "skills/ghost-skill/SKILL.md"})
	if code := externalTestBody(t, missing, 404)["error"].(map[string]any)["code"]; code != "not_found" {
		t.Fatalf("wrong code %v", code)
	}
	// Plan files are not addressable through the knowledge tool.
	denied := f.call(t, "owner", "read_workflow_knowledge", map[string]any{"workflow_id": "invoices", "path": "planning/plan.json"})
	if code := externalTestBody(t, denied, 403)["error"].(map[string]any)["code"]; code != "protected_path" {
		t.Fatalf("wrong code %v", code)
	}
	// Traversal outside the skill catalog is rejected before any file access.
	escape := f.call(t, "owner", "read_workflow_knowledge", map[string]any{"workflow_id": "invoices", "path": "skills/../../workflow.json"})
	externalTestBody(t, escape, 400)
}

func TestExternalWorkflowKnowledgeRequiresFilesRead(t *testing.T) {
	f := newExternalToolsFixture(t)
	// A workflow reader with a normal session can read knowledge, exactly
	// like read_file. The scope gate below applies to PAT callers. This
	// fixture has no skills directory, so the response must say the catalog
	// is unavailable rather than presenting an empty workspace.
	unwarned := externalTestBody(t, f.call(t, "reader", "list_workflow_knowledge", map[string]any{"workflow_id": "invoices"}), 200)
	if _, ok := unwarned["warnings"]; !ok {
		t.Fatalf("missing catalog warning: %v", unwarned)
	}
	// Same call with an explicit PAT lacking files:read is rejected on scope,
	// proving knowledge reads cannot smuggle workflow content past the token.
	w := httptest.NewRecorder()
	data := `{"name":"files","arguments":{"action":"knowledge_list","workflow_id":"invoices"}}`
	f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", data, patClaims("reader", []string{"workflows:read"}), nil))
	scoped := externalTestBody(t, w, 403)
	if scoped["error"].(map[string]any)["code"] != "insufficient_scope" {
		t.Fatalf("wrong error: %v", scoped)
	}
	w = httptest.NewRecorder()
	f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", data, patClaims("reader", []string{"workflows:read", "files:read"}), nil))
	externalTestBody(t, w, 200)
}
