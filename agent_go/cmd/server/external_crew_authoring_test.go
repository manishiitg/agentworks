package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func crewWriter(userID string) *UserClaims {
	return &UserClaims{UserID: userID, Username: userID, AccessToken: &accesstokens.Token{Name: "laptop", Scopes: []string{"crews:read", "crews:write"}, AllCrews: true}}
}

func crewAuthoringFile(t *testing.T, env triggerLinkEnv, path string) string {
	t.Helper()
	env.mock.mu.Lock()
	defer env.mock.mu.Unlock()
	return env.mock.files[path]
}

func TestExternalCrewAuthoringRoundTrip(t *testing.T) {
	ensureProjectStateRoot(t)
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","can_create":true},{"id":"viewer","username":"viewer","role":"viewer","products":["work"]}]}`)
	owner, other := crewWriter("owner"), crewWriter("other")

	create := map[string]any{
		"name": "Support Triage", "icon": "🛟", "role": "Support triage lead",
		"purpose":         "Triage inbound tickets and route them to the right team.",
		"selected_skills": []any{"triage"},
		"files":           map[string]any{"skills/triage/SKILL.md": "---\nname: triage\n---\nSort tickets by severity.\n"},
		"functions": []any{map[string]any{
			"name": "triage_ticket", "description": "Classify one ticket.", "instructions": "Read the ticket and return a severity.",
			"input_schema":  map[string]any{"type": "object", "required": []any{"ticket"}, "properties": map[string]any{"ticket": map[string]any{"type": "string"}}},
			"result_schema": map[string]any{"type": "object", "properties": map[string]any{"severity": map[string]any{"type": "string"}}},
		}},
		"schedules": []any{map[string]any{"name": "Morning sweep", "message": "Triage the overnight queue.", "cron_expression": "0 9 * * 1-5", "timezone": "Asia/Kolkata"}},
	}
	code, out := externalCrewRequest(t, env, owner, "create_crew", create)
	if code != 200 || out["created"] != true {
		t.Fatalf("create_crew = %d %v", code, out)
	}
	crewID, _ := out["crew_id"].(string)
	if out["role"] != "Support triage lead" || !strings.HasPrefix(out["purpose"].(string), "Triage inbound") {
		t.Fatalf("created crew must carry role and purpose: %v", out)
	}
	access, _ := out["access"].(map[string]any)
	if access["can_edit"] != true || access["your_access"] != "owner" {
		t.Fatalf("creator must own the new Crew: %v", access)
	}
	root := "Crew/support-triage-" + crewID[:8]
	var product map[string]any
	if err := json.Unmarshal([]byte(crewAuthoringFile(t, env, root+"/product.json")), &product); err != nil {
		t.Fatalf("product.json at the UI layout path: %v", err)
	}
	if identity := product["identity"].(map[string]any); identity["name"] != "Support Triage" || identity["icon"] != "🛟" || identity["role"] != "Support triage lead" {
		t.Fatalf("identity = %v", identity)
	}
	runtime := crewAuthoringFile(t, env, root+"/workflow.json")
	if !strings.Contains(runtime, `"triage"`) || !strings.Contains(runtime, "Morning sweep") || strings.Contains(runtime, "Workflow/") {
		t.Fatalf("workflow.json must hold skills, schedules and no workflow context: %s", runtime)
	}
	if !strings.Contains(crewAuthoringFile(t, env, root+"/functions.json"), "triage_ticket") {
		t.Fatal("functions.json must hold the declared function")
	}

	// list_crews and get_crew report the identity object (was null).
	_, listed := externalCrewRequest(t, env, owner, "list_crews", map[string]any{"query": "support"})
	rows, _ := listed["crews"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["identity"].(map[string]any)["name"] != "Support Triage" {
		t.Fatalf("list_crews identity = %v", listed)
	}
	code, got := externalCrewRequest(t, env, owner, "get_crew", map[string]any{"crew_id": crewID})
	schedules, _ := got["schedules"].([]any)
	if code != 200 || len(schedules) != 1 || len(got["skills"].([]any)) != 1 {
		t.Fatalf("get_crew = %d %v", code, got)
	}
	scheduleID := schedules[0].(map[string]any)["id"].(string)

	// Only the owner edits; a reader gets a clear refusal and nothing changes.
	if code, out := externalCrewRequest(t, env, other, "update_crew", map[string]any{"crew_id": crewID, "role": "Hijacked"}); code != 403 {
		t.Fatalf("non-owner update = %d %v", code, out)
	}
	code, got = externalCrewRequest(t, env, other, "get_crew", map[string]any{"crew_id": crewID})
	if code != 200 || got["role"] != "Support triage lead" || got["access"].(map[string]any)["can_edit"] != false {
		t.Fatalf("reader view = %d %v", code, got)
	}

	// A bad section refuses the whole update before anything is written.
	if code, _ := externalCrewRequest(t, env, owner, "update_crew", map[string]any{"crew_id": crewID, "role": "Changed", "schedules": map[string]any{"update": []any{map[string]any{"id": "missing", "enabled": false}}}}); code != 400 {
		t.Fatalf("unknown schedule must refuse the update, got %d", code)
	}
	if _, got := externalCrewRequest(t, env, owner, "get_crew", map[string]any{"crew_id": crewID}); got["role"] != "Support triage lead" {
		t.Fatalf("refused update must not change the role: %v", got["role"])
	}

	code, out = externalCrewRequest(t, env, owner, "update_crew", map[string]any{
		"crew_id": crewID, "role": "Escalation lead", "purpose": "Escalate the hard tickets.",
		"functions": map[string]any{"upsert": []any{map[string]any{"name": "escalate", "description": "Escalate a ticket.", "instructions": "Page the on-call owner."}}},
		"schedules": map[string]any{
			"update": []any{map[string]any{"id": scheduleID, "enabled": false, "run_destination": "isolated"}},
			"add":    []any{map[string]any{"name": "Weekly report", "message": "Summarize the week.", "cadence_hours": 168}},
		},
		"files": map[string]any{"notes/runbook.md": "Escalation runbook"},
		"return_spec": true,
	})
	if code != 200 || out["role"] != "Escalation lead" || out["purpose"] != "Escalate the hard tickets." {
		t.Fatalf("update_crew = %d %v", code, out)
	}
	if fns := out["functions"].([]any); len(fns) != 3 { // ask + triage_ticket + escalate
		t.Fatalf("functions after upsert = %v", fns)
	}
	schedules = out["schedules"].([]any)
	if len(schedules) != 2 || schedules[0].(map[string]any)["enabled"] != false || schedules[0].(map[string]any)["run_destination"] != "isolated" {
		t.Fatalf("schedules after update = %v", schedules)
	}
	if crewAuthoringFile(t, env, root+"/notes/runbook.md") != "Escalation runbook" {
		t.Fatal("update_crew must write project files")
	}
	// Without return_spec the reply is a summary: names and what changed, not every function's instructions (PLAT-837).
	code, out = externalCrewRequest(t, env, owner, "update_crew", map[string]any{"crew_id": crewID, "files": map[string]any{"notes/second.md": "x"}})
	if code != 200 || out["updated"] != true || len(out["files_written"].([]any)) != 1 || len(out["functions"].([]any)) != 3 {
		t.Fatalf("compact update_crew reply = %d %v", code, out)
	}
	if _, has := out["purpose"]; has {
		t.Fatalf("the compact reply must not carry the whole spec: %v", out)
	}
	if first := out["functions"].([]any)[0]; first == nil || fmt.Sprintf("%T", first) != "string" {
		t.Fatalf("the compact reply lists function names, got %v", out["functions"])
	}
	// A retry after a dropped connection is safe: the same add is not added twice, and removing a gone id is not an error.
	code, out = externalCrewRequest(t, env, owner, "update_crew", map[string]any{"crew_id": crewID, "return_spec": true, "schedules": map[string]any{
		"add":    []any{map[string]any{"name": "Weekly report", "message": "Summarize the week.", "cadence_hours": 168}},
		"remove": []any{"already-gone"},
	}})
	if code != 200 || len(out["schedules"].([]any)) != 2 {
		t.Fatalf("a retried add must not duplicate the schedule: %d %v", code, out["schedules"])
	}
	for _, private := range []string{"product.json", "functions.json", "db/x.sqlite", "builder/conversation/a.json", "../escape.md"} {
		if code, _ := externalCrewRequest(t, env, owner, "update_crew", map[string]any{"crew_id": crewID, "files": map[string]any{private: "x"}}); code != 400 {
			t.Fatalf("private file %q must be refused, got %d", private, code)
		}
	}

	// Export carries skill files and function instructions: owner only.
	if code, out := externalCrewRequest(t, env, other, "export_crew", map[string]any{"crew_id": crewID}); code != 403 {
		t.Fatalf("a non-owner export must be refused, got %d %v", code, out)
	}
	// Export -> import on another account reproduces the Crew, schedules off.
	code, exported := externalCrewRequest(t, env, owner, "export_crew", map[string]any{"crew_id": crewID})
	spec, _ := exported["spec"].(map[string]any)
	if code != 200 || spec["kind"] != crewSpecKind || spec["files"].(map[string]any)["skills/triage/SKILL.md"] == nil {
		t.Fatalf("export_crew = %d %v", code, exported)
	}
	if _, leaked := spec["files"].(map[string]any)["notes/runbook.md"]; leaked {
		t.Fatal("export ships only the skill files the Crew selects")
	}
	code, imported := externalCrewRequest(t, env, other, "import_crew", map[string]any{"spec": spec})
	if code != 200 || imported["crew_id"] == crewID || imported["role"] != "Escalation lead" {
		t.Fatalf("import_crew = %d %v", code, imported)
	}
	if access := imported["access"].(map[string]any); access["can_edit"] != true {
		t.Fatalf("the importer owns the imported Crew: %v", access)
	}
	for _, raw := range imported["schedules"].([]any) {
		if raw.(map[string]any)["enabled"] != false {
			t.Fatalf("imported schedules must arrive disabled: %v", raw)
		}
	}
	if fns := imported["functions"].([]any); len(fns) != 3 {
		t.Fatalf("imported functions = %v", fns)
	}
}

func TestExternalCrewAuthoringAccess(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","can_create":true},{"id":"viewer","username":"viewer","role":"viewer","products":["work"]},{"id":"editor","username":"editor","can_create":false,"can_edit":true,"products":["work"]}]}`)
	spec := map[string]any{"name": "Probe", "role": "Probe", "purpose": "Probe access."}

	if code, out := externalCrewRequest(t, env, crewWriter("viewer"), "create_crew", spec); code != 403 {
		t.Fatalf("read-only account create = %d %v", code, out)
	}
	// "can create" is the one switch for new workflows, Relays and Crews: an editor without it makes no Crew (server A).
	if code, out := externalCrewRequest(t, env, crewWriter("editor"), "create_crew", spec); code != 403 {
		t.Fatalf("an account without the create permission created a Crew = %d %v", code, out)
	}
	if code, _ := externalCrewRequest(t, env, crewWriter("editor"), "import_crew", map[string]any{"spec": spec}); code != 403 {
		t.Fatalf("an account without the create permission imported a Crew, got %d", code)
	}
	bounded := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:write"}, CrewIDs: []string{"beta"}}}
	if code, _ := externalCrewRequest(t, env, bounded, "create_crew", spec); code != 403 {
		t.Fatalf("a token bounded to some Crews must not create, got %d", code)
	}
	// alpha belongs to owner: a bounded token may still edit Crews it names.
	boundedAlpha := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:write"}, CrewIDs: []string{"alpha"}}}
	if code, out := externalCrewRequest(t, env, boundedAlpha, "update_crew", map[string]any{"crew_id": "alpha", "role": "Reviewer", "purpose": "Review changes."}); code != 200 || out["role"] != "Reviewer" {
		t.Fatalf("owner update within the bound = %d %v", code, out)
	}
	if code, _ := externalCrewRequest(t, env, boundedAlpha, "update_crew", map[string]any{"crew_id": "beta", "role": "x"}); code != 404 {
		t.Fatalf("Crew outside the token bound must be not-found, got %d", code)
	}
	// gamma belongs to "other": the owner of the token is a reader there.
	if code, _ := externalCrewRequest(t, env, crewWriter("owner"), "update_crew", map[string]any{"crew_id": "gamma", "role": "x"}); code != 403 {
		t.Fatalf("another owner's Crew must be refused, got %d", code)
	}
	if code, _ := externalCrewRequest(t, env, crewWriter("owner"), "create_crew", map[string]any{"name": "Bad", "role": "x", "purpose": "y", "selected_skills": []any{"no-such-skill"}}); code != 400 {
		t.Fatalf("unknown skill must refuse creation, got %d", code)
	}

	readOnly := &UserClaims{AccessToken: &accesstokens.Token{Scopes: []string{"crews:read", "crews:run"}, AllCrews: true}}
	for _, name := range []string{"create_crew", "update_crew", "import_crew"} {
		if externalTokenAllows(readOnly, externalTool{Name: name}) {
			t.Fatalf("%s must need crews:write", name)
		}
		if !externalTokenAllows(crewWriter("owner"), externalTool{Name: name}) {
			t.Fatalf("crews:write must allow %s", name)
		}
	}
	if !externalTokenAllows(readOnly, externalTool{Name: "export_crew"}) {
		t.Fatal("export_crew is a read")
	}
	if grant := mcpOAuthTokenForGrant(mcpOAuthGrant{UserID: "u", Scopes: []string{"crews:write"}}); !grant.AllCrews {
		t.Fatal("an OAuth grant with crews:write must reach the user's Crews")
	}
}
