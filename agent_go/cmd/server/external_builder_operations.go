package server

import (
	"context"
	"database/sql"
	"errors"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// External authoring is opt-in at both deployment and connection level.
func externalBuilderEnabled() bool {
	return strings.EqualFold(os.Getenv("AGENTWORKS_MCP_BUILDER_ENABLED"), "true")
}

func externalBuilderDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("builder_chat", "Ask the configured Builder model to edit a selected workflow in your existing workflow chat (the owner's main chat). Requires builder:chat and current write access. Use a unique submission_id; retries with the same payload return the same operation. Pass wait_seconds to get the answer (or a pending question) in the same call; otherwise poll builder action=status. This release edits plans/code through managed tools; native shell, account administration and connected account tools are unavailable.", true, true, map[string]any{
		"message": map[string]any{"type": "string", "minLength": 1, "maxLength": 32000}, "submission_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "session_id": externalString("Optional existing chat belonging to you in this workflow; omit to continue the main/latest chat."), "wait_seconds": externalBuilderWaitSchema}, "message", "submission_id")
	add("builder_file_history", "List your audited Builder file edits for one workflow-relative path, including revisions and restore IDs. Previous credentials from the same account remain recoverable.", false, true,
		map[string]any{"path": externalString("Workflow-relative source file path, such as code/task.py.")}, "path")
	add("builder_restore_file", "Restore the content that preceded one audited Builder file edit. Requires the file's current revision to prevent overwriting newer work.", true, true,
		map[string]any{"path": externalString("Workflow-relative source file path."), "edit_id": externalString("Edit ID from builder action=file_history."), "expected_revision": externalString("Current revision from read_file, or missing.")}, "path", "edit_id", "expected_revision")
	// The workflow's Pulse, from MCP (never from Slack). Same grant as
	// builder_chat: talking to Pulse can make the Builder act within Pulse's
	// autonomy levels.
	add("builder_pulse_chat", "Send a message to the selected workflow's Pulse (the agent that owns its goal) and start its turn. Use for the goal: how it is doing, what Pulse is working on, direction for it. Poll builder action=pulse_status for the reply. Requires builder:chat and write access; the workflow's Pulse must be on with a goal in soul.md.", true, true,
		map[string]any{"message": map[string]any{"type": "string", "minLength": 1, "maxLength": 4000}}, "message")
	add("builder_pulse_status", "Read the selected workflow's Pulse conversation: whether it is busy, its latest messages (newest last), the latest goal check and pending decisions. Use after builder action=pulse_chat.", false, true,
		map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 40}})
	for _, name := range []string{"builder_status", "builder_reply_input", "builder_cancel"} {
		props := map[string]any{"operation_id": externalString("Operation ID returned by builder action=chat.")}
		required := []string{"operation_id"}
		desc := "Poll your connection's Builder operation: status, final answer, error and pending_inputs. Does not expose unrelated turns in the shared chat."
		if name == "builder_reply_input" {
			props["request_id"] = externalString("Pending input unique_id from builder action=status.")
			props["response"] = externalString("Answer to the pending question.")
			required = append(required, "request_id", "response")
			desc = "Answer this Builder operation's pending question. Requires current Builder permission."
		} else if name == "builder_cancel" {
			desc = "Cancel this connection's Builder operation and its queued/active work. Other turns in the main chat continue. Completed edits are not rolled back."
		} else {
			props["wait_seconds"] = externalBuilderWaitSchema
			desc += " Pass wait_seconds to wait for the answer or a pending question instead of polling."
		}
		add(name, desc, true, true, props, required...)
	}
}

// externalBuilderWaitSchema lets a caller wait for a Builder operation in one
// call instead of polling. Capped like every MCP wait, under the proxy timeout.
var externalBuilderWaitSchema = map[string]any{"type": "integer", "minimum": 0, "maximum": externalCrewMaxWaitSeconds, "description": "Seconds to wait for the final answer or a pending question before returning (max 25). On status queued/running, call builder action=status with wait_seconds again."}

type externalBuilderOperation struct {
	ID           string    `json:"operation_id"`
	UserID       string    `json:"-"`
	GrantID      string    `json:"-"`
	WorkflowID   string    `json:"workflow_id"`
	Workspace    string    `json:"-"`
	SessionID    string    `json:"session_id"`
	SubmissionID string    `json:"submission_id"`
	Message      string    `json:"-"`
	Status       string    `json:"status"`
	Answer       string    `json:"answer,omitempty"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	StartedAt    time.Time `json:"started_at,omitempty"`
}

type externalBuilderRuntime struct {
	sync.Mutex
	sessions map[string]*externalBuilderActive
}
type externalBuilderActive struct {
	sync.Mutex
	id       string
	finished bool
	cancel   func()
}

func (api *StreamingAPI) externalBuilderOwnsSession(session string) bool {
	api.externalBuilderRuntime.Lock()
	defer api.externalBuilderRuntime.Unlock()
	return api.externalBuilderRuntime.sessions[session] != nil
}
func (api *StreamingAPI) externalBuilderActive(op externalBuilderOperation) *externalBuilderActive {
	api.externalBuilderRuntime.Lock()
	defer api.externalBuilderRuntime.Unlock()
	active := api.externalBuilderRuntime.sessions[op.SessionID]
	if active != nil && active.id == op.ID {
		return active
	}
	return nil
}

// This database lives in server-owned auth state, outside editable workflows.
// The workspace queue carries only an operation selector, never its authority.
func openExternalBuilderStore() (*mcpOAuthStore, error) {
	s, err := openMCPOAuthStore()
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS external_builder_operations (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, grant_id TEXT NOT NULL, workflow_id TEXT NOT NULL,
 workspace TEXT NOT NULL, session_id TEXT NOT NULL, submission_id TEXT NOT NULL, message TEXT NOT NULL,
 status TEXT NOT NULL, answer TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
 started_at INTEGER NOT NULL DEFAULT 0, UNIQUE(user_id,grant_id,submission_id))`)
	if err != nil {
		s.Close()
		return nil, err
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS external_builder_edits (
 id TEXT PRIMARY KEY, operation_id TEXT NOT NULL, user_id TEXT NOT NULL, grant_id TEXT NOT NULL,
 workflow_id TEXT NOT NULL, workspace TEXT NOT NULL, tool TEXT NOT NULL, path TEXT NOT NULL,
 before_revision TEXT NOT NULL DEFAULT '', after_revision TEXT NOT NULL DEFAULT '',
 before_content TEXT NOT NULL DEFAULT '', after_content TEXT NOT NULL DEFAULT '',
 before_exists INTEGER NOT NULL DEFAULT 0, before_content_available INTEGER NOT NULL DEFAULT 1,
 before_size INTEGER NOT NULL DEFAULT 0, after_size INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL)`)
	if err != nil {
		s.Close()
		return nil, err
	}
	if err = migrateAndPruneExternalBuilderEdits(s.db); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

const externalBuilderColumns = `id,user_id,grant_id,workflow_id,workspace,session_id,submission_id,message,status,answer,error,created_at,started_at`

func scanExternalBuilder(row interface{ Scan(...any) error }) (externalBuilderOperation, error) {
	var op externalBuilderOperation
	var created, started int64
	err := row.Scan(&op.ID, &op.UserID, &op.GrantID, &op.WorkflowID, &op.Workspace, &op.SessionID, &op.SubmissionID, &op.Message, &op.Status, &op.Answer, &op.Error, &created, &started)
	op.CreatedAt = time.Unix(0, created)
	if started != 0 {
		op.StartedAt = time.Unix(0, started)
	}
	return op, err
}
func readExternalBuilder(ctx context.Context, id string) (externalBuilderOperation, error) {
	s, err := openExternalBuilderStore()
	if err != nil {
		return externalBuilderOperation{}, err
	}
	defer s.Close()
	return scanExternalBuilder(s.db.QueryRowContext(ctx, `SELECT `+externalBuilderColumns+` FROM external_builder_operations WHERE id=?`, id))
}
func settleExternalBuilder(id, status, answer, detail string) error {
	s, err := openExternalBuilderStore()
	if err != nil {
		return err
	}
	defer s.Close()
	_, err = s.db.Exec(`UPDATE external_builder_operations SET status=?,answer=?,error=? WHERE id=? AND status IN ('queued','running')`, status, answer, detail, id)
	return err
}

// Resolve both token kinds without ever converting a restricted connection
// into an unrestricted app identity (including when restoring delayed turns).
func activeExternalGrantClaims(ctx context.Context, id string) (*UserClaims, error) {
	if strings.HasPrefix(id, "oauth-") {
		g, err := mcpOAuthFamilyActive(ctx, strings.TrimPrefix(id, "oauth-"))
		if err != nil {
			return nil, err
		}
		return accessTokenClaims(mcpOAuthTokenForGrant(g))
	}
	s, err := openAccessTokens()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	token, err := s.Active(ctx, id, time.Now())
	if err != nil {
		return nil, err
	}
	return accessTokenClaims(token)
}
func validateBuilderGrant(ctx context.Context, claims *UserClaims, workflow, workspace string) (*UserClaims, error) {
	if !externalBuilderEnabled() || claims == nil || claims.AccessToken == nil {
		return nil, errors.New("Builder MCP is not authorized")
	}
	live, err := activeExternalGrantClaims(ctx, claims.AccessToken.ID)
	if err != nil {
		return nil, err
	}
	if live.UserID != claims.UserID || !live.AccessToken.AllowsWorkflow(workflow) || !userAllowedWorkflowID(live, workflow) {
		return nil, errors.New("Builder grant no longer authorizes this workflow")
	}
	level, manifest := workflowAccessForWorkspacePath(ctx, live, workspace)
	if manifest == nil || manifest.ID != workflow || (level != WorkflowAccessOwner && level != WorkflowAccessWrite) {
		return nil, errors.New("workflow write access is required")
	}
	product := "agentworks"
	authorized := live.AccessToken.BuilderAccess()
	if manifest.Kind == "relay" {
		product = "relays"
		authorized = authorized || live.AccessToken.RelayBuilderAccess()
	}
	if !authorized || !userAllowedProduct(live, product) {
		return nil, errors.New("Builder grant does not authorize this product")
	}
	return live, nil
}

// The manifest's connected servers are a browser Builder choice, not authority
// delegated to an external MCP client. Apply this after manifest selection.
func externalBuilderMCPServers(req QueryRequest, selected []string) []string {
	if req.ExternalBuilderOperationID != "" {
		return []string{mcpclient.NoServers}
	}
	return selected
}
func (api *StreamingAPI) validateExternalBuilderTurn(ctx context.Context, id, session, workspace string) (*UserClaims, error) {
	op, err := readExternalBuilder(ctx, id)
	if err != nil {
		return nil, err
	}
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.AccessToken == nil || op.UserID != claims.UserID || op.GrantID != claims.AccessToken.ID || op.SessionID != session || op.Workspace != workspace || (op.Status != "queued" && op.Status != "running") {
		return nil, errors.New("Builder operation binding is invalid or no longer active")
	}
	live, err := validateBuilderGrant(ctx, claims, op.WorkflowID, op.Workspace)
	if err != nil {
		return nil, err
	}
	live.ExternalBuilderOperationID = id
	return live, nil
}

func (api *StreamingAPI) externalBuilderOperationCall(w http.ResponseWriter, r *http.Request, name string, args map[string]interface{}, workflow DiscoveredWorkflow) {
	claims, err := validateBuilderGrant(r.Context(), GetUserFromContext(r.Context()), workflow.Manifest.ID, workflow.WorkspacePath)
	if err != nil {
		externalError(w, 403, "builder_not_authorized", err.Error())
		return
	}
	if name == "builder_chat" {
		api.submitExternalBuilder(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), args, workflow)
		return
	}
	if name == "builder_pulse_chat" || name == "builder_pulse_status" {
		api.externalPulseCall(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), name, args, workflow)
		return
	}
	if name == "builder_file_history" || name == "builder_restore_file" {
		api.externalBuilderFileCall(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), name, args, workflow)
		return
	}
	op, err := readExternalBuilder(r.Context(), externalArg(args, "operation_id"))
	if err != nil || op.UserID != claims.UserID || op.GrantID != claims.AccessToken.ID || op.WorkflowID != workflow.Manifest.ID || op.Workspace != workflow.WorkspacePath {
		externalError(w, 404, "operation_not_found", "Builder operation not found")
		return
	}
	switch name {
	case "builder_cancel":
		active := api.externalBuilderActive(op)
		if active != nil {
			active.Lock()
			defer active.Unlock()
		}
		if err := settleExternalBuilder(op.ID, "canceled", "", "Canceled by caller"); err != nil {
			externalError(w, 503, "storage_unavailable", err.Error())
			return
		}
		if err := api.removeQueuedExternalBuilder(r.Context(), op); err != nil {
			externalError(w, 503, "queue_unavailable", err.Error())
			return
		}
		if active != nil && !active.finished && active.cancel != nil {
			active.cancel()
		}
		op, err = readExternalBuilder(r.Context(), op.ID)
		if err != nil {
			externalError(w, 503, "storage_unavailable", err.Error())
			return
		}
		externalJSON(w, op)
	case "builder_reply_input":
		active := api.externalBuilderActive(op)
		if active == nil {
			externalError(w, 409, "input_not_pending", "This operation is not waiting for input")
			return
		}
		active.Lock()
		defer active.Unlock()
		fresh, readErr := readExternalBuilder(r.Context(), op.ID)
		if readErr != nil || fresh.Status != "running" {
			externalError(w, 409, "input_not_pending", "Operation is no longer active")
			return
		}
		requestID := externalArg(args, "request_id")
		found := false
		if !active.finished && op.Status == "running" {
			for _, input := range externalBuilderPending(op) {
				if input.UniqueID == requestID {
					found = true
				}
			}
		}
		if !found {
			externalError(w, 409, "input_not_pending", "Question does not belong to this active operation")
			return
		}
		if err := virtualtools.GetHumanFeedbackStore().SubmitResponseForOperation(op.SessionID, op.ID, requestID, externalArg(args, "response"), time.Now()); err != nil {
			externalError(w, 409, "input_not_pending", err.Error())
			return
		}
		externalJSON(w, map[string]string{"operation_id": op.ID, "request_id": requestID, "status": "submitted"})
	default:
		// A process restart cannot establish completion of a previously running
		// operation. Never replay an uncertain edit or fabricate success.
		if op.Status == "running" && api.externalBuilderActive(op) == nil {
			_ = settleExternalBuilder(op.ID, "interrupted", "", "Server restarted; inspect the workflow before submitting a new operation")
			op, err = readExternalBuilder(r.Context(), op.ID)
			if err != nil {
				externalError(w, 503, "storage_unavailable", err.Error())
				return
			}
		}
		externalJSON(w, api.externalBuilderStatus(r.Context(), op, externalCrewWait(args)))
	}
}

type externalBuilderStatusView struct {
	externalBuilderOperation
	Pending    []virtualtools.HumanFeedbackRequest `json:"pending_inputs"`
	NeedsInput bool                                `json:"needs_user_input"`
}

// externalBuilderStatus returns the operation with its pending questions,
// waiting up to wait for it to finish or ask something.
func (api *StreamingAPI) externalBuilderStatus(ctx context.Context, op externalBuilderOperation, wait time.Duration) externalBuilderStatusView {
	deadline := time.Now().Add(wait)
	for {
		pending := []virtualtools.HumanFeedbackRequest{}
		if op.Status == "running" && api.externalBuilderActive(op) != nil {
			pending = externalBuilderPending(op)
		}
		view := externalBuilderStatusView{op, pending, len(pending) > 0}
		if view.NeedsInput || (op.Status != "queued" && op.Status != "running") || !time.Now().Before(deadline) {
			return view
		}
		select {
		case <-ctx.Done():
			return view
		case <-time.After(500 * time.Millisecond):
		}
		fresh, err := readExternalBuilder(ctx, op.ID)
		if err != nil {
			return view
		}
		op = fresh
	}
}

func externalBuilderPending(op externalBuilderOperation) []virtualtools.HumanFeedbackRequest {
	rows := []virtualtools.HumanFeedbackRequest{}
	if op.StartedAt.IsZero() {
		return rows
	}
	for _, input := range virtualtools.GetHumanFeedbackStore().PendingForSession(op.SessionID, time.Now()) {
		if input.OperationID == op.ID {
			rows = append(rows, input)
		}
	}
	return rows
}

func (api *StreamingAPI) submitExternalBuilder(w http.ResponseWriter, r *http.Request, args map[string]interface{}, workflow DiscoveredWorkflow) {
	op, ok := api.submitExternalBuilderOperation(w, r, args, workflow)
	if !ok {
		return
	}
	// The submit lock is released by now, so waiting never blocks this
	// person's other submissions.
	if wait := externalCrewWait(args); wait > 0 {
		externalJSON(w, api.externalBuilderStatus(r.Context(), op, wait))
		return
	}
	externalJSON(w, op)
}

// submitExternalBuilderOperation records (or finds the retried) operation and
// queues it. It writes the error response itself and returns false on failure.
func (api *StreamingAPI) submitExternalBuilderOperation(w http.ResponseWriter, r *http.Request, args map[string]interface{}, workflow DiscoveredWorkflow) (externalBuilderOperation, bool) {
	claims := GetUserFromContext(r.Context())
	message := externalArg(args, "message")
	submission := externalArg(args, "submission_id")
	requestedSession := externalArg(args, "session_id")
	if strings.TrimSpace(message) == "" || len(message) > 32000 || strings.TrimSpace(submission) == "" || len(submission) > 128 {
		externalError(w, 400, "invalid_arguments", "message and submission_id are required within their size limits")
		return externalBuilderOperation{}, false
	}
	// Serialize selection and reservation across connections for the same person.
	lock := productConversationRegistryMutex("external-builder:" + claims.UserID + ":" + workflow.WorkspacePath)
	lock.Lock()
	defer lock.Unlock()
	s, err := openExternalBuilderStore()
	if err != nil {
		externalError(w, 503, "storage_unavailable", err.Error())
		return externalBuilderOperation{}, false
	}
	defer s.Close()
	old, err := scanExternalBuilder(s.db.QueryRowContext(r.Context(), `SELECT `+externalBuilderColumns+` FROM external_builder_operations WHERE user_id=? AND grant_id=? AND submission_id=?`, claims.UserID, claims.AccessToken.ID, submission))
	if err == nil {
		if old.Message != message || old.WorkflowID != workflow.Manifest.ID || (requestedSession != "" && old.SessionID != requestedSession) {
			externalError(w, 409, "submission_conflict", "submission_id was already used with another payload")
			return externalBuilderOperation{}, false
		}
		if old.Status == "queued" {
			if err := api.ensureExternalBuilderQueued(r.Context(), claims, old); err != nil {
				externalError(w, 503, "queue_unavailable", err.Error())
				return externalBuilderOperation{}, false
			}
		}
		return old, true
	}
	if !errors.Is(err, sql.ErrNoRows) {
		externalError(w, 503, "storage_unavailable", err.Error())
		return externalBuilderOperation{}, false
	}
	var pending int
	if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM external_builder_operations WHERE user_id=? AND status IN ('queued','running')`, claims.UserID).Scan(&pending); err != nil {
		externalError(w, 503, "storage_unavailable", err.Error())
		return externalBuilderOperation{}, false
	}
	if pending >= 16 {
		externalError(w, 429, "builder_busy", "Finish or cancel pending Builder operations before submitting more")
		return externalBuilderOperation{}, false
	}
	session := requestedSession
	if session != "" {
		if _, _, err = api.externalBuilderSession(r, workflow.WorkspacePath, session); err != nil {
			externalError(w, 404, "session_not_found", "Your workflow chat was not found")
			return externalBuilderOperation{}, false
		}
	} else {
		if live := api.findLiveWorkflowBuilderSession(r.Context(), workflow.Manifest.ID, workflow.WorkspacePath); live != nil {
			session = live.SessionID
		}
		if session == "" {
			restored, restoreErr := api.restoreLatestBuilderConversation(r.Context(), workflow.Manifest.ID, workflow.WorkspacePath)
			if restoreErr != nil {
				externalError(w, 503, "history_unavailable", restoreErr.Error())
				return externalBuilderOperation{}, false
			}
			if restored != nil {
				session = restored.SessionID
			}
		}
		if session != "" {
			if _, _, err = api.externalBuilderSession(r, workflow.WorkspacePath, session); err != nil {
				externalError(w, 409, "session_unavailable", "The current workflow chat cannot be safely resumed")
				return externalBuilderOperation{}, false
			}
		}
		// Before the queued first turn materializes history, a second connection
		// must use that same conversation rather than create a duplicate.
		if session == "" {
			_ = s.db.QueryRowContext(r.Context(), `SELECT session_id FROM external_builder_operations WHERE user_id=? AND workspace=? AND status IN ('queued','running') ORDER BY created_at DESC LIMIT 1`, claims.UserID, workflow.WorkspacePath).Scan(&session)
		}
		if session == "" {
			session = uuid.NewString()
		}
	}
	op := externalBuilderOperation{ID: uuid.NewString(), UserID: claims.UserID, GrantID: claims.AccessToken.ID, WorkflowID: workflow.Manifest.ID, Workspace: workflow.WorkspacePath, SessionID: session, SubmissionID: submission, Message: message, Status: "queued", CreatedAt: time.Now()}
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO external_builder_operations (id,user_id,grant_id,workflow_id,workspace,session_id,submission_id,message,status,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, op.ID, op.UserID, op.GrantID, op.WorkflowID, op.Workspace, op.SessionID, op.SubmissionID, op.Message, op.Status, op.CreatedAt.UnixNano())
	if err != nil {
		externalError(w, 503, "storage_unavailable", err.Error())
		return externalBuilderOperation{}, false
	}
	if err := api.ensureExternalBuilderQueued(r.Context(), claims, op); err != nil {
		externalError(w, 503, "queue_unavailable", err.Error())
		return externalBuilderOperation{}, false
	}
	return op, true
}

func (api *StreamingAPI) runQueuedExternalBuilder(ctx context.Context, turn queuedConversationTurn, reqMap map[string]interface{}) (internalSessionTurnResult, error) {
	id := turn.Principal.ExternalBuilderOperationID
	op, err := readExternalBuilder(ctx, id)
	if err != nil {
		return internalSessionTurnResult{}, err
	}
	if op.Message != turn.Request.Query || op.UserID != turn.UserID || op.Workspace != turn.Request.SelectedFolder || op.WorkflowID != turn.Request.PresetQueryID || op.SessionID != turn.SessionID {
		return internalSessionTurnResult{}, errors.New("queued Builder payload does not match its trusted operation")
	}
	claims, err := api.validateExternalBuilderTurn(ctx, id, op.SessionID, op.Workspace)
	if err != nil {
		_ = settleExternalBuilder(id, "failed", "", err.Error())
		return internalSessionTurnResult{}, err
	}
	active := &externalBuilderActive{id: id}
	api.externalBuilderRuntime.Lock()
	if api.externalBuilderRuntime.sessions == nil {
		api.externalBuilderRuntime.sessions = map[string]*externalBuilderActive{}
	}
	if api.externalBuilderRuntime.sessions[op.SessionID] != nil {
		api.externalBuilderRuntime.Unlock()
		return internalSessionTurnResult{}, errors.New("Builder conversation is occupied")
	}
	api.externalBuilderRuntime.sessions[op.SessionID] = active
	api.externalBuilderRuntime.Unlock()
	defer func() {
		api.externalBuilderRuntime.Lock()
		delete(api.externalBuilderRuntime.sessions, op.SessionID)
		api.externalBuilderRuntime.Unlock()
	}()
	s, err := openExternalBuilderStore()
	if err != nil {
		return internalSessionTurnResult{}, err
	}
	started := time.Now()
	result, err := s.db.ExecContext(ctx, `UPDATE external_builder_operations SET status='running',started_at=? WHERE id=? AND status='queued'`, started.UnixNano(), id)
	s.Close()
	if err != nil {
		return internalSessionTurnResult{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		_ = settleExternalBuilder(id, "interrupted", "", "An earlier attempt may have executed; automatic replay refused")
		return internalSessionTurnResult{}, errors.New("Builder operation is no longer queued")
	}
	ctx, cancel := context.WithCancel(context.WithValue(ctx, UserContextKey, claims))
	defer cancel()
	active.Lock()
	active.cancel = func() {
		api.interruptWorkflowPolicySession(op.SessionID, "")
		virtualtools.GetHumanFeedbackStore().WithdrawOperation(op.ID)
		cancel()
	}
	active.Unlock()
	stopped := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-ticker.C:
				check, c := context.WithTimeout(context.Background(), 4*time.Second)
				_, e := api.validateExternalBuilderTurn(context.WithValue(check, UserContextKey, claims), id, op.SessionID, op.Workspace)
				c()
				if e != nil {
					active.Lock()
					if !active.finished {
						_ = settleExternalBuilder(id, "canceled", "", e.Error())
						active.cancel()
					}
					active.Unlock()
					return
				}
			}
		}
	}()
	// Queue documents are editable workspace state. Reconstruct the request
	// from the trusted operation instead of trusting queued model/policy fields.
	reqMap, err = queryRequestToMap(externalBuilderQuery(op))
	if err != nil {
		active.Lock()
		active.finished = true
		close(stopped)
		active.Unlock()
		return internalSessionTurnResult{}, err
	}
	// The queue owns the conversation until this exact execution tree finishes.
	var outcome internalSessionTurnResult
	if _, err = api.validateExternalBuilderTurn(ctx, id, op.SessionID, op.Workspace); err != nil {
	} else if api.internalExternalBuilderTurn != nil {
		outcome, err = api.internalExternalBuilderTurn(ctx, reqMap, op.SessionID, op.UserID)
	} else {
		outcome, err = api.startSessionInternalWithResult(ctx, reqMap, op.SessionID, op.UserID, nil)
	}
	active.Lock()
	active.finished = true
	close(stopped)
	status, detail := "completed", ""
	if err != nil {
		status = "failed"
		detail = err.Error()
	}
	saveErr := settleExternalBuilder(id, status, truncateTriggerTargetResult(outcome.FinalResponse), detail)
	active.Unlock()
	if err == nil {
		err = saveErr
	}
	return outcome, err
}

// Managed authoring has no shell. Keep one tool list for both permission checks
// and the direct coding-agent bridge so registered tools stay callable.
var externalBuilderManagedTools = []string{
	"list_dashboards", "get_dashboard", "create_dashboard", "update_dashboard", "validate_dashboard", "preview_dashboard", "publish_dashboard", "restore_dashboard", "get_dashboard_link",
	"read_file", "write_file", "list_files", "search_files",
	"add_step", "manage_group", "manage_step_route", "change_step_type", "maintain_plan", "create_plan", "delete_plan_steps", "get_step_prompts", "update_step", "update_step_config", "update_validation_schema", "update_variable", "validate_plan_change", "get_plan_prompt_health", "get_contract_upgrades", "get_llm_config", "get_workflow_command_guidance", "human_feedback", "get_file_link", "get_report_link",
}

var externalBuilderKnowledgeTools = knowledgebase.ToolNames()

func externalBuilderToolDenied(claims *UserClaims, name string) bool {
	if claims == nil || claims.ExternalBuilderOperationID == "" {
		return false
	}
	name = knowledgebase.CanonicalToolName(name)
	if slices.Contains(externalBuilderKnowledgeTools, name) {
		return claims.AccessToken == nil || !claims.AccessToken.Allows("knowledgebase:read")
	}
	return !slices.Contains(externalBuilderManagedTools, name)
}

// API models receive managed tools directly. Coding CLIs receive the same
// tools through their direct MCP bridge, without needing an HTTP shell call.
func externalBuilderTransport(claims *UserClaims) (codeExecution bool, directBridge []string) {
	if claims == nil || claims.ExternalBuilderOperationID == "" {
		return true, nil
	}
	for _, name := range append(slices.Clone(externalBuilderManagedTools), externalBuilderKnowledgeTools...) {
		if !externalBuilderToolDenied(claims, name) {
			directBridge = append(directBridge, name)
		}
	}
	return false, directBridge
}

func externalBuilderQuery(op externalBuilderOperation) QueryRequest {
	return QueryRequest{Query: op.Message, AgentMode: "workflow_phase", PhaseID: "workflow-builder", PresetQueryID: op.WorkflowID, SelectedFolder: op.Workspace, RestoredConversationSessionID: op.SessionID, TriggeredBy: "external", DisableLiveInputDelivery: true}
}

func (api *StreamingAPI) removeQueuedExternalBuilder(ctx context.Context, op externalBuilderOperation) error {
	lock := productConversationRegistryMutex(conversationTurnQueuePath(op.UserID))
	lock.Lock()
	defer lock.Unlock()
	turns, err := api.readConversationTurnQueue(ctx, op.UserID)
	if err != nil {
		return err
	}
	kept := turns[:0]
	for _, turn := range turns {
		if turn.Principal.ExternalBuilderOperationID != op.ID || turn.StartedAt != nil {
			kept = append(kept, turn)
		}
	}
	return api.writeConversationTurnQueue(ctx, op.UserID, kept)
}

// Repairs a crash between durable reservation and queue insertion. A retry of
// an accepted/uncertain submission can enqueue only work proven not started.
func (api *StreamingAPI) ensureExternalBuilderQueued(ctx context.Context, claims *UserClaims, op externalBuilderOperation) error {
	turns, err := api.readConversationTurnQueue(ctx, op.UserID)
	if err != nil {
		return err
	}
	for _, turn := range turns {
		if turn.Principal.ExternalBuilderOperationID == op.ID {
			api.kickConversationTurnQueue(op.SessionID)
			return nil
		}
	}
	current, err := readExternalBuilder(ctx, op.ID)
	if err != nil {
		return err
	}
	if current.Status != "queued" {
		return nil
	}
	copy := *claims
	copy.ExternalBuilderOperationID = op.ID
	ctx = context.WithValue(ctx, UserContextKey, &copy)
	ctx = context.WithValue(ctx, chatSubmissionContextKey{}, chatSubmissionContext{ID: op.ID, Owner: op.UserID, Session: op.SessionID, Message: op.Message})
	turn, _, err := api.enqueueConversationTurn(ctx, op.UserID, op.SessionID, externalBuilderQuery(op))
	if err != nil {
		return err
	}
	api.recordQueuedConversationUserMessage(op.SessionID, turn)
	api.kickConversationTurnQueue(op.SessionID)
	return nil
}
