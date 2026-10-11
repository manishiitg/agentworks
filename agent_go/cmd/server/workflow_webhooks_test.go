package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

func webhookTestSchedule(t *testing.T, mode string) WorkflowSchedule {
	t.Helper()
	id := uuid.NewString()
	encrypted, err := encryptSecretValueWithAAD("test-trigger-secret", webhookAAD("wf_test", id))
	if err != nil {
		t.Fatal(err)
	}
	return WorkflowSchedule{ID: id, Name: "Issue", ScheduleType: "webhook", Enabled: true, Timezone: "UTC", Webhook: &WorkflowWebhookConfig{AuthMode: mode, EncryptedSecret: encrypted}, GroupNames: []string{"default"}, RouteSelections: map[string]string{"route": "issue"}, Mode: "workshop", WorkshopMode: "run", PulseMode: "basic", PulseModeReason: "Run finalization"}
}

func webhookTestRequest(s WorkflowSchedule, body string) *http.Request {
	r := httptest.NewRequest("POST", "/api/hooks/workflow/"+s.ID, strings.NewReader(body))
	r = mux.SetURLVars(r, map[string]string{"id": s.ID})
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "delivery-123")
	if s.Webhook.AuthMode == "github" {
		mac := hmac.New(sha256.New, []byte("test-trigger-secret"))
		_, _ = mac.Write([]byte(body))
		r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		r.Header.Set("X-GitHub-Delivery", "github-delivery-123")
		r.Header.Set("X-GitHub-Event", "issues")
	} else {
		r.Header.Set("Authorization", "Bearer test-trigger-secret")
	}
	return r
}

func TestWebhookReceiverAuthenticationInputAndErrors(t *testing.T) {
	tests := []struct {
		name, mode, body string
		mutate           func(*http.Request, *WorkflowSchedule)
		existingErr      error
		startErr         error
		want             int
		starts           int
	}{
		{name: "generic arbitrary payload", mode: "bearer", body: `{"route_selections":{"route":"evil"},"selected_folder":"other","model":"evil","issue":{"number":17}}`, want: 202, starts: 1},
		{name: "github signed unicode JSON", mode: "github", body: `{ "title": "café", "number": 17 }`, want: 202, starts: 1},
		{name: "missing secret", mode: "bearer", body: `{}`, mutate: func(r *http.Request, _ *WorkflowSchedule) { r.Header.Del("Authorization") }, want: 401},
		{name: "wrong secret", mode: "bearer", body: `{}`, mutate: func(r *http.Request, _ *WorkflowSchedule) { r.Header.Set("Authorization", "Bearer wrong") }, want: 401},
		{name: "query token rejected", mode: "bearer", body: `{}`, mutate: func(r *http.Request, _ *WorkflowSchedule) {
			r.Header.Del("Authorization")
			r.URL.RawQuery = "token=test-trigger-secret"
		}, want: 401},
		{name: "github does not accept bearer", mode: "github", body: `{}`, mutate: func(r *http.Request, _ *WorkflowSchedule) {
			r.Header.Del("X-Hub-Signature-256")
			r.Header.Set("Authorization", "Bearer test-trigger-secret")
		}, want: 401},
		{name: "github tampered", mode: "github", body: `{}`, mutate: func(r *http.Request, _ *WorkflowSchedule) {
			r.Header.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64))
		}, want: 401},
		{name: "github ping", mode: "github", body: `{}`, mutate: func(r *http.Request, _ *WorkflowSchedule) { r.Header.Set("X-GitHub-Event", "ping") }, want: 200},
		{name: "disabled", mode: "bearer", body: `{}`, mutate: func(_ *http.Request, s *WorkflowSchedule) { s.Enabled = false }, want: 410},
		{name: "disabled still authenticates", mode: "bearer", body: `{}`, mutate: func(r *http.Request, s *WorkflowSchedule) { s.Enabled = false; r.Header.Del("Authorization") }, want: 401},
		{name: "malformed JSON", mode: "bearer", body: `{`, want: 400},
		{name: "trailing JSON", mode: "bearer", body: `{} {}`, want: 400},
		{name: "wrong content type", mode: "bearer", body: `{}`, mutate: func(r *http.Request, _ *WorkflowSchedule) { r.Header.Set("Content-Type", "text/plain") }, want: 415},
		{name: "oversized", mode: "bearer", body: `"` + strings.Repeat("x", maxWebhookBodyBytes) + `"`, want: 413},
		{name: "delivery ID too long", mode: "bearer", body: `{}`, mutate: func(r *http.Request, _ *WorkflowSchedule) { r.Header.Set("Idempotency-Key", strings.Repeat("x", 257)) }, want: 400},
		{name: "storage unavailable", mode: "bearer", body: `{}`, existingErr: errors.New("offline"), want: 503},
		{name: "busy", mode: "bearer", body: `{}`, startErr: errors.New("busy"), want: 503, starts: 1},
		{name: "clock trigger cannot be called", mode: "bearer", body: `{}`, mutate: func(_ *http.Request, s *WorkflowSchedule) { s.ScheduleType = "cron" }, want: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched := webhookTestSchedule(t, tt.mode)
			req := webhookTestRequest(sched, tt.body)
			if tt.mutate != nil {
				tt.mutate(req, &sched)
			}
			started := 0
			receiver := webhookReceiver{
				find: func(context.Context, string) (*ScheduleSearchResult, error) {
					return &ScheduleSearchResult{WorkspacePath: "Workflow/test", Manifest: &WorkflowManifest{ID: "wf_test", Schedules: []WorkflowSchedule{sched}}}, nil
				},
				existing: func(context.Context, string) (schedulerstate.Run, error) {
					if tt.existingErr != nil {
						return schedulerstate.Run{}, tt.existingErr
					}
					return schedulerstate.Run{}, schedulerstate.ErrRunNotFound
				},
				start: func(path, id, origin string, input *WorkflowWebhookDelivery) (string, error) {
					started++
					if path != "Workflow/test" || id != sched.ID || origin != "" || string(input.Payload) != tt.body {
						t.Fatalf("delivery changed target or payload: %+v", input)
					}
					if input.RunID == "" || input.ReceivedAt.IsZero() {
						t.Fatal("missing delivery metadata")
					}
					if tt.mode == "github" && (input.Event != "issues" || input.DeliveryID != "github-delivery-123") {
						t.Fatalf("wrong GitHub metadata: %+v", input)
					}
					return input.RunID, tt.startErr
				},
			}
			w := httptest.NewRecorder()
			receiver.receive(w, req)
			if w.Code != tt.want || started != tt.starts {
				t.Fatalf("status=%d starts=%d, want %d/%d: %s", w.Code, started, tt.want, tt.starts, w.Body.String())
			}
			if tt.name == "busy" && w.Header().Get("Retry-After") == "" {
				t.Fatal("missing retry guidance")
			}
		})
	}
}

func TestWebhookWithoutIdempotencyKeyStartsDistinctRuns(t *testing.T) {
	sched := webhookTestSchedule(t, "bearer")
	runs := make(map[string]schedulerstate.Run)
	receiver := webhookReceiver{
		find: func(context.Context, string) (*ScheduleSearchResult, error) {
			return &ScheduleSearchResult{WorkspacePath: "Workflow/test", Manifest: &WorkflowManifest{ID: "wf_test", Schedules: []WorkflowSchedule{sched}}}, nil
		},
		existing: func(_ context.Context, id string) (schedulerstate.Run, error) {
			if run, ok := runs[id]; ok {
				return run, nil
			}
			return schedulerstate.Run{}, schedulerstate.ErrRunNotFound
		},
		start: func(_ string, _ string, _ string, input *WorkflowWebhookDelivery) (string, error) {
			runs[input.RunID] = schedulerstate.Run{RunID: input.RunID}
			return input.RunID, nil
		},
	}
	var firstID string
	for i := 0; i < 2; i++ {
		req := webhookTestRequest(sched, `{}`)
		req.Header.Del("Idempotency-Key")
		w := httptest.NewRecorder()
		receiver.receive(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("keyless delivery %d: status=%d body=%s", i, w.Code, w.Body.String())
		}
		var response struct {
			RunID      string `json:"run_id"`
			DeliveryID string `json:"delivery_id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.DeliveryID == "" || response.RunID != webhookDeliveryRunID("wf_test", sched.ID, response.DeliveryID) {
			t.Fatalf("keyless delivery %d has inconsistent identity: %+v", i, response)
		}
		if i == 0 {
			firstID = response.RunID
		} else if response.RunID == firstID {
			t.Fatalf("distinct keyless deliveries reused run ID %s", firstID)
		}
	}
}

func TestWebhookDuplicateDeliverySurvivesRestartAndConcurrentRetries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := schedulerstate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	sched := webhookTestSchedule(t, "github")
	var starts atomic.Int32
	workspacePath := "Workflow/test"
	receiver := webhookReceiver{
		find: func(context.Context, string) (*ScheduleSearchResult, error) {
			return &ScheduleSearchResult{WorkspacePath: workspacePath, Manifest: &WorkflowManifest{ID: "wf_test", Schedules: []WorkflowSchedule{sched}}}, nil
		},
		existing: func(ctx context.Context, id string) (schedulerstate.Run, error) { return store.GetRun(ctx, id) },
		start: func(_ string, id, _ string, input *WorkflowWebhookDelivery) (string, error) {
			err := store.BeginRun(context.Background(), schedulerstate.Run{RunID: input.RunID, ScheduleID: id, ScopeType: "workflow", ScopeID: "test", LockKey: "test", TriggerSource: "webhook"})
			if err != nil {
				return "", err
			}
			starts.Add(1)
			return input.RunID, nil
		},
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			receiver.receive(w, webhookTestRequest(sched, `{}`))
			if w.Code != 200 && w.Code != 202 {
				t.Errorf("concurrent retry: %d %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	if starts.Load() != 1 {
		t.Fatalf("started %d runs", starts.Load())
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = schedulerstate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath = "Workflow/renamed" // Stable workflow/trigger IDs preserve authentication and deduplication.
	w := httptest.NewRecorder()
	receiver.receive(w, webhookTestRequest(sched, `{}`))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"duplicate":true`) || starts.Load() != 1 {
		t.Fatalf("restart replay: %d %s", w.Code, w.Body.String())
	}
}

func TestWebhookManagementPermissionsRotationAndClockIsolation(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"reader","username":"reader","can_create":false}]}`)
	manifest := NewWorkflowManifest("API test")
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}, Readers: []string{"reader"}}
	raw, _ := json.Marshal(manifest)
	mock := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/test"):            string(raw),
		"Workflow/test/planning/plan.json":       `{"steps":[{"type":"routing","id":"route","title":"Choose","routing_question":"Which?","routes":[{"route_id":"issue","route_name":"Issue","next_step_id":"work"},{"route_id":"other","route_name":"Other","next_step_id":"work"}]},{"type":"scripted","id":"work","title":"Work","description":"Work"}]}`,
		"Workflow/test/variables/variables.json": `{"groups":[{"name":"default"}]}`,
	}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	svc := NewSchedulerService(&StreamingAPI{})
	req := workflowWebhookRequest{WorkspacePath: "Workflow/test", Name: "Issue created", Enabled: true, AuthMode: "bearer", RouteSelections: map[string]string{"route": "issue"}, GroupNames: []string{"default"}}
	w := httptest.NewRecorder()
	svc.saveWorkflowWebhook(w, sharedSecretsRequest("POST", "/api/workflow-webhooks", "reader", req))
	if w.Code != 403 {
		t.Fatalf("reader mutation: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	svc.saveWorkflowWebhook(w, sharedSecretsRequest("POST", "/api/workflow-webhooks", "owner", req))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created workflowWebhookResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Secret) != 64 || len(svc.jobs) != 0 {
		t.Fatalf("secret missing or clock job registered: %+v", created)
	}
	if _, err := svc.TriggerNow("Workflow/test", created.ID); err == nil || !strings.Contains(err.Error(), "authenticated delivery") {
		t.Fatalf("API trigger accepted a payload-free manual invocation: %v", err)
	}
	stored, found, err := ReadWorkflowManifest(context.Background(), "Workflow/test")
	if err != nil || !found {
		t.Fatal(err)
	}
	if strings.Contains(stored.Schedules[0].Webhook.EncryptedSecret, created.Secret) {
		t.Fatal("plaintext persisted")
	}
	if missed := ComputeWorkflowScheduleMissedStatus(stored.Schedules[0], nil, time.Now().UTC()); missed.MissedRunCount != 0 {
		t.Fatal("API trigger counted as missed clock run")
	}
	w = httptest.NewRecorder()
	svc.listWorkflowWebhooks(w, sharedSecretsRequest("GET", "/api/workflow-webhooks?workspace_path=Workflow/test", "reader", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), created.Secret) || strings.Contains(w.Body.String(), "encrypted_secret") {
		t.Fatalf("list leaked secret or inaccessible: %s", w.Body.String())
	}
	req.RotateSecret = true
	update := mux.SetURLVars(sharedSecretsRequest("PUT", "/api/workflow-webhooks/"+created.ID, "owner", req), map[string]string{"id": created.ID})
	w = httptest.NewRecorder()
	svc.saveWorkflowWebhook(w, update)
	if w.Code != 200 {
		t.Fatalf("rotate: %d %s", w.Code, w.Body.String())
	}
	var rotated workflowWebhookResponse
	_ = json.Unmarshal(w.Body.Bytes(), &rotated)
	if rotated.Secret == created.Secret || rotated.Secret == "" {
		t.Fatal("secret did not rotate")
	}
	stored, _, _ = ReadWorkflowManifest(context.Background(), "Workflow/test")
	cfg := stored.Schedules[0].Webhook
	secret, err := decryptSecretValueWithAAD(cfg.EncryptedSecret, webhookAAD(stored.ID, created.ID))
	if err != nil {
		t.Fatal(err)
	}
	authReq := httptest.NewRequest("POST", "/", nil)
	authReq.Header.Set("Authorization", "Bearer "+created.Secret)
	if verifyWebhookRequest(cfg, secret, authReq, nil) {
		t.Fatal("old secret accepted")
	}
	if _, err := decryptSecretValueWithAAD(cfg.EncryptedSecret, webhookAAD("Workflow/other", created.ID)); err == nil {
		t.Fatal("ciphertext portable across workflows")
	}
	req.RouteSelections = map[string]string{"route": "deleted"}
	w = httptest.NewRecorder()
	svc.saveWorkflowWebhook(w, mux.SetURLVars(sharedSecretsRequest("PUT", "/api/workflow-webhooks/"+created.ID, "owner", req), map[string]string{"id": created.ID}))
	if w.Code != 400 {
		t.Fatalf("unknown route accepted: %d", w.Code)
	}
	req.Enabled = false
	req.RotateSecret = false
	w = httptest.NewRecorder()
	svc.saveWorkflowWebhook(w, mux.SetURLVars(sharedSecretsRequest("PUT", "/api/workflow-webhooks/"+created.ID, "owner", req), map[string]string{"id": created.ID}))
	if w.Code != 200 {
		t.Fatalf("cannot disable broken route binding: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	svc.deleteWorkflowWebhook(w, mux.SetURLVars(sharedSecretsRequest("DELETE", "/api/workflow-webhooks/"+created.ID+"?workspace_path=Workflow/test", "owner", nil), map[string]string{"id": created.ID}))
	if w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	stored, _, _ = ReadWorkflowManifest(context.Background(), "Workflow/test")
	if len(stored.Schedules) != 0 {
		t.Fatal("trigger survived deletion")
	}
}

func TestWebhookPolicyRejectsOverridesAndManualInvocation(t *testing.T) {
	sched := webhookTestSchedule(t, "bearer")
	for _, mutate := range []func(*WorkflowSchedule){
		func(s *WorkflowSchedule) { s.TriggerPayload = json.RawMessage(`{"model":"bad"}`) },
		func(s *WorkflowSchedule) { s.Messages = []string{"run something else"} },
		func(s *WorkflowSchedule) { s.CollisionPolicy = "queue_latest" },
		func(s *WorkflowSchedule) { s.AfterScheduleID = "other" },
		func(s *WorkflowSchedule) { s.AfterScheduleIDs = []string{"other"} },
		func(s *WorkflowSchedule) { s.WorkshopMode = "workshop" },
	} {
		copy := sched
		mutate(&copy)
		if validateWebhookSchedule(copy) == nil {
			t.Fatalf("unsafe trigger allowed: %+v", copy)
		}
	}
	if !shouldSkipAuth("/api/hooks/workflow/"+sched.ID) || shouldSkipAuth("/api/workflow-webhooks") {
		t.Fatal("wrong authentication boundary")
	}
}

func TestWebhookSessionOriginAndHistoryMetadata(t *testing.T) {
	sctx := &ScheduleContext{WorkflowID: "wf_test", WorkspacePath: "Workflow/test", Schedule: WorkflowSchedule{ID: "hook", Name: "PR reviews", ScheduleType: "webhook"}, WebhookInput: &WorkflowWebhookDelivery{DeliveryID: "delivery-1", Event: "pull_request", ReceivedAt: time.Now().UTC(), Payload: json.RawMessage(`{"commit_sha":"0b8b40e69a1234567890abcdef1234567890abcd","component":"chat","env":"staging","deployed_at":"2026-09-23T10:32:19Z","private":"not-for-history"}`)}}
	svc := &SchedulerService{}
	req := svc.buildWorkshopRequest(context.Background(), sctx)
	if req["triggered_by"] != "webhook" {
		t.Fatalf("incorrect origin: %v", req["triggered_by"])
	}
	if !isScheduledSessionIdentity("opaque-session", "webhook") {
		t.Fatal("webhooks must retain the external execution lane")
	}
	data, err := json.Marshal(ScheduleRunEntry{ID: "run-1", TriggerSource: "webhook", Webhook: webhookRunMetadata(sctx)})
	if err != nil {
		t.Fatal(err)
	}
	var restored ScheduleRunEntry
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Webhook == nil || restored.Webhook.DeliveryID != "delivery-1" || restored.Webhook.Event != "pull_request" || restored.Webhook.TriggerName != "PR reviews" {
		t.Fatalf("missing delivery metadata: %s", data)
	}
	if restored.Webhook.CommitSHA != "0b8b40e69a1234567890abcdef1234567890abcd" || restored.Webhook.Component != "chat" || restored.Webhook.Env != "staging" || restored.Webhook.DeployedAt != "2026-09-23T10:32:19Z" {
		t.Fatalf("missing deploy metadata: %s", data)
	}
	if strings.Contains(string(data), "not-for-history") || strings.Contains(string(data), "payload") {
		t.Fatalf("history leaks payload: %s", data)
	}
}

func TestWorkflowWebhookPayloadIsLoadedOnDemand(t *testing.T) {
	workspace := "Workflow/test"
	sched := webhookTestSchedule(t, "bearer")
	manifest := NewWorkflowManifest("API test")
	manifest.ID = "wf_test"
	manifest.Schedules = []WorkflowSchedule{sched}
	manifestRaw, _ := json.Marshal(manifest)
	runs := []ScheduleRunEntry{{
		ID:         "run-1",
		ScheduleID: sched.ID,
		Status:     "success",
		StartedAt:  time.Now().UTC(),
		Webhook:    &WebhookRunMetadata{TriggerName: sched.Name, DeliveryID: "delivery-1", ReceivedAt: time.Now().UTC()},
	}}
	// The payload remains addressable by run ID after newer history pages
	// push this delivery beyond the old 200-run window.
	for i := 0; i < maxScheduleRuns; i++ {
		runs = append(runs, ScheduleRunEntry{ID: uuid.NewString(), ScheduleID: sched.ID, Status: "success", StartedAt: time.Now().UTC().Add(time.Duration(i+1) * time.Second)})
	}
	runsRaw, _ := json.Marshal(runs)
	deliveryRaw, _ := json.Marshal(WorkflowWebhookDelivery{
		RunID:      "run-1",
		DeliveryID: "delivery-1",
		ReceivedAt: time.Now().UTC(),
		Payload:    json.RawMessage(`{"action":"opened","nested":{"value":42}}`),
	})
	mock := &mockWorkspaceAPI{files: map[string]string{
		manifestPath(workspace):              string(manifestRaw),
		scheduleRunsPath(workspace):          string(runsRaw),
		webhookInputPath(workspace, "run-1"): string(deliveryRaw),
	}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	router := mux.NewRouter()
	WorkflowWebhookRoutes(router, &SchedulerService{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/workflow-webhooks/"+sched.ID+"/runs/run-1/payload", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("payload: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		RawPayload string `json:"raw_payload"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.RawPayload != `{"action":"opened","nested":{"value":42}}` {
		t.Fatalf("unexpected payload: %q", response.RawPayload)
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/workflow-webhooks/"+sched.ID+"/runs/other-run/payload", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-run payload lookup: %d %s", w.Code, w.Body.String())
	}
}
