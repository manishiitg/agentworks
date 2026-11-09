package server

import (
	"context"
	"strings"
	"testing"
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
	trigger.RunDestination = runDestinationNew
	if got := productWebhookDTO(trigger).RunDestination; got != runDestinationNew || !trigger.ownConversation() || !trigger.runDestinationSetting().New {
		t.Fatalf("a new conversation each time is saved and shown as %q", got)
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
