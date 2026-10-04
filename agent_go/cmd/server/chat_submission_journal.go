package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// The journal is durable acceptance, not a native-CLI exactly-once claim. An
// interrupted dispatch remains uncertain unless the closed provider's final
// native transcript proves it ended before the submission existed.
// Workspace PUT and the process-local lock require a single writer per owner;
// multiple active server replicas need storage-backed compare-and-swap.
type chatSubmissionRecord struct {
	ID         string `json:"submission_id"`
	Owner      string `json:"owner"`
	Project    string `json:"project"`
	Session    string `json:"session_id"`
	Message    string `json:"message"`
	State      string `json:"state"`
	UpdatedAt  string `json:"updated_at"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Response   string `json:"response,omitempty"`
}

type chatSubmissionStore struct {
	resolveProject func(owner, session string) (string, error)
	read           func(context.Context, string) (string, bool, error)
	write          func(context.Context, string, string) error
}
type chatSubmissionContextKey struct{}
type chatSubmissionContext struct {
	ID, Owner, Session, Message string
	// handle lets a dispatch that outlives the HTTP response (live input to a
	// busy CLI) record its real outcome after the early "accepted" reply.
	handle *chatSubmissionHandle
}

// chatSubmissionHandle serializes the request's own journal write (finish)
// with a later background resolution. Once resolved late, finish no longer
// overwrites the record with the early reply.
type chatSubmissionHandle struct {
	mu      sync.Mutex
	late    bool
	persist func(status int, body []byte)
}

// resolveLate records the dispatch's real outcome after the response was sent.
func (h *chatSubmissionHandle) resolveLate(status int, body []byte) {
	if h == nil || h.persist == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.late = true
	h.persist(status, body)
}

// submissionStateForResponse maps a dispatch response to its journal state.
func submissionStateForResponse(status int, body []byte) string {
	state := "accepted"
	if status >= 400 {
		state = "rejected"
	}
	var response map[string]interface{}
	if json.Unmarshal(body, &response) == nil {
		if response["delivery_status"] == "sent_to_cli" {
			state = "delivery_confirmed"
		}
		if response["delivery_status"] == "delivery_uncertain" {
			state = "delivery_uncertain"
		}
	}
	return state
}

type chatSubmissionUnknownProjectKey struct{}

const queuedChatSubmissionDedupeWindow = 2 * time.Minute

const (
	// uncertainSubmissionAdoptTimeout bounds how long a same-key retry waits
	// for the in-flight dispatch to resolve its journal record before the
	// retry is answered 409. finish() resolves the record when the first
	// attempt's HTTP handling completes, so ordinary races (double submit,
	// impatience retry, timeout redelivery) adopt within seconds; only a
	// genuinely wedged dispatch still surfaces delivery_uncertain.
	uncertainSubmissionAdoptTimeout = 30 * time.Second
	// uncertainSubmissionAdoptPollInterval spaces journal re-reads while a
	// retry waits. Reads are small single-file fetches.
	uncertainSubmissionAdoptPollInterval = 500 * time.Millisecond
)

func submissionDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizeQueuedChatSubmission(message string) string {
	message = strings.TrimSpace(message)
	if !strings.HasPrefix(message, "#") {
		return message
	}
	colon := strings.IndexByte(message, ':')
	if colon < 2 {
		return message
	}
	for _, r := range message[1:colon] {
		if r < '0' || r > '9' {
			return message
		}
	}
	return strings.TrimSpace(message[colon+1:])
}

// beginChatSubmission must run after ownership authorization and before any
// delivery. Its finish callback saves the result before exposing success.
func (api *StreamingAPI) beginChatSubmission(w http.ResponseWriter, r *http.Request, session, project, message string) (http.ResponseWriter, *http.Request, func(), bool) {
	owner := strings.TrimSpace(GetUserIDFromContext(r.Context()))
	if accepted, ok := r.Context().Value(chatSubmissionContextKey{}).(chatSubmissionContext); ok && accepted.Owner == owner && accepted.Session == session && accepted.Message == message {
		return w, r, func() {}, true
	}
	if owner == "" || strings.TrimSpace(session) == "" {
		http.Error(w, "Submission owner and session are required", http.StatusUnauthorized)
		return w, r, func() {}, false
	}
	id := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if id == "" {
		id = uuid.NewString()
	}
	if len(id) > 256 {
		http.Error(w, "Idempotency-Key exceeds 256 characters", http.StatusBadRequest)
		return w, r, func() {}, false
	}
	w.Header().Set("X-Submission-ID", id)
	r.Header.Set("Idempotency-Key", id)
	queuedDelivery := strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Queued-Chat-Delivery")), "true")
	journalIdentity := id
	if queuedDelivery {
		// Queue workers in separate browser tabs cannot share an in-memory lock
		// or random idempotency key. Give the same owner/session/message one
		// durable receipt for a bounded window. The queue batch transport may
		// prefix a single message with "#1:"; that is presentation, not identity.
		journalIdentity = "queued:" + session + ":" + normalizeQueuedChatSubmission(message)
	}
	path := filepath.ToSlash(filepath.Join(chatHistoryRoot(owner), "submissions", submissionDigest(journalIdentity)+".json"))
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	store := chatSubmissionStore{read: readFileFromWorkspace, write: writeRawFileToWorkspace, resolveProject: durableSubmissionProject}
	if api.internalChatSubmissionStore != nil {
		store = *api.internalChatSubmissionStore
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
	raw, exists, err := store.read(ctx, path)
	cancel()
	if err != nil {
		lock.Unlock()
		http.Error(w, "Cannot read durable submission journal", http.StatusServiceUnavailable)
		return w, r, func() {}, false
	}
	record := chatSubmissionRecord{ID: id, Owner: owner, Project: project, Session: session, Message: message, State: "delivery_uncertain"}
	if exists {
		var previous chatSubmissionRecord
		if json.Unmarshal([]byte(raw), &previous) != nil {
			lock.Unlock()
			http.Error(w, "Unreadable durable submission record", http.StatusServiceUnavailable)
			return w, r, func() {}, false
		}
		// A cold live-input retry has no project selection of its own. The
		// owner/session-bound receipt is its durable project identity.
		if r.Context().Value(chatSubmissionUnknownProjectKey{}) == true {
			project = previous.Project
		}
		sameMessage := previous.Message == message
		if queuedDelivery {
			sameMessage = normalizeQueuedChatSubmission(previous.Message) == normalizeQueuedChatSubmission(message)
		}
		projectMatches := previous.Project == project
		// Older /query receipts omitted the preset-resolved workflow folder.
		// Prove that legacy binding from the same owner's durable conversation;
		// never let a key move to a different explicit project or session.
		if !projectMatches && previous.Owner == owner && previous.Session == session && sameMessage && previous.Project == "" && strings.HasPrefix(project, "Workflow/") {
			resolver := store.resolveProject
			if resolver == nil {
				resolver = durableSubmissionProject
			}
			verified, resolveErr := resolver(owner, session)
			projectMatches = resolveErr == nil && verified == project
		}
		if previous.Owner != owner || !projectMatches || previous.Session != session || !sameMessage {
			lock.Unlock()
			http.Error(w, "Idempotency-Key already belongs to another submission", http.StatusConflict)
			return w, r, func() {}, false
		}
		// A queue action is semantically idempotent only around its delivery
		// race. Permit a deliberate identical action after the bounded window,
		// while never overwriting an uncertain dispatch.
		if queuedDelivery && previous.State != "delivery_uncertain" {
			if updatedAt, parseErr := time.Parse(time.RFC3339Nano, previous.UpdatedAt); parseErr == nil && time.Since(updatedAt) > queuedChatSubmissionDedupeWindow {
				exists = false
			}
		}
		if exists && previous.State == "delivery_uncertain" {
			canRetry := false
			if api.internalUncertainSubmissionRetryChecker != nil {
				canRetry = api.internalUncertainSubmissionRetryChecker(r.Context(), previous)
			} else {
				canRetry = api.canRetryUncertainChatSubmission(r.Context(), previous)
			}
			if canRetry {
				exists = false
				log.Printf("[CHAT_SUBMISSION] Reconciled submission %s as not delivered after its retained tmux closed; retrying through a resumed turn", previous.ID)
			}
		}
		if exists && previous.State == "rejected" && storedSubmissionWasTurnRunning(previous) {
			// turn_running proves the message was never sent or queued, so a
			// retry (same key, or the same queued-delivery identity) safely
			// dispatches instead of replaying the rejection forever. All
			// other rejections keep their replay semantics.
			exists = false
			log.Printf("[CHAT_SUBMISSION] Re-dispatching submission %s after turn_running rejection", previous.ID)
		}
		if !exists {
			// Keep the path lock and replace the expired semantic receipt below.
		} else {
			lock.Unlock()
			if previous.State == "delivery_uncertain" {
				if adopted := api.awaitUncertainChatSubmission(r.Context(), store, path); adopted != nil {
					log.Printf("[CHAT_SUBMISSION] Adopted resolved submission %s for same-key retry", adopted.ID)
					replayChatSubmissionOutcome(w, *adopted)
					return w, r, func() {}, false
				}
				writeSubmissionUncertain(w, id)
				return w, r, func() {}, false
			}
			replayChatSubmissionOutcome(w, previous)
			return w, r, func() {}, false
		}
	}
	if r.Context().Value(chatSubmissionUnknownProjectKey{}) == true {
		resolver := store.resolveProject
		if resolver == nil {
			resolver = durableSubmissionProject
		}
		project, err = resolver(owner, session)
		if err != nil {
			lock.Unlock()
			http.Error(w, "Cannot resolve durable conversation project; restore the conversation before sending", http.StatusConflict)
			return w, r, func() {}, false
		}
		record.Project = project
		// Rehydrate the verified binding so the subsequent transcript append
		// uses the same durable workflow after this server restart.
		api.sessionWorkspaceMu.Lock()
		if api.sessionWorkspaceFolders == nil {
			api.sessionWorkspaceFolders = map[string]string{}
		}
		api.sessionWorkspaceFolders[session] = project
		api.sessionWorkspaceMu.Unlock()
	}
	save := func() error {
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		data, err := json.Marshal(record)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
		defer cancel()
		return store.write(ctx, path, string(data))
	}
	if err := save(); err != nil {
		lock.Unlock()
		http.Error(w, "Cannot durably accept submission; nothing was sent", http.StatusServiceUnavailable)
		return w, r, func() {}, false
	}
	// Release before dispatch: concurrent retries see uncertain instead of waiting
	// for a provider or risking a second send.
	lock.Unlock()
	capture := &internalResponseCapture{header: w.Header().Clone()}
	handle := &chatSubmissionHandle{}
	handle.persist = func(status int, body []byte) {
		record.HTTPStatus = status
		record.Response = string(body)
		record.State = submissionStateForResponse(status, body)
		if err := save(); err != nil {
			log.Printf("[CHAT_SUBMISSION] Could not record late outcome for submission %s: %v", id, err)
		}
	}
	finish := func() {
		// A response buffered before a panic is not a dispatch acknowledgement.
		// Keep the durable pre-dispatch uncertainty, then preserve HTTP recovery.
		if panicValue := recover(); panicValue != nil {
			panic(panicValue)
		}
		status := capture.status
		if status == 0 {
			status = http.StatusOK
		}
		handle.mu.Lock()
		if !handle.late {
			record.HTTPStatus = status
			record.Response = capture.body.String()
			record.State = submissionStateForResponse(status, capture.body.Bytes())
			if record.Response == "" {
				handle.mu.Unlock()
				writeSubmissionUncertain(w, id)
				return
			}
			if err := save(); err != nil {
				handle.mu.Unlock()
				writeSubmissionUncertain(w, id)
				return
			}
		}
		handle.mu.Unlock()
		for key, values := range capture.header {
			w.Header()[key] = values
		}
		w.WriteHeader(status)
		_, _ = w.Write(capture.body.Bytes())
	}
	r = r.WithContext(context.WithValue(r.Context(), chatSubmissionContextKey{}, chatSubmissionContext{ID: id, Owner: owner, Session: session, Message: message, handle: handle}))
	return capture, r, finish, true
}

// awaitUncertainChatSubmission waits for an in-flight dispatch to resolve the
// journal record at path, returning the resolved record so a same-key retry
// transparently adopts the first attempt's outcome instead of surfacing 409.
// It returns nil when the record stays uncertain past the bounded wait, the
// request ends, or the journal cannot be read: genuinely stuck dispatches keep
// the 409 so a duplicate is never sent blind. Callers must not hold the path
// lock: finish() saves without it, and holding it would serialize every
// same-key retry behind the full wait.
func (api *StreamingAPI) awaitUncertainChatSubmission(ctx context.Context, store chatSubmissionStore, path string) *chatSubmissionRecord {
	timeout := uncertainSubmissionAdoptTimeout
	interval := uncertainSubmissionAdoptPollInterval
	if api != nil {
		if api.internalUncertainSubmissionAdoptTimeout > 0 {
			timeout = api.internalUncertainSubmissionAdoptTimeout
		}
		if api.internalUncertainSubmissionAdoptPollInterval > 0 {
			interval = api.internalUncertainSubmissionAdoptPollInterval
		}
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		// A read failure is not a resolution. Workspace reads can flap;
		// torn output keeps the retry waiting instead of 409ing early.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		raw, exists, err := store.read(readCtx, path)
		cancel()
		if err == nil && exists {
			var current chatSubmissionRecord
			if json.Unmarshal([]byte(raw), &current) == nil && current.State != "" && current.State != "delivery_uncertain" {
				return &current
			}
		}
		if time.Now().After(deadline) {
			return nil
		}
	}
}

// storedSubmissionWasTurnRunning reports whether a journaled rejection is the
// input-lane turn_running backstop, which proves the message was never sent
// or queued and is therefore safe to dispatch again.
func storedSubmissionWasTurnRunning(record chatSubmissionRecord) bool {
	if strings.TrimSpace(record.Response) == "" {
		return false
	}
	var response map[string]interface{}
	if json.Unmarshal([]byte(record.Response), &response) != nil {
		return false
	}
	errorValue, _ := response["error"].(string)
	return errorValue == "turn_running"
}

// replayChatSubmissionOutcome serves a previously stored submission outcome so
// a retry adopts the first attempt's result instead of dispatching again.
func replayChatSubmissionOutcome(w http.ResponseWriter, record chatSubmissionRecord) {
	if record.HTTPStatus < 100 || record.HTTPStatus > 599 || record.Response == "" {
		http.Error(w, "Incomplete durable submission outcome", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(record.HTTPStatus)
	_, _ = w.Write([]byte(record.Response))
}

// canRetryUncertainChatSubmission proves that an ambiguous receipt was not
// delivered by consulting the final native transcript of a now-closed tmux
// session. The proof is valid whether the transcript ended before the receipt
// or continued beyond it: once the session is closed, absence of the exact
// human prompt means the provider never accepted that submission. Missing
// transcript evidence, an active turn, or a live tmux remains uncertain.
func (api *StreamingAPI) canRetryUncertainChatSubmission(ctx context.Context, record chatSubmissionRecord) bool {
	if api == nil || strings.TrimSpace(record.Session) == "" || strings.TrimSpace(record.Message) == "" {
		return false
	}
	recordTime, err := time.Parse(time.RFC3339Nano, record.UpdatedAt)
	if err != nil {
		return false
	}
	// A live terminal, a running turn or a retained session may be the very CLI the submission went to, so they keep it uncertain. But a live terminal that
	// started AFTER the submission cannot hold it (any CLI: the provider was switched, or the old CLI closed and a new one launched): it and its turns say nothing
	// about this submission. Without this an old uncertain message of a chat that is in use again was never reconciled: the browser re-sent it, waited 30 s and got
	// 409 every time, although the chat kept working (Code on Excellence, 2026-10-04). The transcript check below stays the proof of non-delivery.
	if snapshot, live := api.liveMainCodingTmuxSnapshot(record.Session); live {
		if snapshot.CreatedAt.IsZero() || !snapshot.CreatedAt.After(recordTime) {
			return false
		}
	} else {
		if api.hasActiveTurnCancel(record.Session) {
			return false
		}
		if retainedSession, ok := mcpagent.LookupSession(record.Session); ok && retainedSession.ActiveTurnID() != "" {
			return false
		}
	}

	raw, err := ReadChatHistoryConversation(record.Owner, record.Session, record.Project)
	if err != nil || len(raw) == 0 {
		return false
	}
	var conversation map[string]interface{}
	if json.Unmarshal(raw, &conversation) != nil {
		return false
	}
	runtimeRaw, err := json.Marshal(conversation["runtime"])
	if err != nil {
		return false
	}
	var runtime claudeNativeTranscriptRuntime
	if json.Unmarshal(runtimeRaw, &runtime) != nil {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(runtime.Provider))
	nativeSessionID := strings.TrimSpace(runtime.ExternalSessionID)
	workingDir := ""
	accountHome := ""
	if runtime.AgentSessionHandle != nil && runtime.AgentSessionHandle.Provider != nil {
		if provider == "" {
			provider = strings.ToLower(strings.TrimSpace(runtime.AgentSessionHandle.Provider.Provider))
		}
		if nativeSessionID == "" {
			nativeSessionID = strings.TrimSpace(runtime.AgentSessionHandle.Provider.NativeSessionID)
		}
		workingDir = strings.TrimSpace(runtime.AgentSessionHandle.Provider.WorkingDir)
		if runtime.AgentSessionHandle.ConnectionID != "" {
			keys, keyErr := api.connectionAPIKeys(ctx, providerAccountScope{Principal: record.Owner, WorkspacePath: record.Project}, provider, runtime.AgentSessionHandle.ConnectionID)
			if keyErr != nil {
				return false
			}
			accountHome = keys.RuntimeEnvironment["HOME"]
		}
	}
	messages, maxTimestamp, _, ok, err := nativeTranscriptMessagesForRuntime(provider, nativeSessionID, workingDir, accountHome)
	if err != nil || !ok || maxTimestamp.IsZero() {
		return false
	}
	want := strings.TrimSpace(record.Message)
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "human") && strings.TrimSpace(builderConversationMessageText(message)) == want {
			return false
		}
	}
	return true
}

func writeSubmissionUncertain(w http.ResponseWriter, id string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "delivery_uncertain", "delivery_status": "delivery_uncertain", "submission_id": id, "message": fmt.Sprintf("Submission %s is durable but delivery is uncertain. Reconcile this submission before sending it again.", id)})
}

// ListUnresolvedChatSubmissions exposes durable accepted input for recovery.
// These records are evidence to reconcile, never instructions to resend. CLI
// transport acknowledgement is not proof that an assistant answer was saved.
func ListUnresolvedChatSubmissions(ctx context.Context, owner, session string) ([]chatSubmissionRecord, error) {
	if strings.TrimSpace(owner) == "" {
		return nil, fmt.Errorf("submission owner required")
	}
	root := filepath.ToSlash(filepath.Join(chatHistoryRoot(owner), "submissions"))
	listing, exists, err := listWorkspaceFolder(ctx, root, 1)
	if err != nil || !exists {
		return nil, err
	}
	paths := []string{}
	collectWorkspaceFilePaths(listing, &paths)
	result := []chatSubmissionRecord{}
	for _, path := range paths {
		if !strings.HasSuffix(path, ".json") {
			continue
		}
		raw, exists, err := readFileFromWorkspace(ctx, path)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		var record chatSubmissionRecord
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, err
		}
		if record.Owner == owner && (session == "" || record.Session == session) && (record.State == "accepted" || record.State == "delivery_uncertain") {
			result = append(result, record)
		}
	}
	return result, nil
}

// Submission lookup is scoped to the authenticated owner, regardless of the
// supplied key. It works even after the in-memory terminal registry was lost.
func (api *StreamingAPI) handleChatSubmissionStatus(w http.ResponseWriter, r *http.Request) {
	owner := strings.TrimSpace(GetUserIDFromContext(r.Context()))
	if owner == "" {
		http.Error(w, "Submission owner required", http.StatusUnauthorized)
		return
	}
	id := strings.TrimSpace(r.PathValue("submission_id"))
	if id == "" {
		id = strings.TrimSpace(mux.Vars(r)["submission_id"])
	}
	if id == "" || len(id) > 256 {
		http.Error(w, "Invalid submission ID", http.StatusBadRequest)
		return
	}
	path := filepath.ToSlash(filepath.Join(chatHistoryRoot(owner), "submissions", submissionDigest(id)+".json"))
	raw, exists, err := readFileFromWorkspace(r.Context(), path)
	if err != nil {
		http.Error(w, "Cannot read submission", http.StatusServiceUnavailable)
		return
	}
	if !exists {
		http.Error(w, "Submission not found", http.StatusNotFound)
		return
	}
	var record chatSubmissionRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		http.Error(w, "Unreadable submission", http.StatusServiceUnavailable)
		return
	}
	if record.Owner != owner {
		http.Error(w, "Submission not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(record)
}

func durableSubmissionProject(owner, session string) (string, error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(session) == "" {
		return "", fmt.Errorf("conversation owner and session required")
	}
	rawCentral, centralErr := ReadChatHistoryConversation(owner, session, "")
	centralRuntimeFound := false
	if centralErr == nil && len(rawCentral) > 0 {
		var central struct {
			UserID    string                   `json:"user_id"`
			SessionID string                   `json:"session_id"`
			Runtime   *ChatHistoryAgentRuntime `json:"runtime"`
		}
		if json.Unmarshal(rawCentral, &central) != nil {
			return "", fmt.Errorf("unreadable central conversation identity")
		}
		centralOwner := strings.TrimSpace(central.UserID)
		if centralOwner == "" {
			centralOwner = "default"
		}
		if centralOwner != owner || central.SessionID != session {
			return "", fmt.Errorf("central conversation identity mismatch")
		}
		centralRuntimeFound = central.Runtime != nil
		if centralRuntimeFound && strings.TrimSpace(central.Runtime.WorkspacePath) != "" {
			return canonicalChatHistoryWorkspacePath(owner, central.Runtime.WorkspacePath), nil
		}
	}

	// A workflow's builder transcript is deliberately stored beside the workflow,
	// not in the user's global chat_history index. Discover those durable folders
	// when the process-local binding disappeared; never guess from a session name.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	roots := []string{"Workflow"}
	for _, product := range projectProducts {
		roots = append(roots, filepath.ToSlash(filepath.Join("_users", sanitizeUserIDForPath(owner), product.ProjectsRoot)))
	}
	projects := map[string]bool{}
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		folders, err := submissionProjectFolders(ctx, root)
		if err != nil {
			return "", err
		}
		for _, folder := range folders {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			candidates, err := submissionProjectConversationFiles(ctx, folder)
			if err != nil {
				return "", err
			}
			for _, candidate := range candidates {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				if filepath.Base(candidate) != chatHistoryConversationFileName(session) {
					continue
				}
				candidate = filepath.ToSlash(filepath.Clean(candidate))
				if !strings.HasPrefix(candidate, folder+"/builder/") {
					continue
				}
				// User-partitioned workflow paths must agree with the record owner too.
				marker := "/builder/conversation/users/"
				if start := strings.Index(candidate, marker); start >= 0 {
					partition := strings.SplitN(candidate[start+len(marker):], "/", 2)[0]
					if partition != sanitizeUserIDForPath(owner) {
						continue
					}
				}
				var raw string
				if local, ok := resolveLocalWorkflowDir(folder); ok {
					rel := strings.TrimPrefix(candidate, folder+"/")
					bytes, err := os.ReadFile(filepath.Join(local, filepath.FromSlash(rel)))
					if err != nil {
						return "", err
					}
					raw = string(bytes)
				} else {
					var found bool
					raw, found, err = readFileFromWorkspace(ctx, candidate)
					if err != nil {
						return "", err
					}
					if !found {
						continue
					}
				}
				var record struct {
					UserID    string                   `json:"user_id"`
					SessionID string                   `json:"session_id"`
					Runtime   *ChatHistoryAgentRuntime `json:"runtime"`
				}
				if json.Unmarshal([]byte(raw), &record) != nil {
					return "", fmt.Errorf("unreadable conversation identity")
				}
				recordOwner := strings.TrimSpace(record.UserID)
				if recordOwner == "" {
					recordOwner = "default"
				}
				if recordOwner != owner || record.SessionID != session {
					continue
				}

				project := canonicalChatHistoryWorkspacePath(owner, folder)
				if record.Runtime != nil && strings.TrimSpace(record.Runtime.WorkspacePath) != "" && canonicalChatHistoryWorkspacePath(owner, record.Runtime.WorkspacePath) != project {
					return "", fmt.Errorf("conversation project identity mismatch")
				}
				projects[project] = true
			}
		}
	}
	if len(projects) > 1 {
		return "", fmt.Errorf("conversation session belongs to multiple projects")
	}
	for project := range projects {
		return project, nil
	}
	if centralRuntimeFound {
		return "", nil
	}
	if centralErr != nil {
		return "", centralErr
	}
	return "", fmt.Errorf("conversation runtime unavailable")
}

func submissionProjectFolders(ctx context.Context, root string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	folders := []string{}
	if local, ok := resolveLocalWorkflowDir(root); ok {
		entries, err := os.ReadDir(local)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if entry.IsDir() {
				folders = append(folders, root+"/"+entry.Name())
			}
		}
		return folders, nil
	}
	listing, exists, err := listWorkspaceFolder(ctx, root, 1)
	if err != nil || !exists {
		return nil, err
	}
	// API listings may wrap children in the root or return them directly.
	var collect func([]virtualtools.WorkspaceFolderItem)
	collect = func(items []virtualtools.WorkspaceFolderItem) {
		for _, item := range items {
			path := strings.Trim(item.FilePath, "/")
			if item.Type == "folder" && strings.HasPrefix(path, root+"/") && !strings.Contains(strings.TrimPrefix(path, root+"/"), "/") {
				folders = append(folders, path)
			}
			if path == root {
				collect(item.Children)
			}
		}
	}
	collect(listing)
	return folders, nil
}

func submissionProjectConversationFiles(ctx context.Context, folder string) ([]string, error) {
	if local, ok := resolveLocalWorkflowDir(folder); ok {
		paths, err := filepath.Glob(filepath.Join(local, "builder", "session-*-conversation.json"))
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(filepath.Join(local, "builder", "conversation"), func(path string, entry os.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), "session-") && strings.HasSuffix(entry.Name(), "-conversation.json") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		for i, path := range paths {
			paths[i] = workflowRelativeConversationPath(folder, local, path)
		}
		return paths, nil
	}
	listing, exists, err := listWorkspaceFolder(ctx, folder+"/builder", 8)
	if err != nil || !exists {
		return nil, err
	}
	paths := []string{}
	collectWorkspaceFilePaths(listing, &paths)
	return paths, nil
}
