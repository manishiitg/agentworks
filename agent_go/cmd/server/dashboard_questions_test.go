package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestDashboardQuestionCapabilityMatrix(t *testing.T) {
	allowed := dashboardQuestionPolicy{Authenticated: true, Active: true, Product: true, View: true, Published: true, AskEnabled: true, AskScope: true}
	for _, tc := range []struct {
		name   string
		change func(*dashboardQuestionPolicy)
		view   bool
		ask    bool
		reason string
		mode   string
	}{
		{"reader", func(*dashboardQuestionPolicy) {}, true, true, "", "question_only"},
		{"editor", func(*dashboardQuestionPolicy) {}, true, true, "", "question_only"},
		{"owner", func(p *dashboardQuestionPolicy) { p.Owner = true }, true, true, "", "owner"},
		{"signed_out", func(p *dashboardQuestionPolicy) { p.Authenticated = false }, false, false, "authentication_required", ""},
		{"disabled", func(p *dashboardQuestionPolicy) { p.Active = false }, false, false, "account_disabled", ""},
		{"product", func(p *dashboardQuestionPolicy) { p.Product = false }, false, false, "product_unavailable", ""},
		{"private", func(p *dashboardQuestionPolicy) { p.View = false }, false, false, "dashboard_unavailable", ""},
		{"draft", func(p *dashboardQuestionPolicy) { p.Published = false }, false, false, "not_published", ""},
		{"programs_only", func(p *dashboardQuestionPolicy) { p.AskEnabled = false }, true, false, "ask_disabled", ""},
		{"owner_ask_off", func(p *dashboardQuestionPolicy) { p.Owner, p.AskEnabled = true, false }, true, false, "ask_disabled", ""},
		{"read_consent", func(p *dashboardQuestionPolicy) { p.AskScope = false }, true, false, "ask_consent_required", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := allowed
			tc.change(&policy)
			got := dashboardQuestionCapabilities(policy)
			if got.CanView != tc.view || got.CanAsk != tc.ask || got.Reason != tc.reason || got.Mode != tc.mode {
				t.Fatalf("capability: %+v", got)
			}
		})
	}
}

// Real JWT-authenticated HTTP routes, the production workspace document and
// dashboard handlers, and disk persistence. There is no model or storage mock.
func TestDashboardQuestionHTTPContract(t *testing.T) {
	f := newExternalToolsFixture(t)
	withProjectOwnerRegistry(t)
	t.Setenv("AUTH_SECRET", "dashboard-question-test-secret")
	t.Setenv("PUBLIC_URL", "https://example.com")
	api := f.api
	start := func() *httptest.Server {
		router := mux.NewRouter()
		routes := router.PathPrefix("/api").Subrouter()
		routes.HandleFunc("/dashboards", api.handleDashboards).Methods(http.MethodPost)
		api.registerDashboardQuestionRoutes(routes)
		server := httptest.NewServer(AuthMiddleware(router))
		t.Cleanup(server.Close)
		return server
	}
	server := start()
	tokens := map[string]string{}
	for _, user := range []string{"owner", "reader", "readonly-owner", "outsider"} {
		token, err := GenerateJWT(user, user, "")
		if err != nil {
			t.Fatal(err)
		}
		tokens[user] = token
	}
	type result struct {
		status int
		body   map[string]any
		err    error
	}
	request := func(user, method, p string, body any) result {
		raw, err := json.Marshal(body)
		if err != nil {
			return result{err: err}
		}
		req, err := http.NewRequest(method, server.URL+p, bytes.NewReader(raw))
		if err != nil {
			return result{err: err}
		}
		req.Header.Set("Content-Type", "application/json")
		if user != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[user])
		}
		response, err := server.Client().Do(req)
		if err != nil {
			return result{err: err}
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		out := result{status: response.StatusCode, err: err}
		if err == nil {
			out.err = json.Unmarshal(data, &out.body)
		}
		if response.Header.Get("Cache-Control") != "no-store" && user != "" {
			out.err = fmt.Errorf("private response without no-store: %s", data)
		}
		return out
	}
	call := func(user, method, p string, body any, status int) map[string]any {
		t.Helper()
		out := request(user, method, p, body)
		if out.err != nil || out.status != status {
			t.Fatalf("%s %s as %s: %d %+v (%v), want %d", method, p, user, out.status, out.body, out.err, status)
		}
		return out.body
	}
	op := func(action, root, slug, revision, text string) map[string]any {
		t.Helper()
		return call("owner", http.MethodPost, "/api/dashboards", map[string]any{"action": action, "workspace": root, "dashboard_id": slug, "expected_revision": revision, "title": "Overview", "files": map[string]string{"index.html": "<!doctype html><html><head><title>Overview</title></head><body>" + text + "</body></html>"}}, 200)
	}
	root, slug := "Workflow/invoices", "overview"
	created := op("create", root, slug, "", "Total: 42")
	revision := created["dashboard"].(map[string]any)["revision"].(string)
	op("publish", root, slug, revision, "")
	get := op("get", root, slug, "", "")
	resource := get["resource_id"].(string)
	if _, err := uuid.Parse(resource); err != nil || strings.Contains(resource, "owner") {
		t.Fatalf("not an opaque resource: %s", resource)
	}
	base := "/api/dashboards/" + resource
	ownerView := call("owner", http.MethodGet, base, nil, 200)
	readerView := call("reader", http.MethodGet, base, nil, 200)
	if ownerView["capability"].(map[string]any)["mode"] != "owner" || readerView["capability"].(map[string]any)["mode"] != "question_only" {
		t.Fatalf("owner/reader modes: %v %v", ownerView, readerView)
	}
	readonlyView := call("readonly-owner", http.MethodGet, base, nil, 200)
	if readonlyView["capability"].(map[string]any)["mode"] != "question_only" {
		t.Fatal("read-only account gained full owner mode")
	}
	call("", http.MethodGet, base, nil, 401)
	call("outsider", http.MethodGet, base, nil, 404)
	listed := call("reader", http.MethodPost, "/api/dashboards", map[string]any{"action": "list", "workspace": root}, 200)
	if listed["dashboards"].([]any)[0].(map[string]any)["resource_id"] != resource {
		t.Fatal("discovery did not reuse the resource id")
	}
	// A draft can be edited, but cannot become question evidence even for its owner.
	updated := op("update", root, slug, revision, "Private draft: 99")
	draft := updated["dashboard"].(map[string]any)["revision"].(string)
	args := dashboardQuestionSubmission{Question: "Where did 42 come from?", Revision: revision, SubmissionID: "first", Context: dashboardQuestionContext{SelectedText: "42", NearbyLabel: "Total", Filters: map[string]string{"period": "today"}}}
	bad := args
	bad.Revision, bad.SubmissionID = draft, "private-draft"
	call("owner", http.MethodPost, base+"/questions", bad, 403)
	call("reader", http.MethodPost, base+"/questions", bad, 403)
	call("reader", http.MethodPost, base+"/questions", map[string]any{"question": "inject", "captured_revision": revision, "submission_id": "inject", "workspace": "Workflow/secret", "owner": "owner"}, 400)
	// Concurrent uncertain retries must create exactly one turn and conversation.
	results := make(chan result, 4)
	for range 4 {
		go func() { results <- request("owner", http.MethodPost, base+"/questions", args) }()
	}
	var first map[string]any
	for range 4 {
		out := <-results
		if out.err != nil || out.status != 200 {
			t.Fatalf("concurrent submit: %+v", out)
		}
		if first == nil {
			first = out.body
		} else if first["question_id"] != out.body["question_id"] || first["conversation_id"] != out.body["conversation_id"] {
			t.Fatalf("duplicate turn: %v %v", first, out.body)
		}
	}
	question := first["question_id"].(string)
	conversation := first["conversation_id"].(string)
	questionPath, conversationPath := base+"/questions/"+question, base+"/conversations/"+conversation
	bad = args
	bad.Question = "Different payload"
	call("owner", http.MethodPost, base+"/questions", bad, 409)
	for _, p := range []string{questionPath, conversationPath} {
		call("reader", http.MethodGet, p, nil, 404)
	}
	call("reader", http.MethodPost, questionPath+"/cancel", nil, 404)
	bad = args
	bad.ConversationID, bad.SubmissionID = conversation, "foreign-conversation"
	call("reader", http.MethodPost, base+"/questions", bad, 404)
	// The same submission id belongs to a different namespace for another viewer.
	readerTurn := call("reader", http.MethodPost, base+"/questions", args, 200)
	if readerTurn["conversation_id"] == conversation || readerTurn["question_id"] == question {
		t.Fatal("viewers shared a conversation or turn")
	}
	waitFailed := func(user, p string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			turn := call(user, http.MethodGet, p, nil, 200)
			if turn["state"] == "failed" {
				if turn["error"] != "adapter_not_implemented" || turn["answer"] != "" || turn["runtime"] != nil || turn["request_hash"] != nil {
					t.Fatalf("stub manufactured an answer or leaked internal fields: %v", turn)
				}
				return turn
			}
			if time.Now().After(deadline) {
				t.Fatalf("stub did not settle: %v", turn)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitFailed("owner", questionPath)
	waitFailed("reader", base+"/questions/"+readerTurn["question_id"].(string))
	// Publishing later does not overwrite the original observed value/revision.
	op("publish", root, slug, draft, "")
	history := call("owner", http.MethodGet, questionPath, nil, 200)
	if history["captured_revision"] != revision || history["current_revision"] != draft || history["captured_is_current"] != false || history["context"].(map[string]any)["selected_text"] != "42" {
		t.Fatalf("lost captured evidence: %v", history)
	}
	followup := args
	followup.SubmissionID, followup.ConversationID, followup.Question = "followup", conversation, "Why did it change?"
	followupTurn := call("owner", http.MethodPost, base+"/questions", followup, 200)
	waitFailed("owner", base+"/questions/"+followupTurn["question_id"].(string))
	if len(call("owner", http.MethodGet, conversationPath, nil, 200)["questions"].([]any)) != 2 {
		t.Fatal("followup lost its conversation")
	}
	// Access revocation applies to existing ids, reads, retries, and cancellation.
	f.write(t, root+"/workflow.json", `{"id":"invoices","created_by":"owner","access":{"owners":["owner"]}}`)
	call("reader", http.MethodGet, base, nil, 404)
	call("reader", http.MethodGet, base+"/questions/"+readerTurn["question_id"].(string), nil, 404)
	call("reader", http.MethodPost, base+"/questions", args, 404)
	call("reader", http.MethodPost, base+"/questions/"+readerTurn["question_id"].(string)+"/cancel", nil, 404)
	// Seed an in-flight turn at the durable boundary to exercise cancellation
	// and process recovery deterministically, without a delayed fake adapter.
	ctx := context.Background()
	dashboardQuestionMu.Lock()
	store, err := api.readDashboardQuestions(ctx, "owner")
	if err == nil {
		queued := store.Turns[0]
		queued.ID, queued.SubmissionID, queued.State, queued.Error = uuid.NewString(), "cancel-pending", "queued", ""
		store.Turns = append(store.Turns, queued)
		err = saveDashboardQuestionJSON(ctx, dashboardQuestionStorePath("owner"), store)
	}
	dashboardQuestionMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	queuedID := store.Turns[len(store.Turns)-1].ID
	cancelled := call("owner", http.MethodPost, base+"/questions/"+queuedID+"/cancel", nil, 200)
	if cancelled["state"] != "cancelled" || api.transitionDashboardQuestion(ctx, "owner", queuedID, "queued", "running", "") {
		t.Fatal("cancelled turn was dispatched")
	}
	dashboardQuestionMu.Lock()
	store, err = api.readDashboardQuestions(ctx, "owner")
	if err == nil {
		interrupted := store.Turns[0]
		interrupted.ID, interrupted.SubmissionID, interrupted.State, interrupted.Error = uuid.NewString(), "interrupted", "running", ""
		store.Turns = append(store.Turns, interrupted)
		err = saveDashboardQuestionJSON(ctx, dashboardQuestionStorePath("owner"), store)
	}
	dashboardQuestionMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	interruptedID := store.Turns[len(store.Turns)-1].ID
	server.Close()
	api = &StreamingAPI{}
	server = start()
	recovered := call("owner", http.MethodGet, base+"/questions/"+interruptedID, nil, 200)
	if recovered["state"] != "failed" || recovered["error"] != "interrupted" {
		t.Fatalf("restart replayed or lost running turn: %v", recovered)
	}
	retried := call("owner", http.MethodPost, base+"/questions", args, 200)
	if retried["question_id"] != question || retried["state"] != "failed" {
		t.Fatalf("restart lost duplicate receipt: %v", retried)
	}
	// Narrowed connections never obtain the full owner conversation mode.
	claims := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"dashboards:read", "runs:execute"}, WorkflowIDs: []string{"invoices"}}}
	resolved, err := api.resolveDashboardQuestion(ctx, claims, resource, revision)
	if err != nil || !resolved.Capability.CanAsk || resolved.Capability.Mode != "question_only" {
		t.Fatalf("run-only owner authority: %+v %v", resolved.Capability, err)
	}
	claims.AccessToken.Scopes = []string{"dashboards:read"}
	resolved, err = api.resolveDashboardQuestion(ctx, claims, resource, revision)
	if err != nil || !resolved.Capability.CanView || resolved.Capability.CanAsk {
		t.Fatalf("read-only connection gained asks: %+v %v", resolved.Capability, err)
	}
	// The Crew's current runtime switch overrides older product settings.
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["agentworks","work"]},{"id":"reader","username":"reader","products":["agentworks","work"]}]}`)
	stubCrewLookups(t, map[string]string{"Crew/dashboard-team": "owner"}, map[string]string{})
	f.write(t, "Crew/dashboard-team/product.json", `{"id":"dashboard-team","product":"work","title":"Team","capabilities":{"free_text_ask":true}}`)
	f.write(t, "Crew/dashboard-team/workflow.json", `{"schema_version":1,"capabilities":{"free_text_ask":false}}`)
	crewCreated := op("create", "Crew/dashboard-team", "team", "", "Crew total")
	crewRevision := crewCreated["dashboard"].(map[string]any)["revision"].(string)
	op("publish", "Crew/dashboard-team", "team", crewRevision, "")
	crewID := op("get", "Crew/dashboard-team", "team", "", "")["resource_id"].(string)
	crewBase := "/api/dashboards/" + crewID
	for _, user := range []string{"owner", "reader"} {
		capability := call(user, http.MethodGet, crewBase, nil, 200)["capability"].(map[string]any)
		if capability["can_view"] != true || capability["can_ask"] != false || capability["reason"] != "ask_disabled" {
			t.Fatalf("Crew ask switch: %v", capability)
		}
		bad = args
		bad.Revision = crewRevision
		call(user, http.MethodPost, crewBase+"/questions", bad, 403)
	}
	t.Run("registry_failure_preserves_dashboard_reads", func(t *testing.T) {
		// A real unreadable registry must disable Ask without taking down the
		// existing get, project listing, or cross-project discovery APIs.
		if err := writeFileToWorkspace(context.Background(), dashboardQuestionRegistryPath, "{invalid"); err != nil {
			t.Fatal(err)
		}
		body := call("owner", http.MethodPost, "/api/dashboards", map[string]any{"action": "get", "workspace": root, "dashboard_id": slug}, 200)
		if body["resource_id"] != nil || body["dashboard"].(map[string]any)["id"] != slug {
			t.Fatalf("registry failure broke dashboard get: %v", body)
		}
		for _, args := range []map[string]any{{"action": "list", "workspace": root}, {"action": "list"}} {
			body = call("owner", http.MethodPost, "/api/dashboards", args, 200)
			rows := body["dashboards"].([]any)
			if len(rows) == 0 {
				t.Fatalf("registry failure hid ordinary dashboards: %v", body)
			}
			for _, item := range rows {
				if item.(map[string]any)["resource_id"] != nil {
					t.Fatal("unavailable registry produced a question resource id")
				}
			}
		}
	})
}
