package server

import "testing"

// Pins the rule that an external client never receives stored webhook secrets, and that the server's own copy is untouched.
func TestExternalWorkflowViewDropsWebhookSecrets(t *testing.T) {
	item := DiscoveredWorkflow{Manifest: &WorkflowManifest{Schedules: []WorkflowSchedule{{ID: "s1", Webhook: &WorkflowWebhookConfig{AuthMode: "bearer", EncryptedSecret: "ciphertext"}}}}}
	view := externalWorkflowView(item)
	if got := view.Manifest.Schedules[0].Webhook.EncryptedSecret; got != "" {
		t.Fatalf("secret leaked: %q", got)
	}
	if view.Manifest.Schedules[0].Webhook.AuthMode != "bearer" || item.Manifest.Schedules[0].Webhook.EncryptedSecret != "ciphertext" {
		t.Fatal("view must keep the rest and leave the original intact")
	}
}
