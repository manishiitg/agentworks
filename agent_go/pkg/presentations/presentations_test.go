package presentations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	mcpagentevents "github.com/manishiitg/mcpagent/events"
)

// capturingWorkspaceServer fakes the one endpoint Upsert calls
// (/api/mutate) and records the last SQL params sent, so tests can assert
// what actually would have been written without a real SQLite file.
func capturingWorkspaceServer(t *testing.T) (*httptest.Server, *[]interface{}) {
	t.Helper()
	var lastParams []interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mutate" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var params workspace.MutateWorkflowDBParams
		if err := json.Unmarshal(body, &params); err != nil {
			t.Fatalf("server: decode request: %v", err)
		}
		if len(params.Statements) != 1 {
			t.Fatalf("expected exactly one statement, got %d", len(params.Statements))
		}
		lastParams = params.Statements[0].Params
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"results":[{"rows_affected":1}],"total_rows_affected":1}}`))
	}))
	return server, &lastParams
}

func validPresentation() Presentation {
	return Presentation{
		Kind:          "media.video",
		IdentityKey:   "outputs/final.mp4",
		Title:         "Launch video",
		WorkspacePath: "Chats/Example Product/projects/demo",
		SessionID:     "example-product:project:demo",
		Activity:      &orchestratorevents.PresentationActivity{Label: "Video ready", Destination: "Videos panel", Detail: "Ready to review"},
		Payload:       map[string]interface{}{"path": "outputs/final.mp4", "verdict": "pass"},
		Resources:     []map[string]string{{"kind": "workspace.file", "path": "outputs/final.mp4", "role": "primary"}},
	}
}

func TestUpsertWritesTheDeclaredKindNotAHardcodedOne(t *testing.T) {
	server, lastParams := capturingWorkspaceServer(t)
	defer server.Close()
	client := workspace.NewClient(server.URL)

	event, err := Upsert(context.Background(), client, validPresentation())
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if event.Kind != "media.video" {
		t.Errorf("event.Kind = %q, want %q", event.Kind, "media.video")
	}
	// Params order matches the SQL column list in Upsert: id, kind, ...
	if len(*lastParams) < 2 || (*lastParams)[1] != "media.video" {
		t.Fatalf("row kind = %v, want %q — a hardcoded kind would have written the wrong value silently", (*lastParams)[1], "media.video")
	}
}

// Two presentations with the same kind+identity key must resolve to the same
// row so a repeat show_video call updates the existing entry in place instead
// of duplicating it.
func TestUpsertIsStableForTheSameIdentity(t *testing.T) {
	first := IdentityFromKey("media.video:outputs/final.mp4")
	second := IdentityFromKey("media.video:outputs/final.mp4")
	if first != second {
		t.Fatalf("same identity key produced different ids: %q vs %q", first, second)
	}
	other := IdentityFromKey("media.video:outputs/other.mp4")
	if first == other {
		t.Fatal("different identity keys collided")
	}
	// Kind participates in identity too: the same file path presented under a
	// different kind must not collide with the video's row.
	crossKind := IdentityFromKey("media.image:outputs/final.mp4")
	if first == crossKind {
		t.Fatal("different kinds with the same path collided into one row")
	}
}

func TestWorkspaceDatabasePathStripsCanonicalUserScope(t *testing.T) {
	tests := map[string]string{
		"_users/default/Chats/Example Product/projects/demo": "Chats/Example Product/projects/demo/db/db.sqlite",
		"Chats/Example Product/projects/demo":                "Chats/Example Product/projects/demo/db/db.sqlite",
	}
	for input, want := range tests {
		if got := workspaceDatabasePath(input); got != want {
			t.Errorf("workspaceDatabasePath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestUpsertRequiresEveryField(t *testing.T) {
	server, _ := capturingWorkspaceServer(t)
	defer server.Close()
	client := workspace.NewClient(server.URL)

	cases := []struct {
		name   string
		break_ func(*Presentation)
	}{
		{"kind", func(p *Presentation) { p.Kind = "" }},
		{"identity key", func(p *Presentation) { p.IdentityKey = "" }},
		{"title", func(p *Presentation) { p.Title = "" }},
		{"workspace path", func(p *Presentation) { p.WorkspacePath = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validPresentation()
			tc.break_(&p)
			if _, err := Upsert(context.Background(), client, p); err == nil {
				t.Fatalf("missing %s should have been rejected before any network call", tc.name)
			}
		})
	}
}

func TestDeleteRemovesOnlyTheRequestedPresentation(t *testing.T) {
	server, lastParams := capturingWorkspaceServer(t)
	defer server.Close()
	if err := Delete(context.Background(), workspace.NewClient(server.URL), "Chats/Example Product/projects/demo", "presentation-video", "media.video"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(*lastParams) != 2 || (*lastParams)[0] != "presentation-video" || (*lastParams)[1] != "media.video" {
		t.Fatalf("delete params = %#v, want presentation id and kind", *lastParams)
	}
}

// Event is a type alias for orchestrator_events.PresentationUpdatedEvent, so
// this has nothing to unmarshal or convert by hand — the struct itself is
// what reaches the frontend, registered in cmd/schema-gen so it gets a real
// generated TypeScript interface instead of an untyped map. This asserts the
// alias resolves to a real EventData, so a future refactor that quietly
// breaks the registration fails to compile rather than failing silently at
// runtime.
func TestEventIsARealRegisteredEventType(t *testing.T) {
	var event Event
	var _ mcpagentevents.EventData = &event
	if got := event.GetEventType(); got != orchestratorevents.PresentationUpdated {
		t.Fatalf("GetEventType() = %q, want %q", got, orchestratorevents.PresentationUpdated)
	}
}

// The event must carry everything a listener needs to render without a
// second database round trip: no field here is optional in practice.
func TestUpsertReturnsEnoughToRenderWithoutASecondFetch(t *testing.T) {
	server, _ := capturingWorkspaceServer(t)
	defer server.Close()
	client := workspace.NewClient(server.URL)

	event, err := Upsert(context.Background(), client, validPresentation())
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if event.PresentationID == "" || event.Kind != "media.video" || event.Title == "" ||
		event.WorkspacePath == "" || len(event.Payload) == 0 || event.Activity == nil || event.Activity.Destination != "Videos panel" {
		t.Fatalf("event is missing fields a listener needs: %+v", event)
	}
}

// Upsert cannot know the database's true post-write revision (see the type's
// doc comment), so the struct must not carry a field for it — a field that
// looks authoritative and is quietly wrong is worse than no field at all.
func TestEventHasNoFabricatedRevisionField(t *testing.T) {
	if _, hasField := reflect.TypeOf(Event{}).FieldByName("Revision"); hasField {
		t.Error("Event has a Revision field; Upsert has no way to populate it correctly")
	}
}
