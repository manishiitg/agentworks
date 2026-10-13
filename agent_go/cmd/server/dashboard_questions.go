package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// Client observations are stored verbatim as untrusted context. They never
// select a source workspace, grant access, or authorize tools.
type dashboardQuestionContext struct {
	SelectedText string            `json:"selected_text,omitempty"`
	NearbyLabel  string            `json:"nearby_label,omitempty"`
	Row          []string          `json:"row,omitempty"`
	Column       string            `json:"column,omitempty"`
	ElementID    string            `json:"element_id,omitempty"`
	CapturedAt   time.Time         `json:"captured_at,omitempty"`
	Filters      map[string]string `json:"filters,omitempty"`
	RecordIDs    []string          `json:"record_ids,omitempty"`
	RunIDs       []string          `json:"run_ids,omitempty"`
}

type dashboardQuestionSubmission struct {
	Revision       string                   `json:"captured_revision"`
	Context        dashboardQuestionContext `json:"context"`
	Question       string                   `json:"question"`
	ConversationID string                   `json:"conversation_id,omitempty"`
	SubmissionID   string                   `json:"submission_id"`
}

func (api *StreamingAPI) registerDashboardQuestionRoutes(router *mux.Router) {
	router.HandleFunc("/dashboards/{dashboard}", api.handleDashboardQuestionView).Methods(http.MethodGet)
	router.HandleFunc("/dashboards/{dashboard}/questions", api.handleDashboardQuestionSubmit).Methods(http.MethodPost)
	router.HandleFunc("/dashboards/{dashboard}/questions/{question}", api.handleDashboardQuestionRead).Methods(http.MethodGet)
	router.HandleFunc("/dashboards/{dashboard}/conversations/{conversation}", api.handleDashboardQuestionRead).Methods(http.MethodGet)
	router.HandleFunc("/dashboards/{dashboard}/questions/{question}/cancel", api.handleDashboardQuestionCancel).Methods(http.MethodPost)
}

func (api *StreamingAPI) dashboardQuestionRequest(w http.ResponseWriter, r *http.Request, revision string, ask bool) (resolvedDashboardQuestion, bool) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		externalError(w, 401, "authentication_required", "sign in to ask about a dashboard")
		return resolvedDashboardQuestion{}, false
	}
	out, err := api.resolveDashboardQuestion(r.Context(), claims, mux.Vars(r)["dashboard"], revision)
	if err != nil {
		dashboardError(w, err)
		return out, false
	}
	if !out.Capability.CanView || ask && !out.Capability.CanAsk {
		externalError(w, 403, out.Capability.Reason, "dashboard questions are unavailable for this viewer")
		return out, false
	}
	return out, true
}

func (api *StreamingAPI) handleDashboardQuestionView(w http.ResponseWriter, r *http.Request) {
	resolved, ok := api.dashboardQuestionRequest(w, r, "", false)
	if !ok {
		return
	}
	externalJSON(w, map[string]any{"resource_id": resolved.Resource.ID, "project_id": resolved.Target.ID, "product": resolved.Target.Kind, "dashboard": resolved.Document, "capability": resolved.Capability})
}

func (api *StreamingAPI) handleDashboardQuestionSubmit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var args dashboardQuestionSubmission
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil || decoder.Decode(new(any)) != io.EOF {
		externalError(w, 400, "invalid_arguments", "invalid dashboard question")
		return
	}
	args.Question = strings.TrimSpace(args.Question)
	if args.Question == "" || len(args.Question) > 4096 || !utf8.ValidString(args.Question) || args.SubmissionID == "" || len(args.SubmissionID) > 128 || !utf8.ValidString(args.SubmissionID) || len(args.Revision) != 64 {
		externalError(w, 400, "invalid_arguments", "question, captured revision and stable submission id required")
		return
	}
	if args.ConversationID != "" {
		if _, err := uuid.Parse(args.ConversationID); err != nil {
			externalError(w, 400, "invalid_arguments", "invalid conversation id")
			return
		}
	}
	contextJSON, _ := json.Marshal(args.Context)
	if len(contextJSON) > 16<<10 {
		externalError(w, 400, "invalid_arguments", "dashboard context is too large")
		return
	}
	resolved, ok := api.dashboardQuestionRequest(w, r, args.Revision, true)
	if !ok {
		return
	}
	viewer := GetUserFromContext(r.Context()).UserID
	raw, _ := json.Marshal(struct {
		Resource string `json:"resource"`
		dashboardQuestionSubmission
	}{resolved.Resource.ID, args})
	hash := sha256.Sum256(raw)
	requestHash := hex.EncodeToString(hash[:])
	turn, created, err := api.submitDashboardQuestion(r.Context(), viewer, resolved.Resource.ID, args, requestHash)
	if err != nil {
		dashboardError(w, err)
		return
	}
	if created {
		// Use a bounded server context so a client disconnect does not discard
		// the durable submission. Authorization is checked again at dispatch.
		claims := *GetUserFromContext(r.Context())
		go api.dispatchDashboardQuestion(claims, turn)
	}
	response := dashboardQuestionResponse(turn)
	response["current_revision"] = resolved.Document.Published
	response["captured_is_current"] = turn.Revision == resolved.Document.Published
	externalJSON(w, response)
}

func (api *StreamingAPI) submitDashboardQuestion(ctx context.Context, viewer, resource string, args dashboardQuestionSubmission, hash string) (dashboardQuestionTurn, bool, error) {
	unlock := dashboardQuestionViewerLocks.Lock(viewer)
	defer unlock()
	store, err := api.readDashboardQuestions(ctx, viewer)
	if err != nil {
		return dashboardQuestionTurn{}, false, err
	}
	for _, turn := range store.Turns {
		if turn.SubmissionID == args.SubmissionID {
			if turn.RequestHash != hash {
				return dashboardQuestionTurn{}, false, dashboardFailure(409, "submission id already used for a different question")
			}
			return turn, false, nil
		}
	}
	conversation := args.ConversationID
	if conversation != "" {
		found := false
		for _, c := range store.Conversations {
			if c.ID == conversation && c.ResourceID == resource {
				found = true
				break
			}
		}
		if !found {
			return dashboardQuestionTurn{}, false, dashboardFailure(404, "conversation unavailable")
		}
	}
	// Keep idempotency receipts instead of evicting them and risking replay.
	if len(store.Turns) >= 1000 || conversation == "" && len(store.Conversations) >= 300 {
		return dashboardQuestionTurn{}, false, dashboardFailure(429, "dashboard question history is full")
	}
	now := time.Now().UTC()
	if conversation == "" {
		conversation = uuid.NewString()
		store.Conversations = append(store.Conversations, dashboardQuestionConversation{ID: conversation, ResourceID: resource, CreatedAt: now})
	}
	turn := dashboardQuestionTurn{ID: uuid.NewString(), ConversationID: conversation, ResourceID: resource, SubmissionID: args.SubmissionID, RequestHash: hash, Question: args.Question, Revision: args.Revision, Context: args.Context, State: "queued", CreatedAt: now, UpdatedAt: now, Runtime: api.dashboardQuestionRuntime}
	store.Turns = append(store.Turns, turn)
	err = saveDashboardQuestionJSON(ctx, dashboardQuestionStorePath(viewer), store)
	return turn, err == nil, err
}

// Slice one exercises the real turn contract without a model or any execution
// adapter. In particular, a transport receipt can never mark a turn answered.
func (api *StreamingAPI) dispatchDashboardQuestion(claims UserClaims, turn dashboardQuestionTurn) {
	unlock := api.dashboardQuestionLanes.Lock(claims.UserID)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !api.transitionDashboardQuestion(ctx, claims.UserID, turn.ID, "queued", "running", "") {
		return
	}
	errorCode := "adapter_not_implemented"
	resolved, err := api.resolveDashboardQuestion(ctx, &claims, turn.ResourceID, turn.Revision)
	if err != nil || !resolved.Capability.CanAsk {
		errorCode = "access_revoked"
	} else {
		// The target adapter inherits this admitted origin. It annotates the
		// target's cost events rather than charging a second dashboard event.
		ctx = costActivityContext(ctx, claims.UserID, "dashboard_question", "")
		a := costobserver.AttributionFromContext(ctx)
		a.OriginDashboardID, a.OriginDashboardName = resolved.Resource.ID, resolved.Document.Title
		ctx = costobserver.ContextWithAttribution(ctx, a)
	}
	api.transitionDashboardQuestion(ctx, claims.UserID, turn.ID, "running", "failed", errorCode)
}

func (api *StreamingAPI) transitionDashboardQuestion(ctx context.Context, viewer, id, from, to, code string) bool {
	unlock := dashboardQuestionViewerLocks.Lock(viewer)
	defer unlock()
	store, err := api.readDashboardQuestions(ctx, viewer)
	if err != nil {
		return false
	}
	for i := range store.Turns {
		turn := &store.Turns[i]
		if turn.ID == id && turn.State == from && turn.Runtime == api.dashboardQuestionRuntime {
			turn.State, turn.Error, turn.UpdatedAt = to, code, time.Now().UTC()
			return saveDashboardQuestionJSON(ctx, dashboardQuestionStorePath(viewer), store) == nil
		}
	}
	return false
}

func (api *StreamingAPI) handleDashboardQuestionRead(w http.ResponseWriter, r *http.Request) {
	resolved, ok := api.dashboardQuestionRequest(w, r, "", false)
	if !ok {
		return
	}
	viewer := GetUserFromContext(r.Context()).UserID
	unlock := dashboardQuestionViewerLocks.Lock(viewer)
	store, err := api.readDashboardQuestions(r.Context(), viewer)
	unlock()
	if err != nil {
		dashboardError(w, err)
		return
	}
	vars := mux.Vars(r)
	turns := []map[string]any{}
	evidence := map[string]error{resolved.Document.Published: nil}
	found := false
	for _, conversation := range store.Conversations {
		if conversation.ID == vars["conversation"] && conversation.ResourceID == resolved.Resource.ID {
			found = true
		}
	}
	for _, turn := range store.Turns {
		if turn.ResourceID != resolved.Resource.ID || (vars["question"] != "" && turn.ID != vars["question"]) || (vars["conversation"] != "" && turn.ConversationID != vars["conversation"]) {
			continue
		}
		// Old captured snapshots are evidence only while still readable now.
		evidenceErr, checked := evidence[turn.Revision]
		if !checked {
			_, evidenceErr = api.resolveDashboardQuestionResource(r.Context(), GetUserFromContext(r.Context()), resolved.Resource, turn.Revision)
			evidence[turn.Revision] = evidenceErr
		}
		found = true
		response := dashboardQuestionResponse(turn)
		if evidenceErr != nil {
			if vars["question"] != "" {
				dashboardError(w, evidenceErr)
				return
			}
			// Preserve the place in history without returning stale evidence,
			// questions quoting that evidence, or answers derived from it.
			response = map[string]any{
				"question_id": turn.ID, "conversation_id": turn.ConversationID,
				"resource_id": turn.ResourceID, "state": turn.State,
				"captured_revision": turn.Revision, "created_at": turn.CreatedAt,
				"updated_at": turn.UpdatedAt, "error": "captured_evidence_unavailable",
			}
		}
		response["evidence_available"] = evidenceErr == nil
		response["current_revision"] = resolved.Document.Published
		response["captured_is_current"] = turn.Revision == resolved.Document.Published
		turns = append(turns, response)
	}
	if !found {
		externalError(w, 404, "not_found", "conversation unavailable")
		return
	}
	if vars["question"] != "" {
		externalJSON(w, turns[0])
		return
	}
	externalJSON(w, map[string]any{"conversation_id": vars["conversation"], "resource_id": resolved.Resource.ID, "questions": turns})
}

func (api *StreamingAPI) handleDashboardQuestionCancel(w http.ResponseWriter, r *http.Request) {
	resolved, ok := api.dashboardQuestionRequest(w, r, "", false)
	if !ok {
		return
	}
	viewer := GetUserFromContext(r.Context()).UserID
	unlock := dashboardQuestionViewerLocks.Lock(viewer)
	store, err := api.readDashboardQuestions(r.Context(), viewer)
	unlock()
	if err != nil {
		dashboardError(w, err)
		return
	}
	for _, turn := range store.Turns {
		if turn.ID == mux.Vars(r)["question"] && turn.ResourceID == resolved.Resource.ID {
			if _, err := api.resolveDashboardQuestion(r.Context(), GetUserFromContext(r.Context()), turn.ResourceID, turn.Revision); err != nil {
				dashboardError(w, err)
				return
			}
			break
		}
	}
	unlock = dashboardQuestionViewerLocks.Lock(viewer)
	defer unlock()
	store, err = api.readDashboardQuestions(r.Context(), viewer)
	if err != nil {
		dashboardError(w, err)
		return
	}
	for i := range store.Turns {
		turn := &store.Turns[i]
		if turn.ID != mux.Vars(r)["question"] || turn.ResourceID != resolved.Resource.ID {
			continue
		}
		if turn.State == "queued" || turn.State == "running" {
			turn.State, turn.Error, turn.UpdatedAt = "cancelled", "", time.Now().UTC()
			if err := saveDashboardQuestionJSON(r.Context(), dashboardQuestionStorePath(viewer), store); err != nil {
				dashboardError(w, err)
				return
			}
		}
		externalJSON(w, dashboardQuestionResponse(*turn))
		return
	}
	externalError(w, 404, "not_found", "question unavailable")
}
