package services

import (
	"strings"
	"testing"
)

func TestFormatWhatsAppSlugChoicesSortsConfiguredRoutes(t *testing.T) {
	message := formatWhatsAppSlugChoices(WhatsAppRouting{
		"testing":     {WorkflowID: "wf-testing"},
		"customer":    {WorkflowID: "wf-customer"},
		"customer-qa": {WorkflowID: "wf-customer-qa"},
	})

	wantOrder := []string{"1. @customer", "2. @customer-qa", "3. @testing"}
	last := -1
	for _, want := range wantOrder {
		at := strings.Index(message, want)
		if at < 0 {
			t.Fatalf("choice message %q does not contain %q", message, want)
		}
		if at <= last {
			t.Fatalf("choice message %q does not keep sorted order %v", message, wantOrder)
		}
		last = at
	}
	if !strings.Contains(message, "until you send @off") {
		t.Fatalf("choice message %q does not explain route memory", message)
	}
}

func TestFormatWhatsAppSlugChoicesExplainsEmptyRouting(t *testing.T) {
	message := formatWhatsAppSlugChoices(nil)
	if !strings.Contains(message, "No WhatsApp destinations are configured") || !strings.Contains(message, "workflows and Crews") {
		t.Fatalf("empty-route message = %q", message)
	}
}

func TestFormatWhatsAppSlugChoicesIncludesCrewRoutes(t *testing.T) {
	message := formatWhatsAppSlugChoices(WhatsAppRouting{
		"invoice-processing": {WorkflowID: "wf-invoices", WorkspacePath: "Workflow/invoices"},
		"company-ca": {
			ProfileID:       "work",
			ConversationKey: "company-ca-1234",
			WorkspacePath:   "Chats/Work/projects/company-ca-1234",
			ProfileLabel:    "Company CA",
		},
	})
	for _, want := range []string{"Choose a workflow or Crew", "@company-ca", "@invoice-processing"} {
		if !strings.Contains(message, want) {
			t.Fatalf("choice message %q does not contain %q", message, want)
		}
	}
}
