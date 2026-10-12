package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

const dashboardQuestionRegistryPath = "_system/dashboard_questions/resources.json"

// As with the messaging store, mutations are serialized by the API process and
// persisted atomically by the workspace backend. These files are server-owned,
// never project evidence or a source of access grants.
var dashboardQuestionMu sync.Mutex

type dashboardQuestionResource struct {
	ID        string `json:"id"`
	Root      string `json:"root"`
	ProjectID string `json:"project_id"`
	Kind      string `json:"kind"`
	Dashboard string `json:"dashboard"`
}

type dashboardQuestionRegistry struct {
	Resources []dashboardQuestionResource `json:"resources"`
}

type dashboardQuestionConversation struct {
	ID         string    `json:"conversation_id"`
	ResourceID string    `json:"resource_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type dashboardQuestionTurn struct {
	ID             string                   `json:"question_id"`
	ConversationID string                   `json:"conversation_id"`
	ResourceID     string                   `json:"resource_id"`
	SubmissionID   string                   `json:"submission_id"`
	RequestHash    string                   `json:"request_hash"`
	Question       string                   `json:"question"`
	Revision       string                   `json:"captured_revision"`
	Context        dashboardQuestionContext `json:"context"`
	State          string                   `json:"state"`
	Answer         string                   `json:"answer,omitempty"`
	Error          string                   `json:"error,omitempty"`
	CreatedAt      time.Time                `json:"created_at"`
	UpdatedAt      time.Time                `json:"updated_at"`
	Runtime        string                   `json:"runtime"`
}

type dashboardQuestionStore struct {
	Viewer        string                          `json:"viewer"`
	Conversations []dashboardQuestionConversation `json:"conversations"`
	Turns         []dashboardQuestionTurn         `json:"turns"`
}

func dashboardQuestionStorePath(viewer string) string {
	sum := sha256.Sum256([]byte(viewer))
	return "_system/dashboard_questions/viewers/" + hex.EncodeToString(sum[:]) + ".json"
}

func readDashboardQuestionJSON(ctx context.Context, p string, out any) error {
	raw, exists, err := readFileFromWorkspace(ctx, p)
	if err != nil || !exists {
		return err
	}
	return json.Unmarshal([]byte(raw), out)
}

func saveDashboardQuestionJSON(ctx context.Context, p string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, p, string(raw))
}

// Called only after ordinary dashboard authorization. A stable random resource
// id names a project/dashboard pair; it never conveys sharing permission.
func registerDashboardQuestionResource(ctx context.Context, target dashboardTarget, slug string) (string, error) {
	dashboardQuestionMu.Lock()
	defer dashboardQuestionMu.Unlock()
	var registry dashboardQuestionRegistry
	if err := readDashboardQuestionJSON(ctx, dashboardQuestionRegistryPath, &registry); err != nil {
		return "", err
	}
	for _, resource := range registry.Resources {
		if resource.Root == target.Root && resource.ProjectID == target.ID && resource.Kind == target.Kind && resource.Dashboard == slug {
			return resource.ID, nil
		}
	}
	if len(registry.Resources) >= 10000 {
		return "", dashboardFailure(503, "dashboard resource registry is full")
	}
	id := uuid.NewString()
	registry.Resources = append(registry.Resources, dashboardQuestionResource{ID: id, Root: target.Root, ProjectID: target.ID, Kind: target.Kind, Dashboard: slug})
	return id, saveDashboardQuestionJSON(ctx, dashboardQuestionRegistryPath, registry)
}

func addDashboardQuestionResource(ctx context.Context, target dashboardTarget, document dashboards.Dashboard, row map[string]any) {
	// Legacy loose HTML has no immutable publication history. Keep its current
	// discovery behavior; the question contract starts with managed bundles.
	if !document.Managed {
		return
	}
	id, err := registerDashboardQuestionResource(ctx, target, document.ID)
	if err != nil {
		// Question availability must not become a dependency of ordinary
		// dashboard discovery. No id means the caller cannot offer Ask.
		log.Printf("[DASHBOARD QUESTIONS] Resource registration unavailable: %v", err)
		return
	}
	row["resource_id"] = id
}

// Caller holds dashboardQuestionMu. An interrupted request is never silently
// re-run after a restart. A retry returns its durable terminal status instead.
func (api *StreamingAPI) readDashboardQuestions(ctx context.Context, viewer string) (dashboardQuestionStore, error) {
	store := dashboardQuestionStore{Viewer: viewer}
	p := dashboardQuestionStorePath(viewer)
	if err := readDashboardQuestionJSON(ctx, p, &store); err != nil {
		return store, err
	}
	if store.Viewer != viewer {
		return store, dashboardFailure(404, "conversation unavailable")
	}
	if api.dashboardQuestionRuntime == "" {
		api.dashboardQuestionRuntime = uuid.NewString()
	}
	changed := false
	for i := range store.Turns {
		turn := &store.Turns[i]
		if (turn.State == "queued" || turn.State == "running") && turn.Runtime != api.dashboardQuestionRuntime {
			turn.State, turn.Error, turn.UpdatedAt = "failed", "interrupted", time.Now().UTC()
			changed = true
		}
	}
	if changed {
		return store, saveDashboardQuestionJSON(ctx, p, store)
	}
	return store, nil
}

// Internal hash and runtime fields never leave the server.
func dashboardQuestionResponse(turn dashboardQuestionTurn) map[string]any {
	return map[string]any{
		"question_id": turn.ID, "conversation_id": turn.ConversationID,
		"resource_id": turn.ResourceID, "submission_id": turn.SubmissionID,
		"question": turn.Question, "captured_revision": turn.Revision, "context": turn.Context,
		"state": turn.State, "answer": turn.Answer, "error": turn.Error,
		"created_at": turn.CreatedAt, "updated_at": turn.UpdatedAt,
	}
}
