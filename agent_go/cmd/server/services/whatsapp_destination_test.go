package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoverWhatsAppDestinationsIncludesOwnedCrewProjects(t *testing.T) {
	SetCrewScopeHooks(nil, nil, func(_ context.Context, userID string) []SharedCrewListing {
		if userID != "owner-1" {
			t.Errorf("Crew listing user = %q, want owner-1", userID)
		}
		return []SharedCrewListing{{ID: "company-ca", Title: "Company CA", WorkspacePath: "Crew/company-ca", ProfileID: "work"}}
	})
	t.Cleanup(func() { SetCrewScopeHooks(nil, nil, nil) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-User-ID"); got != "owner-1" {
			t.Errorf("X-User-ID = %q, want owner-1", got)
		}
		switch {
		case r.URL.Path == "/api/documents" && r.URL.Query().Get("folder") == "Workflow":
			fmt.Fprint(w, `{"success":true,"data":[{"filepath":"Workflow/invoices/workflow.json","type":"file"},{"filepath":"Workflow/review-relay/workflow.json","type":"file"}]}`)
		case r.URL.Path == "/api/documents/Workflow/invoices/workflow.json":
			fmt.Fprint(w, `{"success":true,"data":{"filepath":"Workflow/invoices/workflow.json","content":"{\"id\":\"wf-invoices\",\"label\":\"Invoice Processing\"}"}}`)
		case r.URL.Path == "/api/documents/Workflow/review-relay/workflow.json":
			fmt.Fprint(w, `{"success":true,"data":{"filepath":"Workflow/review-relay/workflow.json","content":"{\"id\":\"relay-review\",\"kind\":\"relay\",\"label\":\"Review Relay\"}"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	candidates, err := (&WhatsAppService{}).discoverDestinationCandidates(context.Background(), &WhatsAppOwner{UserID: "owner-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 3 {
		t.Fatalf("destinations = %+v, want the owned Crew, Goal and Flow", candidates)
	}
	crew := candidates[0]
	if crew.Kind != "crew" || crew.Label != "Company CA" || crew.ProfileID != "work" || crew.ConversationKey != "company-ca" || crew.WorkspacePath != "Crew/company-ca" {
		t.Fatalf("Crew destination = %+v", crew)
	}
	flow := candidates[2]
	if flow.Kind != "relay" || flow.ID != "relay-review" || flow.kindLabel() != "Flow" || !flow.matchesRoute(flow.route("run")) {
		t.Fatalf("Flow destination did not preserve its actual route: %+v", flow)
	}
	workflow := candidates[1]
	if workflow.Kind != "workflow" || workflow.ID != "wf-invoices" || workflow.Label != "Invoice Processing" {
		t.Fatalf("workflow destination = %+v", workflow)
	}
}

func TestDiscoverWhatsAppDestinationsHidesInaccessible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/documents" && r.URL.Query().Get("folder") == "Workflow":
			fmt.Fprint(w, `{"success":true,"data":[{"filepath":"Workflow/invoices/workflow.json","type":"file"},{"filepath":"Workflow/secret/workflow.json","type":"file"}]}`)
		case r.URL.Path == "/api/documents" && r.URL.Query().Get("folder") == "Chats/Work/projects":
			fmt.Fprint(w, `{"success":true,"data":[]}`)
		case r.URL.Path == "/api/documents/Workflow/invoices/workflow.json":
			fmt.Fprint(w, `{"success":true,"data":{"filepath":"Workflow/invoices/workflow.json","content":"{\"id\":\"wf-invoices\",\"label\":\"Invoice Processing\"}"}}`)
		case r.URL.Path == "/api/documents/Workflow/secret/workflow.json":
			fmt.Fprint(w, `{"success":true,"data":{"filepath":"Workflow/secret/workflow.json","content":"{\"id\":\"wf-secret\",\"label\":\"Secret\"}"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	svc := &WhatsAppService{}
	svc.SetWorkflowAccessFunc(func(ctx context.Context, userID string, route ChannelRoute) (bool, error) {
		if userID != "owner-1" {
			t.Errorf("access check userID = %q, want owner-1", userID)
		}
		return route.WorkflowID != "wf-secret", nil
	})
	candidates, err := svc.discoverDestinationCandidates(context.Background(), &WhatsAppOwner{UserID: "owner-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ID != "wf-invoices" || candidates[0].Number != 1 {
		t.Fatalf("destinations = %+v, want only accessible wf-invoices numbered 1", candidates)
	}
}

func TestWhatsAppDestinationListIncludesWorkflowsAndCrews(t *testing.T) {
	candidates := []whatsappDestinationCandidate{
		{Number: 1, Kind: "crew", ID: "company-ca", Label: "Company CA"},
		{Number: 2, Kind: "workflow", ID: "wf-invoices", Label: "Invoice Processing"},
	}
	message := formatWhatsAppDestinationList(candidates)
	for _, want := range []string{"Goals, Crews and Flows:", "1. Crew: Company CA", "2. Workflow: Invoice Processing"} {
		if !strings.Contains(message, want) {
			t.Fatalf("destination list %q does not contain %q", message, want)
		}
	}
}

func TestCrewDestinationBuildsAWorkProfileRoute(t *testing.T) {
	candidate := whatsappDestinationCandidate{
		Kind:            "crew",
		ID:              "company-ca-1234",
		Label:           "Company CA",
		WorkspacePath:   "Chats/Work/projects/company-ca-1234",
		ProfileID:       "work",
		ConversationKey: "company-ca-1234",
		ProfileLabel:    "Company CA",
	}
	route := candidate.route("workshop")
	if route.WorkflowID != "" || route.ProfileID != "work" || route.ConversationKey != candidate.ID || route.WorkspacePath != candidate.WorkspacePath {
		t.Fatalf("Crew route = %+v", route)
	}
	if route.WorkshopMode != "run" || route.BotGrant != "run" {
		t.Fatalf("Crew route permissions = %+v, want run-only", route)
	}
	if !candidate.matchesRoute(route) || candidate.autoRouteKey() != "crew:company-ca-1234" {
		t.Fatalf("Crew destination did not recognize its route: candidate=%+v route=%+v", candidate, route)
	}
}

func TestAssignDestinationRouteKeepsSameNameWorkflowAndCrewDistinct(t *testing.T) {
	svc := &WhatsAppService{routing: make(WhatsAppRouting)}
	workflow := whatsappDestinationCandidate{Kind: "workflow", ID: "wf-company-ca", Label: "Company CA", Slug: "company-ca", WorkspacePath: "Workflow/company-ca"}
	crew := whatsappDestinationCandidate{
		Kind: "crew", ID: "company-ca-1234", Label: "Company CA", Slug: "company-ca",
		WorkspacePath: "Chats/Work/projects/company-ca-1234", ProfileID: "work", ConversationKey: "company-ca-1234", ProfileLabel: "Company CA",
	}
	if got := svc.assignDestinationRouteLocked(workflow, "run"); got != "company-ca" {
		t.Fatalf("workflow slug = %q", got)
	}
	if got := svc.assignDestinationRouteLocked(crew, "workshop"); got != "company-ca-company-ca-1234" {
		t.Fatalf("Crew collision slug = %q", got)
	}
	route := svc.routing["company-ca-company-ca-1234"]
	if route.ProfileID != "work" || route.ConversationKey != "company-ca-1234" || route.WorkshopMode != "run" {
		t.Fatalf("Crew collision route = %+v", route)
	}
}

func TestMatchWhatsAppDestinationCandidateCanSelectCrewByNumberOrName(t *testing.T) {
	candidates := []whatsappDestinationCandidate{
		{Number: 1, Kind: "workflow", ID: "wf-report", Label: "Report"},
		{Number: 2, Kind: "crew", ID: "company-ca", Label: "Company CA", Slug: "company-ca"},
	}
	for _, query := range []string{"2", "Company CA", "company-ca"} {
		candidate, _ := matchWhatsAppDestinationCandidate(candidates, query)
		if candidate == nil || candidate.Kind != "crew" || candidate.ID != "company-ca" {
			t.Fatalf("query %q matched %+v, want Company CA Crew", query, candidate)
		}
	}
}
