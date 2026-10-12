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
var dashboardQuestionRegistryMu sync.Mutex
var dashboardQuestionViewerLocks dashboardQuestionLockSet

// Waiters hold a reference before waiting, so an idle key can be removed
// without letting a later caller acquire a different lock for the same key.
type dashboardQuestionLockSet struct {
	mu    sync.Mutex
	locks map[string]*dashboardQuestionLock
}

type dashboardQuestionLock struct {
	mu   sync.Mutex
	refs int
}

func (s *dashboardQuestionLockSet) Lock(key string) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[string]*dashboardQuestionLock{}
	}
	lock := s.locks[key]
	if lock == nil {
		lock = &dashboardQuestionLock{}
		s.locks[key] = lock
	}
	lock.refs++
	s.mu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.locks, key)
		}
		s.mu.Unlock()
	}
}

type dashboardQuestionDiscovery struct {
	Target   dashboardTarget
	Document dashboards.Dashboard
	Row      map[string]any
}

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
func registerDashboardQuestionResources(ctx context.Context, items []dashboardQuestionDiscovery) ([]string, error) {
	dashboardQuestionRegistryMu.Lock()
	defer dashboardQuestionRegistryMu.Unlock()
	var registry dashboardQuestionRegistry
	if err := readDashboardQuestionJSON(ctx, dashboardQuestionRegistryPath, &registry); err != nil {
		return nil, err
	}
	type key struct{ root, project, kind, dashboard string }
	ids := make(map[key]string, len(registry.Resources))
	for _, resource := range registry.Resources {
		ids[key{resource.Root, resource.ProjectID, resource.Kind, resource.Dashboard}] = resource.ID
	}
	result := make([]string, len(items))
	changed := false
	for i, item := range items {
		if !item.Document.Managed {
			continue
		}
		target, slug := item.Target, item.Document.ID
		k := key{target.Root, target.ID, target.Kind, slug}
		id := ids[k]
		if id == "" {
			if len(registry.Resources) >= 10000 {
				return nil, dashboardFailure(503, "dashboard resource registry is full")
			}
			id = uuid.NewString()
			registry.Resources = append(registry.Resources, dashboardQuestionResource{ID: id, Root: target.Root, ProjectID: target.ID, Kind: target.Kind, Dashboard: slug})
			ids[k], changed = id, true
		}
		result[i] = id
	}
	if changed {
		if err := saveDashboardQuestionJSON(ctx, dashboardQuestionRegistryPath, registry); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func addDashboardQuestionResource(ctx context.Context, target dashboardTarget, document dashboards.Dashboard, row map[string]any) {
	// Legacy loose HTML has no immutable publication history. Keep its current
	// discovery behavior; the question contract starts with managed bundles.
	if !document.Managed {
		return
	}
	addDashboardQuestionResources(ctx, []dashboardQuestionDiscovery{{target, document, row}})
}

func addDashboardQuestionResources(ctx context.Context, items []dashboardQuestionDiscovery) {
	managed := false
	for _, item := range items {
		managed = managed || item.Document.Managed
	}
	if !managed {
		return
	}
	ids, err := registerDashboardQuestionResources(ctx, items)
	if err != nil {
		// Question availability must not become a dependency of ordinary
		// dashboard discovery. No id means the caller cannot offer Ask.
		log.Printf("[DASHBOARD QUESTIONS] Resource registration unavailable: %v", err)
		return
	}
	for i, id := range ids {
		if id != "" {
			items[i].Row["resource_id"] = id
		}
	}
}

// Caller holds this viewer's persistence lock. An interrupted request is never silently
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
	api.dashboardQuestionRuntimeOnce.Do(func() {
		api.dashboardQuestionRuntime = uuid.NewString()
	})
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
