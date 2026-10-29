package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProductWebhookTriggerValidationAndDTO(t *testing.T) {
	trigger := productWebhookTrigger{
		ID: "4c98bba9-b433-4bf8-b1de-68eebd143a6c", Name: "Issue opened",
		Enabled: true, Message: "Triage the incoming issue",
		Webhook: &WorkflowWebhookConfig{AuthMode: "bearer", EncryptedSecret: "ciphertext"},
	}
	if err := validateProductWebhook(trigger); err != nil {
		t.Fatal(err)
	}
	dto := productWebhookDTO(trigger)
	if dto.Path != "/api/hooks/"+trigger.ID || dto.AuthMode != "bearer" || dto.Message != trigger.Message || dto.RunDestination != runDestinationChat {
		t.Fatalf("dto = %+v", dto)
	}
	trigger.RunDestination = runDestinationIsolated
	if got := productWebhookDTO(trigger).RunDestination; got != runDestinationIsolated {
		t.Fatalf("run destination = %q", got)
	}
	for name, mutate := range map[string]func(*productWebhookTrigger){
		"message": func(value *productWebhookTrigger) { value.Message = "" },
		"auth":    func(value *productWebhookTrigger) { value.Webhook.AuthMode = "none" },
		"secret":  func(value *productWebhookTrigger) { value.Webhook.EncryptedSecret = "" },
	} {
		copy := trigger
		webhook := *trigger.Webhook
		copy.Webhook = &webhook
		mutate(&copy)
		if err := validateProductWebhook(copy); err == nil {
			t.Fatalf("%s should be rejected", name)
		}
	}
}

func TestProductWebhookUsesItsOwnAuthenticationBoundary(t *testing.T) {
	if !shouldSkipAuth("/api/hooks/product/4c98bba9-b433-4bf8-b1de-68eebd143a6c") {
		t.Fatal("product webhook delivery must reach its own secret verifier without a user JWT")
	}
	if !shouldSkipAuth("/api/hooks/product/4c98bba9-b433-4bf8-b1de-68eebd143a6c/runs/run-1") {
		t.Fatal("product webhook run polling must reach its own secret verifier without a user JWT")
	}
	if shouldSkipAuth("/api/product-webhooks") {
		t.Fatal("product webhook configuration must still require user authentication")
	}
}

func TestProductWebhookStatusPath(t *testing.T) {
	if got := productWebhookStatusPath("trigger-1", "run-1"); got != "/api/hooks/trigger-1/runs/run-1" {
		t.Fatalf("status path = %q", got)
	}
}

func TestProductWebhookRunStatusDTO(t *testing.T) {
	completed := time.Now().UTC()
	for _, tt := range []struct {
		status   string
		terminal bool
	}{
		{"queued", false},
		{"running", false},
		{"success", true},
		{"error", true},
		{"stopped", true},
	} {
		entry := &ScheduleRunEntry{ID: "run-1", Status: tt.status, FinalResponse: "done", Error: "boom", SessionID: "sess-1", StartedAt: completed, CompletedAt: &completed}
		got := productWebhookRunStatusDTO(entry)
		if got.RunID != "run-1" || got.Status != tt.status || got.Terminal != tt.terminal {
			t.Fatalf("status=%q dto = %+v", tt.status, got)
		}
		if got.FinalResponse != "done" || got.Error != "boom" || got.SessionID != "sess-1" {
			t.Fatalf("status=%q dto dropped fields: %+v", tt.status, got)
		}
		if !got.StartedAt.Equal(completed) || got.CompletedAt == nil || !got.CompletedAt.Equal(completed) {
			t.Fatalf("status=%q dto dropped timestamps: %+v", tt.status, got)
		}
	}
}

// PLAT-871-15: moving authentication out of the manifest must keep only ciphertext.
func TestProjectWebhookFileStoresOnlyEncryptedSecret(t *testing.T) {
	_, _ = newScheduleRunWorkspaceStub(t)
	manifest := productProjectManifest{Product: "work", ID: "project-1", OwnerID: "owner"}
	manifest.Triggers = []productWebhookTrigger{{ID: "4c98bba9-b433-4bf8-b1de-68eebd143a6c", Name: "Deploy", Enabled: true, Message: "Inspect this deployment", Webhook: &WorkflowWebhookConfig{AuthMode: "github", EncryptedSecret: "encrypted-value"}}}
	ctx := context.Background()
	if err := saveCrewEntryActions(ctx, "Crew/project-1", manifest); err != nil {
		t.Fatal(err)
	}
	raw, found, err := readFileFromWorkspace(ctx, "Crew/project-1/webhooks.json")
	if err != nil || !found || !strings.Contains(raw, `"encrypted_secret": "encrypted-value"`) || strings.Contains(raw, `"secret"`) {
		t.Fatalf("webhook encryption storage contract: found=%v err=%v", found, err)
	}
}
