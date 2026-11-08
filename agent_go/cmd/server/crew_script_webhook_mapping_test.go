package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtrigger"
)

// PLAT-746-450: raw signed deliveries must reach actual Python with typed
// values and the saved preset, rather than being skipped as old plan routes.
func checkSavedScriptRawWebhook(t *testing.T, api *StreamingAPI, binding crewExecutableBinding, verify func(string, string, string, string)) {
	t.Helper()
	ctx := t.Context()
	id := uuid.NewString()
	secret := "fixture-hook-credential"
	manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
	if err != nil || !found {
		t.Fatal(err)
	}
	ciphertext, err := encryptSecretValueWithAAD(secret, webhookAAD(manifest.ID, id))
	if err != nil {
		t.Fatal(err)
	}
	schedule := WorkflowSchedule{ID: id, Name: "Raw script inputs", ScheduleType: "webhook", Mode: "workshop", WorkshopMode: "run", Enabled: true, ChatID: "main", InputPreset: "Saved", Webhook: &WorkflowWebhookConfig{AuthMode: "bearer", EncryptedSecret: ciphertext, InputMode: "raw", AllowedVariables: []string{"count"}, PayloadMappings: &WorkflowWebhookPayloadMappings{Inputs: map[string]workflowtrigger.InputMapping{"count": {Source: "event.count"}}}}}
	if err := validateWebhookPayloadMappings(ctx, binding.Root, schedule.Webhook, nil, nil); err != nil {
		t.Fatal(err)
	}
	manifest.Schedules = append(manifest.Schedules, schedule)
	if err := WriteWorkflowManifest(ctx, binding.Root, manifest); err != nil {
		t.Fatal(err)
	}
	receiver := webhookReceiver{find: findScheduleByIDAny, start: api.scheduler.triggerSavedSchedule, existing: api.scheduler.existingWebhookRun}
	router := mux.NewRouter()
	router.HandleFunc("/api/hooks/workflow/{id}", receiver.receive).Methods("POST")
	endpoint := httptest.NewServer(router)
	defer endpoint.Close()
	post := func(delivery, body string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "POST", endpoint.URL+"/api/hooks/workflow/"+id, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", delivery)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response.StatusCode, raw
	}
	if status, raw := post("invalid-input", `{"event":{"count":"not-an-integer"}}`); status != 400 || !strings.Contains(string(raw), "count") {
		t.Fatalf("bad typed field was accepted: HTTP%d %s", status, raw)
	}
	// A queued delivery must be validated again against the current type and
	// fixed values, rather than trust admission's old contract snapshot.
	for _, invalid := range []WorkflowSchedule{
		{InputPreset: "Saved", Inputs: map[string]string{"count": "3"}, Webhook: schedule.Webhook},
		{InputPreset: "missing-preset", Webhook: schedule.Webhook},
	} {
		if err := webhookDeliveryStartError(context.Background(), binding.Root, invalid, &WorkflowWebhookDelivery{Variables: map[string]string{"count": "7"}}); err == nil {
			t.Fatal("changed saved inputs/preset escaped launch revalidation")
		}
	}
	if status, raw := post("missing-without-default", `{}`); status != 400 || !strings.Contains(string(raw), "count") {
		t.Fatalf("missing mapped input without default: HTTP%d %s", status, raw)
	}
	status, raw := post("valid-input", `{"event":{"count":7},"input_preset":"not-selectable"}`)
	var accepted struct {
		RunID string `json:"run_id"`
	}
	if status != 202 || json.Unmarshal(raw, &accepted) != nil || accepted.RunID == "" {
		t.Fatalf("raw webhook HTTP%d %s", status, raw)
	}
	verify(accepted.RunID, id, "7", "Saved")
	// PLAT-746-450: an absent source uses the declared script default; a
	// present value of the wrong type must still fail instead of using it.
	definition, err := readCrewScript(ctx, binding.Root)
	if err != nil {
		t.Fatal(err)
	}
	for i := range definition.Inputs {
		if definition.Inputs[i].Name == "count" {
			definition.Inputs[i].Value = "7"
		}
	}
	if err := saveCrewScriptInputs(ctx, binding.Root, definition.InputDefinitions()); err != nil {
		t.Fatal(err)
	}
	status, raw = post("declared-default", `{}`)
	if status != 202 || json.Unmarshal(raw, &accepted) != nil || accepted.RunID == "" {
		t.Fatalf("declared default webhook HTTP%d %s", status, raw)
	}
	verify(accepted.RunID, id, "7", "Saved")
	if status, raw := post("bad-supplied-with-default", `{"event":{"count":"invalid"}}`); status != 400 || !strings.Contains(string(raw), "count") {
		t.Fatalf("bad supplied value used default: HTTP%d %s", status, raw)
	}
}
